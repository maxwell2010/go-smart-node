//go:build mintcandy_bootstrap_unanimity && (mintcandy_mainnet || mintcandy_testnet)

package app

import (
	"testing"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestBootstrapBallotSimulatesFinalGas(t *testing.T) {
	evmID := uint64(identity.TestnetEVMChainID)
	if identity.ProfileChainID == identity.MainnetChainID {
		evmID = identity.MainnetEVMChainID
	}
	a := testApp(t, identity.ProfileChainID, evmID)
	ctx, proposer, _, deposit := nativeGovernanceTestState(t, a)
	a.withdrawals.profile.EnableNativeUpgradeGovernance = true
	id, err := a.GovKeeper.ProposalID.Peek(ctx)
	require.NoError(t, err)
	submit, err := govv1.NewMsgSubmitProposal(nil, deposit, proposer.String(), "bootstrap-gas-signal", "Test voting gas", "Isolated fixture", false)
	require.NoError(t, err)
	_, err = a.MsgServiceRouter().Handler(submit)(ctx, submit)
	require.NoError(t, err)
	vote := govv1.NewMsgVote(proposer, id, govv1.OptionYes, "")
	transient := a.tkeys[bootstrapVoteStore]
	key := sdk.Uint64ToBigEndian(id)
	gas := func(mode sdk.ExecMode, limit uint64, wantMarker bool) uint64 {
		t.Helper()
		child, _ := ctx.CacheContext()
		meter := storetypes.NewGasMeter(limit)
		child = child.WithExecMode(mode).WithGasMeter(meter)
		_, err := a.MsgServiceRouter().Handler(vote)(child, vote)
		require.NoError(t, err)
		used := meter.GasConsumed()
		// Test reads use their own meter after execution at the exact estimated limit.
		require.Equal(t, wantMarker, child.WithGasMeter(storetypes.NewInfiniteGasMeter()).TransientStore(transient).Has(key))
		require.False(t, ctx.TransientStore(transient).Has(key))
		return used
	}
	const available = ^uint64(0)
	baseline := gas(sdk.ExecModeSimulate, available, false)
	require.Equal(t, baseline, gas(sdk.ExecModeFinalize, baseline, false))
	require.NoError(t, a.NativeValidatorKeeper.InitializeBootstrapUnanimity(ctx, id))
	estimated := gas(sdk.ExecModeSimulate, available, true)
	require.Greater(t, estimated, baseline)
	require.Equal(t, estimated, gas(sdk.ExecModeFinalize, estimated, true))
}
