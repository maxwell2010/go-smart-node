package types

import (
	"fmt"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"math"
)

func DefaultGenesisState() *GenesisState { return &GenesisState{Params: DefaultParams()} }

// Validate checks native schema consistency. It cannot reconcile bank escrow,
// custom-coin conversion prices or NFT ownership without their keeper state.
func (g GenesisState) Validate() error {
	if err := g.Params.Validate(); err != nil {
		return err
	}
	vals := map[string]Validator{}
	keys := map[string]bool{}
	var bonded uint64
	var bondedPower int64
	for _, v := range g.Validators {
		if err := v.Validate(); err != nil {
			return err
		}
		k, err := v.ConsensusPubKey()
		if err != nil {
			return err
		}
		if _, ok := vals[v.OperatorAddress]; ok || keys[string(k.Address())] {
			return fmt.Errorf("duplicate validator operator or consensus address")
		}
		vals[v.OperatorAddress] = v
		keys[string(k.Address())] = true
		if v.Status == BondStatus_Bonded {
			bonded++
			if v.Stake > cmttypes.MaxTotalVotingPower-bondedPower {
				return fmt.Errorf("bonded native power exceeds consensus limit")
			}
			bondedPower += v.Stake
		}
	}
	if bonded > uint64(g.Params.MaxValidators) {
		return fmt.Errorf("bonded validator limit exceeded")
	}
	addressPair := func(delegator, validator string) error {
		if err := validateAddress(delegator, identity.Bech32PrefixAccAddr); err != nil {
			return err
		}
		if _, ok := vals[validator]; !ok {
			return fmt.Errorf("delegation references unknown validator")
		}
		return nil
	}
	delegations := map[string]bool{}
	counts := map[string]uint32{}
	nfts := map[string]bool{}
	for _, d := range g.Delegations {
		if err := addressPair(d.Delegator, d.Validator); err != nil {
			return err
		}
		if err := d.Stake.Validate(); err != nil {
			return err
		}
		key := fmt.Sprintf("%s/%s/%d/%s", d.Delegator, d.Validator, d.Stake.Type, d.Stake.ID)
		if delegations[key] {
			return fmt.Errorf("duplicate native delegation")
		}
		delegations[key] = true
		for _, id := range d.Stake.SubTokenIDs {
			key := fmt.Sprintf("%s/%d", d.Stake.ID, id)
			if nfts[key] {
				return fmt.Errorf("NFT sub-token is delegated more than once")
			}
			nfts[key] = true
		}
		counts[d.Validator]++
		if counts[d.Validator] > g.Params.MaxDelegations {
			return fmt.Errorf("validator delegation limit exceeded")
		}
	}
	pairs := map[string]bool{}
	for _, u := range g.Undelegations {
		if err := addressPair(u.Delegator, u.Validator); err != nil {
			return err
		}
		pair := u.Delegator + "/" + u.Validator
		if pairs[pair] {
			return fmt.Errorf("duplicate undelegation pair")
		}
		pairs[pair] = true
		if len(u.Entries) == 0 || uint64(len(u.Entries)) > uint64(g.Params.MaxEntries) {
			return fmt.Errorf("invalid undelegation entry count")
		}
		for _, e := range u.Entries {
			if e.CreationHeight < 0 || e.CompletionTime.IsZero() {
				return fmt.Errorf("invalid undelegation entry timing")
			}
			if err := e.Stake.Validate(); err != nil {
				return err
			}
		}
	}
	trios := map[string]bool{}
	for _, r := range g.Redelegations {
		if err := addressPair(r.Delegator, r.ValidatorSrc); err != nil {
			return err
		}
		if err := addressPair(r.Delegator, r.ValidatorDst); err != nil {
			return err
		}
		if r.ValidatorSrc == r.ValidatorDst {
			return fmt.Errorf("redelegation source equals destination")
		}
		trio := r.Delegator + "/" + r.ValidatorSrc + "/" + r.ValidatorDst
		if trios[trio] {
			return fmt.Errorf("duplicate redelegation trio")
		}
		trios[trio] = true
		if len(r.Entries) == 0 || uint64(len(r.Entries)) > uint64(g.Params.MaxEntries) {
			return fmt.Errorf("invalid redelegation entry count")
		}
		for _, e := range r.Entries {
			if e.CreationHeight < 0 || e.CompletionTime.IsZero() {
				return fmt.Errorf("invalid redelegation entry timing")
			}
			if err := e.Stake.Validate(); err != nil {
				return err
			}
		}
	}
	var total int64
	powers := map[string]bool{}
	for _, p := range g.LastValidatorPowers {
		if _, ok := vals[p.Address]; !ok || powers[p.Address] || p.Power < 0 || p.Power > math.MaxInt64-total {
			return fmt.Errorf("invalid historical validator power")
		}
		powers[p.Address] = true
		total += p.Power
	}
	if g.LastTotalPower < 0 || total != g.LastTotalPower {
		return fmt.Errorf("last total power does not match native validator power records")
	}
	offline := map[string]bool{}
	for _, o := range g.ValidatorOfflineSince {
		v, ok := vals[o.ValidatorAddress]
		if !ok || offline[o.ValidatorAddress] || o.OfflineSince.IsZero() || v.Online {
			return fmt.Errorf("invalid offline validator tracker")
		}
		offline[o.ValidatorAddress] = true
	}
	return nil
}
