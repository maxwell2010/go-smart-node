package app

import (
	"crypto/ecdsa"
	"encoding/json"
	"errors"
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
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// This fixture measures ordinary native KV work after real signed EVM execution.
// It is not the production financial receipt processor.
type measuredNativeHook struct {
	app   *App
	title string
	fail  bool
	used  uint64
}

func (h *measuredNativeHook) PostTxProcessing(ctx sdk.Context, _ common.Address, _ core.Message, receipt *ethtypes.Receipt) error {
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return nil
	}
	before := ctx.GasMeter().GasConsumed()
	params, err := h.app.NativeCoinKeeper.GetParams(ctx)
	if err != nil {
		return err
	}
	params.BaseTitle = h.title
	if err := h.app.NativeCoinKeeper.SetParams(ctx, params); err != nil {
		return err
	}
	h.used = ctx.GasMeter().GasConsumed() - before
	ctx.EventManager().EmitEvent(sdk.NewEvent("native_work", sdk.NewAttribute("title", h.title)))
	if h.fail {
		return errors.New("native event processing rejected")
	}
	return nil
}

func checkNativeHookGas(t *testing.T, a *App, key *ecdsa.PrivateKey, vals *cmttypes.ValidatorSet, start time.Time) {
	t.Helper()
	ctx := func() sdk.Context {
		h := a.LastBlockHeight() + 1
		return a.NewUncachedContext(false, cmtproto.Header{ChainID: a.chainID, Height: h, Time: start.Add(time.Duration(h) * time.Second), ProposerAddress: sdk.ConsAddress(vals.Proposer.Address)})
	}
	hook := &measuredNativeHook{app: a, title: "native work charged"}
	a.EVMKeeper.SetHooks(hook)
	from := crypto.PubkeyToAddress(key.PublicKey)
	to := common.HexToAddress("0x1234000000000000000000000000000000004321")
	price := big.NewInt(1_000_000_000_000)
	beforeParams, err := a.NativeCoinKeeper.GetParams(ctx())
	require.NoError(t, err)
	args, err := json.Marshal(map[string]interface{}{"from": from, "to": to, "gasPrice": hexutil.EncodeBig(price), "value": "0x7"})
	require.NoError(t, err)
	estimate, err := a.EVMKeeper.EstimateGas(ctx(), &evmtypes.EthCallRequest{Args: args, GasCap: 500_000, ProposerAddress: sdk.ConsAddress(vals.Proposer.Address)})
	require.NoError(t, err)
	require.Greater(t, estimate.Gas, uint64(21_000), "estimation must include native post-processing work")
	afterEstimate, err := a.NativeCoinKeeper.GetParams(ctx())
	require.NoError(t, err)
	require.Equal(t, beforeParams.BaseTitle, afterEstimate.BaseTitle, "estimation cannot install native changes")
	send := func(gas uint64, success bool) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		beforeFrom := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		beforeTo := a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(to.Bytes()), identity.BaseDenom).Amount
		nonce := a.EVMKeeper.GetNonce(ctx(), from)
		raw := ethtypes.NewTransaction(nonce, to, big.NewInt(7), gas, price, nil)
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
		block, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: start.Add(time.Duration(height) * time.Second), ProposerAddress: vals.Proposer.Address, NextValidatorsHash: vals.Hash(), Txs: [][]byte{encoded}})
		require.NoError(t, err)
		require.Len(t, block.TxResults, 1)
		require.Zero(t, block.TxResults[0].Code, block.TxResults[0].Log)
		var nativeEvents int
		for _, event := range block.TxResults[0].Events {
			if event.Type == "native_work" {
				nativeEvents++
			}
		}
		if success {
			require.Equal(t, 1, nativeEvents)
		} else {
			require.Zero(t, nativeEvents, "reverted native work must not publish success events")
		}
		var data sdk.TxMsgData
		require.NoError(t, a.AppCodec().Unmarshal(block.TxResults[0].Data, &data))
		require.Len(t, data.MsgResponses, 1)
		response := &evmtypes.MsgEthereumTxResponse{}
		require.NoError(t, response.Unmarshal(data.MsgResponses[0].Value))
		if success {
			require.Empty(t, response.VmError)
		} else {
			require.NotEmpty(t, response.VmError)
		}
		_, err = a.Commit()
		require.NoError(t, err)
		fee := sdkmath.NewIntFromBigInt(new(big.Int).Mul(price, new(big.Int).SetUint64(response.GasUsed)))
		debit, credit := fee, sdkmath.ZeroInt()
		if success {
			debit, credit = fee.AddRaw(7), sdkmath.NewInt(7)
		}
		require.True(t, debit.Equal(beforeFrom.Sub(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount)), "sender debit must match receipt fee and committed value")
		require.True(t, credit.Equal(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(to.Bytes()), identity.BaseDenom).Amount.Sub(beforeTo)), "recipient credit must match committed value")
		require.Equal(t, nonce+1, a.EVMKeeper.GetNonce(ctx(), from))
		return response
	}
	accepted := send(estimate.Gas, true)
	require.Positive(t, hook.used)
	require.Equal(t, uint64(21_000)+hook.used, accepted.GasUsed)
	oversized := send(500_000, true)
	require.Equal(t, uint64(250_000), oversized.GasUsed, "successful combined work pays the minimum floor once")
	committed, err := a.NativeCoinKeeper.GetParams(ctx())
	require.NoError(t, err)
	require.Equal(t, hook.title, committed.BaseTitle)
	hook.title, hook.fail = "rejected native work", true
	rejected := send(500_000, false)
	require.Equal(t, uint64(250_000), rejected.GasUsed, "minimum gas multiplier applies to combined work once")
	unchanged, err := a.NativeCoinKeeper.GetParams(ctx())
	require.NoError(t, err)
	require.Equal(t, committed.BaseTitle, unchanged.BaseTitle)
	hook.fail = false
	exhausted := send(21_001, false)
	require.Equal(t, uint64(21_001), exhausted.GasUsed)
	unchanged, err = a.NativeCoinKeeper.GetParams(ctx())
	require.NoError(t, err)
	require.Equal(t, committed.BaseTitle, unchanged.BaseTitle)
}
