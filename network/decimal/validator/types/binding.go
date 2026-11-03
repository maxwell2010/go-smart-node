package types

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	nativenfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

const MaxPositionOriginUnits = int(nativenfttypes.MaxIsolatedOriginUnits)
const MaxBindingPageSize uint32 = 256
const MaxBindingBatchSize = 64

func (d *EVMSourceDomain) Validate() error {
	if d == nil || d.ChainID == "" || len(d.ChainID) > 128 || strings.TrimSpace(d.ChainID) != d.ChainID || strings.ContainsAny(d.ChainID, "\x00\r\n") || d.EVMChainID == 0 {
		return errors.New("invalid configured EVM binding domain")
	}
	return nil
}
func fullWord(value []byte) bool  { return len(value) == 32 }
func zeroWord(value []byte) bool  { return fullWord(value) && bytes.Equal(value, make([]byte, 32)) }
func address20(value []byte) bool { return len(value) == 20 && !bytes.Equal(value, make([]byte, 20)) }
func rawAmount(value sdkmath.Int) bool {
	return !value.IsNil() && !value.IsNegative() && value.BigInt().BitLen() <= 256
}
func positionIDs(ids []uint32) error {
	if len(ids) == 0 || len(ids) > MaxPositionOriginUnits {
		return errors.New("invalid exact native NFT unit count")
	}
	for i, id := range ids {
		if id == 0 || (i > 0 && ids[i-1] >= id) {
			return errors.New("exact native NFT units must be positive, sorted and unique")
		}
	}
	return nil
}
func (r *EVMNFTReference) Validate() error {
	if r == nil || !address20(r.Contract) || !fullWord(r.TokenID) || r.NativeTokenID == "" || len(r.NativeTokenID) > 256 || strings.TrimSpace(r.NativeTokenID) != r.NativeTokenID {
		return errors.New("invalid full external/native NFT reference")
	}
	if r.Standard != 721 && r.Standard != 1155 {
		return errors.New("unsupported explicit NFT standard")
	}
	if err := positionIDs(r.NativeSubTokenIDs); err != nil {
		return err
	}
	if r.Standard == 721 && len(r.NativeSubTokenIDs) != 1 {
		return errors.New("ERC721 reference must identify exactly one native unit")
	}
	return nil
}
func sameNFTRef(a, b *EVMNFTReference) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return bytes.Equal(a.Contract, b.Contract) && bytes.Equal(a.TokenID, b.TokenID) && a.NativeTokenID == b.NativeTokenID && a.Standard == b.Standard && sameUnitIDs(a.NativeSubTokenIDs, b.NativeSubTokenIDs)
}
func sameUnitIDs(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (p *NFTPositionIdentity) Validate() error {
	if p == nil {
		return errors.New("NFT position identity is missing")
	}
	if err := p.Origin.Validate(); err != nil {
		return err
	}
	if err := p.Custody.Validate(); err != nil {
		return err
	}
	if !rawAmount(p.OriginQuantity) || !p.OriginQuantity.IsPositive() || !p.OriginQuantity.Equal(sdkmath.NewInt(int64(len(p.Origin.NativeSubTokenIDs)))) || !rawAmount(p.ReceiptQuantity) || !p.ReceiptQuantity.Equal(sdkmath.OneInt()) || p.Custody.Standard != 721 {
		return errors.New("origin quantity and live receipt quantity must remain distinct and exact")
	}
	switch p.Mode {
	case NFTPositionMode_NFT_POSITION_DIRECT_721:
		if p.Origin.Standard != 721 || !sameNFTRef(p.Origin, p.Custody) || len(p.Vault) != 0 || len(p.PositionID) != 0 || len(p.ParentPositionID) != 0 {
			return errors.New("direct721 identity cannot alias an isolated vault position")
		}
	case NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT:
		if !address20(p.Vault) || !bytes.Equal(p.Vault, p.Custody.Contract) || !fullWord(p.PositionID) || zeroWord(p.PositionID) || !bytes.Equal(p.PositionID, p.Custody.TokenID) || !fullWord(p.ParentPositionID) {
			return errors.New("isolated receipt must retain full vault-scoped position identity")
		}
		// Parent is lineage information, never a required live native lookup.
		if !zeroWord(p.ParentPositionID) && bytes.Compare(p.ParentPositionID, p.PositionID) >= 0 {
			return errors.New("isolated position parent must precede its monotonic child")
		}
		if bytes.Equal(p.Origin.Contract, p.Custody.Contract) && bytes.Equal(p.Origin.TokenID, p.Custody.TokenID) {
			return errors.New("origin and isolated receipt identities must not be conflated")
		}
	default:
		return errors.New("unsupported NFT position mode")
	}
	return nil
}
func (a *EVMReserveAsset) Validate() error {
	if a == nil || len(a.Token) != 20 {
		return errors.New("reserve asset requires its explicit kind and address word")
	}
	if err := sdk.ValidateDenom(a.NativeDenom); err != nil {
		return err
	}
	switch a.ReserveKind {
	case 1:
		if !bytes.Equal(a.Token, make([]byte, 20)) {
			return errors.New("native DEL reserve cannot declare an ERC20 token")
		}
	case 2:
		if !address20(a.Token) {
			return errors.New("ERC20 reserve requires its actual token address")
		}
	default:
		return errors.New("reserve kind must retain pinned DEL1/DRC202 semantics, including zero backing")
	}
	return nil
}
func (t *NativeStakeTarget) Validate() error {
	if t == nil || t.ID == "" || len(t.ID) > 256 || strings.TrimSpace(t.ID) != t.ID {
		return errors.New("native stake target is missing")
	}
	switch t.StakeType {
	case StakeType_Coin, StakeType_CoinHold:
		if len(t.SubTokenIDs) != 0 {
			return errors.New("coin target cannot contain NFT units")
		}
		return sdk.ValidateDenom(t.ID)
	case StakeType_NFT, StakeType_NFTHold:
		return positionIDs(t.SubTokenIDs)
	default:
		return errors.New("invalid native stake target type")
	}
}
func targetMatchesNFT(t *NativeStakeTarget, p *NFTPositionIdentity) bool {
	return t.ID == p.Custody.NativeTokenID && sameUnitIDs(t.SubTokenIDs, p.Custody.NativeSubTokenIDs)
}
func (b *EVMStakeBinding) Validate() error {
	if b == nil || !address20(b.Gateway) || !fullWord(b.StakeID) || !address20(b.Validator) || !address20(b.Beneficiary) || !address20(b.TokenContract) || !fullWord(b.TokenID) || !fullWord(b.HoldTimestamp) || !fullWord(b.PenaltyCursor) {
		return errors.New("stake binding must retain full source identities and uint256 words")
	}
	if !rawAmount(b.RawAmount) || !rawAmount(b.RawNftBackedAmount) || !rawAmount(b.ReportedReserveAmount) || b.NativeHoldStartTime < 0 || b.CreatedHeight < 0 || b.UpdatedHeight < b.CreatedHeight {
		return errors.New("invalid stake binding values or provenance")
	}
	if err := b.ReserveAsset.Validate(); err != nil {
		return err
	}
	if err := b.NativeTarget.Validate(); err != nil {
		return err
	}
	held := !zeroWord(b.HoldTimestamp)
	switch b.Kind {
	case EVMStakeBindingKind_EVM_STAKE_BINDING_COIN:
		if !zeroWord(b.TokenID) || !fullWord(b.RawHoldStartTime) || b.NFTPosition != nil || len(b.CoinCompanionStakeID) != 0 || b.RawNftBackedAmount.GT(b.RawAmount) || !b.ReportedReserveAmount.IsZero() || b.NativeTarget.ID != b.ReserveAsset.NativeDenom {
			return errors.New("coin binding must distinguish direct amount from NFT companion backing")
		}
		if b.NativeTarget.StakeType != StakeType_Coin && b.NativeTarget.StakeType != StakeType_CoinHold {
			return errors.New("coin/native target mismatch")
		}
		if b.NativeTarget.StakeType == StakeType_CoinHold && !held {
			return errors.New("held native coin target requires source hold identity")
		}
		if b.ReserveAsset.ReserveKind == 2 && !bytes.Equal(b.TokenContract, b.ReserveAsset.Token) {
			return errors.New("coin token differs from its declared reserve asset")
		}
	case EVMStakeBindingKind_EVM_STAKE_BINDING_NFT:
		if len(b.RawHoldStartTime) != 0 || !b.RawNftBackedAmount.IsZero() || (len(b.CoinCompanionStakeID) != 0 && !fullWord(b.CoinCompanionStakeID)) {
			return errors.New("NFT binding cannot invent coin getter fields or truncate its companion ID")
		}
		if err := b.NFTPosition.Validate(); err != nil {
			return err
		}
		if (b.NativeTarget.StakeType != StakeType_NFT && b.NativeTarget.StakeType != StakeType_NFTHold) || !targetMatchesNFT(b.NativeTarget, b.NFTPosition) || !bytes.Equal(b.TokenContract, b.NFTPosition.Custody.Contract) || !bytes.Equal(b.TokenID, b.NFTPosition.Custody.TokenID) {
			return errors.New("native NFT target must reference the actual live custody receipt")
		}
		if b.NativeTarget.StakeType == StakeType_NFTHold && !held {
			return errors.New("held native NFT target requires source hold identity")
		}
		if b.Active && !b.RawAmount.Equal(b.NFTPosition.ReceiptQuantity) {
			return errors.New("live NFT raw quantity differs from its receipt quantity")
		}
	default:
		return errors.New("invalid EVM stake binding kind")
	}
	if !held && b.NativeHoldStartTime != 0 {
		return errors.New("unheld binding cannot invent native hold provenance")
	}
	return nil
}
func (b *EVMFrozenBinding) Validate() error {
	if b == nil || !address20(b.Gateway) || !fullWord(b.FrozenIndex) || !fullWord(b.SourceStakeID) || !address20(b.SourceValidator) || !address20(b.Beneficiary) || !fullWord(b.CompletionTimestamp) || zeroWord(b.CompletionTimestamp) || !fullWord(b.NativeEntryHash) {
		return errors.New("frozen binding requires full queue and native entry identities")
	}
	if !rawAmount(b.RawAmount) || !rawAmount(b.ReportedReserveAmount) || b.CreatedHeight < 0 || b.UpdatedHeight < b.CreatedHeight {
		return errors.New("invalid frozen binding values or provenance")
	}
	if err := b.ReserveAsset.Validate(); err != nil {
		return err
	}
	if err := b.NativeTarget.Validate(); err != nil {
		return err
	}
	if b.FreezeType == 1 {
		if len(b.DestinationValidator) != 0 {
			return errors.New("withdrawal cannot declare a destination validator")
		}
	} else if b.FreezeType == 2 {
		if !address20(b.DestinationValidator) || bytes.Equal(b.SourceValidator, b.DestinationValidator) {
			return errors.New("invalid frozen transfer destination")
		}
	} else {
		return errors.New("invalid frozen stake type")
	}
	switch b.SourceKind {
	case EVMStakeBindingKind_EVM_STAKE_BINDING_COIN:
		if b.NFTPosition != nil || (b.NativeTarget.StakeType != StakeType_Coin && b.NativeTarget.StakeType != StakeType_CoinHold) || b.NativeTarget.ID != b.ReserveAsset.NativeDenom || !b.ReportedReserveAmount.IsZero() {
			return errors.New("invalid frozen coin target")
		}
	case EVMStakeBindingKind_EVM_STAKE_BINDING_NFT:
		if err := b.NFTPosition.Validate(); err != nil {
			return err
		}
		if (b.NativeTarget.StakeType != StakeType_NFT && b.NativeTarget.StakeType != StakeType_NFTHold) || !targetMatchesNFT(b.NativeTarget, b.NFTPosition) || (b.Active && !b.RawAmount.Equal(b.NFTPosition.ReceiptQuantity)) {
			return errors.New("frozen NFT must retain its exact receipt units")
		}
	default:
		return errors.New("invalid frozen source kind")
	}
	return nil
}

// Framed references retain full identifiers; no uint32/uint64 token-ID cast.
func EVMStakeBindingKey(kind EVMStakeBindingKind, gateway, id []byte) ([]byte, error) {
	if (kind != EVMStakeBindingKind_EVM_STAKE_BINDING_COIN && kind != EVMStakeBindingKind_EVM_STAKE_BINDING_NFT) || !address20(gateway) || !fullWord(id) {
		return nil, errors.New("invalid stake binding key")
	}
	out := []byte{byte(kind)}
	out = append(out, gateway...)
	return append(out, id...), nil
}
func EVMFrozenBindingKey(gateway, index []byte) ([]byte, error) {
	if !address20(gateway) || !fullWord(index) {
		return nil, errors.New("invalid frozen binding key")
	}
	out := append([]byte(nil), gateway...)
	return append(out, index...), nil
}
func CustodyUnitKey(nativeID string, id uint32) ([]byte, error) {
	if nativeID == "" || len(nativeID) > 256 || id == 0 {
		return nil, errors.New("invalid exact custody unit")
	}
	out := make([]byte, 4, 4+len(nativeID)+4)
	binary.BigEndian.PutUint32(out, uint32(len(nativeID)))
	out = append(out, []byte(nativeID)...)
	var suffix [4]byte
	binary.BigEndian.PutUint32(suffix[:], id)
	return append(out, suffix[:]...), nil
}
func CanonicalBindingBytes(message proto.Message) ([]byte, error) {
	data, err := proto.Marshal(message)
	if err != nil {
		return nil, err
	}
	if len(data) > 64*1024 {
		return nil, fmt.Errorf("binding exceeds64KiB record bound")
	}
	return data, nil
}
