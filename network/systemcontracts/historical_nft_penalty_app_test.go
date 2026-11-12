package systemcontracts_test

import (
	"errors"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const (
	activeHistoricalNFTPenaltyMode   = "active-historical-nft-penalty"
	activeHistoricalNFTPenaltyPassed = "owned EVM historical NFT burn preserves late reserve and custody across rollback and retry"
)

func TestOwnedAppActiveHistoricalNFTPenalty(t *testing.T) {
	testSignedDELFixture(t, activeHistoricalNFTPenaltyMode, activeHistoricalNFTPenaltyPassed)
}

func runSignedActiveHistoricalNFTPenalty(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	master contracts.Artifact, masterAddress common.Address, early, late settlement.NFTIsolation,
	direct, unitReserve sdkmath.Int, infraction int64,
) {
	t.Helper()
	e := f.env
	nft, validators := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int { return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount }
	getRow := func(ctx sdk.Context, kind validatortypes.EVMStakeBindingKind, id common.Hash) validatortypes.EVMStakeBinding {
		t.Helper()
		row, err := validators.GetEVMStakeBinding(ctx, kind, f.target[:], id[:])
		require.NoError(t, err)
		return row
	}
	getLineage := func(ctx sdk.Context, snapshot nftview.PositionSnapshot) nfttypes.IsolatedPositionLineage {
		t.Helper()
		lineage, err := nft.GetIsolatedLineage(ctx, snapshot.Asset.Contract[:], snapshot.Asset.TokenID[:])
		require.NoError(t, err)
		return lineage
	}
	getPosition := func(ctx sdk.Context, id common.Hash) nftview.PositionSnapshot {
		t.Helper()
		position, err := reader.Position(ctx, id)
		require.NoError(t, err)
		return position
	}
	const coinKind = validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN
	const nftKind = validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT
	initialSupply := supply(ctx())
	initialNFTReserve := unitReserve.MulRaw(2)
	const percent uint16 = 5
	loss := unitReserve.QuoRaw(20)
	require.True(t, sdkmath.NewIntWithDecimal(1, 17).Equal(loss))
	positions := []struct {
		proof    settlement.NFTIsolation
		row      validatortypes.EVMStakeBinding
		lineage  nfttypes.IsolatedPositionLineage
		snapshot nftview.PositionSnapshot
		eligible bool
		loss     sdkmath.Int
	}{{proof: early, eligible: true, loss: loss}, {proof: late, loss: sdkmath.ZeroInt()}}
	for i := range positions {
		p := &positions[i]
		p.row = getRow(ctx(), nftKind, p.proof.StakeID)
		p.snapshot = getPosition(ctx(), p.proof.Position.Asset.TokenID)
		p.lineage = getLineage(ctx(), p.snapshot)
		require.Equal(t, f.target, p.snapshot.Owner)
		require.Equal(t, p.eligible, p.row.CreatedHeight < infraction)
		require.True(t, unitReserve.Equal(p.row.ReportedReserveAmount))
	}
	require.Equal(t, infraction, positions[1].row.CreatedHeight)
	require.Equal(t, early.Companion.StakeID, late.Companion.StakeID)
	coinBefore := getRow(ctx(), coinKind, early.Companion.StakeID)
	assertCommitted := func(applied bool) {
		t.Helper()
		cursor, totalLoss := int64(0), sdkmath.ZeroInt()
		if applied {
			cursor, totalLoss = 1, loss
		}
		wantCursor := common.BigToHash(big.NewInt(cursor))
		require.Equal(t, cursor, f.read(master, masterAddress, "getPenaltyCount", f.validator).(*big.Int).Int64())
		require.True(t, initialSupply.Sub(totalLoss).Equal(supply(ctx())))
		for _, p := range positions {
			want := unitReserve
			if applied {
				want = want.Sub(p.loss)
			}
			row := getRow(ctx(), nftKind, p.proof.StakeID)
			require.Equal(t, p.row.NFTPosition, row.NFTPosition)
			require.Equal(t, p.row.CreatedHeight, row.CreatedHeight)
			require.True(t, row.Active)
			require.True(t, want.Equal(row.ReportedReserveAmount))
			require.Equal(t, wantCursor, common.BytesToHash(row.PenaltyCursor))
			require.Equal(t, cursor, f.read(f.gateway, f.target, "getNFTStakePenaltyIndex", p.proof.StakeID).(*big.Int).Int64())
			actual := getPosition(ctx(), p.snapshot.Asset.TokenID)
			expected := p.snapshot
			expected.ReserveAmount = common.BigToHash(want.BigInt())
			expected.DeclaredCurrencyReserve = common.BigToHash(new(big.Int).Sub(expected.DeclaredCurrencyReserve.Big(), totalLoss.BigInt()))
			expected.HeldCurrencyReserve = common.BigToHash(new(big.Int).Sub(expected.HeldCurrencyReserve.Big(), totalLoss.BigInt()))
			require.Equal(t, expected, actual, "NFT receipt custody, origin, and reserve identity must survive")
			expectedLineage := p.lineage
			expectedLineage.Reserve.Amount = common.BigToHash(want.BigInt()).Bytes()
			require.Equal(t, expectedLineage, getLineage(ctx(), actual))
		}
		coin := getRow(ctx(), coinKind, early.Companion.StakeID)
		require.True(t, coinBefore.RawAmount.Sub(totalLoss).Equal(coin.RawAmount))
		require.True(t, coinBefore.RawNftBackedAmount.Sub(totalLoss).Equal(coin.RawNftBackedAmount))
		require.Equal(t, coinBefore.PenaltyCursor, coin.PenaltyCursor)
		f.backing(direct)
		f.activeReserve(direct.Add(initialNFTReserve).Sub(totalLoss))
		require.Empty(t, f.native().Undelegations)
	}
	assertCommitted(false)

	var routeLogs []int
	e.app.EVMKeeper.SetInternalCommitGuard(func(child sdk.Context, msg core.Message, logs []*ethtypes.Log) error {
		if executor.AuthorizeMasterHistoricalPenalty(child, msg) {
			return nil
		}
		if executor.AuthorizeActiveHistoricalNFTPenalty(child, msg) {
			require.False(t, executor.AuthorizeActiveNFTPenalty(child, msg))
			routeLogs = append(routeLogs, len(logs))
			return nil
		}
		return errors.New("test-only internal EVM call lacks exact historical NFT penalty authorization")
	})
	appendAndProject := func(projectErr error) error {
		t.Helper()
		return executor.AppendMasterHistoricalPenalty(ctx(), f.validator, infraction, supply, func(child sdk.Context, announcement settlement.MasterPenaltyProof) error {
			require.Equal(t, percent, announcement.Percentage)
			for _, p := range positions {
				plan, err := validators.PlanActiveNFTHistoricalPenalty(child, p.row, infraction)
				require.NoError(t, err)
				require.Equal(t, p.eligible, plan.Eligible)
				require.True(t, p.loss.Equal(plan.Loss))
				err = executor.ApplyActiveHistoricalNFTPenalty(child, p.row, announcement, plan.Eligible, supply, func(project sdk.Context, proof settlement.ActiveNFTPenaltyProof) error {
					require.Equal(t, plan.Eligible, proof.Eligible)
					require.True(t, plan.Loss.Equal(proof.Burned))
					require.True(t, plan.Remaining.Equal(proof.RemainingReserve))
					require.Equal(t, announcement.BeforeCursor, proof.BeforeCursor)
					require.Equal(t, announcement.AfterCursor, proof.AfterCursor)
					coin := proof.Companion
					require.True(t, direct.Equal(coin.Remaining))
					return validators.ApplyGatewayActiveHistoricalNFTPenalty(project, nft, validatorkeeper.GatewayNFTPenalty{
						Before: validatorkeeper.GatewayNFTPosition{
							Companion: validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: coin.Beneficiary,
								Validator: coin.Validator, StakeID: coin.StakeID, Amount: coin.Remaining, NFTBacked: coin.NFTBacked, PenaltyCursor: proof.CompanionCursor},
							StakeID: p.proof.StakeID, Lineage: p.lineage,
						},
						BeforeCursor: proof.BeforeCursor, AfterCursor: proof.AfterCursor, RemainingReserve: proof.RemainingReserve,
					}, infraction, plan.Eligible)
				})
				if err != nil {
					return err
				}
			}
			require.True(t, initialSupply.Sub(loss).Equal(supply(child)))
			return projectErr
		})
	}
	injected := errors.New("reject historical NFT projection after both positions")
	require.ErrorIs(t, appendAndProject(injected), injected)
	require.Len(t, routeLogs, 2)
	require.Positive(t, routeLogs[0])
	require.Zero(t, routeLogs[1], "late NFT cursor-only settlement emits no financial logs")
	assertCommitted(false)
	for _, p := range positions {
		require.Equal(t, p.row, getRow(ctx(), nftKind, p.proof.StakeID))
	}
	require.Equal(t, coinBefore, getRow(ctx(), coinKind, early.Companion.StakeID))

	require.NoError(t, appendAndProject(nil))
	require.Len(t, routeLogs, 4)
	require.Positive(t, routeLogs[2])
	require.Zero(t, routeLogs[3])
	assertCommitted(true)
	t.Log(activeHistoricalNFTPenaltyPassed)
}
