package storage

import (
	"encoding/hex"
	"strings"
	"testing"

	"cosmossdk.io/log"
	multistore "cosmossdk.io/store"
	"cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/crypto/tmhash"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	dbm "github.com/cosmos/cosmos-db"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	gethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
)

func accountBytesField(number protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
}

func accountAny(t *testing.T, typeURL string, body []byte) []byte {
	t.Helper()
	value, err := (&codectypes.Any{TypeUrl: typeURL, Value: body}).Marshal()
	require.NoError(t, err)
	return value
}

func accountWireParts(t *testing.T) (AccountCodeClaim, []byte, []byte, []byte) {
	t.Helper()
	claim := AccountCodeClaim{ChainID: "decimal_75-1", StateHeight: 10,
		ExpectedCodeHash: [32]byte(gethcrypto.Keccak256Hash([]byte("disposable-reviewed-non-proxy-runtime")))}
	claim.Address[19] = 21
	address, err := bech32.ConvertAndEncode("d0", claim.Address[:])
	require.NoError(t, err)
	base, err := (&authtypes.BaseAccount{Address: address, AccountNumber: 7, Sequence: 3}).Marshal()
	require.NoError(t, err)
	body := append(accountBytesField(1, base), accountBytesField(2, []byte("0x"+hex.EncodeToString(claim.ExpectedCodeHash[:])))...)
	return claim, base, body, accountAny(t, legacyEthAccountURL, body)
}

// These are real SDK IAVL and multistore proofs over a disposable database.
// The constructed checkpoint tests Merkle/API conformance only, not network
// trust establishment. The integration controller separately tests finality.
func accountCMSProof(t *testing.T, claim AccountCodeClaim, raw []byte) (finality.VerifiedCheckpoint, *cmtcrypto.ProofOps) {
	t.Helper()
	database := dbm.NewMemDB()
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	cms := multistore.NewCommitMultiStore(database, log.NewNopLogger(), metrics.NewNoOpMetrics())
	accKey, evmKey := storetypes.NewKVStoreKey("acc"), storetypes.NewKVStoreKey("evm")
	cms.MountStoreWithDB(accKey, storetypes.StoreTypeIAVL, nil)
	cms.MountStoreWithDB(evmKey, storetypes.StoreTypeIAVL, nil)
	require.NoError(t, cms.LoadLatestVersion())
	require.NoError(t, cms.SetInitialVersion(claim.StateHeight))
	key := append([]byte{0x01}, claim.Address[:]...)
	cms.GetKVStore(accKey).Set(key, raw)
	cms.GetKVStore(evmKey).Set([]byte{1}, []byte("disposable-other-store"))
	commit := cms.Commit()
	query, err := cms.(storetypes.Queryable).Query(&storetypes.RequestQuery{
		Path: "/acc/key", Data: key, Height: claim.StateHeight, Prove: true})
	require.NoError(t, err)
	require.Equal(t, claim.StateHeight, query.Height)
	require.Equal(t, raw, query.Value)
	require.NotNil(t, query.ProofOps)
	checkpoint := finality.VerifiedCheckpoint{Checkpoint: finality.Checkpoint{
		ChainID: claim.ChainID, Height: claim.StateHeight + 1}}
	copy(checkpoint.Hash[:], tmhash.Sum([]byte("disposable-account-header")))
	copy(checkpoint.AppHash[:], commit.Hash)
	return checkpoint, query.ProofOps
}

func TestAccountCodeMembershipWithRealCMSProofs(t *testing.T) {
	claim, _, _, raw := accountWireParts(t)
	checkpoint, ops := accountCMSProof(t, claim, raw)
	require.NoError(t, VerifyAccountCodeMembership(checkpoint, claim, raw, ops))
	// SDK gogo omits zero account number and sequence; they are optional fields.
	address, err := bech32.ConvertAndEncode("d0", claim.Address[:])
	require.NoError(t, err)
	base, err := (&authtypes.BaseAccount{Address: address}).Marshal()
	require.NoError(t, err)
	body := append(accountBytesField(1, base), accountBytesField(2, []byte("0x"+hex.EncodeToString(claim.ExpectedCodeHash[:])))...)
	raw = accountAny(t, legacyEthAccountURL, body)
	checkpoint, ops = accountCMSProof(t, claim, raw)
	require.NoError(t, VerifyAccountCodeMembership(checkpoint, claim, raw, ops))
}

