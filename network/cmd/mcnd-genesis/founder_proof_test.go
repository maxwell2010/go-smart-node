package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/launchgenesis"
	"github.com/stretchr/testify/require"
)

func TestFounderOwnershipCommand(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	founder := crypto.PubkeyToAddress(key.PublicKey).Hex()
	plan, err := os.ReadFile("../../docs/ru/public-launch-allocation.draft.json")
	require.NoError(t, err)
	var allocation map[string]any
	require.NoError(t, json.Unmarshal(plan, &allocation))
	allocation["chain_id"], allocation["evm_chain_id"] = "mintcandy_990014-1", "990014"
	allocation["founder_evm_address"] = founder
	allocation["decisions"].(map[string]any)["selected_stake_owner_evm_address"] = founder
	plan, err = json.Marshal(allocation)
	require.NoError(t, err)

	work := t.TempDir()
	planFile := filepath.Join(work, "allocation.json")
	require.NoError(t, os.WriteFile(planFile, plan, 0o600))
	digest := fmt.Sprintf("%x", sha256.Sum256(plan))
	challenge := strings.Repeat("1f", 32)
	commonArgs := []string{"--plan", planFile, "--approved-plan-sha256", digest, "--challenge", challenge}
	run := func(action string, flags ...string) (string, error) {
		t.Helper()
		cmd := founderProofCommand()
		output := new(bytes.Buffer)
		cmd.SetOut(output)
		cmd.SetErr(output)
		cmd.SetArgs(append(append([]string{action}, commonArgs...), flags...))
		err := cmd.Execute()
		return output.String(), err
	}
	messageFile := filepath.Join(work, "founder-message.txt")
	_, err = run("payload", "--output", messageFile)
	require.NoError(t, err)
	message, err := os.ReadFile(messageFile)
	require.NoError(t, err)
	require.Contains(t, string(message), "Chain ID: mintcandy_990014-1\nEVM chain ID: 990014")
	require.Contains(t, string(message), "Founder wallet: "+founder)
	require.Contains(t, string(message), "Allocation plan SHA256: "+digest)
	require.True(t, strings.HasSuffix(string(message), "Challenge: "+challenge))
	_, err = run("payload", "--output", messageFile)
	require.ErrorIs(t, err, os.ErrExist)

	signatureFile := filepath.Join(work, "founder-signature.txt")
	signature, err := crypto.Sign(accounts.TextHash(message), key)
	require.NoError(t, err)
	signature[64] += 27
	require.NoError(t, os.WriteFile(signatureFile, fmt.Appendf(nil, "0x%x\n", signature), 0o600))
	reportJSON, err := run("verify", "--signature", signatureFile)
	require.NoError(t, err)
	var report launchgenesis.FounderOwnershipReport
	require.NoError(t, json.Unmarshal([]byte(reportJSON), &report))
	require.Equal(t, "FOUNDER_WALLET_OWNERSHIP_VERIFIED", report.Status)
	require.Equal(t, founder, report.FounderAddress)
	require.Equal(t, digest, report.PlanSHA256)
	require.Equal(t, challenge, report.Challenge)
	require.False(t, report.NetworkLaunchAuthorized)

	_, err = run("verify", "--signature", signatureFile, "--challenge", strings.Repeat("e4", 32))
	require.ErrorContains(t, err, "does not match the founder wallet")
	_, err = run("payload", "--challenge", strings.Repeat("0", 64))
	require.ErrorContains(t, err, "invalid challenge")
	_, err = run("payload", "--approved-plan-sha256", strings.Repeat("0", 64))
	require.ErrorContains(t, err, "independently approved SHA256")

	modified := append(plan, '\n')
	require.NoError(t, os.WriteFile(planFile, modified, 0o600))
	_, err = run("verify", "--signature", signatureFile)
	require.ErrorContains(t, err, "independently approved SHA256")
	_, err = run("verify", "--signature", signatureFile, "--approved-plan-sha256", fmt.Sprintf("%x", sha256.Sum256(modified)))
	require.ErrorContains(t, err, "does not match the founder wallet")
	require.NoError(t, os.WriteFile(planFile, plan, 0o600))

	impostor, err := crypto.GenerateKey()
	require.NoError(t, err)
	signature, err = crypto.Sign(accounts.TextHash(message), impostor)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(signatureFile, fmt.Appendf(nil, "0x%x\n", signature), 0o600))
	_, err = run("verify", "--signature", signatureFile)
	require.ErrorContains(t, err, "does not match the founder wallet")
}
