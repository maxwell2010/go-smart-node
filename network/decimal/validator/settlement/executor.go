// Package settlement proves a withdrawal from the approved Gateway in the
// caller's SDK context. The App owns scheduling and signed-message admission.
package settlement

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

const (
	freezeWithdraw   = 1
	statusCompleted  = 0
	statusFrozen     = 1
	tokenERC721      = 2
	tokenDEL         = 4
	frozenWordCount  = 11
	freezeStatusWord = 8
)

type Config struct {
	EVMGas      uint64
	MaxLogs     uint32
	MaxLogBytes uint32
}

// Proof carries the actual bounded logs for native projection. Message is set
// only for an internal completion, which has no signed Ethereum transaction.
type Proof struct {
	Source  evmview.StakeSource
	Index   common.Hash
	Receipt *ethtypes.Receipt
	Message *core.Message
}

type Executor struct {
	evm                     *evmkeeper.Keeper
	auth                    *evmview.Reader
	roles                   evmview.StakeRoles
	config                  Config
	gateway, wrapper, vault abi.ABI
}

// Sender is reserved for the internal settlement call. It has no owner role
// and must remain an inert address in the active EVM profile.
func Sender() common.Address {
	return common.BytesToAddress(authtypes.NewModuleAddress("native_withdrawal_settlement"))
}

func NewExecutor(evm *evmkeeper.Keeper, auth *evmview.Reader, config Config) (*Executor, error) {
	roles, err := auth.ConfiguredStakeRoles(evm)
	if err != nil {
		return nil, err
	}
	if config.EVMGas < 21_000 || config.EVMGas > 5_000_000 || config.MaxLogs == 0 || config.MaxLogs > 1024 || config.MaxLogBytes == 0 || config.MaxLogBytes > 1024*1024 {
		return nil, errors.New("invalid Gateway completion work limits")
	}
	sender := Sender()
	if sender == (common.Address{}) || sender == common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName)) || sender == roles.Gateway || sender == roles.WrappedDEL || sender == roles.PositionVault {
		return nil, errors.New("Gateway completion sender collides with a privileged role")
	}
	e := &Executor{evm: evm, auth: auth, roles: roles, config: config}
	for _, contract := range []struct {
		name string
		abi  *abi.ABI
	}{{"MintCandyIsolatedDelegation", &e.gateway}, {"MintCandyWrappedDEL", &e.wrapper}, {"MintCandyNFTPositionVault", &e.vault}} {
		*contract.abi, err = contracts.CompiledABI(contract.name)
		if err != nil {
			return nil, err
		}
	}
	if e.gateway.Methods["complete"].Sig != "complete(uint256[])" || e.gateway.Methods["getFrozenStake"].Sig != "getFrozenStake(uint256)" || e.gateway.Events["WithdrawCompleted"].Sig != "WithdrawCompleted(uint256)" {
		return nil, errors.New("compiler Gateway completion ABI differs")
	}
	return e, nil
}

