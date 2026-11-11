package keeper

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtcrypto "github.com/cometbft/cometbft/crypto"
	cryptoencoding "github.com/cometbft/cometbft/crypto/encoding"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	abciConsensusAnchorKey byte = 0x43
	abciAnchorBytes             = 1 + 8 + sha256.Size + 4 + sha256.Size
)

type abciConsensusAnchor struct {
	height         int64
	currentHash    [sha256.Size]byte
	previousCount  uint32
	previousDigest [sha256.Size]byte
}

func (k *StateKeeper) getABCIConsensusAnchor(ctx sdk.Context) (abciConsensusAnchor, error) {
	var a abciConsensusAnchor
	data, err := k.service.OpenKVStore(ctx).Get([]byte{abciConsensusAnchorKey})
	if err != nil {
		return a, err
	}
	if len(data) != abciAnchorBytes || data[0] != 1 {
		return a, errors.New("missing or invalid native ABCI consensus anchor")
	}
	a.height = int64(binary.BigEndian.Uint64(data[1:9]))
	copy(a.currentHash[:], data[9:41])
	a.previousCount = binary.BigEndian.Uint32(data[41:45])
	copy(a.previousDigest[:], data[45:])
	if a.height != ctx.BlockHeight() || a.currentHash == ([sha256.Size]byte{}) ||
		(a.previousCount == 0) != (a.previousDigest == ([sha256.Size]byte{})) {
		return a, errors.New("native ABCI consensus anchor differs from the current height or prior commit")
	}
	return a, nil
}

func (k *StateKeeper) setABCIConsensusAnchor(ctx sdk.Context, a abciConsensusAnchor) error {
	var data [abciAnchorBytes]byte
	data[0] = 1
	binary.BigEndian.PutUint64(data[1:9], uint64(a.height))
	copy(data[9:41], a.currentHash[:])
	binary.BigEndian.PutUint32(data[41:45], a.previousCount)
	copy(data[45:], a.previousDigest[:])
	return k.service.OpenKVStore(ctx).Set([]byte{abciConsensusAnchorKey}, data[:])
}

// BootstrapABCIConsensus seeds the first two effective sets from the exact
// positive updates the application returns to Comet during InitChain.
func (k *StateKeeper) BootstrapABCIConsensus(ctx sdk.Context, initialHeight int64, initial []types.Validator, updates []abci.ValidatorUpdate) error {
	if initialHeight <= 0 || initialHeight > math.MaxInt64-2 ||
		(ctx.BlockHeight() != 0 && ctx.BlockHeight() != initialHeight && ctx.BlockHeight() != initialHeight-1) {
		return errors.New("invalid native ABCI initial height")
	}
	set, err := ConsensusSet(initial)
	if err != nil {
		return err
	}
	if len(updates) != len(set.Validators) {
		return errors.New("InitChain validator count differs from native consensus genesis")
	}
	seen := make(map[string]bool, len(updates))
	for _, update := range updates {
		pub, err := cryptoencoding.PubKeyFromProto(update.PubKey)
		if err != nil {
			return err
		}
		address := pub.Address()
		_, expected := set.GetByAddress(address)
		if seen[string(address)] || expected == nil || update.Power != expected.VotingPower ||
			pub.Type() != expected.PubKey.Type() || !bytes.Equal(pub.Bytes(), expected.PubKey.Bytes()) {
			return errors.New("InitChain validator key or power differs from native consensus genesis")
		}
		seen[string(address)] = true
	}
	child, commit := ctx.CacheContext()
	present, err := k.service.OpenKVStore(child).Has([]byte{abciConsensusAnchorKey})
	if err != nil {
		return err
	}
	if present {
		return errors.New("native ABCI consensus already initialized")
	}
	powers := make([]types.LastValidatorPower, 0, len(initial))
	for _, val := range initial {
		stored, err := k.GetValidator(child, val.OperatorAddress)
		if err != nil {
			return err
		}
		if stored.Jailed || stored.Status != types.BondStatus_Bonded || stored.Stake != val.Stake {
			return errors.New("native initial validator power or status differs from InitChain")
		}
		powers = append(powers, types.LastValidatorPower{Address: val.OperatorAddress, Power: val.Stake})
	}
	if err := k.SetLastValidatorPowers(child, powers); err != nil {
		return err
	}
	// SDK53 InitChainer uses initialHeight itself for non-1 genesis heights.
	if err := k.BootstrapConsensusSchedule(child.WithBlockHeight(0), initialHeight, initial); err != nil {
		return err
	}
	anchor := abciConsensusAnchor{height: initialHeight}
	copy(anchor.currentHash[:], set.Hash())
	if err := k.setABCIConsensusAnchor(child, anchor); err != nil {
		return err
	}
	commit()
	return nil
}

