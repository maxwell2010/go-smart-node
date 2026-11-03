package governance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"math/big"
	"reflect"
	"sort"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/formulas"
	nativetypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// AggregateReserveSource values complete active native ownership, including NFT
// reserve amounts, using same-context custom coin records. No RPC or bank mint
// is involved. This is an explicit governance policy, not a redemption quote.
type CoinSnapshotReader interface {
	GetCoin(sdk.Context, string) (cointypes.Coin, error)
	GetParams(sdk.Context) (cointypes.Params, error)
}

type AggregateReserveSource struct{ Coins CoinSnapshotReader }

func ownershipGenesis(r Request) (*nativetypes.GenesisState, error) {
	var g nativetypes.GenesisState
	if err := proto.Unmarshal(r.NativeGenesis, &g); err != nil {
		return nil, err
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if g.Params.BaseDenom != identity.BaseDenom {
		return nil, errors.New("ownership requires del base")
	}
	return &g, nil
}

func (s AggregateReserveSource) DependencyHash(ctx context.Context, r Request) ([32]byte, error) {
	if s.Coins == nil || (reflect.ValueOf(s.Coins).Kind() == reflect.Pointer && reflect.ValueOf(s.Coins).IsNil()) {
		return [32]byte{}, errors.New("native coin state is required")
	}
	g, err := ownershipGenesis(r)
	if err != nil {
		return [32]byte{}, err
	}
	c, err := sdkContext(ctx)
	if err != nil {
		return [32]byte{}, err
	}
	if c.ChainID() != r.ChainID || c.BlockHeight() != r.Height {
		return [32]byte{}, errors.New("valuation context mismatch")
	}
	p, err := s.Coins.GetParams(c)
	if err != nil {
		return [32]byte{}, err
	}
	if p.BaseDenom != identity.BaseDenom {
		return [32]byte{}, errors.New("coin base denomination mismatch")
	}
	denoms := map[string]bool{}
	for _, d := range g.Delegations {
		if d.Stake.Stake.Denom != identity.BaseDenom {
			denoms[d.Stake.Stake.Denom] = true
		}
	}
	names := make([]string, 0, len(denoms))
	for name := range denoms {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte("native-aggregate-ownership-inputs-v1"))
	h.Write(r.StateHash[:])
	write := func(data []byte) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		h.Write(size[:])
		h.Write(data)
	}
	b, err := proto.Marshal(&p)
	if err != nil {
		return [32]byte{}, err
	}
	write(b)
	for _, name := range names {
		coin, err := s.Coins.GetCoin(c, name)
		if err != nil {
			return [32]byte{}, err
		}
		b, err = proto.Marshal(&coin)
		if err != nil {
			return [32]byte{}, err
		}
		write(b)
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result, nil
}

// saleReturn contains legacy formula panics within an ordinary valuation error.
// All arithmetic outside the pinned formula uses unbounded intermediates before
// checking the final SDK integer bound.
func saleReturn(supply, reserve, amount sdkmath.Int, crr uint32) (result sdkmath.Int, err error) {
	defer func() {
		if recover() != nil {
			result = sdkmath.Int{}
			err = errors.New("custom ownership formula failed")
		}
	}()
	if !supply.IsPositive() || reserve.IsNegative() || amount.IsNegative() || amount.GT(supply) || crr < 10 || crr > 100 {
		return sdkmath.Int{}, errors.New("invalid aggregate custom reserve inputs")
	}
	if crr == 100 {
		v := new(big.Int).Mul(reserve.BigInt(), amount.BigInt())
		v.Quo(v, supply.BigInt())
		return sdkmath.NewIntFromBigInt(v), nil
	}
	result = formulas.CalculateSaleReturn(supply, reserve, uint(crr), amount)
	if result.IsNil() || result.IsNegative() || result.GT(reserve) {
		return sdkmath.Int{}, errors.New("custom ownership formula outside reserve bounds")
	}
	return result, nil
}

func (s AggregateReserveSource) SnapshotWeights(ctx context.Context, r Request) (Weights, error) {
	if _, err := s.DependencyHash(ctx, r); err != nil {
		return Weights{}, err
	}
	g, err := ownershipGenesis(r)
	if err != nil {
		return Weights{}, err
	}
	c, err := sdkContext(ctx)
	if err != nil {
		return Weights{}, err
	}
	totals := map[string]*big.Int{}
	groups := map[string][]int{}
	keys := make([][32]byte, len(g.Delegations))
	for i, d := range g.Delegations {
		name := d.Stake.Stake.Denom
		if totals[name] == nil {
			totals[name] = new(big.Int)
		}
		totals[name].Add(totals[name], d.Stake.Stake.Amount.BigInt())
		if totals[name].BitLen() > 256 {
			return Weights{}, errors.New("aggregate ownership overflows")
		}
		groups[name] = append(groups[name], i)
		keys[i], err = PositionHash(d)
		if err != nil {
			return Weights{}, err
		}
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	amounts := make([]*big.Int, len(g.Delegations))
	for _, name := range names {
		ids := groups[name]
		total := totals[name]
		if name == identity.BaseDenom {
			for _, i := range ids {
				amounts[i] = new(big.Int).Mul(g.Delegations[i].Stake.Stake.Amount.BigInt(), scale)
			}
			continue
		}
		coin, err := s.Coins.GetCoin(c, name)
		if err != nil {
			return Weights{}, err
		}
		if total.Sign() == 0 {
			for _, i := range ids {
				amounts[i] = new(big.Int)
			}
			continue
		}
		value, err := saleReturn(coin.Volume, coin.Reserve, sdkmath.NewIntFromBigInt(total), coin.CRR)
		if err != nil {
			return Weights{}, fmt.Errorf("value %s: %w", name, err)
		}
		budget := new(big.Int).Mul(value.BigInt(), scale)
		allocated := new(big.Int)
		remainders := map[int]*big.Int{}
		for _, i := range ids {
			numerator := new(big.Int).Mul(budget, g.Delegations[i].Stake.Stake.Amount.BigInt())
			q, rem := new(big.Int), new(big.Int)
			q.QuoRem(numerator, total, rem)
			amounts[i] = q
			remainders[i] = rem
			allocated.Add(allocated, q)
		}
		sort.Slice(ids, func(i, j int) bool {
			a, b := ids[i], ids[j]
			if cmp := remainders[a].Cmp(remainders[b]); cmp != 0 {
				return cmp > 0
			}
			return bytes.Compare(keys[a][:], keys[b][:]) < 0
		})
		dust := new(big.Int).Sub(budget, allocated)
		if !dust.IsInt64() || dust.Int64() >= int64(len(ids)) {
			return Weights{}, errors.New("invalid ownership allocation remainder")
		}
		for i := int64(0); i < dust.Int64(); i++ {
			amounts[ids[i]].Add(amounts[ids[i]], big.NewInt(1))
		}
	}
	out := Weights{ChainID: r.ChainID, Height: r.Height, StateHash: r.StateHash, ValuationID: "native-aggregate-sale-return-ownership-v1"}
	byValidator := map[string]*big.Int{}
	for _, v := range g.Validators {
		byValidator[v.OperatorAddress] = new(big.Int)
	}
	for i, d := range g.Delegations {
		if amounts[i].BitLen() > 256 {
			return Weights{}, errors.New("scaled position ownership overflows")
		}
		out.Positions = append(out.Positions, PositionWeight{PositionHash: keys[i], Ownership: sdkmath.NewIntFromBigInt(amounts[i])})
		byValidator[d.Validator].Add(byValidator[d.Validator], amounts[i])
	}
	for _, v := range g.Validators {
		n := byValidator[v.OperatorAddress]
		if n.BitLen() > 256 {
			return Weights{}, errors.New("scaled validator ownership overflows")
		}
		out.Totals = append(out.Totals, ValidatorOwnership{Validator: v.OperatorAddress, Ownership: sdkmath.NewIntFromBigInt(n)})
	}
	return out, nil
}
