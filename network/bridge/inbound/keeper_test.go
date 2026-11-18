package inbound

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	"github.com/cometbft/cometbft/crypto/tmhash"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
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

	"github.com/maxwell2010/go-smart-node/network/bridge/client"
	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
	"github.com/maxwell2010/go-smart-node/network/bridge/ledger"
	"github.com/maxwell2010/go-smart-node/network/bridge/protocol"
)

// These are isolated SDK stores and disposable validator keys. The Decimal-
// shaped chain ID exercises the fixed route, not a public mainnet connection.
type environment struct {
	ctx        sdk.Context
	cms        storetypes.CommitMultiStore
	db         dbm.DB
	bank       bankkeeper.BaseKeeper
	clients    *client.Keeper
	ledger     *ledger.Keeper
	inbound    *Keeper
	serviceKey *storetypes.KVStoreKey
}

func testAddress(last byte) sdk.AccAddress {
	out := make(sdk.AccAddress, 20)
	out[19] = last
	return out
}

func openEnvironment(t *testing.T, directory string) environment {
	t.Helper()
	var database dbm.DB
	if directory == "" {
		database = dbm.NewMemDB()
	} else {
		var err error
		database, err = dbm.NewDB("bridge-inbound", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
	}
	cms := multistore.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
	authKey, bankKey, bridgeKey := storetypes.NewKVStoreKey("auth"), storetypes.NewKVStoreKey("bank"), storetypes.NewKVStoreKey("bridge")
	for _, key := range []*storetypes.KVStoreKey{authKey, bankKey, bridgeKey} {
		cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
	}
	require.NoError(t, cms.LoadLatestVersion())
	now := time.Date(2026, 11, 18, 0, 2, 0, 0, time.UTC)
	ctx := sdk.NewContext(cms, cmtproto.Header{ChainID: "mintcandy_990012-1", Height: 1, Time: now}, false, log.NewNopLogger())
	interfaces := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaces)
	cdc := codec.NewProtoCodec(interfaces)
	prefix := sdk.GetConfig().GetBech32AccountAddrPrefix()
	addressCodec := codecaddress.NewBech32Codec(prefix)
	authority, err := addressCodec.BytesToString(testAddress(90))
	require.NoError(t, err)
	ak := authkeeper.NewAccountKeeper(cdc, runtime.NewKVStoreService(authKey), authtypes.ProtoBaseAccount,
		map[string][]string{ledger.ModuleName: {authtypes.Minter}}, addressCodec, prefix, authority)
	bk := bankkeeper.NewBaseKeeper(cdc, runtime.NewKVStoreService(bankKey), ak, map[string]bool{}, authority, log.NewNopLogger())
	if cms.LastCommitID().Version == 0 {
		require.NoError(t, ak.Params.Set(ctx, authtypes.DefaultParams()))
		require.NoError(t, bk.SetParams(ctx, banktypes.DefaultParams()))
		require.NotNil(t, ak.GetModuleAccount(ctx, ledger.ModuleName))
	}
	service := runtime.NewKVStoreService(bridgeKey)
	clients, err := client.NewKeeper(service)
	require.NoError(t, err)
	deposits, err := ledger.NewKeeper(service, bk)
	require.NoError(t, err)
	inbound, err := NewKeeper(service, clients, deposits)
	require.NoError(t, err)
	return environment{ctx, cms, database, bk, clients, deposits, inbound, bridgeKey}
}

type sourceFixture struct {
	deposit            protocol.Deposit
	anchor             finality.Checkpoint
	trusted, candidate *cmt.LightBlock
	policy             finality.Policy
	proof              *cmtcrypto.ProofOps
}

