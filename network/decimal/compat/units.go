package compat

import (
	"crypto/sha256"

	sdkmath "cosmossdk.io/math"
)

func EtherToWei(amount sdkmath.Int) sdkmath.Int  { return amount.MulRaw(1_000_000_000_000_000_000) }
func FinneyToWei(amount sdkmath.Int) sdkmath.Int { return amount.MulRaw(1_000_000_000_000_000) }
func CalcHashSHA256(value string) []byte         { hash := sha256.Sum256([]byte(value)); return hash[:] }
