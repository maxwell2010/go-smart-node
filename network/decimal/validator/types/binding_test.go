package types

import (
	"bytes"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func bindingWord(n byte) []byte    { out := make([]byte, 32); out[31] = n; return out }
func bindingAddress(n byte) []byte { return bytes.Repeat([]byte{n}, 20) }
func isolatedBindingFixture() EVMStakeBinding {
	origin := &EVMNFTReference{Contract: bindingAddress(7), TokenID: make([]byte, 32), NativeTokenID: "origin-native", NativeSubTokenIDs: []uint32{5, 9}, Standard: 1155}
	position := make([]byte, 32)
	position[5] = 1 // Full uint256, well above uint64.
	receipt := &EVMNFTReference{Contract: bindingAddress(8), TokenID: position, NativeTokenID: "receipt-native", NativeSubTokenIDs: []uint32{1}, Standard: 721}
	return EVMStakeBinding{Kind: EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, Gateway: bindingAddress(3), StakeID: bytes.Repeat([]byte{0xff}, 32), Validator: bindingAddress(1), Beneficiary: bindingAddress(2), TokenContract: bindingAddress(8), TokenID: append([]byte(nil), position...), HoldTimestamp: bindingWord(0), PenaltyCursor: bytes.Repeat([]byte{0xff}, 32), RawAmount: sdkmath.OneInt(), RawNftBackedAmount: sdkmath.ZeroInt(), ReportedReserveAmount: sdkmath.NewInt(20), ReserveAsset: &EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, CoinCompanionStakeID: bindingWord(17), NFTPosition: &NFTPositionIdentity{Mode: NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT, Origin: origin, Custody: receipt, OriginQuantity: sdkmath.NewInt(2), ReceiptQuantity: sdkmath.OneInt(), Vault: bindingAddress(8), PositionID: append([]byte(nil), position...), ParentPositionID: bindingWord(1)}, NativeTarget: &NativeStakeTarget{StakeType: StakeType_NFT, ID: "receipt-native", SubTokenIDs: []uint32{1}}, Active: true, CreatedHeight: 7, UpdatedHeight: 8}
}
func cloneBinding(t *testing.T, in EVMStakeBinding) EVMStakeBinding {
	t.Helper()
	data, err := proto.Marshal(&in)
	require.NoError(t, err)
	var out EVMStakeBinding
	require.NoError(t, proto.Unmarshal(data, &out))
	return out
}
func bindingGenesisFixture(t *testing.T, b EVMStakeBinding) *GenesisState {
	g := DefaultGenesisState()
	g.BindingDomain = &EVMSourceDomain{ChainID: "mintcandy_990001-1", EVMChainID: 990001}
	g.Validators = []Validator{fixture(t, 1, 1)}
	g.EVMStakeBindings = []EVMStakeBinding{b}
	g.Delegations = []Delegation{{Delegator: testAddress(t, identity.Bech32PrefixAccAddr, 2), Validator: testAddress(t, identity.Bech32PrefixValAddr, 1), Stake: NewStakeNFT(b.NativeTarget.ID, b.NativeTarget.SubTokenIDs, sdk.NewCoin(identity.BaseDenom, b.ReportedReserveAmount))}}
	if len(b.CoinCompanionStakeID) != 0 {
		g.EVMStakeBindings = append(g.EVMStakeBindings, EVMStakeBinding{Kind: EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, Gateway: bindingAddress(3), StakeID: append([]byte(nil), b.CoinCompanionStakeID...), Validator: bindingAddress(1), Beneficiary: bindingAddress(2), TokenContract: bindingAddress(4), TokenID: bindingWord(0), HoldTimestamp: bindingWord(0), RawHoldStartTime: bindingWord(0), PenaltyCursor: bindingWord(0), RawAmount: b.ReportedReserveAmount, RawNftBackedAmount: b.ReportedReserveAmount, ReportedReserveAmount: sdkmath.ZeroInt(), ReserveAsset: &EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, NativeTarget: &NativeStakeTarget{StakeType: StakeType_Coin, ID: identity.BaseDenom}, Active: true})
	}
	return g
}
func TestBindingFullUint256AndIndependentOriginReceipt(t *testing.T) {
	b := isolatedBindingFixture()
	require.NoError(t, b.Validate())
	data, err := CanonicalBindingBytes(&b)
	require.NoError(t, err)
	var out EVMStakeBinding
	require.NoError(t, proto.Unmarshal(data, &out))
	require.NoError(t, out.Validate())
	require.Equal(t, b.TokenID, out.TokenID)
	require.Equal(t, b.StakeID, out.StakeID)
	require.Equal(t, b.PenaltyCursor, out.PenaltyCursor)
	require.Equal(t, make([]byte, 32), out.NFTPosition.Origin.TokenID, "origin ID0 is preserved, not treated as absent")
	require.Equal(t, sdkmath.NewInt(2), out.NFTPosition.OriginQuantity)
	require.Equal(t, sdkmath.OneInt(), out.NFTPosition.ReceiptQuantity)
	require.Equal(t, []uint32{5, 9}, out.NFTPosition.Origin.NativeSubTokenIDs)
	require.Equal(t, []uint32{1}, out.NativeTarget.SubTokenIDs)
	key, err := EVMStakeBindingKey(b.Kind, b.Gateway, b.StakeID)
	require.NoError(t, err)
	require.Equal(t, 53, len(key))
	require.Equal(t, b.StakeID, key[21:])
	// Parent1 is informative lineage, with no native parent object required.
	g := bindingGenesisFixture(t, b)
	require.NoError(t, g.Validate())
}
func TestBindingRejectsCastsAliasesAndInventedNFTGetterFields(t *testing.T) {
	tests := map[string]func(*EVMStakeBinding){
		"truncatedexternalID": func(b *EVMStakeBinding) { b.NFTPosition.Origin.TokenID = []byte{0} },
		"truncatedstakeID":    func(b *EVMStakeBinding) { b.StakeID = b.StakeID[1:] },
		"truncatedpositionID": func(b *EVMStakeBinding) { b.NFTPosition.PositionID = b.NFTPosition.PositionID[1:] },
		"zerovaultposition": func(b *EVMStakeBinding) {
			b.TokenID = bindingWord(0)
			b.NFTPosition.Custody.TokenID = bindingWord(0)
			b.NFTPosition.PositionID = bindingWord(0)
		},
		"originquantitycast": func(b *EVMStakeBinding) {
			b.NFTPosition.OriginQuantity = sdkmath.NewIntFromBigInt(new(big.Int).Lsh(big.NewInt(1), 200))
		},
		"receiptquantityconflated": func(b *EVMStakeBinding) { b.NFTPosition.ReceiptQuantity = sdkmath.NewInt(2) },
		"receiptusesoriginIDs":     func(b *EVMStakeBinding) { b.NativeTarget.SubTokenIDs = []uint32{5, 9} },
		"originIDduplicates":       func(b *EVMStakeBinding) { b.NFTPosition.Origin.NativeSubTokenIDs = []uint32{5, 5} },
		"originIDunordered":        func(b *EVMStakeBinding) { b.NFTPosition.Origin.NativeSubTokenIDs = []uint32{9, 5} },
		"nativeIDcastzero": func(b *EVMStakeBinding) {
			b.NFTPosition.Custody.NativeSubTokenIDs = []uint32{0}
			b.NativeTarget.SubTokenIDs = []uint32{0}
		},
		"inventedNFTholdgetter": func(b *EVMStakeBinding) { b.RawHoldStartTime = bindingWord(1) },
		"lostzerobackedkind":    func(b *EVMStakeBinding) { b.ReportedReserveAmount = sdkmath.ZeroInt(); b.ReserveAsset.ReserveKind = 0 },
		"nativekindwithtoken":   func(b *EVMStakeBinding) { b.ReserveAsset.Token = bindingAddress(4) },
		"parentnotearlier": func(b *EVMStakeBinding) {
			b.NFTPosition.ParentPositionID = append([]byte(nil), b.NFTPosition.PositionID...)
		},
		"sameoriginandreceipt": func(b *EVMStakeBinding) {
			b.NFTPosition.Origin.Contract = append([]byte(nil), b.NFTPosition.Custody.Contract...)
			b.NFTPosition.Origin.TokenID = append([]byte(nil), b.NFTPosition.Custody.TokenID...)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			b := cloneBinding(t, isolatedBindingFixture())
			mutate(&b)
			require.Error(t, b.Validate())
		})
	}
	zero := isolatedBindingFixture()
	zero.ReportedReserveAmount = sdkmath.ZeroInt()
	require.NoError(t, zero.Validate(), "slashed zero receipt retains its backed DEL kind and live identity")
}
func TestBindingDomainAndCustodyUniqueness(t *testing.T) {
	g := DefaultGenesisState()
	b := isolatedBindingFixture()
	g.Validators = []Validator{fixture(t, 1, 1)}
	g.EVMStakeBindings = []EVMStakeBinding{b}
	require.Error(t, g.Validate())
	g = bindingGenesisFixture(t, b)
	require.NoError(t, g.Validate())
	second := cloneBinding(t, b)
	second.StakeID = bindingWord(2)
	g.EVMStakeBindings = append(g.EVMStakeBindings, second)
	require.Error(t, g.Validate(), "same live receipt cannot support two binding keys")
	second.Active = false
	g.EVMStakeBindings[len(g.EVMStakeBindings)-1] = second
	require.NoError(t, g.Validate(), "inactive tombstone does not own live custody")
	for _, domain := range []*EVMSourceDomain{nil, {ChainID: "", EVMChainID: 1}, {ChainID: "network", EVMChainID: 0}, {ChainID: " network", EVMChainID: 1}, {ChainID: "network\n", EVMChainID: 1}} {
		require.Error(t, domain.Validate())
	}
}
func TestCoinBindingPreservesCompanionAndHoldDimensions(t *testing.T) {
	b := EVMStakeBinding{Kind: EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, Gateway: bindingAddress(3), StakeID: bindingWord(11), Validator: bindingAddress(1), Beneficiary: bindingAddress(2), TokenContract: bindingAddress(4), TokenID: bindingWord(0), HoldTimestamp: bindingWord(5), RawHoldStartTime: bindingWord(1), PenaltyCursor: bindingWord(2), RawAmount: sdkmath.NewInt(100), RawNftBackedAmount: sdkmath.NewInt(40), ReportedReserveAmount: sdkmath.ZeroInt(), ReserveAsset: &EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, NativeTarget: &NativeStakeTarget{StakeType: StakeType_Coin, ID: identity.BaseDenom}, NativeHoldStartTime: 1, Active: true}
	require.NoError(t, b.Validate(), "normalized total may partition holds without adding them again")
	b.RawNftBackedAmount = sdkmath.NewInt(101)
	require.Error(t, b.Validate())
}
func TestFrozenIndexZeroAndFullCompletionWord(t *testing.T) {
	source := isolatedBindingFixture()
	b := EVMFrozenBinding{Gateway: source.Gateway, FrozenIndex: bindingWord(0), SourceKind: source.Kind, SourceStakeID: source.StakeID, SourceValidator: source.Validator, Beneficiary: source.Beneficiary, FreezeType: 1, CompletionTimestamp: bindingWord(50), RawAmount: sdkmath.OneInt(), ReportedReserveAmount: sdkmath.NewInt(20), ReserveAsset: source.ReserveAsset, NFTPosition: source.NFTPosition, NativeTarget: source.NativeTarget, NativeEntryHash: bindingWord(6), Active: true}
	require.NoError(t, b.Validate())
	key, err := EVMFrozenBindingKey(b.Gateway, b.FrozenIndex)
	require.NoError(t, err)
	require.Equal(t, bindingWord(0), key[20:])
	b.CompletionTimestamp = []byte{50}
	require.Error(t, b.Validate())
}

func TestBindingInactiveCompanionCannotSupplyActiveNFTBacking(t *testing.T) {
	g := bindingGenesisFixture(t, isolatedBindingFixture())
	require.NoError(t, g.Validate())
	g.EVMStakeBindings[1].Active = false
	require.Error(t, g.Validate())
}
func TestNativeQueueHashRequiresItsTypedEntry(t *testing.T) {
	var absent *UndelegationEntry
	_, err := NativeQueueEntryHash(1, "owner", "validator", "", absent)
	require.Error(t, err)
	_, err = NativeQueueEntryHash(1, "owner", "validator", "", &RedelegationEntry{})
	require.Error(t, err)
	_, err = NativeQueueEntryHash(2, "owner", "validator", "target", &UndelegationEntry{})
	require.Error(t, err)
}
