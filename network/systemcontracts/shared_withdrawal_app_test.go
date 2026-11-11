package systemcontracts_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// These fixtures seed records already admitted by the future financial
// bootstrap. Signed messages and EndBlock then use the real App keepers.
func replaceWithdrawalFixtureStore(t *testing.T, e isolatedBackendEnv, module string, init func(sdk.Context) error) {
	t.Helper()
	ctx, commit := e.ctx().CacheContext()
	store := ctx.KVStore(e.app.GetKey(module))
	maxRows := 256
	if module == nfttypes.StoreKey {
		// The automatic max-size fixture leaves its archived origin index
		// in the store while the signed fixture appends a separate claim.
		maxRows += int(nfttypes.MaxIsolatedOriginUnits)
	}
	var keys [][]byte
	it := store.Iterator(nil, nil)
	for ; it.Valid() && len(keys) < maxRows; it.Next() {
		keys = append(keys, bytes.Clone(it.Key()))
	}
	tooMany := it.Valid()
	require.NoError(t, it.Close())
	require.False(t, tooMany, "fixture store exceeds its %d-row limit", maxRows)
	var profile []byte
	if module == validatortypes.StoreKey {
		profile = bytes.Clone(store.Get([]byte{ownedapp.WithdrawalProfileKey}))
	}
	for _, key := range keys {
		store.Delete(key)
	}
	require.NoError(t, init(ctx))
	if len(profile) != 0 {
		store.Set([]byte{ownedapp.WithdrawalProfileKey}, profile)
	}
	commit()
}

