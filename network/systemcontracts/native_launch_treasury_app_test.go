package systemcontracts_test

import (
	"encoding/hex"
	"os"
	"path/filepath"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	keymultisig "github.com/cosmos/cosmos-sdk/crypto/keys/multisig"
	"github.com/cosmos/cosmos-sdk/crypto/types/multisig"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const nativeLaunchTreasuryProcessEnv = "MCND_NATIVE_TREASURY_PROCESS"

type nativeLaunchTreasuryFixture struct {
	Treasury        string `json:"treasury"`
	Recipient       string `json:"recipient"`
	FeeCollector    string `json:"feeCollector"`
	InitialSequence uint64 `json:"initialSequence"`
	AmountAtomic    string `json:"amountAtomic"`
	FeeAtomic       string `json:"feeAtomic"`
	InvalidTx       string `json:"invalidTx"`
	ValidTx         string `json:"validTx"`
}

func verifyNativeLaunchTreasurySpend(l nativeHistoricalLifecycle, dir string, key *keymultisig.LegacyAminoPubKey) {
	t := l.t
	t.Helper()
	denom := identity.BaseDenom
	treasury := sdk.AccAddress(key.Address())
	recipient := sdk.AccAddress(l.expect.Actor.Bytes())
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	ctx := l.snapshot()
	account := l.app.AccountKeeper.GetAccount(ctx, treasury)
	require.NotNil(t, account)
	sequence := account.GetSequence()
	treasuryBefore := l.app.BankKeeper.GetBalance(ctx, treasury, denom).Amount
	recipientBefore := l.app.BankKeeper.GetBalance(ctx, recipient, denom).Amount
	collectorBefore := l.app.BankKeeper.GetBalance(ctx, collector, denom).Amount
	supplyBefore := l.supply()
	amount := sdkmath.NewIntWithDecimal(10, 18)
	fee := sdkmath.NewIntWithDecimal(1, 18)

	config := l.app.GetTxConfig()
	builder := config.NewTxBuilder()
	require.NoError(t, builder.SetMsgs(banktypes.NewMsgSend(treasury, recipient, sdk.NewCoins(sdk.NewCoin(denom, amount)))))
	builder.SetGasLimit(500_000)
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewCoin(denom, fee)))
	members := key.GetPubKeys()
	partial := multisig.NewMultisig(len(members))
	complete := multisig.NewMultisig(len(members))
	mode := signing.SignMode_SIGN_MODE_LEGACY_AMINO_JSON
	signer := authsigning.SignerData{Address: treasury.String(), ChainID: l.doc.ChainID,
		AccountNumber: account.GetAccountNumber(), Sequence: sequence, PubKey: key}
	// Both transactions share the message, fee, sequence and the two existing
	// signatures. The valid transaction adds a signature from a third member.
	for _, index := range []int{4, 0, 2} {
		sig, err := clienttx.SignWithPrivKey(t.Context(), mode, signer, builder, nativeFourLaunchTreasuryKey(index), config, sequence)
		require.NoError(t, err)
		require.NoError(t, multisig.AddSignatureV2(complete, sig, members))
		if index != 2 {
			require.NoError(t, multisig.AddSignatureV2(partial, sig, members))
		}
	}
	encode := func(data *signing.MultiSignatureData) []byte {
		t.Helper()
		require.NoError(t, builder.SetSignatures(signing.SignatureV2{PubKey: key, Data: data, Sequence: sequence}))
		encoded, err := config.TxEncoder()(builder.GetTx())
		require.NoError(t, err)
		return encoded
	}
	invalidTx, validTx := encode(partial), encode(complete)
	if os.Getenv(nativeLaunchTreasuryProcessEnv) == "1" {
		writeNativeHistoricalJSON(t, filepath.Join(dir, "treasury.json"), nativeLaunchTreasuryFixture{
			Treasury: treasury.String(), Recipient: recipient.String(), FeeCollector: collector.String(), InitialSequence: sequence,
			AmountAtomic: amount.String(), FeeAtomic: fee.String(), InvalidTx: hex.EncodeToString(invalidTx), ValidTx: hex.EncodeToString(validTx),
		})
	}

	assertCommittedState := func(treasuryAmount, recipientAmount, collectorAmount sdkmath.Int, wantSequence uint64) {
		t.Helper()
		ctx := l.snapshot()
		require.Equal(t, treasuryAmount.String(), l.app.BankKeeper.GetBalance(ctx, treasury, denom).Amount.String())
		require.Equal(t, recipientAmount.String(), l.app.BankKeeper.GetBalance(ctx, recipient, denom).Amount.String())
		require.Equal(t, collectorAmount.String(), l.app.BankKeeper.GetBalance(ctx, collector, denom).Amount.String())
		require.Equal(t, wantSequence, l.app.AccountKeeper.GetAccount(ctx, treasury).GetSequence())
		require.Equal(t, supplyBefore.String(), l.supply().String())
	}
	unauthorized := sdkerrors.ErrUnauthorized.ABCICode()
	rejected, err := l.app.CheckTx(&abci.RequestCheckTx{Tx: invalidTx})
	require.NoError(t, err)
	require.Equal(t, unauthorized, rejected.Code, rejected.Log)
	failed := l.finalize([][]byte{invalidTx}, nil)
	require.Len(t, failed.TxResults, 1)
	require.Equal(t, unauthorized, failed.TxResults[0].Code, failed.TxResults[0].Log)
	l.commit(failed)
	assertCommittedState(treasuryBefore, recipientBefore, collectorBefore, sequence)

	accepted, err := l.app.CheckTx(&abci.RequestCheckTx{Tx: validTx})
	require.NoError(t, err)
	require.Zero(t, accepted.Code, accepted.Log)
	succeeded := l.finalize([][]byte{validTx}, nil)
	require.Len(t, succeeded.TxResults, 1)
	require.Zero(t, succeeded.TxResults[0].Code, succeeded.TxResults[0].Log)
	l.commit(succeeded)
	assertCommittedState(treasuryBefore.Sub(amount).Sub(fee), recipientBefore.Add(amount), collectorBefore.Add(fee), sequence+1)
}
