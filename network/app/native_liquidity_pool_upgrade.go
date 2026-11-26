package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const nativeLiquidityPoolUpgradeKey byte = 0x71

func nativeLiquidityPoolUpgradeAddress(chainID string) common.Address {
	hash := crypto.Keccak256Hash([]byte("mintcandy/liquidity-pool/wmsp-cusdt/v1:"), []byte(chainID))
	return common.BytesToAddress(hash.Bytes())
}

func mainnetDRC20PoolPin() NativeLiquidityPoolProfile {
	issuer := common.HexToAddress("0x40900a48273644768c09183e00e43528c17a29f6")
	return NativeLiquidityPoolProfile{
		Address:     nativeLiquidityPoolUpgradeAddress(identity.MainnetChainID),
		CUSDT:       common.HexToAddress("0x16c1e12e5f0f338e9521082fd671559845406b96"),
		CUSDTOwner:  issuer,
		Initializer: issuer,
	}
}

func validateNativeLiquidityPoolUpgradePin(chainID string, profile WithdrawalProfile, pin NativeLiquidityPoolProfile) error {
	if chainID != identity.ProfileChainID ||
		(chainID != identity.MainnetChainID && chainID != identity.TestnetChainID) {
		return errors.New("native liquidity pool upgrade requires a supported MintCandy network binary")
	}
	if profile.NativePool != (NativeLiquidityPoolProfile{}) ||
		!profile.EnableNativeUpgradeGovernance || pin.Address != nativeLiquidityPoolUpgradeAddress(chainID) ||
		pin.CUSDT == (common.Address{}) || pin.Initializer != pin.CUSDTOwner {
		return errors.New("native liquidity pool upgrade requires the designated pool, CUSDT and its issuer")
	}
	if chainID == identity.MainnetChainID &&
		(pin.CUSDT != common.HexToAddress("0x16c1e12e5f0f338e9521082fd671559845406b96") ||
			pin.CUSDTOwner != common.HexToAddress("0x40900a48273644768c09183e00e43528c17a29f6") ||
			profile.WrappedDEL != common.HexToAddress("0xDF85fb0aE97E5A753A732a8Eb0c6180282e274AC")) {
		return errors.New("native liquidity pool upgrade requires the canonical mainnet WMSP and CUSDT")
	}
	profile.NativePool = pin
	return validateNativeLiquidityPoolProfile(profile)
}

func (app *App) validateNativeLiquidityPoolUpgrade(ctx sdk.Context, pin NativeLiquidityPoolProfile) error {
	if _, err := app.prepareNativeLiquidityPoolUpgrade(ctx, pin); err != nil {
		return err
	}
	return nil
}

func (app *App) prepareNativeLiquidityPoolUpgrade(ctx sdk.Context, pin NativeLiquidityPoolProfile) (*nativeLiquidityPool, error) {
	c := app.withdrawals
	if ctx.ChainID() != app.chainID || c == nil || c.customCollateral == nil {
		return nil, errors.New("native liquidity pool upgrade requires committed native collateral state")
	}
	if err := validateNativeLiquidityPoolUpgradePin(ctx.ChainID(), c.profile, pin); err != nil {
		return nil, err
	}
	if ctx.KVStore(c.customCollateral.store).Has([]byte{nativeLiquidityPoolUpgradeKey}) {
		return nil, errors.New("native liquidity pool upgrade was already applied")
	}
	addr := sdk.AccAddress(pin.Address.Bytes())
	if account := app.AccountKeeper.GetAccount(ctx, addr); account != nil {
		base, ok := account.(*authtypes.BaseAccount)
		if !ok || base.GetPubKey() != nil || base.GetSequence() != 0 {
			return nil, errors.New("native liquidity pool address is occupied by an auth account")
		}
	}
	evm := app.EVMKeeper
	if !evmtypes.IsEmptyCodeHash(evm.GetCodeHash(ctx, pin.Initializer).Bytes()) {
		return nil, errors.New("native liquidity pool requires a code-free first provider")
	}
	if !evmtypes.IsEmptyCodeHash(evm.GetCodeHash(ctx, pin.Address).Bytes()) {
		return nil, errors.New("native liquidity pool address already contains an EVM contract")
	}
	var storageExists bool
	evm.ForEachStorage(ctx, pin.Address, func(_, _ common.Hash) bool {
		storageExists = true
		return false
	})
	if storageExists {
		return nil, errors.New("native liquidity pool address already contains EVM storage")
	}
	profile := c.profile
	profile.NativePool = pin
	pool, err := newNativeLiquidityPool(profile, c.customCollateral)
	if err != nil {
		return nil, err
	}
	if !pool.registry.exactCode(ctx, pool.wrapper, pool.wrapperCode) {
		return nil, errors.New("native liquidity pool upgrade WMSP runtime differs")
	}
	if err := pool.verifyWrapperBacking(ctx); err != nil {
		return nil, err
	}
	if err := pool.authenticatePair(ctx, pool.wrapper, pin.CUSDT); err != nil {
		return nil, err
	}
	return pool, nil
}

