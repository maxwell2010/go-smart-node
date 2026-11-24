package systemcontracts_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func TestExportLaunchMediaCreation(t *testing.T) {
	output := os.Getenv("MCND_MEDIA_FIXTURE")
	if output == "" {
		t.Skip("requires a private isolated launch fixture")
	}
	require.True(t, filepath.IsAbs(output))
	dir, err := os.Lstat(filepath.Dir(output))
	require.NoError(t, err)
	require.True(t, dir.IsDir())
	require.Zero(t, dir.Mode().Perm()&0077)
	uri := os.Getenv("MCND_MEDIA_URI")
	require.Regexp(t, regexp.MustCompile(`^ipfs://bafkrei[a-z2-7]{52}$`), uri)
	kind := os.Getenv("MCND_MEDIA_KIND")
	if kind == "" {
		kind = "erc20"
	}
	require.Contains(t, []string{"erc20", "erc721", "erc1155", "erc721-backed", "erc1155-backed"}, kind)
	genesis, err := os.ReadFile(os.Getenv("MCND_MEDIA_GENESIS"))
	require.NoError(t, err)
	doc, err := cmttypes.GenesisDocFromJSON(genesis)
	require.NoError(t, err)
	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	evmID := spec.evmID
	if doc.ChainID == identity.MainnetChainID {
		localNetwork, err := os.Readlink("/proc/self/ns/net")
		require.NoError(t, err)
		initialNetwork, err := os.Readlink("/proc/1/ns/net")
		require.NoError(t, err)
		require.NotEqual(t, initialNetwork, localNetwork)
		evmID = identity.MainnetEVMChainID
	} else {
		require.Equal(t, nativeSponsoredLaunchChainID, doc.ChainID)
		require.False(t, strings.HasSuffix(kind, "-backed"))
	}
	var state struct {
		Withdrawal struct {
			TokenCenter common.Address `json:"token_center"`
			NFTCenter   common.Address `json:"nft_center"`
		} `json:"withdrawal_settlement"`
	}
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	key, err := crypto.ToECDSA(nativeLaunchAccountKey(1, spec).Bytes())
	require.NoError(t, err)
	creator := crypto.PubkeyToAddress(key.PublicKey)
	target, payload, event, nonce, value := launchMediaTransaction(t, kind, creator, uri,
		state.Withdrawal.TokenCenter, state.Withdrawal.NFTCenter)
	require.NotEqual(t, common.Address{}, target)
	gas := uint64(3_000_000)
	if kind != "erc20" {
		gas = 12_000_000
	}
	tx := ethtypes.NewTx(&ethtypes.LegacyTx{To: &target, Nonce: nonce, Gas: gas,
		GasPrice: big.NewInt(1_000_000_000_000), Data: payload, Value: value})
	signed, err := ethtypes.SignTx(tx, ethtypes.NewEIP155Signer(new(big.Int).SetUint64(evmID)), key)
	require.NoError(t, err)
	raw, err := signed.MarshalBinary()
	require.NoError(t, err)
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	writeErr := json.NewEncoder(file).Encode(map[string]string{
		"creator": creator.Hex(), "target": target.Hex(), "kind": kind, "uri": uri,
		"event": event.Hex(), "hash": signed.Hash().Hex(), "raw": hexutil.Encode(raw),
		"chainId": doc.ChainID, "genesisSha256": fmt.Sprintf("%x", sha256.Sum256(genesis)),
	})
	closeErr := file.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
}

func launchMediaTransaction(t *testing.T, kind string, creator common.Address, uri string,
	tokenCenter, nftCenter common.Address,
) (common.Address, []byte, common.Hash, uint64, *big.Int) {
	t.Helper()
	collection := os.Getenv("MCND_MEDIA_COLLECTION")
	refund := os.Getenv("MCND_MEDIA_REFUND")
	value := new(big.Int)
	if kind == "erc20" {
		require.Empty(t, collection)
		require.Empty(t, refund)
		factory, err := contracts.CompiledABI("NewNetworkTokenCenter")
		require.NoError(t, err)
		payload, err := factory.Pack("createTokenReserveless", "LAUNCH AVATAR TOKEN", "media", false, true,
			big.NewInt(10), new(big.Int), uri)
		require.NoError(t, err)
		return tokenCenter, payload, factory.Events["TokenReservelessDeployed"].ID, 0, value
	}
	multi := strings.HasPrefix(kind, "erc1155")
	backed := strings.HasSuffix(kind, "-backed")
	if collection != "" {
		require.True(t, common.IsHexAddress(collection))
		contract, event := "DRC721Reserveless", "Transfer"
		args := []interface{}{creator, uri}
		if multi {
			contract, event = "DRC1155Reserveless", "TransferSingle"
			args = []interface{}{creator, big.NewInt(123), big.NewInt(2), uri}
		}
		method, nonce := "mint", uint64(1)
		if backed {
			contract = "NewNetworkDRC721"
			if multi {
				contract = "MintCandyIsolatedDRC1155"
			}
			method = "mintByDEL"
			value.SetInt64(2_000_000_000_000_000_000)
			if multi {
				value.Mul(value, big.NewInt(2))
			}
		} else {
			args = append(args, new(big.Int), common.Address{})
		}
		if refund != "" {
			require.True(t, backed)
			require.Contains(t, []string{"first", "last"}, refund)
			method, nonce = "burn", 2
			value.SetInt64(0)
			args = []interface{}{big.NewInt(1)}
			if multi {
				args = []interface{}{big.NewInt(123), big.NewInt(1)}
				if refund == "last" {
					nonce++
				}
			} else {
				require.Equal(t, "first", refund)
			}
		}
		asset, err := contracts.CompiledABI(contract)
		require.NoError(t, err)
		payload, err := asset.Pack(method, args...)
		require.NoError(t, err)
		return common.HexToAddress(collection), payload, asset.Events[event].ID, nonce, value
	}
	require.Empty(t, refund)
	factory, err := contracts.CompiledABI("NewNetworkNFTCenter")
	require.NoError(t, err)
	method := "createDRC721"
	if multi {
		method = "createDRC1155"
	}
	if !backed {
		method += "Reserveless"
	}
	metadata := struct {
		Creator     common.Address
		Symbol      string
		Name        string
		ContractURI string
		Refundable  bool
		Burnable    bool
	}{creator, "MEDIA", "Media rehearsal collection", uri, true, true}
	payload, err := factory.Pack(method, metadata)
	require.NoError(t, err)
	return nftCenter, payload, factory.Events["NFTCreated"].ID, 0, value
}
