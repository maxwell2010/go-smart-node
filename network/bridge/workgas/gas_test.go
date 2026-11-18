package workgas

import (
	"math"
	"strings"
	"testing"

	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/crypto/ed25519"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/stretchr/testify/require"
)

type gasRecord struct {
	amount     uint64
	descriptor string
}
type recordingMeter struct {
	gas     uint64
	records []gasRecord
}

func (m *recordingMeter) ConsumeGas(amount uint64, descriptor string) {
	m.gas += amount
	m.records = append(m.records, gasRecord{amount, descriptor})
}

// Metadata only: pricing must never need a correctly signed block, hash a
// validator set, or marshal protobuf. Actual verification has separate tests.
func pricedBlock(count int) *cmt.LightBlock {
	rows := make([]*cmt.Validator, count)
	for i := range rows {
		key := make(ed25519.PubKey, ed25519.PubKeySize)
		key[0], key[1] = byte(i), byte(i>>8)
		rows[i] = &cmt.Validator{PubKey: key, Address: make([]byte, 20), VotingPower: 1}
	}
	var proposer *cmt.Validator
	if len(rows) > 0 {
		proposer = rows[0]
	}
	return &cmt.LightBlock{SignedHeader: &cmt.SignedHeader{Header: &cmt.Header{ChainID: "bridge_fixture-1"},
		Commit: &cmt.Commit{Signatures: make([]cmt.CommitSig, count)}},
		ValidatorSet: &cmt.ValidatorSet{Validators: rows, Proposer: proposer}}
}

func pricedProofs() *cmtcrypto.ProofOps {
	return &cmtcrypto.ProofOps{Ops: []cmtcrypto.ProofOp{
		{Type: "ics23:iavl", Key: make([]byte, 53), Data: []byte{0xff}},
		{Type: "ics23:simple", Key: []byte("evm"), Data: []byte{0xff}},
	}}
}

func TestEveryMalformedOperationStillPaysBase(t *testing.T) {
	cases := map[string]func(Meter) error{
		"negative decode":  func(m Meter) error { return ChargeDecode(m, -1) },
		"large decode":     func(m Meter) error { return ChargeDecode(m, MaxRecordBytes+1) },
		"bootstrap nil":    func(m Meter) error { return ChargeBootstrap(m, nil) },
		"update-input nil": func(m Meter) error { return ChargeUpdateInput(m, nil) },
		"update nil":       func(m Meter) error { return ChargeUpdate(m, nil, pricedBlock(4)) },
		"membership nil":   func(m Meter) error { return ChargeMembership(m, nil, nil) },
		"membership large": func(m Meter) error { return ChargeMembership(m, make([]byte, MaxAccountBytes+1), pricedProofs()) },
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			m := &recordingMeter{}
			require.Error(t, call(m))
			require.Equal(t, BaseGas, m.gas)
			require.Len(t, m.records, 1)
			require.True(t, strings.HasSuffix(m.records[0].descriptor, "/base"))
		})
	}
}

func TestNilAndTypedNilMetersFailClosed(t *testing.T) {
	var typed *recordingMeter
	for _, m := range []Meter{nil, typed} {
		require.Error(t, ChargeDecode(m, 1))
		require.Error(t, ChargeBootstrap(m, pricedBlock(4)))
		require.Error(t, ChargeUpdate(m, pricedBlock(4), pricedBlock(4)))
		require.Error(t, ChargeMembership(m, []byte{1}, pricedProofs()))
		require.Error(t, ChargeDeposit(m))
	}
}

func TestUpdateChargesBothSetsAndThreeCandidatePasses(t *testing.T) {
	m := &recordingMeter{}
	require.NoError(t, ChargeUpdate(m, pricedBlock(2), pricedBlock(7)))
	expected := BaseGas + 2*MaxBlockBytes*ByteGas + 9*ValidatorGas + (2+3*7)*SignaturePassGas
	require.Equal(t, expected, m.gas)
	reverse := &recordingMeter{}
	require.NoError(t, ChargeUpdate(reverse, pricedBlock(7), pricedBlock(2)))
	require.Greater(t, m.gas, reverse.gas, "candidate has three reserved signature passes")
	bootstrap := &recordingMeter{}
	require.NoError(t, ChargeBootstrap(bootstrap, pricedBlock(4)))
	require.Equal(t, BaseGas+MaxBlockBytes*ByteGas+4*ValidatorGas+4*SignaturePassGas, bootstrap.gas)
}

