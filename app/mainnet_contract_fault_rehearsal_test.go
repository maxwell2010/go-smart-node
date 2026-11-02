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

	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	"bitbucket.org/decimalteam/go-smart-node/contracts"
	"bitbucket.org/decimalteam/go-smart-node/contracts/delegationv7"
	cointypes "bitbucket.org/decimalteam/go-smart-node/x/coin/types"
	validatorkeeper "bitbucket.org/decimalteam/go-smart-node/x/validator/keeper"
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
	inner     evmtypes.EvmHooks
	target    common.Address
	armed     bool
	injected  bool
	lastError error
}

func (h *receiptCorruptionHooks) PostTxProcessing(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	if h.armed {
		for _, log := range receipt.Logs {
			if log.Address == h.target && len(log.Topics) > 0 && len(log.Data) > 0 {
				log.Data = nil
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
	// Reconstruct the identical keeper only to install a test-only hook wrapper;
	// all callers retain the same field pointer, stores and production dependencies.
	dsc.EvmKeeper = *evmkeeper.NewKeeper(dsc.AppCodec(), dsc.GetKey(evmtypes.StoreKey), dsc.GetTKey(evmtypes.TransientKey), authtypes.NewModuleAddress(govtypes.ModuleName), dsc.AccountKeeper, dsc.BankKeeper, &dsc.ValidatorKeeper, dsc.FeeKeeper, nil, evmgeth.NewEVM, "", dsc.GetSubspace(evmtypes.ModuleName))
	dsc.EvmKeeper.WithChainID(ctx)
	hooks := &receiptCorruptionHooks{inner: evmkeeper.NewMultiEvmHooks(dsc.CoinKeeper.Hooks(), dsc.NFTKeeper.Hooks(), dsc.ValidatorKeeper.Hooks()), target: target}
	dsc.EvmKeeper.SetHooks(hooks)
	deliver := func(to common.Address, amount *big.Int, data []byte) *evmtypes.MsgEthereumTxResponse {
		return rehearsalDeliver(t, dsc, context(), key, to, amount, data)
	}
	transfer := deliver(common.HexToAddress("0x1234567890123456789012345678901234567890"), big.NewInt(100), nil)
	require.Empty(t, transfer.VmError, "real snapshot registry must permit ordinary native EVM transfer")
	t.Log("actual snapshot native EVM transfer passed signature, outer ante, fee deduction and production hooks")
	payload, err := legacyABI.Pack("delegateDEL", validator)
	require.NoError(t, err)
	amount := big.NewInt(100_000_000_000_000_000)
	baseline := deliver(target, amount, payload)
	if !dsc.ValidatorKeeper.LegacyDelegationEventsEnabled(context()) {
		require.Equal(t, evmtypes.ErrPostTxProcessing.Error(), baseline.VmError)
		require.Error(t, hooks.lastError)
		require.Contains(t, hooks.lastError.Error(), "financial contract emitted unknown event")
		t.Logf("pre-recovery activation incompatibility confirmed: %v", hooks.lastError)
		if os.Getenv("DSC_REQUIRE_MAINNET_CONTRACT_FAULT_REHEARSAL") == "1" {
			t.Fatal("fault rehearsal gate requires an actual post-recovery snapshot with committed legacy-event migration; do not set the flag manually")
		}
		return
	}
	require.Empty(t, baseline.VmError, "baseline must execute the genuine delegation runtime and hooks: %v", hooks.lastError)
	require.NotEmpty(t, baseline.Logs)
	var emitted delegationv7.DelegationStakeUpdated
	foundStakeEvent := false
	for _, log := range baseline.Logs {
		ethLog := log.ToEthereum()
		if ethLog.Address == target && len(ethLog.Topics) > 0 && ethLog.Topics[0] == legacyABI.Events["StakeUpdated"].ID {
			require.NoError(t, contracts.UnpackLog(legacyABI, &emitted, "StakeUpdated", ethLog))
			foundStakeEvent = true
			break
		}
	}
	require.True(t, foundStakeEvent, "baseline must emit a genuine legacy StakeUpdated event")
	require.Positive(t, emitted.Stake.Amount.Sign())
	queryStake := func() []byte {
		res, callErr := dsc.EvmKeeper.CallEVM(context(), *legacyABI, from, target, false, "getStake", validator, from, emitted.Stake.Token)
		require.NoError(t, callErr)
		require.Empty(t, res.VmError)
		values, unpackErr := legacyABI.Unpack("getStake", res.Ret)
		require.NoError(t, unpackErr)
		stake := abi.ConvertType(values[0], new(delegationv7.IDecimalDelegationCommonStake)).(*delegationv7.IDecimalDelegationCommonStake)
		require.Positive(t, stake.Amount.Sign(), "the queried real contract stake must exist")
		return append([]byte(nil), res.Ret...)
	}
	beforeStake := queryStake()
	beforeNative, found := dsc.ValidatorKeeper.GetDelegation(context(), account.GetAddress(), validatorAddress, cmdcfg.BaseDenom)
	require.True(t, found, "production hook must have created the corresponding native delegation")
	beforeAccount := dsc.EvmKeeper.GetBalance(context(), from)
	beforeContract := dsc.EvmKeeper.GetBalance(context(), target)
	hooks.armed = true
	failed := deliver(target, amount, payload)
	require.True(t, hooks.injected, "fault must corrupt a real emitted event, not a fabricated deployment")
	require.Error(t, hooks.lastError)
	require.Equal(t, evmtypes.ErrPostTxProcessing.Error(), failed.VmError)
	require.Empty(t, failed.Logs)
	require.Equal(t, beforeStake, queryStake(), "genuine delegation storage must roll back")
	afterNative, found := dsc.ValidatorKeeper.GetDelegation(context(), account.GetAddress(), validatorAddress, cmdcfg.BaseDenom)
	require.True(t, found)
	require.Equal(t, beforeNative, afterNative, "native delegation state must also roll back")
	require.Equal(t, beforeContract, dsc.EvmKeeper.GetBalance(context(), target), "contract value transfer must roll back")
	afterAccount := dsc.EvmKeeper.GetBalance(context(), from)
	require.Positive(t, new(big.Int).Sub(beforeAccount, afterAccount).Sign(), "failed transaction must still pay actual execution fees")
	t.Logf("snapshot height=%d actual delegation=%s injected hook error=%v fee debit=%s", block.Height-1, target, hooks.lastError, new(big.Int).Sub(beforeAccount, afterAccount))
	// Deliberately do not Commit: every mutation exists only in this disposable copy.
}

func rehearsalDeliver(t *testing.T, dsc *DSC, ctx sdk.Context, key *ecdsa.PrivateKey, to common.Address, amount *big.Int, data []byte) *evmtypes.MsgEthereumTxResponse {
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
	return response
}
