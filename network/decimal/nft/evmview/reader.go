// Package evmview authenticates current-release fresh NFT code and reads final
// ownership. It does not admit collections, mint native units or project reserves.
package evmview

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

type Metadata interface {
	GetCollectionByDRC(sdk.Context, string) (types.Collection, error)
}

// ProtocolProfile selects one explicitly reviewed implementation family.
type ProtocolProfile uint8

const (
	ProfileSharedReference ProtocolProfile = iota
	ProfileIsolatedPositionsV1
)

// Config is an explicit current-release manifest. All expected code derives from
// compiler artifacts and these known constructor/link inputs, never observed code.
// A different implementation family requires an independently reviewed release.
type Config struct {
	Protocol          ProtocolProfile
	MasterValidator   common.Address
	PositionVault     common.Address
	WrappedDEL        common.Address
	ChainID           string
	EVMChainID        uint64
	Registry          common.Address
	NFTCenter         common.Address
	Delegation        common.Address
	DelegationLibrary common.Address
	EVMGas            uint64
	SDKGas            uint64
	MaxOwners         uint32
	MaxQuantity       uint32
}

type Reader struct {
	metadata                             Metadata
	evm                                  *evmkeeper.Keeper
	config                               Config
	owner                                common.Address
	registry, factory, delegation, proxy contracts.Artifact
	vault, master, wrapper               contracts.Artifact
	children                             map[uint8]contracts.Artifact
	libraryRuntime                       []byte
}

var _ receipts.Authenticator = (*Reader)(nil)
var _ receipts.FinalReader = (*Reader)(nil)

func nilDependency(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func New(metadata Metadata, evm *evmkeeper.Keeper, config Config) (*Reader, error) {
	if nilDependency(metadata) || evm == nil {
		return nil, errors.New("NFT view requires native metadata and an owned EVM keeper")
	}
	if err := types.ValidateExternalDomain(config.ChainID, config.EVMChainID); err != nil {
		return nil, err
	}
	if config.Protocol != ProfileSharedReference && config.Protocol != ProfileIsolatedPositionsV1 {
		return nil, errors.New("unknown NFT protocol profile")
	}
	if config.Protocol == ProfileSharedReference && (config.PositionVault != (common.Address{}) || config.WrappedDEL != (common.Address{}) || config.MasterValidator != (common.Address{})) {
		return nil, errors.New("shared reference profile cannot carry isolated role bindings")
	}
	addresses := []common.Address{config.Registry, config.NFTCenter, config.Delegation, config.DelegationLibrary}
	if config.Protocol == ProfileIsolatedPositionsV1 {
		addresses = append(addresses, config.PositionVault, config.WrappedDEL)
		if config.MasterValidator != (common.Address{}) {
			addresses = append(addresses, config.MasterValidator)
		}
	}
	seen := make(map[common.Address]bool)
	for _, address := range addresses {
		if address == (common.Address{}) || seen[address] {
			return nil, errors.New("NFT view requires distinct explicit system and library addresses")
		}
		seen[address] = true
	}
	if config.EVMGas < 21000 || config.EVMGas > 5_000_000 || config.SDKGas == 0 || config.SDKGas > 20_000_000 || config.MaxOwners == 0 || config.MaxOwners > 256 || config.MaxQuantity == 0 || config.MaxQuantity > 10000 {
		return nil, errors.New("NFT view requires bounded EVM/SDK and ownership work limits")
	}
	r := &Reader{metadata: metadata, evm: evm, config: config, owner: common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName)), children: make(map[uint8]contracts.Artifact)}
	var err error
	if r.registry, err = contracts.Load("DecimalContractCenter"); err != nil {
		return nil, err
	}
	if r.factory, err = contracts.Load("NewNetworkNFTCenter"); err != nil {
		return nil, err
	}
	if r.proxy, err = contracts.Load("DecimalContractCenterProxy"); err != nil {
		return nil, err
	}
	delegationName := "MintCandyDelegation"
	if config.Protocol == ProfileIsolatedPositionsV1 {
		delegationName = "MintCandyIsolatedDelegation"
		if r.vault, err = contracts.Load("MintCandyNFTPositionVault"); err != nil {
			return nil, err
		}
		if r.master, err = contracts.Load("NewNetworkMasterValidator"); err != nil {
			return nil, err
		}
		if r.wrapper, err = contracts.Load("MintCandyWrappedDEL"); err != nil {
			return nil, err
		}
	}
	if r.delegation, err = contracts.LoadLinked(delegationName, map[string]common.Address{"owned/DecimalDelegation.sol:DelegationNFTLib": config.DelegationLibrary}); err != nil {
		return nil, err
	}
	r.libraryRuntime, err = contracts.ExpectedRuntime("MintCandyDelegationNFTLib", nil, map[string]common.Hash{"owned/DecimalDelegation.sol:DelegationNFTLib.library_deploy_address": common.BytesToHash(config.DelegationLibrary.Bytes())})
	if err != nil {
		return nil, err
	}
	families := map[uint8]string{2: "NewNetworkDRC721", 3: "NewNetworkDRC1155", 4: "DRC721Reserveless", 5: "DRC1155Reserveless"}
	if config.Protocol == ProfileIsolatedPositionsV1 {
		families[3] = "MintCandyIsolatedDRC1155"
	}
	for family, name := range families {
		artifact, err := contracts.Load(name)
		if err != nil {
			return nil, err
		}
		r.children[family] = artifact
	}
	return r, nil
}

