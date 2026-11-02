package adapter

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"
	"time"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, denom string) (sdk.Context, store.KVStoreService, *nativekeeper.StateKeeper, []nativetypes.Validator) {
	t.Helper()
	key := storetypes.NewKVStoreKey("native-adapter")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	service := runtime.NewKVStoreService(key)
	native, e := nativekeeper.NewStateKeeper(service)
	require.NoError(t, e)
	g := nativetypes.DefaultGenesisState()
	g.Params.BaseDenom = denom
	for id := byte(1); id < 3; id++ {
		raw := bytes.Repeat([]byte{id}, 20)
		operator, e := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(raw)
		require.NoError(t, e)
		reward, e := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(raw)
		require.NoError(t, e)
		v, e := nativetypes.NewValidator(operator, reward, ed25519.GenPrivKeyFromSecret([]byte{id}).PubKey(), nativetypes.Description{Moniker: "native"}, sdkmath.LegacyMustNewDecFromStr("0.1"))
		require.NoError(t, e)
		v.Status = nativetypes.BondStatus_Bonded
		v.Stake = int64(id) * 7
		v.Rewards = sdkmath.NewInt(3)
		v.TotalRewards = sdkmath.NewInt(10)
		g.Validators = append(g.Validators, v)
	}
	require.NoError(t, native.InitGenesis(ctx, g))
	set, e := nativekeeper.ConsensusSet(g.Validators)
	require.NoError(t, e)
	h, e := nativetypes.NewHistoricalInfo(cmtproto.Header{Height: 1, ChainID: "mintcandy_300001-1", Time: time.Unix(1, 0).UTC(), ValidatorsHash: set.Hash(), NextValidatorsHash: set.Hash()}, g.Validators)
	require.NoError(t, e)
	require.NoError(t, native.SetHistoricalInfo(ctx, h))
	return ctx, service, native, g.Validators
}

func state(t *testing.T, ctx sdk.Context, service store.KVStoreService) map[string][]byte {
	t.Helper()
	it, e := service.OpenKVStore(ctx).Iterator(nil, nil)
	require.NoError(t, e)
	result := map[string][]byte{}
	for ; it.Valid(); it.Next() {
		require.NoError(t, it.Error())
		result[string(it.Key())] = bytes.Clone(it.Value())
	}
	require.NoError(t, it.Close())
	return result
}

func TestEVMReadOnlyProjectionUsesHydratedNativeRows(t *testing.T) {
	ctx, service, native, vals := fixture(t, "del")
	scale := sdkmath.NewIntWithDecimal(1, 18)
	a, e := NewEVM(native, scale)
	require.NoError(t, e)
	before := state(t, ctx, service)
	cons, e := vals[0].GetConsAddr()
	require.NoError(t, e)
	v, e := a.GetValidatorByConsAddr(ctx, cons)
	require.NoError(t, e)
	require.True(t, v.Tokens.Equal(scale.MulRaw(7)))
	require.EqualValues(t, 7, v.ConsensusPower(scale))
	require.Equal(t, vals[0].OperatorAddress, v.GetOperator())
	require.True(t, v.DelegatorShares.IsNil())
	require.True(t, v.MinSelfDelegation.IsNil())
	raw, e := a.ValidatorAddressCodec().StringToBytes(v.GetOperator())
	require.NoError(t, e)
	require.Equal(t, bytes.Repeat([]byte{1}, 20), raw)
	_, e = a.ValidatorAddressCodec().StringToBytes(vals[0].RewardAddress)
	require.Error(t, e)
	denom, e := a.BondDenom(ctx)
	require.NoError(t, e)
	require.Equal(t, "del", denom)
	require.Equal(t, before, state(t, ctx, service))
	child, commit := ctx.CacheContext()
	updated := vals[0]
	updated.Stake = 99
	updated.Rewards = sdkmath.NewInt(4)
	require.NoError(t, native.SetValidator(child, updated))
	projected, e := a.GetValidatorByConsAddr(child, cons)
	require.NoError(t, e)
	require.EqualValues(t, 99, projected.ConsensusPower(scale))
	parent, e := a.GetValidatorByConsAddr(ctx, cons)
	require.NoError(t, e)
	require.EqualValues(t, 7, parent.ConsensusPower(scale))
	commit()
	parent, e = a.GetValidatorByConsAddr(ctx, cons)
	require.NoError(t, e)
	require.EqualValues(t, 99, parent.ConsensusPower(scale))
	// Returned key bytes and SDK token containers cannot mutate native state.
	parent.ConsensusPubkey.Value[0] ^= 1
	parent.Tokens.BigInt().SetInt64(0)
	fresh, e := a.GetValidatorByConsAddr(ctx, cons)
	require.NoError(t, e)
	require.Equal(t, updated.ConsensusPubkey.Value, fresh.ConsensusPubkey.Value)
	require.EqualValues(t, 99, fresh.ConsensusPower(scale))
}

