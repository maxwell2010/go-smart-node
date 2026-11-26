package app

import (
	"errors"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// bootstrapProposalUnanimous only tests eligibility. Activation height, proposal
// admission time and final tally are enforced by its caller.
func (app *App) bootstrapProposalUnanimous(ctx sdk.Context, id uint64, operators [4]common.Address) (bool, error) {
	var expected [4]string
	operatorCodec := app.NativeValidatorKeeper.ValidatorAddressCodec()
	accountCodec := address.NewBech32Codec(identity.Bech32PrefixAccAddr)
	consenting := make(map[string]bool, len(operators))
	hasYes := func(voter sdk.AccAddress) (bool, error) {
		vote, err := app.GovKeeper.Votes.Get(ctx, collections.Join(id, voter))
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return vote.ProposalId == id && fullBootstrapYes(vote.Options), nil
	}
	for i, operator := range operators {
		if operator == (common.Address{}) {
			return false, nil
		}
		yes, err := hasYes(sdk.AccAddress(operator[:]))
		if err != nil || !yes {
			return false, err
		}
		expected[i], err = operatorCodec.BytesToString(operator[:])
		if err != nil {
			return false, err
		}
		account, err := accountCodec.BytesToString(operator[:])
		if err != nil {
			return false, err
		}
		consenting[account] = true
	}
	fits, err := app.NativeValidatorKeeper.BootstrapVotingSnapshotFits(ctx, expected)
	if err != nil || !fits {
		return false, err
	}
	state, err := app.NativeValidatorKeeper.ReadActiveDELVotingState(ctx)
	if err != nil {
		return false, err
	}
	return allBootstrapStakeConsents(state, consenting, func(owner string) (bool, error) {
		voter, err := accountCodec.StringToBytes(owner)
		if err != nil {
			return false, err
		}
		return hasYes(voter)
	})
}

func fullBootstrapYes(options []*govv1.WeightedVoteOption) bool {
	if len(options) != 1 || options[0] == nil || options[0].Option != govv1.OptionYes {
		return false
	}
	weight, err := sdkmath.LegacyNewDecFromStr(options[0].Weight)
	return err == nil && weight.Equal(sdkmath.LegacyOneDec())
}

// The active governance reader weighs each owner's position by its native
// reserve. Requiring each owner to explicitly agree preserves the SDK vote
// override even when all validator operators have already voted yes.
func allBootstrapStakeConsents(state *validatortypes.GenesisState, consenting map[string]bool, lookup func(string) (bool, error)) (bool, error) {
	if len(state.Validators) != 4 || len(consenting) != 4 {
		return false, nil
	}
	for _, validator := range state.Validators {
		if validator.Jailed || validator.ConsensusPower() <= 0 {
			return false, nil
		}
	}
	for _, position := range state.Delegations {
		if !position.Stake.Stake.Amount.IsPositive() || consenting[position.Delegator] {
			continue
		}
		yes, err := lookup(position.Delegator)
		if err != nil || !yes {
			return false, err
		}
		consenting[position.Delegator] = true
	}
	return true, nil
}
