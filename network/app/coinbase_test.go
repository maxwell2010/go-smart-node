package app

import (
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// Execute COINBASE through signed transactions and real FinalizeBlock/Commit.
// Expected bytes come from the actual validator's explicit address codec.
func checkCoinbaseOpcode(t *testing.T, a *App, key *ecdsa.PrivateKey, vals *cmttypes.ValidatorSet, start time.Time) {
	t.Helper()
	from := crypto.PubkeyToAddress(key.PublicKey)
	price := big.NewInt(1_000_000_000_000)
	context := func() sdk.Context {
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: a.chainID, Height: a.LastBlockHeight() + 1, Time: start.Add(time.Duration(a.LastBlockHeight()+1) * time.Second)})
	}
	validator, err := a.StakingKeeper.GetValidatorByConsAddr(context(), sdk.ConsAddress(vals.Proposer.Address))
	require.NoError(t, err)
	operator, err := a.StakingKeeper.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	require.NoError(t, err)
	require.Len(t, operator, common.AddressLength)
	expected := common.BytesToAddress(operator)
	send := func(to *common.Address, input []byte) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		before := a.BankKeeper.GetBalance(context(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		nonce := a.EVMKeeper.GetNonce(context(), from)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: to, Gas: 300_000, GasPrice: price, Value: new(big.Int), Data: input})
		signer := ethtypes.NewEIP155Signer(new(big.Int).SetUint64(a.evmChainID))
		signed, err := ethtypes.SignTx(raw, signer, key)
		require.NoError(t, err)
		msg := &evmtypes.MsgEthereumTx{}
		require.NoError(t, msg.FromSignedEthereumTx(signed, signer))
		tx, err := msg.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, err)
		encoded, err := a.TxConfig().TxEncoder()(tx)
		require.NoError(t, err)
		height := a.LastBlockHeight() + 1
		block, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: start.Add(time.Duration(height) * time.Second), Txs: [][]byte{encoded}, NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address})
		require.NoError(t, err)
		require.Len(t, block.TxResults, 1)
		require.Zero(t, block.TxResults[0].Code, block.TxResults[0].Log)
		var result sdk.TxMsgData
		require.NoError(t, a.AppCodec().Unmarshal(block.TxResults[0].Data, &result))
		require.Len(t, result.MsgResponses, 1)
		response := &evmtypes.MsgEthereumTxResponse{}
		require.NoError(t, response.Unmarshal(result.MsgResponses[0].Value))
		require.Empty(t, response.VmError)
		require.Positive(t, response.GasUsed)
		_, err = a.Commit()
		require.NoError(t, err)
		fee := sdkmath.NewIntFromBigInt(new(big.Int).Mul(price, new(big.Int).SetUint64(response.GasUsed)))
		require.Equal(t, fee, before.Sub(a.BankKeeper.GetBalance(context(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount))
		require.Equal(t, nonce+1, a.EVMKeeper.GetNonce(context(), from))
		return response
	}
	// Runtime: COINBASE; PUSH1 0; MSTORE; PUSH1 32; PUSH1 0; RETURN.
	// The twelve-byte init prefix installs that nine-byte executable unchanged.
	creation, err := hex.DecodeString("6009600c60003960096000f34160005260206000f3")
	require.NoError(t, err)
	contract := crypto.CreateAddress(from, a.EVMKeeper.GetNonce(context(), from))
	send(nil, creation)
	response := send(&contract, nil)
	require.Len(t, response.Ret, 32)
	actual := common.BytesToAddress(response.Ret)
	t.Logf("COINBASE=%s validator operator=%s", actual, expected)
	require.Equal(t, expected, actual, "EVM COINBASE must decode validator Bech32 rather than truncate its text bytes")
}