func sourceProof(t *testing.T) sourceFixture {
	t.Helper()
	database := dbm.NewMemDB()
	defer database.Close()
	cms := multistore.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
	key := storetypes.NewKVStoreKey("evm")
	cms.MountStoreWithDB(key, storetypes.StoreTypeIAVL, database)
	require.NoError(t, cms.LoadLatestVersion())
	require.NoError(t, cms.SetInitialVersion(10))
	deposit := protocol.Deposit{SourceEVMChainID: 75, DestinationEVMChainID: 990012}
	deposit.SourceEscrow[19], deposit.DestinationBridgeID[31] = 21, 22
	deposit.Sender[19], deposit.Recipient[19], deposit.Amount[31] = 23, 24, 25
	id, err := protocol.OperationID(deposit)
	require.NoError(t, err)
	slot := protocol.CommitmentSlot(id)
	stateKey := append([]byte{2}, deposit.SourceEscrow[:]...)
	stateKey = append(stateKey, slot[:]...)
	cms.GetKVStore(key).Set(stateKey, id[:])
	commit := cms.Commit()
	require.EqualValues(t, 10, commit.Version)
	cms.Commit()
	query, err := cms.(storetypes.Queryable).Query(&storetypes.RequestQuery{Path: "/evm/key", Data: stateKey, Height: 10, Prove: true})
	require.NoError(t, err)
	require.EqualValues(t, 0, query.Code)
	require.Equal(t, id[:], query.Value)
	require.Len(t, query.ProofOps.Ops, 2)
	privateKeys := make(map[string]ed25519.PrivKey)
	rows := make([]*cmt.Validator, 4)
	for i := range rows {
		private := ed25519.GenPrivKeyFromSecret([]byte(fmt.Sprintf("disposable-inbound-source-%d", i)))
		privateKeys[string(private.PubKey().Address())] = private
		rows[i] = cmt.NewValidator(private.PubKey(), 1)
	}
	validators := cmt.NewValidatorSet(rows)
	base := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	makeBlock := func(height int64, stamp time.Time, previous cmt.BlockID, appHash []byte) *cmt.LightBlock {
		block := &cmt.LightBlock{SignedHeader: &cmt.SignedHeader{Header: &cmt.Header{
			Version: cmtversion.Consensus{Block: version.BlockProtocol}, ChainID: ledger.OriginChainID,
			Height: height, Time: stamp, LastBlockID: previous,
			LastCommitHash: tmhash.Sum([]byte("last")), DataHash: tmhash.Sum([]byte("data")),
			ValidatorsHash: validators.Hash(), NextValidatorsHash: validators.Hash(), ConsensusHash: tmhash.Sum([]byte("params")),
			AppHash: appHash, LastResultsHash: tmhash.Sum([]byte("results")), EvidenceHash: tmhash.Sum([]byte("evidence")),
			ProposerAddress: validators.Proposer.Address}}, ValidatorSet: validators.Copy()}
		block.Commit = &cmt.Commit{Height: height, Round: 0, BlockID: cmt.BlockID{Hash: block.Header.Hash(),
			PartSetHeader: cmt.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte(fmt.Sprint("parts", height)))}}}
		for _, row := range block.ValidatorSet.Validators {
			vote := cmtproto.Vote{Type: cmtproto.PrecommitType, Height: height, Round: 0,
				BlockID: block.Commit.BlockID.ToProto(), Timestamp: stamp}
			signature, err := privateKeys[string(row.Address)].Sign(cmt.VoteSignBytes(ledger.OriginChainID, &vote))
			require.NoError(t, err)
			block.Commit.Signatures = append(block.Commit.Signatures, cmt.CommitSig{BlockIDFlag: cmt.BlockIDFlagCommit,
				ValidatorAddress: row.Address, Timestamp: stamp, Signature: signature})
		}
		return block
	}
	previous := cmt.BlockID{Hash: tmhash.Sum([]byte("block9")), PartSetHeader: cmt.PartSetHeader{Total: 1, Hash: tmhash.Sum([]byte("parts9"))}}
	trusted := makeBlock(10, base, previous, tmhash.Sum([]byte("state9")))
	candidate := makeBlock(11, base.Add(time.Minute), trusted.Commit.BlockID, commit.Hash)
	anchor := finality.Checkpoint{ChainID: ledger.OriginChainID, Height: 10}
	copy(anchor.Hash[:], trusted.Hash())
	policy := finality.Policy{ChainID: ledger.OriginChainID, TrustingPeriod: time.Hour,
		UnbondingPeriod: 7 * 24 * time.Hour, MaxClockDrift: 30 * time.Second, MaxValidators: 16}
	return sourceFixture{deposit, anchor, trusted, candidate, policy, query.ProofOps}
}

