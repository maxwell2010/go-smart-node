package systemcontracts_test

import (
	"errors"
	"math/big"
	"strings"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const (
	frozenNFTPenaltyPassed    = "frozen isolated NFT penalties preserve both receipts and roll back rejected reserve loss"
	frozenNFTMaxUnitsMode     = "frozen-nft-penalty-max-units"
	frozenNFTBothEligibleMode = "frozen-nft-penalty-both-eligible"
	frozenNFTWithHeldMode     = "frozen-nft-and-held-penalty-max-units"
)

func TestOwnedAppSignedFrozenNFTPenalty(t *testing.T) {
	testSignedDELFixture(t, "frozen-nft-penalty", frozenNFTPenaltyPassed)
}

func TestOwnedAppSignedMaxUnitFrozenNFTPenalty(t *testing.T) {
	for _, mode := range []string{frozenNFTMaxUnitsMode, frozenNFTBothEligibleMode} {
		t.Run(mode, func(t *testing.T) {
			output := testSignedDELFixture(t, mode, frozenNFTPenaltyPassed)
			const prefix = "frozen NFT penalty gas origin_units=1024+1024 "
			_, measurement, found := strings.Cut(output, prefix)
			require.True(t, found, "isolated fixture did not report the 1024-unit measurement")
			line, _, _ := strings.Cut(measurement, "\n")
			t.Log(prefix + strings.TrimSpace(line))
		})
	}
}

type frozenNFTPenaltyClaim struct {
	out     settlement.NFTWithdrawal
	row     validatortypes.EVMFrozenBinding
	lineage nfttypes.IsolatedPositionLineage
}

// Freeze the signed receipt and project the queue entry in the same state used by the penalty fixture.
func freezeSignedNFTPenaltyClaim(t *testing.T, f signedDELFixture, executor *settlement.Executor, stake settlement.NFTIsolation,
	inclusion func(*evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt),
) frozenNFTPenaltyClaim {
	e := f.env
	nft, validators := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	t.Helper()
	vault, id := stake.Position.Asset.Contract, stake.Position.Asset.TokenID
	data, err := f.gateway.ABI.Pack("withdrawNFT", f.validator, vault, id.Big())
	require.NoError(t, err)
	response := f.signed("withdrawNFT", sdkmath.ZeroInt(), true, f.validator, vault, id.Big())
	committed, receipt := inclusion(response)
	out, err := executor.VerifySignedNFTWithdrawal(committed, core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}, receipt)
	require.NoError(t, err)
	lineage, err := nft.GetIsolatedLineage(committed, vault[:], id[:])
	require.NoError(t, err)
	require.NoError(t, validators.ApplyGatewayNFTWithdrawal(committed, validatorkeeper.GatewayNFTPosition{
		Companion: validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
			StakeID: out.Companion.StakeID, Amount: out.Companion.Remaining, NFTBacked: out.Companion.NFTBacked},
		StakeID: out.StakeID, Lineage: lineage,
	}, out.FrozenIndex, out.Deadline))
	row, err := validators.GetEVMFrozenBinding(e.ctx(), f.target[:], out.FrozenIndex[:])
	require.NoError(t, err)
	return frozenNFTPenaltyClaim{out, row, lineage}
}

