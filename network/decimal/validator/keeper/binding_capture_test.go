package keeper

import (
	"bytes"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"
)

func TestBindingCaptureScopesExactRowsAndSurvivesNoopReplay(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, true)
	ctx = ctx.WithTxBytes([]byte("ordinary signed transaction fixture"))
	b := bindingNativeFixture()
	references := []BindingReference{{Kind: b.Kind, Gateway: b.Gateway, ID: b.StakeID}}
	capture, err := k.CaptureBindingState(ctx, references)
	require.NoError(t, err)
	require.NoError(t, k.ValidateBindingCapture(ctx, capture))
	references[0].ID[0] = 99
	require.NoError(t, k.ValidateBindingCapture(ctx, capture), "capture owns independent reference bytes")
	require.Error(t, k.ValidateBindingCapture(ctx.WithBlockHeight(11), capture))
	require.Error(t, k.ValidateBindingCapture(ctx.WithTxBytes([]byte("different transaction")), capture))
	require.Error(t, k.ValidateBindingCapture(ctx.WithChainID("different"), capture))
	changed := append([]byte(nil), capture...)
	changed[len(changed)-1] ^= 1
	require.Error(t, k.ValidateBindingCapture(ctx, changed))
	changed = append(append([]byte(nil), capture...), 0)
	require.Error(t, k.ValidateBindingCapture(ctx, changed))
	b = bindingNativeFixture()
	p, err := k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &b}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, p))
	require.NoError(t, k.ValidateBindingCapture(ctx, capture), "exact no-op does not invalidate prestate")
	b.ReportedReserveAmount = sdkmath.NewInt(19)
	p, err = k.PrepareBindingPlan(ctx, []BindingMutation{{Stake: &b}})
	require.NoError(t, err)
	require.NoError(t, k.ApplyBindingPlan(ctx, p))
	require.ErrorIs(t, k.ValidateBindingCapture(ctx, capture), ErrStaleBindingPlan)
	require.False(t, bytes.Equal(capture, make([]byte, len(capture))))
}
func TestBindingCaptureRejectsOverboundAndTruncatedReferences(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	token, err := k.CaptureBindingState(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, k.ValidateBindingCapture(ctx, token))
	for _, bad := range [][]byte{nil, {1}, make([]byte, 5000), token[:len(token)-1]} {
		require.Error(t, k.ValidateBindingCapture(ctx, bad))
	}
	_, err = k.CaptureBindingState(ctx, make([]BindingReference, 65))
	require.Error(t, err)
}
