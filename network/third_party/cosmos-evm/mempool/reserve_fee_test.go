package mempool

import (
	"context"
	"math/big"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/client"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkmempool "github.com/cosmos/cosmos-sdk/types/mempool"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/stretchr/testify/require"
	protov2 "google.golang.org/protobuf/proto"
)

// A complete-envelope stand-in deliberately has no SDK signatures. It carries
// the EVM signer/nonce and an extra gas allowance, like an app-owned decoder's
// verified reserve-fee wrapper. The pool must retain this object, not rebuild ETH.
type reserveFeePoolTx struct {
	message          *evmtypes.MsgEthereumTx
	marked           bool
	originalEnvelope []byte
}

func (tx *reserveFeePoolTx) GetMsgs() []sdk.Msg                    { return []sdk.Msg{tx.message} }
func (tx *reserveFeePoolTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }
func (tx *reserveFeePoolTx) GetGas() uint64                        { return tx.message.AsTransaction().Gas() + 300000 }
func (*reserveFeePoolTx) GetFee() sdk.Coins                        { return sdk.NewCoins(sdk.NewInt64Coin("del", 100000000)) }
func (tx *reserveFeePoolTx) FeePayer() []byte                      { return tx.message.GetFrom() }
func (*reserveFeePoolTx) FeeGranter() []byte                       { return nil }
func (tx *reserveFeePoolTx) IsNativeReserveFeeTx() bool            { return tx.marked }
func (*reserveFeePoolTx) GetExtensionOptions() []*codectypes.Any {
	return []*codectypes.Any{{TypeUrl: "/cosmos.evm.vm.v1.ExtensionOptionsEthereumTx"}}
}
func (*reserveFeePoolTx) GetNonCriticalExtensionOptions() []*codectypes.Any { return nil }

func reserveFeeFixtureTx() *reserveFeePoolTx {
	msg := evmtypes.NewTx(&evmtypes.EvmTxArgs{ChainID: big.NewInt(990001), Nonce: 12, Amount: new(big.Int), GasLimit: 21000, GasPrice: big.NewInt(100)})
	msg.From = make([]byte, 20)
	msg.From[19] = 1
	return &reserveFeePoolTx{message: msg, marked: true, originalEnvelope: []byte("outer-sdk-envelope-and-separate-fee-consent")}
}
func reserveFeePoolConfig() sdkmempool.PriorityNonceMempoolConfig[math.Int] {
	return withEthereumSignerExtraction(sdkmempool.PriorityNonceMempoolConfig[math.Int]{TxPriority: sdkmempool.TxPriority[math.Int]{
		GetTxPriority: func(context.Context, sdk.Tx) math.Int { return math.OneInt() },
		Compare:       func(a, b math.Int) int { return a.BigInt().Cmp(b.BigInt()) }, MinValue: math.ZeroInt(),
	}})
}

func TestReserveFeeEnvelopeRetainedInSDKLaneAndRemoved(t *testing.T) {
	ctx := sdk.Context{}.WithBlockHeight(1)
	pool := sdkmempool.NewPriorityMempool(reserveFeePoolConfig())
	m := &ExperimentalEVMMempool{cosmosPool: pool, logger: log.NewNopLogger(), blockchain: &Blockchain{latestCtx: ctx}}
	tx := reserveFeeFixtureTx()
	_, err := m.getEVMMessage(tx)
	require.ErrorIs(t, err, ErrNotEVMTransaction)
	// No ETH txpool is present: accidentally stripping the wrapper here would
	// fail instead of being masked by a funded native-balance test fixture.
	require.NoError(t, m.Insert(ctx, tx))
	require.Equal(t, 1, pool.CountTx())
	iterator := NewEVMMempoolIterator(nil, pool.Select(ctx, nil), log.NewNopLogger(), nil, "del", big.NewInt(990001), nil)
	require.NotNil(t, iterator)
	selected := iterator.Tx()
	require.Same(t, tx, selected)
	require.Equal(t, []byte("outer-sdk-envelope-and-separate-fee-consent"), selected.(*reserveFeePoolTx).originalEnvelope)
	require.Equal(t, uint64(321000), selected.(sdk.FeeTx).GetGas())
	require.Nil(t, iterator.Next())
	require.NoError(t, m.Remove(tx))
	require.Zero(t, pool.CountTx())
	// Ordinary native-paid Ethereum txs retain the original raw ETH route.
	tx.marked = false
	message, err := m.getEVMMessage(tx)
	require.NoError(t, err)
	require.Same(t, tx.message, message)
}

type reserveFeeTxConfig struct {
	client.TxConfig
	tx sdk.Tx
}

func (c reserveFeeTxConfig) TxDecoder() sdk.TxDecoder {
	return func([]byte) (sdk.Tx, error) { return c.tx, nil }
}
func TestReserveFeeInvalidNonceNeverQueuesUnwrappedEthereum(t *testing.T) {
	m := &ExperimentalEVMMempool{txConfig: reserveFeeTxConfig{tx: reserveFeeFixtureTx()}}
	require.ErrorIs(t, m.InsertInvalidNonce([]byte("original signed envelope")), ErrNotEVMTransaction)
}

type reserveFeeFallback struct{ used bool }

func (f *reserveFeeFallback) GetSigners(sdk.Tx) ([]sdkmempool.SignerData, error) {
	f.used = true
	return []sdkmempool.SignerData{sdkmempool.NewSignerData(sdk.AccAddress([]byte("cosmos")), 9)}, nil
}
func TestSDKLaneUsesEthereumSignerNonceAndPreservesCosmosFallback(t *testing.T) {
	fallback := &reserveFeeFallback{}
	cfg := reserveFeePoolConfig()
	cfg.SignerExtractor = fallback
	configured := withEthereumSignerExtraction(cfg)
	tx := reserveFeeFixtureTx()
	signers, err := configured.SignerExtractor.GetSigners(tx)
	require.NoError(t, err)
	require.Equal(t, []sdkmempool.SignerData{sdkmempool.NewSignerData(tx.message.GetFrom(), 12)}, signers)
	require.False(t, fallback.used)
	plain := &reserveFeePoolTx{message: tx.message}
	// A plain SDK tx need not expose extension options. Use its sdk.Tx facade.
	wrapped := struct{ sdk.Tx }{plain}
	_, err = configured.SignerExtractor.GetSigners(wrapped)
	require.NoError(t, err)
	require.True(t, fallback.used)
	require.Same(t, fallback, cfg.SignerExtractor, "configuration input must not be mutated")
}
