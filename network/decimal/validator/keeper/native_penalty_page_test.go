package keeper

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativePenaltyMassPagesRestartRetryAndNativeAccounting(t *testing.T) {
	var baseline uint64
	for _, n := range []int{10, 100, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			ctx, service, _ := environment(t)
			ctx = ctx.WithChainID(bindingTestChain).WithBlockTime(time.Unix(2000, 0).UTC())
			k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
			require.NoError(t, err)
			g := bindingGenesisState(t, false)
			for i := 0; i < n; i++ {
				p := gatewayDELFixture()
				p.Beneficiary = common.BigToAddress(big.NewInt(int64(100 + i)))
				p.StakeID = crypto.Keccak256Hash(p.Validator[:], p.Beneficiary[:], p.WrappedDEL[:], make([]byte, 64))
				row, err := p.binding(p.Amount, 1, 1)
				require.NoError(t, err)
				owner, operator, err := p.nativePair()
				require.NoError(t, err)
				g.EVMStakeBindings = append(g.EVMStakeBindings, row)
				g.Delegations = append(g.Delegations, types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, p.Amount))})
			}
			require.NoError(t, k.InitGenesis(ctx, g))
			ctx = ctx.WithBlockHeight(10).WithExecMode(sdk.ExecModeFinalize)
			p := penaltyProgressFixture(t, ctx, 1)
			require.NoError(t, k.StartNativePenaltyProgress(ctx, p))
			coinPhase := p
			coinPhase.Phase = NativePenaltyActiveCoin
			require.NoError(t, k.AdvanceNativePenaltyProgress(ctx, p, coinPhase))
			p = coinPhase
			coin := types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
			operator := p.Operator
			beforeReserve, err := k.GetActiveDELReserve(ctx, operator)
			require.NoError(t, err)
			require.True(t, beforeReserve.Equal(sdkmath.NewInt(int64(n*12))))
			var maxGas uint64
			for processed := 0; processed < n; processed++ {
				meter := storetypes.NewGasMeter(24_000_000)
				page, commit := ctx.WithGasMeter(meter).CacheContext()
				rows, err := k.GetPendingActivePenaltyKindPage(page, stateAddress(1), stateWord(0), coin, 1)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				row := rows[0]
				source := gatewayDELFixture()
				source.Beneficiary = common.BytesToAddress(row.Beneficiary)
				source.StakeID = common.BytesToHash(row.StakeID)
				require.NoError(t, k.ApplyGatewayDELPenalty(page, source, sdkmath.NewInt(11), common.BigToHash(big.NewInt(1))))
				after := p
				after.Burned = p.Burned.AddRaw(1)
				require.NoError(t, k.AdvanceNativePenaltyProgress(page, p, after))
				if processed == n/2 {
					// Crash after projected financial writes but before the outer page commit.
					unchanged, err := k.GetEVMStakeBinding(ctx, row.Kind, row.Gateway, row.StakeID)
					require.NoError(t, err)
					require.True(t, unchanged.RawAmount.Equal(sdkmath.NewInt(12)))
					saved, err := k.GetNativePenaltyProgress(ctx, stateAddress(1))
					require.NoError(t, err)
					require.Equal(t, p, saved)
					k, err = NewStateKeeperWithDomain(service, bindingTestChain, 990001)
					require.NoError(t, err)
					retried, err := k.GetPendingActivePenaltyKindPage(ctx, stateAddress(1), stateWord(0), coin, 1)
					require.NoError(t, err)
					require.Equal(t, row.StakeID, retried[0].StakeID)
					// Discard the first page and execute the same liability anew.
					page, commit = ctx.WithGasMeter(meter).CacheContext()
					require.NoError(t, k.ApplyGatewayDELPenalty(page, source, sdkmath.NewInt(11), common.BigToHash(big.NewInt(1))))
					require.NoError(t, k.AdvanceNativePenaltyProgress(page, p, after))
				}
				commit()
				p = after
				require.Less(t, meter.GasConsumed(), uint64(24_000_000))
				if meter.GasConsumed() > maxGas {
					maxGas = meter.GasConsumed()
				}
			}
			if baseline == 0 {
				baseline = maxGas
			}
			require.LessOrEqual(t, maxGas, baseline*2, "page work must not scan unrelated delegators")
			left, err := k.GetPendingActivePenaltyKindPage(ctx, stateAddress(1), stateWord(0), coin, 1)
			require.NoError(t, err)
			require.Empty(t, left)
			require.NoError(t, k.RequireActiveStakePenaltyCatchup(ctx, stateAddress(1), stateWord(1)))
			afterReserve, err := k.GetActiveDELReserve(ctx, operator)
			require.NoError(t, err)
			require.True(t, afterReserve.Equal(beforeReserve.SubRaw(int64(n))))
			require.True(t, p.Burned.Equal(sdkmath.NewInt(int64(n))))
			for p.Phase < NativePenaltyFinished {
				after := p
				after.Phase++
				require.NoError(t, k.AdvanceNativePenaltyProgress(ctx, p, after))
				p = after
			}
			require.NoError(t, k.CompleteNativePenaltyProgress(ctx, p))
			t.Logf("positions=%d page_max_sdk_gas=%d native_loss=%s", n, maxGas, p.Burned)
		})
	}
}

func TestPendingPenaltyKindPageSeparatesNFTAndMovedCursor(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	direct, nft := seedGatewayDELNFTCompanion(t, ctx, k)
	coins, err := k.GetPendingActivePenaltyKindPage(ctx, direct.Validator[:], stateWord(0), types.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, 1)
	require.NoError(t, err)
	require.Len(t, coins, 1)
	nfts, err := k.GetPendingActivePenaltyKindPage(ctx, direct.Validator[:], stateWord(0), types.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, 1)
	require.NoError(t, err)
	require.Len(t, nfts, 1)
	require.Equal(t, nft.StakeID, nfts[0].StakeID)
	moved := nfts[0]
	moved.PenaltyCursor = stateWord(1)
	plan, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &moved}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, plan))
	old, err := k.GetPendingActivePenaltyKindPage(ctx, direct.Validator[:], stateWord(0), moved.Kind, 1)
	require.NoError(t, err)
	require.Empty(t, old)
	current, err := k.GetPendingActivePenaltyKindPage(ctx, direct.Validator[:], stateWord(1), moved.Kind, 1)
	require.NoError(t, err)
	require.Equal(t, []types.EVMStakeBinding{moved}, current)
	_, err = k.GetPendingActivePenaltyKindPage(ctx, direct.Validator[:], stateWord(1), moved.Kind, 0)
	require.Error(t, err)
}
