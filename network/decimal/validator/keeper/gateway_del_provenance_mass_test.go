package keeper

import (
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
	"math/big"
	"testing"
	"time"
)

// Diagnostic census is test-only; runtime deposits inspect one bounded record.
func (k *StateKeeper) countDELProvenanceTranches(ctx sdk.Context, validator common.Address) (int, error) {
	count := 0
	var after []byte
	for {
		active, next, err := k.GetActiveStakePenaltyPage(ctx, validator[:], after, types.MaxBindingPageSize)
		if err != nil {
			return 0, err
		}
		for _, row := range active {
			if row.Kind == gatewayDELCoinBinding {
				lots, err := k.GetActiveDELProvenance(ctx, row)
				if err != nil {
					return 0, err
				}
				count += len(lots)
			}
		}
		if next == nil {
			break
		}
		after = next
	}
	after = nil
	for {
		frozen, next, err := k.GetValidatorFrozenBindingsPage(ctx, validator[:], after, types.MaxBindingPageSize)
		if err != nil {
			return 0, err
		}
		for _, row := range frozen {
			if row.SourceKind == gatewayDELCoinBinding {
				lots, _, err := k.GetFrozenDELProvenance(ctx, row)
				if err != nil {
					return 0, err
				}
				count += len(lots)
			}
		}
		if next == nil {
			break
		}
		after = next
	}
	return count, nil
}

func TestDELProvenanceMassGenesisAndIndependentDeposit(t *testing.T) {
	var firstGas uint64
	for _, n := range []int{10, 100, 1000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			ctx, service, _ := environment(t)
			ctx = ctx.WithChainID(bindingTestChain).WithBlockTime(time.Unix(2000, 0).UTC())
			k, err := NewStateKeeperWithDomain(service, bindingTestChain, 990001)
			require.NoError(t, err)
			g := bindingGenesisState(t, false)
			positions := make([]GatewayDELStake, n)
			for i := range positions {
				p := gatewayDELFixture()
				p.Beneficiary = common.BigToAddress(big.NewInt(int64(100 + i)))
				p.StakeID = crypto.Keccak256Hash(p.Validator[:], p.Beneficiary[:], p.WrappedDEL[:], make([]byte, 64))
				row, err := p.binding(p.Amount, 1, 1)
				require.NoError(t, err)
				owner, operator, err := p.nativePair()
				require.NoError(t, err)
				g.EVMStakeBindings = append(g.EVMStakeBindings, row)
				g.Delegations = append(g.Delegations, types.Delegation{Delegator: owner, Validator: operator, Stake: types.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, p.Amount))})
				positions[i] = p
			}
			require.NoError(t, k.InitGenesis(ctx, g))
			require.NoError(t, k.InitializeDELProvenance(ctx, positions[0].Gateway, positions[0].WrappedDEL))
			ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2002, 0).UTC())
			for _, p := range positions {
				requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, p), []int64{0}, []int64{12})
			}
			meter := storetypes.NewGasMeter(2_000_000)
			metered := ctx.WithGasMeter(meter)
			p := positions[n-1]
			p.Amount = p.Amount.AddRaw(3)
			require.NoError(t, k.ApplyGatewayDELDelegation(metered, p, sdkmath.NewInt(3)))
			requireDELProvenance(t, activeDELProvenanceForTest(t, ctx, k, p), []int64{0, 2}, []int64{12, 3})
			if firstGas == 0 {
				firstGas = meter.GasConsumed()
			}
			require.LessOrEqual(t, meter.GasConsumed(), firstGas*2, "deposit must not scan unrelated positions")
			t.Logf("positions=%d deposit_sdk_gas=%d", n, meter.GasConsumed())
			before, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, positions[0].Gateway[:], positions[0].StakeID[:])
			require.NoError(t, err)
			plan, err := k.PlanActiveDELHistoricalPenalty(ctx, before, 2)
			require.NoError(t, err)
			require.True(t, plan.Eligible.Equal(positions[0].Amount))
		})
	}
}
