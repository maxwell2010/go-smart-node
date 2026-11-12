package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	nativeadapter "github.com/maxwell2010/go-smart-node/network/decimal/validator/adapter"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	nativevalidator "github.com/maxwell2010/go-smart-node/network/decimal/validator/module"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

type nativeConsensusInitKey struct{}

func (app *App) usesNativeConsensus(ctx context.Context) bool {
	genesis, _ := ctx.Value(nativeConsensusInitKey{}).(bool)
	return genesis || (app.withdrawals != nil && app.withdrawals.profile.EnableNativeConsensus)
}

type consensusEVMReader struct {
	types.StakingKeeper
	native *nativeadapter.EVM
	app    *App
}

func (r consensusEVMReader) reader(ctx context.Context) types.StakingKeeper {
	if r.app.usesNativeConsensus(ctx) {
		return r.native
	}
	return r.StakingKeeper
}

func (r consensusEVMReader) GetHistoricalInfo(ctx context.Context, height int64) (stakingtypes.HistoricalInfo, error) {
	return r.reader(ctx).GetHistoricalInfo(ctx, height)
}

func (r consensusEVMReader) GetValidatorByConsAddr(ctx context.Context, addr sdk.ConsAddress) (stakingtypes.Validator, error) {
	return r.reader(ctx).GetValidatorByConsAddr(ctx, addr)
}

func (r consensusEVMReader) BondDenom(ctx context.Context) (string, error) {
	return r.reader(ctx).BondDenom(ctx)
}

func (app *App) newConsensusEVMReader() consensusEVMReader {
	native, err := nativeadapter.NewEVM(app.NativeValidatorKeeper, sdkmath.NewInt(validatorkeeper.NativeDELAtomsPerPower))
	if err != nil {
		panic(err)
	}
	return consensusEVMReader{StakingKeeper: app.StakingKeeper, native: native, app: app}
}

// Native consensus admits bank transfers and the scoped EVM Gateway, with
// optional IBC v1 and signal-only governance.
type nativeConsensusCircuit struct{ app *App }

func (c nativeConsensusCircuit) IsAllowed(ctx context.Context, typeURL string) (bool, error) {
	if !c.app.usesNativeConsensus(ctx) {
		return true, nil
	}
	if c.app.withdrawals != nil && c.app.withdrawals.profile.EnableNativeIBCV1 && nativeIBCV1Message(typeURL) {
		return true, nil
	}
	return typeURL == sdk.MsgTypeURL(&types.MsgEthereumTx{}) ||
		typeURL == sdk.MsgTypeURL(&banktypes.MsgSend{}) ||
		typeURL == sdk.MsgTypeURL(&banktypes.MsgMultiSend{}) ||
		(c.app.usesNativeGovernance() && nativeGovernanceMessage(typeURL)), nil
}

type sdkEconomicsPrecompile struct {
	vm.PrecompiledContract
	app *App
}

func (p sdkEconomicsPrecompile) Run(evm *vm.EVM, contract *vm.Contract, readOnly bool) ([]byte, error) {
	db, ok := evm.StateDB.(interface{ GetContext() sdk.Context })
	if !ok || p.app.usesNativeConsensus(db.GetContext()) {
		return nil, vm.ErrExecutionReverted
	}
	return p.PrecompiledContract.Run(evm, contract, readOnly)
}

func (app *App) guardSDKEconomicsPrecompiles(precompiles map[common.Address]vm.PrecompiledContract) {
	for _, id := range []string{"0x0800", "0x0801", "0x0802", "0x0805", "0x0806"} {
		addr := common.HexToAddress(id)
		if precompile, ok := precompiles[addr]; ok {
			precompiles[addr] = sdkEconomicsPrecompile{PrecompiledContract: precompile, app: app}
		}
	}
}

func (app *App) nativeConsensusBlockManager() module.Manager {
	m := *app.ModuleManager
	m.OrderBeginBlockers = slices.DeleteFunc(slices.Clone(m.OrderBeginBlockers), func(name string) bool {
		switch name {
		case "mint", "distribution", "slashing", "evidence", "staking":
			return true
		}
		return false
	})
	m.OrderEndBlockers = slices.DeleteFunc(slices.Clone(m.OrderEndBlockers), func(name string) bool {
		return name == "gov" || name == "staking"
	})
	return m
}

// The native keeper is initialized and bootstrapped before other modules so
// the EVM can already resolve native metadata. The local manager copy supplies
// its one validator update set to the SDK's InitGenesis aggregator.
type bootstrappedValidatorGenesis struct {
	nativevalidator.AppModule
	updates []abci.ValidatorUpdate
}

func (m bootstrappedValidatorGenesis) InitGenesis(sdk.Context, codec.JSONCodec, json.RawMessage) []abci.ValidatorUpdate {
	return m.updates
}

