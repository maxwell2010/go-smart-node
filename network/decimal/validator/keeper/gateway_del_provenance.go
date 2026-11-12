package keeper

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"math/big"
	"slices"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	nativeStakeProvenanceMarker byte = 0x5c
	activeDELProvenancePrefix   byte = 0x5d
	frozenDELProvenancePrefix   byte = 0x5e
	maxDELProvenanceTranches         = 128
	maxDELProvenancePositions        = 2 // The managed penalty census bound.
	delProvenanceTimeSize            = 8 + 4
	delProvenanceTrancheSize         = 8 + delProvenanceTimeSize + 32
)

// DELProvenanceTranche records still-custodied direct DEL from one funding
// block. Hold lots use FundedAt.Unix() as their native hold start time.
// Fresh genesis principal is present before height one and has height zero.
type DELProvenanceTranche struct {
	FundedHeight int64
	FundedAt     time.Time
	Amount       sdkmath.Int
}

// NativeStakeProvenanceEnabled reports whether the profile initialized an
// exact native provenance ledger from fresh genesis.
func (k *StateKeeper) NativeStakeProvenanceEnabled(ctx sdk.Context) (bool, error) {
	marker, err := k.service.OpenKVStore(ctx).Get([]byte{nativeStakeProvenanceMarker})
	if err != nil || marker == nil {
		return false, err
	}
	if !bytes.Equal(marker, []byte{1}) {
		return false, errors.New("invalid native stake provenance marker")
	}
	return true, nil
}

// InitializeDELProvenance runs in the original InitChain context of a fresh
// height-one network, after the caller attests the exact EVM genesis escrow.
// The fresh profile must disallow frozen claims and nonordinary active stakes.
func (k *StateKeeper) InitializeDELProvenance(ctx sdk.Context, gateway, wrapped common.Address) error {
	if ctx.BlockHeight() != 0 || ctx.BlockTime().Unix() <= 0 {
		return errors.New("native stake provenance requires fresh height-one genesis")
	}
	enabled, err := k.NativeStakeProvenanceEnabled(ctx)
	if err != nil {
		return err
	}
	if enabled {
		return errors.New("native stake provenance is already initialized")
	}
	child, write := ctx.CacheContext()
	read := child.WithBlockHeight(1)
	rows, next, err := k.GetEVMStakeBindingsPage(read, nil, types.MaxBindingPageSize)
	if err != nil {
		return err
	}
	if len(rows) == 0 || next != nil {
		return errors.New("native stake provenance requires bounded active genesis positions")
	}
	counts := make(map[common.Address]int, len(rows))
	for _, row := range rows {
		if row.CreatedHeight > 1 || row.UpdatedHeight > 1 {
			return errors.New("native stake provenance cannot initialize from historical binding heights")
		}
		if err := k.VerifyGenesisGatewayDEL(read, gateway, wrapped, row); err != nil {
			return err
		}
		operator := common.BytesToAddress(row.Validator)
		counts[operator]++
		if counts[operator] > maxDELProvenancePositions {
			return errors.New("native stake provenance exceeds its genesis validator position limit")
		}
		key := delProvenanceKey(activeDELProvenancePrefix, row.Gateway, row.StakeID)
		if err := k.storeDELProvenance(child, key, []DELProvenanceTranche{{FundedAt: ctx.BlockTime(), Amount: row.RawAmount}}, time.Time{}); err != nil {
			return err
		}
	}
	if err := k.service.OpenKVStore(child).Set([]byte{nativeStakeProvenanceMarker}, []byte{1}); err != nil {
		return err
	}
	write()
	return nil
}

// GetActiveDELProvenance reads the exact remaining direct principal for a
// coin binding. Logical NFT companion balances are accounted for separately.
func (k *StateKeeper) GetActiveDELProvenance(ctx sdk.Context, row types.EVMStakeBinding) ([]DELProvenanceTranche, error) {
	if row.Kind != gatewayDELCoinBinding || !row.Active || row.RawAmount.IsNil() || row.RawNftBackedAmount.IsNil() || row.RawAmount.LT(row.RawNftBackedAmount) {
		return nil, errors.New("invalid active DEL provenance binding")
	}
	key := delProvenanceKey(activeDELProvenancePrefix, row.Gateway, row.StakeID)
	lots, _, err := k.readDELProvenance(ctx, key, row.RawAmount.Sub(row.RawNftBackedAmount))
	return lots, err
}

