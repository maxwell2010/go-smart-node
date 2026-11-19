package app

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func copyUserNFTLogs(logs []*ethtypes.Log) []*ethtypes.Log {
	result := make([]*ethtypes.Log, len(logs))
	for i, log := range logs {
		value := *log
		value.Data = append([]byte(nil), log.Data...)
		value.Topics = append([]common.Hash(nil), log.Topics...)
		result[i] = &value
	}
	return result
}

func TestUserNFTCreationEventsAreExactAndOrdered(t *testing.T) {
	factory, err := contracts.CompiledABI("NewNetworkNFTCenter")
	require.NoError(t, err)
	center, target, beacon := common.HexToAddress("0x1001"), common.HexToAddress("0x1002"), common.HexToAddress("0x1003")
	metadata := userNFTMetadata{Creator: common.HexToAddress("0x1004"), Symbol: "USER", Name: "User NFT", ContractURI: "ipfs://collection", Refundable: true, Burnable: true}
	expected, err := userNFTCreationLogs(factory, center, target, beacon, 1, metadata)
	require.NoError(t, err)
	require.Len(t, expected, 4)
	require.Equal(t, crypto.Keccak256Hash([]byte("ContractURIUpdated()")), expected[1].Topics[0])
	require.NoError(t, requireUserNFTLogs(copyUserNFTLogs(expected), expected))
	for name, mutate := range map[string]func([]*ethtypes.Log) []*ethtypes.Log{
		"foreign emitter":               func(v []*ethtypes.Log) []*ethtypes.Log { v[1].Address = center; return v },
		"extra data":                    func(v []*ethtypes.Log) []*ethtypes.Log { v[1].Data = []byte{0}; return v },
		"missing topic":                 func(v []*ethtypes.Log) []*ethtypes.Log { v[0].Topics = v[0].Topics[:1]; return v },
		"different beacon":              func(v []*ethtypes.Log) []*ethtypes.Log { v[0].Topics[1] = common.HexToHash("0x99"); return v },
		"different initialized version": func(v []*ethtypes.Log) []*ethtypes.Log { v[2].Data = common.BigToHash(big.NewInt(2)).Bytes(); return v },
		"missing event":                 func(v []*ethtypes.Log) []*ethtypes.Log { return v[1:] },
		"extra event":                   func(v []*ethtypes.Log) []*ethtypes.Log { return append(v, v[1]) },
		"reordered event":               func(v []*ethtypes.Log) []*ethtypes.Log { v[1], v[2] = v[2], v[1]; return v },
	} {
		t.Run(name, func(t *testing.T) { require.Error(t, requireUserNFTLogs(mutate(copyUserNFTLogs(expected)), expected)) })
	}
}

func TestUserNFTMintEventsAuthenticateAuxiliaryValues(t *testing.T) {
	target, sender, recipient := common.HexToAddress("0x2001"), common.HexToAddress("0x2002"), common.HexToAddress("0x2003")
	id, uri := common.BigToHash(big.NewInt(123)), "ipfs://same-metadata"
	for _, tc := range []struct {
		name, artifact  string
		family          byte
		quantity        uint32
		payment, supply int64
		fresh           bool
		count           int
	}{
		{"backed721", "NewNetworkDRC721", 2, 1, 2, 0, true, 4},
		{"free721", "DRC721Reserveless", 4, 1, 0, 0, true, 3},
		{"backed1155fresh", "MintCandyIsolatedDRC1155", 3, 2, 4, 2, true, 4},
		{"backed1155existing", "MintCandyIsolatedDRC1155", 3, 2, 4, 5, false, 3},
		{"free1155", "DRC1155Reserveless", 5, 2, 0, 0, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asset, err := contracts.CompiledABI(tc.artifact)
			require.NoError(t, err)
			expected, err := userNFTMintLogs(asset, target, sender, recipient, tc.family, id, tc.quantity, uri, big.NewInt(tc.payment), tc.fresh, big.NewInt(tc.supply))
			require.NoError(t, err)
			require.Len(t, expected, tc.count)
			require.NoError(t, requireUserNFTLogs(copyUserNFTLogs(expected), expected))
			for i, event := range expected {
				// A recognized topic must not make malformed or substituted data
				// acceptable. These checks include both reserve snapshots.
				actual := copyUserNFTLogs(expected)
				actual[i].Data = append(actual[i].Data, 0)
				require.Error(t, requireUserNFTLogs(actual, expected))
				actual = copyUserNFTLogs(expected)
				actual[i].Topics = append(actual[i].Topics, common.Hash{})
				require.Error(t, requireUserNFTLogs(actual, expected))
				if event.Topics[0] == crypto.Keccak256Hash([]byte("URI(string,uint256)")) {
					actual = copyUserNFTLogs(expected)
					actual[i].Topics[1] = common.BigToHash(big.NewInt(124))
					require.Error(t, requireUserNFTLogs(actual, expected))
					data, err := asset.Events["URI"].Inputs.NonIndexed().Pack("ipfs://substituted")
					require.NoError(t, err)
					actual = copyUserNFTLogs(expected)
					actual[i].Data = data
					require.Error(t, requireUserNFTLogs(actual, expected))
				}
			}
			if tc.family == 3 {
				for _, index := range []int{tc.count - 3, tc.count - 1} {
					values, err := asset.Events["ReserveUpdated"].Inputs.NonIndexed().Unpack(expected[index].Data)
					require.NoError(t, err)
					require.Equal(t, id.Big(), values[0].(*big.Int))
					snapshot := values[1].(*big.Int)
					wantSupply := tc.supply
					if index == tc.count-3 {
						wantSupply -= int64(tc.quantity)
					}
					require.Equal(t, big.NewInt(wantSupply), snapshot)
					// Canonical ABI, wrong supply: no malformed encoding is needed
					// to exercise strict financial event rejection.
					values[1] = new(big.Int).Add(snapshot, big.NewInt(1))
					data, err := asset.Events["ReserveUpdated"].Inputs.NonIndexed().Pack(values...)
					require.NoError(t, err)
					actual := copyUserNFTLogs(expected)
					actual[index].Data = data
					require.Error(t, requireUserNFTLogs(actual, expected))
				}
			}
		})
	}
}

func TestUserNFTMintEventsRejectInvalidBackingAndFreshSupply(t *testing.T) {
	asset, err := contracts.CompiledABI("MintCandyIsolatedDRC1155")
	require.NoError(t, err)
	for _, tc := range []struct {
		quantity        uint32
		payment, supply int64
		fresh           bool
	}{
		{2, 3, 2, true}, {2, 0, 2, true}, {2, 4, 1, false}, {2, 4, 3, true},
	} {
		_, err := userNFTMintLogs(asset, common.Address{}, common.Address{}, common.Address{}, 3, common.Hash{}, tc.quantity, "ipfs://asset", big.NewInt(tc.payment), tc.fresh, big.NewInt(tc.supply))
		require.Error(t, err)
	}
}
