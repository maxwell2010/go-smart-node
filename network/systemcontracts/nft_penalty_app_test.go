package systemcontracts_test

import (
	"errors"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/withdrawalassets"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const nftPenaltyPassed = "active isolated NFT penalty burns only its DEL reserve and rolls back the bank, EVM and both native keepers on failure"
const nftPenaltyZeroExitPassed = "zero-reserve isolated NFT withdrawal preserves its units and completes exactly once"

func TestOwnedAppSignedActiveNFTPenalty(t *testing.T) {
	testSignedDELFixture(t, "nft-penalty", nftPenaltyPassed)
}

func TestOwnedAppSignedZeroReserveNFTExit(t *testing.T) {
	testSignedDELFixture(t, "nft-penalty-zero-exit", nftPenaltyZeroExitPassed)
}

func runSignedActiveNFTPenalty(t *testing.T, e isolatedBackendEnv, profile ownedapp.WithdrawalProfile,
	gateway, master, wrapper, origin, vault contracts.Artifact, originAddress common.Address,
	reader *nftview.Reader, gov func(contracts.Artifact, common.Address, string, ...interface{}),
	mode string,
) {
	t.Helper()
	f := prepareSignedDELFixture(t, e, profile, gateway, master, wrapper, gov)
	f.declareValidator()
	nft, validators := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	principal, unitReserve := sdkmath.NewIntWithDecimal(3, 18), sdkmath.NewIntWithDecimal(2, 18)
	percentage := uint16(25)
	zeroExit := mode == "nft-penalty-zero-exit"
	if zeroExit {
		principal, percentage = sdkmath.ZeroInt(), 100
	}
	executor, err := settlement.NewExecutor(e.app.EVMKeeper, reader, settlement.Config{EVMGas: 750_000, MaxLogs: 32, MaxLogBytes: 16384})
	require.NoError(t, err)
	position := func(transition settlement.DELTransition, cursor common.Hash) validatorkeeper.GatewayDELStake {
		return validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: transition.Beneficiary,
			Validator: transition.Validator, StakeID: transition.StakeID, Amount: transition.Remaining,
			NFTBacked: transition.NFTBacked, PenaltyCursor: cursor}
	}
	inclusion := func(response *evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt) {
		height := e.app.LastBlockHeight()
		ctx := e.ctx().WithBlockHeight(height).WithBlockTime(e.ctx().BlockTime().Add(-time.Second))
		receipt := signedNFTReceipt(response)
		receipt.BlockNumber = big.NewInt(height)
		return ctx, receipt
	}
	if principal.IsPositive() {
		data, err := gateway.ABI.Pack("delegateDEL", f.validator)
		require.NoError(t, err)
		response := f.signed("delegateDEL", principal, true, f.validator)
		committed, receipt := inclusion(response)
		proof, err := executor.VerifySignedDEL(committed, core.Message{From: f.user, To: &f.target, Value: principal.BigInt(), Data: data}, receipt)
		require.NoError(t, err)
		require.NoError(t, validators.ApplyGatewayDELDelegation(committed, position(proof, common.Hash{}), proof.Amount))
	}

	originID := common.BigToHash(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 224), big.NewInt(880)))
	lateUnits := uint32(1)
	if mode == "frozen-nft-penalty-max-units" {
		lateUnits = nfttypes.MaxIsolatedOriginUnits
	}
	seedActiveNFTPenaltyOrigin(t, f, profile, origin, vault, originAddress, originID, unitReserve, lateUnits+1)
	data, err := origin.ABI.Pack("setApprovalForAll", f.target, true)
	require.NoError(t, err)
	e.send(e.keys[0], &originAddress, new(big.Int), data, true)
	delegate := func(units uint32) settlement.NFTIsolation {
		t.Helper()
		args := []interface{}{f.validator, originAddress, originID.Big(), big.NewInt(int64(units))}
		data, err := gateway.ABI.Pack("delegateNFT1155", args...)
		require.NoError(t, err)
		response := f.signed("delegateNFT1155", sdkmath.ZeroInt(), true, args...)
		committed, receipt := inclusion(response)
		ctx, commit := committed.CacheContext()
		proof, err := executor.VerifySignedNFTIsolation(ctx, core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}, receipt)
		require.NoError(t, err)
		p := proof.Position
		lineage, err := nft.AdmitGatewayIsolation(ctx, nftkeeper.GatewayIsolation{
			Vault: p.Asset.Contract, Origin: p.Origin.Contract, Owner: f.user, Gateway: f.target,
			PositionID: p.Asset.TokenID, OriginTokenID: p.Origin.TokenID, NextPositionID: p.NextPositionID,
			Quantity: p.OriginQuantity, OriginSupplyAfter: proof.OriginSupplyAfter,
			Reserve: sdkmath.NewIntFromBigInt(p.ReserveAmount.Big()), OriginReserveAfter: proof.OriginReserveAfter,
		})
		require.NoError(t, err)
		require.NoError(t, validators.ApplyGatewayNFTDelegation(ctx, validatorkeeper.GatewayNFTPosition{
			Companion: position(proof.Companion, common.Hash{}), StakeID: proof.StakeID, Lineage: lineage,
		}))
		commit()
		return proof
	}
	penalized, sibling := delegate(1), delegate(lateUnits)
	if mode == "frozen-nft-penalty" || mode == "frozen-nft-penalty-max-units" {
		runSignedFrozenNFTPenalty(t, f, reader, executor, penalized, sibling, unitReserve, inclusion)
		return
	}
	getRow := func(kind validatortypes.EVMStakeBindingKind, id common.Hash) validatortypes.EVMStakeBinding {
		t.Helper()
		row, err := validators.GetEVMStakeBinding(e.ctx(), kind, f.target[:], id[:])
		require.NoError(t, err)
		return row
	}
	getNFT := func(id common.Hash) validatortypes.EVMStakeBinding {
		return getRow(validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, id)
	}
	getCoin := func() validatortypes.EVMStakeBinding {
		return getRow(validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN, penalized.Companion.StakeID)
	}
	getLineage := func(id common.Hash) nfttypes.IsolatedPositionLineage {
		t.Helper()
		lineage, err := nft.GetIsolatedLineage(e.ctx(), profile.PositionVault[:], id[:])
		require.NoError(t, err)
		return lineage
	}
	before, siblingBefore, coinBefore := getNFT(penalized.StakeID), getNFT(sibling.StakeID), getCoin()
	beforeLineage, siblingLineage := getLineage(penalized.Position.Asset.TokenID), getLineage(sibling.Position.Asset.TokenID)
	ctx := func() sdk.Context {
		return e.ctx().WithExecMode(sdk.ExecModeFinalize).WithGasMeter(storetypes.NewGasMeter(24_000_000))
	}
	supply := func(ctx sdk.Context) sdkmath.Int { return e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount }
	initialSupply := supply(ctx())
	initialVault, err := reader.Position(ctx(), penalized.Position.Asset.TokenID)
	require.NoError(t, err)
	initialCustody := sdkmath.NewIntFromBigInt(initialVault.HeldCurrencyReserve.Big())
	initialReserve := principal.Add(unitReserve.MulRaw(2))
	loss := unitReserve.MulRaw(int64(percentage)).QuoRaw(100)
	remaining := unitReserve.Sub(loss)
	check := func(applied bool) {
		t.Helper()
		cursor, reduction := int64(0), sdkmath.ZeroInt()
		if applied {
			cursor, reduction = 1, loss
		}
		require.Equal(t, cursor, f.read(master, profile.MasterValidator, "getPenaltyCount", f.validator).(*big.Int).Int64())
		require.Equal(t, cursor, f.read(gateway, f.target, "getNFTStakePenaltyIndex", penalized.StakeID).(*big.Int).Int64())
		require.Zero(t, f.read(gateway, f.target, "getNFTStakePenaltyIndex", sibling.StakeID).(*big.Int).Sign())
		require.True(t, initialSupply.Sub(reduction).Equal(supply(ctx())))
		for _, check := range []struct {
			id      common.Hash
			reserve sdkmath.Int
		}{{penalized.Position.Asset.TokenID, unitReserve.Sub(reduction)}, {sibling.Position.Asset.TokenID, unitReserve}} {
			p, err := reader.Position(ctx(), check.id)
			require.NoError(t, err)
			require.Equal(t, f.target, p.Owner)
			require.Equal(t, common.BigToHash(check.reserve.BigInt()), p.ReserveAmount)
			require.Equal(t, common.BigToHash(unitReserve.MulRaw(2).Sub(reduction).BigInt()), p.DeclaredCurrencyReserve)
			require.Equal(t, common.BigToHash(initialCustody.Sub(reduction).BigInt()), p.HeldCurrencyReserve)
		}
		require.Equal(t, siblingBefore, getNFT(sibling.StakeID))
		require.Equal(t, siblingLineage, getLineage(sibling.Position.Asset.TokenID))
		f.backing(principal)
		f.activeReserve(initialReserve.Sub(reduction))
		require.Empty(t, f.native().Undelegations)
	}
	e.app.EVMKeeper.SetInternalCommitGuard(func(ctx sdk.Context, msg core.Message, _ []*ethtypes.Log) error {
		if executor.AuthorizeMasterPenalty(ctx, msg) || executor.AuthorizeActiveNFTPenalty(ctx, msg) {
			return nil
		}
		return errors.New("test internal EVM call lacks exact penalty authorization")
	})
	apply := func(injected error) error {
		return executor.AppendMasterPenalty(ctx(), f.validator, percentage, supply, func(ctx sdk.Context, _ settlement.MasterPenaltyProof) error {
			return executor.ApplyActiveNFTPenalty(ctx, before, percentage, supply, func(ctx sdk.Context, proof settlement.ActiveNFTPenaltyProof) error {
				require.True(t, loss.Equal(proof.Burned))
				require.True(t, remaining.Equal(proof.RemainingReserve))
				require.True(t, principal.Equal(proof.Companion.Remaining))
				require.True(t, unitReserve.MulRaw(2).Equal(proof.Companion.NFTBacked))
				err := validators.ApplyGatewayNFTPenalty(ctx, nft, validatorkeeper.GatewayNFTPenalty{
					Before:       validatorkeeper.GatewayNFTPosition{Companion: position(proof.Companion, proof.CompanionCursor), StakeID: penalized.StakeID, Lineage: beforeLineage},
					BeforeCursor: proof.BeforeCursor, AfterCursor: proof.AfterCursor, RemainingReserve: proof.RemainingReserve,
				})
				if err != nil {
					return err
				}
				return injected
			})
		})
	}
	injected := errors.New("reject the penalty after projecting both keepers")
	require.ErrorIs(t, apply(injected), injected)
	check(false)
	require.Equal(t, before, getNFT(penalized.StakeID))
	require.Equal(t, beforeLineage, getLineage(penalized.Position.Asset.TokenID))
	require.Equal(t, coinBefore, getCoin())
	require.NoError(t, apply(nil))
	check(true)
	after := getNFT(penalized.StakeID)
	require.True(t, remaining.Equal(after.ReportedReserveAmount))
	require.Equal(t, common.BigToHash(big.NewInt(1)).Bytes(), after.PenaltyCursor)
	afterLineage := getLineage(penalized.Position.Asset.TokenID)
	require.Equal(t, common.BigToHash(remaining.BigInt()).Bytes(), afterLineage.Reserve.Amount)
	afterLineage.Reserve.Amount = beforeLineage.Reserve.Amount
	require.Equal(t, beforeLineage, afterLineage)
	coin := getCoin()
	require.True(t, coinBefore.RawAmount.Sub(loss).Equal(coin.RawAmount))
	require.True(t, coinBefore.RawNftBackedAmount.Sub(loss).Equal(coin.RawNftBackedAmount))
	require.Equal(t, coinBefore.PenaltyCursor, coin.PenaltyCursor)
	require.Error(t, executor.ApplyActiveNFTPenalty(ctx(), after, percentage, supply, func(sdk.Context, settlement.ActiveNFTPenaltyProof) error {
		t.Fatal("replayed active NFT penalty reached native projection")
		return nil
	}))
	check(true)
	if zeroExit {
		id := penalized.Position.Asset.TokenID
		data, err := gateway.ABI.Pack("withdrawNFT", f.validator, profile.PositionVault, id.Big())
		require.NoError(t, err)
		msg := core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}
		exactID, err := executor.SignedNFTWithdrawalStakeID(msg)
		require.NoError(t, err)
		require.Equal(t, penalized.StakeID, exactID)
		response := f.signed("withdrawNFT", sdkmath.ZeroInt(), true, f.validator, profile.PositionVault, id.Big())
		committed, receipt := inclusion(response)
		out, err := executor.VerifySignedPenalizedNFTWithdrawal(committed, msg, receipt, after)
		require.NoError(t, err)
		require.True(t, out.CompanionCursorAcknowledged)
		require.Equal(t, common.Hash{}, out.Position.ReserveAmount)
		bad := *receipt
		bad.Logs = append([]*ethtypes.Log(nil), receipt.Logs...)
		changed := *bad.Logs[0]
		changed.Data = append([]byte(nil), changed.Data...)
		changed.Data[len(changed.Data)-1] = 1
		bad.Logs[0] = &changed
		_, err = executor.VerifySignedPenalizedNFTWithdrawal(committed, msg, &bad, after)
		require.Error(t, err, "signed exit cannot report a coin reserve change")
		_, err = executor.VerifySignedNFTWithdrawal(committed, msg, receipt)
		require.Error(t, err)
		lineage := getLineage(id)
		require.NoError(t, validators.ApplyGatewayPenalizedNFTWithdrawal(committed, validatorkeeper.GatewayNFTPosition{
			Companion: position(out.Companion, out.Companion.PenaltyCursor), StakeID: out.StakeID, Lineage: lineage,
		}, out.FrozenIndex, out.Deadline, out.PenaltyCursor, out.CompanionCursorAcknowledged))
		frozen, err := validators.GetEVMFrozenBinding(e.ctx(), f.target[:], out.FrozenIndex[:])
		require.NoError(t, err)
		require.True(t, frozen.Active)
		require.True(t, frozen.RawAmount.Equal(sdkmath.OneInt()))
		require.True(t, frozen.ReportedReserveAmount.IsZero())
		require.Equal(t, lineage.OriginSubTokenIDs, frozen.NFTPosition.Origin.NativeSubTokenIDs)
		require.Equal(t, []uint32{lineage.ReceiptSubTokenID}, frozen.NativeTarget.SubTokenIDs)
		require.Equal(t, out.PenaltyCursor, common.BytesToHash(getCoin().PenaltyCursor))
		require.Equal(t, siblingBefore, getNFT(sibling.StakeID))
		completePenalizedNFTExit(t, f, reader, executor, frozen, out, lineage, inclusion, 0)
		require.True(t, initialSupply.Sub(loss).Equal(supply(ctx())))
		f.backing(sdkmath.ZeroInt())
		t.Log(nftPenaltyZeroExitPassed)
	}
	t.Log(nftPenaltyPassed)
}

