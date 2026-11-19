package app

import (
	"errors"
	"fmt"
	"strconv"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	nativeFeeRewardEscrowModule        = "native_fee_rewards"
	nativeFeeRewardPageGas      uint64 = 4_000_000
)

func (app *App) nativeDELRecipientPolicy(profile WithdrawalProfile) func(sdk.AccAddress) bool {
	reserved := map[common.Address]bool{
		profile.Registry: true, profile.Gateway: true, profile.Library: true,
		profile.NFTCenter: true, profile.PositionVault: true, profile.MasterValidator: true,
		profile.WrappedDEL: true, common.BytesToAddress(authtypes.NewModuleAddress(authtypes.FeeCollectorName)): true,
	}
	if profile.customCollateralEnabled() {
		reserved[profile.TokenCenter] = true
		reserved[profile.CurveConverter] = true
	}
	return func(addr sdk.AccAddress) bool {
		return len(addr) == common.AddressLength && !reserved[common.BytesToAddress(addr)] && !app.BankKeeper.BlockedAddr(addr)
	}
}

func (app *App) validateNativeFeeRewardGenesis(ctx sdk.Context, controller *withdrawalController) error {
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	if !app.BankKeeper.GetBalance(ctx, collector, identity.BaseDenom).IsZero() {
		return errors.New("native fee rewards require an empty genesis DEL fee collector")
	}
	if !app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(nativeFeeRewardEscrowModule), identity.BaseDenom).IsZero() {
		return errors.New("native fee rewards require an empty genesis reward escrow")
	}
	return controller.keeper.ValidateNativeFeeRewardAdmission(ctx, controller.delRecipientAllowed)
}

func (app *App) beginNativeConsensus(ctx sdk.Context) (sdk.BeginBlock, error) {
	child, write := ctx.CacheContext()
	commit, err := app.NativeValidatorKeeper.RecordABCICommit(child)
	if err != nil {
		return sdk.BeginBlock{}, fmt.Errorf("record native prior commit: %w", err)
	}
	if app.withdrawals.profile.EnableNativeFeeRewards && commit.Height > 0 {
		if err := app.distributeNativeFees(child, commit); err != nil {
			return sdk.BeginBlock{}, fmt.Errorf("distribute native fees for height %d: %w", commit.Height, err)
		}
	}
	if app.withdrawals.profile.EnableNativeOrdinaryDELDowntime && commit.Height > 0 {
		if err := app.applyNativePenalties(child); err != nil {
			return sdk.BeginBlock{}, err
		}
	}
	manager := app.nativeConsensusBlockManager()
	result, err := manager.BeginBlock(child)
	if err != nil {
		return sdk.BeginBlock{}, err
	}
	result.Events = append(child.EventManager().ABCIEvents(), result.Events...)
	write()
	return result, nil
}

// Prior-block fees are final after EVM gas refunds. Only the committed DEL
// balance is accrued, using the authenticated prior set and pre-transaction
// ownership weights. Automatic payout pages run independently of fee arrivals.
func (app *App) distributeNativeFees(ctx sdk.Context, commit validatorkeeper.VerifiedABCICommit) error {
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	fees := app.BankKeeper.GetBalance(ctx, collector, identity.BaseDenom).Amount
	votes := make([]validatorkeeper.NativeFeeRewardVote, 0, len(commit.Votes))
	for _, vote := range commit.Votes {
		if vote.Flag == cmtproto.BlockIDFlagCommit {
			votes = append(votes, validatorkeeper.NativeFeeRewardVote{Operator: vote.Operator, Power: vote.Power})
		}
	}
	if fees.IsPositive() {
		child, write := ctx.CacheContext()
		err := app.NativeValidatorKeeper.AccrueNativeFeeRewards(child, fees, votes)
		if err != nil && !errors.Is(err, validatorkeeper.ErrNoNativeFeeRewardRecipients) {
			return err
		}
		if err == nil {
			if err := app.BankKeeper.SendCoinsFromModuleToModule(child, authtypes.FeeCollectorName, nativeFeeRewardEscrowModule,
				sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, fees))); err != nil {
				return err
			}
			if err := app.checkNativeFeeRewardEscrow(child); err != nil {
				return err
			}
			write()
		}
	}
	return app.payNativeFeeRewardPage(ctx)
}

func (app *App) checkNativeFeeRewardEscrow(ctx sdk.Context) error {
	owed, err := app.NativeValidatorKeeper.AggregateNativeFeeRewardLiability(ctx)
	if err != nil {
		return err
	}
	balance := app.BankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(nativeFeeRewardEscrowModule), identity.BaseDenom).Amount
	// Unsolicited native transfers are surplus, never newly earned rewards.
	// They must not be able to stop consensus by breaking exact equality.
	if balance.LT(owed) {
		return errors.New("native fee reward escrow does not cover recorded DEL liabilities")
	}
	return nil
}

func (app *App) payNativeFeeRewardPage(ctx sdk.Context) (err error) {
	child, write := ctx.CacheContext()
	meter := storetypes.NewGasMeter(nativeFeeRewardPageGas)
	child = child.WithGasMeter(meter)
	defer func() {
		if p := recover(); p != nil {
			switch p.(type) {
			case storetypes.ErrorOutOfGas, storetypes.ErrorGasOverflow:
				// This page rolls back; prior accrual remains owed in escrow.
				ctx.Logger().Error("native fee reward page exhausted its work budget")
				err = nil
			default:
				panic(p)
			}
		}
		ctx.GasMeter().ConsumeGas(meter.GasConsumedToLimit(), "automatic native fee reward page")
	}()
	_, err = app.NativeValidatorKeeper.ProcessNativeFeeRewards(child, ctx.BlockHeight(), validatorkeeper.MaxNativeFeeRewardPage,
		func(payment sdk.Context, recipient sdk.AccAddress, value sdkmath.Int) error {
			if value.IsNil() || !value.IsPositive() || !app.withdrawals.delRecipientAllowed(recipient) {
				return errors.New("native reward recipient is not admitted")
			}
			amount := sdk.NewCoin(identity.BaseDenom, value)
			if err := app.BankKeeper.SendCoinsFromModuleToAccount(payment, nativeFeeRewardEscrowModule, recipient, sdk.NewCoins(amount)); err != nil {
				return err
			}
			payment.EventManager().EmitEvent(sdk.NewEvent("decimal_native_fee_reward", sdk.NewAttribute("height", strconv.FormatInt(ctx.BlockHeight(), 10)),
				sdk.NewAttribute("recipient", recipient.String()), sdk.NewAttribute("amount", amount.String())))
			return nil
		})
	if err != nil {
		return err
	}
	if err := app.checkNativeFeeRewardEscrow(child); err != nil {
		return err
	}
	write()
	return nil
}
