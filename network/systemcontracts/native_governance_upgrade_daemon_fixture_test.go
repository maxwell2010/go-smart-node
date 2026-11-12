package systemcontracts_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const nativeConsensusUpgradeGovernanceEnv = "MCND_NATIVE_CONSENSUS_UPGRADE_GOVERNANCE"
const nativeConsensusFullUpgradeEnv = "MCND_NATIVE_CONSENSUS_FULL_PROFILE_UPGRADE"

func nativeGovernanceUpgradeFixture(t *testing.T, e isolatedBackendEnv, genesis map[string]json.RawMessage, fullUpgrade bool) []byte {
	t.Helper()
	ctx, cdc := e.ctx(), e.app.AppCodec()
	var gov govv1.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(e.app.DefaultGenesis()[govtypes.ModuleName], &gov))
	period := 10 * time.Second
	expedited := 4 * time.Second
	gov.Params.VotingPeriod = &period
	gov.Params.ExpeditedVotingPeriod = &expedited
	deposit := sdk.NewCoins(sdk.NewInt64Coin(identity.BaseDenom, 1_000_000_000_000_000_000))
	gov.Params.MinDeposit = deposit
	gov.Params.ExpeditedMinDeposit = sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, deposit[0].Amount.MulRaw(2)))
	require.NoError(t, govv1.ValidateGenesis(&gov))
	genesis[govtypes.ModuleName] = cdc.MustMarshalJSON(&gov)

	key := &ethsecp256k1.PrivKey{Key: crypto.FromECDSA(e.keys[3])}
	address := sdk.AccAddress(key.PubKey().Address())
	var auth authtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[authtypes.ModuleName], &auth))
	accounts, err := authtypes.UnpackAccounts(auth.Accounts)
	require.NoError(t, err)
	var actor authtypes.GenesisAccount
	seen := map[uint64]bool{}
	for _, account := range accounts {
		require.False(t, seen[account.GetAccountNumber()], "auth genesis must preserve all signed account numbers")
		seen[account.GetAccountNumber()] = true
		if account.GetAddress().Equals(address) {
			actor = account
		}
	}
	require.NotNil(t, actor)
	require.NotNil(t, actor.GetPubKey())
	require.Equal(t, key.PubKey().Bytes(), actor.GetPubKey().Bytes())
	require.Equal(t, actor.GetSequence(), e.app.EVMKeeper.GetNonce(ctx, crypto.PubkeyToAddress(e.keys[3].PublicKey)))
	require.True(t, e.app.BankKeeper.GetBalance(ctx, address, identity.BaseDenom).Amount.GTE(deposit[0].Amount.MulRaw(5)))

	config := e.app.GetTxConfig()
	sequence := actor.GetSequence()
	sign := func(msg sdk.Msg) map[string]string {
		t.Helper()
		builder := config.NewTxBuilder()
		require.NoError(t, builder.SetMsgs(msg))
		builder.SetGasLimit(500_000)
		builder.SetFeeAmount(deposit)
		mode := signing.SignMode_SIGN_MODE_DIRECT
		sig := signing.SignatureV2{PubKey: key.PubKey(), Sequence: sequence, Data: &signing.SingleSignatureData{SignMode: mode}}
		require.NoError(t, builder.SetSignatures(sig))
		signed, err := clienttx.SignWithPrivKey(context.Background(), mode, authsigning.SignerData{
			Address: address.String(), ChainID: ctx.ChainID(), AccountNumber: actor.GetAccountNumber(),
			Sequence: sequence, PubKey: key.PubKey(),
		}, builder, key, config, sequence)
		require.NoError(t, err)
		require.NoError(t, builder.SetSignatures(signed))
		raw, err := config.TxEncoder()(builder.GetTx())
		require.NoError(t, err)
		sum := sha256.Sum256(raw)
		sequence++
		return map[string]string{"tx": base64.StdEncoding.EncodeToString(raw), "hash": hex.EncodeToString(sum[:])}
	}

	const planName = "mintcandy-native-sdk-migrations-v1"
	height := ctx.BlockHeight() + 32
	if fullUpgrade {
		// Give the genesis-at-height-one fixture time to commit the signed
		// withdrawal after approval and still finish before the handoff.
		height = 64
	}
	metadata := fmt.Sprintf(`mintcandy-sdk-upgrade/v1:{"name":"%s","height":%d}`, planName, height)
	propose := func(title string) sdk.Msg {
		t.Helper()
		msg, err := govv1.NewMsgSubmitProposal(nil, deposit, address.String(), metadata, title,
			"Execute the registered SDK migration handler in the isolated network.", false)
		require.NoError(t, err)
		return msg
	}
	rejected, passed := gov.StartingProposalId, gov.StartingProposalId+1
	rejectedProposal := sign(propose("Reject SDK migration"))
	rejectedVote := sign(govv1.NewMsgVote(address, rejected, govv1.OptionNo, ""))
	passedProposal := sign(propose("Approve SDK migration"))
	passedVote := sign(govv1.NewMsgVote(address, passed, govv1.OptionYes, ""))
	expectations, err := json.Marshal(map[string]any{
		"planName": planName, "planHeight": height, "metadata": metadata, "actorNative": address.String(),
		"accountNumber": actor.GetAccountNumber(), "sequence": actor.GetSequence(), "depositAtomic": deposit[0].Amount.String(),
		"rejectedID": rejected, "passedID": passed,
		"rejectedProposal": rejectedProposal, "rejectedVote": rejectedVote,
		"passedProposal": passedProposal, "passedVote": passedVote,
	})
	require.NoError(t, err)
	return expectations
}
