package types

import (
	"bytes"
	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/tx/signing"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"math/big"
	"reflect"
	"testing"
	"time"
)

func testAddress(t *testing.T, prefix string, value byte) string {
	t.Helper()
	s, e := address.NewBech32Codec(prefix).BytesToString(bytes.Repeat([]byte{value}, 20))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func fixture(t *testing.T, id byte, power int64) Validator {
	t.Helper()
	v, e := NewValidator(testAddress(t, identity.Bech32PrefixValAddr, id), testAddress(t, identity.Bech32PrefixAccAddr, id), ed25519.GenPrivKeyFromSecret([]byte{id}).PubKey(), Description{Moniker: "native validator"}, sdkmath.LegacyMustNewDecFromStr("0.1"))
	if e != nil {
		t.Fatal(e)
	}
	v.Status = BondStatus_Bonded
	v.Stake = power
	v.Rewards = sdkmath.NewInt(3)
	v.TotalRewards = sdkmath.NewInt(10)
	return v
}
func roundTrip(t *testing.T, in, out proto.Message) {
	t.Helper()
	b, e := proto.Marshal(in)
	if e != nil {
		t.Fatal(e)
	}
	if e = proto.Unmarshal(b, out); e != nil {
		t.Fatal(e)
	}
}

func TestNativePowerAndFinancialRoundTrip(t *testing.T) {
	v := fixture(t, 1, 12345)
	var out Validator
	roundTrip(t, &v, &out)
	if out.ConsensusPower() != 12345 || out.PotentialConsensusPower() != 12345 || !out.Rewards.Equal(v.Rewards) || !out.TotalRewards.Equal(v.TotalRewards) {
		t.Fatal("native power/rewards were converted or lost")
	}
	out.Status = BondStatus_Unbonded
	if out.ConsensusPower() != 0 || out.PotentialConsensusPower() != 12345 {
		t.Fatal("incorrect native bond-status power")
	}
	out.Rewards = sdkmath.Int{}
	if out.Validate() == nil {
		t.Fatal("nil rewards accepted")
	}
	out = v
	out.OperatorAddress = testAddress(t, "cosmosvaloper", 1)
	if out.Validate() == nil {
		t.Fatal("foreign prefix accepted")
	}
	out = v
	out.ConsensusPubkey = nil
	if out.Validate() == nil {
		t.Fatal("missing key accepted")
	}
}

func TestNativeNFTReserveHoldsRoundTrip(t *testing.T) {
	s := NewStakeNFT("nft-token", []uint32{1, 2}, sdk.NewCoin("del", sdkmath.NewInt(100)))
	s.Holds = []*StakeHold{{Amount: sdkmath.NewInt(40), SubTokenIDs: []uint32{1}, HoldStartTime: 100, HoldEndTime: 200}}
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
	var out Stake
	roundTrip(t, &s, &out)
	if !s.Equal(&out) || !out.Stake.Amount.Equal(sdkmath.NewInt(100)) || !out.Holds[0].Amount.Equal(sdkmath.NewInt(40)) {
		t.Fatal("hold was lost or added twice to total reserve")
	}
	out.Holds[0].Amount = sdkmath.NewInt(41)
	if s.Equal(&out) {
		t.Fatal("equality ignored hold")
	}
	out.Holds[0].Amount = sdkmath.NewInt(101)
	if out.Validate() == nil {
		t.Fatal("oversized hold accepted")
	}
	out = s
	out.Holds = []*StakeHold{nil}
	if out.Validate() == nil {
		t.Fatal("nil hold accepted")
	}
	out = s
	out.SubTokenIDs = []uint32{1, 1}
	if out.Validate() == nil {
		t.Fatal("duplicate NFT accepted")
	}
	out = s
	out.Holds = []*StakeHold{{Amount: sdkmath.NewInt(1), SubTokenIDs: []uint32{3}, HoldStartTime: 1, HoldEndTime: 2}}
	if out.Validate() == nil {
		t.Fatal("foreign held NFT accepted")
	}
	zero := NewStakeCoin(sdk.NewCoin("del", sdkmath.ZeroInt()))
	if e := zero.Validate(); e != nil {
		t.Fatal("fully slashed zero stake must serialize", e)
	}
}

func TestNativeHoldAggregateRejectsOverflow(t *testing.T) {
	maximum := sdkmath.NewIntFromBigInt(new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1)))
	s := NewStakeCoin(sdk.NewCoin("del", maximum))
	s.Holds = []*StakeHold{{Amount: maximum, HoldStartTime: 1, HoldEndTime: 2}, {Amount: maximum, HoldStartTime: 2, HoldEndTime: 3}}
	if err := s.Validate(); err == nil {
		t.Fatal("overlapping total reserve accepted")
	}
}