func initFixture(t *testing.T, env environment, source sourceFixture) {
	t.Helper()
	require.NoError(t, env.clients.InitClient(env.ctx, "decimal-mainnet", source.anchor, source.trusted, source.policy))
	route := Route{ClientID: "decimal-mainnet", SourceChainID: ledger.OriginChainID, SourceEVMChainID: 75,
		SourceEscrow: source.deposit.SourceEscrow, DestinationChainID: env.ctx.ChainID(), DestinationEVMChainID: 990012,
		DestinationBridgeID: source.deposit.DestinationBridgeID, MinimumStateHeight: 10, MaxHeaderAge: 5 * time.Minute}
	require.NoError(t, env.inbound.InitRoute(env.ctx, "decimal-del", route))
}

func assertMint(t *testing.T, env environment, source sourceFixture, expected int64) {
	t.Helper()
	recipient := sdk.AccAddress(source.deposit.Recipient[:])
	require.Equal(t, sdkmath.NewInt(expected), env.bank.GetBalance(env.ctx, recipient, ledger.VoucherDenom).Amount)
	require.Equal(t, sdkmath.NewInt(expected), env.bank.GetSupply(env.ctx, ledger.VoucherDenom).Amount)
	require.True(t, env.bank.GetSupply(env.ctx, "del").IsZero())
	id, err := protocol.OperationID(source.deposit)
	require.NoError(t, err)
	seen, err := env.ledger.HasDeposit(env.ctx, id)
	require.NoError(t, err)
	require.Equal(t, expected != 0, seen)
}

