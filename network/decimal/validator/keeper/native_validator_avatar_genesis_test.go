package keeper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cosmossdk.io/core/store"
	"github.com/cosmos/gogoproto/proto"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/stretchr/testify/require"
)

const avatarGenesisIPFS = "ipfs://bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku/avatar.png"
const avatarGenesisHTTPS = "https://cdn.example.org/validator-two.png"

func avatarGenesisFixture(t *testing.T) *types.GenesisState {
	t.Helper()
	g := genesis(t)
	// Input ordering is deliberately different from the raw operator-byte
	// ordering used by the canonical exporter.
	g.ValidatorAvatars = []types.ValidatorAvatar{
		{OperatorAddress: g.Validators[1].OperatorAddress, AvatarURI: avatarGenesisHTTPS},
		{OperatorAddress: g.Validators[0].OperatorAddress, AvatarURI: avatarGenesisIPFS},
	}
	require.NoError(t, g.Validate())
	return g
}

func TestNativeValidatorAvatarGenesisCodecExportImport(t *testing.T) {
	ctx, _, k := environment(t)
	g := avatarGenesisFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	for _, record := range g.ValidatorAvatars {
		got, err := k.GetNativeValidatorAvatar(ctx, record.OperatorAddress)
		require.NoError(t, err)
		require.Equal(t, record.AvatarURI, got)
	}
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, exported.ValidatorAvatars, 2)
	require.Equal(t, g.Validators[0].OperatorAddress, exported.ValidatorAvatars[0].OperatorAddress)
	require.Equal(t, g.Validators[1].OperatorAddress, exported.ValidatorAvatars[1].OperatorAddress)

	// This is the populated protobuf JSON codec used by the module's genesis
	// entry points. JSON preserves both avatars and the existing crypto Any.
	cdc := types.NewCodec()
	moduleJSON, err := cdc.MarshalJSON(exported)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(moduleJSON, &envelope))
	require.Contains(t, envelope, "validator_avatars")
	var fromJSON types.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(moduleJSON, &fromJSON))
	require.NoError(t, fromJSON.Validate())
	require.Equal(t, exported.ValidatorAvatars, fromJSON.ValidatorAvatars)
	raw, err := proto.Marshal(&fromJSON)
	require.NoError(t, err)
	var fromProto types.GenesisState
	require.NoError(t, proto.Unmarshal(raw, &fromProto))
	require.NoError(t, fromProto.Validate())
	require.Equal(t, exported.ValidatorAvatars, fromProto.ValidatorAvatars)

	ctx2, _, k2 := environment(t)
	require.NoError(t, k2.InitGenesis(ctx2, &fromProto))
	again, err := k2.ExportGenesis(ctx2)
	require.NoError(t, err)
	first, err := proto.Marshal(exported)
	require.NoError(t, err)
	second, err := proto.Marshal(again)
	require.NoError(t, err)
	require.Equal(t, first, second, "export/import must preserve financial rows and canonical avatar ordering")
	jsonAgain, err := cdc.MarshalJSON(again)
	require.NoError(t, err)
	require.Equal(t, moduleJSON, jsonAgain)
}

func TestNativeValidatorAvatarGenesisLegacyAbsentAndClear(t *testing.T) {
	cdc := types.NewCodec()
	g := genesis(t)
	legacyJSON, err := cdc.MarshalJSON(g)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(legacyJSON, &envelope))
	delete(envelope, "validator_avatars")
	delete(envelope, "validatorAvatars")
	legacyJSON, err = json.Marshal(envelope)
	require.NoError(t, err)
	var legacy types.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(legacyJSON, &legacy))
	require.Empty(t, legacy.ValidatorAvatars)
	ctx, service, k := environment(t)
	require.NoError(t, k.InitGenesis(ctx, &legacy))
	operator := g.Validators[0].OperatorAddress
	got, err := k.GetNativeValidatorAvatar(ctx, operator)
	require.NoError(t, err)
	require.Empty(t, got)
	require.NoError(t, k.SetNativeValidatorAvatar(ctx, operator, avatarGenesisIPFS))
	// A dropped outer transaction must not clear the committed pointer.
	dropped, _ := ctx.CacheContext()
	require.NoError(t, k.SetNativeValidatorAvatar(dropped, operator, ""))
	got, err = k.GetNativeValidatorAvatar(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, avatarGenesisIPFS, got)
	broken, err := NewStateKeeper(failingService{service: service, prefix: nativeValidatorAvatarPrefix, deleteOnly: true})
	require.NoError(t, err)
	require.ErrorContains(t, broken.SetNativeValidatorAvatar(ctx, operator, ""), "injected delete failure")
	got, err = k.GetNativeValidatorAvatar(ctx, operator)
	require.NoError(t, err)
	require.Equal(t, avatarGenesisIPFS, got)
	require.NoError(t, k.SetNativeValidatorAvatar(ctx, operator, ""))
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, exported.ValidatorAvatars, "cleared avatars have no blank genesis record")
	ctx2, _, k2 := environment(t)
	require.NoError(t, k2.InitGenesis(ctx2, exported))
	got, err = k2.GetNativeValidatorAvatar(ctx2, operator)
	require.NoError(t, err)
	require.Empty(t, got)
}

type avatarGenesisFailService struct {
	service store.KVStoreService
	key     []byte
}

