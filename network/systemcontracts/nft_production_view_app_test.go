package systemcontracts_test

import (
	"math/big"
	"testing"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// Only native collection admission is a fixture. The production provider itself
// validates real compiled EVM code, registry state and actual final ownership.
type productionNFTMetadata struct {
	known     *signedNFTAuthenticator
	creator   common.Address
	overrides map[common.Address]nfttypes.Collection
}

func (m productionNFTMetadata) GetCollectionByDRC(_ sdk.Context, address string) (nfttypes.Collection, error) {
	target := common.HexToAddress(address)
	if override, found := m.overrides[target]; found {
		return override, nil
	}
	asset, found := m.known.Collections[target]
	if !found {
		return nfttypes.Collection{}, nftkeeper.ErrNotFound
	}
	standard := nfttypes.NftType_NFT721
	if asset.Family == 3 || asset.Family == 5 {
		standard = nfttypes.NftType_NFT1155
	}
	return nfttypes.Collection{Creator: sdk.AccAddress(m.creator.Bytes()).String(), Denom: "fixture_" + target.Hex()[2:], TypeNft: standard, AddressDRC: target.Hex(), ReserveLess: asset.Family >= 4}, nil
}

type productionNFTViewFixture struct {
	reader   *nftview.Reader
	metadata productionNFTMetadata
	config   nftview.Config
}

func (f *signedNFTAuthenticator) productionView(t *testing.T, delegation, library, creator common.Address) *productionNFTViewFixture {
	t.Helper()
	metadata := productionNFTMetadata{known: f, creator: creator}
	config := nftview.Config{ChainID: "mintcandy_990001-1", EVMChainID: 990001, Registry: f.Registry, NFTCenter: f.Factory, Delegation: delegation, DelegationLibrary: library, EVMGas: 100000, SDKGas: 5_000_000, MaxOwners: 16, MaxQuantity: 10000}
	reader, err := nftview.New(metadata, f.App.EVMKeeper, config)
	require.NoError(t, err)
	return &productionNFTViewFixture{reader: reader, metadata: metadata, config: config}
}

func (f *productionNFTViewFixture) checkRead(t *testing.T, ctx sdk.Context, target, from, to common.Address, id *big.Int, fromCount, toCount uint32) {
	t.Helper()
	child, _ := ctx.CacheContext()
	child = child.WithGasMeter(storetypes.NewGasMeter(15_000_000))
	profile, known, err := f.reader.AuthenticateCollection(child, target, receipts.EntryPointGovernance)
	require.NoError(t, err)
	require.True(t, known)
	authGas := child.GasMeter().GasConsumed()
	require.Equal(t, []common.Address{f.config.Delegation}, profile.Escrows)
	counts, err := f.reader.Ownership(child, profile, receipts.AssetKey{Contract: target, TokenID: common.BigToHash(id)}, []common.Address{from, to})
	require.NoError(t, err)
	require.Equal(t, map[common.Address]uint32{from: fromCount, to: toCount}, counts)
	require.Positive(t, child.GasMeter().GasConsumed())
	combinedGas := child.GasMeter().GasConsumed()
	t.Logf("production NFT %s SDK gas: auth=%d ownership-including-auth=%d combined=%d", profile.NFTType, authGas, combinedGas-authGas, combinedGas)
	higherConfig := f.config
	higherConfig.EVMGas *= 2
	higher, err := nftview.New(f.metadata, f.metadata.known.App.EVMKeeper, higherConfig)
	require.NoError(t, err)
	higherCtx, _ := ctx.CacheContext()
	higherCtx = higherCtx.WithGasMeter(storetypes.NewGasMeter(15_000_000))
	_, higherKnown, err := higher.AuthenticateCollection(higherCtx, target, receipts.EntryPointGovernance)
	require.NoError(t, err)
	require.True(t, higherKnown)
	require.Equal(t, authGas, higherCtx.GasMeter().GasConsumed(), "internal view cost must not grow with the transaction minimum fee floor")
	profile.Escrows[0] = common.Address{}
	_, err = f.reader.Ownership(child, profile, receipts.AssetKey{Contract: target, TokenID: common.BigToHash(id)}, []common.Address{from, to})
	require.ErrorContains(t, err, "profile differs", "a detached caller profile must not alter reader policy")
}

func (f *productionNFTViewFixture) checkTransfer(t *testing.T, ctx sdk.Context, response *evmtypes.MsgEthereumTxResponse, target, from, to common.Address, id *big.Int) {
	t.Helper()
	a721, err := contracts.CompiledABI("DRC721")
	require.NoError(t, err)
	a1155, err := contracts.CompiledABI("DRC1155")
	require.NoError(t, err)
	decoder, err := receipts.NewDecoder(f.reader, a721, a1155, receipts.Limits{MaxLogs: 32, MaxBytes: 65536, MaxAssets: 8, MaxIDs: 64, MaxUnits: 100})
	require.NoError(t, err)
	child, _ := ctx.CacheContext()
	child = child.WithGasMeter(storetypes.NewGasMeter(15_000_000))
	decoded, err := decoder.Decode(child, receipts.Execution{EntryPoint: receipts.EntryPointSignedEthereum, Receipt: signedNFTReceipt(response)})
	require.NoError(t, err)
	require.NotEmpty(t, decoded.Transfers)
	for _, move := range decoded.Transfers {
		require.Equal(t, common.BigToHash(id), move.Asset.TokenID)
	}
	fromCount, toCount := uint32(0), uint32(1)
	if decoded.Transfers[0].NFTType == nfttypes.NftType_NFT1155 {
		fromCount = 1
		if len(response.Logs) > 0 && common.HexToHash(response.Logs[0].Topics[0]) == a1155.Events["TransferBatch"].ID {
			fromCount, toCount = 0, 2
		}
	}
	f.checkRead(t, ctx, target, from, to, id, fromCount, toCount)
	// Ordinary incorrect metadata/domain/budget inputs fail without chain writes.
	wrongConfig := f.config
	wrongConfig.EVMChainID++
	wrong, err := nftview.New(f.metadata, f.metadata.known.App.EVMKeeper, wrongConfig)
	require.NoError(t, err)
	_, _, err = wrong.AuthenticateCollection(child, target, receipts.EntryPointSignedEthereum)
	require.ErrorContains(t, err, "network domain")
	lowConfig := f.config
	lowConfig.SDKGas = 1
	low, err := nftview.New(f.metadata, f.metadata.known.App.EVMKeeper, lowConfig)
	require.NoError(t, err)
	before := child.GasMeter().GasConsumed()
	_, _, err = low.AuthenticateCollection(child, target, receipts.EntryPointSignedEthereum)
	require.ErrorContains(t, err, "SDK gas limit")
	require.Greater(t, child.GasMeter().GasConsumed(), before, "failed bounded reads still charge inherited work")
	collection, err := f.metadata.GetCollectionByDRC(child, target.Hex())
	require.NoError(t, err)
	collection.ReserveLess = !collection.ReserveLess
	wrongMetadata := f.metadata
	wrongMetadata.overrides = map[common.Address]nfttypes.Collection{target: collection}
	wrong, err = nftview.New(wrongMetadata, f.metadata.known.App.EVMKeeper, f.config)
	require.NoError(t, err)
	_, _, err = wrong.AuthenticateCollection(child, target, receipts.EntryPointSignedEthereum)
	require.ErrorContains(t, err, "factory family")
}
