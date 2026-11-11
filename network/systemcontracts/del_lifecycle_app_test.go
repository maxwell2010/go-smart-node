package systemcontracts_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/x/vm/statedb"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const (
	delLifecycleFixtureEnv = "MCND_SIGNED_DEL_LIFECYCLE_FIXTURE"
	delLifecyclePassed     = "signed DEL delegate, native freeze, and exactly-once completion passed"
)

// The SDK's EVM configuration belongs to one App per process. Reuse the
// existing real proxy bootstrap in a fresh test process for the DEL profile.
func TestOwnedAppSignedDELDelegationLifecycle(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=2m",
		"-test.run=^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$")
	child.Env = append(os.Environ(), delLifecycleFixtureEnv+"=1", withdrawalGenesisFixtureEnv+"=")
	output, err := child.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), delLifecyclePassed)
	t.Log(delLifecyclePassed)
}

type gatewayDELStake struct {
	Validator, Delegator, Token  common.Address
	Amount, TokenId              *big.Int
	TokenType                    uint8
	HoldTimestamp, HoldStartTime *big.Int
}

func runSignedDELDelegationLifecycle(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, gateway, master, wrapper contracts.Artifact,
	gov func(contracts.Artifact, common.Address, string, ...interface{})) {
	t.Helper()
	require.NotNil(t, e.nextGasLimit)
	require.NotNil(t, e.advance)
	user := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	validator := crypto.CreateAddress(user, e.app.EVMKeeper.GetNonce(e.ctx(), user)+1000)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	beneficiary, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(user.Bytes())
	require.NoError(t, err)
	key := bytes.Repeat([]byte{0x42}, 32)
	meta, err := json.Marshal(map[string]interface{}{
		"operator_address": operator, "reward_address": user.Hex(), "consensus_pubkey": base64.StdEncoding.EncodeToString(key),
		"description": map[string]string{"moniker": "del-lifecycle"}, "commission": "0",
	})
	require.NoError(t, err)
	gov(master, profile.MasterValidator, "addValidators", []struct {
		Validator common.Address
		Meta      string
	}{{validator, string(meta)}})
	const freezeSeconds = 20
	gov(gateway, profile.Gateway, "setFreezeTime", big.NewInt(freezeSeconds), new(big.Int))

	config, err := json.Marshal(profile)
	require.NoError(t, err)
	e.ctx().KVStore(e.app.GetKey(validatortypes.StoreKey)).Set([]byte{ownedapp.WithdrawalProfileKey}, config)
	require.NoError(t, e.app.LoadWithdrawalSettlement(e.ctx()))

	amount := sdkmath.NewIntWithDecimal(3, 18)
	partial := sdkmath.NewIntWithDecimal(1, 18)
	zero := sdkmath.ZeroInt()
	eventOnce := func(name string, expected map[string]string) {
		t.Helper()
		count := 0
		for _, event := range e.events() {
			if event.Type != name {
				continue
			}
			count++
			attributes := make(map[string]string, len(event.Attributes))
			for _, attribute := range event.Attributes {
				attributes[attribute.Key] = attribute.Value
			}
			for key, value := range expected {
				require.Equal(t, value, attributes[key], "%s attribute %s", name, key)
			}
		}
		require.Equal(t, 1, count, name)
	}
	target, wrapped := profile.Gateway, profile.WrappedDEL
	signed := func(method string, value sdkmath.Int, success bool, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := gateway.ABI.Pack(method, args...)
		require.NoError(t, err)
		e.nextGasLimit(24_000_000)
		return e.send(e.keys[0], &target, value.BigInt(), data, success)
	}
	read := func(artifact contracts.Artifact, contract common.Address, method string, args ...interface{}) interface{} {
		t.Helper()
		data, err := artifact.ABI.Pack(method, args...)
		require.NoError(t, err)
		ctx, _ := e.ctx().CacheContext()
		ctx = ctx.WithGasMeter(storetypes.NewGasMeter(3_000_000))
		msg := core.Message{From: user, To: &contract, Value: new(big.Int), GasLimit: 500_000,
			GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		response, err := e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), msg, nil, false, false, true)
		require.NoError(t, err)
		require.Empty(t, response.VmError)
		values, err := artifact.ABI.Unpack(method, response.Ret)
		require.NoError(t, err)
		require.Len(t, values, 1)
		return values[0]
	}
	backing := func(want sdkmath.Int) {
		t.Helper()
		require.Equal(t, want.BigInt(), read(wrapper, wrapped, "totalSupply"))
		require.Equal(t, want.BigInt(), read(wrapper, wrapped, "balanceOf", target))
		require.Equal(t, want, e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(wrapped.Bytes()), identity.BaseDenom).Amount)
		require.Equal(t, want.BigInt(), e.app.EVMKeeper.GetBalance(e.ctx(), wrapped).ToBig())
	}
	activeStake := func(want sdkmath.Int) {
		t.Helper()
		raw := read(gateway, target, "getStake", validator, user, wrapped)
		row := abi.ConvertType(raw, new(gatewayDELStake)).(*gatewayDELStake)
		require.Equal(t, want.BigInt(), row.Amount)
		if want.IsPositive() {
			require.Equal(t, validator, row.Validator)
			require.Equal(t, user, row.Delegator)
			require.Equal(t, wrapped, row.Token)
			require.Equal(t, uint8(4), row.TokenType)
			require.Zero(t, row.TokenId.Sign())
			require.Zero(t, row.HoldTimestamp.Sign())
			require.Zero(t, row.HoldStartTime.Sign())
		}
	}
	k := e.app.NativeValidatorKeeper
	native := func() *validatortypes.GenesisState {
		t.Helper()
		state, err := k.ExportGenesis(e.ctx())
		require.NoError(t, err)
		return state
	}
	require.Empty(t, native().Delegations)
	require.Empty(t, native().Undelegations)
	bankSupply := e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom)
	backing(zero)

	// Master allows this source, but the app must roll back the signed deposit
	// and wrapper mint until the native operator has been declared.
	signed("delegateDEL", amount, false, validator)
	activeStake(zero)
	backing(zero)
	require.Empty(t, native().Delegations)
	require.Empty(t, native().EVMStakeBindings)

	v, err := validatortypes.NewValidator(operator, beneficiary, &ed25519.PubKey{Key: key},
		validatortypes.Description{Moniker: "del-lifecycle"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	require.NoError(t, k.SetValidator(e.ctx(), v))
	signed("delegateDEL", amount, true, validator)
	stakeID := read(gateway, target, "getStakeId", validator, user, wrapped).([32]byte)
	require.NotEqual(t, [32]byte{}, stakeID)
	eventOnce("decimal_del_delegated", map[string]string{"stake_id": common.Hash(stakeID).Hex(), "amount": amount.String()})
	kind := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	binding, err := k.GetEVMStakeBinding(e.ctx(), kind, target.Bytes(), stakeID[:])
	require.NoError(t, err)
	require.True(t, binding.Active)
	require.Equal(t, amount, binding.RawAmount)
	require.Equal(t, user.Bytes(), binding.Beneficiary)
	require.Equal(t, validator.Bytes(), binding.Validator)
	delegation, err := k.GetDelegation(e.ctx(), beneficiary, operator, validatortypes.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, validatortypes.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount)), delegation.Stake)
	activeStake(amount)
	backing(amount)

	beforePartial := native()
	signed("delegateDEL", partial, false, validator)
	signed("withdraw", zero, false, validator, wrapped, partial.BigInt())
	require.Equal(t, beforePartial, native())
	activeStake(amount)
	backing(amount)

	requestTime := e.ctx().BlockTime()
	response := signed("withdraw", zero, true, validator, wrapped, amount.BigInt())
	event := gateway.ABI.Events["WithdrawRequest"]
	var index *big.Int
	for _, log := range response.Logs {
		if common.HexToAddress(log.Address) != target || len(log.Topics) == 0 || common.HexToHash(log.Topics[0]) != event.ID {
			continue
		}
		require.Nil(t, index)
		values, err := event.Inputs.NonIndexed().Unpack(log.Data)
		require.NoError(t, err)
		require.Len(t, values, 3)
		require.Equal(t, stakeID, values[0])
		index = values[1].(*big.Int)
	}
	require.NotNil(t, index)
	frozenID := common.BigToHash(index)
	eventOnce("decimal_del_withdrawal_requested", map[string]string{
		"stake_id": common.Hash(stakeID).Hex(), "frozen_index": frozenID.Hex(), "amount": amount.String(),
	})
	frozen, err := k.GetEVMFrozenBinding(e.ctx(), target.Bytes(), frozenID.Bytes())
	require.NoError(t, err)
	require.True(t, frozen.Active)
	require.Equal(t, stakeID[:], frozen.SourceStakeID)
	require.Equal(t, amount, frozen.RawAmount)
	deadline := requestTime.Add(freezeSeconds * time.Second)
	require.Equal(t, common.BigToHash(big.NewInt(deadline.Unix())).Bytes(), frozen.CompletionTimestamp)
	_, err = k.GetDelegation(e.ctx(), beneficiary, operator, validatortypes.StakeType_Coin, identity.BaseDenom)
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	pending := native()
	require.Len(t, pending.Undelegations, 1)
	pair := pending.Undelegations[0]
	require.Equal(t, beneficiary, pair.Delegator)
	require.Equal(t, operator, pair.Validator)
	require.Len(t, pair.Entries, 1)
	require.True(t, deadline.Equal(pair.Entries[0].CompletionTime))
	require.Equal(t, delegation.Stake, pair.Entries[0].Stake)
	activeStake(zero)
	backing(amount)
	frozenStatus := func() uint8 {
		t.Helper()
		raw := read(gateway, target, "getFrozenStake", index)
		return abi.ConvertType(raw, new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal).FreezeStatus
	}
	require.Equal(t, uint8(1), frozenStatus())
	signed("complete", zero, false, []*big.Int{index})
	require.Equal(t, pending, native())
	backing(amount)
	for e.ctx().BlockTime().Before(deadline) {
		e.advance()
	}

	e.credit.Set(amount.BigInt())
	signed("complete", zero, true, []*big.Int{index})
	require.Equal(t, uint8(0), frozenStatus())
	settled, err := k.GetEVMFrozenBinding(e.ctx(), target.Bytes(), frozenID.Bytes())
	require.NoError(t, err)
	require.False(t, settled.Active)
	require.Empty(t, native().Undelegations)
	backing(zero)
	eventOnce("decimal_withdrawal_completed", nil)

	signed("complete", zero, false, []*big.Int{index})
	backing(zero)
	require.Empty(t, native().Undelegations)
	require.Equal(t, bankSupply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom))
	t.Log(delLifecyclePassed)
}
