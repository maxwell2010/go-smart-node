package keeper

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"

	sdkmath "cosmossdk.io/math"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const (
	maxNativeFeeRewardRows  = 128
	maxNativeFeeRewardBytes = 2 << 20
)

var ErrNativeFeeRewardLimit = errors.New("native fee reward positions exceed the pilot row or byte limit")

// NativeFeeRewardVote is a committing H-1 vote authenticated by the caller.
// Power must be from that commit, rather than the operator's current state.
type NativeFeeRewardVote struct {
	Operator string
	Power    int64
}

type NativeFeeRewardPayout struct {
	Recipient sdk.AccAddress
	Amount    sdkmath.Int
}

type nativeFeeRewardPositions struct {
	owners map[string]*big.Int
	total  *big.Int
}

// ValidateNativeFeeRewardAdmission checks the same pilot limits as payout
// planning, so an admitted stake change cannot make fee distribution unbounded.
// The caller supplies the bank and system account restrictions for recipients.
func (k *StateKeeper) ValidateNativeFeeRewardAdmission(ctx sdk.Context, allowed func(sdk.AccAddress) bool) error {
	positions, err := k.readNativeFeeRewardPositions(ctx)
	if err != nil {
		return err
	}
	if allowed == nil {
		return errors.New("native fee reward admission requires a recipient policy")
	}
	for _, operator := range sortedDelegationKeys(positions) {
		validator, err := k.GetValidator(ctx, operator)
		if err != nil {
			return err
		}
		indexed, err := k.readActiveDELReserve(ctx, operator)
		if err != nil {
			return err
		}
		if positions[operator].total.Cmp(indexed) != 0 {
			return errors.New("native fee reward positions differ from the indexed active DEL reserve")
		}
		commission, err := feeRewardAddress(validator.RewardAddress)
		if err != nil {
			return err
		}
		if !allowed(commission) {
			return fmt.Errorf("native fee reward commission address is not admitted for %s", operator)
		}
		for _, owner := range sortedDelegationKeys(positions[operator].owners) {
			if !allowed(sdk.AccAddress(owner)) {
				return fmt.Errorf("native fee reward owner address is not admitted for %s", operator)
			}
		}
	}
	return nil
}

// PlanNativeFeeRewards allocates already-collected DEL without changing state.
// Call at H before any transaction, against post-H-1 positions. A committing
// operator with zero active DEL receives no share. An empty plan leaves the
// fees in the collector. Otherwise the payouts sum exactly to fees.
func (k *StateKeeper) PlanNativeFeeRewards(ctx sdk.Context, fees sdkmath.Int, votes []NativeFeeRewardVote) ([]NativeFeeRewardPayout, error) {
	if fees.IsNil() || fees.IsNegative() {
		return nil, errors.New("native fee rewards require initialized nonnegative DEL fees")
	}
	if len(votes) > maxNativeFeeRewardRows {
		return nil, errors.New("native fee rewards exceed the committing voter limit")
	}
	if fees.IsZero() || len(votes) == 0 {
		return nil, nil
	}
	positions, err := k.readNativeFeeRewardPositions(ctx)
	if err != nil {
		return nil, err
	}
	type allocation struct {
		positions  nativeFeeRewardPositions
		reward     sdk.AccAddress
		commission sdkmath.LegacyDec
	}
	operators := make(map[string]allocation, len(votes))
	weights := make(map[string]*big.Int, len(votes))
	seen := make(map[string]bool, len(votes))
	var totalPower int64
	for _, vote := range votes {
		if vote.Power <= 0 || vote.Power > cmttypes.MaxTotalVotingPower-totalPower {
			return nil, errors.New("invalid native fee reward committing power")
		}
		totalPower += vote.Power
		key, err := operatorKey(validatorPrefix, vote.Operator)
		if err != nil {
			return nil, err
		}
		operator := string(key[1:])
		if seen[operator] {
			return nil, errors.New("duplicate native fee reward committing operator")
		}
		seen[operator] = true
		validator, err := k.GetValidator(ctx, vote.Operator)
		if err != nil {
			return nil, err
		}
		indexed, err := k.readActiveDELReserve(ctx, vote.Operator)
		if err != nil {
			return nil, err
		}
		position, active := positions[vote.Operator]
		if !active && indexed.Sign() == 0 {
			continue
		}
		if !active || position.total.Cmp(indexed) != 0 {
			return nil, errors.New("native fee reward positions differ from the indexed active DEL reserve")
		}
		reward, err := feeRewardAddress(validator.RewardAddress)
		if err != nil {
			return nil, err
		}
		operators[operator] = allocation{positions: position, reward: reward, commission: validator.Commission}
		weights[operator] = big.NewInt(vote.Power)
	}
	if len(weights) == 0 {
		return nil, nil
	}

	payouts := make(map[string]*big.Int)
	commissionDenominator := sdkmath.LegacyOneDec().BigInt()
	for _, share := range splitNativeFeeRewards(fees.BigInt(), weights) {
		operator := operators[share.recipient]
		commission := new(big.Int).Mul(share.amount, operator.commission.BigInt())
		commission.Quo(commission, commissionDenominator)
		addNativeFeeReward(payouts, string(operator.reward), commission)
		delegators := new(big.Int).Sub(share.amount, commission)
		for _, owner := range splitNativeFeeRewards(delegators, operator.positions.owners) {
			addNativeFeeReward(payouts, owner.recipient, owner.amount)
		}
	}
	result := make([]NativeFeeRewardPayout, 0, len(payouts))
	distributed := new(big.Int)
	for _, recipient := range sortedDelegationKeys(payouts) {
		amount := payouts[recipient]
		distributed.Add(distributed, amount)
		result = append(result, NativeFeeRewardPayout{Recipient: sdk.AccAddress(recipient), Amount: sdkmath.NewIntFromBigInt(amount)})
	}
	if distributed.Cmp(fees.BigInt()) != 0 {
		return nil, errors.New("native fee reward payout plan does not conserve DEL")
	}
	return result, nil
}

