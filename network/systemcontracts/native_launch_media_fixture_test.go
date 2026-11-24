package systemcontracts_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
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
	}
	var state struct {
		Withdrawal struct {
			TokenCenter common.Address `json:"token_center"`
		} `json:"withdrawal_settlement"`
	}
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	center := state.Withdrawal.TokenCenter
	require.NotEqual(t, common.Address{}, center)
	key, err := crypto.ToECDSA(nativeLaunchAccountKey(1, spec).Bytes())
	require.NoError(t, err)
	abi, err := contracts.CompiledABI("NewNetworkTokenCenter")
	require.NoError(t, err)
	payload, err := abi.Pack("createTokenReserveless", "LAUNCH AVATAR TOKEN", "media", false, true,
		big.NewInt(10), new(big.Int), uri)
	require.NoError(t, err)
	tx := ethtypes.NewTx(&ethtypes.LegacyTx{To: &center, Gas: 3_000_000,
		GasPrice: big.NewInt(1_000_000_000_000), Data: payload})
	signed, err := ethtypes.SignTx(tx, ethtypes.NewEIP155Signer(new(big.Int).SetUint64(evmID)), key)
	require.NoError(t, err)
	raw, err := signed.MarshalBinary()
	require.NoError(t, err)
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	require.NoError(t, err)
	writeErr := json.NewEncoder(file).Encode(map[string]string{
		"creator": crypto.PubkeyToAddress(key.PublicKey).Hex(), "factory": center.Hex(), "uri": uri,
		"event": abi.Events["TokenReservelessDeployed"].ID.Hex(), "hash": signed.Hash().Hex(), "raw": hexutil.Encode(raw),
		"chainId": doc.ChainID, "genesisSha256": fmt.Sprintf("%x", sha256.Sum256(genesis)),
	})
	closeErr := file.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)
}