func TestStoredClientProofAndRealBankMintOnce(t *testing.T) {
	env, source := openEnvironment(t, ""), sourceProof(t)
	defer env.db.Close()
	initFixture(t, env, source)
	require.NoError(t, env.inbound.ReceiveWithUpdate(env.ctx, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	assertMint(t, env, source, 25)
	checkpoint, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
	require.NoError(t, err)
	require.EqualValues(t, 11, checkpoint.Height)
	require.ErrorIs(t, env.inbound.Receive(env.ctx, "decimal-del", source.deposit, 10, source.proof), ledger.ErrAlreadyProcessed)
	assertMint(t, env, source, 25)
}

func TestInvalidProofRollsBackHeaderBankAndReplay(t *testing.T) {
	env, source := openEnvironment(t, ""), sourceProof(t)
	defer env.db.Close()
	initFixture(t, env, source)
	source.proof.Ops[0].Data[0] ^= 1
	require.Error(t, env.inbound.ReceiveWithUpdate(env.ctx, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	checkpoint, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
	require.NoError(t, err)
	require.EqualValues(t, 10, checkpoint.Height)
	assertMint(t, env, source, 0)
	source.proof.Ops[0].Data[0] ^= 1
	require.NoError(t, env.inbound.ReceiveWithUpdate(env.ctx, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	assertMint(t, env, source, 25)
}

type failAfterBankSend struct{ ledger.BankKeeper }

func (bank failAfterBankSend) SendCoinsFromModuleToAccount(ctx context.Context, module string, recipient sdk.AccAddress, coins sdk.Coins) error {
	if err := bank.BankKeeper.SendCoinsFromModuleToAccount(ctx, module, recipient, coins); err != nil {
		return err
	}
	return errors.New("injected bank payout failure")
}

func TestPayoutFailureAndOuterRollbackCannotAdvanceClient(t *testing.T) {
	env, source := openEnvironment(t, ""), sourceProof(t)
	defer env.db.Close()
	initFixture(t, env, source)
	failedLedger, err := ledger.NewKeeper(runtime.NewKVStoreService(env.serviceKey), failAfterBankSend{env.bank})
	require.NoError(t, err)
	failed, err := NewKeeper(runtime.NewKVStoreService(env.serviceKey), env.clients, failedLedger)
	require.NoError(t, err)
	beforeEvents := len(env.ctx.EventManager().Events())
	require.Error(t, failed.ReceiveWithUpdate(env.ctx, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	checkpoint, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
	require.NoError(t, err)
	require.EqualValues(t, 10, checkpoint.Height)
	require.Len(t, env.ctx.EventManager().Events(), beforeEvents)
	assertMint(t, env, source, 0)
	outer, _ := env.ctx.CacheContext()
	require.NoError(t, env.inbound.ReceiveWithUpdate(outer, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	assertMint(t, env, source, 0)
	checkpoint, err = env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
	require.NoError(t, err)
	require.EqualValues(t, 10, checkpoint.Height)
	require.NoError(t, env.inbound.ReceiveWithUpdate(env.ctx, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	assertMint(t, env, source, 25)
}

func TestRouteAndClaimDomainsArePinned(t *testing.T) {
	for _, name := range []string{"source-evm", "destination-evm", "escrow", "bridge", "recipient", "amount", "sender", "nonce", "height", "local-chain"} {
		t.Run(name, func(t *testing.T) {
			env, source := openEnvironment(t, ""), sourceProof(t)
			defer env.db.Close()
			initFixture(t, env, source)
			deposit, height, ctx := source.deposit, int64(10), env.ctx
			switch name {
			case "source-evm":
				deposit.SourceEVMChainID++
			case "destination-evm":
				deposit.DestinationEVMChainID++
			case "escrow":
				deposit.SourceEscrow[0] ^= 1
			case "bridge":
				deposit.DestinationBridgeID[0] ^= 1
			case "recipient":
				deposit.Recipient[0] ^= 1
			case "amount":
				deposit.Amount[31]++
			case "sender":
				deposit.Sender[0] ^= 1
			case "nonce":
				deposit.Nonce[31]++
			case "height":
				height--
			case "local-chain":
				ctx = ctx.WithChainID("mintcandy_990013-1")
			}
			require.Error(t, env.inbound.ReceiveWithUpdate(ctx, "decimal-del", source.candidate, deposit, height, source.proof))
			assertMint(t, env, source, 0)
			cp, err := env.clients.LatestCheckpoint(env.ctx, "decimal-mainnet")
			require.NoError(t, err)
			require.EqualValues(t, 10, cp.Height)
		})
	}
}

func TestStaleSourceBlocksExecutionWithinTrustingPeriod(t *testing.T) {
	env, source := openEnvironment(t, ""), sourceProof(t)
	defer env.db.Close()
	initFixture(t, env, source)
	require.NoError(t, func() error { _, err := env.clients.Update(env.ctx, "decimal-mainnet", source.candidate); return err }())
	stale := env.ctx.WithBlockTime(env.ctx.BlockTime().Add(10 * time.Minute))
	require.ErrorContains(t, env.inbound.Receive(stale, "decimal-del", source.deposit, 10, source.proof), "stale")
	assertMint(t, env, source, 0)
}

func TestPersistentReplayAndRoutesSurviveLevelDBRestart(t *testing.T) {
	directory := t.TempDir()
	env, source := openEnvironment(t, directory), sourceProof(t)
	initFixture(t, env, source)
	require.NoError(t, env.inbound.ReceiveWithUpdate(env.ctx, "decimal-del", source.candidate, source.deposit, 10, source.proof))
	env.cms.Commit()
	require.NoError(t, env.db.Close())
	env = openEnvironment(t, directory)
	defer env.db.Close()
	assertMint(t, env, source, 25)
	require.ErrorIs(t, env.inbound.Receive(env.ctx, "decimal-del", source.deposit, 10, source.proof), ledger.ErrAlreadyProcessed)
	route, err := env.inbound.GetRoute(env.ctx, "decimal-del")
	require.NoError(t, err)
	require.ErrorIs(t, env.inbound.InitRoute(env.ctx, "decimal-del", route), ErrRouteExists)
	assertMint(t, env, source, 25)
}