func feeRewardAddress(value string) (sdk.AccAddress, error) {
	raw, err := sdk.GetFromBech32(value, identity.Bech32PrefixAccAddr)
	if err != nil || len(raw) != 20 || bytes.Equal(raw, make([]byte, 20)) {
		return nil, errors.New("invalid native fee reward recipient")
	}
	return sdk.AccAddress(raw), nil
}

func (k *StateKeeper) readNativeFeeRewardPositions(ctx sdk.Context) (positions map[string]nativeFeeRewardPositions, err error) {
	if err = k.checkDelegationIndex(ctx); err != nil {
		return nil, err
	}
	indexed, err := k.hasActiveDELReserveIndex(ctx)
	if err != nil {
		return nil, err
	}
	if !indexed {
		return nil, ErrActiveDELReserveIndexUnavailable
	}
	it, err := k.service.OpenKVStore(ctx).Iterator([]byte{delegationPrefix}, []byte{delegationPrefix + 1})
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := it.Close(); closeErr != nil {
			positions, err = nil, errors.Join(err, closeErr)
		}
	}()
	positions = make(map[string]nativeFeeRewardPositions)
	var count, totalBytes int
	for ; it.Valid(); it.Next() {
		if err = it.Error(); err != nil {
			return nil, err
		}
		key, data := it.Key(), it.Value()
		count++
		totalBytes += len(key) + len(data)
		if count > maxNativeFeeRewardRows || totalBytes > maxNativeFeeRewardBytes {
			return nil, ErrNativeFeeRewardLimit
		}
		if len(data) > MaxRecordBytes {
			return nil, errors.New("native fee reward position exceeds the record size limit")
		}
		var row types.Delegation
		if err = proto.Unmarshal(data, &row); err != nil {
			return nil, err
		}
		if !bytes.Equal(key, delegationKey(row)) {
			return nil, errors.New("native fee reward position key mismatch")
		}
		if err = row.Stake.Validate(); err != nil {
			return nil, err
		}
		coin := row.Stake.Stake
		if coin.Denom != identity.BaseDenom || coin.Amount.IsZero() {
			continue
		}
		owner, err := feeRewardAddress(row.Delegator)
		if err != nil {
			return nil, err
		}
		operator, present := positions[row.Validator]
		if !present {
			operator = nativeFeeRewardPositions{owners: make(map[string]*big.Int), total: new(big.Int)}
			positions[row.Validator] = operator
		}
		amount := coin.Amount.BigInt()
		// Holds partition the row's amount. NFT collateral belongs to its
		// native NFT row; a companion EVM balance is not a delegation row.
		addNativeFeeReward(operator.owners, string(owner), amount)
		operator.total.Add(operator.total, amount)
	}
	// The SDK cache iterator reports ordinary exhaustion as invalid.
	// Check errors while the cursor is valid, and always check Close.
	return positions, nil
}

func addNativeFeeReward(payouts map[string]*big.Int, recipient string, amount *big.Int) {
	if amount.Sign() == 0 {
		return
	}
	if payouts[recipient] == nil {
		payouts[recipient] = new(big.Int)
	}
	payouts[recipient].Add(payouts[recipient], amount)
}

type nativeFeeRewardShare struct {
	recipient string
	amount    *big.Int
	remainder *big.Int
}

// splitNativeFeeRewards uses exact integer intermediates. Largest remainders
// receive the indivisible atoms, with raw addresses as deterministic ties.
func splitNativeFeeRewards(total *big.Int, weights map[string]*big.Int) []nativeFeeRewardShare {
	if total.Sign() == 0 {
		return nil
	}
	denominator := new(big.Int)
	for _, weight := range weights {
		denominator.Add(denominator, weight)
	}
	shares := make([]nativeFeeRewardShare, 0, len(weights))
	remaining := new(big.Int).Set(total)
	for recipient, weight := range weights {
		numerator := new(big.Int).Mul(total, weight)
		amount, remainder := numerator.QuoRem(numerator, denominator, new(big.Int))
		shares = append(shares, nativeFeeRewardShare{recipient: recipient, amount: amount, remainder: remainder})
		remaining.Sub(remaining, amount)
	}
	sort.Slice(shares, func(i, j int) bool {
		order := shares[i].remainder.Cmp(shares[j].remainder)
		if order != 0 {
			return order > 0
		}
		return shares[i].recipient < shares[j].recipient
	})
	one := big.NewInt(1)
	for i := 0; i < int(remaining.Int64()); i++ {
		shares[i].amount.Add(shares[i].amount, one)
	}
	return shares
}
