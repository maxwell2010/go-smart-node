package ledger

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"cosmossdk.io/core/store"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codecaddress "github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

type ledgerFixture struct {
	ctx     sdk.Context
	cms     storetypes.CommitMultiStore
	db      dbm.DB
	bank    bankkeeper.BaseKeeper
	service store.KVStoreService
	ledger  *Keeper
}

func testAddress(value byte) sdk.AccAddress {
	address := make(sdk.AccAddress, 20)
	address[19] = value
	return address
}

func testID(value byte) [32]byte { var id [32]byte; id[31] = value; return id }

func openLedgerFixture(t *testing.T, directory string) ledgerFixture {
	t.Helper()
	var database dbm.DB
	if directory == "" {
		database = dbm.NewMemDB()
	} else {
		var err error
		database, err = dbm.NewDB("bridge-ledger", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
	}
	cms := multistore.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
	authKey, bankKey, bridgeKey := storetypes.NewKVStoreKey("auth"), storetypes.NewKVStoreKey("bank"), storetypes.NewKVStoreKey("bridge")
	for _, key := range []*storetypes.KVStoreKey{authKey, bankKey, bridgeKey} {
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
	}
	require.NoError(t, cms.LoadLatestVersion())
	ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "mintcandy_990012-1", Height: 1}, false, log.NewNopLogger())
	interfaces := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaces)
	cdc := codec.NewProtoCodec(interfaces)
	prefix := sdk.GetConfig().GetBech32AccountAddrPrefix()
	addressCodec := codecaddress.NewBech32Codec(prefix)
	authority, err := addressCodec.BytesToString(testAddress(90))
	require.NoError(t, err)
	ak := authkeeper.NewAccountKeeper(cdc, runtime.NewKVStoreService(authKey), authtypes.ProtoBaseAccount,
		map[string][]string{ModuleName: {authtypes.Minter}, "funding": {authtypes.Minter}}, addressCodec, prefix, authority)
	bk := bankkeeper.NewBaseKeeper(cdc, runtime.NewKVStoreService(bankKey), ak, map[string]bool{}, authority, log.NewNopLogger())
	if cms.LastCommitID().Version == 0 {
		require.NoError(t, ak.Params.Set(ctx, authtypes.DefaultParams()))
		require.NoError(t, bk.SetParams(ctx, banktypes.DefaultParams()))
		// GetModuleAccount allocates unique account numbers through real auth.
		require.NotNil(t, ak.GetModuleAccount(ctx, ModuleName))
		require.NotNil(t, ak.GetModuleAccount(ctx, "funding"))
		// Independent fixture allocation: ledger itself never mints native del.
		seed := sdk.NewCoins(sdk.NewInt64Coin("del", 100))
		require.NoError(t, bk.MintCoins(ctx, "funding", seed))
		require.NoError(t, bk.SendCoinsFromModuleToAccount(ctx, "funding", testAddress(1), seed))
	}
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	service := runtime.NewKVStoreService(bridgeKey)
	k, err := NewKeeper(service, bk)
	require.NoError(t, err)
	return ledgerFixture{ctx: ctx, cms: cms, db: database, bank: bk, service: service, ledger: k}
}

func assertLedgerBalances(t *testing.T, f ledgerFixture, recipient sdk.AccAddress, voucher int64) {
	t.Helper()
	require.Equal(t, sdkmath.NewInt(voucher), f.bank.GetBalance(f.ctx, recipient, VoucherDenom).Amount)
	require.Equal(t, sdkmath.NewInt(voucher), f.bank.GetSupply(f.ctx, VoucherDenom).Amount)
	require.True(t, f.bank.GetBalance(f.ctx, authtypes.NewModuleAddress(ModuleName), VoucherDenom).IsZero())
	require.Equal(t, sdkmath.NewInt(100), f.bank.GetSupply(f.ctx, "del").Amount)
	require.Equal(t, sdkmath.NewInt(100), f.bank.GetBalance(f.ctx, testAddress(1), "del").Amount)
}

