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

func runSignedFrozenNFTPenalty(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	first, second settlement.NFTIsolation, unitReserve sdkmath.Int, bothEligible bool,
	inclusion func(*evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt),
) {
	t.Helper()
	e := f.env
	nft, validators := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	type withdrawal struct {
		out     settlement.NFTWithdrawal
		row     validatortypes.EVMFrozenBinding
		lineage nfttypes.IsolatedPositionLineage
	}
	getRow := func(index common.Hash) validatortypes.EVMFrozenBinding {
		t.Helper()
		row, err := validators.GetEVMFrozenBinding(e.ctx(), f.target[:], index[:])
		require.NoError(t, err)
		return row
	}
	withdraw := func(stake settlement.NFTIsolation) withdrawal {
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
		return withdrawal{out, getRow(out.FrozenIndex), lineage}
	}
	early, late := withdraw(first), withdraw(second)
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
			for _, claim := range []withdrawal{early, late} {
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
	for _, claim := range []withdrawal{early, late} {
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