func (s avatarGenesisFailService) OpenKVStore(ctx context.Context) store.KVStore {
	return avatarGenesisFailStore{KVStore: s.service.OpenKVStore(ctx), key: s.key}
}

type avatarGenesisFailStore struct {
	store.KVStore
	key []byte
}

func (s avatarGenesisFailStore) Set(key, value []byte) error {
	if bytes.Equal(key, s.key) {
		return errors.New("injected second avatar write failure")
	}
	return s.KVStore.Set(key, value)
}

func TestNativeValidatorAvatarGenesisInvalidOrFailedImportIsAtomic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{"duplicate", func(g *types.GenesisState) { g.ValidatorAvatars = append(g.ValidatorAvatars, g.ValidatorAvatars[0]) }},
		{"unknown operator", func(g *types.GenesisState) {
			g.ValidatorAvatars[0].OperatorAddress = validator(t, 9, 0).OperatorAddress
		}},
		{"malformed URI", func(g *types.GenesisState) { g.ValidatorAvatars[0].AvatarURI = "javascript:alert(1)" }},
		{"invalid UTF8", func(g *types.GenesisState) {
			g.ValidatorAvatars[0].AvatarURI = "https://example.org/" + string([]byte{0xff})
		}},
		{"empty record", func(g *types.GenesisState) { g.ValidatorAvatars[0].AvatarURI = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, service, k := environment(t)
			g := avatarGenesisFixture(t)
			tc.mutate(g)
			require.Error(t, k.InitGenesis(ctx, g))
			it, err := service.OpenKVStore(ctx).Iterator(nil, nil)
			require.NoError(t, err)
			require.False(t, it.Valid(), "failed import must not persist params, validators or avatars")
			require.NoError(t, it.Close())
			require.NoError(t, k.InitGenesis(ctx, genesis(t)), "an invalid import must leave a fresh store")
		})
	}
	ctx, service, k := environment(t)
	g := avatarGenesisFixture(t)
	// InitGenesis iterates the submitted list; fail on the second record after
	// validators and one avatar have already been staged in its child cache.
	raw, err := k.ValidatorAddressCodec().StringToBytes(g.ValidatorAvatars[1].OperatorAddress)
	require.NoError(t, err)
	broken, err := NewStateKeeper(avatarGenesisFailService{service: service, key: append([]byte{nativeValidatorAvatarPrefix}, raw...)})
	require.NoError(t, err)
	require.ErrorContains(t, broken.InitGenesis(ctx, g), "injected second avatar write failure")
	it, err := service.OpenKVStore(ctx).Iterator(nil, nil)
	require.NoError(t, err)
	require.False(t, it.Valid())
	require.NoError(t, it.Close())
	require.NoError(t, k.InitGenesis(ctx, g))
}

func TestNativeValidatorAvatarGenesisRejectsCorruptStore(t *testing.T) {
	for _, tc := range []struct {
		name       string
		key, value []byte
	}{
		{"missing address", []byte{nativeValidatorAvatarPrefix}, []byte(avatarGenesisHTTPS)},
		{"short address", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{1}, 19)...), []byte(avatarGenesisHTTPS)},
		{"long address", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{1}, 21)...), []byte(avatarGenesisHTTPS)},
		{"orphan", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{9}, 20)...), []byte(avatarGenesisHTTPS)},
		{"empty record", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{1}, 20)...), []byte{}},
		{"invalid UTF8", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{1}, 20)...), []byte{0xff}},
		{"bad scheme", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{1}, 20)...), []byte("data:image/png;base64,AAAA")},
		{"oversized", append([]byte{nativeValidatorAvatarPrefix}, bytes.Repeat([]byte{1}, 20)...), []byte("https://example.org/" + strings.Repeat("x", 513))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, service, k := environment(t)
			require.NoError(t, k.InitGenesis(ctx, genesis(t)))
			require.NoError(t, service.OpenKVStore(ctx).Set(tc.key, tc.value))
			knownKey, err := operatorKey(nativeValidatorAvatarPrefix, genesis(t).Validators[0].OperatorAddress)
			require.NoError(t, err)
			if bytes.Equal(tc.key, knownKey) {
				_, err := k.GetNativeValidatorAvatar(ctx, genesis(t).Validators[0].OperatorAddress)
				require.Error(t, err, "direct getter must not publish corrupt stored text")
			}
			_, err = k.ExportGenesis(ctx)
			require.Error(t, err, "export must not omit, repair or publish a corrupt avatar record")
		})
	}
}

func TestNativeValidatorAvatarSetterRejectsURIWithoutChanges(t *testing.T) {
	ctx, _, k := environment(t)
	g := avatarGenesisFixture(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	beforeBytes, err := proto.Marshal(before)
	require.NoError(t, err)
	for _, value := range []string{"http://example.org/avatar.png", "https://example.org/\x00a", string([]byte{0xff}), "https://example.org/" + strings.Repeat("x", 513)} {
		require.Error(t, k.SetNativeValidatorAvatar(ctx, g.Validators[0].OperatorAddress, value))
	}
	require.Error(t, k.SetNativeValidatorAvatar(ctx, validator(t, 9, 0).OperatorAddress, avatarGenesisHTTPS))
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	afterBytes, err := proto.Marshal(after)
	require.NoError(t, err)
	require.Equal(t, beforeBytes, afterBytes)
}
