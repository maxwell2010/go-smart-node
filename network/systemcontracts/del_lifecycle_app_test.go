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
	delLifecyclePassed     = "signed DEL delegation, top-up, partial and full withdrawals, and exactly-once payouts passed"
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

	one := sdkmath.NewIntWithDecimal(1, 18)
	two := sdkmath.NewIntWithDecimal(2, 18)
	three := sdkmath.NewIntWithDecimal(3, 18)
	five := three.Add(two)
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
		require.Zero(t, want.BigInt().Cmp(read(wrapper, wrapped, "totalSupply").(*big.Int)))
		require.Zero(t, want.BigInt().Cmp(read(wrapper, wrapped, "balanceOf", target).(*big.Int)))
		require.True(t, want.Equal(e.app.BankKeeper.GetBalance(e.ctx(), sdk.AccAddress(wrapped.Bytes()), identity.BaseDenom).Amount))
		require.Zero(t, want.BigInt().Cmp(e.app.EVMKeeper.GetBalance(e.ctx(), wrapped).ToBig()))
	}
	k := e.app.NativeValidatorKeeper
	stakeID := read(gateway, target, "getStakeId", validator, user, wrapped).([32]byte)
	require.NotEqual(t, [32]byte{}, stakeID)
	stakeHash := common.Hash(stakeID)
	kind := validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	coinStake := func(amount sdkmath.Int) validatortypes.Stake {
		return validatortypes.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))
	}
	var createdHeight int64
	activeStake := func(want sdkmath.Int) {
		t.Helper()
		raw := read(gateway, target, "getStake", validator, user, wrapped)
		row := abi.ConvertType(raw, new(gatewayDELStake)).(*gatewayDELStake)
		require.Zero(t, want.BigInt().Cmp(row.Amount))
		binding, bindingErr := k.GetEVMStakeBinding(e.ctx(), kind, target.Bytes(), stakeID[:])
		delegation, delegationErr := k.GetDelegation(e.ctx(), beneficiary, operator, validatortypes.StakeType_Coin, identity.BaseDenom)
		if want.IsZero() {
			require.ErrorIs(t, bindingErr, validatorkeeper.ErrNotFound)
			require.ErrorIs(t, delegationErr, validatorkeeper.ErrNotFound)
			return
		}
		require.Equal(t, validator, row.Validator)
		require.Equal(t, user, row.Delegator)
		require.Equal(t, wrapped, row.Token)
		require.Equal(t, uint8(4), row.TokenType)
		require.Zero(t, row.TokenId.Sign())
		require.Zero(t, row.HoldTimestamp.Sign())
		require.Zero(t, row.HoldStartTime.Sign())
		require.NoError(t, bindingErr)
		require.True(t, binding.Active)
		require.True(t, want.Equal(binding.RawAmount))
		require.Equal(t, createdHeight, binding.CreatedHeight)
		require.Equal(t, user.Bytes(), binding.Beneficiary)
		require.Equal(t, validator.Bytes(), binding.Validator)
		require.NoError(t, delegationErr)
		require.True(t, delegation.Stake.Equal(coinStake(want)))
	}
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
	signed("delegateDEL", three, false, validator)
	activeStake(zero)
	backing(zero)
	require.Empty(t, native().Delegations)
	require.Empty(t, native().EVMStakeBindings)

	v, err := validatortypes.NewValidator(operator, beneficiary, &ed25519.PubKey{Key: key},
		validatortypes.Description{Moniker: "del-lifecycle"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	require.NoError(t, k.SetValidator(e.ctx(), v))
	createdHeight = e.ctx().BlockHeight()
	signed("delegateDEL", three, true, validator)
	eventOnce("decimal_del_delegated", map[string]string{"stake_id": stakeHash.Hex(), "amount": three.String()})
	activeStake(three)
	backing(three)
	signed("delegateDEL", two, true, validator)
	eventOnce("decimal_del_delegated", map[string]string{"stake_id": stakeHash.Hex(), "amount": two.String()})
	activeStake(five)
	backing(five)

	beforeInvalid := native()
	signed("delegateHoldDEL", one, false, validator, big.NewInt(e.ctx().BlockTime().Add(time.Minute).Unix()))
	signed("withdraw", zero, false, validator, wrapped, five.Add(one).BigInt())
	require.Equal(t, beforeInvalid, native())
	activeStake(five)
	backing(five)

	type claim struct {
		index    *big.Int
		deadline time.Time
		amount   sdkmath.Int
	}
	checkClaim := func(item claim, pending bool) {
		t.Helper()
		frozenID := common.BigToHash(item.index)
		frozen, err := k.GetEVMFrozenBinding(e.ctx(), target.Bytes(), frozenID.Bytes())
		require.NoError(t, err)
		require.Equal(t, pending, frozen.Active)
		require.Equal(t, stakeID[:], frozen.SourceStakeID)
		require.True(t, item.amount.Equal(frozen.RawAmount))
		require.Equal(t, common.BigToHash(big.NewInt(item.deadline.Unix())).Bytes(), frozen.CompletionTimestamp)
		raw := read(gateway, target, "getFrozenStake", item.index)
		evm := abi.ConvertType(raw, new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		var status uint8
		if pending {
			status = 1
		}
		require.Equal(t, status, evm.FreezeStatus)
		require.Zero(t, item.amount.BigInt().Cmp(evm.Stake.Amount))
	}
	request := func(amount sdkmath.Int) claim {
		t.Helper()
		item := claim{deadline: e.ctx().BlockTime().Add(freezeSeconds * time.Second), amount: amount}
		response := signed("withdraw", zero, true, validator, wrapped, amount.BigInt())
		event := gateway.ABI.Events["WithdrawRequest"]
		for _, log := range response.Logs {
			if common.HexToAddress(log.Address) != target || len(log.Topics) == 0 || common.HexToHash(log.Topics[0]) != event.ID {
				continue
			}
			require.Nil(t, item.index)
			values, err := event.Inputs.NonIndexed().Unpack(log.Data)
			require.NoError(t, err)
			require.Len(t, values, 3)
			require.Equal(t, stakeID, values[0])
			item.index = values[1].(*big.Int)
		}
		require.NotNil(t, item.index)
		eventOnce("decimal_del_withdrawal_requested", map[string]string{
			"stake_id": stakeHash.Hex(), "frozen_index": common.BigToHash(item.index).Hex(), "amount": amount.String(),
		})
		checkClaim(item, true)
		return item
	}
	checkQueue := func(claims ...claim) {
		t.Helper()
		queue := native().Undelegations
		if len(claims) == 0 {
			require.Empty(t, queue)
			return
		}
		require.Len(t, queue, 1)
		pair := queue[0]
		require.Equal(t, beneficiary, pair.Delegator)
		require.Equal(t, operator, pair.Validator)
		require.Len(t, pair.Entries, len(claims))
		for i, item := range claims {
			require.True(t, item.deadline.Equal(pair.Entries[i].CompletionTime))
			require.True(t, pair.Entries[i].Stake.Equal(coinStake(item.amount)))
			checkClaim(item, true)
		}
	}
	rejectComplete := func(item claim) {
		t.Helper()
		before := native()
		signed("complete", zero, false, []*big.Int{item.index})
		require.Equal(t, before, native())
	}
	settle := func(item claim, remainingBacking sdkmath.Int) {
		t.Helper()
		for e.ctx().BlockTime().Before(item.deadline) {
			e.advance()
		}
		e.credit.Set(item.amount.BigInt())
		signed("complete", zero, true, []*big.Int{item.index})
		checkClaim(item, false)
		backing(remainingBacking)
		eventOnce("decimal_withdrawal_completed", map[string]string{
			"frozen_index": common.BigToHash(item.index).Hex(),
		})
		rejectComplete(item)
		backing(remainingBacking)
	}

	first := request(one)
	activeStake(five.Sub(one))
	backing(five)
	rejectComplete(first)
	e.advance()
	e.advance()
	second := request(two)
	require.NotEqual(t, common.BigToHash(first.index), common.BigToHash(second.index))
	activeStake(two)
	backing(five)
	checkQueue(first, second)
	settle(first, five.Sub(one))
	activeStake(two)
	checkQueue(second)
	rejectComplete(second)
	settle(second, two)
	activeStake(two)
	checkQueue()

	last := request(two)
	activeStake(zero)
	backing(two)
	checkQueue(last)
	settle(last, zero)
	checkQueue()
	activeStake(zero)
	backing(zero)
	require.Equal(t, bankSupply, e.app.BankKeeper.GetSupply(e.ctx(), identity.BaseDenom))
	t.Log(delLifecyclePassed)
}