// bounded always discards reads and charges the inherited caller meter, including
// on local read-budget exhaustion. Outer transaction gas exhaustion propagates.
func (r *Reader) bounded(ctx sdk.Context, read func(sdk.Context) error) (err error) {
	if r == nil || r.evm == nil {
		return errors.New("NFT view is not initialized")
	}
	child, _ := ctx.CacheContext()
	meter := storetypes.NewGasMeter(r.config.SDKGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); ok {
				err = errors.New("NFT view SDK gas limit exceeded")
			} else {
				panic(recovered)
			}
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "bounded native NFT view")
	}()
	return read(child)
}

func (r *Reader) domain(ctx sdk.Context) error {
	chain := evmtypes.GetChainConfig()
	if ctx.ChainID() != r.config.ChainID || chain == nil || chain.ChainId != r.config.EVMChainID {
		return errors.New("NFT view differs from actual configured network domain")
	}
	return nil
}

func (r *Reader) code(ctx sdk.Context, address common.Address, expected []byte) error {
	if address == (common.Address{}) || len(expected) == 0 || !bytes.Equal(expected, r.evm.GetCode(ctx, r.evm.GetCodeHash(ctx, address))) {
		return errors.New("NFT view runtime differs from current compiler manifest")
	}
	return nil
}

// Standard ERC1967 implementation slot from the pinned ERC1967Utils source.
var implementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")

func (r *Reader) system(ctx sdk.Context, target common.Address, artifact contracts.Artifact) error {
	if err := r.code(ctx, target, r.proxy.Runtime); err != nil {
		return err
	}
	word := r.evm.GetState(ctx, target, implementationSlot)
	if !bytes.Equal(word[:12], make([]byte, 12)) {
		return errors.New("noncanonical NFT system implementation word")
	}
	if err := r.code(ctx, common.BytesToAddress(word[12:]), artifact.Runtime); err != nil {
		return err
	}
	owner, err := r.address(ctx, target, artifact.ABI, "owner")
	if err != nil {
		return err
	}
	if owner != r.owner {
		return errors.New("NFT system ownership differs from governance module")
	}
	return nil
}

func (r *Reader) read(ctx sdk.Context, target common.Address, contract abi.ABI, method string, expectedBytes int, args ...interface{}) ([]interface{}, error) {
	data, err := contract.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	child, _ := ctx.CacheContext()
	message := core.Message{From: r.owner, To: &target, Value: new(big.Int), GasLimit: r.config.EVMGas, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
	response, err := r.evm.ApplyMessage(child, statedb.New(child, r.evm, statedb.NewEmptyTxConfig()), message, nil, false, false, true)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("invalid bounded NFT getter gas result")
	}
	usedGas := response.GasUsed
	if response.MaxUsedGas > usedGas {
		usedGas = response.MaxUsedGas
	}
	if usedGas > r.config.EVMGas {
		return nil, errors.New("invalid bounded NFT getter gas result")
	}
	ctx.GasMeter().ConsumeGas(usedGas, "NFT final getter EVM work before refunds")
	if response.VmError != "" {
		return nil, fmt.Errorf("NFT getter reverted: %s", response.VmError)
	}
	if len(response.Ret) != expectedBytes {
		return nil, errors.New("NFT getter response exceeds exact ABI bound")
	}
	values, err := contract.Unpack(method, response.Ret)
	if err != nil {
		return nil, err
	}
	encoded, err := contract.Methods[method].Outputs.Pack(values...)
	if err != nil || !bytes.Equal(encoded, response.Ret) {
		return nil, errors.New("NFT getter response is not canonical ABI")
	}
	return values, nil
}

func (r *Reader) address(ctx sdk.Context, target common.Address, contract abi.ABI, method string, args ...interface{}) (common.Address, error) {
	values, err := r.read(ctx, target, contract, method, 32, args...)
	if err != nil {
		return common.Address{}, err
	}
	if len(values) != 1 {
		return common.Address{}, errors.New("NFT getter returned unexpected values")
	}
	value, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, errors.New("NFT getter did not return an address")
	}
	return value, nil
}

