//go:build !experimental_ibc

package app

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	"bitbucket.org/decimalteam/go-smart-node/contracts"
	"bitbucket.org/decimalteam/go-smart-node/contracts/delegation"
	"bitbucket.org/decimalteam/go-smart-node/contracts/delegationv7"
	cointypes "bitbucket.org/decimalteam/go-smart-node/x/coin/types"
	validatorkeeper "bitbucket.org/decimalteam/go-smart-node/x/validator/keeper"
	validatortypes "bitbucket.org/decimalteam/go-smart-node/x/validator/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	evmkeeper "github.com/decimalteam/ethermint/x/evm/keeper"
	evmtypes "github.com/decimalteam/ethermint/x/evm/types"
	evmgeth "github.com/decimalteam/ethermint/x/evm/vm/geth"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	abci "github.com/tendermint/tendermint/abci/types"
	cmtjson "github.com/tendermint/tendermint/libs/json"
	tmtypes "github.com/tendermint/tendermint/types"
)

// receiptCorruptionHooks injects a decode fault into a genuine contract event.
// It never replaces deployed bytecode or skips the production hook chain.
type receiptCorruptionHooks struct {
	inner         evmtypes.EvmHooks
	target        common.Address
	topic         common.Hash
	armed         bool
	injected      bool
	injectedTopic common.Hash
	lastError     error
}

// rehearsalEmittedStake selects only an ABI identified by the genuine receipt's
// event topic. The post-recovery runtime may emit the current eight-field tuple;
// the pre-recovery runtime emits the seven-field tuple. Never rewrite receipts.
func rehearsalEmittedStake(t *testing.T, response *evmtypes.MsgEthereumTxResponse, target common.Address) (*abi.ABI, common.Hash, delegation.IDecimalDelegationCommonStake) {
	t.Helper()
	current, err := delegation.DelegationMetaData.GetAbi()
	require.NoError(t, err)
	legacy, err := delegationv7.DelegationMetaData.GetAbi()
	require.NoError(t, err)
	var topics []common.Hash
	for _, log := range response.Logs {
		entry := log.ToEthereum()
		if entry.Address != target || len(entry.Topics) == 0 {
			continue
		}
		topics = append(topics, entry.Topics[0])
		if entry.Topics[0] == current.Events["StakeUpdated"].ID {
			var event delegation.DelegationStakeUpdated
			require.NoError(t, contracts.UnpackLog(current, &event, "StakeUpdated", entry))
			return current, entry.Topics[0], event.Stake
		}
		if entry.Topics[0] == legacy.Events["StakeUpdated"].ID {
			var event delegationv7.DelegationStakeUpdated
			require.NoError(t, contracts.UnpackLog(legacy, &event, "StakeUpdated", entry))
			s := event.Stake
			return legacy, entry.Topics[0], delegation.IDecimalDelegationCommonStake{Validator: s.Validator, Delegator: s.Delegator, Token: s.Token, Amount: s.Amount, TokenId: s.TokenId, TokenType: s.TokenType, HoldTimestamp: s.HoldTimestamp, HoldStartTime: new(big.Int)}
		}
	}
	t.Fatalf("genuine receipt must contain a supported StakeUpdated topic; actual topics=%v", topics)
	return nil, common.Hash{}, delegation.IDecimalDelegationCommonStake{}
}

func (h *receiptCorruptionHooks) PostTxProcessing(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	if h.armed {
		for _, log := range receipt.Logs {
			if log.Address == h.target && len(log.Topics) > 0 && log.Topics[0] == h.topic && len(log.Data) > 0 {
				log.Data = nil
				h.injectedTopic = log.Topics[0]
				h.injected = true
				break
			}
		}
	}
	h.lastError = h.inner.PostTxProcessing(ctx, msg, receipt)
	return h.lastError
}

