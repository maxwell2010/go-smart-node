package authority

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/authority/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

const (
	MaxCalls           = 8
	MaxCallGas  uint64 = 2_000_000
	MaxBatchGas uint64 = 8_000_000
	ReadGas     uint64 = 100_000
	NativeGas   uint64 = 1_000_000
)

// ERC1967 implementation slot, from the pinned verified ERC1967Utils source.
var implementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")

type Role struct {
	Address                 common.Address
	ProxyCodeHash           common.Hash
	ApprovedImplementations []common.Hash
	ApprovedChildren        map[string][]common.Hash
}
type Manifest struct {
	Registry Role
	Roles    map[string]Role
}

type Keeper struct {
	evm      *evmkeeper.Keeper
	manifest Manifest
	hooks    evmtypes.EvmHooks
	owner    common.Address
}

var _ types.MsgServer = (*Keeper)(nil)

func NewKeeper(evm *evmkeeper.Keeper, manifest Manifest, hooks evmtypes.EvmHooks) (*Keeper, error) {
	if evm == nil || hooks == nil || nilHook(hooks) || len(manifest.Roles) == 0 {
		return nil, errors.New("authority requires EVM, native receipt pipeline and reviewed manifest")
	}
	copyRole := func(r Role) (Role, error) {
		if r.Address == (common.Address{}) || r.ProxyCodeHash == (common.Hash{}) || len(r.ApprovedImplementations) == 0 || len(r.ApprovedImplementations) > 64 || len(r.ApprovedChildren) > 6 {
			return Role{}, errors.New("incomplete authority role binding")
		}
		r.ApprovedImplementations = append([]common.Hash(nil), r.ApprovedImplementations...)
		children := map[string][]common.Hash{}
		for slot, hashes := range r.ApprovedChildren {
			if len(slot) > 48 || len(hashes) == 0 || len(hashes) > 64 {
				return Role{}, errors.New("invalid child implementation policy")
			}
			children[slot] = append([]common.Hash(nil), hashes...)
			for _, hash := range hashes {
				if hash == (common.Hash{}) {
					return Role{}, errors.New("zero child code hash")
				}
			}
		}
		r.ApprovedChildren = children
		for _, h := range r.ApprovedImplementations {
			if h == (common.Hash{}) {
				return Role{}, errors.New("zero approved code hash")
			}
		}
		return r, nil
	}
	registry, err := copyRole(manifest.Registry)
	if err != nil {
		return nil, err
	}
	owned := Manifest{Registry: registry, Roles: map[string]Role{}}
	seen := map[common.Address]bool{registry.Address: true}
	for role, r := range manifest.Roles {
		if _, ok := roleABI(role); !ok {
			return nil, fmt.Errorf("unsupported system role %q", role)
		}
		if seen[r.Address] {
			return nil, errors.New("duplicate authority target address")
		}
		seen[r.Address] = true
		if err := validateChildSlots(role, r.ApprovedChildren); err != nil {
			return nil, err
		}
		value, err := copyRole(r)
		if err != nil {
			return nil, err
		}
		owned.Roles[role] = value
	}
	return &Keeper{evm: evm, manifest: owned, hooks: hooks, owner: common.BytesToAddress(authtypes.NewModuleAddress("gov"))}, nil
}

func validateChildSlots(role string, slots map[string][]common.Hash) error {
	allowed := map[string]map[string]bool{
		"token-center": {"upgradeToken": true, "upgradeTokenReserveless": true},
		"nft-center":   {"upgradeNFT:0:true": true, "upgradeNFT:0:false": true, "upgradeNFT:1:true": true, "upgradeNFT:1:false": true},
	}
	for slot := range slots {
		if !allowed[role][slot] {
			return fmt.Errorf("invalid child implementation slot %q for role %q", slot, role)
		}
	}
	return nil
}

