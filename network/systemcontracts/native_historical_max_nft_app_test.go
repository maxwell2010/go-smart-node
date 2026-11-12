package systemcontracts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const nativeHistoricalMaxNFTPhaseEnv = "MCND_NATIVE_HISTORICAL_MAX_NFT_PHASE"
const historicalMaxNFTGasPrefix = "typed historical penalty NFT units=1024 DEL tranches=127 sdk_gas="

func TestOwnedAppNativeHistoricalMaxNFTAndDEL(t *testing.T) {
	if os.Getenv(nativeHistoricalMaxNFTPhaseEnv) == "run" {
		runNativeHistoricalMaxNFTAndDEL(t, os.Getenv(nativeConsensusDaemonFixtureEnv))
		return
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	dir := filepath.Join(t.TempDir(), "native-historical-max-nft")
	require.NoError(t, os.Mkdir(dir, 0700))
	for _, phase := range []string{"seed", "run"} {
		filter := "^TestOwnedAppNativeHistoricalMaxNFTAndDEL$"
		historicalPhase := ""
		if phase == "seed" {
			filter = "^TestOwnedAppSignedRegistryBootstrap$/^authenticated_isolated_position_reader$"
			historicalPhase = "seed"
		}
		child := exec.CommandContext(t.Context(), binary, "-test.v", "-test.timeout=3m", "-test.run="+filter)
		child.Env = append(os.Environ(), nativeHistoricalMaxNFTPhaseEnv+"="+phase, nativeHistoricalConsensusPhaseEnv+"="+historicalPhase,
			nativeConsensusDaemonFixtureEnv+"="+dir, nativeConsensusFeeRewardsEnv+"=", nativeConsensusIBCV1Env+"=",
			nativeConsensusUpgradeGovernanceEnv+"=", nativeConsensusJailRecoveryEnv+"=", nativeConsensusHeldDowntimeEnv+"=",
			nativeConsensusNFTDowntimeEnv+"=", signedNFTAdmissionPhaseEnv+"=", delLifecycleFixtureEnv+"=", withdrawalGenesisFixtureEnv+"=")
		output, err := child.CombinedOutput()
		require.NoError(t, err, "%s phase: %s", phase, output)
		if phase == "run" {
			_, measurement, found := strings.Cut(string(output), historicalMaxNFTGasPrefix)
			require.True(t, found, "isolated fixture did not report its historical work measurement")
			line, _, _ := strings.Cut(measurement, "\n")
			t.Log(historicalMaxNFTGasPrefix + strings.TrimSpace(line))
		}
	}
}

func runNativeHistoricalMaxNFTAndDEL(t *testing.T, dir string) {
	t.Helper()
	doc, err := cmttypes.GenesisDocFromFile(filepath.Join(dir, "genesis.json"))
	require.NoError(t, err)
	var fixture struct {
		nativeHistoricalExpectations
		DELStakeID  common.Hash          `json:"delStakeId"`
		NFTApprove  struct{ Raw string } `json:"nftApprove"`
		NFTDelegate struct{ Raw string } `json:"nftDelegate"`
	}
	readNativeHistoricalJSON(t, filepath.Join(dir, "expectations.json"), &fixture)
	require.Len(t, fixture.Validators, 3)
	require.NotEqual(t, fixture.StakeID, fixture.DELStakeID)
	app := ownedapp.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{
		flags.FlagHome: filepath.Join(dir, "max-nft-home"), flags.FlagChainID: doc.ChainID, srvflags.EVMChainID: uint64(990001),
	})
	t.Cleanup(func() { require.NoError(t, app.Close()) })
	params := doc.ConsensusParams.ToProto()
	init, err := app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
		ConsensusParams: &params, AppStateBytes: doc.AppState})
	require.NoError(t, err)
	set, err := cmttypes.PB2TM.ValidatorUpdates(init.Validators)
	require.NoError(t, err)
	l := nativeHistoricalLifecycle{t: t, app: app, doc: doc, expect: fixture.nativeHistoricalExpectations,
		target: fixture.Validators[2], set: cmttypes.NewValidatorSet(set)}
	l.commit(l.finalize(nil, nil))
	for _, raw := range []string{fixture.NFTApprove.Raw, fixture.NFTDelegate.Raw} {
		var signed ethtypes.Transaction
		require.NoError(t, signed.UnmarshalBinary(common.FromHex(raw)))
		l.submitSignedEthereum(&signed)
	}
	validator := common.HexToAddress(l.target.EVM)
	unit := sdkmath.NewIntWithDecimal(1, 18)
	for range 126 {
		l.signedGateway("delegateDEL", unit, validator)
	}

	keeper, nft := app.NativeValidatorKeeper, app.NativeNFTKeeper
	ctx := l.snapshot()
	const coinKind = validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	const nftKind = validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT
	rows, next, err := keeper.GetActiveStakePenaltyPage(ctx, validator[:], nil, 2)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Nil(t, next)
	coin, err := keeper.GetEVMStakeBinding(ctx, coinKind, fixture.Gateway[:], fixture.DELStakeID[:])
	require.NoError(t, err)
	receipt, err := keeper.GetEVMStakeBinding(ctx, nftKind, fixture.Gateway[:], fixture.StakeID[:])
	require.NoError(t, err)
	require.Len(t, receipt.NFTPosition.Origin.NativeSubTokenIDs, int(nfttypes.MaxIsolatedOriginUnits))
	require.Equal(t, coin.StakeID, receipt.CoinCompanionStakeID)
	require.True(t, unit.MulRaw(2048).Equal(receipt.ReportedReserveAmount))
	require.True(t, receipt.ReportedReserveAmount.Equal(coin.RawNftBackedAmount))
	require.True(t, unit.MulRaw(128).Equal(coin.RawAmount.Sub(coin.RawNftBackedAmount)))
	lots, err := keeper.GetActiveDELProvenance(ctx, coin)
	require.NoError(t, err)
	require.Len(t, lots, 127)

	infraction := ctx.BlockHeight() - 1
	require.Less(t, receipt.CreatedHeight, infraction)
	history, err := keeper.GetHistoricalInfo(ctx, infraction)
	require.NoError(t, err)
	observed, err := validatorkeeper.ConsensusSet(history.Valset)
	require.NoError(t, err)
	_, target := observed.GetByAddress(common.FromHex(l.target.Consensus))
	require.NotNil(t, target)
	directLoss := unit.MulRaw(126).QuoRaw(20)
	nftLoss := receipt.ReportedReserveAmount.QuoRaw(20)
	totalLoss := directLoss.Add(nftLoss)
	beforeSupply := l.supply()
	vault := sdk.AccAddress(receipt.TokenContract)
	wrapper := sdk.AccAddress(fixture.Wrapped[:])
	vaultBalance := app.BankKeeper.GetBalance(ctx, vault, identity.BaseDenom).Amount
	wrapperBalance := app.BankKeeper.GetBalance(ctx, wrapper, identity.BaseDenom).Amount

	// This is the authenticated ABCI metadata boundary. No vote signatures or
	// raw evidence objects are produced by the test.
	block := l.finalize(nil, []abci.Misbehavior{{Type: abci.MisbehaviorType_DUPLICATE_VOTE,
		Validator: abci.Validator{Address: target.Address, Power: target.VotingPower}, Height: infraction,
		Time: history.Header.Time, TotalVotingPower: observed.TotalVotingPower()}})
	var applied []abci.Event
	for _, event := range block.Events {
		require.NotEqual(t, "decimal_native_duplicate_vote_deferred", event.Type)
		if event.Type == "decimal_native_duplicate_vote_applied" {
			applied = append(applied, event)
		}
	}
	require.Len(t, applied, 1)
	attrs := make(map[string]string)
	for _, attr := range applied[0].Attributes {
		attrs[attr.Key] = attr.Value
	}
	require.Equal(t, totalLoss.String(), attrs["burned"])
	gas, err := strconv.ParseUint(attrs["gas_used"], 10, 64)
	require.NoError(t, err)
	require.Positive(t, gas)
	require.LessOrEqual(t, gas, uint64(24_000_000))
	l.commit(block)
	l.assertMasterPenaltyState(true)
	require.True(t, beforeSupply.Sub(totalLoss).Equal(l.supply()))
	ctx = l.snapshot()
	require.True(t, vaultBalance.Sub(nftLoss).Equal(app.BankKeeper.GetBalance(ctx, vault, identity.BaseDenom).Amount))
	require.True(t, wrapperBalance.Sub(directLoss).Equal(app.BankKeeper.GetBalance(ctx, wrapper, identity.BaseDenom).Amount))
	afterCoin, err := keeper.GetEVMStakeBinding(ctx, coinKind, fixture.Gateway[:], fixture.DELStakeID[:])
	require.NoError(t, err)
	afterNFT, err := keeper.GetEVMStakeBinding(ctx, nftKind, fixture.Gateway[:], fixture.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, common.Hash{31: 1}.Bytes(), afterCoin.PenaltyCursor)
	require.Equal(t, afterCoin.PenaltyCursor, afterNFT.PenaltyCursor)
	require.Equal(t, receipt.NFTPosition, afterNFT.NFTPosition)
	require.True(t, receipt.ReportedReserveAmount.Sub(nftLoss).Equal(afterNFT.ReportedReserveAmount))
	require.True(t, afterNFT.ReportedReserveAmount.Equal(afterCoin.RawNftBackedAmount))
	require.True(t, coin.RawAmount.Sub(totalLoss).Equal(afterCoin.RawAmount))
	newLots, err := keeper.GetActiveDELProvenance(ctx, afterCoin)
	require.NoError(t, err)
	require.Len(t, newLots, len(lots))
	for i, lot := range lots {
		want := lot.Amount
		if lot.FundedHeight < infraction {
			want = want.Sub(want.QuoRaw(20))
		}
		require.True(t, want.Equal(newLots[i].Amount), "tranche at height %d", lot.FundedHeight)
		require.Equal(t, lot.FundedHeight, newLots[i].FundedHeight)
		require.Equal(t, lot.FundedAt, newLots[i].FundedAt)
	}
	unitReceipt, err := nft.GetSubToken(ctx, afterNFT.NativeTarget.ID, 1)
	require.NoError(t, err)
	require.Equal(t, sdk.AccAddress(fixture.Gateway[:]).String(), unitReceipt.Owner)
	t.Logf("%s%d budget=24000000", historicalMaxNFTGasPrefix, gas)
}
