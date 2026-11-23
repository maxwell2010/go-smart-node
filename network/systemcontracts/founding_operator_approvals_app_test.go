package systemcontracts_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/launchgenesis"
	"github.com/stretchr/testify/require"
)

func TestVerifyPublishedFoundingOperatorApprovals(t *testing.T) {
	root := os.Getenv("MCND_APPROVAL_REHEARSAL_ROOT")
	if root == "" {
		t.Skip("requires the isolated founding rehearsal")
	}
	require.True(t, filepath.IsAbs(root))
	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	require.Len(t, ifaces, 1)
	require.NotZero(t, ifaces[0].Flags&net.FlagLoopback)

	var installed struct {
		ChainID          string `json:"chainId"`
		GenesisSHA256    string `json:"genesisSha256"`
		ManifestSHA256   string `json:"manifestSha256"`
		SignatureChecked bool   `json:"signatureVerified"`
	}
	readNativeHistoricalJSON(t, filepath.Join(root, "evidence", "validator-install.json"), &installed)
	require.Equal(t, nativeSponsoredLaunchChainID, installed.ChainID)
	require.True(t, installed.SignatureChecked)

	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	assembler := filepath.Join(root, "published-tools", "bin", "mcnd-genesis")
	commonArgs := []string{
		"--manifest", filepath.Join(root, "consent-manifest.json"),
		"--genesis", filepath.Join(root, "assembled", "public", "genesis.json"),
		"--approved-manifest-sha256", installed.ManifestSHA256,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run := func(command string, flags ...string) ([]byte, error) {
		args := append([]string{"launch-consent", command}, commonArgs...)
		return exec.CommandContext(ctx, assembler, append(args, flags...)...).CombinedOutput()
	}

	codec := address.NewBech32Codec(identity.Bech32PrefixValAddr)
	var confirmations []launchgenesis.OperatorConsent
	var operators []string
	for _, index := range nativeFourLaunchAccountIndices {
		key, err := crypto.ToECDSA(nativeLaunchAccountKey(index, spec).Bytes())
		require.NoError(t, err)
		operator, err := codec.BytesToString(crypto.PubkeyToAddress(key.PublicKey).Bytes())
		require.NoError(t, err)
		payload, err := run("payload", "--operator", operator)
		require.NoError(t, err, "%s", payload)
		require.Contains(t, string(payload), installed.ManifestSHA256)
		signature, err := crypto.Sign(accounts.TextHash(payload), key)
		require.NoError(t, err)
		signature[64] += 27
		confirmations = append(confirmations, launchgenesis.OperatorConsent{
			OperatorAddress: operator,
			Signature:       "0x" + hex.EncodeToString(signature),
		})
		operators = append(operators, operator)
	}

	signaturesPath := filepath.Join(t.TempDir(), "confirmations.json")
	verify := func(rows []launchgenesis.OperatorConsent) ([]byte, error) {
		raw, err := json.Marshal(rows)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(signaturesPath, raw, 0600))
		return run("verify", "--signatures", signaturesPath)
	}
	output, err := verify(confirmations[:3])
	require.Error(t, err)
	require.Contains(t, string(output), "each of the four")
	output, err = verify(confirmations)
	require.NoError(t, err, "%s", output)
	var report launchgenesis.ConsentReport
	require.NoError(t, json.Unmarshal(output, &report))
	slices.Sort(operators)
	require.Equal(t, operators, report.VerifiedOperators)
	require.Equal(t, "FOUR_OPERATOR_ACKNOWLEDGMENTS_VERIFIED", report.Status)
	require.Equal(t, installed.ChainID, report.ChainID)
	require.Equal(t, installed.GenesisSHA256, report.GenesisSHA256)
	require.Equal(t, installed.ManifestSHA256, report.ManifestSHA256)
	require.False(t, report.NetworkLaunchAuthorized)
	require.NoError(t, os.WriteFile(filepath.Join(root, "evidence", "operator-consents.json"), output, 0600))
}