func TestPreflightNeverMarshalsOrChecksSignatures(t *testing.T) {
	m := &recordingMeter{}
	block := pricedBlock(4)
	// These signatures are absent and metadata are not valid Comet headers.
	// Pricing still succeeds because it is only a cheap bounded reservation.
	require.NoError(t, ChargeUpdateInput(m, block))
	require.Equal(t, BaseGas, m.gas)
	require.NoError(t, ChargeBootstrap(m, block))
	proofs := pricedProofs() // Deliberately malformed protobuf bytes.
	require.NoError(t, ChargeMembership(m, []byte{1}, proofs))
	memberCost := BaseGas + 3*ByteGas + 2*MaxProofPathNodes*ProofNodeGas
	member := &recordingMeter{}
	require.NoError(t, ChargeMembership(member, []byte{1}, proofs))
	require.Equal(t, memberCost, member.gas)
}

func TestBlockAndProofBoundsRejectBeforeExpensiveProcessing(t *testing.T) {
	for _, change := range []func(*cmt.LightBlock){
		func(b *cmt.LightBlock) { b.Header.ChainID = strings.Repeat("x", 51) },
		func(b *cmt.LightBlock) { b.Header.AppHash = make([]byte, 33) },
		func(b *cmt.LightBlock) { b.ValidatorSet.Validators[0].PubKey = ed25519.PubKey(make([]byte, 33)) },
		func(b *cmt.LightBlock) { b.Commit.Signatures[0].Signature = make([]byte, 65) },
		func(b *cmt.LightBlock) { b.ValidatorSet.Proposer = nil },
	} {
		block := pricedBlock(4)
		change(block)
		m := &recordingMeter{}
		require.Error(t, ChargeBootstrap(m, block))
		require.Equal(t, BaseGas, m.gas)
	}
	require.Error(t, ChargeBootstrap(&recordingMeter{}, pricedBlock(MaxValidators+1)))
	require.NoError(t, ChargeBootstrap(&recordingMeter{}, pricedBlock(MaxValidators)))
	proofs := pricedProofs()
	proofs.Ops[0].Data = make([]byte, MaxProofOpBytes+1)
	m := &recordingMeter{}
	require.Error(t, ChargeMembership(m, []byte{1}, proofs))
	require.Equal(t, BaseGas, m.gas)
}

func TestOverflowChecksNeverWrapMeterCharges(t *testing.T) {
	m := &recordingMeter{}
	require.Error(t, reserve(m, "overflow", item{math.MaxUint64, 2}))
	require.Error(t, reserve(m, "overflow", item{math.MaxUint64, 1}, item{1, 1}))
	require.Zero(t, m.gas)
	require.Empty(t, m.records)
}

func TestSDKOutOfGasPropagatesAndSpentGasIsNotRefunded(t *testing.T) {
	m := storetypes.NewGasMeter(BaseGas + 10)
	require.Panics(t, func() { _ = ChargeMembership(m, []byte{1}, pricedProofs()) })
	require.Greater(t, uint64(m.GasConsumed()), BaseGas)
}

func TestConservativeFourValidatorReceiveReservationsFitTwentyFourMillion(t *testing.T) {
	m := &recordingMeter{}
	block := pricedBlock(4)
	require.NoError(t, ChargeDeposit(m))
	require.NoError(t, ChargeUpdateInput(m, block))
	// Includes an intentionally generous ten bootstrap passes and ten real-
	// sized stored records, not a production performance/calibration claim.
	for range 10 {
		require.NoError(t, ChargeDecode(m, 8192))
		require.NoError(t, ChargeBootstrap(m, block))
	}
	require.NoError(t, ChargeUpdate(m, block, block))
	for range 2 {
		require.NoError(t, ChargeMembership(m, make([]byte, 153), pricedProofs()))
	}
	require.Less(t, m.gas, uint64(24_000_000))
	// A large validator set must be able to exhaust a budget instead of being
	// silently discounted to the fixture's small set.
	limited := storetypes.NewGasMeter(24_000_000)
	require.Panics(t, func() { _ = ChargeUpdate(limited, pricedBlock(MaxValidators), pricedBlock(MaxValidators)) })
}
