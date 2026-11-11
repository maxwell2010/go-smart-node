package systemcontracts_test

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	corestore "cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	"cosmossdk.io/store/prefix"
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
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

// The native projection fixture uses its own prefix in the same App multistore
// cache as EVM and bank. No application hook or genesis admission is changed.
type withdrawalFixtureService struct{ key *storetypes.KVStoreKey }

func (s withdrawalFixtureService) OpenKVStore(ctx context.Context) corestore.KVStore {
	return withdrawalFixtureStore{prefix.NewStore(sdk.UnwrapSDKContext(ctx).KVStore(s.key), []byte("test/withdrawal-settlement/"))}
}

type withdrawalFixtureStore struct{ storetypes.KVStore }

func (s withdrawalFixtureStore) Get(key []byte) ([]byte, error) { return s.KVStore.Get(key), nil }
func (s withdrawalFixtureStore) Has(key []byte) (bool, error)   { return s.KVStore.Has(key), nil }
func (s withdrawalFixtureStore) Set(key, value []byte) error {
	s.KVStore.Set(key, value)
	return nil
}
func (s withdrawalFixtureStore) Delete(key []byte) error {
	s.KVStore.Delete(key)
	return nil
}
func (s withdrawalFixtureStore) Iterator(start, end []byte) (corestore.Iterator, error) {
	return s.KVStore.Iterator(start, end), nil
}
func (s withdrawalFixtureStore) ReverseIterator(start, end []byte) (corestore.Iterator, error) {
	return s.KVStore.ReverseIterator(start, end), nil
}

type gatewayFrozenWithdrawal struct {
	Stake struct {
		Validator, Delegator, Token  common.Address
		Amount, TokenId              *big.Int
		TokenType                    uint8
		HoldTimestamp, HoldStartTime *big.Int
	}
	FreezeStatus, FreezeType uint8
	UnfreezeTimestamp        *big.Int
}

