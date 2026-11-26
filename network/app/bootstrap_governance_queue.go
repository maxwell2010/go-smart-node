package app

import (
	"errors"
	"strconv"
	"time"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/ethereum/go-ethereum/common"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// The transient store limits end-block checks to proposals voted on in this block.
const bootstrapVoteStore = "native-governance-votes-transient"

func (app *App) bootstrapOperators(ctx sdk.Context) ([4]common.Address, bool, error) {
	var operators [4]common.Address
	if ctx.ChainID() != identity.ProfileChainID || (ctx.ChainID() != identity.MainnetChainID && ctx.ChainID() != identity.TestnetChainID) {
		return operators, false, nil
	}
	roster, err := app.NativeValidatorKeeper.NativeLiveValidatorOperators(ctx)
	if errors.Is(err, nativekeeper.ErrNotFound) {
		return operators, false, nil
	}
	if err != nil || len(roster) != len(operators) {
		return operators, false, err
	}
	// Public operator addresses from the signed launch, independent of monikers
	// and of reward destinations, which operators can edit.
	launch := map[common.Address]bool{
		common.HexToAddress("0x021c5bF9a6eb7e30430226e652ec8c5703B2493F"): true,
		common.HexToAddress("0x77Bab2F42752040e0d0b927dF9cf3c6755953A2b"): true,
		common.HexToAddress("0xac0f9eB76365f467c34cEE188682176B987C3527"): true,
		common.HexToAddress("0x3a572fe604efDe0807A408ad503E5A919C026144"): true,
	}
	for i, operator := range roster {
		raw, err := app.NativeValidatorKeeper.ValidatorAddressCodec().StringToBytes(operator)
		if err != nil || len(raw) != common.AddressLength {
			return operators, false, errors.New("invalid bootstrap governance operator address")
		}
		operators[i] = common.BytesToAddress(raw)
		if ctx.ChainID() == identity.MainnetChainID && !launch[operators[i]] {
			return operators, false, nil
		}
	}
	return operators, true, nil
}

func (app *App) advanceBootstrapVotes(ctx sdk.Context) error {
	if !identity.BootstrapUnanimityExecution {
		return nil
	}
	firstID, err := app.NativeValidatorKeeper.BootstrapUnanimityMinimumID(ctx)
	if err != nil || firstID == 0 {
		return err
	}
	changed := ctx.TransientStore(app.tkeys[bootstrapVoteStore])
	iter := changed.Iterator(nil, nil)
	hasVotes := iter.Valid()
	if err := iter.Close(); err != nil || !hasVotes {
		return err
	}
	operators, ok, err := app.bootstrapOperators(ctx)
	if err != nil || !ok {
		return err
	}
	return app.advanceUnanimousVoting(ctx, firstID, func(id uint64) (bool, error) {
		if !changed.Has(sdk.Uint64ToBigEndian(id)) {
			return false, nil
		}
		return app.bootstrapProposalUnanimous(ctx, id, operators)
	})
}

// Requeue for this block's ordinary SDK tally and native action dispatch.
// Collect first, since mutating a queue with an open iterator is unsupported.
func (app *App) advanceUnanimousVoting(ctx sdk.Context, firstID uint64, eligible func(uint64) (bool, error)) error {
	if firstID == 0 {
		return nil
	}
	var ready []govv1.Proposal
	now := ctx.BlockTime()
	count := 0
	err := app.GovKeeper.ActiveProposalsQueue.Walk(ctx, nil, func(key collections.Pair[time.Time, uint64], id uint64) (bool, error) {
		count++
		if count > maxNativeGovOpenProposals || key.K2() != id {
			return true, errors.New("invalid native bootstrap voting queue")
		}
		if id < firstID || !key.K1().After(now) {
			return false, nil
		}
		ok, err := eligible(id)
		if err != nil || !ok {
			return false, err
		}
		p, err := app.GovKeeper.Proposals.Get(ctx, id)
		if err != nil {
			return true, err
		}
		if p.Id != id || p.Status != govv1.StatusVotingPeriod || len(p.Messages) != 0 || p.VotingEndTime == nil || !p.VotingEndTime.Equal(key.K1()) {
			return true, errors.New("invalid native bootstrap voting proposal")
		}
		ready = append(ready, p)
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, p := range ready {
		if err := app.GovKeeper.ActiveProposalsQueue.Remove(ctx, collections.Join(*p.VotingEndTime, p.Id)); err != nil {
			return err
		}
		p.VotingEndTime = &now
		if err := app.GovKeeper.SetProposal(ctx, p); err != nil {
			return err
		}
		if err := app.GovKeeper.ActiveProposalsQueue.Set(ctx, collections.Join(now, p.Id), p.Id); err != nil {
			return err
		}
		ctx.EventManager().EmitEvent(sdk.NewEvent("mintcandy_unanimous_voting_closed",
			sdk.NewAttribute("proposal_id", strconv.FormatUint(p.Id, 10))))
	}
	return nil
}
