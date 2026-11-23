// Package identity defines the separate network's address conventions and
// validates its Cosmos/EVM signing domains. It does not allocate a public chain ID.
package identity

import (
	"fmt"
	"regexp"
	"strconv"
)

const (
	MainnetChainID       = "mintcandy_9905401-1"
	MainnetEVMChainID    = 9905401
	MainnetBaseDenom     = "msp"
	TestnetChainID       = "mintcandy_990098-1"
	TestnetEVMChainID    = 990098
	TestnetBaseDenom     = "tmsp"
	Bech32Prefix         = "d0"
	Bech32PrefixAccAddr  = "d0"
	Bech32PrefixAccPub   = "d0pub"
	Bech32PrefixValAddr  = "d0valoper"
	Bech32PrefixValPub   = "d0valoperpub"
	Bech32PrefixConsAddr = "d0valcons"
	Bech32PrefixConsPub  = "d0valconspub"
)

var chainPattern = regexp.MustCompile(`^mintcandy_([1-9][0-9]*)-([1-9][0-9]*)$`)

// ValidateChainIDs rejects accidental Decimal chain reuse and mismatched signing
// domains. A production launch additionally needs a globally unique EVM ID;
// passing this function is not a registry reservation or mainnet compatibility.
func ValidateChainIDs(chainID string, evmID uint64) error {
	if len(chainID) > 50 {
		return fmt.Errorf("chain ID exceeds CometBFT's 50-byte limit")
	}
	parts := chainPattern.FindStringSubmatch(chainID)
	if parts == nil {
		return fmt.Errorf("a fresh mintcandy_<evm-id>-<revision> chain ID is required")
	}
	parsed, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || parsed != evmID || evmID == 0 {
		return fmt.Errorf("Cosmos and EVM chain IDs do not match")
	}
	if _, err := strconv.ParseUint(parts[2], 10, 64); err != nil {
		return fmt.Errorf("invalid chain revision: %w", err)
	}
	switch evmID {
	case 75, 202020, 20202020:
		return fmt.Errorf("Decimal production and development signing domains cannot be reused")
	}
	return nil
}

// ValidateProfileChainIDs checks that the binary's native asset belongs to this
// signing domain. Legacy binaries remain compatible with existing del chains.
func ValidateProfileChainIDs(chainID string, evmID uint64) error {
	if err := ValidateChainIDs(chainID, evmID); err != nil {
		return err
	}
	if ProfileChainID != "" && chainID != ProfileChainID {
		return fmt.Errorf("%s profile requires chain ID %q", BaseDenom, ProfileChainID)
	}
	return nil
}
