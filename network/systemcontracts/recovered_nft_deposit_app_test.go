package systemcontracts_test

import (
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/stretchr/testify/require"
)

const recoveredNFTDepositPassed = "signed NFT isolation begins at the recovered Master cursor without charging past penalties"

func TestOwnedAppSignedRecoveredNFTDeposit(t *testing.T) {
	testSignedDELFixture(t, "recovered-nft-deposit", recoveredNFTDepositPassed)
}

func verifyRecoveredNFTDeposit(t *testing.T, f signedDELFixture, executor *settlement.Executor, profile ownedapp.WithdrawalProfile,
	origin, vault contracts.Artifact, originAddress common.Address, existing, supply sdkmath.Int,
) {
	t.Helper()
	e := f.env
	validators, nft := e.app.NativeValidatorKeeper, e.app.NativeNFTKeeper
	reserve := sdkmath.NewIntWithDecimal(2, 18)
	originID := common.BigToHash(new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 224), big.NewInt(881)))
	seedActiveNFTPenaltyOrigin(t, f, profile, origin, vault, originAddress, originID, reserve, 1)
	approval, err := origin.ABI.Pack("setApprovalForAll", f.target, true)
	require.NoError(t, err)
	e.send(e.keys[0], &originAddress, new(big.Int), approval, true)

	args := []interface{}{f.validator, originAddress, originID.Big(), big.NewInt(1)}
	data, err := f.gateway.ABI.Pack("delegateNFT1155", args...)
	require.NoError(t, err)
	response := f.signed("delegateNFT1155", sdkmath.ZeroInt(), true, args...)
	height := e.app.LastBlockHeight()
	ctx := e.ctx().WithBlockHeight(height).WithBlockTime(e.ctx().BlockTime().Add(-time.Second)).
		WithGasMeter(storetypes.NewGasMeter(24_000_000))
	receipt := signedNFTReceipt(response)
	receipt.BlockNumber = big.NewInt(height)
	require.Len(t, receipt.Logs, 12, "the existing DEL companion advances the aggregate cursor once")
	msg := core.Message{From: f.user, To: &f.target, Value: new(big.Int), Data: data}
	_, err = executor.VerifySignedNFTIsolation(ctx.WithGasMeter(storetypes.NewGasMeter(24_000_000)), msg, receipt)
	require.Error(t, err, "historical isolation still requires a zero Master cursor")

	bad := *receipt
	bad.Logs = append([]*ethtypes.Log(nil), receipt.Logs...)
	ack := *bad.Logs[8]
	ack.Data = append([]byte(nil), ack.Data...)
	ack.Data[len(ack.Data)-1] = 1
	bad.Logs[8] = &ack
	_, err = executor.VerifySignedNFTIsolationWithDowntime(ctx.WithGasMeter(storetypes.NewGasMeter(24_000_000)), msg, &bad)
	require.ErrorContains(t, err, "aggregate penalty acknowledgement")

	proof, err := executor.VerifySignedNFTIsolationWithDowntime(ctx, msg, receipt)
	require.NoError(t, err)
	cursor := common.BigToHash(big.NewInt(1))
	require.Equal(t, cursor, proof.Companion.PenaltyCursor)
	require.True(t, proof.Companion.Remaining.Equal(existing))
	require.True(t, proof.Companion.NFTBacked.Equal(reserve))
	p := proof.Position
	require.Equal(t, uint32(1), p.OriginQuantity)
	require.Zero(t, proof.OriginSupplyAfter)
	require.True(t, proof.OriginReserveAfter.IsZero())
	lineage, err := nft.AdmitGatewayIsolation(ctx, nftkeeper.GatewayIsolation{
		Vault: p.Asset.Contract, Origin: p.Origin.Contract, Owner: f.user, Gateway: f.target,
		PositionID: p.Asset.TokenID, OriginTokenID: p.Origin.TokenID, NextPositionID: p.NextPositionID,
		Quantity: p.OriginQuantity, OriginSupplyAfter: proof.OriginSupplyAfter,
		Reserve: sdkmath.NewIntFromBigInt(p.ReserveAmount.Big()), OriginReserveAfter: proof.OriginReserveAfter,
	})
	require.NoError(t, err)
	position := validatorkeeper.GatewayNFTPosition{
		Companion: validatorkeeper.GatewayDELStake{Gateway: f.target, WrappedDEL: f.wrapped, Beneficiary: f.user, Validator: f.validator,
			StakeID: proof.Companion.StakeID, Amount: proof.Companion.Remaining, NFTBacked: proof.Companion.NFTBacked, PenaltyCursor: cursor},
		StakeID: proof.StakeID, Lineage: lineage,
	}
	validator, err := validators.GetValidator(ctx, f.operator)
	require.NoError(t, err)
	validator.Online, validator.Jailed = true, true
	require.NoError(t, validators.SetValidator(ctx, validator))
	require.ErrorContains(t, validators.ApplyGatewayNFTDelegation(ctx, position), "unjailed")
	validator.Jailed = false
	require.NoError(t, validators.SetValidator(ctx, validator))
	require.NoError(t, validators.ApplyGatewayNFTDelegation(ctx, position))
	binding, err := validators.GetEVMStakeBinding(ctx, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, f.target.Bytes(), proof.StakeID.Bytes())
	require.NoError(t, err)
	require.Equal(t, cursor.Bytes(), binding.PenaltyCursor)
	require.True(t, binding.ReportedReserveAmount.Equal(reserve))
	require.NoError(t, validators.RequireActiveStakePenaltyCatchup(ctx, f.validator.Bytes(), cursor.Bytes()))
	f.backing(existing)
	f.activeReserveAtCursor(existing.Add(reserve), cursor)
	require.True(t, supply.Equal(e.app.BankKeeper.GetSupply(ctx, identity.BaseDenom).Amount))
	t.Log(recoveredNFTDepositPassed)
}