func nilHook(hooks evmtypes.EvmHooks) bool {
	value := reflect.ValueOf(hooks)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func roleABI(role string) (string, bool) {
	name, ok := map[string]string{"master-validator": "NewNetworkMasterValidator", "delegation": "DecimalDelegation", "delegation-nft": "DecimalDelegationNFT", "token-center": "DecimalTokenCenter", "nft-center": "DecimalNFTCenter"}[role]
	return name, ok
}

func approved(hash common.Hash, hashes []common.Hash) bool {
	for _, h := range hashes {
		if h == hash {
			return true
		}
	}
	return false
}

func (k *Keeper) invoke(ctx sdk.Context, target common.Address, data []byte, gas uint64, commit bool) (*evmtypes.MsgEthereumTxResponse, core.Message, error) {
	msg := core.Message{From: k.owner, To: &target, Value: new(big.Int), GasLimit: gas, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: append([]byte(nil), data...), SkipNonceChecks: true, SkipFromEOACheck: true}
	result, err := k.evm.ApplyMessage(ctx, statedb.New(ctx, k.evm, statedb.NewEmptyTxConfig()), msg, nil, commit, false, true)
	if err != nil {
		return nil, msg, err
	}
	if result == nil || result.GasUsed > gas {
		return nil, msg, errors.New("invalid internal EVM gas result")
	}
	ctx.GasMeter().ConsumeGas(result.GasUsed, "governance internal EVM work")
	if result.VmError != "" {
		return nil, msg, fmt.Errorf("system contract execution reverted: %s", result.VmError)
	}
	return result, msg, nil
}

func (k *Keeper) read(ctx sdk.Context, target common.Address, contractABI abi.ABI, method string, args ...interface{}) ([]interface{}, error) {
	data, err := contractABI.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	disposable, _ := ctx.CacheContext()
	result, _, err := k.invoke(disposable, target, data, ReadGas, false)
	if err != nil {
		return nil, err
	}
	if len(result.Ret) != 32 {
		return nil, errors.New("system getter response exceeds bound")
	}
	return contractABI.Unpack(method, result.Ret)
}

func (k *Keeper) checkProxy(ctx sdk.Context, r Role) (common.Address, common.Hash, error) {
	if k.evm.GetCodeHash(ctx, r.Address) != r.ProxyCodeHash {
		return common.Address{}, common.Hash{}, errors.New("system proxy code differs from manifest")
	}
	word := k.evm.GetState(ctx, r.Address, implementationSlot)
	if !bytes.Equal(word[:12], make([]byte, 12)) {
		return common.Address{}, common.Hash{}, errors.New("noncanonical ERC1967 implementation word")
	}
	implementation := common.BytesToAddress(word[12:])
	hash := k.evm.GetCodeHash(ctx, implementation)
	if implementation == (common.Address{}) || !approved(hash, r.ApprovedImplementations) || len(k.evm.GetCode(ctx, hash)) == 0 {
		return common.Address{}, common.Hash{}, errors.New("system implementation is not approved")
	}
	ownerABI, err := contracts.CompiledABI("DecimalContractCenter")
	if err != nil {
		return common.Address{}, common.Hash{}, err
	}
	owner, err := k.read(ctx, r.Address, ownerABI, "owner")
	if err != nil {
		return common.Address{}, common.Hash{}, err
	}
	if len(owner) != 1 || owner[0] != (interface{})(k.owner) {
		return common.Address{}, common.Hash{}, errors.New("system contract owner is not governance")
	}
	return implementation, hash, nil
}

func (k *Keeper) ExecuteSystemContract(goctx context.Context, msg *types.MsgExecuteSystemContract) (response *types.MsgExecuteSystemContractResponse, err error) {
	if k == nil || msg == nil {
		return nil, errors.New("missing authority request")
	}
	codec := address.NewBech32Codec(identity.Bech32PrefixAccAddr)
	signer, err := codec.StringToBytes(msg.Authority)
	if err != nil || !bytes.Equal(signer, k.owner.Bytes()) {
		return nil, errors.New("only governance module authority may execute system contracts")
	}
	if len(msg.Calls) == 0 || len(msg.Calls) > MaxCalls {
		return nil, errors.New("invalid authority call count")
	}
	var gas uint64
	for _, call := range msg.Calls {
		if call == nil || len(call.Role) == 0 || len(call.Role) > 32 || call.GasLimit == 0 || call.GasLimit > MaxCallGas || call.GasLimit > MaxBatchGas-gas || len(call.Data) < 4 || len(call.Data) > 32768 {
			return nil, errors.New("authority call exceeds policy bounds")
		}
		gas += call.GasLimit
	}
	ctx := sdk.UnwrapSDKContext(goctx)
	if err := reserve(ctx, gas+uint64(len(msg.Calls))*(4*ReadGas+NativeGas)); err != nil {
		return nil, err
	}
	cached, write := ctx.CacheContext()
	meter := storetypes.NewGasMeter(gas + uint64(len(msg.Calls))*(4*ReadGas+NativeGas))
	cached = cached.WithGasMeter(meter)
	charged := false
	charge := func() {
		if !charged {
			charged = true
			ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "governance authority batch")
		}
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			switch recovered.(type) {
			case storetypes.ErrorOutOfGas:
				err = errors.New("authority work limit exhausted")
				response = nil
			default:
				panic(recovered)
			}
		}
		charge()
	}()
	var used uint64
	for _, call := range msg.Calls {
		executed, err := k.execute(cached, call)
		if err != nil {
			return nil, err
		}
		used += executed
	}
	charge()
	write()
	return &types.MsgExecuteSystemContractResponse{GasUsed: used}, nil
}