func runGatewayWithdrawalSettlement(t *testing.T, e isolatedBackendEnv, gateway contracts.Artifact, target, wrapped, validator common.Address) {
	t.Helper()
	owner := crypto.PubkeyToAddress(e.keys[1].PublicKey)
	beneficiary := crypto.PubkeyToAddress(e.keys[0].PublicKey)
	amount := sdkmath.NewInt(3_000_000_000_000_000_000)
	signed := func(key int, method string, value *big.Int, args ...interface{}) *evmtypes.MsgEthereumTxResponse {
		t.Helper()
		data, err := gateway.ABI.Pack(method, args...)
		require.NoError(t, err)
		return e.send(e.keys[key], &target, value, data, true)
	}
	signed(1, "setFreezeTime", new(big.Int), big.NewInt(60), new(big.Int))
	signed(0, "delegateDEL", amount.BigInt(), validator)
	created := e.ctx()
	request := signed(0, "withdraw", new(big.Int), validator, wrapped, amount.BigInt())
	withdrawRequest := gateway.ABI.Events["WithdrawRequest"]
	var frozenID *big.Int
	var sourceID [32]byte
	var freeze gatewayFrozenWithdrawal
	for _, log := range request.Logs {
		if common.HexToAddress(log.Address) != target || len(log.Topics) == 0 || common.HexToHash(log.Topics[0]) != withdrawRequest.ID {
			continue
		}
		require.Nil(t, frozenID, "one signed request must freeze exactly one stake")
		values, err := withdrawRequest.Inputs.NonIndexed().Unpack(log.Data)
		require.NoError(t, err)
		require.Len(t, values, 3)
		sourceID = values[0].([32]byte)
		frozenID = values[1].(*big.Int)
		freeze = *abi.ConvertType(values[2], new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
	}
	require.NotNil(t, frozenID)
	require.NotEqual(t, [32]byte{}, sourceID)
	require.Equal(t, validator, freeze.Stake.Validator)
	require.Equal(t, beneficiary, freeze.Stake.Delegator)
	require.Equal(t, wrapped, freeze.Stake.Token)
	require.Equal(t, amount.BigInt(), freeze.Stake.Amount)
	require.Equal(t, uint8(1), freeze.FreezeStatus)
	require.Equal(t, uint8(0), freeze.FreezeType)
	deadline := created.BlockTime().Add(time.Minute)
	require.Equal(t, big.NewInt(deadline.Unix()), freeze.UnfreezeTimestamp)

	// Never write this outer cache to the application fixture. The scheduler
	// commits successful attempts into it, through the actual SDK CacheContext.
	outer, _ := e.ctx().CacheContext()
	outer = outer.WithGasMeter(storetypes.NewGasMeter(15_000_000))
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	storeKey := e.app.GetKey(validatortypes.StoreKey)
	require.NotNil(t, storeKey)
	k, err := validatorkeeper.NewStateKeeperWithDomain(withdrawalFixtureService{storeKey}, domain.ChainID, domain.EVMChainID)
	require.NoError(t, err)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(validator.Bytes())
	require.NoError(t, err)
	delegator, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(beneficiary.Bytes())
	require.NoError(t, err)
	v, err := validatortypes.NewValidator(operator, delegator, &ed25519.PubKey{Key: bytes.Repeat([]byte{0x71}, 32)}, validatortypes.Description{Moniker: "withdrawal-fixture"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	entry := validatortypes.UndelegationEntry{CreationHeight: created.BlockHeight(), CompletionTime: deadline, Stake: validatortypes.NewStakeCoin(sdk.NewCoin(identity.BaseDenom, amount))}
	entryHash, err := validatortypes.NativeQueueEntryHash(1, delegator, operator, "", &entry)
	require.NoError(t, err)
	binding := validatortypes.EVMFrozenBinding{
		Gateway: target.Bytes(), FrozenIndex: common.BigToHash(frozenID).Bytes(), SourceKind: validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN,
		SourceStakeID: sourceID[:], SourceValidator: validator.Bytes(), Beneficiary: beneficiary.Bytes(),
		FreezeType: 1, CompletionTimestamp: common.BigToHash(freeze.UnfreezeTimestamp).Bytes(), RawAmount: amount,
		ReportedReserveAmount: sdkmath.ZeroInt(), ReserveAsset: &validatortypes.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom},
		NativeTarget:    &validatortypes.NativeStakeTarget{StakeType: validatortypes.StakeType_Coin, ID: identity.BaseDenom},
		NativeEntryHash: entryHash, Active: true, CreatedHeight: created.BlockHeight(), UpdatedHeight: created.BlockHeight(),
	}
	genesis := validatortypes.DefaultGenesisState()
	genesis.BindingDomain = &domain
	genesis.Validators = []validatortypes.Validator{v}
	genesis.Undelegations = []validatortypes.Undelegation{{Delegator: delegator, Validator: operator, Entries: []validatortypes.UndelegationEntry{entry}}}
	genesis.EVMFrozenBindings = []validatortypes.EVMFrozenBinding{binding}
	require.NoError(t, k.InitGenesis(outer, genesis))

	const attemptGas, evmGas = 3_000_000, 500_000
	apply := func(ctx sdk.Context, commit bool, data []byte) (*evmtypes.MsgEthereumTxResponse, error) {
		message := core.Message{From: owner, To: &target, Value: new(big.Int), GasLimit: evmGas, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int), Data: data, SkipNonceChecks: true, SkipFromEOACheck: true}
		return e.app.EVMKeeper.ApplyMessage(ctx, statedb.New(ctx, e.app.EVMKeeper, statedb.NewEmptyTxConfig()), message, nil, commit, false, true)
	}
	completion, err := gateway.ABI.Pack("complete", []*big.Int{frozenID})
	require.NoError(t, err)
	getFrozen, err := gateway.ABI.Pack("getFrozenStake", frozenID)
	require.NoError(t, err)
	freezeStatus := func(ctx sdk.Context) uint8 {
		t.Helper()
		read, _ := ctx.CacheContext()
		read = read.WithGasMeter(storetypes.NewGasMeter(attemptGas))
		result, err := apply(read, false, getFrozen)
		require.NoError(t, err)
		require.Empty(t, result.VmError)
		values, err := gateway.ABI.Unpack("getFrozenStake", result.Ret)
		require.NoError(t, err)
		require.Len(t, values, 1)
		return abi.ConvertType(values[0], new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal).FreezeStatus
	}
	bank := func(ctx sdk.Context, account common.Address) sdkmath.Int {
		t.Helper()
		balance := e.app.BankKeeper.GetBalance(ctx, sdk.AccAddress(account.Bytes()), identity.BaseDenom).Amount
		require.Equal(t, balance.BigInt(), e.app.EVMKeeper.GetBalance(ctx, account).ToBig())
		return balance
	}
	before := bank(outer, beneficiary)
	backing := bank(outer, wrapped)
	supply := e.app.BankKeeper.GetSupply(outer, identity.BaseDenom).Amount
	require.True(t, backing.GTE(amount))
	assertState := func(ctx sdk.Context, active bool, expectedBalance, expectedBacking sdkmath.Int) {
		t.Helper()
		g, err := k.ExportGenesis(ctx)
		require.NoError(t, err)
		require.Len(t, g.EVMFrozenBindings, 1)
		require.Equal(t, active, g.EVMFrozenBindings[0].Active)
		if active {
			require.Len(t, g.Undelegations, 1)
			require.Len(t, g.Undelegations[0].Entries, 1)
			require.Equal(t, entry, g.Undelegations[0].Entries[0])
			require.Equal(t, uint8(1), freezeStatus(ctx))
		} else {
			require.Empty(t, g.Undelegations)
			require.Equal(t, uint8(0), freezeStatus(ctx))
		}
		require.Equal(t, expectedBalance, bank(ctx, beneficiary))
		require.Equal(t, expectedBacking, bank(ctx, wrapped))
		require.Equal(t, supply, e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount)
	}
	const projectionEvent = "test_withdrawal_projected"
	attempts := 0
	execute := func(child sdk.Context, row validatortypes.EVMFrozenBinding) error {
		attempts++
		require.Equal(t, uint64(attemptGas), child.GasMeter().Limit())
		require.Equal(t, binding.FrozenIndex, row.FrozenIndex)
		response, err := apply(child, true, completion)
		require.NoError(t, err)
		require.Empty(t, response.VmError)
		used := max(response.GasUsed, response.MaxUsedGas)
		require.Positive(t, used)
		require.LessOrEqual(t, used, uint64(evmGas))
		child.GasMeter().ConsumeGas(used, "fixture Gateway settlement")
		withdrawCompleted := gateway.ABI.Events["WithdrawCompleted"]
		var count int
		for _, log := range response.Logs {
			if common.HexToAddress(log.Address) != target || len(log.Topics) == 0 || common.HexToHash(log.Topics[0]) != withdrawCompleted.ID {
				continue
			}
			values, err := withdrawCompleted.Inputs.NonIndexed().Unpack(log.Data)
			require.NoError(t, err)
			require.Equal(t, []interface{}{frozenID}, values)
			count++
		}
		require.Equal(t, 1, count)
		require.Equal(t, before.Add(amount), bank(child, beneficiary), "real WDEL unwrap credits exactly one DEL payout inside the attempt")
		row.Active, row.UpdatedHeight = false, child.BlockHeight()
		plan, err := k.PrepareBindingPlan(child, []validatorkeeper.BindingMutation{{Frozen: &row}})
		require.NoError(t, err)
		require.NoError(t, k.ApplyBindingPlan(child, plan))
		child.EventManager().EmitEvent(sdk.NewEvent(projectionEvent))
		if attempts == 1 {
			return errors.New("fixture projection rejected after Gateway payout")
		}
		return nil
	}
	projected := func(ctx sdk.Context) int {
		count := 0
		for _, event := range ctx.EventManager().Events() {
			if event.Type == projectionEvent {
				count++
			}
		}
		return count
	}
	outer = outer.WithBlockTime(deadline.Add(-time.Second))
	result, err := k.SettleMatureWithdrawals(outer, attemptGas, execute)
	require.NoError(t, err)
	require.Empty(t, result)
	require.Zero(t, attempts)
	assertState(outer, true, before, backing)

	outer = outer.WithBlockTime(deadline)
	gasBefore := outer.GasMeter().GasConsumed()
	result, err = k.SettleMatureWithdrawals(outer, attemptGas, execute)
	require.NoError(t, err)
	require.Equal(t, validatorkeeper.WithdrawalSettlementResult{Retried: 1}, result)
	require.Greater(t, outer.GasMeter().GasConsumed(), gasBefore)
	require.Zero(t, projected(outer))
	assertState(outer, true, before, backing)

	outer = outer.WithBlockHeight(outer.BlockHeight() + 1).WithBlockTime(deadline.Add(time.Second))
	result, err = k.SettleMatureWithdrawals(outer, attemptGas, execute)
	require.NoError(t, err)
	require.Equal(t, validatorkeeper.WithdrawalSettlementResult{Settled: 1}, result)
	require.Equal(t, 1, projected(outer))
	assertState(outer, false, before.Add(amount), backing.Sub(amount))

	outer = outer.WithBlockHeight(outer.BlockHeight() + 1).WithBlockTime(deadline.Add(2 * time.Second))
	result, err = k.SettleMatureWithdrawals(outer, attemptGas, execute)
	require.NoError(t, err)
	require.Empty(t, result)
	require.Equal(t, 2, attempts)
	require.Equal(t, 1, projected(outer))
	assertState(outer, false, before.Add(amount), backing.Sub(amount))
	require.Equal(t, before, bank(e.ctx(), beneficiary), "fixture cache never writes to the app's committed state")
}
