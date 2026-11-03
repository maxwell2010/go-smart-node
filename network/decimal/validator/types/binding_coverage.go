package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func bindingNativePair(owner, validator []byte) (string, string, error) {
	delegator, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(owner)
	if err != nil {
		return "", "", err
	}
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator)
	return delegator, operator, err
}
func nativeTargetKey(owner, validator string, target *NativeStakeTarget) string {
	return fmt.Sprintf("%s/%s/%d/%s", owner, validator, target.StakeType, target.ID)
}
func framedBindingPart(out, value []byte) []byte {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(value)))
	out = append(out, size[:]...)
	return append(out, value...)
}

// NativeQueueEntryHash identifies a normalized queue entry including its pair
// or trio. Identical entries have identical hashes and are counted as a multiset.
func NativeQueueEntryHash(kind uint32, delegator, source, destination string, entry proto.Message) ([]byte, error) {
	if kind != 1 && kind != 2 {
		return nil, errors.New("invalid native queue hash kind")
	}
	switch kind {
	case 1:
		if value, ok := entry.(*UndelegationEntry); !ok || value == nil {
			return nil, errors.New("withdraw queue hash requires a nonnil undelegation entry")
		}
	case 2:
		if value, ok := entry.(*RedelegationEntry); !ok || value == nil {
			return nil, errors.New("transfer queue hash requires a nonnil redelegation entry")
		}
	}
	data, err := proto.Marshal(entry)
	if err != nil {
		return nil, err
	}
	out := []byte("native-validator-queue-v1")
	out = append(out, byte(kind))
	for _, value := range [][]byte{[]byte(delegator), []byte(source), []byte(destination), data} {
		out = framedBindingPart(out, value)
	}
	sum := sha256.Sum256(out)
	return sum[:], nil
}