func TestAuthenticatedMalformedLegacyAccountStillFailsClosed(t *testing.T) {
	claim, base, body, raw := accountWireParts(t)
	code := []byte("0x" + hex.EncodeToString(claim.ExpectedCodeHash[:]))
	makeBody := func(baseValue, codeValue []byte) []byte {
		return append(accountBytesField(1, baseValue), accountBytesField(2, codeValue)...)
	}
	otherAddress := claim.Address
	otherAddress[0] = 1
	otherBech32, err := bech32.ConvertAndEncode("d0", otherAddress[:])
	require.NoError(t, err)
	wrongBase, err := (&authtypes.BaseAccount{Address: otherBech32}).Marshal()
	require.NoError(t, err)
	wrongPrefix, err := bech32.ConvertAndEncode("cosmos", claim.Address[:])
	require.NoError(t, err)
	wrongPrefixBase, err := (&authtypes.BaseAccount{Address: wrongPrefix}).Marshal()
	require.NoError(t, err)
	address, err := bech32.ConvertAndEncode("d0", claim.Address[:])
	require.NoError(t, err)
	bad := map[string][]byte{
		"unsupported_type":       accountAny(t, "/cosmos.auth.v1beta1.BaseAccount", body),
		"missing_body":           accountBytesField(1, []byte(legacyEthAccountURL)),
		"duplicate_any_value":    append(append([]byte(nil), raw...), accountBytesField(2, body)...),
		"unknown_any_field":      append(append([]byte(nil), raw...), accountBytesField(3, []byte("unknown"))...),
		"duplicate_eth_code":     accountAny(t, legacyEthAccountURL, append(append([]byte(nil), body...), accountBytesField(2, code)...)),
		"unknown_eth_field":      accountAny(t, legacyEthAccountURL, append(append([]byte(nil), body...), accountBytesField(3, []byte("unknown"))...)),
		"missing_base":           accountAny(t, legacyEthAccountURL, accountBytesField(2, code)),
		"missing_code":           accountAny(t, legacyEthAccountURL, accountBytesField(1, base)),
		"wrong_address":          accountAny(t, legacyEthAccountURL, makeBody(wrongBase, code)),
		"wrong_prefix":           accountAny(t, legacyEthAccountURL, makeBody(wrongPrefixBase, code)),
		"duplicate_base_address": accountAny(t, legacyEthAccountURL, makeBody(append(append([]byte(nil), base...), accountBytesField(1, []byte(address))...), code)),
		"unknown_base_field":     accountAny(t, legacyEthAccountURL, makeBody(append(append([]byte(nil), base...), accountBytesField(5, []byte("unknown"))...), code)),
		"short_code":             accountAny(t, legacyEthAccountURL, makeBody(base, []byte("0x01"))),
		"invalid_hex_code":       accountAny(t, legacyEthAccountURL, makeBody(base, []byte("0x"+strings.Repeat("g", 64)))),
		"code_without_prefix":    accountAny(t, legacyEthAccountURL, makeBody(base, code[2:])),
		"zero_code":              accountAny(t, legacyEthAccountURL, makeBody(base, []byte("0x"+strings.Repeat("0", 64)))),
		"empty_code":             accountAny(t, legacyEthAccountURL, makeBody(base, nil)),
		"truncated_any":          {0x0a, 0xff},
		"wrong_wire_type":        {0x08, 0x01},
		"nonminimal_tag":         append([]byte{0x8a, 0x00}, raw[1:]...),
		"oversized_value":        make([]byte, MaxAccountAnyBytes+1),
	}
	for name, invalid := range bad {
		t.Run(name, func(t *testing.T) {
			checkpoint, ops := accountCMSProof(t, claim, invalid)
			require.NotPanics(t, func() { require.Error(t, VerifyAccountCodeMembership(checkpoint, claim, invalid, ops)) })
		})
	}
}

func TestAccountCodeProofCannotBeReboundToAnotherRouteOrHeight(t *testing.T) {
	for _, name := range []string{"code_hash", "zero_expected", "empty_runtime", "address", "height", "chain", "root", "nil_proof", "wrong_op_type", "wrong_store", "wrong_key", "truncated_proof", "oversized_proof", "changed_raw_value"} {
		t.Run(name, func(t *testing.T) {
			claim, _, _, raw := accountWireParts(t)
			checkpoint, ops := accountCMSProof(t, claim, raw)
			switch name {
			case "code_hash":
				claim.ExpectedCodeHash[0] ^= 1
			case "zero_expected":
				claim.ExpectedCodeHash = [32]byte{}
			case "empty_runtime":
				claim.ExpectedCodeHash = [32]byte(gethcrypto.Keccak256Hash(nil))
			case "address":
				claim.Address[0] ^= 1
			case "height":
				claim.StateHeight--
			case "chain":
				claim.ChainID = "other-1"
			case "root":
				checkpoint.AppHash[0] ^= 1
			case "nil_proof":
				ops = nil
			case "wrong_op_type":
				ops.Ops[0].Type = "ics23:simple"
			case "wrong_store":
				ops.Ops[1].Key = []byte("evm")
			case "wrong_key":
				ops.Ops[0].Key[1] ^= 1
			case "truncated_proof":
				ops.Ops[0].Data = []byte{0x0a, 0xff}
			case "oversized_proof":
				ops.Ops[0].Data = make([]byte, MaxProofOpBytes+1)
			case "changed_raw_value":
				raw[len(raw)-1] ^= 1
			}
			require.NotPanics(t, func() { require.Error(t, VerifyAccountCodeMembership(checkpoint, claim, raw, ops)) })
		})
	}
}