func (app *App) initNativeConsensusModules(ctx sdk.Context, genesis GenesisState, validators *validatortypes.GenesisState, initialHeight int64) (*abci.ResponseInitChain, error) {
	var staking stakingtypes.GenesisState
	if err := app.appCodec.UnmarshalJSON(genesis[stakingtypes.ModuleName], &staking); err != nil {
		return nil, err
	}
	if staking.Params.BondDenom != identity.BaseDenom || staking.LastTotalPower.IsNil() || !staking.LastTotalPower.IsZero() ||
		len(staking.Validators)+len(staking.Delegations)+len(staking.UnbondingDelegations)+len(staking.Redelegations)+len(staking.LastValidatorPowers) != 0 {
		return nil, errors.New("native consensus genesis requires an empty SDK staking ledger in DEL")
	}
	var genutil genutiltypes.GenesisState
	if err := app.appCodec.UnmarshalJSON(genesis[genutiltypes.ModuleName], &genutil); err != nil {
		return nil, err
	}
	if len(genutil.GenTxs) != 0 {
		return nil, errors.New("native consensus genesis does not admit SDK genesis transactions")
	}
	if err := validatorkeeper.ValidateSigningWindowSize(validators.Params.SignedBlocksWindow); err != nil {
		return nil, err
	}
	if err := app.NativeValidatorKeeper.InitGenesis(ctx, validators); err != nil {
		return nil, err
	}
	eligible, err := consensusOperators(ctx, app.NativeValidatorKeeper)
	if err != nil {
		return nil, err
	}
	// SDK 0.53 supplies InitialHeight itself to InitChain for a non-one
	// initial height. The schedule is seeded before that first block.
	updates, err := app.NativeValidatorKeeper.BootstrapStakeABCIConsensus(ctx.WithBlockHeight(initialHeight-1), initialHeight, eligible)
	if err != nil {
		return nil, err
	}
	manager := *app.ModuleManager
	manager.Modules = maps.Clone(manager.Modules)
	manager.Modules[validatortypes.ModuleName] = bootstrappedValidatorGenesis{
		AppModule: nativevalidator.NewAppModule(app.NativeValidatorKeeper),
		updates:   updates,
	}
	return manager.InitGenesis(ctx, app.appCodec, genesis)
}

func consensusOperators(ctx sdk.Context, keeper *validatorkeeper.StateKeeper) ([]string, error) {
	if operators, err := keeper.NativeLiveValidatorOperators(ctx); !errors.Is(err, validatorkeeper.ErrNotFound) {
		return operators, err
	}
	validators, err := keeper.GetValidators(ctx)
	if err != nil {
		return nil, err
	}
	if len(validators) == 0 || len(validators) > maxWithdrawalGenesisRows {
		return nil, errors.New("native consensus operator roster exceeds its genesis bounds")
	}
	out := make([]string, len(validators))
	for i, validator := range validators {
		out[i] = validator.OperatorAddress
	}
	return out, nil
}

func (c *withdrawalController) projectAndCheckConsensus(ctx sdk.Context, project func(sdk.Context) error) error {
	if !c.profile.EnableNativeConsensus {
		return project(ctx)
	}
	child, write := ctx.CacheContext()
	if err := project(child); err != nil {
		return err
	}
	eligible, err := consensusOperators(child, c.keeper)
	if err != nil {
		return err
	}
	if err := c.keeper.ValidateStakeConsensusSelection(child, eligible); err != nil {
		return fmt.Errorf("stake change would leave an invalid native validator set: %w", err)
	}
	if c.profile.EnableNativeFeeRewards {
		if err := c.keeper.ValidateNativeFeeRewardAdmission(child, c.feeRewardRecipientAllowed); err != nil {
			return fmt.Errorf("stake change exceeds native fee reward admission: %w", err)
		}
	}
	if c.profile.EnableNativeOrdinaryDELDowntime {
		if err := c.validateOrdinaryDowntimeAdmission(child); err != nil {
			return fmt.Errorf("stake change exceeds the ordinary DEL downtime profile: %w", err)
		}
	}
	write()
	return nil
}

func (app *App) verifyNativeConsensusState(ctx sdk.Context, enabled bool) error {
	anchor, err := app.NativeValidatorKeeper.HasABCIConsensusAnchor(ctx)
	if err != nil {
		return err
	}
	if anchor != enabled {
		return errors.New("stored native consensus anchor differs from the authenticated profile")
	}
	if !enabled {
		return nil
	}
	return app.NativeValidatorKeeper.VerifyABCIConsensusStartup(ctx, ctx.BlockHeight())
}

func (app *App) endNativeConsensus(ctx sdk.Context) (sdk.EndBlock, error) {
	child, write := ctx.CacheContext()
	manager := app.nativeConsensusBlockManager()
	result, err := manager.EndBlock(child)
	if err != nil {
		return sdk.EndBlock{}, err
	}
	if len(result.ValidatorUpdates) != 0 {
		return sdk.EndBlock{}, errors.New("unexpected SDK validator updates in native consensus")
	}
	if err := app.withdrawals.settle(child); err != nil {
		return sdk.EndBlock{}, err
	}
	eligible, err := consensusOperators(child, app.NativeValidatorKeeper)
	if err != nil {
		return sdk.EndBlock{}, err
	}
	result.ValidatorUpdates, err = app.NativeValidatorKeeper.ScheduledStakeABCIEndBlock(child, eligible)
	if err != nil {
		return sdk.EndBlock{}, err
	}
	if app.usesNativeGovernance() {
		if err := app.endNativeGovernance(child); err != nil {
			return sdk.EndBlock{}, err
		}
	}
	if app.usesNativeDuplicateVote() {
		if err := app.NativeValidatorKeeper.VerifyNativeEvidenceLimits(child, app.GetConsensusParams(child).Evidence); err != nil {
			return sdk.EndBlock{}, err
		}
		if err := app.NativeValidatorKeeper.RecordNativeEvidenceBlock(child); err != nil {
			return sdk.EndBlock{}, err
		}
	}
	result.Events = append(result.Events, child.EventManager().ABCIEvents()...)
	write()
	return result, nil
}
