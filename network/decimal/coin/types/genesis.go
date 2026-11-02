package types

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/maxwell2010/go-smart-node/network/decimal/coin/errors"
)

// NewGenesisState creates a new genesis state.
func NewGenesisState(params Params, coins []Coin, checks []Check) *GenesisState {
	return &GenesisState{
		Params: params,
		Coins:  coins,
		Checks: checks,
	}
}

// DefaultGenesisState returns a default genesis state.
func DefaultGenesisState() *GenesisState {
	return NewGenesisState(DefaultParams(), []Coin{}, []Check{})
}

// Validate performs basic genesis state validation returning an error upon any failure.
func (gs *GenesisState) Validate() error {
	if gs == nil {
		return fmt.Errorf("coin genesis is missing")
	}
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	// Check there are no coins with the same denom
	seenCoins := make(map[string]bool)
	seenContracts := make(map[string]bool)
	remainingReserve := gs.Params.BaseVolume
	for _, coin := range gs.Coins {
		if err := coin.Validate(); err != nil {
			return fmt.Errorf("coin %q: %w", coin.Denom, err)
		}
		if coin.Denom == gs.Params.BaseDenom || seenCoins[coin.Denom] {
			return errors.DuplicateCoinInGenesis
		}
		if coin.DRC20Contract != "" {
			contract := strings.ToLower(coin.DRC20Contract)
			if seenContracts[contract] {
				return fmt.Errorf("duplicate coin contract in genesis")
			}
			seenContracts[contract] = true
		}
		// This is a necessary allocation bound, not full bank reconciliation.
		if coin.Reserve.GT(remainingReserve) {
			return fmt.Errorf("coin reserves exceed declared base allocation")
		}
		remainingReserve = remainingReserve.Sub(coin.Reserve)
		seenCoins[coin.Denom] = true
	}
	// Check there are no checks with the same hash
	seenChecks := make(map[string]bool)
	for _, check := range gs.Checks {
		if err := check.Validate(); err != nil {
			return fmt.Errorf("genesis check: %w", err)
		}
		if check.Coin.Denom != gs.Params.BaseDenom && !seenCoins[check.Coin.Denom] {
			return fmt.Errorf("check references an unknown coin")
		}
		checkHash := check.HashFull()
		checkHashStr := hex.EncodeToString(checkHash[:])
		if seenChecks[checkHashStr] {
			return errors.DuplicateCheckInGenesis
		}
		seenChecks[checkHashStr] = true
	}

	return nil
}
