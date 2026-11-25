package app

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

func TestUserNFTBatch(t *testing.T) {
	for _, implementation := range []string{"DRC1155Reserveless", "MintCandyIsolatedDRC1155"} {
		t.Run(implementation, func(t *testing.T) {
			asset, err := contracts.CompiledABI(implementation)
			require.NoError(t, err)
			operator := common.HexToAddress("0x100")
			from := common.HexToAddress("0x200")
			to := common.HexToAddress("0x300")
			target := common.HexToAddress("0x400")
			maxID := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
			for _, size := range []int{1, 2, 32} {
				ids := make([]*big.Int, size)
				quantities := make([]*big.Int, size)
				for i := range ids {
					ids[i] = maxID // repeated IDs reconcile against aggregate ownership
					quantities[i] = big.NewInt(int64(128 / size))
				}
				event, err := userNFTBatchEvent(asset, target, operator, from, to, ids, quantities)
				require.NoError(t, err)
				name := "TransferBatch"
				var values []interface{}
				if size == 1 {
					name = "TransferSingle"
					values = []interface{}{ids[0], quantities[0]}
				} else {
					values = []interface{}{ids, quantities}
				}
				encoded, err := asset.Events[name].Inputs.NonIndexed().Pack(values...)
				require.NoError(t, err)
				require.Equal(t, target, event.Address)
				require.Equal(t, []common.Hash{asset.Events[name].ID, customAddressWord(operator), customAddressWord(from), customAddressWord(to)}, event.Topics)
				require.Equal(t, encoded, event.Data)
			}
			one := big.NewInt(1)
			for _, bad := range []struct{ ids, amounts []*big.Int }{
				{nil, nil}, {[]*big.Int{one}, nil},
				{make([]*big.Int, 33), make([]*big.Int, 33)},
				{[]*big.Int{nil}, []*big.Int{one}},
				{[]*big.Int{new(big.Int).Add(maxID, one)}, []*big.Int{one}},
				{[]*big.Int{one}, []*big.Int{new(big.Int)}},
				{[]*big.Int{one}, []*big.Int{new(big.Int).Lsh(one, 64)}},
				{[]*big.Int{one, one}, []*big.Int{big.NewInt(128), one}},
			} {
				_, err := userNFTBatchEvent(asset, target, operator, from, to, bad.ids, bad.amounts)
				require.Error(t, err)
			}
		})
	}
}