// GetFrozenDELProvenance reads the exact remaining principal and freeze time
// for a direct DEL claim. The freeze height is the binding's CreatedHeight.
func (k *StateKeeper) GetFrozenDELProvenance(ctx sdk.Context, row types.EVMFrozenBinding) ([]DELProvenanceTranche, time.Time, error) {
	if !row.Active || row.FreezeType != 1 || row.SourceKind != gatewayDELCoinBinding || row.RawAmount.IsNil() || !row.RawAmount.IsPositive() {
		return nil, time.Time{}, errors.New("invalid frozen DEL provenance binding")
	}
	lots, frozenAt, err := k.readDELProvenance(ctx, delProvenanceKey(frozenDELProvenancePrefix, row.Gateway, row.FrozenIndex), row.RawAmount)
	if err != nil {
		return nil, time.Time{}, err
	}
	if row.CreatedHeight <= 0 || lots[len(lots)-1].FundedHeight > row.CreatedHeight {
		return nil, time.Time{}, errors.New("frozen DEL provenance postdates the claim")
	}
	return lots, frozenAt, nil
}

func delProvenanceKey(prefix byte, gateway, id []byte) []byte {
	key := make([]byte, 0, 1+common.AddressLength+common.HashLength)
	key = append(key, prefix)
	key = append(key, gateway...)
	return append(key, id...)
}

func (k *StateKeeper) readDELProvenance(ctx sdk.Context, key []byte, expected sdkmath.Int) ([]DELProvenanceTranche, time.Time, error) {
	if len(key) != 1+common.AddressLength+common.HashLength {
		return nil, time.Time{}, errors.New("invalid DEL provenance key")
	}
	raw, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return nil, time.Time{}, err
	}
	if raw == nil && expected.IsZero() {
		return nil, time.Time{}, nil
	}
	if len(raw) < delProvenanceTimeSize+delProvenanceTrancheSize ||
		(len(raw)-delProvenanceTimeSize)%delProvenanceTrancheSize != 0 {
		return nil, time.Time{}, errors.New("missing or malformed DEL provenance")
	}
	count := (len(raw) - delProvenanceTimeSize) / delProvenanceTrancheSize
	if count > maxDELProvenanceTranches {
		return nil, time.Time{}, errors.New("DEL provenance exceeds the tranche limit")
	}
	frozenAt, err := readDELProvenanceTime(raw[:delProvenanceTimeSize])
	if err != nil || (key[0] == frozenDELProvenancePrefix) != !frozenAt.IsZero() {
		return nil, time.Time{}, errors.New("invalid DEL provenance freeze time")
	}
	lots := make([]DELProvenanceTranche, count)
	var total big.Int
	for i := range lots {
		start := delProvenanceTimeSize + i*delProvenanceTrancheSize
		part := raw[start : start+delProvenanceTrancheSize]
		funded, err := readDELProvenanceTime(part[8 : 8+delProvenanceTimeSize])
		if err != nil {
			return nil, time.Time{}, err
		}
		amount := new(big.Int).SetBytes(part[8+delProvenanceTimeSize:])
		lot := DELProvenanceTranche{FundedHeight: int64(binary.BigEndian.Uint64(part[:8])), FundedAt: funded, Amount: sdkmath.NewIntFromBigInt(amount)}
		if err := validateDELProvenanceTranche(lots[:i], lot, frozenAt); err != nil {
			return nil, time.Time{}, err
		}
		if lot.FundedHeight > ctx.BlockHeight() || lot.FundedAt.After(ctx.BlockTime()) {
			return nil, time.Time{}, errors.New("DEL provenance tranche postdates the block")
		}
		lots[i] = lot
		total.Add(&total, amount)
	}
	if total.Cmp(expected.BigInt()) != 0 {
		return nil, time.Time{}, errors.New("DEL provenance total differs from the binding")
	}
	return lots, frozenAt, nil
}

