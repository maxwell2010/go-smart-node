package keeper

import (
	"bytes"
	"testing"
	"time"

	corestore "cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

func mutationGuardSnapshot(t *testing.T, ctx sdk.Context, service corestore.KVStoreService) map[string][]byte {
	t.Helper()
	it, err := service.OpenKVStore(ctx).Iterator(nil, nil)
	require.NoError(t, err)
	out := map[string][]byte{}
	for ; it.Valid(); it.Next() {
		out[string(it.Key())] = bytes.Clone(it.Value())
	}
	require.NoError(t, it.Error())
	require.NoError(t, it.Close())
	return out
}

func TestPendingPenaltyBlocksGatewayMutationsBeforeStoreChanges(t *testing.T) {
	for _, reason := range []byte{NativePenaltyDowntime, NativePenaltyDuplicateVote} {
		for _, route := range []string{"deposit", "withdraw", "nft-deposit", "nft-withdraw", "hold-reset", "complete", "unjail"} {
			t.Run(string(rune('0'+reason))+"/"+route, func(t *testing.T) {
				ctx, service, k := bindingEnvironment(t, false)
				ctx = ctx.WithEventManager(sdk.NewEventManager())
				position := gatewayDELFixture()
				require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
				key, err := nativePenaltyProgressKey(reason, position.Validator[:])
				require.NoError(t, err)
				// Even malformed durable progress must close custody, not be
				// interpreted as "no penalty" by a failed record decoder.
				require.NoError(t, service.OpenKVStore(ctx).Set(key, []byte("corrupt-progress")))
				before := mutationGuardSnapshot(t, ctx, service)
				events := len(ctx.EventManager().Events())
				operator, err := k.ValidatorAddressCodec().BytesToString(position.Validator[:])
				require.NoError(t, err)
				next := position
				next.Amount = next.Amount.AddRaw(1)
				nftCompanion := position
				nftCompanion.NFTBacked = sdkmath.NewInt(5)
				nft := gatewayNFTFixture(t, nftCompanion, 1, 5, []uint32{1})
				switch route {
				case "deposit":
					err = k.ApplyGatewayDELDelegation(ctx, next, sdkmath.OneInt())
				case "withdraw":
					next.Amount = position.Amount.SubRaw(1)
					err = k.ApplyGatewayDELWithdrawal(ctx, next, common.Hash{31: 9}, sdkmath.OneInt(), time.Unix(2010, 0))
				case "nft-deposit":
					err = k.ApplyGatewayNFTDelegation(ctx, nft)
				case "nft-withdraw":
					err = k.ApplyGatewayNFTWithdrawal(ctx, nft, common.Hash{31: 9}, time.Unix(2010, 0))
				case "hold-reset":
					held := gatewayDELHoldFixture(1900, 1999, 1)
					err = k.ApplyGatewayDELHoldReset(ctx, held, next, sdkmath.OneInt())
				case "complete":
					err = k.ApplyWithdrawalCompletion(ctx, types.EVMFrozenBinding{SourceValidator: position.Validator[:]})
				case "unjail":
					err = k.ReleaseNativeDowntimeJail(ctx, operator)
				}
				require.ErrorIs(t, err, ErrNativePenaltyPending)
				require.Equal(t, before, mutationGuardSnapshot(t, ctx, service))
				require.Len(t, ctx.EventManager().Events(), events)
				require.NoError(t, k.RequireNoPendingNativePenalty(ctx, stateAddress(6)), "unrelated operators remain usable")
				require.ErrorIs(t, k.RequireNoPendingNativePenalty(ctx, stateAddress(6), position.Validator[:]), ErrNativePenaltyPending, "a future source/destination move must fence both operators")
				require.NoError(t, service.OpenKVStore(ctx).Delete(key))
				require.NoError(t, k.RequireNoPendingNativePenalty(ctx, position.Validator[:]))
				require.NoError(t, k.ApplyGatewayDELDelegation(ctx, nextDeposit(position), sdkmath.OneInt()), "same user deposit can retry once penalty finalization clears its fence")
			})
		}
	}
}

func nextDeposit(p GatewayDELStake) GatewayDELStake { p.Amount = p.Amount.AddRaw(1); return p }

func TestPendingPenaltyStopsAutomaticExecutorButNotOtherValidator(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	position := gatewayDELFixture()
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	position.Amount = sdkmath.ZeroInt()
	index := common.Hash{31: 9}
	deadline := ctx.BlockTime().Add(time.Second)
	require.NoError(t, k.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(12), deadline))
	row, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
	require.NoError(t, err)
	key, err := nativePenaltyProgressKey(NativePenaltyDowntime, position.Validator[:])
	require.NoError(t, err)
	require.NoError(t, service.OpenKVStore(ctx).Set(key, []byte("malformed-but-blocking")))
	called := false
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(deadline.Add(time.Second))
	result, err := k.SettleMatureWithdrawalsLimit(ctx, 1, 500_000, func(sdk.Context, types.EVMFrozenBinding) error { called = true; return nil })
	require.NoError(t, err)
	require.False(t, called, "a pending penalty must stop execution before its financial callback")
	require.Equal(t, 1, result.Waiting)
	require.Zero(t, result.Settled)
	after, err := k.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
	require.NoError(t, err)
	require.Equal(t, row, after)
	// The trusted penalty coordinator clears the barrier only on completion.
	require.NoError(t, service.OpenKVStore(ctx).Delete(key))
	require.NoError(t, k.RequireNoPendingNativePenalty(ctx, row.SourceValidator))
}