// Complete executes exactly one matured withdrawal in a child cache. The
// projection must succeed before EVM, bank and native changes are committed.
func (e *Executor) Complete(ctx sdk.Context, row validatortypes.EVMFrozenBinding, project func(sdk.Context, Proof) error) error {
	if project == nil {
		return errors.New("Gateway completion requires native projection")
	}
	index, beneficiary, err := e.binding(ctx, row)
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	source, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	if err = e.requireInert(child, Sender()); err != nil {
		return fmt.Errorf("invalid reserved settlement sender: %w", err)
	}
	if err = e.requireInert(child, beneficiary); err != nil {
		return err
	}
	before, err := e.readFrozen(child, index)
	if err != nil {
		return err
	}
	payout, err := e.matchFrozen(before, row, statusFrozen)
	if err != nil {
		return err
	}
	payoutBefore, err := e.payoutState(child, payout, e.roles.Gateway)
	if err != nil {
		return err
	}
	data, err := e.gateway.Pack("complete", []*big.Int{index.Big()})
	if err != nil {
		return err
	}
	result, message, err := e.invoke(child, e.roles.Gateway, data, true)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Gateway complete returned unexpected data")
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, GasUsed: result.GasUsed, Logs: evmtypes.LogsToEthereum(result.Logs)}
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	if len(markers) != 1 || markers[0].index != index {
		return errors.New("internal Gateway call did not complete exactly the requested index")
	}
	if err = e.validatePayoutLog(receipt, markers[0], payout); err != nil {
		return err
	}
	afterSource, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	if afterSource != source {
		return errors.New("Gateway source changed during completion")
	}
	after, err := e.readFrozen(child, index)
	if err != nil {
		return err
	}
	if _, err = e.matchFrozen(after, row, statusCompleted); err != nil {
		return err
	}
	before[freezeStatusWord] = common.Hash{} // The compiled Completed enum is zero.
	if before != after {
		return errors.New("Gateway completion changed other frozen stake fields")
	}
	payoutAfter, err := e.payoutState(child, payout, beneficiary)
	if err != nil {
		return err
	}
	if err = payout.checkDelta(payoutBefore, payoutAfter); err != nil {
		return err
	}
	if err = project(child, Proof{Source: source, Index: index, Receipt: receipt, Message: &message}); err != nil {
		return fmt.Errorf("project Gateway completion: %w", err)
	}
	commit()
	return nil
}

// ValidateSigned validates a selected completion after actual signed EVM
// execution. The caller must enforce direct canonical complete calldata and
// process every completed index in the same signed transaction cache.
func (e *Executor) ValidateSigned(ctx sdk.Context, row validatortypes.EVMFrozenBinding, receipt *ethtypes.Receipt) (Proof, error) {
	index, beneficiary, err := e.binding(ctx, row)
	if err != nil {
		return Proof{}, err
	}
	markers, err := e.completions(receipt)
	if err != nil {
		return Proof{}, err
	}
	if err = signedMetadata(ctx, receipt); err != nil {
		return Proof{}, err
	}
	var match *completion
	for i := range markers {
		if markers[i].index == index {
			match = &markers[i]
			break
		}
	}
	if match == nil {
		return Proof{}, errors.New("signed Gateway receipt did not complete the requested index")
	}
	source, err := e.auth.AuthenticateStakeProfile(ctx)
	if err != nil {
		return Proof{}, err
	}
	// A direct completion cannot create its existing beneficiary. Cancun
	// rules keep an existing callback contract's code visible after execution,
	// so an inert poststate can be used by the signed receipt validator.
	chain := evmtypes.GetEthChainConfig()
	if chain == nil || !chain.IsCancun(big.NewInt(ctx.BlockHeight()), uint64(ctx.BlockTime().Unix())) {
		return Proof{}, errors.New("signed Gateway completion requires Cancun recipient semantics")
	}
	if err = e.requireInert(ctx, beneficiary); err != nil {
		return Proof{}, err
	}
	after, err := e.readFrozen(ctx, index)
	if err != nil {
		return Proof{}, err
	}
	payout, err := e.matchFrozen(after, row, statusCompleted)
	if err != nil {
		return Proof{}, err
	}
	if err = e.validatePayoutLog(receipt, *match, payout); err != nil {
		return Proof{}, err
	}
	if _, err = e.payoutState(ctx, payout, beneficiary); err != nil {
		return Proof{}, err
	}
	return Proof{Source: source, Index: index, Receipt: cloneReceipt(receipt)}, nil
}