func (k *StateKeeper) storeDELProvenance(ctx sdk.Context, key []byte, lots []DELProvenanceTranche, frozenAt time.Time) error {
	state := k.service.OpenKVStore(ctx)
	if len(lots) == 0 {
		return state.Delete(key)
	}
	if len(lots) > maxDELProvenanceTranches {
		return errors.New("DEL provenance exceeds the tranche limit")
	}
	if (key[0] == frozenDELProvenancePrefix) != !frozenAt.IsZero() || (!frozenAt.IsZero() && frozenAt.Unix() <= 0) {
		return errors.New("invalid DEL provenance freeze time")
	}
	raw := make([]byte, delProvenanceTimeSize+len(lots)*delProvenanceTrancheSize)
	writeDELProvenanceTime(raw[:delProvenanceTimeSize], frozenAt)
	for i, lot := range lots {
		if err := validateDELProvenanceTranche(lots[:i], lot, frozenAt); err != nil {
			return err
		}
		start := delProvenanceTimeSize + i*delProvenanceTrancheSize
		part := raw[start : start+delProvenanceTrancheSize]
		binary.BigEndian.PutUint64(part[:8], uint64(lot.FundedHeight))
		writeDELProvenanceTime(part[8:8+delProvenanceTimeSize], lot.FundedAt)
		lot.Amount.BigInt().FillBytes(part[8+delProvenanceTimeSize:])
	}
	return state.Set(key, raw)
}

func validateDELProvenanceTranche(previous []DELProvenanceTranche, lot DELProvenanceTranche, frozenAt time.Time) error {
	if lot.FundedHeight < 0 || lot.FundedAt.Unix() <= 0 || lot.Amount.IsNil() || !lot.Amount.IsPositive() ||
		(!frozenAt.IsZero() && lot.FundedAt.After(frozenAt)) {
		return errors.New("invalid DEL provenance tranche")
	}
	if len(previous) != 0 {
		last := previous[len(previous)-1]
		if last.FundedHeight >= lot.FundedHeight || last.FundedAt.After(lot.FundedAt) {
			return errors.New("DEL provenance tranches are not in funding order")
		}
	}
	return nil
}

func readDELProvenanceTime(raw []byte) (time.Time, error) {
	seconds, nanos := int64(binary.BigEndian.Uint64(raw[:8])), binary.BigEndian.Uint32(raw[8:])
	if seconds == 0 && nanos == 0 {
		return time.Time{}, nil
	}
	if seconds <= 0 || nanos >= uint32(time.Second) {
		return time.Time{}, errors.New("invalid DEL provenance block time")
	}
	return time.Unix(seconds, int64(nanos)).UTC(), nil
}

func writeDELProvenanceTime(out []byte, timestamp time.Time) {
	if !timestamp.IsZero() {
		binary.BigEndian.PutUint64(out[:8], uint64(timestamp.Unix()))
		binary.BigEndian.PutUint32(out[8:], uint32(timestamp.Nanosecond()))
	}
}

func combineDELProvenance(first, second []DELProvenanceTranche) ([]DELProvenanceTranche, error) {
	lots := append(slices.Clone(first), second...)
	slices.SortFunc(lots, func(a, b DELProvenanceTranche) int {
		return cmp.Compare(a.FundedHeight, b.FundedHeight)
	})
	combined := lots[:0]
	for _, lot := range lots {
		if n := len(combined); n != 0 && combined[n-1].FundedHeight == lot.FundedHeight {
			last := &combined[n-1]
			if !last.FundedAt.Equal(lot.FundedAt) {
				return nil, errors.New("same-height DEL provenance has different block times")
			}
			total := new(big.Int).Add(last.Amount.BigInt(), lot.Amount.BigInt())
			if total.BitLen() > 256 {
				return nil, errors.New("combined DEL provenance exceeds 256 bits")
			}
			last.Amount = sdkmath.NewIntFromBigInt(total)
			continue
		}
		combined = append(combined, lot)
	}
	return combined, nil
}