func runSignedFrozenNFTPenalty(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	first, second settlement.NFTIsolation, unitReserve sdkmath.Int, bothEligible bool,
	inclusion func(*evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt),
) {
	t.Helper()
	e := f.env
	nft, validators := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	getRow := func(index common.Hash) validatortypes.EVMFrozenBinding {
		t.Helper()
		row, err := validators.GetEVMFrozenBinding(e.ctx(), f.target[:], index[:])
		require.NoError(t, err)
		return row
	}
	early := freezeSignedNFTPenaltyClaim(t, f, executor, first, inclusion)
	late := freezeSignedNFTPenaltyClaim(t, f, executor, second, inclusion)
	earlyReserve := unitReserve.MulRaw(int64(first.Position.OriginQuantity))
	lateReserve := unitReserve.MulRaw(int64(second.Position.OriginQuantity))
	require.Len(t, early.lineage.OriginSubTokenIDs, int(first.Position.OriginQuantity))
	require.Len(t, late.lineage.OriginSubTokenIDs, int(second.Position.OriginQuantity))
	require.Less(t, early.row.CreatedHeight, late.row.CreatedHeight)
	infraction := late.row.CreatedHeight
	eligibleClaims := 1
	totalBurn := lateReserve
	if bothEligible {
		infraction = early.row.CreatedHeight
		eligibleClaims++
		totalBurn = totalBurn.Add(earlyReserve)
	}
	const attemptGas = uint64(24_000_000)
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(attemptGas))
	}
	supply := func(ctx sdk.Context) sdkmath.Int { return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount }
	initialSupply := supply(ctx())
	pair, err := validators.GetUndelegation(e.ctx(), f.beneficiary, f.operator)
	require.NoError(t, err)
	require.Len(t, pair.Entries, 2)
	f.activeReserve(sdkmath.NewIntWithDecimal(3, 18))
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterPenalty(ctx, msg) || executor.AuthorizeFrozenDELPenalty(ctx, msg) {
			return nil
		}
		return errors.New("test internal EVM call lacks exact frozen NFT penalty authorization")
	})
	const percentage uint16 = 100
	apply := func(injected error) error {
		execution := ctx()
		err := executor.AppendMasterPenalty(execution, f.validator, percentage, supply, func(ctx sdk.Context, _ settlement.MasterPenaltyProof) error {
			for _, claim := range []frozenNFTPenaltyClaim{early, late} {
				if err := executor.ApplyFrozenNFTPenalty(ctx, claim.row, infraction, percentage, supply, func(ctx sdk.Context, proof settlement.FrozenNFTPenaltyProof) error {
					eligible := claim.row.CreatedHeight >= infraction
					burn := sdkmath.ZeroInt()
					if eligible {
						burn = claim.row.ReportedReserveAmount
					}
					require.Equal(t, eligible, proof.Eligible)
					require.True(t, burn.Equal(proof.Burned))
					require.True(t, claim.row.ReportedReserveAmount.Sub(burn).Equal(proof.RemainingReserve))
					return validators.ApplyGatewayFrozenNFTPenalty(ctx, nft, claim.row, infraction, proof.BeforeCursor, proof.AfterCursor, proof.RemainingReserve)
				}); err != nil {
					return err
				}
			}
			return injected
		})
		if err == nil {
			used := execution.GasMeter().GasConsumed()
			require.Positive(t, used)
			require.LessOrEqual(t, used, attemptGas)
			t.Logf("frozen NFT penalty gas origin_units=%d+%d sdk_gas=%d budget=%d eligible_claims=%d",
				first.Position.OriginQuantity, second.Position.OriginQuantity, used, attemptGas, eligibleClaims)
		}
		return err
	}
	injected := errors.New("reject after both frozen NFT projections")
	require.ErrorIs(t, apply(injected), injected)
	require.True(t, initialSupply.Equal(supply(ctx())))
	require.Equal(t, early.row, getRow(early.out.FrozenIndex))
	require.Equal(t, late.row, getRow(late.out.FrozenIndex))
	rolledBack, err := validators.GetUndelegation(e.ctx(), f.beneficiary, f.operator)
	require.NoError(t, err)
	require.Equal(t, pair, rolledBack)
	for _, claim := range []frozenNFTPenaltyClaim{early, late} {
		position, err := reader.Position(ctx(), claim.out.Position.Asset.TokenID)
		require.NoError(t, err)
		require.Equal(t, common.BigToHash(claim.row.ReportedReserveAmount.BigInt()), position.ReserveAmount)
	}
	require.NoError(t, apply(nil))
	require.True(t, initialSupply.Sub(totalBurn).Equal(supply(ctx())))
	earlyAfter, lateAfter := getRow(early.out.FrozenIndex), getRow(late.out.FrozenIndex)
	for _, row := range []validatortypes.EVMFrozenBinding{earlyAfter, lateAfter} {
		require.True(t, row.Active)
		require.True(t, sdkmath.OneInt().Equal(row.RawAmount))
	}
	require.Equal(t, early.row.NFTPosition, earlyAfter.NFTPosition)
	require.Equal(t, late.row.NFTPosition, lateAfter.NFTPosition)
	require.NotEqual(t, late.row.NativeEntryHash, lateAfter.NativeEntryHash)
	require.True(t, lateAfter.ReportedReserveAmount.IsZero())
	if bothEligible {
		require.NotEqual(t, early.row.NativeEntryHash, earlyAfter.NativeEntryHash)
		require.True(t, earlyAfter.ReportedReserveAmount.IsZero())
	} else {
		require.Equal(t, early.row.NativeEntryHash, earlyAfter.NativeEntryHash)
		require.True(t, earlyReserve.Equal(earlyAfter.ReportedReserveAmount))
	}
	f.backing(sdkmath.NewIntWithDecimal(3, 18))
	f.activeReserve(sdkmath.NewIntWithDecimal(3, 18))
	require.Error(t, executor.ApplyFrozenNFTPenalty(ctx(), lateAfter, infraction, percentage, supply,
		func(sdk.Context, settlement.FrozenNFTPenaltyProof) error {
			t.Fatal("already reconciled frozen penalty reached projection")
			return nil
		}))
	completePenalizedNFTExit(t, f, reader, executor, lateAfter, late.out, late.lineage, inclusion, 1)
	completePenalizedNFTExit(t, f, reader, executor, earlyAfter, early.out, early.lineage, inclusion, 0)
	require.True(t, initialSupply.Sub(totalBurn).Equal(supply(ctx())))
	t.Log(frozenNFTPenaltyPassed)
}

