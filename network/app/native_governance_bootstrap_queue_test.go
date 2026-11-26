package app

import (
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestBootstrapUnanimousQueueAndSDKSettlement(t *testing.T) {
	chain, evm := identity.TestnetChainID, uint64(identity.TestnetEVMChainID)
	if identity.ProfileChainID == identity.MainnetChainID {
		chain, evm = identity.MainnetChainID, identity.MainnetEVMChainID
	}
	app := testApp(t, chain, evm)
	ctx, proposer, voter, funds := nativeGovernanceTestState(t, app)
	ctx = ctx.WithExecMode(sdk.ExecModeFinalize)
	app.withdrawals.profile.EnableNativeUpgradeGovernance = true
	govAccount := authtypes.NewModuleAddress(govtypes.ModuleName)
	params, err := app.GovKeeper.Params.Get(ctx)
	require.NoError(t, err)
	deposit := funds.QuoInt(sdkmath.NewInt(4))
	params.MinDeposit = deposit
	require.NoError(t, app.GovKeeper.Params.Set(ctx, params))

	submit := func(metadata string) uint64 {
		t.Helper()
		id, err := app.GovKeeper.ProposalID.Peek(ctx)
		require.NoError(t, err)
		msg, err := govv1.NewMsgSubmitProposal(nil, deposit, proposer.String(), metadata, "Governance test", "Isolated fixture", false)
		require.NoError(t, err)
		_, err = app.MsgServiceRouter().Handler(msg)(ctx, msg)
		require.NoError(t, err)
		for _, addr := range []sdk.AccAddress{proposer, voter} {
			vote := govv1.NewMsgVote(addr, id, govv1.OptionYes, "")
			_, err = app.MsgServiceRouter().Handler(vote)(ctx, vote)
			require.NoError(t, err)
		}
		return id
	}
	old := submit("older-signal")
	upgrade := submit(fmt.Sprintf("%s:{\"name\":%q,\"height\":80}", nativeUpgradeSchema, nativeSDKMigrationPlan))
	pending := submit("ineligible-signal")
	signal := submit("second-eligible-signal")
	proposal, err := app.GovKeeper.Proposals.Get(ctx, old)
	require.NoError(t, err)
	deadline := *proposal.VotingEndTime
	ctx = ctx.WithBlockTime(ctx.BlockTime().Add(time.Second)).WithBlockHeight(2)
	require.True(t, ctx.BlockTime().Before(deadline))
	require.Equal(t, funds[0], app.BankKeeper.GetBalance(ctx, govAccount, identity.BaseDenom))

	err = app.nativeGovernance.Run(ctx, func(scoped sdk.Context) error {
		return app.advanceUnanimousVoting(scoped, upgrade, func(id uint64) (bool, error) {
			require.NotEqual(t, old, id)
			return id != pending, nil
		})
	})
	require.NoError(t, err)
	require.NoError(t, app.endNativeGovernance(ctx))
	for _, id := range []uint64{old, upgrade, pending, signal} {
		p, err := app.GovKeeper.Proposals.Get(ctx, id)
		require.NoError(t, err)
		queued, err := app.GovKeeper.ActiveProposalsQueue.Has(ctx, collections.Join(deadline, id))
		require.NoError(t, err)
		deposits, err := app.GovKeeper.GetDeposits(ctx, id)
		require.NoError(t, err)
		if id == old || id == pending {
			require.Equal(t, govv1.StatusVotingPeriod, p.Status)
			require.True(t, p.VotingEndTime.Equal(deadline))
			require.True(t, queued)
			require.Len(t, deposits, 1)
			continue
		}
		require.Equal(t, govv1.StatusPassed, p.Status)
		require.True(t, p.VotingEndTime.Equal(ctx.BlockTime()))
		require.Equal(t, "10000000000000000000", p.FinalTallyResult.YesCount)
		require.False(t, queued)
		require.Empty(t, deposits)
	}
	plan, err := app.UpgradeKeeper.GetUpgradePlan(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(80), plan.Height)
	require.Equal(t, nativeSDKMigrationPlan, plan.Name)
	half := deposit[0].Add(deposit[0])
	require.Equal(t, half, app.BankKeeper.GetBalance(ctx, proposer, identity.BaseDenom))
	require.Equal(t, half, app.BankKeeper.GetBalance(ctx, govAccount, identity.BaseDenom))
	require.NoError(t, app.endNativeGovernance(ctx.WithBlockHeight(3).WithBlockTime(deadline)))
	require.Equal(t, funds[0], app.BankKeeper.GetBalance(ctx, proposer, identity.BaseDenom))
	require.True(t, app.BankKeeper.GetBalance(ctx, govAccount, identity.BaseDenom).IsZero())
}
