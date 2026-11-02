package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func addr(t *testing.T, prefix string, id byte) string {
	t.Helper()
	s, e := address.NewBech32Codec(prefix).BytesToString(bytes.Repeat([]byte{id}, 20))
	require.NoError(t, e)
	return s
}
func validator(t *testing.T, id byte, power int64) types.Validator {
	t.Helper()
	v, e := types.NewValidator(addr(t, identity.Bech32PrefixValAddr, id), addr(t, identity.Bech32PrefixAccAddr, id), ed25519.GenPrivKeyFromSecret([]byte{id}).PubKey(), types.Description{Moniker: "native"}, sdkmath.LegacyMustNewDecFromStr("0.1"))
	require.NoError(t, e)
	v.Status = types.BondStatus_Bonded
	v.Stake = power
	v.Rewards = sdkmath.NewInt(3)
	v.TotalRewards = sdkmath.NewInt(10)
	return v
}
func environment(t *testing.T) (sdk.Context, store.KVStoreService, *StateKeeper) {
	t.Helper()
	key := storetypes.NewKVStoreKey("native-validator")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient"))
	service := runtime.NewKVStoreService(key)
	k, e := NewStateKeeper(service)
	require.NoError(t, e)
	return ctx, service, k
}
func genesis(t *testing.T) *types.GenesisState {
	t.Helper()
	a, b := validator(t, 1, 100), validator(t, 2, 200)
	g := types.DefaultGenesisState()
	g.Params.HistoricalEntries = 2
	g.Validators = []types.Validator{a, b}
	g.LastValidatorPowers = []types.LastValidatorPower{{Address: a.OperatorAddress, Power: 100}, {Address: b.OperatorAddress, Power: 200}}
	g.LastTotalPower = 300
	nft := types.NewStakeNFT("nft-token", []uint32{1, 2}, sdk.NewCoin("del", sdkmath.NewInt(100)))
	nft.Holds = []*types.StakeHold{{Amount: sdkmath.NewInt(40), SubTokenIDs: []uint32{1}, HoldStartTime: 100, HoldEndTime: 200}}
	g.Delegations = []types.Delegation{{Delegator: a.RewardAddress, Validator: a.OperatorAddress, Stake: nft}}
	g.Undelegations = []types.Undelegation{{Delegator: a.RewardAddress, Validator: a.OperatorAddress, Entries: []types.UndelegationEntry{{CreationHeight: 1, CompletionTime: time.Unix(1000, 0).UTC(), Stake: types.NewStakeCoin(sdk.NewCoin("del", sdkmath.NewInt(7)))}}}}
	g.Redelegations = []types.Redelegation{{Delegator: a.RewardAddress, ValidatorSrc: a.OperatorAddress, ValidatorDst: b.OperatorAddress, Entries: []types.RedelegationEntry{{CreationHeight: 1, CompletionTime: time.Unix(2000, 0).UTC(), Stake: types.NewStakeCoin(sdk.NewCoin("del", sdkmath.NewInt(9)))}}}}
	g.ValidatorOfflineSince = []types.ValidatorOfflineSince{{ValidatorAddress: a.OperatorAddress, OfflineSince: time.Unix(100, 0).UTC()}}
	require.NoError(t, g.Validate())
	return g
}

func TestNativeGenesisExportHydratesRealDynamicRows(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	export, e := k.ExportGenesis(ctx)
	require.NoError(t, e)
	require.EqualValues(t, 300, export.LastTotalPower)
	require.Equal(t, g.Validators[0].Rewards, export.Validators[0].Rewards)
	require.Equal(t, g.Delegations[0].Stake.Stake, export.Delegations[0].Stake.Stake)
	require.Equal(t, g.Delegations[0].Stake.Holds[0].Amount, export.Delegations[0].Stake.Holds[0].Amount)
	ctx2, _, k2 := environment(t)
	require.NoError(t, k2.InitGenesis(ctx2, export))
	again, e := k2.ExportGenesis(ctx2)
	require.NoError(t, e)
	firstBytes, e := proto.Marshal(export)
	require.NoError(t, e)
	secondBytes, e := proto.Marshal(again)
	require.NoError(t, e)
	require.Equal(t, firstBytes, secondBytes)
	v := export.Validators[0]
	key, e := operatorKey(validatorPrefix, v.OperatorAddress)
	require.NoError(t, e)
	raw, e := service.OpenKVStore(ctx).Get(key)
	require.NoError(t, e)
	var metadata types.Validator
	require.NoError(t, proto.Unmarshal(raw, &metadata))
	require.True(t, metadata.Rewards.IsZero())
	require.Zero(t, metadata.Stake)
	cons, e := v.GetConsAddr()
	require.NoError(t, e)
	read, e := k.GetValidatorByConsAddr(ctx, cons)
	require.NoError(t, e)
	require.EqualValues(t, 100, read.ConsensusPower())
	require.True(t, read.Rewards.Equal(sdkmath.NewInt(3)))
	denom, e := k.BondDenom(ctx)
	require.NoError(t, e)
	require.Equal(t, "del", denom)
	duration, e := k.UnbondingTime(ctx)
	require.NoError(t, e)
	require.Equal(t, 28*24*time.Hour, duration)
	require.ErrorContains(t, k.InitGenesis(ctx, g), "not empty")
}

