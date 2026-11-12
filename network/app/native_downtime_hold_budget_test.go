package app

import (
	"bytes"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestNativeHeldDowntimeLotAdmissionPreservesSplitExit(t *testing.T) {
	app := newWithdrawalSnapshotTestApp(t)
	const chain = "mintcandy_990001-1"
	start := time.Unix(5000, 0).UTC()
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: chain, Height: 10, Time: start})
	service := runtime.NewKVStoreService(app.keys[validatortypes.StoreKey])
	keeper, err := validatorkeeper.NewStateKeeperWithDomain(service, chain, 990001)
	require.NoError(t, err)
	require.NoError(t, keeper.InitGenesis(ctx, validatortypes.DefaultGenesisState()))
	profile := WithdrawalProfile{
		Gateway: common.Address{19: 1}, WrappedDEL: common.Address{19: 2},
		EnableNativeConsensus: true, EnableNativeOrdinaryDELDowntime: true,
		EnableDELHold: true, EnableNativeHeldDELDowntime: true,
	}
	controller := &withdrawalController{profile: profile, keeper: keeper}
	beneficiary, validator := common.Address{19: 3}, common.Address{19: 4}
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(beneficiary[:])
	require.NoError(t, err)
	operator, err := keeper.ValidatorAddressCodec().BytesToString(validator[:])
	require.NoError(t, err)
	publicKey := &ed25519.PubKey{Key: bytes.Repeat([]byte{0x42}, ed25519.PubKeySize)}
	v, err := validatortypes.NewValidator(operator, owner, publicKey, validatortypes.Description{Moniker: "held-lot-budget"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	require.NoError(t, keeper.SetValidator(ctx, v))

	makePosition := func(start, deadline time.Time) validatorkeeper.GatewayDELStake {
		until, tokenID := common.BigToHash(big.NewInt(deadline.Unix())), common.Hash{}
		return validatorkeeper.GatewayDELStake{
			Gateway: profile.Gateway, WrappedDEL: profile.WrappedDEL, Validator: validator, Beneficiary: beneficiary,
			StakeID: crypto.Keccak256Hash(validator[:], beneficiary[:], profile.WrappedDEL[:], tokenID[:], until[:]),
			Amount:  sdkmath.ZeroInt(), HoldStartTime: start.Unix(), HoldDeadline: deadline.Unix(),
		}
	}
	deadline := start.Add(200 * time.Second)
	position := makePosition(start, deadline)
	deposit := sdkmath.NewInt(2)
	for i := range nativeDowntimeHoldLots - 2 {
		// Seed normal native lots using the same projection as the signed route.
		ctx = ctx.WithBlockHeight(int64(10 + i)).WithBlockTime(start.Add(time.Duration(i) * time.Second))
		position.Amount = position.Amount.Add(deposit)
		require.NoError(t, keeper.ApplyGatewayDELDelegation(ctx, position, deposit))
	}
	checkLots := func(ctx sdk.Context, want int) {
		t.Helper()
		count, err := controller.ordinaryDowntimeHoldLots(ctx, validator[:])
		require.NoError(t, err)
		require.Equal(t, want, count)
	}

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(time.Second))
	twoBuckets, _ := ctx.CacheContext()
	second := makePosition(ctx.BlockTime(), deadline.Add(time.Second))
	second.Amount = deposit
	require.NoError(t, controller.projectGatewayDELDeposit(twoBuckets, second, deposit))
	checkLots(twoBuckets, nativeDowntimeHoldLots-1) // both bindings share the same native pair

	position.Amount = position.Amount.Add(deposit)
	require.NoError(t, controller.projectGatewayDELDeposit(ctx, position, deposit))
	checkLots(ctx, nativeDowntimeHoldLots-1)
	position.Amount = position.Amount.Add(deposit)
	require.NoError(t, controller.projectGatewayDELDeposit(ctx, position, deposit)) // same timestamp
	checkLots(ctx, nativeDowntimeHoldLots-1)

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(ctx.BlockTime().Add(time.Second))
	rejected := position
	rejected.Amount = rejected.Amount.Add(deposit)
	require.ErrorContains(t, controller.projectGatewayDELDeposit(ctx, rejected, deposit), "limit of 127 hold lots")
	checkLots(ctx, nativeDowntimeHoldLots-1)
	binding, err := keeper.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, profile.Gateway[:], position.StakeID[:])
	require.NoError(t, err)
	require.True(t, position.Amount.Equal(binding.RawAmount))

	// A larger historical snapshot can still queue a partial withdrawal.
	legacy, _ := ctx.CacheContext()
	require.NoError(t, keeper.ApplyGatewayDELDelegation(legacy, rejected, deposit))
	legacy = legacy.WithBlockHeight(legacy.BlockHeight() + 1).WithBlockTime(deadline.Add(time.Second))
	rejected.Amount = rejected.Amount.SubRaw(1)
	require.NoError(t, keeper.ApplyGatewayDELWithdrawal(legacy, rejected, common.Hash{}, sdkmath.OneInt(), deadline.Add(time.Minute)))
	checkLots(legacy, nativeDowntimeHoldLots+1)
	require.NoError(t, controller.validateOrdinaryDowntimeAdmission(legacy))

	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + 1).WithBlockTime(deadline.Add(time.Second))
	position.Amount = position.Amount.SubRaw(1)
	require.NoError(t, keeper.ApplyGatewayDELWithdrawal(ctx, position, common.Hash{}, sdkmath.OneInt(), deadline.Add(time.Minute)))
	checkLots(ctx, nativeDowntimeHoldLots)
	require.NoError(t, controller.validateOrdinaryDowntimeAdmission(ctx))
	pending, err := keeper.GetUndelegation(ctx, owner, operator)
	require.NoError(t, err)
	require.Len(t, pending.Entries, 1)
	require.Len(t, pending.Entries[0].Stake.Holds, 1)
	require.True(t, pending.Entries[0].Stake.Holds[0].Amount.Equal(sdkmath.OneInt()))
}
