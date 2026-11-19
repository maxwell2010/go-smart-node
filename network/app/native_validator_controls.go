package app

import (
	"bytes"
	sdkmath "cosmossdk.io/math"
	"encoding/json"
	"errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"io"
	"strings"
	"unicode/utf8"
)

type ValidatorControlMetadata struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	AvatarURI   *string `json:"avatar_uri,omitempty"`
	Commission  *string `json:"commission,omitempty"`
}

// CanonicalValidatorControlMetadata normalizes offline input without accessing state.
func CanonicalValidatorControlMetadata(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > 2048 || !utf8.Valid(raw) {
		return nil, errors.New("invalid bounded validator metadata")
	}
	var meta ValidatorControlMetadata
	// Reject duplicate keys before normalization, including escaped aliases.
	keys := json.NewDecoder(bytes.NewReader(raw))
	first, err := keys.Token()
	if err != nil || first != json.Delim('{') {
		return nil, errors.New("validator metadata must be a JSON object")
	}
	seen := make(map[string]bool)
	for keys.More() {
		key, err := keys.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return nil, errors.New("duplicate validator metadata field")
		}
		seen[name] = true
		var value json.RawMessage
		if err := keys.Decode(&value); err != nil {
			return nil, err
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("null validator metadata field")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&meta); err != nil {
		return nil, err
	}
	var tail interface{}
	if err := decoder.Decode(&tail); err != io.EOF {
		return nil, errors.New("trailing validator metadata")
	}
	if meta.Name == nil && meta.Description == nil && meta.AvatarURI == nil && meta.Commission == nil {
		return nil, errors.New("empty validator metadata change")
	}
	valid := func(s string, max int) bool {
		if len(s) > max || !utf8.ValidString(s) {
			return false
		}
		for _, r := range s {
			if r < 32 || r == 127 {
				return false
			}
		}
		return true
	}
	if meta.Name != nil {
		v := strings.TrimSpace(*meta.Name)
		if v == "" || !valid(v, 70) {
			return nil, errors.New("invalid validator name")
		}
		meta.Name = &v
	}
	if meta.Description != nil && !valid(*meta.Description, 280) {
		return nil, errors.New("invalid validator description")
	}
	if meta.AvatarURI != nil {
		if err := validatortypes.ValidateValidatorAvatarURI(*meta.AvatarURI); err != nil {
			return nil, err
		}
	}
	if meta.Commission != nil {
		v, err := sdkmath.LegacyNewDecFromStr(*meta.Commission)
		if err != nil || v.IsNegative() || v.GT(sdkmath.LegacyOneDec()) {
			return nil, errors.New("commission must be between zero and one")
		}
		normalized := v.String()
		meta.Commission = &normalized
	}
	return json.Marshal(meta)
}

func (c *withdrawalController) admitNativeValidatorControl(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) (bool, error) {
	if !c.profile.EnableNativeConsensus || msg.To == nil || *msg.To != c.profile.MasterValidator || len(msg.Data) < 4 {
		return false, nil
	}
	master, err := contracts.CompiledABI("NewNetworkMasterValidator")
	if err != nil {
		return true, err
	}
	method, err := master.MethodById(msg.Data[:4])
	if err != nil {
		return false, nil
	}
	switch method.Name {
	case "pauseSelf", "unpauseSelf", "pauseValidator", "unpauseValidator", "updateValidatorMeta":
	default:
		return false, nil
	}
	if sender != msg.From || sender == (common.Address{}) {
		return true, errors.New("validator control requires authenticated operator sender")
	}
	operator, err := c.keeper.ValidatorAddressCodec().BytesToString(sender[:])
	if err != nil {
		return true, err
	}
	validator, err := c.keeper.GetValidator(ctx, operator)
	if err != nil {
		return true, err
	}
	// Keep the separate, proven downtime unjail transition. Ordinary enable is
	// never a shortcut around jail, exclusion or delayed custody reconciliation.
	if validator.Jailed && method.Name == "unpauseSelf" {
		return false, nil
	}
	if validator.Jailed {
		return true, errors.New("jailed validator cannot use ordinary self controls")
	}
	retired, err := c.keeper.IsNativeValidatorRetired(ctx, operator)
	if err != nil {
		return true, err
	}
	if retired {
		return true, errors.New("retired validator cannot use self controls")
	}
	consensus, err := validator.GetConsAddr()
	if err != nil {
		return true, err
	}
	if _, err := c.keeper.GetNativeDuplicateVoteExclusion(ctx, consensus); !errors.Is(err, validatorkeeper.ErrNotFound) {
		if err != nil {
			return true, err
		}
		return true, errors.New("consensus key is permanently excluded")
	}
	if err := c.keeper.RequireNoPendingNativePenalty(ctx, sender[:]); err != nil {
		return true, err
	}
	child, write := ctx.CacheContext()
	proof, err := c.executor.VerifySignedValidatorControl(child, msg, receipt)
	if err != nil {
		return true, err
	}
	if proof.Action == "metadata" {
		canonical, err := CanonicalValidatorControlMetadata([]byte(proof.Metadata))
		if err != nil {
			return true, err
		}
		if !bytes.Equal(canonical, []byte(proof.Metadata)) {
			return true, errors.New("validator metadata must be canonical")
		}
		var meta ValidatorControlMetadata
		if err := json.Unmarshal(canonical, &meta); err != nil {
			return true, err
		}
		if meta.Name != nil {
			validator.Description.Moniker = *meta.Name
		}
		if meta.Description != nil {
			validator.Description.Details = *meta.Description
		}
		if meta.Commission != nil {
			validator.Commission, err = sdkmath.LegacyNewDecFromStr(*meta.Commission)
			if err != nil {
				return true, err
			}
		}
		if err := c.keeper.SetValidator(child, validator); err != nil {
			return true, err
		}
		if meta.AvatarURI != nil {
			if err := c.keeper.SetNativeValidatorAvatar(child, operator, *meta.AvatarURI); err != nil {
				return true, err
			}
		}
	} else {
		validator.Online = proof.Action == "enable"
		if err := c.keeper.SetValidator(child, validator); err != nil {
			return true, err
		}
	}
	child.EventManager().EmitEvent(sdk.NewEvent("mintcandy_validator_self_control", sdk.NewAttribute("operator", operator), sdk.NewAttribute("action", proof.Action)))
	write()
	return true, nil
}
