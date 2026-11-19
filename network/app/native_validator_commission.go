package app

import (
	"fmt"

	sdkmath "cosmossdk.io/math"
)

// NativeValidatorMinCommissionPercent is the protocol floor for new native
// validators and signed commission changes.
const NativeValidatorMinCommissionPercent = 5

func validateNativeValidatorCommission(rate sdkmath.LegacyDec) error {
	floor := sdkmath.LegacyNewDecWithPrec(NativeValidatorMinCommissionPercent, 2)
	if rate.IsNil() || rate.LT(floor) || rate.GT(sdkmath.LegacyOneDec()) {
		return fmt.Errorf("native validator commission must be between %d%% and 100%%", NativeValidatorMinCommissionPercent)
	}
	return nil
}
