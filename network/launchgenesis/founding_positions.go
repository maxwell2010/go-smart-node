package launchgenesis

import (
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/app"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

// ApplyFoundingPositions programs the prepared ledger in a disposable seed App
// after deployment of the cold launch's contracts. Both ledgers are committed
// to ctx together. The caller must subsequently serialize and attest the exact
// assembled genesis in a fresh InitChain before treating it as a candidate.
func ApplyFoundingPositions(a *app.App, ctx sdk.Context, profile app.WithdrawalProfile, sponsor common.Address,
	ledger Ledger, withdrawalSeconds int64,
) error {
	domain, ok := a.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	if !ok || ledger.ChainID != ctx.ChainID() || domain.ChainID != ledger.ChainID || domain.EVMChainID != ledger.EVMChainID {
		return fmt.Errorf("founding ledger signing domain differs from the seed App")
	}
	if ctx.KVStore(a.GetKey(validatortypes.StoreKey)).Has([]byte{app.WithdrawalProfileKey}) {
		return fmt.Errorf("founding ledger requires the pre-activation seed App")
	}
	registry, err := a.NativeValidatorKeeper.GetValidators(ctx)
	if err != nil {
		return err
	}
	if len(registry) != 0 {
		return fmt.Errorf("founding ledger requires an empty native validator roster")
	}
	positions, reserve, err := ledger.verify(profile)
	if err != nil {
		return err
	}
	authority := common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName))
	if withdrawalSeconds <= 0 || sponsor == (common.Address{}) || sponsor == authority {
		return fmt.Errorf("invalid founding sponsor or withdrawal period")
	}
	for _, row := range ledger.Bank.Balances {
		recipient, err := nativeAddress(row.Address, identity.Bech32PrefixAccAddr)
		if err != nil {
			return err
		}
		if recipient == sponsor {
			return fmt.Errorf("disposable sponsor cannot receive a genesis allocation")
		}
	}
	wrapped := sdk.AccAddress(profile.WrappedDEL.Bytes())
	balance := func(ctx sdk.Context) sdkmath.Int {
		return a.BankKeeper.GetBalance(ctx, wrapped, identity.BaseDenom).Amount
	}
	if !balance(ctx).IsZero() {
		return fmt.Errorf("founding wrapper already holds backing")
	}
	master, err := systemcontracts.CompiledABI("NewNetworkMasterValidator")
	if err != nil {
		return err
	}
	gateway, err := systemcontracts.CompiledABI("MintCandyIsolatedDelegation")
	if err != nil {
		return err
	}

	child, commit := ctx.CacheContext()
	call := func(from, to common.Address, value *big.Int, contract abi.ABI, method string, args ...any) error {
		input, err := contract.Pack(method, args...)
		if err != nil {
			return fmt.Errorf("encode %s: %w", method, err)
		}
		execution := child.WithGasMeter(storetypes.NewGasMeter(50_000_000))
		msg := core.Message{From: from, To: &to, Value: value, Data: input, GasLimit: 24_000_000,
			GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := a.EVMKeeper.ApplyMessage(execution,
			statedb.New(execution, a.EVMKeeper, statedb.NewEmptyTxConfig()), msg, nil, true, false, true)
		if err != nil {
			return fmt.Errorf("execute %s: %w", method, err)
		}
		if response.VmError != "" {
			return fmt.Errorf("execute %s: %s", method, response.VmError)
		}
		return nil
	}
	registration := make([]registrationRow, 0, len(positions))
	for _, position := range positions {
		registration = append(registration, position.registration)
	}
	if err := call(authority, profile.MasterValidator, new(big.Int), master, "addValidators", registration); err != nil {
		return err
	}
	if err := call(authority, profile.Gateway, new(big.Int), gateway, "setFreezeTime", big.NewInt(withdrawalSeconds), new(big.Int)); err != nil {
		return err
	}
	for i, position := range positions {
		callArgs := position.funding
		if err := a.NativeValidatorKeeper.SetValidator(child, position.validator); err != nil {
			return err
		}
		if err := call(sponsor, profile.Gateway, callArgs.Amount.BigInt(), gateway, "delegateDELTo", callArgs.Beneficiary, callArgs.Validator); err != nil {
			return fmt.Errorf("funding call %d: %w", i+1, err)
		}
		var words [64]byte // Ordinary delegation has zero token ID and hold timestamp.
		stake := validatorkeeper.GatewayDELStake{
			Gateway: profile.Gateway, WrappedDEL: profile.WrappedDEL, Beneficiary: callArgs.Beneficiary, Validator: callArgs.Validator,
			StakeID: crypto.Keccak256Hash(callArgs.Validator[:], callArgs.Beneficiary[:], profile.WrappedDEL[:], words[:]), Amount: callArgs.Amount,
		}
		if err := a.NativeValidatorKeeper.ApplyGatewayDELDelegation(child, stake, callArgs.Amount); err != nil {
			return err
		}
	}
	if !balance(child).Equal(reserve) {
		return fmt.Errorf("founding Gateway deposits do not match WDEL backing")
	}
	commit()
	return nil
}
