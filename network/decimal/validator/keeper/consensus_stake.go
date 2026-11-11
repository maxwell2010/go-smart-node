package keeper

import (
	"errors"
	"math/big"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// NativeDELAtomsPerPower pins the DEL atto unit used by the owned network.
// It matches the EVM's AttoPowerReduction without reading the SDK global.
const NativeDELAtomsPerPower int64 = 1_000_000_000_000_000_000

func (k *StateKeeper) stakeDerivedConsensusPowers(ctx sdk.Context, validators map[string]types.Validator) (map[string]int64, error) {
	indexed, err := k.hasActiveDELReserveIndex(ctx)
	if err != nil {
		return nil, err
	}
	if !indexed {
		return nil, ErrActiveDELReserveIndexUnavailable
	}
	powers := make(map[string]int64, len(validators))
	reduction := big.NewInt(NativeDELAtomsPerPower)
	maximum := big.NewInt(cmttypes.MaxTotalVotingPower)
	for _, operator := range sortedDelegationKeys(validators) {
		reserve, err := k.readActiveDELReserve(ctx, operator)
		if err != nil {
			return nil, err
		}
		power := reserve.Quo(reserve, reduction)
		if power.Cmp(maximum) > 0 {
			return nil, errors.New("stake-derived native validator power exceeds the Comet limit")
		}
		powers[operator] = power.Int64()
	}
	return powers, nil
}

// ValidateStakeConsensusSelection is a read-only precommit check over the same
// selection as EndBlock. The caller supplies the complete authenticated set of
// approved and unpaused operators, including any proposed state changes.
func (k *StateKeeper) ValidateStakeConsensusSelection(ctx sdk.Context, eligible []string) error {
	_, err := k.selectNativeValidators(ctx, eligible, true)
	return err
}

// BootstrapStakeABCIConsensus derives the first native validator set from the
// funded DEL index and persists the ABCI schedule in one transaction. The
// caller returns these complete validator updates from InitChain.
func (k *StateKeeper) BootstrapStakeABCIConsensus(ctx sdk.Context, initialHeight int64, eligible []string) ([]abci.ValidatorUpdate, error) {
	child, commit := ctx.CacheContext()
	selection, err := k.selectNativeValidators(child, eligible, true)
	if err != nil {
		return nil, err
	}
	selection.previous = nil // InitChain returns the full set, not a delta.
	updates, err := k.applyNativeValidatorSelection(child, selection)
	if err != nil {
		return nil, err
	}
	if err := k.BootstrapABCIConsensus(child, initialHeight, selection.candidates, updates); err != nil {
		return nil, err
	}
	commit()
	return updates, nil
}

// ScheduledStakeABCIEndBlock derives validator power at H from the exact
// indexed reserve, then schedules the resulting Comet updates for H+2.
// Only explicitly eligible, online, unjailed operators with positive power
// enter the candidate set. An empty future set is rejected.
func (k *StateKeeper) ScheduledStakeABCIEndBlock(ctx sdk.Context, eligible []string) ([]abci.ValidatorUpdate, error) {
	return k.scheduledABCIEndBlock(ctx, eligible, true)
}
