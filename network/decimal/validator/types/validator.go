package types

import (
	"bytes"
	sdkmath "cosmossdk.io/math"
	"fmt"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"sort"
	"strings"
	"time"
)

func NewValidator(operator, reward string, key cryptotypes.PubKey, description Description, commission sdkmath.LegacyDec) (Validator, error) {
	if key == nil {
		return Validator{}, fmt.Errorf("missing consensus key")
	}
	a, err := codectypes.NewAnyWithValue(key)
	if err != nil {
		return Validator{}, err
	}
	v := Validator{OperatorAddress: operator, RewardAddress: reward, ConsensusPubkey: a, Description: description,
		Commission: commission, Status: BondStatus_Unbonded, UnbondingTime: time.Unix(0, 0).UTC(), Rewards: sdkmath.ZeroInt(), TotalRewards: sdkmath.ZeroInt()}
	return v, v.Validate()
}

func (v Validator) GetConsAddr() (sdk.ConsAddress, error) {
	k, e := v.ConsensusPubKey()
	if e != nil {
		return nil, e
	}
	return sdk.ConsAddress(k.Address()), nil
}

func validateAddress(value, prefix string) error {
	b, err := address.NewBech32Codec(prefix).StringToBytes(value)
	if err != nil {
		return err
	}
	if len(b) != 20 {
		return fmt.Errorf("address must contain 20 bytes")
	}
	return nil
}

// ConsensusPubKey unpacks an independent Any without relying on a mutable cache.
func (v Validator) ConsensusPubKey() (cryptotypes.PubKey, error) {
	if v.ConsensusPubkey == nil {
		return nil, fmt.Errorf("missing consensus public key")
	}
	registry := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(registry)
	var key cryptotypes.PubKey
	a := &codectypes.Any{TypeUrl: v.ConsensusPubkey.TypeUrl, Value: append([]byte(nil), v.ConsensusPubkey.Value...)}
	if err := codec.NewProtoCodec(registry).UnpackAny(a, &key); err != nil {
		return nil, err
	}
	if key == nil || key.Type() != "ed25519" || len(key.Bytes()) != 32 {
		return nil, fmt.Errorf("native validator requires an ed25519 consensus key")
	}
	return key, nil
}

func (v Validator) Validate() error {
	if err := validateAddress(v.OperatorAddress, identity.Bech32PrefixValAddr); err != nil {
		return fmt.Errorf("operator: %w", err)
	}
	if err := validateAddress(v.RewardAddress, identity.Bech32PrefixAccAddr); err != nil {
		return fmt.Errorf("reward address: %w", err)
	}
	if _, err := v.ConsensusPubKey(); err != nil {
		return err
	}
	if v.Commission.IsNil() || v.Commission.IsNegative() || v.Commission.GT(sdkmath.LegacyOneDec()) {
		return fmt.Errorf("commission must be initialized and between zero and one")
	}
	if v.Status < BondStatus_Unbonded || v.Status > BondStatus_Bonded {
		return fmt.Errorf("invalid bond status")
	}
	if v.Stake < 0 || v.Stake > cmttypes.MaxTotalVotingPower || v.UnbondingHeight < 0 || v.Rewards.IsNil() || v.TotalRewards.IsNil() || v.Rewards.IsNegative() || v.TotalRewards.IsNegative() || v.Rewards.GT(v.TotalRewards) {
		return fmt.Errorf("invalid validator rewards, power or unbonding height")
	}
	if v.DRC20Contract != "" && (len(v.DRC20Contract) != 42 || !strings.HasPrefix(v.DRC20Contract, "0x") || !common.IsHexAddress(v.DRC20Contract) || common.HexToAddress(v.DRC20Contract) == (common.Address{})) {
		return fmt.Errorf("invalid validator token contract")
	}
	if strings.TrimSpace(v.Description.Moniker) == "" || len(v.Description.Moniker) > 70 || len(v.Description.Identity) > 3000 || len(v.Description.Website) > 140 || len(v.Description.SecurityContact) > 140 || len(v.Description.Details) > 280 {
		return fmt.Errorf("invalid validator description")
	}
	return nil
}

// ConsensusPower uses native computed integer stake, not SDK token/share aliases
// or a second power reduction. Dynamic ValidatorRS must be hydrated by a keeper.
func (v Validator) ConsensusPower() int64 {
	if v.Status == BondStatus_Bonded {
		return v.Stake
	}
	return 0
}
func (v Validator) PotentialConsensusPower() int64 { return v.Stake }

// NewHistoricalInfo copies financial fields through protobuf serialization and
// orders the set by native power descending, consensus-address bytes ascending.
func NewHistoricalInfo(header cmtproto.Header, validators []Validator) (HistoricalInfo, error) {
	original := &HistoricalInfo{Header: header, Valset: validators}
	data, err := proto.Marshal(original)
	if err != nil {
		return HistoricalInfo{}, err
	}
	var result HistoricalInfo
	if err = proto.Unmarshal(data, &result); err != nil {
		return result, err
	}
	addresses := map[string][]byte{}
	for _, v := range result.Valset {
		if _, ok := addresses[v.OperatorAddress]; ok {
			return HistoricalInfo{}, fmt.Errorf("duplicate historical operator")
		}
		k, e := v.ConsensusPubKey()
		if e != nil {
			return HistoricalInfo{}, e
		}
		addresses[v.OperatorAddress] = k.Address()
	}
	sort.Slice(result.Valset, func(i, j int) bool {
		a, b := result.Valset[i], result.Valset[j]
		if a.ConsensusPower() != b.ConsensusPower() {
			return a.ConsensusPower() > b.ConsensusPower()
		}
		return bytes.Compare(addresses[a.OperatorAddress], addresses[b.OperatorAddress]) < 0
	})
	return result, result.ValidateBasic()
}

func (h HistoricalInfo) ValidateBasic() error {
	if h.Header.Height <= 0 || h.Header.ChainID == "" || h.Header.Time.IsZero() {
		return fmt.Errorf("historical header is incomplete")
	}
	seen := map[string]bool{}
	operators := map[string]bool{}
	var previous []byte
	var power, total int64
	for i, v := range h.Valset {
		k, err := v.ConsensusPubKey()
		if err != nil {
			return err
		}
		a := k.Address()
		if seen[string(a)] || operators[v.OperatorAddress] {
			return fmt.Errorf("duplicate historical validator")
		}
		seen[string(a)] = true
		operators[v.OperatorAddress] = true
		if err := validateAddress(v.OperatorAddress, identity.Bech32PrefixValAddr); err != nil {
			return err
		}
		if v.Stake < 0 || v.Stake > cmttypes.MaxTotalVotingPower-total || v.Status != BondStatus_Bonded {
			return fmt.Errorf("historical validator power is invalid")
		}
		total += v.Stake
		p := v.ConsensusPower()
		if i > 0 && (p > power || (p == power && bytes.Compare(previous, a) >= 0)) {
			return fmt.Errorf("historical validators are not ordered by power and consensus address")
		}
		power = p
		previous = a
	}
	return nil
}