func TestNativeMutationsRespectOuterCacheAndDoNotAlias(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	child, commit := ctx.CacheContext()
	v := g.Validators[0]
	v.Stake = 123
	v.Rewards = sdkmath.NewInt(4)
	require.NoError(t, k.SetValidator(child, v))
	before, e := k.GetValidator(ctx, v.OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, before.Stake)
	after, e := k.GetValidator(child, v.OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 123, after.Stake)
	d := g.Delegations[0]
	d.Stake.Stake.Amount = sdkmath.NewInt(110)
	require.NoError(t, k.SetDelegation(child, d))
	parent, e := k.ExportGenesis(ctx)
	require.NoError(t, e)
	require.True(t, parent.Delegations[0].Stake.Stake.Amount.Equal(sdkmath.NewInt(100)))
	commit()
	after, e = k.GetValidator(ctx, v.OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 123, after.Stake)
	after.ConsensusPubkey.Value[0] ^= 1
	after.Rewards.BigInt().SetInt64(0)
	fresh, e := k.GetValidator(ctx, v.OperatorAddress)
	require.NoError(t, e)
	require.Equal(t, v.ConsensusPubkey.Value, fresh.ConsensusPubkey.Value)
	require.True(t, fresh.Rewards.Equal(sdkmath.NewInt(4)))
	dropped, _ := ctx.CacheContext()
	p, e := k.GetParams(dropped)
	require.NoError(t, e)
	p.UndelegationTime = 42 * 24 * time.Hour
	require.NoError(t, k.SetParams(dropped, p))
	duration, e := k.UnbondingTime(ctx)
	require.NoError(t, e)
	require.Equal(t, 28*24*time.Hour, duration)
	p.MaxValidators = 1
	require.ErrorContains(t, k.SetParams(ctx, p), "limit exceeded")
}

func TestNativePartialWriteFailureLeavesNoState(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	bad, e := NewStateKeeper(failingService{service: service, prefix: consensusPrefix})
	require.NoError(t, e)
	require.ErrorContains(t, bad.InitGenesis(ctx, g), "injected write failure")
	_, e = k.GetParams(ctx)
	require.Error(t, e)
	vals, e := k.GetValidators(ctx)
	require.NoError(t, e)
	require.Empty(t, vals)
	require.NoError(t, k.InitGenesis(ctx, g))
	v := g.Validators[0]
	v.Stake = 999
	require.ErrorContains(t, bad.SetValidator(ctx, v), "injected write failure")
	stored, e := k.GetValidator(ctx, v.OperatorAddress)
	require.NoError(t, e)
	require.EqualValues(t, 100, stored.Stake)
	queueBad, e := NewStateKeeper(failingService{service: service, prefix: delegationPrefix})
	require.NoError(t, e)
	d := g.Delegations[0]
	d.Stake.Stake.Amount = sdkmath.NewInt(120)
	require.ErrorContains(t, queueBad.SetDelegation(ctx, d), "injected write failure")
	export, e := k.ExportGenesis(ctx)
	require.NoError(t, e)
	require.True(t, export.Delegations[0].Stake.Stake.Amount.Equal(sdkmath.NewInt(100)))
}

func TestNativeGenesisQueueAndPreviousPowerFailuresAreAtomic(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	bad, e := NewStateKeeper(failingService{service: service, prefix: undelegationPrefix})
	require.NoError(t, e)
	require.ErrorContains(t, bad.InitGenesis(ctx, g), "injected write failure")
	_, e = k.GetParams(ctx)
	require.Error(t, e)
	vals, e := k.GetValidators(ctx)
	require.NoError(t, e)
	require.Empty(t, vals)
	require.NoError(t, k.InitGenesis(ctx, g))
	bad, e = NewStateKeeper(failingService{service: service, prefix: powerPrefix})
	require.NoError(t, e)
	require.ErrorContains(t, bad.SetLastValidatorPowers(ctx, []types.LastValidatorPower{{Address: g.Validators[0].OperatorAddress, Power: 500}}), "injected write failure")
	export, e := k.ExportGenesis(ctx)
	require.NoError(t, e)
	require.EqualValues(t, 300, export.LastTotalPower)
	require.Len(t, export.LastValidatorPowers, 2)
}

