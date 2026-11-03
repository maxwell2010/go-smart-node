package evmview

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
)

type BindingKind uint8

const (
	DirectRuntime BindingKind = 1
	ERC1967Proxy  BindingKind = 2
	BeaconProxy   BindingKind = 3
)

// Binding names an explicitly approved deployment, not a self-authenticating
// address/hash read from an untrusted event. BeaconImplementation is the
// beacon factory's own EIP1967 implementation; Implementation is token logic.
type Binding struct {
	Kind                                        BindingKind
	Address, Implementation                     common.Address
	RuntimeHash, ImplementationHash             common.Hash
	Beacon, BeaconImplementation                common.Address
	BeaconRuntimeHash, BeaconImplementationHash common.Hash
}

var implementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
var beaconSlot = common.HexToHash("0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50")

func viewSelector(compiled abi.ABI, name string, kind byte, size int) ([]byte, error) {
	method, ok := compiled.Methods[name]
	signature := name + "()"
	if !ok || method.Sig != signature || len(method.Inputs) != 0 || len(method.Outputs) != 1 || method.Outputs[0].Type.T != kind || method.Outputs[0].Type.Size != size || (method.StateMutability != "view" && method.StateMutability != "pure") || !bytes.Equal(method.ID, crypto.Keccak256([]byte(signature))[:4]) {
		return nil, fmt.Errorf("unexpected compiled %s ABI", name)
	}
	data, err := compiled.Pack(name)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(data), nil
}

// NewWithBeacon requires the separately compiler-pinned factory/beacon ABI.
// There is no enabling flag that skips code, storage or implementation checks.
func NewWithBeacon(metadata Metadata, bank Bank, evm *evmkeeper.Keeper, bindings BindingSource, tokenABI, beaconABI abi.ABI, evmGas, sdkGas uint64) (*Reader, error) {
	r, err := New(metadata, bank, evm, bindings, tokenABI, evmGas, sdkGas)
	if err != nil {
		return nil, err
	}
	r.beaconData, err = viewSelector(beaconABI, "implementation", abi.AddressTy, 20)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Reader) readWord(charge, ctx sdk.Context, address common.Address, data []byte) ([]byte, error) {
	// Each getter gets its own discarded cache, so speculative native-precompile
	// writes cannot influence a later getter in the same valuation.
	callCtx, _ := ctx.CacheContext()
	message := core.Message{From: common.Address{}, To: &address, GasLimit: r.evmGas, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Value: new(big.Int), Data: bytes.Clone(data), SkipNonceChecks: true, SkipFromEOACheck: true}
	stateDB := statedb.New(callCtx, r.evm, statedb.NewEmptyTxConfig())
	response, err := r.evm.ApplyMessage(callCtx, stateDB, message, nil, false, false, false)
	if response != nil {
		charge.GasMeter().ConsumeGas(min(response.GasUsed, r.evmGas), "native coin EVM view")
	}
	if err != nil {
		return nil, fmt.Errorf("coin bounded view: %w", err)
	}
	if response == nil || response.VmError != "" || len(response.Ret) != 32 || response.GasUsed > r.evmGas {
		return nil, errors.New("coin bounded view reverted or returned malformed output")
	}
	return bytes.Clone(response.Ret), nil
}

func (r *Reader) verifyBinding(charge, ctx sdk.Context, b Binding, address common.Address) error {
	implementationWord := r.evm.GetState(ctx, address, implementationSlot)
	beaconWord := r.evm.GetState(ctx, address, beaconSlot)
	noBeaconFields := b.Beacon == (common.Address{}) && b.BeaconRuntimeHash == (common.Hash{}) && b.BeaconImplementation == (common.Address{}) && b.BeaconImplementationHash == (common.Hash{})
	switch b.Kind {
	case DirectRuntime:
		if beaconWord != (common.Hash{}) {
			return errors.New("beacon proxy requires explicit reviewed beacon binding")
		}
		if implementationWord != (common.Hash{}) || b.Implementation != (common.Address{}) || b.ImplementationHash != (common.Hash{}) || !noBeaconFields {
			return errors.New("direct runtime binding contains proxy implementation fields")
		}
		return nil
	case ERC1967Proxy:
		if beaconWord != (common.Hash{}) || !noBeaconFields {
			return errors.New("EIP1967 implementation binding cannot contain beacon fields")
		}
		if b.Implementation == (common.Address{}) || b.ImplementationHash == (common.Hash{}) || implementationWord != common.BytesToHash(b.Implementation.Bytes()) || r.evm.GetCodeHash(ctx, b.Implementation) != b.ImplementationHash {
			return errors.New("coin implementation is not approved")
		}
		return nil
	case BeaconProxy:
		if len(r.beaconData) == 0 {
			return errors.New("beacon proxy requires explicit compiled beacon ABI")
		}
		if implementationWord != (common.Hash{}) || b.Beacon == (common.Address{}) || b.BeaconRuntimeHash == (common.Hash{}) || b.BeaconImplementation == (common.Address{}) || b.BeaconImplementationHash == (common.Hash{}) || b.Implementation == (common.Address{}) || b.ImplementationHash == (common.Hash{}) {
			return errors.New("incomplete explicit beacon/factory implementation approval")
		}
		if beaconWord != common.BytesToHash(b.Beacon.Bytes()) || r.evm.GetCodeHash(ctx, b.Beacon) != b.BeaconRuntimeHash {
			return errors.New("coin beacon is not approved")
		}
		if r.evm.GetState(ctx, b.Beacon, beaconSlot) != (common.Hash{}) || r.evm.GetState(ctx, b.Beacon, implementationSlot) != common.BytesToHash(b.BeaconImplementation.Bytes()) || r.evm.GetCodeHash(ctx, b.BeaconImplementation) != b.BeaconImplementationHash {
			return errors.New("beacon factory implementation is not approved")
		}
		word, err := r.readWord(charge, ctx, b.Beacon, r.beaconData)
		if err != nil {
			return err
		}
		if !bytes.Equal(word[:12], make([]byte, 12)) || common.BytesToAddress(word) != b.Implementation || r.evm.GetCodeHash(ctx, b.Implementation) != b.ImplementationHash {
			return errors.New("resolved beacon token implementation is not approved (including paused/zero result)")
		}
		return nil
	default:
		return errors.New("explicit supported coin binding kind is required")
	}
}
