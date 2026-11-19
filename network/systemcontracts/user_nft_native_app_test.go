//go:build appstate

package systemcontracts_test

import (
	"encoding/json"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const userNFTNativePhaseEnv = "MCND_USER_NFT_NATIVE_PHASE"

func TestOwnedAppNativeUserNFTLifecycle(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0700))
	for _, phase := range []string{"seed", "run", "restart"} {
		filter, historical, custom := "^TestOwnedAppNativeReservelessTokenLifecycle$", "", ""
		if phase == "seed" {
			filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
			historical = "seed"
			custom = "seed"
		}
		child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=4m", "-test.run="+filter)
		child.Env = append(os.Environ(), reservelessNativePhaseEnv+"="+phase, userNFTNativePhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir, nativeHistoricalConsensusPhaseEnv+"="+historical, nativeCustomCollateralGenesisPhaseEnv+"="+custom)
		output, err := child.CombinedOutput()
		require.NoError(t, err, "phase %s: %s", phase, output)
		t.Logf("%s: %s", phase, output)
	}
}

type userNFTCheckpoint struct {
	Collections []common.Address
	NativeIDs   []string
}

func runNativeUserNFTLifecycle(t *testing.T, l nativeHistoricalLifecycle, dir, phase string) {
	center, err := contracts.CompiledABI("NewNetworkNFTCenter")
	require.NoError(t, err)
	var cp userNFTCheckpoint
	key, err := registryFixtureKey(3, true)
	require.NoError(t, err)
	private, err := crypto.ToECDSA(key.Bytes())
	require.NoError(t, err)
	owner := crypto.PubkeyToAddress(private.PublicKey)
	otherKey, err := registryFixtureKey(4, true)
	require.NoError(t, err)
	other := common.BytesToAddress(otherKey.PubKey().Address())
	var profile struct {
		NFTCenter common.Address `json:"nft_center"`
	}
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(l.doc.AppState, &state))
	require.NoError(t, json.Unmarshal(state["withdrawal_settlement"], &profile))
	send := func(keyIndex int, to common.Address, data []byte, value *big.Int, success bool) []byte {
		key, err := registryFixtureKey(keyIndex, true)
		require.NoError(t, err)
		pk, err := crypto.ToECDSA(key.Bytes())
		require.NoError(t, err)
		from := crypto.PubkeyToAddress(pk.PublicKey)
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: l.app.EVMKeeper.GetNonce(l.snapshot(), from), To: &to, Value: value, Gas: 24000000, GasPrice: big.NewInt(1000000000000), Data: data})
		signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(990001)), pk)
		require.NoError(t, err)
		block, response := l.finalizeSignedEthereum(signed)
		if success {
			require.Empty(t, response.VmError)
		} else {
			require.NotEmpty(t, response.VmError)
			require.Empty(t, response.Logs)
		}
		l.commit(block)
		return response.Ret
	}
	if phase == "restart" {
		readNativeHistoricalJSON(t, filepath.Join(dir, "public-nft.json"), &cp)
		require.Len(t, cp.Collections, 4)
		for i, target := range cp.Collections {
			collection, err := l.app.NativeNFTKeeper.GetCollectionByDRC(l.snapshot(), target.Hex())
			require.NoError(t, err)
			require.Equal(t, "User NFT", collection.ExternalName)
			token, err := l.app.NativeNFTKeeper.GetToken(l.snapshot(), cp.NativeIDs[i])
			require.NoError(t, err)
			require.Equal(t, "ipfs://user-nft-metadata", token.URI)
			binding, err := l.app.NativeNFTKeeper.GetExternalNFTBinding(l.snapshot(), cp.NativeIDs[i])
			require.NoError(t, err)
			require.Equal(t, target.Bytes(), binding.Contract)
		}
		return
	}
	for i, method := range []string{"createDRC721", "createDRC1155", "createDRC721Reserveless", "createDRC1155Reserveless"} {
		meta := struct {
			Creator     common.Address
			Symbol      string
			Name        string
			ContractURI string
			Refundable  bool
			Burnable    bool
		}{owner, "USER", "User NFT", "ipfs://user-collection", true, true}
		data, err := center.Pack(method, meta)
		require.NoError(t, err)
		ret := send(3, profile.NFTCenter, data, new(big.Int), true)
		values, err := center.Unpack(method, ret)
		require.NoError(t, err)
		target := values[0].(common.Address)
		name := []string{"NewNetworkDRC721", "MintCandyIsolatedDRC1155", "DRC721Reserveless", "DRC1155Reserveless"}[i]
		asset, err := contracts.CompiledABI(name)
		require.NoError(t, err)
		id := big.NewInt(1)
		is1155 := i == 1 || i == 3
		if is1155 {
			id.SetInt64(123)
		}
		value := new(big.Int)
		var args []interface{}
		mint := "mint"
		if i < 2 {
			mint = "mintByDEL"
			value.SetString("2000000000000000000", 10)
		}
		if is1155 {
			args = []interface{}{owner, id, big.NewInt(2), "ipfs://user-nft-metadata"}
			if i < 2 {
				value.Mul(value, big.NewInt(2))
			}
		} else {
			args = []interface{}{owner, "ipfs://user-nft-metadata"}
		}
		if i >= 2 {
			args = append(args, new(big.Int), common.Address{})
		}
		data, err = asset.Pack(mint, args...)
		require.NoError(t, err)
		send(3, target, data, value, true)
		nativeID, err := nfttypes.FreshExternalTokenID(l.doc.ChainID, 990001, target[:], common.BigToHash(id).Bytes())
		require.NoError(t, err)
		token, err := l.app.NativeNFTKeeper.GetToken(l.snapshot(), nativeID)
		require.NoError(t, err)
		require.Equal(t, "ipfs://user-nft-metadata", token.URI)
		// Wrong creator cannot add units, even though it has native transaction gas.
		send(4, target, data, value, false)
		before, err := l.app.NativeNFTKeeper.GetToken(l.snapshot(), nativeID)
		require.NoError(t, err)
		require.Equal(t, token.Minted, before.Minted)
		if is1155 {
			data, err = asset.Pack("safeTransferFrom", owner, other, id, big.NewInt(1), []byte{})
		} else {
			data, err = asset.Pack("transferFrom", owner, other, id)
		}
		require.NoError(t, err)
		send(3, target, data, new(big.Int), true)
		count, err := l.app.NativeNFTKeeper.GetActiveSubTokenCount(l.snapshot(), nativeID, sdk.AccAddress(other[:]).String())
		require.NoError(t, err)
		require.Equal(t, uint32(1), count)
		cp.Collections = append(cp.Collections, target)
		cp.NativeIDs = append(cp.NativeIDs, nativeID)
	}
	meta := struct {
		Creator     common.Address
		Symbol      string
		Name        string
		ContractURI string
		Refundable  bool
		Burnable    bool
	}{owner, "BAD", "Bad metadata", strings.Repeat("x", 2049), true, true}
	before := l.app.EVMKeeper.GetNonce(l.snapshot(), profile.NFTCenter)
	data, err := center.Pack("createDRC721Reserveless", meta)
	require.NoError(t, err)
	send(3, profile.NFTCenter, data, new(big.Int), false)
	require.Equal(t, before, l.app.EVMKeeper.GetNonce(l.snapshot(), profile.NFTCenter), "late URI rejection must undo deployment nonce")
	encoded, err := json.Marshal(cp)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "public-nft.json"), encoded, 0600))
}
