package types

import (
	"bytes"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func TestValidatorHeaderRawStatusDomainAndFullCount(t *testing.T) {
	validator := bytes.Repeat([]byte{1}, 20)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator)
	require.NoError(t, err)
	good := EVMValidatorHeader{Master: bytes.Repeat([]byte{9}, 20), Validator: validator, NativeOperator: operator, Status: 1, PenaltyCount: bytes.Repeat([]byte{0xff}, 32), LastMetaHash: bytes.Repeat([]byte{3}, 32), CreatedHeight: 1, UpdatedHeight: 2, LastMetaHeight: 1}
	require.NoError(t, good.Validate())
	require.True(t, good.SourceEligible())
	approved := good
	approved.Status = 2
	require.True(t, approved.SourceEligible())
	approved.Paused = true
	require.False(t, approved.SourceEligible())
	approved.Status = 0
	approved.Paused = false
	require.False(t, approved.SourceEligible())
	for name, mutate := range map[string]func(*EVMValidatorHeader){
		"truncated-master": func(h *EVMValidatorHeader) { h.Master = h.Master[1:] },
		"zero-master":      func(h *EVMValidatorHeader) { h.Master = make([]byte, 20) },
		"truncated-count":  func(h *EVMValidatorHeader) { h.PenaltyCount = h.PenaltyCount[1:] },
		"unknown-status":   func(h *EVMValidatorHeader) { h.Status = 3 },
		"missing-meta":     func(h *EVMValidatorHeader) { h.LastMetaHash = make([]byte, 32) },
		"foreign-operator": func(h *EVMValidatorHeader) { h.Validator = bytes.Repeat([]byte{2}, 20) },
		"future-meta":      func(h *EVMValidatorHeader) { h.LastMetaHeight = 3 },
	} {
		t.Run(name, func(t *testing.T) { candidate := good; mutate(&candidate); require.Error(t, candidate.Validate()) })
	}
}
