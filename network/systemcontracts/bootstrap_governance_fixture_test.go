//go:build mintcandy_testnet

package systemcontracts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestExportBootstrapFixtureIdentity(t *testing.T) {
	out := os.Getenv("MCND_BOOTSTRAP_IDENTITY")
	if out == "" {
		t.Skip("isolated genesis builder")
	}
	spec, _ := nativeLaunchSpecForMode("4-empty-sponsored")
	key := nativeLaunchAccountKey(3, spec)
	native, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(key.PubKey().Address())
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]string{"actor": common.BytesToAddress(key.PubKey().Address()).Hex(), "actorNative": native})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(out, raw, 0600))
}

func TestExportBootstrapAction(t *testing.T) {
	out := os.Getenv("MCND_NFT_GOV_OUTPUT")
	if out == "" {
		t.Skip("isolated governance rehearsal")
	}
	requirePrivateMainnetFixture(t) // Only loopback in a separate network namespace.
	raw, err := os.ReadFile(os.Getenv("MCND_NFT_GOV_GENESIS"))
	require.NoError(t, err)
	doc, err := cmttypes.GenesisDocFromJSON(raw)
	require.NoError(t, err)
	require.Equal(t, identity.TestnetChainID, doc.ChainID)
	var input struct {
		Index                               int
		Height                              int64
		ProposalID, AccountNumber, Sequence uint64
		Signal                              bool
	}
	require.NoError(t, json.Unmarshal([]byte(os.Getenv("MCND_NFT_GOV_ACTION")), &input))
	indices := []int{0, 1, 2, 4, 3}
	require.GreaterOrEqual(t, input.Index, 0)
	require.Less(t, input.Index, len(indices))
	spec, _ := nativeLaunchSpecForMode("4-empty-sponsored")
	key := nativeLaunchAccountKey(indices[input.Index], spec)
	signer, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(key.PubKey().Address())
	require.NoError(t, err)
	var msg sdk.Msg
	if input.ProposalID > 0 {
		require.LessOrEqual(t, input.ProposalID, uint64(2))
		msg = &govv1.MsgVote{ProposalId: input.ProposalID, Voter: signer, Option: govv1.OptionYes}
	} else {
		body := "bootstrap-unanimity-signal"
		if !input.Signal {
			require.Positive(t, input.Height)
			require.Less(t, input.Height, int64(256))
			body = fmt.Sprintf(`mintcandy-sdk-upgrade/v1:{"name":"mintcandy-bootstrap-unanimity-v1","height":%d}`, input.Height)
		}
		deposit, err := sdk.ParseCoinsNormalized("10000000000000000000tmsp")
		require.NoError(t, err)
		msg, err = govv1.NewMsgSubmitProposal(nil, deposit, signer, body, "Bootstrap governance", "Isolated tMSP voting rehearsal", false)
		require.NoError(t, err)
	}
	writeFixtureGovernanceTx(t, out, raw, doc.ChainID, identity.TestnetEVMChainID, identity.TestnetBaseDenom, signer, key, input.AccountNumber, input.Sequence, msg)
}