func (r *Reader) AuthenticateCollection(ctx sdk.Context, target common.Address, route receipts.EntryPoint) (profile receipts.CollectionProfile, known bool, err error) {
	if err = route.Validate(); err != nil {
		return profile, false, err
	}
	err = r.bounded(ctx, func(child sdk.Context) error {
		var e error
		profile, known, e = r.authenticate(child, target)
		return e
	})
	if err != nil {
		return receipts.CollectionProfile{}, false, err
	}
	return profile, known, nil
}

func (r *Reader) authenticate(ctx sdk.Context, target common.Address) (receipts.CollectionProfile, bool, error) {
	empty := receipts.CollectionProfile{}
	if err := r.domain(ctx); err != nil {
		return empty, false, err
	}
	collection, err := r.metadata.GetCollectionByDRC(ctx, target.Hex())
	if errors.Is(err, nftkeeper.ErrNotFound) {
		return empty, false, nil
	}
	if err != nil {
		return empty, false, err
	}
	if err := collection.Validate(); err != nil {
		return empty, false, err
	}
	if !common.IsHexAddress(collection.AddressDRC) || common.HexToAddress(collection.AddressDRC) != target {
		return empty, false, errors.New("NFT native collection contract differs")
	}
	kind := uint8(0)
	switch collection.TypeNft {
	case types.NftType_NFT721:
	case types.NftType_NFT1155:
		kind = 1
	default:
		return empty, false, errors.New("NFT native collection has no supported standard")
	}
	family := uint8(2) + kind
	if collection.ReserveLess {
		family = 4 + kind
	}
	asset := r.children[family]
	if err := r.systems(ctx); err != nil {
		return empty, false, err
	}
	if r.config.Protocol == ProfileIsolatedPositionsV1 && target == r.config.PositionVault {
		if kind != 0 || collection.ReserveLess {
			return empty, false, errors.New("isolated vault must be one backed ERC721 collection")
		}
		if err := r.authenticateVault(ctx); err != nil {
			return empty, false, err
		}
		return receipts.CollectionProfile{ChainID: r.config.ChainID, EVMChainID: r.config.EVMChainID, Contract: target, Factory: r.config.NFTCenter, NFTType: types.NftType_NFT721, ProxyCodeHash: crypto.Keccak256Hash(r.proxy.Runtime), ImplementationCodeHash: crypto.Keccak256Hash(r.vault.Runtime), Escrows: []common.Address{r.config.Delegation}}, true, nil
	}
	expectedProxy, err := r.checkChild(ctx, target, kind, !collection.ReserveLess, family)
	if err != nil {
		return empty, false, err
	}
	return receipts.CollectionProfile{ChainID: r.config.ChainID, EVMChainID: r.config.EVMChainID, Contract: target, Factory: r.config.NFTCenter, NFTType: collection.TypeNft, ReserveLess: collection.ReserveLess, ProxyCodeHash: crypto.Keccak256Hash(expectedProxy), ImplementationCodeHash: crypto.Keccak256Hash(asset.Runtime), Escrows: []common.Address{r.config.Delegation}}, true, nil
}

func (r *Reader) checkChild(ctx sdk.Context, target common.Address, kind uint8, withReserve bool, family uint8) ([]byte, error) {
	asset := r.children[family]
	state, err := r.read(ctx, r.config.NFTCenter, r.factory.ABI, "getNFTState", 96, target)
	if err != nil {
		return nil, err
	}
	type nftState struct {
		Active      bool
		NftType     uint8
		WithReserve bool
	}
	if len(state) != 1 {
		return nil, errors.New("invalid NFT factory collection state")
	}
	registered := abi.ConvertType(state[0], new(nftState)).(*nftState)
	if !registered.Active || registered.NftType != kind || registered.WithReserve != withReserve {
		return nil, errors.New("NFT collection is not active in approved factory family")
	}
	implementation, err := r.address(ctx, r.config.NFTCenter, r.factory.ABI, "readImplementation", family)
	if err != nil {
		return nil, err
	}
	if err := r.code(ctx, implementation, asset.Runtime); err != nil {
		return nil, err
	}
	beacon, err := r.address(ctx, r.config.NFTCenter, r.factory.ABI, "beacon", kind, withReserve)
	if err != nil {
		return nil, err
	}
	expectedBeacon, err := contracts.NFTBeaconRuntime(r.config.NFTCenter, kind, withReserve)
	if err != nil {
		return nil, err
	}
	if err := r.code(ctx, beacon, expectedBeacon); err != nil {
		return nil, err
	}
	expectedProxy, err := contracts.PauseAwareProxyRuntime(beacon, r.config.NFTCenter, family)
	if err != nil {
		return nil, err
	}
	if err := r.code(ctx, target, expectedProxy); err != nil {
		return nil, err
	}
	return expectedProxy, nil
}