func (e *Executor) binding(ctx sdk.Context, row validatortypes.EVMFrozenBinding) (common.Hash, common.Address, error) {
	if e == nil || e.evm == nil {
		return common.Hash{}, common.Address{}, errors.New("Gateway completion executor is missing")
	}
	if err := row.Validate(); err != nil {
		return common.Hash{}, common.Address{}, err
	}
	index := common.BytesToHash(row.FrozenIndex)
	beneficiary := common.BytesToAddress(row.Beneficiary)
	now := ctx.BlockTime().Unix()
	if !row.Active || row.FreezeType != freezeWithdraw || index == common.MaxHash || common.BytesToAddress(row.Gateway) != e.roles.Gateway || beneficiary == Sender() || !row.RawAmount.IsPositive() || ctx.BlockHeight() <= 0 || row.UpdatedHeight > ctx.BlockHeight() || now <= 0 || new(big.Int).SetBytes(row.CompletionTimestamp).Cmp(big.NewInt(now)) > 0 {
		return common.Hash{}, common.Address{}, errors.New("binding is not an active mature approved Gateway withdrawal")
	}
	return index, beneficiary, nil
}

func (e *Executor) requireInert(ctx sdk.Context, addr common.Address) error {
	if addr == (common.Address{}) || e.evm.GetCodeHash(ctx, addr) != common.BytesToHash(evmtypes.EmptyCodeHash) {
		return errors.New("Gateway completion requires an address without executable code")
	}
	chain := evmtypes.GetEthChainConfig()
	if chain == nil {
		return errors.New("missing configured EVM rules")
	}
	rules := chain.Rules(big.NewInt(ctx.BlockHeight()), true, uint64(ctx.BlockTime().Unix()))
	for _, precompile := range vm.ActivePrecompiles(rules) {
		if precompile == addr {
			return errors.New("Gateway completion cannot use a standard precompile address")
		}
	}
	_, found, err := e.evm.GetPrecompileInstance(ctx, addr)
	if err != nil {
		return err
	}
	if found {
		return errors.New("Gateway completion cannot use a chain precompile address")
	}
	return nil
}

func (e *Executor) invoke(ctx sdk.Context, target common.Address, data []byte, commit bool) (*evmtypes.MsgEthereumTxResponse, core.Message, error) {
	msg := core.Message{From: Sender(), To: &target, Value: new(big.Int), GasLimit: e.config.EVMGas, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
	result, err := e.evm.ApplyMessage(ctx, statedb.New(ctx, e.evm, statedb.NewEmptyTxConfig()), msg, nil, commit, false, true)
	if result != nil {
		ctx.GasMeter().ConsumeGas(min(max(result.MaxUsedGas, result.GasUsed), msg.GasLimit), "Gateway completion EVM work before refunds")
	}
	if err != nil {
		return nil, msg, err
	}
	if result == nil || result.MaxUsedGas > msg.GasLimit || result.GasUsed > msg.GasLimit {
		return nil, msg, errors.New("invalid Gateway completion gas result")
	}
	if result.VmError != "" {
		return nil, msg, fmt.Errorf("Gateway call reverted: %s", result.VmError)
	}
	return result, msg, nil
}

func (e *Executor) read(ctx sdk.Context, target common.Address, contract abi.ABI, method string, size int, args ...interface{}) ([]byte, error) {
	data, err := contract.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	child, _ := ctx.CacheContext()
	res, _, err := e.invoke(child, target, data, false)
	if err != nil {
		return nil, err
	}
	if len(res.Ret) != size {
		return nil, errors.New("Gateway getter response differs from exact ABI size")
	}
	values, err := contract.Unpack(method, res.Ret)
	if err != nil {
		return nil, err
	}
	canonical, err := contract.Methods[method].Outputs.Pack(values...)
	if err != nil || !bytes.Equal(canonical, res.Ret) {
		return nil, errors.New("Gateway getter returned noncanonical ABI data")
	}
	return canonical, nil
}

type frozenWords [frozenWordCount]common.Hash

func (e *Executor) readFrozen(ctx sdk.Context, index common.Hash) (words frozenWords, err error) {
	ret, err := e.read(ctx, e.roles.Gateway, e.gateway, "getFrozenStake", len(words)*common.HashLength, index.Big())
	if err != nil {
		return words, err
	}
	for i := range words {
		copy(words[i][:], ret[i*common.HashLength:(i+1)*common.HashLength])
	}
	return words, nil
}