func runSharedWithdrawalDEL(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master contracts.Artifact,
	gov func(contracts.Artifact, common.Address, string, ...interface{})) common.Address {
	t.Helper()
	target, wrapped := profile.Gateway, profile.WrappedDEL
	user := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	validator := common.BytesToAddress(crypto.Keccak256([]byte("shared-withdrawal-validator"))[12:])
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	beneficiary, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(user.Bytes())
	require.NoError(t, err)
	key := bytes.Repeat([]byte{0x75}, 32)
	meta, err := json.Marshal(map[string]interface{}{
		"operator_address": operator, "reward_address": user.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(key),
		"description": map[string]string{"moniker": "shared-withdrawal"}, "commission": "0",
	})
	require.NoError(t, err)
	gov(master, profile.MasterValidator, "addValidators", []struct {
		Validator common.Address
		Meta      string
	}{{validator, string(meta)}})
	gov(gateway, target, "setFreezeTime", big.NewInt(20), new(big.Int))
	v, err := validatortypes.NewValidator(operator, beneficiary, &ed25519.PubKey{Key: key}, validatortypes.Description{Moniker: "shared-withdrawal"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	exportDELDelegationDaemonFixture(t, e, profile, v)

	pack := func(method string, args ...interface{}) []byte {
		t.Helper()
		data, err := gateway.ABI.Pack(method, args...)
		require.NoError(t, err)
		return data
	}
	amount := sdkmath.NewIntWithDecimal(3, 18)
	e.send(e.keys[0], &target, amount.BigInt(), pack("delegateDEL", validator), true)
	requestCtx := e.ctx()
	response := e.send(e.keys[0], &target, new(big.Int), pack("withdraw", validator, wrapped, amount.BigInt()), true)
	var id *big.Int
	var sourceID [32]byte
	var deadline time.Time
	event := gateway.ABI.Events["WithdrawRequest"]
	for _, log := range response.Logs {
		if common.HexToAddress(log.Address) != target || len(log.Topics) != 1 || common.HexToHash(log.Topics[0]) != event.ID {
			continue
		}
		require.Nil(t, id)
		values, err := event.Inputs.NonIndexed().Unpack(log.Data)
		require.NoError(t, err)
		sourceID, id = values[0].([32]byte), values[1].(*big.Int)
		frozen := abi.ConvertType(values[2], new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		require.Equal(t, uint8(1), frozen.FreezeType)
		require.Equal(t, user, frozen.Stake.Delegator)
		require.Equal(t, amount.BigInt(), frozen.Stake.Amount)
		deadline = time.Unix(frozen.UnfreezeTimestamp.Int64(), 0).UTC()
	}
	require.NotNil(t, id)
	entry := validatortypes.UndelegationEntry{CreationHeight: requestCtx.BlockHeight(), CompletionTime: deadline,
		Stake: validatortypes.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))}
	hash, err := validatortypes.NativeQueueEntryHash(1, beneficiary, operator, "", &entry)
	require.NoError(t, err)
	binding := validatortypes.EVMFrozenBinding{
		Gateway: target.Bytes(), FrozenIndex: common.BigToHash(id).Bytes(), SourceKind: validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN,
		SourceStakeID: sourceID[:], SourceValidator: validator.Bytes(), Beneficiary: user.Bytes(), FreezeType: 1,
		CompletionTimestamp: common.BigToHash(big.NewInt(deadline.Unix())).Bytes(), RawAmount: amount, ReportedReserveAmount: sdkmath.ZeroInt(),
		ReserveAsset:    &validatortypes.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom},
		NativeTarget:    &validatortypes.NativeStakeTarget{StakeType: validatortypes.StakeType_Coin, ID: identity.BaseDenom},
		NativeEntryHash: hash, Active: true, CreatedHeight: requestCtx.BlockHeight(), UpdatedHeight: requestCtx.BlockHeight(),
	}
	keeper := e.app.NativeValidatorKeeper
	genesis, err := keeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Empty(t, genesis.Validators)
	genesis.Validators = []validatortypes.Validator{v}
	genesis.Undelegations = []validatortypes.Undelegation{{Delegator: beneficiary, Validator: operator, Entries: []validatortypes.UndelegationEntry{entry}}}
	genesis.EVMFrozenBindings = []validatortypes.EVMFrozenBinding{binding}
	replaceWithdrawalFixtureStore(t, e, validatortypes.StoreKey, func(ctx sdk.Context) error { return keeper.InitGenesis(ctx, genesis) })

	state := func() storetypes.KVStore { return e.ctx().KVStore(e.app.GetKey(validatortypes.StoreKey)) }
	config, err := json.Marshal(profile)
	require.NoError(t, err)
	state().Set([]byte{ownedapp.WithdrawalProfileKey}, config)
	require.NoError(t, e.app.LoadWithdrawalSettlement(e.ctx()))
	require.NoError(t, e.app.LoadWithdrawalSettlement(e.ctx()), "identical loaded consensus profile is idempotent")
	completion := pack("complete", []*big.Int{id})
	e.send(e.keys[0], &target, new(big.Int), completion, false)
	for e.ctx().BlockTime().Before(deadline) {
		e.advance()
	}

	balance := func(account common.Address) sdkmath.Int {
		return e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(account.Bytes()), identity.BaseDenom).Amount
	}
	backing := balance(wrapped)
	pending := func(want bool) {
		t.Helper()
		b, err := keeper.GetEVMFrozenBinding(e.ctx(), target.Bytes(), binding.FrozenIndex)
		require.NoError(t, err)
		require.Equal(t, want, b.Active)
	}
	pending(true)

	// The same otherwise-valid EVM action cannot commit through an internal
	// route outside EndBlock. The guard discards the actual EVM writes.
	child, _ := e.ctx().CacheContext()
	child = child.WithGasMeter(storetypes.NewGasMeter(2_000_000))
	message := core.Message{From: settlement.Sender(), To: &target, Value: new(big.Int), GasLimit: 500_000,
		GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: completion, SkipNonceChecks: true, SkipFromEOACheck: true}
	result, err := e.app.EVMKeeper.ApplyMessage(child, statedb.New(child, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, true, false, true)
	require.NoError(t, err)
	require.Contains(t, result.VmError, "requires the block settlement handler")
	require.Empty(t, result.Logs)
	require.Equal(t, backing, e.app.BankKeeper.GetBalance(child, sdk.AccAddress(wrapped.Bytes()), identity.BaseDenom).Amount)

	// A missing native queue row is a controlled late failure: the signed
	// receipt has already paid in EVM, but its native projection must fail.
	it := state().Iterator([]byte{0x21}, []byte{0x22})
	require.True(t, it.Valid())
	pairKey, pairValue := bytes.Clone(it.Key()), bytes.Clone(it.Value())
	it.Next()
	require.False(t, it.Valid())
	require.NoError(t, it.Close())
	state().Delete(pairKey)
	result = e.send(e.keys[0], &target, new(big.Int), completion, false)
	require.Contains(t, result.VmError, "project native withdrawal")
	require.Equal(t, backing, balance(wrapped))
	pending(true)
	state().Set(pairKey, pairValue)

	e.credit.Set(amount.BigInt())
	e.send(e.keys[0], &target, new(big.Int), completion, true)
	pending(false)
	require.Equal(t, backing.Sub(amount).String(), balance(wrapped).String())
	require.Nil(t, state().Get(pairKey))
	var completed int
	for _, event := range e.events() {
		if event.Type == "decimal_withdrawal_completed" {
			completed++
		}
	}
	require.Equal(t, 1, completed)
	e.send(e.keys[0], &target, new(big.Int), completion, false)
	require.Equal(t, backing.Sub(amount).String(), balance(wrapped).String())
	return validator
}