func (k *Keeper) execute(ctx sdk.Context, call *types.SystemCall) (uint64, error) {
	name, ok := roleABI(call.Role)
	binding, configured := k.manifest.Roles[call.Role]
	if !ok || !configured {
		return 0, errors.New("unconfigured authority role")
	}
	if len(call.Target) != 20 || len(call.TargetCodeHash) != 32 || len(call.Implementation) != 20 || len(call.ImplementationCodeHash) != 32 {
		return 0, errors.New("authority binding hashes or addresses have invalid lengths")
	}
	if common.BytesToAddress(call.Target) != binding.Address || common.BytesToHash(call.TargetCodeHash) != binding.ProxyCodeHash {
		return 0, errors.New("authority target differs from reviewed binding")
	}
	if _, _, err := k.checkProxy(ctx, k.manifest.Registry); err != nil {
		return 0, err
	}
	registryABI, err := contracts.CompiledABI("DecimalContractCenter")
	if err != nil {
		return 0, err
	}
	current, err := k.read(ctx, k.manifest.Registry.Address, registryABI, "getContractAddress", call.Role)
	if err != nil {
		return 0, err
	}
	if len(current) != 1 || current[0] != (interface{})(binding.Address) {
		return 0, errors.New("system registry binding changed")
	}
	implementation, hash, err := k.checkProxy(ctx, binding)
	if err != nil {
		return 0, err
	}
	if implementation != common.BytesToAddress(call.Implementation) || hash != common.BytesToHash(call.ImplementationCodeHash) {
		return 0, errors.New("system implementation changed after proposal construction")
	}
	compiled, err := contracts.CompiledABI(name)
	if err != nil {
		return 0, err
	}
	method, err := compiled.MethodById(call.Data[:4])
	if err != nil {
		return 0, err
	}
	allowed := map[string]bool{"pause": true, "unpause": true, "upgrade": true, "setRewardPerBlock": call.Role == "master-validator", "clearRewardPerBlock": call.Role == "master-validator", "upgradeToken": call.Role == "token-center", "upgradeTokenReserveless": call.Role == "token-center", "upgradeNFT": call.Role == "nft-center"}
	if !allowed[method.RawName] {
		return 0, errors.New("system method is not approved for authority execution")
	}
	arguments, err := method.Inputs.Unpack(call.Data[4:])
	if err != nil {
		return 0, err
	}
	canonical, err := method.Inputs.Pack(arguments...)
	if err != nil || !bytes.Equal(canonical, call.Data[4:]) {
		return 0, errors.New("noncanonical authority ABI calldata")
	}
	isUpgrade := method.RawName == "upgrade" || method.RawName == "upgradeToken" || method.RawName == "upgradeTokenReserveless" || method.RawName == "upgradeNFT"
	if isUpgrade {
		if len(call.ReplacementCodeHash) != 32 {
			return 0, errors.New("upgrade requires reviewed replacement code hash")
		}
		target, ok := arguments[0].(common.Address)
		if !ok || target == (common.Address{}) {
			return 0, errors.New("invalid upgrade implementation")
		}
		expected := common.BytesToHash(call.ReplacementCodeHash)
		slot := method.RawName
		if method.RawName == "upgradeNFT" {
			slot = fmt.Sprintf("upgradeNFT:%d:%t", arguments[1].(uint8), arguments[2].(bool))
		}
		allowedHashes := binding.ApprovedChildren[slot]
		if method.RawName == "upgrade" {
			allowedHashes = binding.ApprovedImplementations
			if len(arguments) != 2 || len(arguments[1].([]byte)) != 0 {
				return 0, errors.New("delegatecall migration requires a separately reviewed upgrade path")
			}
		}
		if !approved(expected, allowedHashes) || k.evm.GetCodeHash(ctx, target) != expected || len(k.evm.GetCode(ctx, expected)) == 0 {
			return 0, errors.New("replacement implementation is not approved")
		}
	} else if len(call.ReplacementCodeHash) != 0 {
		return 0, errors.New("unexpected replacement code hash")
	}
	result, message, err := k.invoke(ctx, binding.Address, call.Data, call.GasLimit, true)
	if err != nil {
		return 0, err
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, GasUsed: result.GasUsed, Logs: evmtypes.LogsToEthereum(result.Logs)}
	if err := k.hooks.PostTxProcessing(ctx, k.owner, message, receipt); err != nil {
		return 0, fmt.Errorf("native system receipt failed: %w", err)
	}
	if _, _, err := k.checkProxy(ctx, binding); err != nil {
		return 0, err
	}
	if len(receipt.Logs) > 256 {
		return 0, errors.New("authority receipt log count exceeds policy")
	}
	var logs []*types.SystemLog
	bytesUsed := 0
	for _, log := range receipt.Logs {
		if log == nil || len(log.Topics) > 4 {
			return 0, errors.New("invalid authority receipt log")
		}
		bytesUsed += len(log.Data) + 20 + 32*len(log.Topics)
		if bytesUsed > 32768 {
			return 0, errors.New("authority receipt log data exceeds policy")
		}
		entry := &types.SystemLog{Address: log.Address.Bytes(), Data: append([]byte(nil), log.Data...)}
		for _, topic := range log.Topics {
			entry.Topics = append(entry.Topics, topic.Bytes())
		}
		logs = append(logs, entry)
	}
	if err := ctx.EventManager().EmitTypedEvent(&types.EventSystemContractExecuted{Role: call.Role, Target: binding.Address.Hex(), Selector: hex.EncodeToString(call.Data[:4]), GasUsed: result.GasUsed, Logs: logs}); err != nil {
		return 0, err
	}
	return result.GasUsed, nil
}
