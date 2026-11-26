package systemcontracts_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cmttypes "github.com/cometbft/cometbft/types"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestExportMainnetNFTUpgradeAction(t *testing.T) {
	output := os.Getenv("MCND_NFT_GOV_OUTPUT")
	if output == "" {
		t.Skip("requires a private four-validator update fixture")
	}
	requirePrivateMainnetFixture(t)
	require.True(t, filepath.IsAbs(output))
	dir, err := os.Lstat(filepath.Dir(output))
	require.NoError(t, err)
	require.True(t, dir.IsDir())
	require.Zero(t, dir.Mode().Perm()&0077)
	genesis, err := os.ReadFile(os.Getenv("MCND_NFT_GOV_GENESIS"))
	require.NoError(t, err)
	doc, err := cmttypes.GenesisDocFromJSON(genesis)
	require.NoError(t, err)
	require.Equal(t, identity.MainnetChainID, doc.ChainID)
	var input struct {
		Index         int    `json:"index"`
		AccountNumber uint64 `json:"accountNumber"`
		Sequence      uint64 `json:"sequence"`
		ProposalID    uint64 `json:"proposalId"`
		Height        int64  `json:"height"`
	}
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("MCND_NFT_GOV_ACTION")), &input))
	require.GreaterOrEqual(t, input.Index, 0)
	require.Less(t, input.Index, len(nativeFourLaunchAccountIndices))
	require.Less(t, input.Sequence, uint64(64))
	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	key := nativeLaunchAccountKey(nativeFourLaunchAccountIndices[input.Index], spec)
	signer, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(key.PubKey().Address())
	require.NoError(t, err)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(key.PubKey().Address())
	require.NoError(t, err)
	var state struct {
		Validator struct {
			Validators []struct {
				Operator string `json:"operator_address"`
			} `json:"validators"`
		} `json:"validator"`
		Gov struct {
			Params struct {
				VotingPeriod string `json:"voting_period"`
			} `json:"params"`
		} `json:"gov"`
	}
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	require.Len(t, state.Validator.Validators, 4)
	require.Equal(t, "32s", state.Gov.Params.VotingPeriod)
	var operators []string
	for _, row := range state.Validator.Validators {
		operators = append(operators, row.Operator)
	}
	require.Contains(t, operators, operator)
	var msg sdk.Msg
	if input.ProposalID == 0 {
		require.Greater(t, input.Height, int64(0))
		require.Less(t, input.Height, int64(256))
		body := fmt.Sprintf(`mintcandy-sdk-upgrade/v1:{"name":"mintcandy-native-sdk-migrations-v1","height":%d}`, input.Height)
		deposit, err := sdk.ParseCoinsNormalized("10000000000000000000msp")
		require.NoError(t, err)
		msg, err = govv1.NewMsgSubmitProposal(nil, deposit, signer, body, "MintCandy NFT reserve refunds",
			"Exercise a governed update in an isolated MSP network.", false)
		require.NoError(t, err)
	} else {
		require.Zero(t, input.Height)
		require.Equal(t, uint64(1), input.ProposalID)
		msg = &govv1.MsgVote{ProposalId: input.ProposalID, Voter: signer, Option: govv1.OptionYes}
	}
	writeFixtureGovernanceTx(t, output, genesis, doc.ChainID, identity.MainnetEVMChainID, "msp", signer, key, input.AccountNumber, input.Sequence, msg)
}

func writeFixtureGovernanceTx(t *testing.T, output string, genesis []byte, chain string, evm uint64, denom, signer string, key *ethsecp256k1.PrivKey, account, sequence uint64, msg sdk.Msg) {
	t.Helper()
	config := ownedapp.MakeEncodingConfig(evm).TxConfig
	builder := config.NewTxBuilder()
	require.NoError(t, builder.SetMsgs(msg))
	builder.SetGasLimit(500_000)
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewInt64Coin(denom, 500_000_000_000_000_000)))
	mode := signing.SignMode_SIGN_MODE_DIRECT
	require.NoError(t, builder.SetSignatures(signing.SignatureV2{PubKey: key.PubKey(), Sequence: sequence,
		Data: &signing.SingleSignatureData{SignMode: mode}}))
	signature, err := clienttx.SignWithPrivKey(t.Context(), mode, authsigning.SignerData{
		Address: signer, ChainID: chain, AccountNumber: account, Sequence: sequence, PubKey: key.PubKey(),
	}, builder, key, config, sequence)
	require.NoError(t, err)
	require.NoError(t, builder.SetSignatures(signature))
	raw, err := config.TxEncoder()(builder.GetTx())
	require.NoError(t, err)
	f, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	writeErr := json.NewEncoder(f).Encode(map[string]string{"tx": base64.StdEncoding.EncodeToString(raw),
		"hash": fmt.Sprintf("%x", sha256.Sum256(raw)), "sender": signer, "genesisSha256": fmt.Sprintf("%x", sha256.Sum256(genesis))})
	closeErr := f.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
}