func completePenalizedNFTExit(t *testing.T, f signedDELFixture, reader *nftview.Reader, executor *settlement.Executor,
	frozen validatortypes.EVMFrozenBinding, out settlement.NFTWithdrawal, lineage nfttypes.IsolatedPositionLineage,
	inclusion func(*evmtypes.MsgEthereumTxResponse) (sdk.Context, *ethtypes.Receipt), pending int,
) {
	t.Helper()
	e := f.env
	nft, validators := e.app.NativeNFTKeeper, e.app.NativeValidatorKeeper
	coinABI, err := contracts.CompiledABI("DRC20")
	require.NoError(t, err)
	reserve, err := coinkeeper.NewReserveHook(e.app.NativeCoinKeeper, coinABI,
		func(sdk.Context, common.Address) (sdkmath.Int, sdkmath.Int, error) {
			return sdkmath.Int{}, sdkmath.Int{}, errors.New("unexpected custom reserve transition in NFT exit")
		}, 32)
	require.NoError(t, err)
	assets, err := withdrawalassets.New(reserve, nft, reader, validators, nftreceipts.Limits{
		MaxLogs: 32, MaxBytes: 16384, MaxAssets: 1, MaxIDs: 1, MaxUnits: 1,
	}, false)
	require.NoError(t, err)
	for i := 0; e.ctx().BlockTime().Before(out.Deadline); i++ {
		require.Less(t, i, signedDELFreezeSeconds)
		e.advance()
	}
	args := []*big.Int{out.FrozenIndex.Big()}
	response := f.signed("complete", sdkmath.ZeroInt(), true, args)
	committed, receipt := inclusion(response)
	child, commit := committed.CacheContext()
	proof, err := executor.ValidateSigned(child, frozen, receipt)
	require.NoError(t, err)
	require.NoError(t, assets.Project(child, frozen, proof.Receipt))
	require.NoError(t, validators.ApplyWithdrawalCompletion(child, frozen))
	commit()
	unit, err := nft.GetSubToken(e.ctx(), lineage.ReceiptNativeTokenID, lineage.ReceiptSubTokenID)
	require.NoError(t, err)
	require.Equal(t, f.beneficiary, unit.Owner)
	p, err := reader.Position(e.ctx(), out.Position.Asset.TokenID)
	require.NoError(t, err)
	require.Equal(t, f.user, p.Owner)
	require.Equal(t, common.BigToHash(frozen.ReportedReserveAmount.BigInt()), p.ReserveAmount)
	if pending == 0 {
		require.Empty(t, f.native().Undelegations)
	} else {
		pair, err := validators.GetUndelegation(e.ctx(), f.beneficiary, f.operator)
		require.NoError(t, err)
		require.Len(t, pair.Entries, pending)
	}
	f.signed("complete", sdkmath.ZeroInt(), false, args)
}

