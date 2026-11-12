package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	govlegacy "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	nativegov "github.com/maxwell2010/go-smart-node/network/decimal/validator/governance"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"google.golang.org/grpc"
)

const (
	maxNativeGovOpenProposals = 8
	maxNativeGovProposalVotes = 256
	maxNativeGovDepositors    = 128
)

func (app *App) usesNativeGovernance() bool {
	return app.withdrawals != nil && app.withdrawals.profile.EnableNativeGovernance
}

func (app *App) newNativeGovernanceSessions() (*nativegov.Sessions, error) {
	return nativegov.NewActiveDELSessions(app.NativeValidatorKeeper,
		sdkmath.NewInt(nativekeeper.NativeDELAtomsPerPower), func(ctx sdk.Context) any {
			return ctx.MultiStore().GetKVStore(app.keys[validatortypes.StoreKey])
		})
}

type governanceStakingReader struct {
	govtypes.StakingKeeper
	app *App
}

func (r governanceStakingReader) reader() govtypes.StakingKeeper {
	if r.app.usesNativeGovernance() {
		return r.app.nativeGovernance
	}
	return r.StakingKeeper
}

func (r governanceStakingReader) IterateBondedValidatorsByPower(ctx context.Context, fn func(int64, stakingtypes.ValidatorI) bool) error {
	return r.reader().IterateBondedValidatorsByPower(ctx, fn)
}

func (r governanceStakingReader) IterateDelegations(ctx context.Context, delegator sdk.AccAddress, fn func(int64, stakingtypes.DelegationI) bool) error {
	return r.reader().IterateDelegations(ctx, delegator, fn)
}

func (r governanceStakingReader) TotalBondedTokens(ctx context.Context) (sdkmath.Int, error) {
	return r.reader().TotalBondedTokens(ctx)
}

func nativeGovernanceMessage(url string) bool {
	return url == sdk.MsgTypeURL(&govv1.MsgSubmitProposal{}) ||
		url == sdk.MsgTypeURL(&govv1.MsgDeposit{}) ||
		url == sdk.MsgTypeURL(&govv1.MsgVote{}) ||
		url == sdk.MsgTypeURL(&govv1.MsgVoteWeighted{})
}

// nativeGovernanceServices keeps SDK module registration and migrations intact.
// Only the governance message and tally query entry points need adapters.
type nativeGovernanceServices struct {
	grpc.ServiceRegistrar
	app *App
}

func (r nativeGovernanceServices) RegisterService(desc *grpc.ServiceDesc, service any) {
	switch desc.ServiceName {
	case "cosmos.gov.v1.Msg":
		service = nativeGovernanceMsgServer{MsgServer: service.(govv1.MsgServer), app: r.app}
	case "cosmos.gov.v1.Query":
		query, err := r.app.nativeGovernance.QueryServer(&r.app.GovKeeper)
		if err != nil {
			panic(err)
		}
		service = nativeGovernanceQueryServer{QueryServer: service.(govv1.QueryServer), native: query, app: r.app}
	case "cosmos.gov.v1beta1.Query":
		service = nativeGovernanceLegacyQueryServer{QueryServer: service.(govlegacy.QueryServer), app: r.app}
	}
	r.ServiceRegistrar.RegisterService(desc, service)
}

type nativeGovernanceMsgServer struct {
	govv1.MsgServer
	app *App
}

func (s nativeGovernanceMsgServer) SubmitProposal(ctx context.Context, msg *govv1.MsgSubmitProposal) (*govv1.MsgSubmitProposalResponse, error) {
	if s.app.usesNativeGovernance() {
		if msg == nil || len(msg.Messages) != 0 {
			return nil, errors.New("native governance proposals must have no executable messages")
		}
		if err := s.app.validateNativeUpgradeProposal(sdk.UnwrapSDKContext(ctx), msg.Metadata); err != nil {
			return nil, err
		}
		if _, err := s.app.nativeValidatorRegistration(sdk.UnwrapSDKContext(ctx), msg.Metadata, msg.Proposer, msg.Title); err != nil {
			return nil, err
		}
	}
	return s.MsgServer.SubmitProposal(ctx, msg)
}

type nativeGovernanceQueryServer struct {
	govv1.QueryServer
	native govv1.QueryServer
	app    *App
}

