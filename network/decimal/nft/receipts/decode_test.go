package receipts

import (
	"math/big"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type profileReader map[common.Address]CollectionProfile

func (r profileReader) AuthenticateCollection(_ sdk.Context, address common.Address, _ EntryPoint) (CollectionProfile, bool, error) {
	p, ok := r[address]
	return p, ok, nil
}

func decoderFixture(t *testing.T) (sdk.Context, *Decoder, abi.ABI, abi.ABI, profileReader, common.Address, common.Address) {
	t.Helper()
	ctx := testutil.DefaultContext(storetypes.NewKVStoreKey("nft-decoder"), storetypes.NewTransientStoreKey("transient")).WithChainID("receipt-network-1")
	a721, err := contracts.CompiledABI("DRC721")
	require.NoError(t, err)
	a1155, err := contracts.CompiledABI("DRC1155")
	require.NoError(t, err)
	c721 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	c1155 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	profiles := profileReader{}
	for _, item := range []struct {
		address  common.Address
		standard types.NftType
	}{{c721, types.NftType_NFT721}, {c1155, types.NftType_NFT1155}} {
		profiles[item.address] = CollectionProfile{ChainID: ctx.ChainID(), EVMChainID: 777, Contract: item.address, Factory: common.HexToAddress("0x3333333333333333333333333333333333333333"), NFTType: item.standard, ProxyCodeHash: common.HexToHash("0x01"), ImplementationCodeHash: common.HexToHash("0x02")}
	}
	d, err := NewDecoder(profiles, a721, a1155, Limits{MaxLogs: 32, MaxBytes: 65536, MaxAssets: 8, MaxIDs: 64, MaxUnits: 100})
	require.NoError(t, err)
	return ctx, d, a721, a1155, profiles, c721, c1155
}

func TestCompilerABITransferDecoderOrderingAndBounds(t *testing.T) {
	ctx, d, a721, a1155, _, c721, c1155 := decoderFixture(t)
	from := common.HexToAddress("0x4444444444444444444444444444444444444444")
	to := common.HexToAddress("0x5555555555555555555555555555555555555555")
	word := func(a common.Address) common.Hash { return common.BytesToHash(a.Bytes()) }
	fullID := new(big.Int).Lsh(big.NewInt(1), 200)
	data, err := a1155.Events["TransferBatch"].Inputs.NonIndexed().Pack([]*big.Int{fullID, big.NewInt(7)}, []*big.Int{big.NewInt(2), big.NewInt(3)})
	require.NoError(t, err)
	receipt := &ethtypes.Receipt{Status: 1, TxHash: common.HexToHash("0x99"), Logs: []*ethtypes.Log{
		{Address: c721, Topics: []common.Hash{a721.Events["Transfer"].ID, word(from), word(to), common.BigToHash(fullID)}, Index: 11},
		{Address: c1155, Topics: []common.Hash{a1155.Events["TransferBatch"].ID, word(from), word(from), word(to)}, Data: data, Index: 12},
	}}
	decoded, err := d.Decode(ctx, Execution{EntryPoint: EntryPointSignedEthereum, Receipt: receipt})
	require.NoError(t, err)
	require.Len(t, decoded.Transfers, 3)
	require.Equal(t, common.BigToHash(fullID), decoded.Transfers[0].Asset.TokenID)
	require.Equal(t, common.BigToHash(fullID), decoded.Transfers[1].Asset.TokenID)
	require.Equal(t, MoveRef{LogPosition: 1, ItemPosition: 1}, decoded.Transfers[2].Ref)
	require.Equal(t, uint(12), decoded.Transfers[2].Metadata.Index)
	require.Equal(t, uint32(3), decoded.Transfers[2].Quantity)
	require.Positive(t, ctx.GasMeter().GasConsumed())
	// A late registered malformed payload must not expose earlier partial work.
	receipt.Logs[1].Data = append(receipt.Logs[1].Data, 0)
	decoded, err = d.Decode(ctx, Execution{EntryPoint: EntryPointSignedEthereum, Receipt: receipt})
	require.Error(t, err)
	require.Empty(t, decoded.Transfers)
	require.Nil(t, decoded.Profiles)
}

func TestDecoderKeepsUnsupportedAdmissionAndRoutesClosed(t *testing.T) {
	ctx, d, a721, _, _, c721, _ := decoderFixture(t)
	owner := common.HexToAddress("0x4444444444444444444444444444444444444444")
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{{Address: c721, Topics: []common.Hash{a721.Events["Transfer"].ID, {}, common.BytesToHash(owner.Bytes()), common.HexToHash("0x01")}}}}
	_, err := d.Decode(ctx, Execution{EntryPoint: EntryPointGovernance, Receipt: receipt})
	require.ErrorIs(t, err, ErrAdmissionRequired, "minting requires backing and metadata admission")
	_, err = d.Decode(ctx, Execution{EntryPoint: EntryPointIBC, Receipt: receipt})
	require.ErrorContains(t, err, "unsupported")
	_, err = d.Decode(ctx, Execution{EntryPoint: EntryPointSignedEthereum, Receipt: receipt})
	require.ErrorContains(t, err, "transaction identity")
	receipt.Logs[0] = &ethtypes.Log{Address: c721, Topics: []common.Hash{a721.Events["ReserveUpdated"].ID}}
	_, err = d.Decode(ctx, Execution{EntryPoint: EntryPointGovernance, Receipt: receipt})
	require.ErrorIs(t, err, ErrAdmissionRequired)
	receipt.Status = ethtypes.ReceiptStatusFailed
	decoded, err := d.Decode(ctx, Execution{EntryPoint: EntryPointGovernance, Receipt: receipt})
	require.NoError(t, err)
	require.Empty(t, decoded.Transfers)
}