const frozenNFTWithHeldPassed = "frozen max-unit NFT and 128 active held DEL lots penalized and reconciled together"

func TestOwnedAppSignedMaxUnitFrozenNFTAndHeldPenalty(t *testing.T) {
	testSignedHoldLotBudget(t, frozenNFTWithHeldMode, frozenNFTWithHeldPassed)
}

func runSignedFrozenNFTAndHeldPenalty(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	stake settlement.NFTIsolation, inclusion func(*evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt),
) {
	t.Helper()
	e := f.env
	nft, keeper := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	claim := freezeSignedNFTPenaltyClaim(t, f, executor, stake, inclusion)
	lot := sdkmath.NewIntWithDecimal(1, 18)
	deadline := e.ctx().BlockTime().Unix() + benchmarkHoldLots + 12
	heldID := fundSignedDELPenalty(t, f, executor, lot, deadline)
	for range benchmarkHoldLots - 1 {
		require.Equal(t, heldID, fundSignedDELPenalty(t, f, executor, lot, deadline))
	}
	readHeld := func() validatortypes.EVMStakeBinding {
		t.Helper()
		row, err := keeper.GetEVMStakeBinding(e.ctx(), validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, f.target[:], heldID[:])
		require.NoError(t, err)
		return row
	}
	readFrozen := func() validatortypes.EVMFrozenBinding {
		t.Helper()
		row, err := keeper.GetEVMFrozenBinding(e.ctx(), f.target[:], claim.out.FrozenIndex[:])
		require.NoError(t, err)
		return row
	}
	readLots := func() []*validatortypes.StakeHold {
		t.Helper()
		row, err := keeper.GetDelegation(e.ctx(), f.beneficiary, f.operator, validatortypes.StakeType_CoinHold, identity.BaseDenom)
		require.NoError(t, err)
		return row.Stake.Holds
	}
	active, next, err := keeper.GetActiveStakePenaltyPage(e.ctx(), f.validator[:], nil, 2)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Len(t, active, 1, "the fully withdrawn NFT no longer has an active companion")
	frozen, next, err := keeper.GetValidatorFrozenBindingsPage(e.ctx(), f.validator[:], nil, 2)
	require.NoError(t, err)
	require.Nil(t, next)
	require.Len(t, frozen, 1)
	beforeHeld, beforeLots := readHeld(), readLots()
	require.Len(t, beforeLots, benchmarkHoldLots)
	f.activeReserve(lot.MulRaw(benchmarkHoldLots))
	f.backing(beforeHeld.RawAmount)
	heldLoss := beforeHeld.RawAmount.QuoRaw(2)
	nftLoss := claim.row.ReportedReserveAmount.QuoRaw(2)
	totalLoss := heldLoss.Add(nftLoss)
	const budget uint64 = 24_000_000
	supply := func(ctx sdk.Context) sdkmath.Int { return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount }
	initialSupply := supply(e.ctx())
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterPenalty(ctx, msg) || executor.AuthorizeHeldDELPenalty(ctx, msg) ||
			executor.AuthorizeFrozenDELPenalty(ctx, msg) {
			return nil
		}
		return errors.New("test internal call lacks exact mixed penalty authorization")
	})
	apply := func(injected error) error {
		execution := e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(budget))
		const percentage uint16 = 50
		infraction := claim.row.CreatedHeight
		err := executor.AppendMasterPenalty(execution, f.validator, percentage, supply, func(ctx sdk.Context, master settlement.MasterPenaltyProof) error {
			err := executor.ApplyHeldDELPenalty(ctx, beforeHeld, percentage, supply, func(ctx sdk.Context, proof settlement.ActiveDELPenaltyProof) error {
				require.True(t, heldLoss.Equal(proof.Burned))
				require.Equal(t, master.AfterCursor, common.BytesToHash(proof.After.PenaltyCursor))
				return keeper.ApplyGatewayDELHoldPenalty(ctx, validatorkeeper.GatewayDELStake{
					Gateway: f.target, WrappedDEL: f.wrapped, Validator: f.validator, Beneficiary: f.user,
					StakeID: heldID, Amount: beforeHeld.RawAmount, PenaltyCursor: master.BeforeCursor,
					HoldDeadline: deadline, HoldStartTime: beforeHeld.NativeHoldStartTime,
				}, proof.After.RawAmount, master.AfterCursor)
			})
			if err != nil {
				return err
			}
			err = executor.ApplyFrozenNFTPenalty(ctx, claim.row, infraction, percentage, supply, func(ctx sdk.Context, proof settlement.FrozenNFTPenaltyProof) error {
				require.True(t, proof.Eligible)
				require.True(t, nftLoss.Equal(proof.Burned))
				require.Equal(t, master.AfterCursor, proof.AfterCursor)
				return keeper.ApplyGatewayFrozenNFTPenalty(ctx, nft, claim.row, infraction, proof.BeforeCursor, proof.AfterCursor, proof.RemainingReserve)
			})
			if err != nil {
				return err
			}
			if err := keeper.RequireActiveStakePenaltyCatchup(ctx, f.validator[:], master.AfterCursor[:]); err != nil {
				return err
			}
			return injected
		})
		if err == nil {
			t.Logf("DEL hold penalty gas lots=%d sdk_gas=%d budget=%d frozen_nft_units=%d",
				benchmarkHoldLots, execution.GasMeter().GasConsumed(), budget, stake.Position.OriginQuantity)
		}
		return err
	}
	rejected := errors.New("reject both mixed penalty projections")
	require.ErrorIs(t, apply(rejected), rejected)
	require.Equal(t, beforeHeld, readHeld())
	require.Equal(t, claim.row, readFrozen())
	require.Equal(t, beforeLots, readLots())
	require.True(t, initialSupply.Equal(supply(e.ctx())))

	require.NoError(t, apply(nil))
	heldAfter, nftAfter := readHeld(), readFrozen()
	require.True(t, heldLoss.Equal(heldAfter.RawAmount))
	require.True(t, nftLoss.Equal(nftAfter.ReportedReserveAmount))
	require.Equal(t, claim.row.NFTPosition, nftAfter.NFTPosition)
	require.True(t, initialSupply.Sub(totalLoss).Equal(supply(e.ctx())))
	f.activeReserve(heldLoss)
	f.backing(heldLoss)
	afterLots := readLots()
	require.Len(t, afterLots, benchmarkHoldLots)
	for i, before := range beforeLots {
		require.Equal(t, before.HoldStartTime, afterLots[i].HoldStartTime)
		require.Equal(t, before.HoldEndTime, afterLots[i].HoldEndTime)
		require.True(t, before.Amount.QuoRaw(2).Equal(afterLots[i].Amount))
	}
	completePenalizedNFTExit(t, f, reader, executor, nftAfter, claim.out, claim.lineage, inclusion, 0)
	require.True(t, initialSupply.Sub(totalLoss).Equal(supply(e.ctx())))
	t.Log(frozenNFTWithHeldPassed)
}