func TestEVMHistoricalProjectionPreservesEffectiveCometSetAndDelay(t *testing.T) {
	ctx, _, native, vals := fixture(t, "del")
	scale := sdkmath.NewInt(1000000)
	a, e := NewEVM(native, scale)
	require.NoError(t, e)
	changed := vals[0]
	changed.Stake = 1000
	require.NoError(t, native.SetValidator(ctx, changed))
	h, e := a.GetHistoricalInfo(ctx, 1)
	require.NoError(t, e)
	require.Len(t, h.Valset, 2)
	require.EqualValues(t, 14, h.Valset[0].ConsensusPower(scale))
	require.EqualValues(t, 7, h.Valset[1].ConsensusPower(scale))
	var projected []*cmttypes.Validator
	for _, v := range h.Valset {
		key, e := v.ConsPubKey()
		require.NoError(t, e)
		cmt, e := cryptocodec.ToCmtPubKeyInterface(key)
		require.NoError(t, e)
		projected = append(projected, cmttypes.NewValidator(cmt, v.ConsensusPower(scale)))
	}
	require.Equal(t, h.Header.ValidatorsHash, cmttypes.NewValidatorSet(projected).Hash())
	h.Header.ValidatorsHash[0] ^= 1
	h.Valset[0].ConsensusPubkey.Value[0] ^= 1
	fresh, e := a.GetHistoricalInfo(ctx, 1)
	require.NoError(t, e)
	require.NotEqual(t, h.Header.ValidatorsHash, fresh.Header.ValidatorsHash)
	_, e = a.GetHistoricalInfo(ctx, 2)
	require.ErrorIs(t, e, nativekeeper.ErrNotFound)
}

func TestEVMAdapterRequiresCoherentDomainAndBoundedExplicitScale(t *testing.T) {
	ctx, _, native, vals := fixture(t, "del")
	_, e := NewEVM(nil, sdkmath.OneInt())
	require.Error(t, e)
	_, e = NewEVM(native, sdkmath.Int{})
	require.Error(t, e)
	_, e = NewEVM(native, sdkmath.ZeroInt())
	require.Error(t, e)
	maximum := sdkmath.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	_, e = NewEVM(native, maximum)
	require.ErrorContains(t, e, "overflow")
	original := sdk.DefaultPowerReduction
	sdk.DefaultPowerReduction = sdkmath.NewInt(17)
	t.Cleanup(func() { sdk.DefaultPowerReduction = original })
	scale := sdkmath.NewInt(123)
	a, e := NewEVM(native, scale)
	require.NoError(t, e)
	sdk.DefaultPowerReduction = sdkmath.NewInt(999)
	cons, e := vals[0].GetConsAddr()
	require.NoError(t, e)
	v, e := a.GetValidatorByConsAddr(ctx, cons)
	require.NoError(t, e)
	require.EqualValues(t, 7, v.ConsensusPower(scale))
	other, _, foreign, foreignVals := fixture(t, "other")
	bad, e := NewEVM(foreign, sdkmath.OneInt())
	require.NoError(t, e)
	_, e = bad.BondDenom(other)
	require.ErrorContains(t, e, "del")
	foreignCons, e := foreignVals[0].GetConsAddr()
	require.NoError(t, e)
	_, e = bad.GetValidatorByConsAddr(other, foreignCons)
	require.Error(t, e)
	_, e = bad.GetHistoricalInfo(other, 1)
	require.Error(t, e)
	_, e = (&EVM{}).BondDenom(ctx)
	require.Error(t, e)
}

func TestEVMAdapterExposesNoFinancialOrGovernanceLedger(t *testing.T) {
	_, _, native, _ := fixture(t, "del")
	a, e := NewEVM(native, sdkmath.OneInt())
	require.NoError(t, e)
	typ := reflect.TypeOf(a)
	require.Equal(t, 4, typ.NumMethod())
	require.NotImplements(t, (*govtypes.StakingKeeper)(nil), a)
	for _, name := range []string{"SetValidator", "Delegate", "Undelegate", "TotalBondedTokens", "IterateBondedValidatorsByPower", "IterateDelegations", "MintCoins"} {
		_, exists := typ.MethodByName(name)
		require.False(t, exists, name)
	}
}

func TestEVMMissingCurrentDynamicRowsAreErrorsButHistoryKeepsItsOwnPower(t *testing.T) {
	ctx, service, _, vals := fixture(t, "del")
	native, e := nativekeeper.NewStateKeeper(dynamicFailureService{service})
	require.NoError(t, e)
	a, e := NewEVM(native, sdkmath.OneInt())
	require.NoError(t, e)
	cons, e := vals[0].GetConsAddr()
	require.NoError(t, e)
	_, e = a.GetValidatorByConsAddr(ctx, cons)
	require.ErrorContains(t, e, "dynamic row unavailable")
	// The effective historical set was serialized at that height. Losing a
	// current dynamic row must not substitute zero or today's power into it.
	historical, e := a.GetHistoricalInfo(ctx, 1)
	require.NoError(t, e)
	require.EqualValues(t, 14, historical.Valset[0].ConsensusPower(sdkmath.OneInt()))
}

type dynamicFailureService struct{ store.KVStoreService }

func (s dynamicFailureService) OpenKVStore(ctx context.Context) store.KVStore {
	return dynamicFailureStore{s.KVStoreService.OpenKVStore(ctx)}
}

type dynamicFailureStore struct{ store.KVStore }

func (s dynamicFailureStore) Get(key []byte) ([]byte, error) {
	if len(key) > 0 && key[0] == 0x11 {
		return nil, errors.New("dynamic row unavailable")
	}
	return s.KVStore.Get(key)
}
