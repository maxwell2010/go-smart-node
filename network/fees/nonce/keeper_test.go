package nonce_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
	"time"

	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/fees/nonce"
	"github.com/stretchr/testify/require"
)

func verified(t *testing.T, n *big.Int) authorization.Verified {
	t.Helper()
	key, err := crypto.HexToECDSA("7b181d51dc41a06e32d4c1aa30c0eeeb3130067d92758b1cb9e6bd48340a77f4")
	require.NoError(t, err)
	to := common.HexToAddress("0x2000000000000000000000000000000000000001")
	tx, err := ethtypes.SignTx(ethtypes.NewTransaction(7, to, new(big.Int), 21000, big.NewInt(100), nil), ethtypes.NewEIP155Signer(big.NewInt(990001)), key)
	require.NoError(t, err)
	p := authorization.Policy{CosmosChainID: "mintcandy_990001-1", EVMChainID: big.NewInt(990001), VerifyingModule: to, MaxConversionGasLimit: 400000, ConversionNativeFee: big.NewInt(1)}
	a := authorization.Authorization{TxHash: tx.Hash(), Payer: crypto.PubkeyToAddress(key.PublicKey), Token: to, ExactInput: big.NewInt(100), MinimumNativeOutput: big.NewInt(2100001), NativeFeeBudget: big.NewInt(2100001), FeeNonce: n, ConversionGasLimit: 300000, Deadline: 1800000300}
	digest, err := authorization.SigningDigest(p, a)
	require.NoError(t, err)
	sig, err := crypto.Sign(digest[:], key)
	require.NoError(t, err)
	v, err := authorization.Verify(p, tx, a, sig, time.Unix(1800000000, 0), storetypes.NewInfiniteGasMeter())
	require.NoError(t, err)
	return v
}
func open(t *testing.T, dir string) (sdk.Context, storetypes.CommitMultiStore, dbm.DB, *nonce.Keeper, corestore.KVStoreService) {
	t.Helper()
	var db dbm.DB
	if dir == "" {
		db = dbm.NewMemDB()
	} else {
		var err error
		db, err = dbm.NewDB("nonce", dbm.GoLevelDBBackend, dir)
		require.NoError(t, err)
	}
	cms := multistore.NewCommitMultiStore(db, log.NewNopLogger(), metrics.NewNoOpMetrics())
	key := storetypes.NewKVStoreKey("fees")
	cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, nil)
	require.NoError(t, cms.LoadLatestVersion())
	ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "mintcandy_990001-1", Height: 1, Time: time.Unix(1800000000, 0)}, false, log.NewNopLogger())
	service := runtime.NewKVStoreService(key)
	k, err := nonce.NewKeeper(service)
	require.NoError(t, err)
	return ctx, cms, db, k, service
}
func TestReplayCacheAtomicityAndDiskRestart(t *testing.T) {
	dir := t.TempDir()
	ctx, cms, db, k, _ := open(t, dir)
	zero, one := verified(t, big.NewInt(0)), verified(t, big.NewInt(1))
	current, err := k.Current(ctx, zero.Payer())
	require.NoError(t, err)
	require.Zero(t, current.Sign())
	current.SetInt64(44)
	current, err = k.Current(ctx, zero.Payer())
	require.NoError(t, err)
	require.Zero(t, current.Sign())
	require.NoError(t, k.Consume(ctx, zero))
	require.ErrorIs(t, k.Consume(ctx, zero), nonce.ErrReplay)
	child, _ := ctx.CacheContext()
	require.NoError(t, k.Consume(child, one))
	current, err = k.Current(ctx, zero.Payer())
	require.NoError(t, err)
	require.Equal(t, "1", current.String(), "outer discarded fee cache must discard nonce advancement")
	cms.Commit()
	require.NoError(t, db.Close())
	ctx, cms, db, k, _ = open(t, dir)
	defer func() { require.NoError(t, db.Close()) }()
	current, err = k.Current(ctx, zero.Payer())
	require.NoError(t, err)
	require.Equal(t, "1", current.String())
	require.ErrorIs(t, k.Consume(ctx, zero), nonce.ErrReplay)
	child, write := ctx.CacheContext()
	require.NoError(t, k.Consume(child, one))
	write()
	current, err = k.Current(ctx, zero.Payer())
	require.NoError(t, err)
	require.Equal(t, "2", current.String())
	cms.Commit()
}
func TestCorruptInvalidAndExhaustedNonceCannotAdvance(t *testing.T) {
	ctx, _, db, k, service := open(t, "")
	defer func() { require.NoError(t, db.Close()) }()
	require.Error(t, k.Consume(ctx, authorization.Verified{}))
	_, err := k.Current(ctx, common.Address{})
	require.Error(t, err)
	v := verified(t, big.NewInt(0))
	path := append([]byte(nonce.Prefix), v.Payer().Bytes()...)
	require.NoError(t, service.OpenKVStore(ctx).Set(path, []byte{1}))
	_, err = k.Current(ctx, v.Payer())
	require.ErrorIs(t, err, nonce.ErrCorrupt)
	require.ErrorIs(t, k.Consume(ctx, v), nonce.ErrCorrupt)
	raw, err := service.OpenKVStore(ctx).Get(path)
	require.NoError(t, err)
	require.Equal(t, []byte{1}, raw)
	max := bytes.Repeat([]byte{0xff}, 32)
	require.NoError(t, service.OpenKVStore(ctx).Set(path, max))
	require.ErrorIs(t, k.Consume(ctx, verified(t, new(big.Int).SetBytes(max))), nonce.ErrExhausted)
	raw, err = service.OpenKVStore(ctx).Get(path)
	require.NoError(t, err)
	require.Equal(t, max, raw)
}

