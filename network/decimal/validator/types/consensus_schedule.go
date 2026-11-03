package types

import (
	"bytes"
	"errors"
	cmttypes "github.com/cometbft/cometbft/types"
	"math"
)

// ValidateScheduledSets binds future consensus identities to immutable native
// metadata. Historical scheduled powers may differ from today's dynamic RS.
func ValidateScheduledSets(rows []ScheduledValidatorSet, metadata []Validator) error {
	if len(rows) == 0 {
		return nil
	}
	if len(rows) != 2 || rows[0].EffectiveHeight <= 0 || rows[0].EffectiveHeight == math.MaxInt64 || rows[1].EffectiveHeight != rows[0].EffectiveHeight+1 {
		return errors.New("consensus schedule requires two consecutive positive heights")
	}
	known := map[string]Validator{}
	for _, v := range metadata {
		known[v.OperatorAddress] = v
	}
	for _, row := range rows {
		if len(row.Validators) == 0 {
			return errors.New("scheduled validator set is empty")
		}
		seen := map[string]bool{}
		var previous []byte
		var previousPower, total int64
		for i, v := range row.Validators {
			if err := v.Validate(); err != nil {
				return err
			}
			if v.Status != BondStatus_Bonded || v.Jailed || v.Stake <= 0 || v.Stake > cmttypes.MaxTotalVotingPower-total {
				return errors.New("invalid scheduled consensus power")
			}
			total += v.Stake
			current, ok := known[v.OperatorAddress]
			if !ok {
				return errors.New("scheduled native validator identity is missing")
			}
			address, err := v.GetConsAddr()
			if err != nil {
				return err
			}
			actual, err := current.GetConsAddr()
			if err != nil {
				return err
			}
			if !bytes.Equal(address, actual) || seen[string(address)] {
				return errors.New("scheduled consensus identity differs or duplicates")
			}
			seen[string(address)] = true
			if i > 0 && (v.Stake > previousPower || (v.Stake == previousPower && bytes.Compare(previous, address) >= 0)) {
				return errors.New("scheduled consensus set is not canonical")
			}
			previous, previousPower = address, v.Stake
		}
	}
	return nil
}