// copyClosedRehearsalTree only reads a caller-provided CLOSED snapshot-derived
// database and copies it to a new test directory. It rejects links and devices.
func copyClosedRehearsalTree(t *testing.T, source, destination string) {
	t.Helper()
	require.NoError(t, filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse source symlink %s", path)
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refuse nonregular source %s", path)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	}))
}

func TestMainnetSystemContractFaultRehearsal(t *testing.T) {
	source := os.Getenv("DSC_CLOSED_MAINNET_REHEARSAL_HOME")
	headerFile := os.Getenv("DSC_MAINNET_REHEARSAL_NEXT_BLOCK")
	if source == "" || headerFile == "" {
		t.Skip("set CLOSED snapshot-derived home and canonical next-block JSON; never use a working node home")
	}
	require.True(t, filepath.IsAbs(source))
	require.True(t, filepath.IsAbs(headerFile))
	home := t.TempDir()
	copyClosedRehearsalTree(t, filepath.Join(source, "data", "application.db"), filepath.Join(home, "data", "application.db"))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "config"), 0700))
	genesis, err := os.ReadFile(filepath.Join(source, "config", "genesis.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config", "genesis.json"), genesis, 0600))
	blockBytes, err := os.ReadFile(headerFile)
	require.NoError(t, err)
	var envelope struct {
		Result struct {
			Block json.RawMessage `json:"block"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(blockBytes, &envelope))
	var block tmtypes.Block
	require.NoError(t, cmtjson.Unmarshal(envelope.Result.Block, &block))
	require.Equal(t, "decimal_75-1", block.ChainID)
	dsc, db := reliabilityOpen(t, home)
	defer db.Close()
	require.Equal(t, dsc.LastBlockHeight()+1, block.Height)
	require.Equal(t, strings.ToUpper(fmt.Sprintf("%X", dsc.LastCommitID().Hash)), strings.ToUpper(fmt.Sprintf("%X", block.AppHash)))
	old := validatorkeeper.ReliabilityFixActivationHeights
	validatorkeeper.ReliabilityFixActivationHeights = map[string]int64{block.ChainID: 1}
	t.Cleanup(func() { validatorkeeper.ReliabilityFixActivationHeights = old })
	dsc.BeginBlock(abci.RequestBeginBlock{Header: *block.Header.ToProto()})
	context := func() sdk.Context {
		return dsc.BaseApp.NewContext(false, *block.Header.ToProto()).WithGasMeter(sdk.NewInfiniteGasMeter())
	}
	ctx := context()
	delegationAddress, err := contracts.GetAddressFromContractCenter(ctx, &dsc.EvmKeeper, contracts.NameOfSlugForGetAddressDelegation)
	require.NoError(t, err)
	target := common.HexToAddress(delegationAddress)
	require.NotNil(t, dsc.EvmKeeper.GetAccount(ctx, target))
	legacyABI, err := delegationv7.DelegationMetaData.GetAbi()
	require.NoError(t, err)
	validators := dsc.ValidatorKeeper.GetAllValidators(ctx)
	require.NotEmpty(t, validators)
	validatorAddress, err := sdk.ValAddressFromBech32(validators[0].OperatorAddress)
	require.NoError(t, err)
	validator := common.BytesToAddress(validatorAddress.Bytes())
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	from := crypto.PubkeyToAddress(key.PublicKey)
	account := dsc.AccountKeeper.NewAccountWithAddress(ctx, sdk.AccAddress(from.Bytes()))
	dsc.AccountKeeper.SetAccount(ctx, account)
	funds := sdk.NewCoins(sdk.NewCoin(cmdcfg.BaseDenom, sdk.NewInt(1_000_000).Mul(sdk.NewInt(1_000_000_000_000_000_000))))
	require.NoError(t, dsc.BankKeeper.MintCoins(ctx, cointypes.ModuleName, funds))
	require.NoError(t, dsc.BankKeeper.SendCoinsFromModuleToAccount(ctx, cointypes.ModuleName, account.GetAddress(), funds))
	if os.Getenv("DSC_MAINNET_AUTO_UNBOND_PROBE") == "1" {
		require.NotEqual(t, "1", os.Getenv("DSC_REQUIRE_MAINNET_CONTRACT_FAULT_REHEARSAL"), "auto-unbond exploration cannot satisfy the separate post-recovery event-fault gate")
		probeMainnetAutoUnbond(t, dsc, ctx, key, target, validator)
		return
	}
	// Reconstruct the identical keeper only to install a test-only hook wrapper;
	// all callers retain the same field pointer, stores and production dependencies.
	dsc.EvmKeeper = *evmkeeper.NewKeeper(dsc.AppCodec(), dsc.GetKey(evmtypes.StoreKey), dsc.GetTKey(evmtypes.TransientKey), authtypes.NewModuleAddress(govtypes.ModuleName), dsc.AccountKeeper, dsc.BankKeeper, &dsc.ValidatorKeeper, dsc.FeeKeeper, nil, evmgeth.NewEVM, "", dsc.GetSubspace(evmtypes.ModuleName))
	dsc.EvmKeeper.WithChainID(ctx)
	hooks := &receiptCorruptionHooks{inner: evmkeeper.NewMultiEvmHooks(dsc.CoinKeeper.Hooks(), dsc.NFTKeeper.Hooks(), dsc.ValidatorKeeper.Hooks()), target: target, topic: legacyABI.Events["StakeUpdated"].ID}
	dsc.EvmKeeper.SetHooks(hooks)
	deliver := func(to common.Address, amount *big.Int, data []byte) (*evmtypes.MsgEthereumTxResponse, *big.Int) {
		return rehearsalDeliver(t, dsc, context(), key, to, amount, data)
	}
	receiver := common.HexToAddress("0x1234567890123456789012345678901234567890")
	transferBeforeSender := dsc.EvmKeeper.GetBalance(context(), from)
	transferBeforeReceiver := dsc.EvmKeeper.GetBalance(context(), receiver)
	transfer, transferFee := deliver(receiver, big.NewInt(100), nil)
	require.Empty(t, transfer.VmError, "real snapshot registry must permit ordinary native EVM transfer")
	require.Equal(t, big.NewInt(100), new(big.Int).Sub(dsc.EvmKeeper.GetBalance(context(), receiver), transferBeforeReceiver))
	transferDebit := new(big.Int).Sub(transferBeforeSender, dsc.EvmKeeper.GetBalance(context(), from))
	require.Equal(t, new(big.Int).Add(transferFee, big.NewInt(100)), transferDebit, "actual debit must equal receipt charged gas times signed gas price plus transferred value")
	t.Log("actual snapshot native EVM transfer passed signature, outer ante, fee deduction and production hooks")
	payload, err := legacyABI.Pack("delegateDEL", validator)
	require.NoError(t, err)
	amount := big.NewInt(100_000_000_000_000_000)
	baselineBeforeSender := dsc.EvmKeeper.GetBalance(context(), from)
	baselineBeforeContract := dsc.EvmKeeper.GetBalance(context(), target)
	baseline, baselineFee := deliver(target, amount, payload)
	if !dsc.ValidatorKeeper.LegacyDelegationEventsEnabled(context()) {
		require.Equal(t, evmtypes.ErrPostTxProcessing.Error(), baseline.VmError)
		require.Error(t, hooks.lastError)
		require.Contains(t, hooks.lastError.Error(), "financial contract emitted unknown event")
		require.Equal(t, baselineFee, new(big.Int).Sub(baselineBeforeSender, dsc.EvmKeeper.GetBalance(context(), from)), "real pre-recovery event error must refund all stake value and charge only the exact gas fee")
		require.Equal(t, baselineBeforeContract, dsc.EvmKeeper.GetBalance(context(), target))
		_, found := dsc.ValidatorKeeper.GetDelegation(context(), account.GetAddress(), validatorAddress, cmdcfg.BaseDenom)
		require.False(t, found, "rejected genuine event must not create a native delegation")
		t.Logf("pre-recovery activation incompatibility confirmed: %v", hooks.lastError)
		if os.Getenv("DSC_REQUIRE_MAINNET_CONTRACT_FAULT_REHEARSAL") == "1" {
			t.Fatal("fault rehearsal gate requires an actual post-recovery snapshot with committed legacy-event migration; do not set the flag manually")
		}
		return
	}
	require.Empty(t, baseline.VmError, "baseline must execute the genuine delegation runtime and hooks: %v", hooks.lastError)
	require.NotEmpty(t, baseline.Logs)
	actualABI, actualTopic, emitted := rehearsalEmittedStake(t, baseline, target)
	hooks.topic = actualTopic
	require.Positive(t, emitted.Amount.Sign())
	queryStake := func() []byte {
		res, callErr := dsc.EvmKeeper.CallEVM(context(), *actualABI, from, target, false, "getStake", validator, from, emitted.Token)
		require.NoError(t, callErr)
		require.Empty(t, res.VmError)
		values, unpackErr := actualABI.Unpack("getStake", res.Ret)
		require.NoError(t, unpackErr)
		if actualTopic == legacyABI.Events["StakeUpdated"].ID {
			stake := abi.ConvertType(values[0], new(delegationv7.IDecimalDelegationCommonStake)).(*delegationv7.IDecimalDelegationCommonStake)
			require.Positive(t, stake.Amount.Sign())
		} else {
			stake := abi.ConvertType(values[0], new(delegation.IDecimalDelegationCommonStake)).(*delegation.IDecimalDelegationCommonStake)
			require.Positive(t, stake.Amount.Sign())
		}
		return append([]byte(nil), res.Ret...)
	}
	beforeStake := queryStake()
	beforeNative, found := dsc.ValidatorKeeper.GetDelegation(context(), account.GetAddress(), validatorAddress, cmdcfg.BaseDenom)
	require.True(t, found, "production hook must have created the corresponding native delegation")
	beforeAccount := dsc.EvmKeeper.GetBalance(context(), from)
	beforeContract := dsc.EvmKeeper.GetBalance(context(), target)
	hooks.armed = true
	failed, failedFee := deliver(target, amount, payload)
	require.True(t, hooks.injected, "fault must corrupt a real emitted event, not a fabricated deployment")
	require.Equal(t, actualTopic, hooks.injectedTopic)
	require.Error(t, hooks.lastError)
	require.Equal(t, evmtypes.ErrPostTxProcessing.Error(), failed.VmError)
	require.Empty(t, failed.Logs)
	require.Equal(t, beforeStake, queryStake(), "genuine delegation storage must roll back")
	afterNative, found := dsc.ValidatorKeeper.GetDelegation(context(), account.GetAddress(), validatorAddress, cmdcfg.BaseDenom)
	require.True(t, found)
	require.Equal(t, beforeNative, afterNative, "native delegation state must also roll back")
	require.Equal(t, beforeContract, dsc.EvmKeeper.GetBalance(context(), target), "contract value transfer must roll back")
	afterAccount := dsc.EvmKeeper.GetBalance(context(), from)
	require.Equal(t, failedFee, new(big.Int).Sub(beforeAccount, afterAccount), "failed transaction debit must equal exact receipt gas fee with no extra stake-value loss")
	t.Logf("snapshot height=%d actual delegation=%s injected hook error=%v fee debit=%s", block.Height-1, target, hooks.lastError, new(big.Int).Sub(beforeAccount, afterAccount))
	// Deliberately do not Commit: every mutation exists only in this disposable copy.
}

func probeMainnetAutoUnbond(t *testing.T, dsc *DSC, ctx sdk.Context, key *ecdsa.PrivateKey, target, validator common.Address) {
	t.Helper()
	contractABI, err := delegation.DelegationMetaData.GetAbi()
	require.NoError(t, err)
	queueLength := func(c sdk.Context) *big.Int {
		res, callErr := dsc.EvmKeeper.CallEVM(c, *contractABI, common.Address{}, target, false, "autoUnbondQueueLength")
		require.NoError(t, callErr, "snapshot runtime must implement the actual queue getter before any batch rehearsal")
		require.Empty(t, res.VmError)
		values, unpackErr := contractABI.Unpack("autoUnbondQueueLength", res.Ret)
		require.NoError(t, unpackErr)
		return values[0].(*big.Int)
	}
	before := queueLength(ctx)
	probe, _ := ctx.CacheContext()
	from := crypto.PubkeyToAddress(key.PublicKey)
	res, err := dsc.EvmKeeper.CallEVM(probe, *contractABI, common.Address{}, target, true, "autoUnbondEnqueue", validator, from, big.NewInt(100), common.Address{}, big.NewInt(0))
	require.NoError(t, err, "actual node-only zero-sender enqueue must succeed in discarded cache")
	require.Empty(t, res.VmError)
	require.Equal(t, new(big.Int).Add(before, big.NewInt(1)), queueLength(probe))
	require.Equal(t, before, queueLength(ctx), "discarded queue probe must not leak into parent state")
	payload, err := contractABI.Pack("autoUnbondEnqueue", validator, from, big.NewInt(100), common.Address{}, big.NewInt(0))
	require.NoError(t, err)
	response, _ := rehearsalDeliver(t, dsc, ctx, key, target, big.NewInt(0), payload)
	require.NotEmpty(t, response.VmError, "signed nonzero EOA must not have system enqueue authority")
	reason, err := abi.UnpackRevert(response.Ret)
	require.NoError(t, err)
	require.Equal(t, "Not authorized", reason)
	require.Equal(t, before, queueLength(ctx))
	t.Logf("actual queue getter=%s; zero-sender discarded-cache enqueue succeeded; signed EOA enqueue rejected: %s", before, response.VmError)
	if os.Getenv("DSC_MAINNET_AUTO_UNBOND_BATCH") != "1" {
		return
	}
	// Prepare a genuine EVM stake using the snapshot's historical hook behavior.
	// Do not pretend that strict activation alone migrates legacy native state.
	strictActivation := validatorkeeper.ReliabilityFixActivationHeights
	validatorkeeper.ReliabilityFixActivationHeights = map[string]int64{}
	legacyABI, err := delegationv7.DelegationMetaData.GetAbi()
	require.NoError(t, err)
	payload, err = legacyABI.Pack("delegateDEL", validator)
	require.NoError(t, err)
	amount := big.NewInt(100_000_000_000_000_000)
	baseline, _ := rehearsalDeliver(t, dsc, ctx, key, target, amount, payload)
	validatorkeeper.ReliabilityFixActivationHeights = strictActivation
	require.Empty(t, baseline.VmError, "genuine stake preparation must succeed under historical behavior")
	_, _, emitted := rehearsalEmittedStake(t, baseline, target)
	require.Equal(t, amount, emitted.Amount)
	coin, err := dsc.CoinKeeper.GetCoin(ctx, cmdcfg.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, common.HexToAddress(coin.DRC20Contract), emitted.Token)
	batch, _ := ctx.CacheContext()
	val := sdk.ValAddress(validator.Bytes())
	delegator := sdk.AccAddress(from.Bytes())
	// The native fixture deliberately isolates this validator's batch and mirrors
	// the genuine stake. Existing production delegations remain in the parent.
	store := batch.KVStore(dsc.GetKey(validatortypes.StoreKey))
	for _, existing := range dsc.ValidatorKeeper.GetValidatorDelegations(batch, val) {
		store.Delete(validatortypes.GetValidatorDelegatorDelegationKey(val, existing.GetDelegator(), existing.GetStake().GetID()))
	}
	var tracked []sdk.ValAddress
	dsc.ValidatorKeeper.IterateValidatorOfflineSince(batch, func(address sdk.ValAddress, _ time.Time) bool {
		tracked = append(tracked, append(sdk.ValAddress(nil), address...))
		return false
	})
	for _, address := range tracked {
		dsc.ValidatorKeeper.DeleteValidatorOfflineSince(batch, address)
	}
	valid := validatortypes.NewDelegation(delegator, val, validatortypes.NewStakeCoin(sdk.NewCoin(cmdcfg.BaseDenom, sdk.NewIntFromBigInt(amount))))
	corrupt := validatortypes.NewDelegation(delegator, val, validatortypes.NewStakeCoin(sdk.NewCoin("zzinvalidcoin", sdk.NewIntFromBigInt(amount))))
	dsc.ValidatorKeeper.SetDelegation(batch, valid)
	dsc.ValidatorKeeper.SetDelegation(batch, corrupt)
	ordered := dsc.ValidatorKeeper.GetValidatorDelegations(batch, val)
	require.Len(t, ordered, 2)
	require.Equal(t, cmdcfg.BaseDenom, ordered[0].GetStake().GetStake().Denom)
	params := dsc.ValidatorKeeper.GetParams(batch)
	params.AutoUnbondTimeout = time.Hour
	dsc.ValidatorKeeper.SetParams(batch, params)
	dsc.ValidatorKeeper.SetValidatorOfflineSince(batch, val, batch.BlockTime().Add(-2*time.Hour))
	batchBefore := queueLength(batch)
	prefixProbe, _ := batch.CacheContext()
	require.NoError(t, dsc.ValidatorKeeper.ExecuteAutoUnbondEnqueue(prefixProbe, ordered[0]))
	require.Equal(t, new(big.Int).Add(batchBefore, big.NewInt(1)), queueLength(prefixProbe), "first genuine enqueue must really write before the second failure")
	require.Error(t, dsc.ValidatorKeeper.ExecuteAutoUnbondEnqueue(prefixProbe, ordered[1]))
	dsc.ValidatorKeeper.ProcessAutoUnbond(batch)
	require.Equal(t, batchBefore, queueLength(batch), "failed partial batch must roll back the first actual contract write")
	_, remains := dsc.ValidatorKeeper.GetValidatorOfflineSince(batch, val)
	require.True(t, remains, "failed batch must retain its retry tracker")
	// Repair the controlled native input corruption, not the contract or registry.
	store.Delete(validatortypes.GetDelegationKey(delegator, val, corrupt.GetStake().GetID()))
	store.Delete(validatortypes.GetValidatorDelegatorDelegationKey(val, delegator, corrupt.GetStake().GetID()))
	dsc.ValidatorKeeper.ProcessAutoUnbond(batch)
	require.Equal(t, new(big.Int).Add(batchBefore, big.NewInt(1)), queueLength(batch))
	entryResponse, err := dsc.EvmKeeper.CallEVM(batch, *contractABI, common.Address{}, target, false, "getAutoUnbondEntry", batchBefore)
	require.NoError(t, err)
	entryValues, err := contractABI.Unpack("getAutoUnbondEntry", entryResponse.Ret)
	require.NoError(t, err)
	entry := abi.ConvertType(entryValues[0], new(delegation.IDecimalDelegationAutoUnbondEntry)).(*delegation.IDecimalDelegationAutoUnbondEntry)
	require.Equal(t, validator, entry.Validator)
	require.Equal(t, from, entry.Delegator)
	require.Equal(t, amount, entry.Amount)
	require.Equal(t, emitted.Token, entry.Token)
	require.Zero(t, entry.HoldTimestamp.Sign())
	_, remains = dsc.ValidatorKeeper.GetValidatorOfflineSince(batch, val)
	require.False(t, remains)
	dsc.ValidatorKeeper.ProcessAutoUnbond(batch)
	require.Equal(t, new(big.Int).Add(batchBefore, big.NewInt(1)), queueLength(batch), "successful retry must not enqueue a duplicate")
	require.Equal(t, before, queueLength(ctx), "discarded controlled batch must not leak into parent")
	t.Log("actual deployed auto-unbond partial write rolled back; tracker retained; corrupt native input removed; genuine stake enqueued exactly once on retry")
}

func rehearsalDeliver(t *testing.T, dsc *DSC, ctx sdk.Context, key *ecdsa.PrivateKey, to common.Address, amount *big.Int, data []byte) (*evmtypes.MsgEthereumTxResponse, *big.Int) {
	t.Helper()
	from := crypto.PubkeyToAddress(key.PublicKey)
	gasPrice := dsc.FeeKeeper.GetParams(ctx).MinGasPrice.Ceil().TruncateInt().BigInt()
	gasPrice.Add(gasPrice, big.NewInt(1))
	raw := ethtypes.NewTransaction(dsc.EvmKeeper.GetNonce(ctx, from), to, amount, 3_000_000, gasPrice, data)
	signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(75)), key)
	require.NoError(t, err)
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromEthereumTx(signed))
	tx, err := msg.BuildTx(dsc.GetTxConfig().NewTxBuilder(), cmdcfg.BaseDenom)
	require.NoError(t, err)
	gas, result, err := dsc.BaseApp.SimDeliver(dsc.GetTxConfig().TxEncoder(), tx)
	require.NoError(t, err, "real signature, fee and ante validation must succeed")
	require.Positive(t, gas.GasUsed)
	require.Len(t, result.MsgResponses, 1)
	response := &evmtypes.MsgEthereumTxResponse{}
	require.NoError(t, response.Unmarshal(result.MsgResponses[0].Value))
	// For this legacy EVM transaction, ante deducts gasLimit*gasPrice and
	// RefundGas returns (gasLimit-response.GasUsed)*gasPrice. Decimal's native
	// message commission schedule is not an additional charge on this path.
	chargedFee := new(big.Int).Mul(new(big.Int).SetUint64(response.GasUsed), gasPrice)
	require.Positive(t, chargedFee.Sign())
	return response, chargedFee
}