type failStore struct{ corestore.KVStore }

func (failStore) Set([]byte, []byte) error { return errors.New("injected nonce write failure") }

type failService struct{ corestore.KVStoreService }

func (s failService) OpenKVStore(ctx context.Context) corestore.KVStore {
	return failStore{s.KVStoreService.OpenKVStore(ctx)}
}
func TestStoreFailureCannotConsumeNonce(t *testing.T) {
	ctx, _, db, k, service := open(t, "")
	defer func() { require.NoError(t, db.Close()) }()
	fault, err := nonce.NewKeeper(failService{service})
	require.NoError(t, err)
	v := verified(t, big.NewInt(0))
	require.Error(t, fault.Consume(ctx, v))
	current, err := k.Current(ctx, v.Payer())
	require.NoError(t, err)
	require.Zero(t, current.Sign())
	require.NoError(t, k.Consume(ctx, v))
}

// Production injects the SAME validator store into these two keepers. A bare
// "fees/..." prefix starts with 0x66 and contaminates the exclusion iterator,
// even though direct nonce lookups appear to work. Exercise that real iterator
// boundary and its reader, rather than testing string-prefix arithmetic alone.
func TestFeeNonceCannotContaminateSharedValidatorNamespaces(t *testing.T) {
	dir := t.TempDir()
	ctx, cms, db, k, service := open(t, dir)
	consensus := sdk.ConsAddress(bytes.Repeat([]byte{0x23}, 20))
	exclusionKey := append([]byte{0x66}, consensus...)
	exclusionValue := make([]byte, 17)
	exclusionValue[0] = 1
	binary.BigEndian.PutUint64(exclusionValue[1:9], 2)
	binary.BigEndian.PutUint64(exclusionValue[9:17], 5)
	require.NoError(t, service.OpenKVStore(ctx).Set(exclusionKey, exclusionValue))
	// Adjacent deployed namespaces also remain byte-for-byte unchanged.
	manifestKey, membershipKey := []byte{0x6e}, append([]byte{0x70}, bytes.Repeat([]byte{0x24}, 20)...)
	require.NoError(t, service.OpenKVStore(ctx).Set(manifestKey, []byte("existing manifest")))
	require.NoError(t, service.OpenKVStore(ctx).Set(membershipKey, []byte{1}))
	assertNamespaces := func() {
		t.Helper()
		validator, err := validatorkeeper.NewStateKeeperWithDomain(service, "mintcandy_990001-1", 990001)
		require.NoError(t, err)
		excluded, err := validator.GetNativeDuplicateVoteExclusion(ctx, consensus)
		require.NoError(t, err)
		require.Equal(t, validatorkeeper.NativeDuplicateVoteExclusion{InfractionHeight: 2, SettledHeight: 5}, excluded)
		it, err := service.OpenKVStore(ctx).Iterator([]byte{0x66}, []byte{0x67})
		require.NoError(t, err)
		count := 0
		for ; it.Valid(); it.Next() {
			require.Equal(t, exclusionKey, it.Key(), "fee nonce must never enter the duplicate-vote exclusion scan")
			require.Equal(t, exclusionValue, it.Value())
			count++
		}
		require.NoError(t, it.Close())
		require.Equal(t, 1, count)
		for _, entry := range []struct{ key, value []byte }{{manifestKey, []byte("existing manifest")}, {membershipKey, []byte{1}}} {
			value, err := service.OpenKVStore(ctx).Get(entry.key)
			require.NoError(t, err)
			require.Equal(t, entry.value, value)
		}
		it, err = service.OpenKVStore(ctx).Iterator([]byte{0x71}, []byte{0x72})
		require.NoError(t, err)
		count = 0
		for ; it.Valid(); it.Next() {
			require.True(t, bytes.HasPrefix(it.Key(), []byte(nonce.Prefix)))
			count++
		}
		require.NoError(t, it.Close())
		require.Equal(t, 1, count)
	}
	zero := verified(t, big.NewInt(0))
	require.NoError(t, k.Consume(ctx, zero))
	assertNamespaces()
	cms.Commit()
	require.NoError(t, db.Close())
	ctx, cms, db, k, service = open(t, dir)
	defer func() { require.NoError(t, db.Close()) }()
	assertNamespaces()
	n, err := k.Current(ctx, zero.Payer())
	require.NoError(t, err)
	require.Equal(t, "1", n.String())
	require.ErrorIs(t, k.Consume(ctx, zero), nonce.ErrReplay)
	require.NoError(t, k.Consume(ctx, verified(t, big.NewInt(1))))
	assertNamespaces()
	cms.Commit()
}
