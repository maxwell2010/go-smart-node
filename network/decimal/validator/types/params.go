package types

import (
	sdkmath "cosmossdk.io/math"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"time"
)

// DefaultParams preserves native Decimal durations and power-window semantics.
// It supplies no authority, emergency key, allocation or emission schedule.
func DefaultParams() Params {
	return Params{MaxValidators: 256, MaxDelegations: 1000, MaxEntries: 30, HistoricalEntries: 10000,
		RedelegationTime: 7 * 24 * time.Hour, UndelegationTime: 28 * 24 * time.Hour, BaseDenom: identity.BaseDenom,
		SignedBlocksWindow: 24, MinSignedPerWindow: sdkmath.LegacyMustNewDecFromStr("0.5"),
		SlashFractionDowntime:   sdkmath.LegacyMustNewDecFromStr("0.01"),
		SlashFractionDoubleSign: sdkmath.LegacyMustNewDecFromStr("0.05"), AutoUnbondTimeout: 30 * 24 * time.Hour}
}

func (p Params) Validate() error {
	if p.MaxValidators == 0 || p.MaxDelegations == 0 || p.MaxEntries == 0 {
		return fmt.Errorf("validator, delegation and entry limits must be positive")
	}
	if p.RedelegationTime <= 0 || p.UndelegationTime <= 0 || p.DowntimeJailDuration < 0 || p.AutoUnbondTimeout < 0 {
		return fmt.Errorf("invalid validator durations")
	}
	if p.SignedBlocksWindow <= 0 {
		return fmt.Errorf("signed block window must be positive")
	}
	if err := sdk.ValidateDenom(p.BaseDenom); err != nil {
		return err
	}
	for _, f := range []struct {
		name  string
		value sdkmath.LegacyDec
	}{
		{"minimum signed fraction", p.MinSignedPerWindow}, {"downtime slash fraction", p.SlashFractionDowntime}, {"double-sign slash fraction", p.SlashFractionDoubleSign},
	} {
		if f.value.IsNil() || f.value.IsNegative() || f.value.GT(sdkmath.LegacyOneDec()) {
			return fmt.Errorf("%s must be initialized and between zero and one", f.name)
		}
	}
	if p.MinSignedPerWindow.IsZero() {
		return fmt.Errorf("minimum signed fraction must be positive")
	}
	return nil
}
