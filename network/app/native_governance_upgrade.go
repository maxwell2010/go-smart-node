package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"cosmossdk.io/collections"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	sdkgov "github.com/cosmos/cosmos-sdk/x/gov"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

const (
	nativeUpgradeNamespace = "mintcandy-sdk-upgrade/"
	nativeUpgradeSchema    = nativeUpgradeNamespace + "v1"
	nativeSDKMigrationPlan = "mintcandy-native-sdk-migrations-v1"
)

type nativeUpgradeManifest struct {
	Name   string `json:"name"`
	Height int64  `json:"height"`
}

type nativeUpgradeApproval struct {
	Schema     string `json:"schema"`
	ProposalID uint64 `json:"proposal_id"`
}

func (app *App) usesNativeUpgradeGovernance() bool {
	return app.usesNativeGovernance() && app.withdrawals.profile.EnableNativeUpgradeGovernance
}

func decodeCanonicalNativeUpgrade(data string, value any) error {
	if len(data) == 0 || len(data) > 256 || json.Unmarshal([]byte(data), value) != nil {
		return errors.New("invalid native SDK upgrade manifest")
	}
	canonical, err := json.Marshal(value)
	if err != nil || string(canonical) != data {
		return errors.New("native SDK upgrade manifest must use canonical JSON")
	}
	return nil
}

func parseNativeUpgradeProposal(metadata string) (*nativeUpgradeManifest, error) {
	if !strings.HasPrefix(metadata, nativeUpgradeNamespace) {
		return nil, nil
	}
	body, ok := strings.CutPrefix(metadata, nativeUpgradeSchema+":")
	if !ok {
		return nil, errors.New("unsupported native SDK upgrade proposal schema")
	}
	var manifest nativeUpgradeManifest
	if err := decodeCanonicalNativeUpgrade(body, &manifest); err != nil {
		return nil, err
	}
	if manifest.Name != nativeSDKMigrationPlan || manifest.Height <= 0 {
		return nil, errors.New("native SDK upgrade proposal requires a listed handler and positive height")
	}
	return &manifest, nil
}

func (app *App) validateNativeUpgradeProposal(ctx sdk.Context, metadata string) error {
	manifest, err := parseNativeUpgradeProposal(metadata)
	if err != nil || manifest == nil {
		return err
	}
	if !app.usesNativeUpgradeGovernance() {
		return errors.New("native SDK upgrade governance is disabled")
	}
	if !app.UpgradeKeeper.HasHandler(manifest.Name) || manifest.Height <= ctx.BlockHeight() {
		return errors.New("native SDK upgrade proposal requires a registered handler and future height")
	}
	return nil
}

type dueNativeUpgrade struct {
	id       uint64
	metadata string
	manifest nativeUpgradeManifest
}

func (app *App) dueNativeUpgrades(ctx sdk.Context) ([]dueNativeUpgrade, error) {
	rng := collections.NewPrefixUntilPairRange[time.Time, uint64](ctx.BlockTime())
	var due []dueNativeUpgrade
	count := 0
	err := app.GovKeeper.ActiveProposalsQueue.Walk(ctx, rng, func(key collections.Pair[time.Time, uint64], id uint64) (bool, error) {
		count++
		if count > maxNativeGovOpenProposals {
			return true, errors.New("native governance active proposal limit exceeded")
		}
		proposal, err := app.GovKeeper.Proposals.Get(ctx, id)
		if err != nil {
			return true, err
		}
		if id != key.K2() || proposal.Id != id || proposal.Status != govv1.StatusVotingPeriod ||
			proposal.VotingEndTime == nil || !proposal.VotingEndTime.Equal(key.K1()) || len(proposal.Messages) != 0 {
			return true, errors.New("invalid active native governance proposal")
		}
		manifest, err := parseNativeUpgradeProposal(proposal.Metadata)
		if err != nil {
			return true, err
		}
		if manifest != nil {
			due = append(due, dueNativeUpgrade{id: id, metadata: proposal.Metadata, manifest: *manifest})
		}
		return false, nil
	})
	return due, err
}

func (app *App) endNativeGovernance(ctx sdk.Context) error {
	if !app.usesNativeUpgradeGovernance() {
		return app.nativeGovernance.EndBlock(ctx, &app.GovKeeper)
	}
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("native SDK upgrade governance requires FinalizeBlock")
	}
	return app.nativeGovernance.Run(ctx, func(scoped sdk.Context) error {
		due, err := app.dueNativeUpgrades(scoped)
		if err != nil {
			return err
		}
		if err := sdkgov.EndBlocker(scoped, &app.GovKeeper); err != nil {
			return err
		}
		for _, action := range due {
			proposal, err := app.GovKeeper.Proposals.Get(scoped, action.id)
			if err != nil {
				return err
			}
			if proposal.Status != govv1.StatusPassed {
				continue
			}
			if proposal.Id != action.id || proposal.Metadata != action.metadata || len(proposal.Messages) != 0 || proposal.FinalTallyResult == nil {
				return errors.New("approved native SDK upgrade differs from its tallied proposal")
			}
			if err := app.scheduleNativeUpgrade(scoped, proposal, action.manifest); err != nil {
				return err
			}
		}
		return nil
	})
}

