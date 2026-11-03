// Package nativeburn provides voluntary, immediate-caller-only native DEL burn.
package nativeburn

import (
	"bytes"
	"context"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	cmn "github.com/cosmos/evm/precompiles/common"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const AddressString = "0x0000000000000000000000000000000000000900"
const Selector = "e6287e08"

type BankKeeper interface {
	cmn.BankKeeper
	SendCoinsFromAccountToModule(context.Context, sdk.AccAddress, string, sdk.Coins) error
	BurnCoins(context.Context, string, sdk.Coins) error
}

type Precompile struct {
	cmn.Precompile
	bank   BankKeeper
	method abi.Method
}

var _ vm.PrecompiledContract = (*Precompile)(nil)

func NewPrecompile(bank BankKeeper, compiledABI abi.ABI) (*Precompile, error) {
	if bank == nil {
		return nil, errors.New("native burn requires the actual bank keeper")
	}
	method, ok := compiledABI.Methods["nativeBurn"]
	if !ok || method.Sig != "nativeBurn(uint256)" || method.StateMutability != "nonpayable" || len(method.Inputs) != 1 || method.Inputs[0].Type.T != abi.UintTy || method.Inputs[0].Type.Size != 256 || len(method.Outputs) != 1 || method.Outputs[0].Type.T != abi.BoolTy || !bytes.Equal(method.ID, crypto.Keccak256([]byte("nativeBurn(uint256)"))[:4]) {
		return nil, errors.New("unexpected compiler-pinned nativeBurn ABI")
	}
	method.ID = bytes.Clone(method.ID)
	method.Inputs = append(abi.Arguments(nil), method.Inputs...)
	method.Outputs = append(abi.Arguments(nil), method.Outputs...)
	return &Precompile{Precompile: cmn.Precompile{ContractAddress: common.HexToAddress(AddressString), KvGasConfig: storetypes.KVGasConfig(), TransientKVGasConfig: storetypes.TransientGasConfig(), BalanceHandlerFactory: cmn.NewBalanceHandlerFactory(bank)}, bank: bank, method: method}, nil
}

func (p *Precompile) RequiredGas(input []byte) uint64 {
	return p.Precompile.RequiredGas(input, true) + params.LogGas + 2*params.LogTopicGas + 32*params.LogDataGas
}

func (p *Precompile) Run(evm *vm.EVM, contract *vm.Contract, readOnly bool) ([]byte, error) {
	if evm == nil || contract == nil || evm.Interpreter() == nil {
		return nil, errors.New("native burn requires a genuine EVM call frame")
	}
	// In the pinned fork CALLCODE and DELEGATECALL pass readOnly=true.
	// Normal nested CALL passes false, so consult sticky interpreter state too.
	// Address alone cannot detect delegatecall: the fork constructs a synthetic
	// precompile contract with Address()==p.Address() for every call kind.
	if readOnly || evm.Interpreter().ReadOnly() {
		return nil, vm.ErrWriteProtection
	}
	if contract.Address() != p.Address() || contract.Value() == nil || !contract.Value().IsZero() {
		return cmn.ReturnRevertError(evm, errors.New("native burn requires a zero-value direct CALL"))
	}
	if len(contract.Input) != 36 || !bytes.Equal(contract.Input[:4], p.method.ID) {
		return cmn.ReturnRevertError(evm, errors.New("native burn accepts only nativeBurn(uint256)"))
	}
	amount := new(big.Int).SetBytes(contract.Input[4:])
	caller := contract.Caller()
	return p.RunNativeAction(evm, contract, func(ctx sdk.Context) ([]byte, error) {
		// RunNativeAction flushes prior EVM balance writes first, snapshots all
		// SDK stores and journals rollback; BalanceHandler resynchronizes balances.
		owner := sdk.AccAddress(caller.Bytes())
		if p.bank.BlockedAddr(owner) {
			return nil, errors.New("native burn does not spend protected SDK module balances")
		}
		units := sdkmath.NewIntFromBigInt(amount)
		if units.IsPositive() {
			coins := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, units))
			if err := p.bank.SendCoinsFromAccountToModule(ctx, owner, evmtypes.ModuleName, coins); err != nil {
				return nil, err
			}
			if err := p.bank.BurnCoins(ctx, evmtypes.ModuleName, coins); err != nil {
				return nil, err
			}
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent("mintcandy_native_burn", sdk.NewAttribute("account", owner.String()), sdk.NewAttribute("denom", identity.BaseDenom), sdk.NewAttribute("amount", amount.String())))
		data, err := p.method.Inputs.Pack(amount)
		if err != nil {
			return nil, err
		}
		evm.StateDB.AddLog(&types.Log{Address: p.Address(), Topics: []common.Hash{crypto.Keccak256Hash([]byte("NativeBurn(address,uint256)")), common.BytesToHash(caller.Bytes())}, Data: data, BlockNumber: uint64(ctx.BlockHeight())})
		return p.method.Outputs.Pack(true)
	})
}
