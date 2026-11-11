package systemcontracts_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/ethereum/go-ethereum/common"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const withdrawalGenesisFixtureEnv = "MCND_WITHDRAWAL_GENESIS_FIXTURE_DIR"

func prepareWithdrawalGenesisFixtureClock(t *testing.T, e isolatedBackendEnv) {
	t.Helper()
	if os.Getenv(withdrawalGenesisFixtureEnv) != "" {
		e.genesisClock(time.Now().UTC().Truncate(time.Second))
	}
}

// Export only the pending claim being tested, retaining the actual deployed
// contract runtime, storage and balances from the signed fixture. This
// producer is test-only and never writes a user's node home.
func exportWithdrawalGenesisFixture(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, binding validatortypes.EVMFrozenBinding, beneficiary common.Address, deadline time.Time) {
	t.Helper()
	dir := os.Getenv(withdrawalGenesisFixtureEnv)
	if dir == "" {
		return
	}
	require.True(t, filepath.IsAbs(dir))
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)
	require.Greater(t, time.Until(deadline), 45*time.Second, "daemon needs enough time to observe pending genesis")

	ctx, cdc := e.ctx(), e.app.AppCodec()
	genesis, err := e.app.ModuleManager.ExportGenesisForModules(ctx, cdc, nil)
	require.NoError(t, err)
	profileJSON, err := json.Marshal(profile)
	require.NoError(t, err)
	genesis[ownedapp.WithdrawalGenesisKey] = profileJSON
	var validatorGenesis validatortypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[validatortypes.ModuleName], &validatorGenesis))
	require.Len(t, validatorGenesis.Undelegations, 1)
	require.Len(t, validatorGenesis.Undelegations[0].Entries, 1)
	validatorGenesis.EVMFrozenBindings = []validatortypes.EVMFrozenBinding{binding}
	validatorGenesis.BindingRevision = 0
	genesis[validatortypes.ModuleName] = cdc.MustMarshalJSON(&validatorGenesis)

	position := binding.NFTPosition
	require.NotNil(t, position)
	require.Equal(t, int64(1024), position.OriginQuantity.Int64())
	var nftGenesis nfttypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[nfttypes.ModuleName], &nftGenesis))
	want := func(id string) bool {
		return id == position.Origin.NativeTokenID || id == position.Custody.NativeTokenID
	}
	var collections []nfttypes.Collection
	for _, col := range nftGenesis.Collections {
		var tokens nfttypes.Tokens
		for _, token := range col.Tokens {
			if want(token.ID) {
				tokens = append(tokens, token)
			}
		}
		if len(tokens) > 0 {
			col.Tokens, col.Supply = tokens, uint32(len(tokens))
			collections = append(collections, col)
		}
	}
	nftGenesis.Collections = collections
	var external []nfttypes.ExternalNFTBinding
	for _, asset := range nftGenesis.ExternalBindings {
		if want(asset.NativeTokenID) {
			external = append(external, asset)
		}
	}
	nftGenesis.ExternalBindings = external
	var lineages []nfttypes.IsolatedPositionLineage
	for _, lineage := range nftGenesis.IsolatedPositions {
		if lineage.ReceiptNativeTokenID == position.Custody.NativeTokenID {
			lineages = append(lineages, lineage)
		}
	}
	require.Len(t, lineages, 1)
	nftGenesis.IsolatedPositions = lineages
	genesis[nfttypes.ModuleName] = cdc.MustMarshalJSON(&nftGenesis)
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(cdc, genesis))
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cdc, genesis))

	write := func(name string, data []byte) {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		require.NoError(t, writeErr)
		require.NoError(t, closeErr)
	}
	doc := cmttypes.GenesisDoc{ChainID: ctx.ChainID(), InitialHeight: ctx.BlockHeight(),
		GenesisTime: time.Now().UTC().Truncate(time.Second).Add(-time.Second), ConsensusParams: cmttypes.DefaultConsensusParams()}
	writeGenesis := func(name string) {
		t.Helper()
		doc.AppState, err = json.Marshal(genesis)
		require.NoError(t, err)
		body, err := cmtjson.MarshalIndent(doc, "", "  ")
		require.NoError(t, err)
		write(name, body)
	}
	writeGenesis("genesis.json")

	// Change the pending deadline in both native representations, retaining a
	// structurally coherent snapshot that disagrees with the actual Gateway.
	pair := &validatorGenesis.Undelegations[0]
	entry := &pair.Entries[0]
	entry.CompletionTime = entry.CompletionTime.Add(time.Second)
	bad := &validatorGenesis.EVMFrozenBindings[0]
	bad.CompletionTimestamp = common.BigToHash(big.NewInt(entry.CompletionTime.Unix())).Bytes()
	bad.NativeEntryHash, err = validatortypes.NativeQueueEntryHash(1, pair.Delegator, pair.Validator, "", entry)
	require.NoError(t, err)
	genesis[validatortypes.ModuleName] = cdc.MustMarshalJSON(&validatorGenesis)
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cdc, genesis), "negative control must reach EVM attestation")
	writeGenesis("invalid-genesis.json")

	vault, err := contracts.CompiledABI("MintCandyNFTPositionVault")
	require.NoError(t, err)
	callData, err := vault.Pack("getPosition", common.BytesToHash(position.PositionID).Big())
	require.NoError(t, err)
	expectations, err := json.Marshal(map[string]interface{}{
		"chainId": ctx.ChainID(), "evmChainId": 990001, "gateway": profile.Gateway.Hex(),
		"dueAt": deadline.Unix(), "invalidError": "authenticate withdrawal genesis",
		"nft": map[string]interface{}{
			"id": common.BytesToHash(binding.FrozenIndex).Hex(), "vault": profile.PositionVault.Hex(), "tokenId": common.BytesToHash(position.PositionID).Hex(),
			"beneficiary": beneficiary.Hex(), "nativeTokenId": position.Custody.NativeTokenID, "nativeSubTokenId": position.Custody.NativeSubTokenIDs[0],
			"originQuantity": 1024, "reserveAtomic": binding.ReportedReserveAmount.String(), "positionCallData": "0x" + hex.EncodeToString(callData),
		},
	})
	require.NoError(t, err)
	write("expectations.json", expectations)
	e.saveGenesisKey(dir)
}