func (app *App) installNativeLiquidityPoolUpgrade(ctx sdk.Context, pin NativeLiquidityPoolProfile) error {
	if ctx.ExecMode() != sdk.ExecModeFinalize {
		return errors.New("native liquidity pool upgrade requires FinalizeBlock")
	}
	child, write := ctx.CacheContext()
	pool, err := app.prepareNativeLiquidityPoolUpgrade(child, pin)
	if err != nil {
		return err
	}
	addr := sdk.AccAddress(pin.Address.Bytes())
	account := app.AccountKeeper.GetAccount(child, addr)
	if account == nil {
		account = app.AccountKeeper.NewAccountWithAddress(child, addr)
	}
	if err := account.SetSequence(1); err != nil {
		return fmt.Errorf("set new liquidity pool account nonce: %w", err)
	}
	app.AccountKeeper.SetAccount(child, account)
	codeHash := crypto.Keccak256Hash(pool.poolCode)
	app.EVMKeeper.SetCode(child, codeHash.Bytes(), pool.poolCode)
	app.EVMKeeper.SetCodeHash(child, pin.Address.Bytes(), codeHash.Bytes())
	if err := pool.verifySnapshot(child, true); err != nil {
		return err
	}
	raw, err := json.Marshal(pin)
	if err != nil {
		return err
	}
	child.KVStore(app.keys[validatortypes.StoreKey]).Set([]byte{nativeLiquidityPoolUpgradeKey}, raw)
	child.EventManager().EmitEvent(sdk.NewEvent("mintcandy_native_liquidity_pool_installed",
		sdk.NewAttribute("address", pin.Address.Hex()),
		sdk.NewAttribute("wmsp", pool.wrapper.Hex()),
		sdk.NewAttribute("cusdt", pin.CUSDT.Hex()),
		sdk.NewAttribute("initializer", pin.Initializer.Hex()),
		sdk.NewAttribute("code_hash", codeHash.Hex()),
	))
	write()
	return nil
}

func (c *withdrawalController) nativeLiquidityPoolAt(ctx sdk.Context) (*nativeLiquidityPool, error) {
	if c.customCollateral == nil {
		return c.nativePool, nil
	}
	// Preserve the existing transaction gas accounting before a pool is installed.
	read := ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())
	raw := read.KVStore(c.customCollateral.store).Get([]byte{nativeLiquidityPoolUpgradeKey})
	if raw == nil {
		return c.nativePool, nil
	}
	var pin NativeLiquidityPoolProfile
	if len(raw) > 256 || json.Unmarshal(raw, &pin) != nil {
		return nil, errors.New("invalid stored native liquidity pool upgrade")
	}
	canonical, err := json.Marshal(pin)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("noncanonical stored native liquidity pool upgrade")
	}
	if err := validateNativeLiquidityPoolUpgradePin(ctx.ChainID(), c.profile, pin); err != nil {
		return nil, err
	}
	profile := c.profile
	profile.NativePool = pin
	return newNativeLiquidityPool(profile, c.customCollateral)
}