func seedActiveNFTPenaltyOrigin(t *testing.T, f signedDELFixture, profile ownedapp.WithdrawalProfile,
	origin, vault contracts.Artifact, originAddress common.Address, originID common.Hash, reserve sdkmath.Int, quantity uint32,
) {
	t.Helper()
	e := f.env
	const uri = "ipfs://active-nft-penalty"
	data, err := origin.ABI.Pack("mintByDEL", f.user, originID.Big(), big.NewInt(int64(quantity)), uri)
	require.NoError(t, err)
	e.nextGasLimit(signedNFTGasLimit)
	e.send(e.keys[0], &originAddress, reserve.MulRaw(int64(quantity)).BigInt(), data, true)
	genesis, err := e.app.NativeNFTKeeper.ExportGenesis(e.ctx())
	require.NoError(t, err)
	require.Empty(t, genesis.Collections)
	domain, ok := e.app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	require.True(t, ok)
	vaultCreator := sdk.AccAddress(e.govOwner.Bytes()).String()
	units := make(nfttypes.SubTokens, quantity)
	for i := range units {
		units[i] = &nfttypes.SubToken{ID: uint32(i + 1), Owner: f.beneficiary}
	}
	genesis.Collections = []nfttypes.Collection{
		{Creator: f.beneficiary, Denom: signedNFTNativeOriginDenom, Supply: 1, TypeNft: nfttypes.NftType_NFT1155,
			AddressDRC: originAddress.Hex(), Tokens: nfttypes.Tokens{{Creator: f.beneficiary, Denom: signedNFTNativeOriginDenom,
				ID: signedNFTNativeOriginID, URI: uri, Reserve: sdk.NewCoin(identity.BaseDenom, reserve), Minted: quantity, SubTokens: units}}},
		{Creator: vaultCreator, Denom: signedNFTNativeVaultDenom, TypeNft: nfttypes.NftType_NFT721, AddressDRC: profile.PositionVault.Hex()},
	}
	genesis.ExternalBindings = []nfttypes.ExternalNFTBinding{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID,
		Contract: originAddress[:], TokenID: originID[:], NativeTokenID: signedNFTNativeOriginID,
		NFTType: nfttypes.NftType_NFT1155, NextSubTokenID: uint64(quantity) + 1}}
	nextPosition := common.BigToHash(f.read(vault, profile.PositionVault, "nextPositionId").(*big.Int))
	genesis.IsolatedVaults = []nfttypes.IsolatedVaultState{{ChainID: domain.ChainID, EVMChainID: domain.EVMChainID,
		Vault: profile.PositionVault[:], NextPositionID: nextPosition[:], ReceiptCreator: vaultCreator, ReceiptDenom: signedNFTNativeVaultDenom}}
	require.NoError(t, genesis.Validate())
	replaceWithdrawalFixtureStore(t, e, nfttypes.StoreKey, func(ctx sdk.Context) error { return e.app.NativeNFTKeeper.InitGenesis(ctx, genesis) })
}