// ValidateEVMNativeCoverage is structural, not EVM authenticity. Bound profiles
// cannot silently omit current native positions. Actual reserve ownership,
// contract code, pending penalties and integer validator power require the
// production controller's same-context cross-module attestation.
func (g GenesisState) ValidateEVMNativeCoverage() error {
	if g.BindingDomain == nil {
		return nil
	}
	type group struct {
		amount sdkmath.Int
		ids    map[uint32]bool
		asset  string
		coin   bool
	}
	groups := map[string]*group{}
	coins := map[string]*EVMStakeBinding{}
	remaining := map[string]sdkmath.Int{}
	for i := range g.EVMStakeBindings {
		b := &g.EVMStakeBindings[i]
		if b.Kind != EVMStakeBindingKind_EVM_STAKE_BINDING_COIN {
			continue
		}
		key, err := EVMStakeBindingKey(b.Kind, b.Gateway, b.StakeID)
		if err != nil {
			return err
		}
		coins[string(key)] = b
		remaining[string(key)] = b.RawNftBackedAmount
	}
	for i := range g.EVMStakeBindings {
		b := &g.EVMStakeBindings[i]
		if !b.Active {
			continue
		}
		if b.Kind == EVMStakeBindingKind_EVM_STAKE_BINDING_NFT && len(b.CoinCompanionStakeID) != 0 {
			key, err := EVMStakeBindingKey(EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, b.Gateway, b.CoinCompanionStakeID)
			if err != nil {
				return err
			}
			coin := coins[string(key)]
			if coin == nil || !coin.Active || !bytes.Equal(coin.Validator, b.Validator) || !bytes.Equal(coin.Beneficiary, b.Beneficiary) || !bytes.Equal(coin.HoldTimestamp, b.HoldTimestamp) || coin.ReserveAsset.ReserveKind != b.ReserveAsset.ReserveKind || coin.ReserveAsset.NativeDenom != b.ReserveAsset.NativeDenom || !bytes.Equal(coin.ReserveAsset.Token, b.ReserveAsset.Token) {
				return errors.New("NFT companion binding is missing or belongs to another source position")
			}
			left := remaining[string(key)]
			if b.ReportedReserveAmount.GT(left) {
				return errors.New("NFT reported reserve exceeds coin companion backing")
			}
			remaining[string(key)] = left.Sub(b.ReportedReserveAmount)
		}
		owner, validator, err := bindingNativePair(b.Beneficiary, b.Validator)
		if err != nil {
			return err
		}
		key := nativeTargetKey(owner, validator, b.NativeTarget)
		coin := b.Kind == EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
		amount := sdkmath.ZeroInt()
		if coin {
			amount = b.RawAmount.Sub(b.RawNftBackedAmount)
			if amount.IsZero() {
				continue
			}
		}
		current := groups[key]
		if current == nil {
			current = &group{amount: sdkmath.ZeroInt(), ids: map[uint32]bool{}, asset: b.ReserveAsset.NativeDenom, coin: coin}
			groups[key] = current
		}
		if current.coin != coin || current.asset != b.ReserveAsset.NativeDenom {
			return errors.New("inconsistent native target binding assets")
		}
		if coin {
			// Bound addition before SDK Int can overflow its256-bit limit.
			sum := new(big.Int).Add(current.amount.BigInt(), amount.BigInt())
			if sum.BitLen() > 256 {
				return errors.New("native coin binding aggregate exceeds256bits")
			}
			current.amount = sdkmath.NewIntFromBigInt(sum)
		} else {
			for _, id := range b.NativeTarget.SubTokenIDs {
				if current.ids[id] {
					return errors.New("native NFT target unit is assigned twice")
				}
				current.ids[id] = true
			}
		}
	}
	// Remaining logical NFT backing is not an independent bank balance.
	for key, left := range remaining {
		if coins[key].Active && !left.IsZero() {
			return errors.New("coin companion has unbound logical NFT backing")
		}
	}
	positive := map[string]bool{}
	for _, d := range g.Delegations {
		target := &NativeStakeTarget{StakeType: d.Stake.Type, ID: d.Stake.ID, SubTokenIDs: d.Stake.SubTokenIDs}
		key := nativeTargetKey(d.Delegator, d.Validator, target)
		current := groups[key]
		coin := d.Stake.Type == StakeType_Coin || d.Stake.Type == StakeType_CoinHold
		if current == nil {
			if coin && d.Stake.Stake.Amount.IsZero() {
				continue
			}
			return errors.New("native delegation has no complete active source bindings")
		}
		if current.coin != coin || current.asset != d.Stake.Stake.Denom {
			return errors.New("source binding and native delegation asset differ")
		}
		if coin {
			if !current.amount.Equal(d.Stake.Stake.Amount) {
				return errors.New("native direct coin total differs from source amounts minus NFT backing")
			}
		} else {
			if len(current.ids) != len(d.Stake.SubTokenIDs) {
				return errors.New("native delegation differs from exact receipt units")
			}
			for _, id := range d.Stake.SubTokenIDs {
				if !current.ids[id] {
					return errors.New("native delegation contains unbound NFT units")
				}
			}
		}
		if d.Stake.Stake.Amount.IsPositive() {
			positive[d.Validator] = true
		}
		delete(groups, key)
	}
	if len(groups) != 0 {
		return errors.New("active source binding has no normalized native delegation")
	}
	for _, v := range g.Validators {
		if v.Stake > 0 && !positive[v.OperatorAddress] {
			return errors.New("positive native validator power has no bound native backing")
		}
	}
	type queue struct {
		count                      int
		owner, source, destination string
		kind                       uint32
		stake                      Stake
		completion                 int64
	}
	queues := map[string]*queue{}
	add := func(kind uint32, owner, source, destination string, stake Stake, completion int64, entry proto.Message) error {
		hash, err := NativeQueueEntryHash(kind, owner, source, destination, entry)
		if err != nil {
			return err
		}
		q := queues[string(hash)]
		if q == nil {
			q = &queue{owner: owner, source: source, destination: destination, kind: kind, stake: stake, completion: completion}
			queues[string(hash)] = q
		}
		q.count++
		return nil
	}
	for _, u := range g.Undelegations {
		for i := range u.Entries {
			e := &u.Entries[i]
			if err := add(1, u.Delegator, u.Validator, "", e.Stake, e.CompletionTime.Unix(), e); err != nil {
				return err
			}
		}
	}
	for _, r := range g.Redelegations {
		for i := range r.Entries {
			e := &r.Entries[i]
			if err := add(2, r.Delegator, r.ValidatorSrc, r.ValidatorDst, e.Stake, e.CompletionTime.Unix(), e); err != nil {
				return err
			}
		}
	}
	for i := range g.EVMFrozenBindings {
		b := &g.EVMFrozenBindings[i]
		if !b.Active {
			continue
		}
		q := queues[string(b.NativeEntryHash)]
		if q == nil || q.count == 0 {
			return errors.New("frozen source binding has no matching native queue entry")
		}
		owner, source, err := bindingNativePair(b.Beneficiary, b.SourceValidator)
		if err != nil {
			return err
		}
		destination := ""
		if b.FreezeType == 2 {
			_, destination, err = bindingNativePair(b.Beneficiary, b.DestinationValidator)
			if err != nil {
				return err
			}
		}
		if owner != q.owner || source != q.source || destination != q.destination || b.FreezeType != q.kind || q.stake.Type != b.NativeTarget.StakeType || q.stake.ID != b.NativeTarget.ID || q.stake.Stake.Denom != b.ReserveAsset.NativeDenom || !sameUnitIDs(q.stake.SubTokenIDs, b.NativeTarget.SubTokenIDs) || q.completion < 0 || new(big.Int).SetBytes(b.CompletionTimestamp).Cmp(big.NewInt(q.completion)) != 0 {
			return errors.New("frozen source/native queue identity or timing differs")
		}
		if b.SourceKind == EVMStakeBindingKind_EVM_STAKE_BINDING_COIN && !b.RawAmount.Equal(q.stake.Stake.Amount) {
			return errors.New("frozen coin amount differs from native queue")
		}
		q.count--
	}
	for _, q := range queues {
		if q.count != 0 {
			return errors.New("native queue entry has no complete source binding")
		}
	}
	return nil
}
