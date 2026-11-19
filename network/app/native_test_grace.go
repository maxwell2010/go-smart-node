package app

import (
	"errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// validateTestGraceProfile permits a temporary penalty exemption only in the
// reserved isolated CI/test domains. It is a consensus genesis parameter, never
// a local config or environment override. The consensus voting rules are intact.
func validateTestGraceProfile(evmChainID uint64, profile WithdrawalProfile) error {
	if profile.TestGraceUntilHeight < 0 {
		return errors.New("test grace end height must be nonnegative")
	}
	if profile.TestGraceUntilHeight == 0 {
		return nil
	}
	if evmChainID < 990001 || evmChainID > 990099 {
		return errors.New("test grace is restricted to isolated EVM chain IDs 990001..990099")
	}
	if !profile.EnableNativeConsensus || !profile.EnableNativeOrdinaryDELDowntime || !profile.EnableNativeDuplicateVote {
		return errors.New("test grace requires native consensus and enabled penalty profiles for automatic resumption")
	}
	return nil
}

func (app *App) nativeTestGraceActive(ctx sdk.Context) bool {
	return app.withdrawals != nil && app.withdrawals.profile.TestGraceUntilHeight > 0 &&
		ctx.BlockHeight() <= app.withdrawals.profile.TestGraceUntilHeight
}
