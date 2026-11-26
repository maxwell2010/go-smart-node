package app

import (
	"bytes"
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

// NativeLiquidityPoolProfile opts a consensus profile into one compiler-pinned
// WMSP/CUSDT pair. CUSDT may be pinned by address for an already created token.
type NativeLiquidityPoolProfile struct {
	Address     common.Address `json:"address"`
	Initializer common.Address `json:"initializer"`
	CUSDTOwner  common.Address `json:"cusdt_owner"`
	CUSDT       common.Address `json:"cusdt,omitzero"`
}

type nativeLiquidityPool struct {
	pin         NativeLiquidityPoolProfile
	wrapper     common.Address
	registry    *customCollateralRegistry
	poolABI     abi.ABI
	wrapperABI  abi.ABI
	poolCode    []byte
	wrapperCode []byte
}

func validateNativeLiquidityPoolProfile(profile WithdrawalProfile) error {
	pool := profile.NativePool
	if pool == (NativeLiquidityPoolProfile{}) {
		return nil
	}
	if !profile.EnableNativeConsensus || !profile.customCollateralEnabled() || pool.Address == (common.Address{}) ||
		pool.Initializer == (common.Address{}) || pool.CUSDTOwner == (common.Address{}) || pool.Address == pool.CUSDT ||
		pool.Address == pool.Initializer || pool.Address == pool.CUSDTOwner {
		return errors.New("native liquidity pool requires a native TokenCenter and distinct pool and authority addresses")
	}
	for _, address := range []common.Address{profile.Registry, profile.Gateway, profile.Library, profile.NFTCenter,
		profile.PositionVault, profile.MasterValidator, profile.WrappedDEL, profile.TokenCenter, profile.CurveConverter} {
		if pool.Address == address || pool.CUSDT != (common.Address{}) && pool.CUSDT == address {
			return errors.New("native liquidity pool overlaps a system role")
		}
	}
	return nil
}

func newNativeLiquidityPool(profile WithdrawalProfile, registry *customCollateralRegistry) (*nativeLiquidityPool, error) {
	if err := validateNativeLiquidityPoolProfile(profile); err != nil || profile.NativePool == (NativeLiquidityPoolProfile{}) {
		return nil, err
	}
	code, err := contracts.LiquidityPoolRuntime(profile.NativePool.Initializer)
	if err != nil {
		return nil, err
	}
	wrapper, err := contracts.Load("MintCandyWrappedDEL")
	if err != nil {
		return nil, err
	}
	poolABI, err := contracts.CompiledABI("MintCandyLiquidityPool")
	if err != nil {
		return nil, err
	}
	return &nativeLiquidityPool{pin: profile.NativePool, wrapper: profile.WrappedDEL, registry: registry,
		poolABI: poolABI, poolCode: code, wrapperABI: wrapper.ABI, wrapperCode: wrapper.Runtime}, nil
}

func (c *withdrawalController) verifyRestoredAssets(ctx sdk.Context, manifest []byte) error {
	if err := c.customCollateral.verifyRestored(ctx, manifest); err != nil {
		return err
	}
	pool, err := c.nativeLiquidityPoolAt(ctx)
	if err != nil {
		return err
	}
	if pool != nil {
		return pool.verifySnapshot(ctx, false)
	}
	return nil
}

func (p *nativeLiquidityPool) verifySnapshot(ctx sdk.Context, genesis bool) error {
	r := p.registry
	if !r.exactCode(ctx, p.pin.Address, p.poolCode) || !r.exactCode(ctx, p.wrapper, p.wrapperCode) {
		return errors.New("native liquidity pool or WMSP differs from the compiler runtime")
	}
	if err := p.verifyWrapperBacking(ctx); err != nil {
		return err
	}
	first, second, err := p.pair(ctx)
	if err != nil {
		return err
	}
	if first == (common.Address{}) && second == (common.Address{}) {
		if r.evm.GetState(ctx, p.pin.Address, common.Hash{}) != (common.Hash{}) ||
			r.evm.GetState(ctx, p.pin.Address, common.BigToHash(big.NewInt(1))) != (common.Hash{}) {
			return errors.New("uninitialized native liquidity pool has reserves or shares")
		}
		return nil
	}
	if genesis {
		return errors.New("native liquidity pool must start uninitialized")
	}
	if err := p.authenticatePair(ctx, first, second); err != nil {
		return err
	}
	return p.verifyCustody(ctx, second)
}

func (p *nativeLiquidityPool) pair(ctx sdk.Context) (common.Address, common.Address, error) {
	// The compiler verifies both offsets. Slot 3 packs entered in byte 0
	// with token0 in bytes 1..20. Slot 4 is token1.
	word0 := p.registry.evm.GetState(ctx, p.pin.Address, common.BigToHash(big.NewInt(3)))
	word1 := p.registry.evm.GetState(ctx, p.pin.Address, common.BigToHash(big.NewInt(4)))
	first := common.BytesToAddress(word0[11:31])
	second := common.BytesToAddress(word1[12:])
	var canonical common.Hash
	copy(canonical[11:31], first.Bytes())
	if word0 != canonical || word1 != customAddressWord(second) {
		return common.Address{}, common.Address{}, errors.New("invalid native liquidity pool storage")
	}
	return first, second, nil
}

func (p *nativeLiquidityPool) verifyWrapperBacking(ctx sdk.Context) error {
	r := p.registry
	supply := r.evm.GetState(ctx, p.wrapper, customSlotOffset(customERC20Slot, 2)).Big()
	balance := r.bank.GetBalance(ctx, sdk.AccAddress(p.wrapper.Bytes()), identity.BaseDenom).Amount.BigInt()
	if balance.Cmp(supply) < 0 {
		return errors.New("native liquidity pool WMSP backing is below supply")
	}
	return nil
}

func (p *nativeLiquidityPool) authenticatePair(ctx sdk.Context, first, second common.Address) error {
	if first != p.wrapper {
		return errors.New("native liquidity pool must use the pinned wrapper as its first asset")
	}
	if second == (common.Address{}) || first == second || p.pin.CUSDT != (common.Address{}) && second != p.pin.CUSDT {
		return errors.New("native liquidity pool requires the pinned CUSDT")
	}
	r := p.registry
	if err := r.authenticateRoles(ctx); err != nil {
		return err
	}
	beacon, err := r.authenticateReservelessFamily(ctx)
	if err != nil {
		return err
	}
	if err := r.authenticateReservelessProxy(ctx, second, beacon); err != nil {
		return err
	}
	var flags common.Hash
	flags[30], flags[31] = 1, 1
	cap := new(big.Int).Exp(big.NewInt(10), big.NewInt(25), nil)
	if !bytes.Equal(ctx.KVStore(r.store).Get(reservelessMembershipKey(second)), []byte{1}) ||
		r.evm.GetState(ctx, second, customOwnableSlot) != customAddressWord(p.pin.CUSDTOwner) ||
		r.evm.GetState(ctx, second, reservelessFlagsSlot) != flags ||
		r.evm.GetState(ctx, second, reservelessCapSlot).Big().Cmp(cap) != 0 ||
		!r.storageStringEquals(ctx, second, customSlotOffset(customERC20Slot, 3), "CUSDT") ||
		!r.storageStringEquals(ctx, second, customSlotOffset(customERC20Slot, 4), "cusdt") {
		return errors.New("native liquidity pool CUSDT membership, ownership or terms differ")
	}
	return nil
}

func (p *nativeLiquidityPool) verifyCustody(ctx sdk.Context, cusdt common.Address) error {
	r := p.registry
	word := r.evm.GetState(ctx, p.pin.Address, common.Hash{})
	shares := r.evm.GetState(ctx, p.pin.Address, common.BigToHash(big.NewInt(1))).Big()
	reserves := [2]*big.Int{new(big.Int).SetBytes(word[18:]), new(big.Int).SetBytes(word[4:18])}
	if word[0] != 0 || word[1] != 0 || word[2] != 0 || word[3] != 0 || shares.BitLen() > 112 {
		return errors.New("invalid native liquidity pool reserves or share word")
	}
	key := crypto.Keccak256Hash(customAddressWord(p.pin.Address).Bytes(), customERC20Slot.Bytes())
	for i, token := range [2]common.Address{p.wrapper, cusdt} {
		balance := r.evm.GetState(ctx, token, key).Big()
		if balance.Cmp(reserves[i]) < 0 || (reserves[i].Sign() == 0) != (shares.Sign() == 0) {
			return errors.New("native liquidity pool reserve exceeds custody or differs from share supply")
		}
	}
	return nil
}

func (p *nativeLiquidityPool) admit(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) (bool, error) {
	if p == nil || msg.To == nil || (*msg.To != p.pin.Address && *msg.To != p.wrapper) {
		return false, nil
	}
	if sender == (common.Address{}) || sender != msg.From || msg.Value == nil || len(msg.Data) < 4 ||
		len(msg.Data) > 132 || len(msg.SetCodeAuthorizations) != 0 || len(msg.BlobHashes) != 0 {
		return true, errors.New("native liquidity pool requires bounded direct signed calldata")
	}
	if err := p.verifySnapshot(ctx, false); err != nil {
		return true, err
	}
	if *msg.To == p.wrapper {
		return true, p.admitWrapper(ctx, msg, receipt)
	}
	if msg.Value.Sign() != 0 {
		return true, errors.New("native liquidity pool accepts zero native value")
	}
	return true, p.admitPool(ctx, msg, receipt)
}

func (p *nativeLiquidityPool) admitWrapper(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	method, err := p.wrapperABI.MethodById(msg.Data[:4])
	if err != nil {
		return err
	}
	args, err := canonicalReservelessArgs(*method, msg.Data)
	if err != nil {
		return err
	}
	var event *ethtypes.Log
	switch method.Sig {
	case "deposit()":
		if msg.Value.Sign() <= 0 || msg.Value.BitLen() > 112 {
			return errors.New("native liquidity pool WMSP deposit is outside the amount limit")
		}
		event = reservelessLog(p.wrapper, "Transfer", common.Address{}, msg.From, msg.Value)
	case "withdraw(uint256)":
		amount := args[0].(*big.Int)
		evm := p.registry.evm
		// The pinned wrapper pays its caller with a native-value call. Only a
		// code-free signer can receive it on this path, without a callback.
		if msg.Value.Sign() != 0 || amount.Sign() <= 0 || amount.BitLen() > 112 ||
			len(evm.GetCode(ctx, evm.GetCodeHash(ctx, msg.From))) != 0 {
			return errors.New("native liquidity pool requires a bounded WMSP withdrawal to the direct signer")
		}
		event = reservelessLog(p.wrapper, "Transfer", msg.From, common.Address{}, amount)
	case "approve(address,uint256)":
		if msg.Value.Sign() != 0 || args[0] != p.pin.Address || args[1].(*big.Int).BitLen() > 112 {
			return errors.New("native liquidity pool requires a bounded WMSP approval for the pinned pool")
		}
		owner := crypto.Keccak256Hash(customAddressWord(msg.From).Bytes(), customSlotOffset(customERC20Slot, 1).Bytes())
		slot := crypto.Keccak256Hash(customAddressWord(p.pin.Address).Bytes(), owner.Bytes())
		if p.registry.evm.GetState(ctx, p.wrapper, slot).Big().Cmp(args[1].(*big.Int)) != 0 {
			return errors.New("native liquidity pool WMSP allowance differs from signed calldata")
		}
		event = reservelessLog(p.wrapper, "Approval", msg.From, p.pin.Address, args[1].(*big.Int))
	default:
		return errors.New("native liquidity pool WMSP method is outside admission")
	}
	if !equalReservelessLogs(receipt.Logs, []*ethtypes.Log{event}) {
		return errors.New("native liquidity pool WMSP receipt differs from signed calldata")
	}
	return nil
}

func (p *nativeLiquidityPool) admitPool(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	method, err := p.poolABI.MethodById(msg.Data[:4])
	if err != nil {
		return err
	}
	args, err := canonicalReservelessArgs(*method, msg.Data)
	if err != nil {
		return err
	}
	first, second, err := p.pair(ctx)
	if err != nil {
		return err
	}
	pool, actor := p.pin.Address, msg.From
	if method.Name == "initialize" {
		event := p.poolABI.Events["PoolInitialized"]
		if actor != p.pin.Initializer || args[0] != first || args[1] != second ||
			!equalReservelessLogs(receipt.Logs, []*ethtypes.Log{{Address: pool, Topics: []common.Hash{event.ID}, Data: msg.Data[4:]}}) {
			return errors.New("native liquidity pool initializer differs from the pinned pair")
		}
		return nil
	}
	if first == (common.Address{}) || second == (common.Address{}) || len(receipt.Logs) != 3 || receipt.Logs[2] == nil {
		return errors.New("native liquidity pool requires an initialized pair and three receipt events")
	}
	var event abi.Event
	switch method.Name {
	case "addLiquidity":
		event = p.poolABI.Events["LiquidityAdded"]
	case "removeLiquidity":
		event = p.poolABI.Events["LiquidityRemoved"]
	case "swapExactInput":
		event = p.poolABI.Events["Swap"]
	default:
		return errors.New("native liquidity pool method is outside admission")
	}
	log := receipt.Logs[2]
	argsABI := event.Inputs.NonIndexed()
	values, err := argsABI.Unpack(log.Data)
	if err != nil {
		return err
	}
	data, err := argsABI.PackValues(values)
	if err != nil {
		return err
	}
	var legs [2]*ethtypes.Log
	switch method.Name {
	case "addLiquidity":
		amount0, amount1, shares := values[0].(*big.Int), values[1].(*big.Int), values[2].(*big.Int)
		if amount0.Cmp(args[0].(*big.Int)) > 0 || amount1.Cmp(args[1].(*big.Int)) > 0 || shares.Cmp(args[2].(*big.Int)) < 0 {
			return errors.New("native liquidity deposit exceeds signed bounds")
		}
		legs = [2]*ethtypes.Log{reservelessLog(first, "Transfer", actor, pool, amount0), reservelessLog(second, "Transfer", actor, pool, amount1)}
	case "removeLiquidity":
		amount0, amount1, shares := values[0].(*big.Int), values[1].(*big.Int), values[2].(*big.Int)
		if shares.Cmp(args[0].(*big.Int)) != 0 || amount0.Cmp(args[1].(*big.Int)) < 0 || amount1.Cmp(args[2].(*big.Int)) < 0 {
			return errors.New("native liquidity withdrawal differs from signed bounds")
		}
		legs = [2]*ethtypes.Log{reservelessLog(first, "Transfer", pool, actor, amount0), reservelessLog(second, "Transfer", pool, actor, amount1)}
	case "swapExactInput":
		direction, input, output := values[0].(bool), values[1].(*big.Int), values[2].(*big.Int)
		if direction != args[0].(bool) || input.Cmp(args[1].(*big.Int)) != 0 || output.Cmp(args[2].(*big.Int)) < 0 {
			return errors.New("native liquidity swap differs from signed bounds")
		}
		if !direction {
			first, second = second, first
		}
		legs = [2]*ethtypes.Log{reservelessLog(first, "Transfer", actor, pool, input), reservelessLog(second, "Transfer", pool, actor, output)}
	}
	terminal := &ethtypes.Log{Address: pool, Topics: []common.Hash{event.ID, customAddressWord(actor)}, Data: data}
	if !equalReservelessLogs(receipt.Logs, []*ethtypes.Log{legs[0], legs[1], terminal}) {
		return errors.New("native liquidity pool receipt differs from signed operation")
	}
	return nil
}