func TestNativeCorruptDynamicRowsAndIndicesFailClosed(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	v := g.Validators[0]
	key, e := operatorKey(dynamicPrefix, v.OperatorAddress)
	require.NoError(t, e)
	require.NoError(t, service.OpenKVStore(ctx).Delete(key))
	_, e = k.GetValidator(ctx, v.OperatorAddress)
	require.ErrorContains(t, e, "dynamic validator row")
	_, e = k.ExportGenesis(ctx)
	require.Error(t, e)
	ctx, service, k = environment(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	cons, e := v.GetConsAddr()
	require.NoError(t, e)
	other := g.Validators[1]
	require.NoError(t, service.OpenKVStore(ctx).Set(append([]byte{consensusPrefix}, cons...), []byte(other.OperatorAddress)))
	_, e = k.GetValidatorByConsAddr(ctx, cons)
	require.ErrorContains(t, e, "index mismatch")
}

func history(t *testing.T, height int64, vals []types.Validator) types.HistoricalInfo {
	t.Helper()
	set, e := ConsensusSet(vals)
	require.NoError(t, e)
	header := cmtproto.Header{ChainID: "mintcandy_300001-1", Height: height, Time: time.Unix(height, 0).UTC(), ValidatorsHash: set.Hash(), NextValidatorsHash: set.Hash()}
	h, e := types.NewHistoricalInfo(header, vals)
	require.NoError(t, e)
	return h
}
func TestNativeHistoryMatchesCometPowerAndPrunesTransactionally(t *testing.T) {
	ctx, _, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	h1, h2, h3 := history(t, 1, g.Validators), history(t, 2, g.Validators), history(t, 3, g.Validators)
	require.NoError(t, k.SetHistoricalInfo(ctx, h1))
	require.NoError(t, k.SetHistoricalInfo(ctx, h2))
	child, commit := ctx.CacheContext()
	require.NoError(t, k.SetHistoricalInfo(child, h3))
	_, e := k.GetHistoricalInfo(ctx, 1)
	require.NoError(t, e)
	_, e = k.GetHistoricalInfo(child, 1)
	require.ErrorIs(t, e, ErrNotFound)
	commit()
	_, e = k.GetHistoricalInfo(ctx, 1)
	require.ErrorIs(t, e, ErrNotFound)
	read, e := k.GetHistoricalInfo(ctx, 3)
	require.NoError(t, e)
	require.Equal(t, h3.Header.ValidatorsHash, read.Header.ValidatorsHash)
	read.Valset[0].ConsensusPubkey.Value[0] ^= 1
	read, e = k.GetHistoricalInfo(ctx, 3)
	require.NoError(t, e)
	require.Equal(t, h3.Valset[0].ConsensusPubkey.Value, read.Valset[0].ConsensusPubkey.Value)
	conflict := h3
	conflict.Header.Time = conflict.Header.Time.Add(time.Second)
	require.ErrorContains(t, k.SetHistoricalInfo(ctx, conflict), "immutable")
	wrong := history(t, 4, g.Validators)
	wrong.Header.ValidatorsHash = []byte{1}
	require.ErrorContains(t, k.SetHistoricalInfo(ctx, wrong), "validators hash")
	require.ErrorContains(t, k.SetHistoricalInfo(ctx, h1), "out of order")
	// Metadata may advance before Comet applies the update. History is supplied
	// from the effective set and must not infer SDK token/share voting power.
	updated := g.Validators[0]
	updated.Stake = 777
	require.NoError(t, k.SetValidator(ctx, updated))
	require.NoError(t, k.SetHistoricalInfo(ctx, history(t, 4, g.Validators)))
}

func TestNativeHistoryPruneFailureRestoresInsertedAndDeletedRecords(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	require.NoError(t, k.SetHistoricalInfo(ctx, history(t, 1, g.Validators)))
	require.NoError(t, k.SetHistoricalInfo(ctx, history(t, 2, g.Validators)))
	bad, e := NewStateKeeper(failingService{service: service, prefix: historyPrefix, deleteOnly: true})
	require.NoError(t, e)
	require.ErrorContains(t, bad.SetHistoricalInfo(ctx, history(t, 3, g.Validators)), "injected delete failure")
	_, e = k.GetHistoricalInfo(ctx, 1)
	require.NoError(t, e)
	_, e = k.GetHistoricalInfo(ctx, 3)
	require.ErrorIs(t, e, ErrNotFound)
}

type failingService struct {
	service    store.KVStoreService
	prefix     byte
	deleteOnly bool
}

func (f failingService) OpenKVStore(ctx context.Context) store.KVStore {
	return failingStore{KVStore: f.service.OpenKVStore(ctx), prefix: f.prefix, deleteOnly: f.deleteOnly}
}

type failingStore struct {
	store.KVStore
	prefix     byte
	deleteOnly bool
}

func (f failingStore) Set(key, value []byte) error {
	if !f.deleteOnly && len(key) > 0 && key[0] == f.prefix {
		return errors.New("injected write failure")
	}
	return f.KVStore.Set(key, value)
}
func (f failingStore) Delete(key []byte) error {
	if f.deleteOnly && len(key) > 0 && key[0] == f.prefix {
		return errors.New("injected delete failure")
	}
	return f.KVStore.Delete(key)
}
