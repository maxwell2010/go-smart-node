package systemcontracts_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nativevalidator "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const delDelegationDaemonFixtureEnv = "MCND_DEL_DELEGATION_FIXTURE_DIR"

// Export the deployed, unstaked Gateway before the signed fixture delegates.
// The live-process harness receives signed, chain-bound test transactions only.
func exportDELDelegationDaemonFixture(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile, validator nativevalidator.Validator, gateway abi.ABI) {
	t.Helper()
	dir := os.Getenv(delDelegationDaemonFixtureEnv)
	if dir == "" {
		return
	}
	require.True(t, filepath.IsAbs(dir))
	info, err := os.Lstat(dir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Zero(t, info.Mode().Perm()&0077)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, files)

	ctx, cdc := e.ctx(), e.app.AppCodec()
	genesis, err := e.app.ModuleManager.ExportGenesisForModules(ctx, cdc, nil)
	require.NoError(t, err)
	profile.SettlementOnly = true
	profile.EnableDELDelegation = true
	genesis[ownedapp.WithdrawalGenesisKey], err = json.Marshal(profile)
	require.NoError(t, err)
	var validators nativevalidator.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[nativevalidator.ModuleName], &validators))
	require.Empty(t, validators.Validators)
	validators.Validators = []nativevalidator.Validator{validator}
	genesis[nativevalidator.ModuleName] = cdc.MustMarshalJSON(&validators)
	require.NoError(t, ownedapp.ReconcileNativeGenesisAllocation(cdc, genesis))
	require.NoError(t, ownedapp.ValidateNativeGenesisAllocation(cdc, genesis))

	doc := cmttypes.GenesisDoc{ChainID: ctx.ChainID(), InitialHeight: ctx.BlockHeight(),
		GenesisTime: time.Now().UTC().Truncate(time.Second).Add(-time.Second), ConsensusParams: cmttypes.DefaultConsensusParams()}
	doc.AppState, err = json.Marshal(genesis)
	require.NoError(t, err)
	body, err := cmtjson.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	write := func(name string, data []byte) {
		t.Helper()
		file, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		require.NoError(t, err)
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		require.NoError(t, writeErr)
		require.NoError(t, closeErr)
	}
	write("genesis.json", body)
	e.saveGenesisKey(dir)

	wrapped := e.load("MintCandyWrappedDEL")
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).StringToBytes(validator.OperatorAddress)
	require.NoError(t, err)
	validatorAddress := common.BytesToAddress(operator)
	key := e.keys[3]
	actor := crypto.PubkeyToAddress(key.PublicKey)
	nonce := e.app.EVMKeeper.GetNonce(ctx, actor)
	amount := big.NewInt(3_000_000_000_000_000_000)
	topUp := big.NewInt(2_000_000_000_000_000_000)
	partial := big.NewInt(1_000_000_000_000_000_000)
	remainder := new(big.Int).Sub(new(big.Int).Add(amount, topUp), partial)
	gasPrice := big.NewInt(1_000_000_000_000)
	const gasLimit = 24_000_000
	signer := ethtypes.NewEIP155Signer(big.NewInt(990001))
	pack := func(method string, values ...interface{}) []byte {
		t.Helper()
		calldata, err := gateway.Pack(method, values...)
		require.NoError(t, err)
		return calldata
	}
	sign := func(offset uint64, value *big.Int, calldata []byte) map[string]string {
		t.Helper()
		tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce + offset, To: &profile.Gateway, Value: value, Gas: gasLimit, GasPrice: gasPrice, Data: calldata})
		signed, err := ethtypes.SignTx(tx, signer, key)
		require.NoError(t, err)
		raw, err := signed.MarshalBinary()
		require.NoError(t, err)
		return map[string]string{"raw": "0x" + hex.EncodeToString(raw), "hash": signed.Hash().Hex()}
	}
	wrappedCall := func(method string, values ...interface{}) string {
		t.Helper()
		calldata, err := wrapped.ABI.Pack(method, values...)
		require.NoError(t, err)
		return "0x" + hex.EncodeToString(calldata)
	}
	zero := new(big.Int)
	first, second := big.NewInt(0), big.NewInt(1)
	var idWord common.Hash
	stakeID := crypto.Keccak256Hash(validatorAddress.Bytes(), actor.Bytes(), profile.WrappedDEL.Bytes(), idWord.Bytes(), idWord.Bytes())
	expect, err := json.Marshal(map[string]interface{}{
		"chainId": ctx.ChainID(), "evmChainId": 990001, "gateway": profile.Gateway.Hex(), "wrapped": profile.WrappedDEL.Hex(),
		"actor": actor.Hex(), "actorNative": sdk.AccAddress(actor.Bytes()).String(), "wrappedNative": sdk.AccAddress(profile.WrappedDEL.Bytes()).String(),
		"validator": validatorAddress.Hex(), "amount": amount.String(), "topUpAmount": topUp.String(), "partialAmount": partial.String(),
		"gasPrice": gasPrice.String(), "gasLimit": gasLimit, "nonce": nonce,
		"stakeId": stakeID.Hex(), "frozenIds": []string{common.BigToHash(first).Hex(), common.BigToHash(second).Hex()},
		"stakeData":   "0x" + hex.EncodeToString(pack("getStake", validatorAddress, actor, profile.WrappedDEL)),
		"reserveData": "0x" + hex.EncodeToString(pack("getValidatorReserve", validatorAddress, profile.WrappedDEL)),
		"frozenData": []string{
			"0x" + hex.EncodeToString(pack("getFrozenStake", first)),
			"0x" + hex.EncodeToString(pack("getFrozenStake", second)),
		},
		"progressData":    "0x" + hex.EncodeToString(pack("frozenStakesMigrationProgress")),
		"supplyData":      wrappedCall("totalSupply"),
		"escrowData":      wrappedCall("balanceOf", profile.Gateway),
		"delegate":        sign(0, amount, pack("delegateDEL", validatorAddress)),
		"topUp":           sign(1, topUp, pack("delegateDEL", validatorAddress)),
		"partialWithdraw": sign(2, zero, pack("withdraw", validatorAddress, profile.WrappedDEL, partial)),
		"repeatComplete":  sign(3, zero, pack("complete", []*big.Int{first})),
		"withdraw":        sign(4, zero, pack("withdraw", validatorAddress, profile.WrappedDEL, remainder)),
	})
	require.NoError(t, err)
	write("expectations.json", expect)
}