func (k *StateKeeper) anchoredConsensus(ctx sdk.Context) ([]types.ScheduledValidatorSet, *cmttypes.ValidatorSet, abciConsensusAnchor, error) {
	a, err := k.getABCIConsensusAnchor(ctx)
	if err != nil {
		return nil, nil, a, err
	}
	rows, err := k.readScheduledPair(ctx)
	if err != nil {
		return nil, nil, a, err
	}
	set, err := ConsensusSet(rows[0].Validators)
	if err != nil {
		return nil, nil, a, err
	}
	if !bytes.Equal(set.Hash(), a.currentHash[:]) {
		return nil, nil, a, errors.New("native effective set differs from the prior ABCI validator hash")
	}
	return rows, set, a, nil
}

// VerifyABCIConsensusStartup audits the schedule and anchor restored at the
// trusted committed height. It never seeds or repairs restored state.
func (k *StateKeeper) VerifyABCIConsensusStartup(ctx sdk.Context, committedHeight int64) error {
	if committedHeight <= 0 || committedHeight > math.MaxInt64-3 || ctx.BlockHeight() != committedHeight {
		return errors.New("invalid committed native ABCI consensus height")
	}
	_, _, anchor, err := k.anchoredConsensus(ctx.WithBlockHeight(committedHeight + 1))
	if err != nil {
		return err
	}
	if anchor.previousCount == 0 {
		return errors.New("restored native ABCI consensus lacks a prior commit")
	}
	return nil
}

// ScheduledABCIEndBlock uses the actual SDK53 FinalizeBlock context. At H, the
// prior commitment authenticates H and NextValidatorsHash authenticates H+1.
// DecidedLastCommit contains H-1 address/power metadata, not H or signatures.
func (k *StateKeeper) ScheduledABCIEndBlock(ctx sdk.Context, eligible []string) ([]abci.ValidatorUpdate, error) {
	return k.scheduledABCIEndBlock(ctx, eligible, false)
}

func (k *StateKeeper) scheduledABCIEndBlock(ctx sdk.Context, eligible []string, deriveStakePower bool) ([]abci.ValidatorUpdate, error) {
	if ctx.BlockHeight() <= 0 || ctx.BlockHeight() > math.MaxInt64-2 {
		return nil, errors.New("invalid scheduled ABCI end block height")
	}
	child, commit := ctx.CacheContext()
	rows, current, anchor, err := k.anchoredConsensus(child)
	if err != nil {
		return nil, err
	}
	next, err := ConsensusSet(rows[1].Validators)
	if err != nil {
		return nil, err
	}
	header := child.BlockHeader()
	if !bytes.Equal(next.Hash(), header.NextValidatorsHash) {
		return nil, errors.New("native next set differs from ABCI next validators hash")
	}
	if len(header.ValidatorsHash) != 0 && !bytes.Equal(header.ValidatorsHash, anchor.currentHash[:]) {
		return nil, errors.New("native effective set differs from the supplied current validators hash")
	}
	if len(child.HeaderHash()) != sha256.Size || !current.HasAddress(header.ProposerAddress) {
		return nil, errors.New("invalid ABCI block hash or effective proposer")
	}
	if err := anchor.verifyLastCommit(child.VoteInfos()); err != nil {
		return nil, err
	}
	// Only the local history projection receives the derived current set hash.
	// SDK53's supplied HeaderHash remains the actual Comet block hash.
	header.ValidatorsHash = bytes.Clone(anchor.currentHash[:])
	updates, err := k.advanceScheduledConsensus(child.WithBlockHeader(header), rows, eligible, deriveStakePower)
	if err != nil {
		return nil, err
	}
	following := abciConsensusAnchor{height: child.BlockHeight() + 1, previousCount: uint32(len(current.Validators))}
	copy(following.currentHash[:], header.NextValidatorsHash)
	h := sha256.New()
	for _, validator := range current.Validators {
		h.Write(validator.Address)
		h.Write(sdk.Uint64ToBigEndian(uint64(validator.VotingPower)))
	}
	copy(following.previousDigest[:], h.Sum(nil))
	if err := k.setABCIConsensusAnchor(child, following); err != nil {
		return nil, err
	}
	commit()
	return updates, nil
}

func (a abciConsensusAnchor) verifyLastCommit(votes []abci.VoteInfo) error {
	if uint64(len(votes)) != uint64(a.previousCount) {
		return errors.New("ABCI prior validator count differs from native consensus")
	}
	if a.previousCount == 0 {
		return nil
	}
	h := sha256.New()
	for _, vote := range votes {
		switch vote.BlockIdFlag {
		case cmtproto.BlockIDFlagAbsent, cmtproto.BlockIDFlagCommit, cmtproto.BlockIDFlagNil:
		default:
			return errors.New("invalid ABCI prior validator vote flag")
		}
		if len(vote.Validator.Address) != cmtcrypto.AddressSize || vote.Validator.Power <= 0 {
			return errors.New("invalid ABCI prior validator address or power")
		}
		h.Write(vote.Validator.Address)
		h.Write(sdk.Uint64ToBigEndian(uint64(vote.Validator.Power)))
	}
	if !bytes.Equal(h.Sum(nil), a.previousDigest[:]) {
		return errors.New("ABCI prior validator address or power differs from native consensus")
	}
	return nil
}