func (app *App) scheduleNativeUpgrade(ctx sdk.Context, proposal govv1.Proposal, manifest nativeUpgradeManifest) error {
	reason := ""
	switch {
	case manifest.Height <= ctx.BlockHeight():
		reason = "native SDK upgrade height is no longer in the future"
	case !app.UpgradeKeeper.HasHandler(manifest.Name):
		reason = "native SDK upgrade handler is unavailable"
	default:
		_, err := app.UpgradeKeeper.GetUpgradePlan(ctx)
		if err == nil {
			reason = "another SDK upgrade is already scheduled"
		} else if !errors.Is(err, upgradetypes.ErrNoUpgradePlanFound) {
			return err
		} else {
			done, err := app.UpgradeKeeper.GetDoneHeight(ctx, manifest.Name)
			if err != nil {
				return err
			}
			if done != 0 {
				reason = "native SDK upgrade handler was already applied"
			}
		}
	}
	id := strconv.FormatUint(proposal.Id, 10)
	if reason != "" {
		proposal.Status, proposal.FailedReason = govv1.StatusFailed, reason
		if err := app.GovKeeper.SetProposal(ctx, proposal); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent("decimal_native_sdk_upgrade_failed",
			sdk.NewAttribute("proposal_id", id), sdk.NewAttribute("reason", reason)))
		return nil
	}
	info, err := json.Marshal(nativeUpgradeApproval{Schema: nativeUpgradeSchema, ProposalID: proposal.Id})
	if err != nil {
		return err
	}
	if err := app.UpgradeKeeper.ScheduleUpgrade(ctx, upgradetypes.Plan{Name: manifest.Name, Height: manifest.Height, Info: string(info)}); err != nil {
		return err
	}
	ctx.EventManager().EmitEvent(sdk.NewEvent("decimal_native_sdk_upgrade_scheduled",
		sdk.NewAttribute("proposal_id", id), sdk.NewAttribute("name", manifest.Name),
		sdk.NewAttribute("height", strconv.FormatInt(manifest.Height, 10))))
	return nil
}

func (app *App) registerNativeGovernanceUpgradeHandlers() {
	app.UpgradeKeeper.SetUpgradeHandler(nativeSDKMigrationPlan, app.runNativeSDKMigration)
}

func (app *App) runNativeSDKMigration(ctx context.Context, plan upgradetypes.Plan, from module.VersionMap) (module.VersionMap, error) {
	s := sdk.UnwrapSDKContext(ctx)
	if !app.usesNativeUpgradeGovernance() || s.ExecMode() != sdk.ExecModeFinalize ||
		plan.Name != nativeSDKMigrationPlan || plan.Height != s.BlockHeight() || plan.ValidateBasic() != nil {
		return nil, errors.New("native SDK upgrade handler requires its approved FinalizeBlock plan")
	}
	pending, err := app.UpgradeKeeper.GetUpgradePlan(s)
	if err != nil {
		return nil, err
	}
	if pending.Name != plan.Name || pending.Height != plan.Height || pending.Info != plan.Info {
		return nil, errors.New("native SDK upgrade handler does not match the scheduled plan")
	}
	var approval nativeUpgradeApproval
	if err := decodeCanonicalNativeUpgrade(plan.Info, &approval); err != nil {
		return nil, err
	}
	if approval.Schema != nativeUpgradeSchema || approval.ProposalID == 0 {
		return nil, errors.New("native SDK upgrade has no valid governance approval")
	}
	proposal, err := app.GovKeeper.Proposals.Get(s, approval.ProposalID)
	if err != nil {
		return nil, err
	}
	manifest, err := parseNativeUpgradeProposal(proposal.Metadata)
	if err != nil {
		return nil, err
	}
	if proposal.Id != approval.ProposalID || proposal.Status != govv1.StatusPassed || proposal.FinalTallyResult == nil ||
		len(proposal.Messages) != 0 || manifest == nil || manifest.Name != plan.Name || manifest.Height != plan.Height {
		return nil, errors.New("native SDK upgrade has no matching passed proposal")
	}
	return app.ModuleManager.RunMigrations(ctx, app.configurator, from)
}