func TestHistoricalNativeOrderAndIndependentCopy(t *testing.T) {
	a, b, c := fixture(t, 1, 7), fixture(t, 2, 9), fixture(t, 3, 7)
	input := []Validator{a, b, c}
	h, e := NewHistoricalInfo(cmtproto.Header{Height: 1, ChainID: "mintcandy_300001-1", Time: time.Unix(1, 0).UTC()}, input)
	if e != nil {
		t.Fatal(e)
	}
	if h.Valset[0].Stake != 9 || input[0].Stake != 7 {
		t.Fatal("incorrect native ordering or input mutation")
	}
	x, _ := h.Valset[1].GetConsAddr()
	y, _ := h.Valset[2].GetConsAddr()
	if bytes.Compare(x, y) >= 0 {
		t.Fatal("consensus address tie-break incorrect")
	}
	h.Valset[0].ConsensusPubkey.Value[0] ^= 1
	if bytes.Equal(h.Valset[0].ConsensusPubkey.Value, input[1].ConsensusPubkey.Value) {
		t.Fatal("copy aliases consensus key")
	}
	if _, e := NewHistoricalInfo(cmtproto.Header{Height: 1, ChainID: "chain", Time: time.Now()}, []Validator{a, a}); e == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestNativeParamsAndGenesisFailClosed(t *testing.T) {
	if e := DefaultGenesisState().Validate(); e != nil {
		t.Fatal(e)
	}
	tests := []func(*Params){func(p *Params) { p.MinSignedPerWindow = sdkmath.LegacyDec{} }, func(p *Params) { p.SlashFractionDowntime = sdkmath.LegacyMustNewDecFromStr("1.1") }, func(p *Params) { p.MaxValidators = 0 }, func(p *Params) { p.AutoUnbondTimeout = -1 }, func(p *Params) { p.RedelegationTime = 0 }}
	for _, mutate := range tests {
		p := DefaultParams()
		mutate(&p)
		if p.Validate() == nil {
			t.Fatal("malformed params accepted")
		}
	}
	p := DefaultParams()
	p.AutoUnbondTimeout = 0
	p.HistoricalEntries = 0
	if e := p.Validate(); e != nil {
		t.Fatal("explicitly disabled optional tracking rejected", e)
	}
	v := fixture(t, 1, 7)
	g := DefaultGenesisState()
	g.Validators = []Validator{v}
	g.LastValidatorPowers = []LastValidatorPower{{Address: v.OperatorAddress, Power: 7}}
	g.LastTotalPower = 7
	g.Delegations = []Delegation{{Delegator: v.RewardAddress, Validator: v.OperatorAddress, Stake: NewStakeCoin(sdk.NewCoin("del", sdkmath.NewInt(10)))}}
	if e := g.Validate(); e != nil {
		t.Fatal(e)
	}
	var out GenesisState
	roundTrip(t, g, &out)
	if e := out.Validate(); e != nil {
		t.Fatal(e)
	}
	g.LastTotalPower = 8
	if g.Validate() == nil {
		t.Fatal("inconsistent total power accepted")
	}
	g.LastTotalPower = 7
	g.Delegations[0].Validator = testAddress(t, identity.Bech32PrefixValAddr, 9)
	if g.Validate() == nil {
		t.Fatal("unknown validator accepted")
	}
	g.Delegations[0].Validator = v.OperatorAddress
	g.Delegations = append(g.Delegations, g.Delegations[0])
	if g.Validate() == nil {
		t.Fatal("duplicate delegation accepted")
	}
}

func TestNativeMessagesAndCryptoAnyRoundTrip(t *testing.T) {
	v := fixture(t, 1, 1)
	m := &MsgCreateValidator{OperatorAddress: v.OperatorAddress, RewardAddress: v.RewardAddress, ConsensusPubkey: v.ConsensusPubkey, Description: v.Description, Commission: v.Commission, Stake: sdk.NewCoin("del", sdkmath.NewInt(10))}
	cdc := NewCodec()
	data, e := cdc.MarshalInterface(m)
	if e != nil {
		t.Fatal(e)
	}
	var out sdk.Msg
	if e = cdc.UnmarshalInterface(data, &out); e != nil {
		t.Fatal(e)
	}
	got, ok := out.(*MsgCreateValidator)
	if !ok || !got.Stake.Equal(m.Stake) || got.ConsensusPubkey.TypeUrl != v.ConsensusPubkey.TypeUrl {
		t.Fatal("native message field loss")
	}
}

func TestNativeMessageSignerDescriptors(t *testing.T) {
	r, e := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{ProtoFiles: proto.HybridResolver, SigningOptions: signing.Options{AddressCodec: address.NewBech32Codec(identity.Bech32PrefixAccAddr), ValidatorAddressCodec: address.NewBech32Codec(identity.Bech32PrefixValAddr)}})
	if e != nil {
		t.Fatal(e)
	}
	RegisterInterfaces(r)
	cdc := codec.NewProtoCodec(r)
	messages := []struct {
		m        proto.Message
		field    string
		operator bool
	}{
		{&MsgCreateValidator{}, "OperatorAddress", true}, {&MsgEditValidator{}, "OperatorAddress", true}, {&MsgSetOnline{}, "Validator", true}, {&MsgSetOffline{}, "Validator", true},
		{&MsgDelegate{}, "Delegator", false}, {&MsgDelegateNFT{}, "Delegator", false}, {&MsgRedelegate{}, "Delegator", false}, {&MsgRedelegateNFT{}, "Delegator", false},
		{&MsgUndelegate{}, "Delegator", false}, {&MsgUndelegateNFT{}, "Delegator", false}, {&MsgCancelRedelegation{}, "Delegator", false}, {&MsgCancelRedelegationNFT{}, "Delegator", false},
		{&MsgCancelUndelegation{}, "Delegator", false}, {&MsgCancelUndelegationNFT{}, "Delegator", false}, {&MsgHaltChain{}, "Sender", false}, {&MsgResumeChain{}, "Sender", false}, {&MsgFreezeChain{}, "Sender", false}, {&MsgUnfreezeChain{}, "Sender", false},
	}
	for _, item := range messages {
		t.Run(proto.MessageName(item.m), func(t *testing.T) {
			prefix := identity.Bech32PrefixAccAddr
			if item.operator {
				prefix = identity.Bech32PrefixValAddr
			}
			reflect.ValueOf(item.m).Elem().FieldByName(item.field).SetString(testAddress(t, prefix, 5))
			a, e := codectypes.NewAnyWithValue(item.m)
			if e != nil {
				t.Fatal(e)
			}
			signers, _, e := cdc.GetMsgAnySigners(a)
			if e != nil {
				t.Fatal(e)
			}
			if len(signers) != 1 || !bytes.Equal(signers[0], bytes.Repeat([]byte{5}, 20)) {
				t.Fatal("incorrect descriptor signer")
			}
			reflect.ValueOf(item.m).Elem().FieldByName(item.field).SetString("invalid")
			a, e = codectypes.NewAnyWithValue(item.m)
			if e != nil {
				t.Fatal(e)
			}
			if _, _, e = cdc.GetMsgAnySigners(a); e == nil {
				t.Fatal("malformed signer accepted")
			}
		})
	}
}
