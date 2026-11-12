package app

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	coreheader "cosmossdk.io/core/header"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/stretchr/testify/require"
)

func checkNativeGovernanceUpgrade(t *testing.T, app *App, ctx sdk.Context, proposer, voter sdk.AccAddress, deposit sdk.Coins) {
	t.Helper()
	ctx, _ = ctx.CacheContext()
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	metadata := func(name string, height int64) string {
		t.Helper()
		body, err := json.Marshal(nativeUpgradeManifest{Name: name, Height: height})
		require.NoError(t, err)
		return nativeUpgradeSchema + ":" + string(body)
	}
	submit := func(at sdk.Context, body string, messages ...sdk.Msg) (uint64, error) {
		t.Helper()
		id, err := app.GovKeeper.ProposalID.Peek(at)
		require.NoError(t, err)
		msg, err := govv1.NewMsgSubmitProposal(messages, deposit, proposer.String(), body,
			"Native SDK upgrade", "Test the pre-registered SDK module migration.", false)
		require.NoError(t, err)
		_, err = app.MsgServiceRouter().Handler(msg)(at, msg)
		return id, err
	}
	vote := func(at sdk.Context, id uint64, address sdk.AccAddress, option govv1.VoteOption) {
		t.Helper()
		msg := govv1.NewMsgVote(address, id, option, "")
		_, err := app.MsgServiceRouter().Handler(msg)(at, msg)
		require.NoError(t, err)
	}
	closeProposal := func(at sdk.Context, id uint64) (sdk.Context, govv1.Proposal) {
		t.Helper()
		proposal, err := app.GovKeeper.Proposals.Get(at, id)
		require.NoError(t, err)
		require.Equal(t, govv1.StatusVotingPeriod, proposal.Status)
		require.NotNil(t, proposal.VotingEndTime)
		closing := at.WithBlockHeight(at.BlockHeight() + 1).WithBlockTime(proposal.VotingEndTime.Add(time.Second))
		require.NoError(t, app.endNativeGovernance(closing))
		proposal, err = app.GovKeeper.Proposals.Get(closing, id)
		require.NoError(t, err)
		return closing, proposal
	}
	const height = int64(50)
	valid := metadata(nativeSDKMigrationPlan, height)
	id, err := submit(ctx, valid)
	require.ErrorContains(t, err, "upgrade governance is disabled")
	app.withdrawals.profile.EnableNativeUpgradeGovernance = true
	t.Cleanup(func() { app.withdrawals.profile.EnableNativeUpgradeGovernance = false })
	require.True(t, app.UpgradeKeeper.HasHandler(nativeSDKMigrationPlan))

	_, err = submit(ctx, metadata("unlisted-software-upgrade", height))
	require.ErrorContains(t, err, "requires a listed handler")
	_, err = submit(ctx, metadata(nativeSDKMigrationPlan, ctx.BlockHeight()))
	require.ErrorContains(t, err, "future height")
	_, err = submit(ctx, fmt.Sprintf("%s:{\"name\":%q,\"height\":%d,\"info\":\"untrusted\"}", nativeUpgradeSchema, nativeSDKMigrationPlan, height))
	require.ErrorContains(t, err, "canonical JSON")
	govAddress := authtypes.NewModuleAddress(govtypes.ModuleName).String()
	for _, authority := range []string{proposer.String(), govAddress} {
		upgrade := &upgradetypes.MsgSoftwareUpgrade{Authority: authority, Plan: upgradetypes.Plan{Name: nativeSDKMigrationPlan, Height: height}}
		handler := app.MsgServiceRouter().Handler(upgrade)
		require.NotNil(t, handler)
		_, err := handler(ctx, upgrade)
		require.ErrorContains(t, err, "circuit breaker")
		_, err = submit(ctx, valid, upgrade)
		require.ErrorContains(t, err, "no executable messages")
	}
	next, err := app.GovKeeper.ProposalID.Peek(ctx)
	require.NoError(t, err)
	require.Equal(t, id, next)
	_, err = app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)

	rejected, err := submit(ctx, valid)
	require.NoError(t, err)
	vote(ctx, rejected, proposer, govv1.OptionNo)
	vote(ctx, rejected, voter, govv1.OptionYes)
	ctx, proposal := closeProposal(ctx, rejected)
	require.Equal(t, govv1.StatusRejected, proposal.Status)
	_, err = app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)

	expired, err := submit(ctx, metadata(nativeSDKMigrationPlan, ctx.BlockHeight()+1))
	require.NoError(t, err)
	vote(ctx, expired, proposer, govv1.OptionYes)
	ctx, proposal = closeProposal(ctx, expired)
	require.Equal(t, govv1.StatusFailed, proposal.Status)
	require.Contains(t, proposal.FailedReason, "no longer in the future")
	_, err = app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)

	passed, err := submit(ctx, valid)
	require.NoError(t, err)
	vote(ctx, passed, proposer, govv1.OptionYes)
	vote(ctx, passed, voter, govv1.OptionNo)
	_, err = app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)
	ctx, proposal = closeProposal(ctx, passed)
	require.Equal(t, govv1.StatusPassed, proposal.Status)
	require.Equal(t, "6000000000000000000", proposal.FinalTallyResult.YesCount)
	require.Equal(t, "4000000000000000000", proposal.FinalTallyResult.NoCount)
	plan, err := app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.NoError(t, err)
	require.Equal(t, nativeSDKMigrationPlan, plan.Name)
	require.Equal(t, height, plan.Height)
	expectedInfo, err := json.Marshal(nativeUpgradeApproval{Schema: nativeUpgradeSchema, ProposalID: passed})
	require.NoError(t, err)
	require.Equal(t, string(expectedInfo), plan.Info)

	conflicting, err := submit(ctx, metadata(nativeSDKMigrationPlan, height+1))
	require.NoError(t, err)
	vote(ctx, conflicting, proposer, govv1.OptionYes)
	ctx, proposal = closeProposal(ctx, conflicting)
	require.Equal(t, govv1.StatusFailed, proposal.Status)
	require.Contains(t, proposal.FailedReason, "already scheduled")
	preserved, err := app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.NoError(t, err)
	require.Equal(t, plan, preserved)

	from := app.ModuleManager.GetVersionMap()
	require.NoError(t, app.UpgradeKeeper.SetModuleVersionMap(ctx, from))
	atUpgrade := ctx.WithBlockHeight(plan.Height).WithHeaderInfo(coreheader.Info{ChainID: ctx.ChainID(), Height: plan.Height, Time: ctx.BlockTime()})
	forged := plan
	forged.Info = fmt.Sprintf("{\"schema\":%q,\"proposal_id\":%d}", nativeUpgradeSchema, rejected)
	_, err = app.runNativeSDKMigration(atUpgrade, forged, from)
	require.ErrorContains(t, err, "does not match the scheduled plan")
	// Keep this child-store rehearsal from changing the reused BaseApp's
	// in-memory protocol version outside the test scope.
	setter := app.UpgradeKeeper.GetVersionSetter()
	app.UpgradeKeeper.SetVersionSetter(nil)
	defer app.UpgradeKeeper.SetVersionSetter(setter)
	require.NoError(t, app.UpgradeKeeper.ApplyUpgrade(atUpgrade, plan))
	name, doneHeight, err := app.UpgradeKeeper.GetLastCompletedUpgrade(atUpgrade)
	require.NoError(t, err)
	require.Equal(t, plan.Name, name)
	require.Equal(t, plan.Height, doneHeight)
	_, err = app.UpgradeKeeper.GetUpgradePlan(atUpgrade)
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound)
}
