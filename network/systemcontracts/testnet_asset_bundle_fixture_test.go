package systemcontracts_test

import (
	"crypto/ecdsa"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// TestSignAssetBundle produces private, disposable transactions for the loopback
// daemon rehearsal. Only receipts and the block heights are published.
func TestSignAssetBundle(t *testing.T) {
	dir := os.Getenv("MCND_ASSET_BUNDLE")
	if dir == "" {
		t.Skip("requires isolated tMSP rehearsal")
	}
	requirePrivateMainnetFixture(t)
	require.Equal(t, identity.TestnetChainID, identity.ProfileChainID)
	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(dir) && info.IsDir() && info.Mode().Perm()&0077 == 0)
	founder, err := crypto.LoadECDSA(os.Getenv("MCND_TESTNET_TARIFF_FOUNDER_FILE"))
	require.NoError(t, err)
	spec, ok := nativeLaunchSpecForMode("4-empty-sponsored")
	require.True(t, ok)
	operator, err := crypto.ToECDSA(nativeLaunchAccountKey(5, spec).Bytes())
	require.NoError(t, err)
	a, b := crypto.PubkeyToAddress(founder.PublicKey), crypto.PubkeyToAddress(operator.PublicKey)
	number := func(env string) uint64 {
		n, err := strconv.ParseUint(os.Getenv(env), 10, 64)
		require.NoError(t, err)
		return n
	}
	address := func(env string) common.Address {
		s := os.Getenv(env)
		require.True(t, common.IsHexAddress(s))
		return common.HexToAddress(s)
	}
	nonces := map[*ecdsa.PrivateKey]uint64{founder: number("MCND_NONCE_A"), operator: number("MCND_NONCE_B")}
	factory := address("MCND_NFT_CENTER")
	factoryNonce := number("MCND_FACTORY_NONCE")
	bi := big.NewInt
	unit := bi(1_000_000_000_000_000_000)
	zero := bi(0)
	rows := []map[string]any{}
	add := func(label, artifact string, key *ecdsa.PrivateKey, target common.Address, value *big.Int, data []byte, pass bool) {
		n := nonces[key]
		nonces[key]++
		tx, err := ethtypes.SignTx(ethtypes.NewTx(&ethtypes.LegacyTx{To: &target, Nonce: n, Gas: 18_000_000, GasPrice: bi(2_000_000_000), Value: value, Data: data}), ethtypes.NewEIP155Signer(bi(identity.TestnetEVMChainID)), key)
		require.NoError(t, err)
		raw, err := tx.MarshalBinary()
		require.NoError(t, err)
		rows = append(rows, map[string]any{"label": label, "asset": artifact, "creator": crypto.PubkeyToAddress(key.PublicKey), "target": target, "nonce": n, "hash": tx.Hash(), "raw": hexutil.Encode(raw), "success": pass})
	}
	var assets []map[string]any
	runPermit := func(artifact, name string, target common.Address, id *big.Int) {
		future := bi(4_000_000_000)
		signed := assetPermitFixture(t, artifact, name, target, b, founder, id, zero, future)
		// Same inner signature, different outer relayer transaction nonce.
		add("expired-permit", artifact, operator, target, zero, assetPermitFixture(t, artifact, name, target, b, founder, id, zero, bi(1)), false)
		add("wrong-domain-permit", artifact, operator, target, zero, assetPermitFixture(t, artifact, name+" OTHER", target, b, founder, id, zero, future), false)
		add("permit", artifact, operator, target, zero, signed, true)
		add("replayed-permit", artifact, operator, target, zero, signed, false)
	}
	for i, artifact := range []string{"DRC20", "DRC20Reserveless", "NewNetworkDRC721", "MintCandyIsolatedDRC1155", "DRC721Reserveless", "DRC1155Reserveless"} {
		abi, err := contracts.CompiledABI(artifact)
		require.NoError(t, err)
		pack := func(method string, args ...interface{}) []byte {
			v, err := abi.Pack(method, args...)
			require.NoError(t, err, "%s.%s", artifact, method)
			return v
		}
		var target common.Address
		var name string
		id := bi(1)
		nft := i >= 2
		multi := i == 3 || i == 5
		backed := i == 0 || i == 2 || i == 3
		uri := "ipfs://bundle-metadata"
		if multi {
			id = bi(123)
		}
		if !nft {
			if backed {
				target = address("MCND_TOKEN_BACKED")
				name = "TOKEN B BAA"
			} else {
				target = address("MCND_TOKEN_FREE")
				name = "TOKEN R RAA"
			}
		} else {
			name = "Bundle " + artifact
			meta := userBundleMeta{a, "BUNDLE", name, uri, true, true}
			center, err := contracts.CompiledABI("NewNetworkNFTCenter")
			require.NoError(t, err)
			method := []string{"createDRC721", "createDRC1155", "createDRC721Reserveless", "createDRC1155Reserveless"}[i-2]
			data, err := center.Pack(method, meta)
			require.NoError(t, err)
			target = crypto.CreateAddress(factory, factoryNonce)
			factoryNonce++
			add("collection", artifact, founder, factory, zero, data, true)
			if multi {
				for _, tokenID := range []int64{123, 124} {
					if backed {
						add("mint", artifact, founder, target, new(big.Int).Mul(unit, bi(2)), pack("mintByDEL", a, bi(tokenID), bi(2), uri), true)
					} else {
						add("mint", artifact, founder, target, zero, pack("mint", a, bi(tokenID), bi(2), uri, zero, common.Address{}), true)
					}
				}
			} else if backed {
				add("mint", artifact, founder, target, unit, pack("mintByDEL", a, uri), true)
			} else {
				add("mint", artifact, founder, target, zero, pack("mint", a, uri, zero, common.Address{}), true)
			}
		}
		var nonceKey interface{} = a
		var approval []byte
		if !nft {
			approval = pack("allowance", a, b)
		} else if multi {
			approval = pack("isApprovedForAll", a, b)
		} else {
			approval = pack("getApproved", id)
			nonceKey = id
		}
		record := map[string]any{"asset": artifact, "target": target, "backed": backed, "nft": nft, "multi": multi, "nonceCall": hexutil.Encode(pack("nonces", nonceKey)), "approvalCall": hexutil.Encode(approval)}
		if nft && backed {
			record["reserveCall"] = hexutil.Encode(pack("getReserve", id))
		}
		assets = append(assets, record)
		if multi {
			batch := pack("safeBatchTransferFrom", a, b, []*big.Int{bi(123), bi(124)}, []*big.Int{bi(1), bi(1)}, []byte{})
			add("unauthorized-batch", artifact, operator, target, zero, batch, false)
		}
		runPermit(artifact, name, target, id)
		if nft && backed {
			top := pack("addReserveByDEL", id)
			add("zero-topup", artifact, operator, target, zero, top, false)
			if multi {
				add("nonuniform-topup", artifact, operator, target, bi(1), top, false)
			}
			add("topup", artifact, operator, target, new(big.Int).Mul(unit, bi(2)), top, true)
		}
		if multi {
			batch := func(ids, amounts []*big.Int) []byte {
				return pack("safeBatchTransferFrom", a, b, ids, amounts, []byte{})
			}
			add("overspend-batch", artifact, operator, target, zero, batch([]*big.Int{bi(123), bi(124)}, []*big.Int{bi(1), bi(3)}), false)
			add("batch", artifact, operator, target, zero, batch([]*big.Int{bi(123), bi(124)}, []*big.Int{bi(1), bi(1)}), true)
		}
		if nft && !backed {
			if multi {
				add("overburn", artifact, founder, target, zero, pack("burn", id, bi(2)), false)
				add("burn", artifact, founder, target, zero, pack("burn", id, bi(1)), true)
				add("repeat-burn", artifact, founder, target, zero, pack("burn", id, bi(1)), false)
			} else {
				// A permit is not ownership of a wallet unit.
				add("foreign-burn", artifact, operator, target, zero, pack("burn", id), false)
				add("burn", artifact, founder, target, zero, pack("burn", id), true)
				add("repeat-burn", artifact, founder, target, zero, pack("burn", id), false)
			}
		}
	}
	encoded, err := json.Marshal(map[string]any{"rows": rows, "assets": assets, "owner": a, "operator": b})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bundle.json"), encoded, 0600))
}

type userBundleMeta struct {
	Creator     common.Address
	Symbol      string
	Name        string
	ContractURI string
	Refundable  bool
	Burnable    bool
}