func (r *Reader) systems(ctx sdk.Context) error {
	for _, binding := range []struct {
		target   common.Address
		artifact contracts.Artifact
	}{{r.config.Registry, r.registry}, {r.config.NFTCenter, r.factory}, {r.config.Delegation, r.delegation}} {
		if err := r.system(ctx, binding.target, binding.artifact); err != nil {
			return err
		}
	}
	if err := r.code(ctx, r.config.DelegationLibrary, r.libraryRuntime); err != nil {
		return err
	}
	for _, binding := range []struct {
		label    string
		expected common.Address
	}{{"nft-center", r.config.NFTCenter}, {"delegation", r.config.Delegation}, {"delegation-nft", r.config.Delegation}} {
		actual, err := r.address(ctx, r.config.Registry, r.registry.ABI, "getContractAddress", binding.label)
		if err != nil {
			return err
		}
		if actual != binding.expected {
			return errors.New("NFT registry routing differs from approved unified profile")
		}
	}
	center, err := r.address(ctx, r.config.NFTCenter, r.factory.ABI, "getContractCenter")
	if err != nil {
		return err
	}
	if center != r.config.Registry {
		return errors.New("NFT factory registry binding differs")
	}

	gatewayRegistry, err := r.address(ctx, r.config.Delegation, r.delegation.ABI, "getContractCenter")
	if err != nil {
		return err
	}
	if gatewayRegistry != r.config.Registry {
		return errors.New("NFT Gateway registry binding differs")
	}

	return nil
}

func (r *Reader) Ownership(ctx sdk.Context, expected receipts.CollectionProfile, key receipts.AssetKey, owners []common.Address) (values map[common.Address]uint32, err error) {
	if r == nil || uint64(len(owners)) > uint64(r.config.MaxOwners) || len(owners) == 0 {
		return nil, errors.New("invalid NFT ownership reader owner count")
	}
	err = r.bounded(ctx, func(child sdk.Context) error {
		profile, known, e := r.authenticate(child, key.Contract)
		if e != nil {
			return e
		}
		if !known || !sameProfile(profile, expected) {
			return errors.New("NFT ownership profile differs from live approved collection")
		}
		family := uint8(2)
		if profile.NFTType == types.NftType_NFT1155 {
			family++
		}
		if profile.ReserveLess {
			family += 2
		}
		asset := r.children[family]
		values = make(map[common.Address]uint32, len(owners))
		var holder common.Address
		if profile.NFTType == types.NftType_NFT721 {
			holder, e = r.address(child, key.Contract, asset.ABI, "ownerOf", key.TokenID.Big())
			if e != nil {
				return e
			}
			if holder == (common.Address{}) {
				return errors.New("ERC721 final owner is zero")
			}
		}
		for _, owner := range owners {
			if owner == (common.Address{}) {
				return errors.New("NFT ownership reader cannot query zero owner")
			}
			if _, duplicate := values[owner]; duplicate {
				return errors.New("NFT ownership reader received duplicate owners")
			}
			if profile.NFTType == types.NftType_NFT721 {
				var value uint32
				if owner == holder {
					value = 1
				}
				values[owner] = value
				continue
			}
			response, e := r.read(child, key.Contract, asset.ABI, "balanceOf", 32, owner, key.TokenID.Big())
			if e != nil {
				return e
			}
			if len(response) != 1 {
				return errors.New("unexpected ERC1155 balance response")
			}
			amount, ok := response[0].(*big.Int)
			if !ok || amount == nil || amount.Sign() < 0 || amount.BitLen() > 32 || amount.Uint64() > uint64(r.config.MaxQuantity) {
				return errors.New("ERC1155 balance exceeds native ownership bounds")
			}
			values[owner] = uint32(amount.Uint64())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return values, nil
}

func sameProfile(a, b receipts.CollectionProfile) bool {
	if a.ChainID != b.ChainID || a.EVMChainID != b.EVMChainID || a.Contract != b.Contract || a.Factory != b.Factory || a.NFTType != b.NFTType || a.ReserveLess != b.ReserveLess || a.ProxyCodeHash != b.ProxyCodeHash || a.ImplementationCodeHash != b.ImplementationCodeHash || len(a.Escrows) != len(b.Escrows) {
		return false
	}
	for i := range a.Escrows {
		if a.Escrows[i] != b.Escrows[i] {
			return false
		}
	}
	return true
}
