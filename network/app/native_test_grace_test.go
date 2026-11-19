package app

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestNativeTestGraceRestrictedAndAutomaticallyExpires(t *testing.T) {
	profile := WithdrawalProfile{EnableNativeConsensus: true, EnableNativeOrdinaryDELDowntime: true,
		EnableNativeDuplicateVote: true, TestGraceUntilHeight: 5}
	for _, id := range []uint64{990001, 990099} {
		require.NoError(t, validateTestGraceProfile(id, profile))
	}
	for _, id := range []uint64{0, 75, 202020, 20202020, 990000, 990100, 9905401} {
		require.Error(t, validateTestGraceProfile(id, profile))
	}
	app := &App{withdrawals: &withdrawalController{profile: profile}}
	require.True(t, app.nativeTestGraceActive(sdk.Context{}.WithBlockHeight(5)))
	require.False(t, app.nativeTestGraceActive(sdk.Context{}.WithBlockHeight(6)))
	require.True(t, app.usesNativeDuplicateVote(), "registration and consensus enforcement remain enabled")
	profile.EnableNativeDuplicateVote = false
	require.Error(t, validateTestGraceProfile(990001, profile))
	profile.TestGraceUntilHeight = -1
	require.Error(t, validateTestGraceProfile(990001, profile))
	profile.TestGraceUntilHeight = 0
	require.NoError(t, validateTestGraceProfile(9905401, profile), "production keeps normal penalties")
}

func TestAllEnabledGraceProfileFitsPersistedProfileBound(t *testing.T) {
	profile := WithdrawalProfile{TestGraceUntilHeight: math.MaxInt64}
	fields := reflect.ValueOf(&profile).Elem()
	addressType := reflect.TypeOf(common.Address{})
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		if field.Kind() == reflect.Bool {
			field.SetBool(true)
		} else if field.Type() == addressType {
			field.Set(reflect.ValueOf(common.HexToAddress("0x1111111111111111111111111111111111111111")))
		}
	}
	raw, err := json.Marshal(profile)
	require.NoError(t, err)
	require.LessOrEqual(t, len(raw), maxWithdrawalProfileJSON, "restart parser must accept the complete enabled profile")
	var restored WithdrawalProfile
	require.NoError(t, json.Unmarshal(raw, &restored))
	require.Equal(t, profile, restored)
}
