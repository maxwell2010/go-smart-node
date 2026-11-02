package keeper

import sdk "github.com/cosmos/cosmos-sdk/types"

// ReliabilityFixActivationHeights is a release-time, per-chain activation table.
// Production IDs remain absent until a coordinated height is agreed. It must never
// be derived from local configuration, wall-clock time, or environment variables.
// Treat it as immutable after application startup.
var ReliabilityFixActivationHeights = map[string]int64{
	"decimal_900001-1": 1,
	"decimal_900002-1": 1,
}

func (k Keeper) reliabilityFixesEnabled(ctx sdk.Context) bool {
	height := ReliabilityFixActivationHeights[ctx.ChainID()]
	return height > 0 && ctx.BlockHeight() >= height
}
