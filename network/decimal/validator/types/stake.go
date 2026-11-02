package types

import (
	sdkmath "cosmossdk.io/math"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"strings"
)

func NewStakeCoin(coin sdk.Coin) Stake {
	return Stake{Type: StakeType_Coin, ID: coin.Denom, Stake: coin}
}
func NewStakeNFT(tokenID string, ids []uint32, reserve sdk.Coin) Stake {
	return Stake{Type: StakeType_NFT, ID: tokenID, Stake: reserve, SubTokenIDs: append([]uint32(nil), ids...)}
}

// Validate preserves total reserve and held portions as separate native fields.
// Holds partition the stored total; they must not be added to it again.
func (s Stake) Validate() error {
	if s.Type < StakeType_Coin || s.Type > StakeType_NFTHold {
		return fmt.Errorf("invalid stake type")
	}
	if s.Stake.Amount.IsNil() || s.Stake.Amount.IsNegative() {
		return fmt.Errorf("stake amount must be initialized and nonnegative")
	}
	if err := sdk.ValidateDenom(s.Stake.Denom); err != nil {
		return err
	}
	nft := s.Type == StakeType_NFT || s.Type == StakeType_NFTHold
	if strings.TrimSpace(s.ID) == "" || (!nft && s.ID != s.Stake.Denom) {
		return fmt.Errorf("invalid native stake ID")
	}
	ids := map[uint32]bool{}
	for _, id := range s.SubTokenIDs {
		if id == 0 || ids[id] {
			return fmt.Errorf("duplicate or zero sub-token ID")
		}
		ids[id] = true
	}
	if (!nft && len(ids) > 0) || (nft && len(ids) == 0) {
		return fmt.Errorf("sub-token IDs do not match stake type")
	}
	total := sdkmath.ZeroInt()
	heldIDs := map[uint32]bool{}
	for _, h := range s.Holds {
		if h == nil || h.Amount.IsNil() || h.Amount.IsNegative() || h.HoldStartTime < 0 || h.HoldEndTime <= h.HoldStartTime {
			return fmt.Errorf("invalid native stake hold")
		}
		// Bound each addition by the remaining reserve before Int can overflow.
		if h.Amount.GT(s.Stake.Amount.Sub(total)) {
			return fmt.Errorf("held reserve exceeds total stake")
		}
		total = total.Add(h.Amount)
		if !nft && len(h.SubTokenIDs) > 0 {
			return fmt.Errorf("coin hold cannot contain sub-token IDs")
		}
		for _, id := range h.SubTokenIDs {
			if !ids[id] || heldIDs[id] {
				return fmt.Errorf("held sub-token must belong uniquely to stake")
			}
			heldIDs[id] = true
		}
	}
	if total.GT(s.Stake.Amount) {
		return fmt.Errorf("held reserve exceeds total stake")
	}
	return nil
}

// Equal includes holds. It deliberately preserves the serialized ID ordering.
func (s *Stake) Equal(other interface{}) bool {
	var o *Stake
	switch v := other.(type) {
	case *Stake:
		o = v
	case Stake:
		o = &v
	default:
		return false
	}
	if s == nil || o == nil {
		return s == nil && o == nil
	}
	a, e := proto.Marshal(s)
	if e != nil {
		return false
	}
	b, e := proto.Marshal(o)
	return e == nil && string(a) == string(b)
}