func TestVerifiedDepositUsesDistinctVoucherAndDurableExactPayload(t *testing.T) {
	f := openLedgerFixture(t, "")
	defer f.db.Close()
	id, recipient, amount := testID(1), testAddress(1), sdkmath.NewInt(25)
	require.NoError(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, recipient, amount))
	assertLedgerBalances(t, f, recipient, 25)
	record, err := f.ledger.GetDeposit(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, id, record.TransferID)
	require.Equal(t, OriginChainID, record.OriginChainID)
	require.Equal(t, "del", record.OriginDenom)
	require.Equal(t, "bridge/decimal/del", record.VoucherDenom)
	require.Equal(t, recipient, record.Recipient)
	require.Equal(t, amount, record.Amount)
	seen, err := f.ledger.HasDeposit(f.ctx, id)
	require.NoError(t, err)
	require.True(t, seen)
	events := len(f.ctx.EventManager().Events())
	require.ErrorIs(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, recipient, amount), ErrAlreadyProcessed)
	require.ErrorIs(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, testAddress(2), sdkmath.NewInt(999)), ErrAlreadyProcessed)
	assertLedgerBalances(t, f, recipient, 25)
	require.Len(t, f.ctx.EventManager().Events(), events)
	var typed []sdk.Event
	for _, event := range f.ctx.EventManager().Events() {
		if event.Type == "mintcandy.bridge.v1.EventVerifiedDeposit" {
			typed = append(typed, event)
		}
	}
	require.Len(t, typed, 1)
	record.Recipient[0] = 9
	record.Amount.BigInt().SetInt64(7)
	again, err := f.ledger.GetDeposit(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, recipient, again.Recipient)
	require.Equal(t, amount, again.Amount)
}

type bankFault struct {
	BankKeeper
	stage string
}

func (bank bankFault) MintCoins(ctx context.Context, module string, coins sdk.Coins) error {
	if err := bank.BankKeeper.MintCoins(ctx, module, coins); err != nil {
		return err
	}
	if bank.stage == "after-mint" {
		return errors.New("injected after real mint")
	}
	return nil
}
func (bank bankFault) SendCoinsFromModuleToAccount(ctx context.Context, module string, recipient sdk.AccAddress, coins sdk.Coins) error {
	if err := bank.BankKeeper.SendCoinsFromModuleToAccount(ctx, module, recipient, coins); err != nil {
		return err
	}
	if bank.stage == "after-send" {
		return errors.New("injected after real send")
	}
	return nil
}

type storeFault struct {
	store.KVStoreService
	stage string
}

func (service storeFault) OpenKVStore(ctx context.Context) store.KVStore {
	return faultStore{KVStore: service.KVStoreService.OpenKVStore(ctx), stage: service.stage}
}

type faultStore struct {
	store.KVStore
	stage string
}

func (state faultStore) Has(key []byte) (bool, error) {
	if state.stage == "read" {
		return false, errors.New("injected replay read failure")
	}
	return state.KVStore.Has(key)
}
func (state faultStore) Set(key, value []byte) error {
	if err := state.KVStore.Set(key, value); err != nil {
		return err
	}
	if state.stage == "after-set" {
		return errors.New("injected after real marker write")
	}
	return nil
}

func TestBankStoreAndTypedEventFailuresRollbackRealSDKAccounting(t *testing.T) {
	for _, stage := range []string{"after-mint", "after-send", "read", "after-set", "event"} {
		t.Run(stage, func(t *testing.T) {
			f := openLedgerFixture(t, "")
			defer f.db.Close()
			service, bank := f.service, BankKeeper(bankFault{BankKeeper: f.bank, stage: stage})
			if stage == "read" || stage == "after-set" {
				service = storeFault{KVStoreService: service, stage: stage}
			}
			broken, err := NewKeeper(service, bank)
			require.NoError(t, err)
			if stage == "event" {
				broken.emit = func(child sdk.Context, event *EventVerifiedDeposit) error {
					require.NoError(t, child.EventManager().EmitTypedEvent(event))
					return errors.New("injected after typed event")
				}
			}
			id, recipient := testID(2), testAddress(1)
			require.Error(t, broken.ExecuteVerifiedDeposit(f.ctx, id, recipient, sdkmath.NewInt(20)))
			assertLedgerBalances(t, f, recipient, 0)
			require.Empty(t, f.ctx.EventManager().Events())
			_, err = f.ledger.GetDeposit(f.ctx, id)
			require.ErrorIs(t, err, ErrNotFound)
			require.NoError(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, recipient, sdkmath.NewInt(20)))
			require.ErrorIs(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, recipient, sdkmath.NewInt(20)), ErrAlreadyProcessed)
			assertLedgerBalances(t, f, recipient, 20)
		})
	}
}

func TestOuterSDKRollbackAlsoDiscardsSuccessfulDeposit(t *testing.T) {
	f := openLedgerFixture(t, "")
	defer f.db.Close()
	id, recipient := testID(3), testAddress(1)
	outer, _ := f.ctx.CacheContext()
	require.NoError(t, f.ledger.ExecuteVerifiedDeposit(outer, id, recipient, sdkmath.NewInt(20)))
	_, err := f.ledger.GetDeposit(outer, id)
	require.NoError(t, err)
	assertLedgerBalances(t, f, recipient, 0)
	_, err = f.ledger.GetDeposit(f.ctx, id)
	require.ErrorIs(t, err, ErrNotFound)
	require.Empty(t, f.ctx.EventManager().Events())
	require.NoError(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, recipient, sdkmath.NewInt(20)))
	assertLedgerBalances(t, f, recipient, 20)
}

