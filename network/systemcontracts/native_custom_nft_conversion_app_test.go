package systemcontracts_test

import (
	"encoding/json"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const (
	nativeCustomNFTPhaseEnv = "MCND_NATIVE_CUSTOM_NFT_PHASE"
	nativeCustomNFTMaxEnv   = "MCND_NATIVE_CUSTOM_NFT_MAX"
	convertedNFTGasPrefix   = "signed custom NFT conversion "
)

func nativeCustomNFTDimensions() (minted, selected uint32) {
	if os.Getenv(nativeCustomNFTMaxEnv) == "1" {
		return nfttypes.MaxIsolatedOriginUnits + 1, nfttypes.MaxIsolatedOriginUnits
	}
	return 3, 1
}

func TestOwnedAppNativeCustomNFTConversionAndWithdrawal(t *testing.T) {
	if phase := os.Getenv(nativeCustomNFTPhaseEnv); phase == "run" || phase == "restart" {
		runNativeCustomNFTConversion(t, os.Getenv(nativeConsensusDaemonFixtureEnv), phase)
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, tc := range []struct{ name, max string }{{"one selected unit", ""}, {"1024 selected units", "1"}} {
		t.Run(tc.name, func(t *testing.T) {
			checkNativeCustomNFTPhases(t, binary, tc.max)
		})
	}
}

func checkNativeCustomNFTPhases(t *testing.T, binary, maxFixture string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "native-custom-nft")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, phase := range []string{"seed", "run", "restart"} {
		if !t.Run(phase, func(t *testing.T) {
			filter, historical, custom := "^TestOwnedAppNativeCustomNFTConversionAndWithdrawal$", "", ""
			if phase == "seed" {
				filter, historical, custom = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$", "seed", "seed"
			}
			child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
			child.Env = append(os.Environ(), nativeCustomNFTPhaseEnv+"="+phase, nativeConsensusDaemonFixtureEnv+"="+dir,
				nativeCustomNFTMaxEnv+"="+maxFixture,
				nativeHistoricalConsensusPhaseEnv+"="+historical, nativeCustomCollateralGenesisPhaseEnv+"="+custom,
				nativeHistoricalMaxNFTPhaseEnv+"=", nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=",
				nativeConsensusUpgradeGovernanceEnv+"=", nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=",
				nativeConsensusNFTDowntimeEnv+"=", nativeConsensusFullUpgradeEnv+"=", signedNFTAdmissionPhaseEnv+"=",
				delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)
			if phase == "run" {
				_, measurement, found := strings.Cut(string(output), convertedNFTGasPrefix)
				require.True(t, found, "isolated custom NFT transaction did not report its gas")
				line, _, _ := strings.Cut(measurement, "\n")
				t.Log(convertedNFTGasPrefix + strings.TrimSpace(line))
			}
		}) {
			return
		}
	}
}

func runNativeCustomNFTConversion(t *testing.T, dir, phase string) {
	t.Helper()
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	var fixture struct {
		nativeHistoricalExpectations
		NftApprove   struct{ Raw string } `json:"nftApprove"`
		NftVault     common.Address       `json:"nftVault"`
		ReceiptToken string               `json:"receiptTokenNative"`
	}
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &fixture)
	require.Len(t, fixture.Validators, 3)
	database, err := dbm.NewDB("native-custom-nft", dbm.GoLevelDBBackend, dir)
	require.NoError(t, err)
	app := ownedapp.NewApp(log.NewNopLogger(), database, true, appOptions{
		flags.FlagHome: filepath.Join(dir, "home"), flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001),
	})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: fixture.nativeHistoricalExpectations, target: fixture.Validators[2]}
	var state ownedapp.GenesisState
	require.NoError(t, json.Unmarshal(doc.AppState, &state))
	var coins cointypes.GenesisState
	require.NoError(t, app.AppCodec().UnmarshalJSON(state[cointypes.ModuleName], &coins))
	require.Len(t, coins.Coins, 1)
	coin := coins.Coins[0]
	minted, quantity := nativeCustomNFTDimensions()
	unitReserve := sdkmath.NewIntWithDecimal(2, 18)
	amount := unitReserve.MulRaw(int64(quantity))
	positionID := common.Hash{31: 1}
	assertState := func(sold sdkmath.Int, burnt uint32) {
		t.Helper()
		ctx := l.snapshot()
		updated, err := app.NativeCoinKeeper.GetCoinByDRC(ctx, coin.DRC20Contract)
		require.NoError(t, err)
		require.True(t, coin.Volume.Sub(sold).Equal(updated.Volume))
		require.True(t, coin.Reserve.Sub(sold).Equal(updated.Reserve))
		coinAddress := sdk.AccAddress(common.HexToAddress(coin.DRC20Contract).Bytes())
		require.True(t, updated.Reserve.Equal(app.BankKeeper.GetBalance(ctx, coinAddress, identity.BaseDenom).Amount))
		require.True(t, sold.Equal(app.BankKeeper.GetBalance(ctx, sdk.AccAddress(fixture.NftVault.Bytes()), identity.BaseDenom).Amount))
		require.True(t, coins.Params.BaseVolume.Equal(l.supply()))
		origin, err := app.NativeNFTKeeper.GetToken(ctx, signedNFTNativeOriginID)
		require.NoError(t, err)
		require.Equal(t, minted, origin.Minted)
		require.Equal(t, burnt, origin.Burnt)
		require.Equal(t, coin.Denom, origin.Reserve.Denom)
		require.True(t, unitReserve.Equal(origin.Reserve.Amount))
		sibling, err := app.NativeNFTKeeper.GetSubToken(ctx, signedNFTNativeOriginID, minted)
		require.NoError(t, err)
		require.Equal(t, sdk.AccAddress(fixture.Actor.Bytes()).String(), sibling.Owner)
	}
	assertReceipt := func(owner common.Address) {
		t.Helper()
		lineage, err := app.NativeNFTKeeper.GetIsolatedLineage(l.snapshot(), fixture.NftVault.Bytes(), positionID.Bytes())
		require.NoError(t, err)
		require.Len(t, lineage.OriginSubTokenIDs, int(quantity))
		for i, id := range lineage.OriginSubTokenIDs {
			require.Equal(t, uint32(i+1), id)
		}
		require.Equal(t, fixture.ReceiptToken, lineage.ReceiptNativeTokenID)
		require.Equal(t, nfttypes.IsolatedReserveDEL, lineage.Reserve.Kind)
		require.Equal(t, common.BigToHash(amount.BigInt()).Bytes(), lineage.Reserve.Amount)
		unit, err := app.NativeNFTKeeper.GetSubToken(l.snapshot(), fixture.ReceiptToken, 1)
		require.NoError(t, err)
		require.Equal(t, sdk.AccAddress(owner.Bytes()).String(), unit.Owner)
	}
	if phase == "run" {
		params := doc.ConsensusParams.ToProto()
		init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
			ConsensusParams: &params, AppStateBytes: doc.AppState})
		require.NoError(t, err)
		validators, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
		require.NoError(t, err)
		l.set = cmttypes.NewValidatorSet(validators)
		l.commit(l.finalize(nil, nil))
		var approval ethtypes.Transaction
		require.NoError(t, approval.UnmarshalBinary(common.FromHex(fixture.NftApprove.Raw)))
		l.submitSignedEthereum(&approval)
		origin, err := app.NativeNFTKeeper.GetExternalNFTBinding(l.snapshot(), signedNFTNativeOriginID)
		require.NoError(t, err)
		validator, originContract := common.HexToAddress(l.target.EVM), common.BytesToAddress(origin.Contract)
		originID, units := common.BytesToHash(origin.TokenID).Big(), big.NewInt(int64(quantity))
		deadline := big.NewInt(l.snapshot().BlockTime().Add(time.Minute).Unix())
		const method = "delegateNFT1155WithConversion"
		block, result := l.finalizeSignedEthereum(l.signedGatewayTx(method, sdkmath.ZeroInt(), validator, originContract,
			originID, units, amount.AddRaw(1).BigInt(), deadline))
		require.NotEmpty(t, result.VmError)
		l.commit(block)
		assertState(sdkmath.ZeroInt(), 0)
		vault, err := app.NativeNFTKeeper.GetIsolatedVaultState(l.snapshot(), fixture.NftVault.Bytes())
		require.NoError(t, err)
		require.Equal(t, positionID.Bytes(), vault.NextPositionID)
		block, result = l.finalizeSignedEthereum(l.signedGatewayTx(method, sdkmath.ZeroInt(), validator, originContract,
			originID, units, amount.BigInt(), deadline))
		require.Empty(t, result.VmError)
		require.LessOrEqual(t, block.TxResults[0].GasUsed, int64(24_000_000))
		require.LessOrEqual(t, result.MaxUsedGas, uint64(24_000_000))
		l.commit(block)
		t.Logf("%sunits=%d sdk_gas=%d evm_peak_gas=%d budget=24000000", convertedNFTGasPrefix, quantity,
			block.TxResults[0].GasUsed, result.MaxUsedGas)
		assertState(amount, quantity)
		assertReceipt(fixture.Gateway)
		nft, err := app.NativeValidatorKeeper.GetEVMStakeBinding(l.snapshot(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT,
			fixture.Gateway.Bytes(), fixture.StakeID.Bytes())
		require.NoError(t, err)
		require.True(t, amount.Equal(nft.ReportedReserveAmount))
		l.signedGateway("withdrawNFT", sdkmath.ZeroInt(), validator, fixture.NftVault, positionID.Big())
		return
	}

	require.Equal(t, "restart", phase)
	require.Equal(t, int64(5), app.LastBlockHeight())
	assertState(amount, quantity)
	assertReceipt(fixture.Gateway)
	keeper := app.NativeValidatorKeeper
	row, err := keeper.GetEVMFrozenBinding(l.snapshot(), fixture.Gateway.Bytes(), make([]byte, common.HashLength))
	require.NoError(t, err)
	require.True(t, row.Active)
	vaultABI, err := contracts.CompiledABI("MintCandyNFTPositionVault")
	require.NoError(t, err)
	recipientKey, err := registryFixtureKey(4, true)
	require.NoError(t, err)
	recipient := common.BytesToAddress(recipientKey.PubKey().Address())
	require.NotEqual(t, fixture.Actor, recipient)
	redeem := func(target common.Address) *ethtypes.Transaction {
		t.Helper()
		data, err := vaultABI.Pack("redeemPosition", positionID.Big(), target)
		require.NoError(t, err)
		return l.signedEthereumCall(fixture.NftVault, sdkmath.ZeroInt(), data)
	}
	block, failed := l.finalizeSignedEthereum(redeem(recipient))
	require.NotEmpty(t, failed.VmError, "wallet cannot redeem before the Gateway releases the receipt")
	l.commit(block)
	assertState(amount, quantity)
	assertReceipt(fixture.Gateway)
	for range 9 {
		l.commit(l.finalize(nil, nil))
	}
	row, err = keeper.GetEVMFrozenBinding(l.snapshot(), fixture.Gateway.Bytes(), row.FrozenIndex)
	require.NoError(t, err)
	require.False(t, row.Active)
	assertState(amount, quantity)
	assertReceipt(fixture.Actor)

	balance := func(address common.Address) sdkmath.Int {
		return app.BankKeeper.GetBalance(l.snapshot(), sdk.AccAddress(address.Bytes()), identity.BaseDenom).Amount
	}
	beforeVault, beforeRecipient, beforeWrapper, beforeSupply := balance(fixture.NftVault), balance(recipient), balance(fixture.Wrapped), l.supply()
	block, failed = l.finalizeSignedEthereum(redeem(fixture.Wrapped))
	require.Contains(t, failed.VmError, "requires an address without executable code",
		"a successful EVM refund to a contract must roll back at native admission")
	l.commit(block)
	assertState(amount, quantity)
	assertReceipt(fixture.Actor)
	require.True(t, beforeWrapper.Equal(balance(fixture.Wrapped)))
	require.True(t, beforeRecipient.Equal(balance(recipient)))

	block, redeemed := l.finalizeSignedEthereum(redeem(recipient))
	require.Empty(t, redeemed.VmError)
	require.LessOrEqual(t, block.TxResults[0].GasUsed, int64(24_000_000))
	require.LessOrEqual(t, redeemed.MaxUsedGas, uint64(24_000_000))
	l.commit(block)
	t.Logf("signed NFT redemption units=%d sdk_gas=%d evm_peak_gas=%d budget=24000000",
		quantity, block.TxResults[0].GasUsed, redeemed.MaxUsedGas)
	assertRedeemed := func() {
		t.Helper()
		require.True(t, beforeVault.Sub(amount).Equal(balance(fixture.NftVault)))
		require.True(t, beforeRecipient.Add(amount).Equal(balance(recipient)))
		require.True(t, beforeSupply.Equal(l.supply()))
		_, err := app.NativeNFTKeeper.GetIsolatedLineage(l.snapshot(), fixture.NftVault.Bytes(), positionID.Bytes())
		require.ErrorIs(t, err, nftkeeper.ErrNotFound)
		_, err = app.NativeNFTKeeper.GetToken(l.snapshot(), fixture.ReceiptToken)
		require.ErrorIs(t, err, nftkeeper.ErrNotFound)
		origin, err := app.NativeNFTKeeper.GetToken(l.snapshot(), signedNFTNativeOriginID)
		require.NoError(t, err)
		require.Equal(t, quantity, origin.Burnt, "redemption does not revive consumed origin units")
	}
	assertRedeemed()
	block, repeated := l.finalizeSignedEthereum(redeem(recipient))
	require.NotEmpty(t, repeated.VmError, "a redeemed vault position cannot pay twice")
	l.commit(block)
	assertRedeemed()
}