func (s nativeGovernanceQueryServer) TallyResult(ctx context.Context, req *govv1.QueryTallyResultRequest) (*govv1.QueryTallyResultResponse, error) {
	if s.app.usesNativeGovernance() {
		return s.native.TallyResult(ctx, req)
	}
	return s.QueryServer.TallyResult(ctx, req)
}

type nativeGovernanceLegacyQueryServer struct {
	govlegacy.QueryServer
	app *App
}

func (s nativeGovernanceLegacyQueryServer) TallyResult(ctx context.Context, req *govlegacy.QueryTallyResultRequest) (result *govlegacy.QueryTallyResultResponse, err error) {
	if !s.app.usesNativeGovernance() {
		return s.QueryServer.TallyResult(ctx, req)
	}
	if req == nil {
		return nil, errors.New("native governance tally request is missing")
	}
	err = s.app.nativeGovernance.ReadOnly(sdk.UnwrapSDKContext(ctx), func(child sdk.Context) error {
		result, err = s.QueryServer.TallyResult(child, req)
		return err
	})
	return result, err
}

type nativeGovernanceHooks struct {
	govtypes.MultiGovHooks
	app *App
}

func (h nativeGovernanceHooks) AfterProposalSubmission(ctx context.Context, _ uint64) error {
	if !h.app.usesNativeGovernance() {
		return nil
	}
	count := 0
	visit := func(collections.Pair[time.Time, uint64], uint64) (bool, error) {
		count++
		if count > maxNativeGovOpenProposals {
			return true, errors.New("native governance open proposal limit reached")
		}
		return false, nil
	}
	if err := h.app.GovKeeper.ActiveProposalsQueue.Walk(ctx, nil, visit); err != nil {
		return err
	}
	return h.app.GovKeeper.InactiveProposalsQueue.Walk(ctx, nil, visit)
}

func (h nativeGovernanceHooks) AfterProposalVote(ctx context.Context, id uint64, voter sdk.AccAddress) error {
	if !h.app.usesNativeGovernance() {
		return nil
	}
	if len(voter) != 20 {
		return errors.New("native governance voter requires a 20-byte account")
	}
	return checkNativeGovernanceProposalLimit(ctx, h.app.GovKeeper.Votes, id, maxNativeGovProposalVotes, "voter")
}

func (h nativeGovernanceHooks) AfterProposalDeposit(ctx context.Context, id uint64, depositor sdk.AccAddress) error {
	if !h.app.usesNativeGovernance() {
		return nil
	}
	// The SDK hook runs before writing a new deposit record.
	exists, err := h.app.GovKeeper.Deposits.Has(ctx, collections.Join(id, depositor))
	if err != nil {
		return err
	}
	limit := maxNativeGovDepositors
	if !exists {
		limit--
	}
	return checkNativeGovernanceProposalLimit(ctx, h.app.GovKeeper.Deposits, id, limit, "depositor")
}

func checkNativeGovernanceProposalLimit[V any](ctx context.Context, entries collections.Map[collections.Pair[uint64, sdk.AccAddress], V], id uint64, limit int, subject string) error {
	count := 0
	return entries.Walk(ctx, collections.NewPrefixedPairRange[uint64, sdk.AccAddress](id), func(collections.Pair[uint64, sdk.AccAddress], V) (bool, error) {
		count++
		if count > limit {
			return true, fmt.Errorf("native governance %s limit reached", subject)
		}
		return false, nil
	})
}

func (app *App) validateNativeGovernanceGenesis(genesis GenesisState) error {
	var gov govv1.GenesisState
	if err := app.appCodec.UnmarshalJSON(genesis[govtypes.ModuleName], &gov); err != nil {
		return err
	}
	if gov.Empty() || len(gov.Proposals)+len(gov.Deposits)+len(gov.Votes) != 0 {
		return errors.New("native governance requires fresh proposals, deposits and votes at genesis")
	}
	if err := govv1.ValidateGenesis(&gov); err != nil {
		return err
	}
	for _, coins := range []sdk.Coins{gov.Params.MinDeposit, gov.Params.ExpeditedMinDeposit} {
		if len(coins) != 1 || coins[0].Denom != identity.BaseDenom || !coins[0].IsPositive() {
			return errors.New("native governance requires positive DEL proposal deposits")
		}
	}
	return nil
}