type reentrantBank struct {
	BankKeeper
	keeper      *Keeper
	id          [32]byte
	recipient   sdk.AccAddress
	amount      sdkmath.Int
	nestedError error
	mintCalls   int
}

func (bank *reentrantBank) MintCoins(ctx context.Context, module string, coins sdk.Coins) error {
	bank.mintCalls++
	if bank.mintCalls > 1 {
		return errors.New("reentrant deposit reached a second mint")
	}
	bank.nestedError = bank.keeper.ExecuteVerifiedDeposit(sdk.UnwrapSDKContext(ctx), bank.id, bank.recipient, bank.amount)
	return bank.BankKeeper.MintCoins(ctx, module, coins)
}

func TestCachedReplayReservationRejectsReentrantBankMint(t *testing.T) {
	f := openLedgerFixture(t, "")
	defer f.db.Close()
	bank := &reentrantBank{BankKeeper: f.bank, id: testID(8), recipient: testAddress(1), amount: sdkmath.NewInt(25)}
	k, err := NewKeeper(f.service, bank)
	require.NoError(t, err)
	bank.keeper = k
	require.NoError(t, k.ExecuteVerifiedDeposit(f.ctx, bank.id, bank.recipient, bank.amount))
	require.ErrorIs(t, bank.nestedError, ErrAlreadyProcessed)
	require.Equal(t, 1, bank.mintCalls)
	assertLedgerBalances(t, f, bank.recipient, 25)
	require.ErrorIs(t, k.ExecuteVerifiedDeposit(f.ctx, bank.id, bank.recipient, bank.amount), ErrAlreadyProcessed)
}

func TestFailedDepositRetryAndReplayProtectionSurviveDiskReopen(t *testing.T) {
	directory := t.TempDir()
	f := openLedgerFixture(t, directory)
	id, recipient := testID(4), testAddress(1)
	broken, err := NewKeeper(f.service, bankFault{BankKeeper: f.bank, stage: "after-send"})
	require.NoError(t, err)
	require.Error(t, broken.ExecuteVerifiedDeposit(f.ctx, id, recipient, sdkmath.NewInt(20)))
	f.cms.Commit()
	require.NoError(t, f.db.Close())
	f = openLedgerFixture(t, directory)
	assertLedgerBalances(t, f, recipient, 0)
	_, err = f.ledger.GetDeposit(f.ctx, id)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, recipient, sdkmath.NewInt(20)))
	f.cms.Commit()
	require.NoError(t, f.db.Close())
	f = openLedgerFixture(t, directory)
	defer f.db.Close()
	assertLedgerBalances(t, f, recipient, 20)
	record, err := f.ledger.GetDeposit(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(20), record.Amount)
	require.ErrorIs(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, id, testAddress(2), sdkmath.NewInt(50)), ErrAlreadyProcessed)
	assertLedgerBalances(t, f, recipient, 20)
}

func TestValidationAndSupplyOverflowDoNotMintOrPanic(t *testing.T) {
	f := openLedgerFixture(t, "")
	defer f.db.Close()
	for _, bad := range []struct {
		id      [32]byte
		address sdk.AccAddress
		amount  sdkmath.Int
	}{
		{[32]byte{}, testAddress(1), sdkmath.OneInt()},
		{testID(1), nil, sdkmath.OneInt()},
		{testID(1), make(sdk.AccAddress, 20), sdkmath.OneInt()},
		{testID(1), make(sdk.AccAddress, 21), sdkmath.OneInt()},
		{testID(1), testAddress(1), sdkmath.Int{}},
		{testID(1), testAddress(1), sdkmath.ZeroInt()},
		{testID(1), testAddress(1), sdkmath.NewInt(-1)},
	} {
		require.Error(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, bad.id, bad.address, bad.amount))
	}
	assertLedgerBalances(t, f, testAddress(1), 0)
	require.Empty(t, f.ctx.EventManager().Events())
	maximum := sdkmath.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	require.NoError(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, testID(10), testAddress(1), maximum))
	events := len(f.ctx.EventManager().Events())
	require.NotPanics(t, func() {
		require.ErrorContains(t, f.ledger.ExecuteVerifiedDeposit(f.ctx, testID(11), testAddress(1), sdkmath.OneInt()), "exceeds uint256")
	})
	require.Equal(t, maximum, f.bank.GetSupply(f.ctx, VoucherDenom).Amount)
	require.Equal(t, maximum, f.bank.GetBalance(f.ctx, testAddress(1), VoucherDenom).Amount)
	seen, err := f.ledger.HasDeposit(f.ctx, testID(11))
	require.NoError(t, err)
	require.False(t, seen)
	require.Len(t, f.ctx.EventManager().Events(), events)
	require.Equal(t, sdkmath.NewInt(100), f.bank.GetSupply(f.ctx, "del").Amount)
}
