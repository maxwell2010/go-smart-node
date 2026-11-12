package app

import (
	"errors"
	"fmt"
	"strconv"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/ethereum/go-ethereum/common"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (app *App) nativeFeeRewardRecipientPolicy(profile WithdrawalProfile) func(sdk.AccAddress) bool {
	reserved := map[common.Address]bool{
		profile.Registry: true, profile.Gateway: true, profile.Library: true,
		profile.NFTCenter: true, profile.PositionVault: true, profile.MasterValidator: true,
		profile.WrappedDEL: true, common.BytesToAddress(authtypes.NewModuleAddress(authtypes.FeeCollectorName)): true,
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
	return controller.keeper.ValidateNativeFeeRewardAdmission(ctx, controller.feeRewardRecipientAllowed)
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
		if err := app.applyOrdinaryDELDowntime(child); err != nil {
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
// balance is distributed, using the authenticated prior set and current
// active positions before this block's transactions change them.
func (app *App) distributeNativeFees(ctx sdk.Context, commit validatorkeeper.VerifiedABCICommit) error {
	collector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	fees := app.BankKeeper.GetBalance(ctx, collector, identity.BaseDenom).Amount
	if fees.IsZero() {
		return nil
	}
	votes := make([]validatorkeeper.NativeFeeRewardVote, 0, len(commit.Votes))
	for _, vote := range commit.Votes {
		if vote.Flag == cmtproto.BlockIDFlagCommit {
			votes = append(votes, validatorkeeper.NativeFeeRewardVote{Operator: vote.Operator, Power: vote.Power})
		}
	}
	payouts, err := app.NativeValidatorKeeper.PlanNativeFeeRewards(ctx, fees, votes)
	if err != nil || len(payouts) == 0 {
		return err
	}
	remaining := fees
	for _, payout := range payouts {
		if payout.Amount.IsNil() || !payout.Amount.IsPositive() || payout.Amount.GT(remaining) ||
			!app.withdrawals.feeRewardRecipientAllowed(payout.Recipient) {
			return errors.New("invalid native fee reward payout")
		}
		remaining = remaining.Sub(payout.Amount)
	}
	if !remaining.IsZero() {
		return errors.New("native fee rewards differ from the DEL available for distribution")
	}
	for _, payout := range payouts {
		amount := sdk.NewCoin(identity.BaseDenom, payout.Amount)
		if err := app.BankKeeper.SendCoinsFromModuleToAccount(ctx, authtypes.FeeCollectorName, payout.Recipient, sdk.NewCoins(amount)); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent("decimal_native_fee_reward",
			sdk.NewAttribute("height", strconv.FormatInt(commit.Height, 10)),
			sdk.NewAttribute("recipient", payout.Recipient.String()), sdk.NewAttribute("amount", amount.String())))
	}
	if !app.BankKeeper.GetBalance(ctx, collector, identity.BaseDenom).IsZero() {
		return errors.New("native fee collector did not settle the exact DEL balance")
	}
	return nil
}
