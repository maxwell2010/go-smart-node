package systemcontracts_test

import (
	"errors"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const (
	frozenHistoricalNFTPenaltyMode   = "frozen-historical-nft-penalty"
	frozenHistoricalNFTPenaltyPassed = "owned EVM historical frozen NFT preserves mature custody and rolls back exact queue changes"
)

func TestOwnedAppFrozenHistoricalNFTPenalty(t *testing.T) {
	testSignedDELFixture(t, frozenHistoricalNFTPenaltyMode, frozenHistoricalNFTPenaltyPassed)
}

func runSignedFrozenHistoricalNFTPenalty(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	master contracts.Artifact, masterAddress common.Address, early, late frozenNFTPenaltyClaim, direct sdkmath.Int, infraction int64,
) {
	t.Helper()
	e := f.env
	nft, keeper := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	// Signed receipts are explicitly projected by this fixture without the
	// automatic settlement controller. Real isolated blocks advance maturity
	// while both receipts remain in Gateway custody.
	for !e.ctx().BlockTime().After(early.out.Deadline) {
		e.advance()
	}
	require.True(t, e.ctx().BlockTime().Before(late.out.Deadline))
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int { return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount }
	getRow := func(ctx sdk.Context, index common.Hash) validatortypes.EVMFrozenBinding {
		t.Helper()
		row, err := keeper.GetEVMFrozenBinding(ctx, f.target[:], index[:])
		require.NoError(t, err)
		return row
	}
	getPosition := func(ctx sdk.Context, id common.Hash) nftview.PositionSnapshot {
		t.Helper()
		position, err := reader.Position(ctx, id)
		require.NoError(t, err)
		return position
	}
	getQueue := func(ctx sdk.Context) validatortypes.Undelegation {
		t.Helper()
		queue, err := keeper.GetUndelegation(ctx, f.beneficiary, f.operator)
		require.NoError(t, err)
		require.Len(t, queue.Entries, 2)
		return queue
	}
	initialSupply := supply(ctx())
	initialQueue := getQueue(ctx())
	loss := early.row.ReportedReserveAmount.QuoRaw(20)
	require.True(t, sdkmath.NewIntWithDecimal(1, 17).Equal(loss))
	claims := []struct {
		claim       frozenNFTPenaltyClaim
		snapshot    nftview.PositionSnapshot
		frozen      gatewayFrozenWithdrawal
		entryHeight int64
		eligible    bool
		loss        sdkmath.Int
	}{{claim: early, eligible: true, loss: loss}, {claim: late, loss: sdkmath.ZeroInt()}}
	for i := range claims {
		c := &claims[i]
		var err error
		c.entryHeight, err = keeper.GatewayFrozenNFTStakeEntryHeight(ctx(), c.claim.row)
		require.NoError(t, err)
		require.Equal(t, c.eligible, c.entryHeight < infraction)
		require.GreaterOrEqual(t, c.claim.row.CreatedHeight, infraction)
		c.snapshot = getPosition(ctx(), c.claim.out.Position.Asset.TokenID)
		require.Equal(t, f.target, c.snapshot.Owner)
		raw := f.read(f.gateway, f.target, "getFrozenStake", c.claim.out.FrozenIndex.Big())
		c.frozen = *abi.ConvertType(raw, new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal)
		require.Equal(t, uint8(1), c.frozen.FreezeStatus)
		require.Equal(t, int64(1), c.frozen.Stake.Amount.Int64())
	}
	require.Equal(t, infraction, claims[1].entryHeight)
	assertCommitted := func(applied bool) {
		t.Helper()
		cursor, totalLoss := int64(0), sdkmath.ZeroInt()
		if applied {
			cursor, totalLoss = 1, loss
		}
		queue := getQueue(ctx())
		require.Equal(t, cursor, f.read(master, masterAddress, "getPenaltyCount", f.validator).(*big.Int).Int64())
		require.True(t, initialSupply.Sub(totalLoss).Equal(supply(ctx())))
		for i, c := range claims {
			want := c.claim.row.ReportedReserveAmount
			if applied {
				want = want.Sub(c.loss)
			}
			index := c.claim.out.FrozenIndex
			row := getRow(ctx(), index)
			require.True(t, row.Active)
			require.True(t, sdkmath.OneInt().Equal(row.RawAmount))
			require.True(t, want.Equal(row.ReportedReserveAmount))
			require.Equal(t, c.claim.row.NFTPosition, row.NFTPosition)
			require.Equal(t, c.claim.row.CompletionTimestamp, row.CompletionTimestamp)
			entry := queue.Entries[i]
			require.Equal(t, c.claim.row.CreatedHeight, entry.CreationHeight)
			require.True(t, c.claim.out.Deadline.Equal(entry.CompletionTime))
			require.True(t, want.Equal(entry.Stake.Stake.Amount))
			hash, err := validatortypes.NativeQueueEntryHash(1, f.beneficiary, f.operator, "", &entry)
			require.NoError(t, err)
			require.Equal(t, hash, row.NativeEntryHash)
			if applied && c.eligible {
				require.NotEqual(t, c.claim.row.NativeEntryHash, row.NativeEntryHash)
			} else {
				require.Equal(t, c.claim.row.NativeEntryHash, row.NativeEntryHash)
			}
			entryHeight, err := keeper.GatewayFrozenNFTStakeEntryHeight(ctx(), row)
			require.NoError(t, err)
			require.Equal(t, c.entryHeight, entryHeight)
			checkpoint := f.readOutputs(f.gateway, f.target, "getFrozenDELCheckpoint", index.Big())
			require.Len(t, checkpoint, 2)
			require.Equal(t, row.CreatedHeight, checkpoint[0].(*big.Int).Int64())
			require.Equal(t, cursor, checkpoint[1].(*big.Int).Int64())
			raw := f.read(f.gateway, f.target, "getFrozenStake", index.Big())
			require.Equal(t, c.frozen, *abi.ConvertType(raw, new(gatewayFrozenWithdrawal)).(*gatewayFrozenWithdrawal))
			expected := c.snapshot
			expected.ReserveAmount = common.BigToHash(want.BigInt())
			expected.DeclaredCurrencyReserve = common.BigToHash(new(big.Int).Sub(expected.DeclaredCurrencyReserve.Big(), totalLoss.BigInt()))
			expected.HeldCurrencyReserve = common.BigToHash(new(big.Int).Sub(expected.HeldCurrencyReserve.Big(), totalLoss.BigInt()))
			require.Equal(t, expected, getPosition(ctx(), c.snapshot.Asset.TokenID))
			vault, id := c.snapshot.Asset.Contract, c.snapshot.Asset.TokenID
			lineage, err := nft.GetIsolatedLineage(ctx(), vault[:], id[:])
			require.NoError(t, err)
			expectedLineage := c.claim.lineage
			expectedLineage.Reserve.Amount = common.BigToHash(want.BigInt()).Bytes()
			require.Equal(t, expectedLineage, lineage)
		}
		f.backing(direct)
		f.activeReserve(direct)
	}
	assertCommitted(false)

	var routeLogs []int
	e.app.EVMKeeper.SetInternalCommitGuard(func(child sdk.Context, msg core.Message, logs []*ethtypes.Log) error {
		if executor.AuthorizeMasterHistoricalPenalty(child, msg) {
			return nil
		}
		if executor.AuthorizeFrozenHistoricalNFTPenalty(child, msg) {
			require.False(t, executor.AuthorizeFrozenDELPenalty(child, msg))
			require.False(t, executor.AuthorizeFrozenHistoricalDELPenalty(child, msg))
			routeLogs = append(routeLogs, len(logs))
			return nil
		}
		return errors.New("test-only internal EVM call lacks exact historical frozen NFT penalty authorization")
	})
	appendAndProject := func(projectErr error) error {
		return executor.AppendMasterHistoricalPenalty(ctx(), f.validator, infraction, supply, func(child sdk.Context, master settlement.MasterPenaltyProof) error {
			require.Equal(t, uint16(5), master.Percentage)
			for _, c := range claims {
				plan, err := keeper.PlanFrozenNFTHistoricalPenalty(child, c.claim.row, infraction)
				require.NoError(t, err)
				require.Equal(t, c.eligible, plan.Eligible)
				require.True(t, c.loss.Equal(plan.Loss))
				err = executor.ApplyFrozenHistoricalNFTPenalty(child, c.claim.row, master, plan.Eligible, supply,
					func(project sdk.Context, proof settlement.FrozenNFTPenaltyProof) error {
						require.Equal(t, c.claim.out.FrozenIndex, proof.Index)
						require.Equal(t, infraction, proof.InfractionHeight)
						require.Equal(t, master.BeforeCursor, proof.BeforeCursor)
						require.Equal(t, master.AfterCursor, proof.AfterCursor)
						require.Equal(t, plan.Eligible, proof.Eligible)
						require.True(t, plan.Loss.Equal(proof.Burned))
						require.True(t, plan.Remaining.Equal(proof.RemainingReserve))
						return keeper.ApplyGatewayFrozenHistoricalNFTPenalty(project, nft, c.claim.row, proof.InfractionHeight,
							proof.BeforeCursor, proof.AfterCursor, proof.RemainingReserve, proof.Eligible)
					})
				if err != nil {
					return err
				}
			}
			require.True(t, initialSupply.Sub(loss).Equal(supply(child)))
			return projectErr
		})
	}
	injected := errors.New("reject historical NFT penalty after both frozen queue projections")
	require.ErrorIs(t, appendAndProject(injected), injected)
	require.Equal(t, []int{4, 1}, routeLogs, "the late claim emits only its cursor acknowledgement")
	assertCommitted(false)
	require.Equal(t, initialQueue, getQueue(ctx()))
	for _, c := range claims {
		require.Equal(t, c.claim.row, getRow(ctx(), c.claim.out.FrozenIndex))
	}

	require.NoError(t, appendAndProject(nil))
	require.Equal(t, []int{4, 1, 4, 1}, routeLogs)
	assertCommitted(true)
	for _, c := range claims {
		row := getRow(ctx(), c.claim.out.FrozenIndex)
		require.ErrorContains(t, keeper.ApplyGatewayFrozenHistoricalNFTPenalty(ctx(), nft, row, infraction, common.Hash{}, common.BigToHash(big.NewInt(1)),
			row.ReportedReserveAmount, c.eligible), "cursor prestate changed")
	}
	t.Log(frozenHistoricalNFTPenaltyPassed)
}
