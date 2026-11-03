package types

import (
	"bytes"
	"errors"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const MaxValidatorHeaderBytes = 1024

func (h *EVMValidatorHeader) Validate() error {
	if h == nil || !address20(h.Master) || !address20(h.Validator) || h.Status > 2 || !fullWord(h.PenaltyCount) || !fullWord(h.LastMetaHash) || bytes.Equal(h.LastMetaHash, make([]byte, 32)) || h.CreatedHeight < 0 || h.UpdatedHeight < h.CreatedHeight || h.LastMetaHeight < h.CreatedHeight || h.LastMetaHeight > h.UpdatedHeight {
		return errors.New("invalid authenticated validator lifecycle header")
	}
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(h.Validator)
	if err != nil {
		return err
	}
	if h.NativeOperator != operator {
		return errors.New("source validator and native operator identity differ")
	}
	return nil
}

// SourceEligible preserves the pinned contract's membership/pause predicate.
// It establishes neither native power, registration authenticity nor unjailing.
func (h *EVMValidatorHeader) SourceEligible() bool {
	return h != nil && (h.Status == 1 || h.Status == 2) && !h.Paused
}
func ValidatorHeaderKey(master, validator []byte) ([]byte, error) {
	if !address20(master) || !address20(validator) {
		return nil, errors.New("validator header requires full nonzero source addresses")
	}
	return append(append([]byte(nil), master...), validator...), nil
}
func CanonicalValidatorHeaderBytes(h *EVMValidatorHeader) ([]byte, error) {
	if err := h.Validate(); err != nil {
		return nil, err
	}
	data, err := proto.Marshal(h)
	if err != nil {
		return nil, err
	}
	if len(data) > MaxValidatorHeaderBytes {
		return nil, errors.New("validator lifecycle header exceeds bound")
	}
	return data, nil
}
func (g GenesisState) ValidateEVMValidatorHeaders() error {
	if len(g.EVMValidatorHeaders) == 0 {
		return nil
	} // Staging metadata fixtures, never a production activation permission.
	if g.BindingDomain == nil {
		return errors.New("validator lifecycle headers require the configured source domain")
	}
	native := map[string]bool{}
	for _, v := range g.Validators {
		native[v.OperatorAddress] = true
	}
	seen := map[string]bool{}
	var master []byte
	for i := range g.EVMValidatorHeaders {
		h := &g.EVMValidatorHeaders[i]
		if err := h.Validate(); err != nil {
			return err
		}
		if !native[h.NativeOperator] || seen[h.NativeOperator] {
			return errors.New("validator lifecycle header references missing or duplicate native metadata")
		}
		if master == nil {
			master = h.Master
		} else if !bytes.Equal(master, h.Master) {
			return errors.New("validator lifecycle headers disagree on authoritative MasterValidator")
		}
		seen[h.NativeOperator] = true
	}
	if len(seen) != len(native) {
		return errors.New("incomplete validator lifecycle header coverage")
	}
	return nil
}
