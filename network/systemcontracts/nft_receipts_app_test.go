package systemcontracts_test

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

type signedNFTCollection struct {
	Beacon, Implementation common.Address
	Artifact               contracts.Artifact
	Family                 uint8
}

// Test-only approved fixture inputs come from constructor arguments and pinned
// compiler artifacts. This is not a production authentication implementation.
type signedNFTAuthenticator struct {
	App                                                                     *ownedapp.App
	Registry, RegistryImplementation, Factory, FactoryImplementation, Owner common.Address
	Proxy, RegistryABI, FactoryABI                                          contracts.Artifact
	Collections                                                             map[common.Address]signedNFTCollection
}

func (f *signedNFTAuthenticator) read(ctx sdk.Context, target common.Address, contract abi.ABI, method string, args ...interface{}) (common.Address, error) {
	data, err := contract.Pack(method, args...)
	if err != nil {
		return common.Address{}, err
	}
	child, _ := ctx.CacheContext()
	message := core.Message{From: f.Owner, To: &target, Value: new(big.Int), GasLimit: 100000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
	response, err := f.App.EVMKeeper.ApplyMessage(child, statedb.New(child, f.App.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
	if err != nil {
		return common.Address{}, err
	}
	if response == nil || response.GasUsed > message.GasLimit {
		return common.Address{}, errors.New("invalid bounded authentication read")
	}
	ctx.GasMeter().ConsumeGas(response.GasUsed, "NFT signed fixture authentication read")
	if response.VmError != "" || len(response.Ret) != 32 {
		return common.Address{}, errors.New("NFT authentication getter rejected or unbounded")
	}
	values, err := contract.Unpack(method, response.Ret)
	if err != nil {
		return common.Address{}, err
	}
	if len(values) != 1 {
		return common.Address{}, errors.New("unexpected NFT authentication response")
	}
	address, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("unexpected NFT authentication address")
	}
	return address, nil
}

func (f *signedNFTAuthenticator) AuthenticateCollection(ctx sdk.Context, target common.Address, route receipts.EntryPoint) (receipts.CollectionProfile, bool, error) {
	empty := receipts.CollectionProfile{}
	collection, known := f.Collections[target]
	if !known {
		return empty, false, nil
	}
	if err := route.Validate(); err != nil {
		return empty, false, err
	}
	if ctx.ChainID() != "mintcandy_990001-1" {
		return empty, false, errors.New("NFT fixture domain differs")
	}
	slot := common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	for _, binding := range []struct {
		address, implementation common.Address
		artifact                contracts.Artifact
	}{{f.Registry, f.RegistryImplementation, f.RegistryABI}, {f.Factory, f.FactoryImplementation, f.FactoryABI}} {
		word := f.App.EVMKeeper.GetState(ctx, binding.address, slot)
		if !bytes.Equal(word[:12], make([]byte, 12)) || common.BytesToAddress(word[12:]) != binding.implementation || f.App.EVMKeeper.GetCodeHash(ctx, binding.address) != crypto.Keccak256Hash(f.Proxy.Runtime) || f.App.EVMKeeper.GetCodeHash(ctx, binding.implementation) != crypto.Keccak256Hash(binding.artifact.Runtime) {
			return empty, false, errors.New("NFT fixture system proxy binding is stale")
		}
		owner, err := f.read(ctx, binding.address, binding.artifact.ABI, "owner")
		if err != nil {
			return empty, false, err
		}
		if owner != f.Owner {
			return empty, false, errors.New("NFT fixture owner differs")
		}
	}
	factory, err := f.read(ctx, f.Registry, f.RegistryABI.ABI, "getContractAddress", "nft-center")
	if err != nil {
		return empty, false, err
	}
	if factory != f.Factory {
		return empty, false, errors.New("NFT fixture registry factory differs")
	}
	registry, err := f.read(ctx, f.Factory, f.FactoryABI.ABI, "getContractCenter")
	if err != nil {
		return empty, false, err
	}
	if registry != f.Registry {
		return empty, false, errors.New("NFT fixture factory registry differs")
	}
	implementation, err := f.read(ctx, f.Factory, f.FactoryABI.ABI, "readImplementation", collection.Family)
	if err != nil {
		return empty, false, err
	}
	if implementation != collection.Implementation || f.App.EVMKeeper.GetCodeHash(ctx, implementation) != crypto.Keccak256Hash(collection.Artifact.Runtime) {
		return empty, false, errors.New("NFT fixture implementation approval differs")
	}
	kind := uint8(0)
	if collection.Family == 3 || collection.Family == 5 {
		kind = 1
	}
	reserve := collection.Family < 4
	beacon, err := f.read(ctx, f.Factory, f.FactoryABI.ABI, "beacon", kind, reserve)
	if err != nil {
		return empty, false, err
	}
	if beacon != collection.Beacon {
		return empty, false, errors.New("NFT fixture beacon binding differs")
	}
	expectedBeacon, err := contracts.NFTBeaconRuntime(f.Factory, kind, reserve)
	if err != nil {
		return empty, false, err
	}
	expectedProxy, err := contracts.PauseAwareProxyRuntime(beacon, f.Factory, collection.Family)
	if err != nil {
		return empty, false, err
	}
	if !bytes.Equal(expectedBeacon, f.App.EVMKeeper.GetCode(ctx, f.App.EVMKeeper.GetCodeHash(ctx, beacon))) || !bytes.Equal(expectedProxy, f.App.EVMKeeper.GetCode(ctx, f.App.EVMKeeper.GetCodeHash(ctx, target))) {
		return empty, false, errors.New("NFT fixture constructor runtime differs")
	}
	standard := nfttypes.NftType_NFT721
	if kind == 1 {
		standard = nfttypes.NftType_NFT1155
	}
	return receipts.CollectionProfile{ChainID: ctx.ChainID(), EVMChainID: 990001, Contract: target, Factory: f.Factory, NFTType: standard, ReserveLess: !reserve, ProxyCodeHash: crypto.Keccak256Hash(expectedProxy), ImplementationCodeHash: crypto.Keccak256Hash(collection.Artifact.Runtime)}, true, nil
}

func (f *signedNFTAuthenticator) decoder(t *testing.T) *receipts.Decoder {
	t.Helper()
	a721, err := contracts.CompiledABI("DRC721")
	require.NoError(t, err)
	a1155, err := contracts.CompiledABI("DRC1155")
	require.NoError(t, err)
	d, err := receipts.NewDecoder(f, a721, a1155, receipts.Limits{MaxLogs: 32, MaxBytes: 65536, MaxAssets: 8, MaxIDs: 64, MaxUnits: 100})
	require.NoError(t, err)
	return d
}

func signedNFTReceipt(response *evmtypes.MsgEthereumTxResponse) *ethtypes.Receipt {
	return &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, TxHash: common.HexToHash(response.Hash), Logs: evmtypes.LogsToEthereum(response.Logs)}
}

func (f *signedNFTAuthenticator) checkMintAdmissionClosed(t *testing.T, ctx sdk.Context, response *evmtypes.MsgEthereumTxResponse) {
	t.Helper()
	readCtx, _ := ctx.CacheContext()
	readCtx = readCtx.WithGasMeter(storetypes.NewGasMeter(2_000_000))
	decoded, err := f.decoder(t).Decode(readCtx, receipts.Execution{EntryPoint: receipts.EntryPointSignedEthereum, Receipt: signedNFTReceipt(response)})
	require.ErrorIs(t, err, receipts.ErrAdmissionRequired)
	require.Empty(t, decoded.Transfers)
}

func (f *signedNFTAuthenticator) checkTransfer(t *testing.T, ctx sdk.Context, response *evmtypes.MsgEthereumTxResponse, target, from, to common.Address, id *big.Int) {
	t.Helper()
	readCtx, _ := ctx.CacheContext()
	readCtx = readCtx.WithGasMeter(storetypes.NewGasMeter(2_000_000))
	receipt := signedNFTReceipt(response)
	decoded, err := f.decoder(t).Decode(readCtx, receipts.Execution{EntryPoint: receipts.EntryPointSignedEthereum, Receipt: receipt})
	require.NoError(t, err)
	require.Len(t, decoded.Transfers, 1)
	move := decoded.Transfers[0]
	require.Equal(t, target, move.Asset.Contract)
	require.Equal(t, common.BigToHash(id), move.Asset.TokenID)
	require.Equal(t, from, move.From)
	require.Equal(t, to, move.To)
	require.Equal(t, uint32(1), move.Quantity)
	require.Positive(t, readCtx.GasMeter().GasConsumed())
	// Ordinary stale approval validation changes only fixture policy, no chain state.
	wrong := *f
	wrong.FactoryImplementation = common.Address{}
	badCtx, _ := ctx.CacheContext()
	badCtx = badCtx.WithGasMeter(storetypes.NewGasMeter(2_000_000))
	partial, err := wrong.decoder(t).Decode(badCtx, receipts.Execution{EntryPoint: receipts.EntryPointSignedEthereum, Receipt: receipt})
	require.ErrorContains(t, err, "binding is stale")
	require.Empty(t, partial.Transfers)
	t.Logf("actual signed %s NFT transfer decoded with approved factory, owner, implementation and constructor runtime", move.NFTType)
}
