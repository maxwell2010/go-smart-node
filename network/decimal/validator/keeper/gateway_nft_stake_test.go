package keeper

import (
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func gatewayNFTFixture(t *testing.T, companion GatewayDELStake, positionNumber byte, reserve int64, originUnits []uint32) GatewayNFTPosition {
	t.Helper()
	positionID := common.BytesToHash([]byte{positionNumber})
	positionID[5] = 1
	vault := stateAddress(8)
	receiptID, err := nfttypes.FreshExternalTokenID(bindingTestChain, 990001, vault, positionID[:])
	require.NoError(t, err)
	originID := common.Hash{6: 1}
	lineage := nfttypes.IsolatedPositionLineage{
		Vault: vault, PositionID: positionID.Bytes(), ParentPositionID: make([]byte, 32),
		OriginContract: stateAddress(7), OriginTokenID: originID.Bytes(), OriginNativeTokenID: "origin-native",
		OriginSubTokenIDs: originUnits, ReceiptNativeTokenID: receiptID, ReceiptSubTokenID: 1,
		Reserve: nfttypes.IsolatedReserveSnapshot{
			Kind: nfttypes.IsolatedReserveDEL, Token: make([]byte, 20), Amount: stateWord(uint64(reserve)),
		},
	}
	id := crypto.Keccak256Hash([]byte("NFT"), vault, positionID[:], companion.Beneficiary[:], companion.Validator[:], lineage.ParentPositionID)
	return GatewayNFTPosition{Companion: companion, StakeID: id, Lineage: lineage}
}

func TestGatewayNFTNativeLifecycleWithDirectDELAndSibling(t *testing.T) {
	ctx, service, k := bindingEnvironment(t, false)
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	companion := gatewayDELFixture()
	companion.Amount, companion.NFTBacked = sdkmath.ZeroInt(), sdkmath.NewInt(5)
	first := gatewayNFTFixture(t, companion, 1, 5, []uint32{2, 8})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, first))
	owner, operator, err := companion.nativePair()
	require.NoError(t, err)
	_, err = k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.ErrorIs(t, err, ErrNotFound)
	coin, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, companion.Gateway[:], companion.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, companion.NFTBacked, coin.RawAmount)
	require.Equal(t, companion.NFTBacked, coin.RawNftBackedAmount)

	ctx = ctx.WithBlockHeight(11).WithBlockTime(time.Unix(2001, 0))
	companion.Amount = sdkmath.NewInt(12)
	require.NoError(t, k.ApplyGatewayDELDelegation(ctx, companion, companion.Amount))
	companion.NFTBacked = sdkmath.NewInt(12)
	second := gatewayNFTFixture(t, companion, 2, 7, []uint32{10})
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, second))
	duplicate := first
	duplicate.Companion = companion
	duplicate.Companion.NFTBacked = duplicate.Companion.NFTBacked.AddRaw(5)
	require.ErrorContains(t, k.ApplyGatewayNFTDelegation(ctx, duplicate), "already has a native binding")
	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, before.Delegations, 3)
	require.Len(t, before.EVMStakeBindings, 3)
	siblingBinding, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], second.StakeID[:])
	require.NoError(t, err)

	ctx = ctx.WithBlockHeight(12).WithBlockTime(time.Unix(2002, 0))
	first.Companion = companion
	first.Companion.NFTBacked = sdkmath.NewInt(7)
	deadline, frozenID := time.Unix(2010, 0).UTC(), common.Hash{}
	changed := first
	changed.Lineage.OriginSubTokenIDs = []uint32{2, 9}
	require.ErrorContains(t, k.ApplyGatewayNFTWithdrawal(ctx, changed, frozenID, deadline), "differs from the active native binding")
	changed = first
	changed.Companion.NFTBacked = sdkmath.NewInt(8)
	require.ErrorContains(t, k.ApplyGatewayNFTWithdrawal(ctx, changed, frozenID, deadline), "differs from the active native binding")
	require.ErrorContains(t, k.ApplyGatewayNFTWithdrawal(ctx, first, frozenID, ctx.BlockTime()), "index or deadline")
	broken, err := NewStateKeeperWithDomain(failingService{service: service, prefix: undelegationPrefix}, bindingTestChain, 990001)
	require.NoError(t, err)
	events := len(ctx.EventManager().Events())
	require.ErrorContains(t, broken.ApplyGatewayNFTWithdrawal(ctx, first, frozenID, deadline), "injected write failure")
	afterFailure, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, afterFailure)
	require.Len(t, ctx.EventManager().Events(), events)
	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, first, frozenID, deadline))
	require.Len(t, ctx.EventManager().Events(), events+1)
	require.Equal(t, "decimal_nft_withdrawal_requested", ctx.EventManager().Events()[events].Type)

	frozen, err := k.GetEVMFrozenBinding(ctx, companion.Gateway[:], frozenID[:])
	require.NoError(t, err)
	hasProvenance, err := service.OpenKVStore(ctx).Has(gatewayFrozenNFTProvenanceKey(&frozen))
	require.NoError(t, err)
	require.False(t, hasProvenance)
	_, err = k.GatewayFrozenNFTStakeEntryHeight(ctx, frozen)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.GetFrozenWithdrawalCreationBlock(ctx, frozen)
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, first.StakeID.Bytes(), frozen.SourceStakeID)
	require.Equal(t, sdkmath.OneInt(), frozen.RawAmount)
	require.Equal(t, sdkmath.NewInt(5), frozen.ReportedReserveAmount)
	require.Equal(t, first.Lineage.OriginSubTokenIDs, frozen.NFTPosition.Origin.NativeSubTokenIDs)
	require.Equal(t, first.Lineage.ReceiptNativeTokenID, frozen.NativeTarget.ID)
	assignment, err := k.LookupCustodyNFTBinding(ctx, first.Lineage.Vault, first.Lineage.PositionID)
	require.NoError(t, err)
	require.Equal(t, &frozen, assignment.Frozen)
	pending, err := k.GetUndelegation(ctx, owner, operator)
	require.NoError(t, err)
	require.Len(t, pending.Entries, 1)
	require.Equal(t, frozen.ReportedReserveAmount, pending.Entries[0].Stake.Stake.Amount)
	require.Equal(t, []uint32{1}, pending.Entries[0].Stake.SubTokenIDs)
	hash, err := types.NativeQueueEntryHash(1, owner, operator, "", &pending.Entries[0])
	require.NoError(t, err)
	require.Equal(t, hash, frozen.NativeEntryHash)
	coin, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, companion.Gateway[:], companion.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, sdkmath.NewInt(19), coin.RawAmount)
	require.Equal(t, sdkmath.NewInt(7), coin.RawNftBackedAmount)
	require.EqualValues(t, 10, coin.CreatedHeight)
	sibling, err := k.GetEVMStakeBinding(ctx, gatewayNFTBinding, companion.Gateway[:], second.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, siblingBinding, sibling)
	withPending, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Len(t, withPending.Delegations, 2)
	require.Equal(t, before.Validators, withPending.Validators)

	ctx = ctx.WithBlockHeight(13).WithBlockTime(deadline)
	require.NoError(t, k.ApplyWithdrawalCompletion(ctx, frozen))
	require.ErrorIs(t, k.ApplyWithdrawalCompletion(ctx, frozen), ErrWithdrawalAlreadyCompleted)
	_, err = k.LookupCustodyNFTBinding(ctx, first.Lineage.Vault, first.Lineage.PositionID)
	require.ErrorIs(t, err, ErrNotFound)
	second.Companion.NFTBacked = sdkmath.ZeroInt()
	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, second, common.BytesToHash([]byte{1}), time.Unix(2020, 0)))
	plain, err := k.GetDelegation(ctx, owner, operator, types.StakeType_Coin, identity.BaseDenom)
	require.NoError(t, err)
	require.Equal(t, companion.Amount, plain.Stake.Stake.Amount)
	coin, err = k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, companion.Gateway[:], companion.StakeID[:])
	require.NoError(t, err)
	require.Equal(t, companion.Amount, coin.RawAmount)
	require.True(t, coin.RawNftBackedAmount.IsZero())
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestGatewayNFTLastReceiptRemovesEmptyCompanion(t *testing.T) {
	ctx, _, k := bindingEnvironment(t, false)
	companion := gatewayDELFixture()
	companion.Amount, companion.NFTBacked = sdkmath.ZeroInt(), sdkmath.NewInt(5)
	position := gatewayNFTFixture(t, companion, 1, 5, []uint32{2, 8})
	invalid := position
	invalid.StakeID = common.Hash{}
	require.ErrorContains(t, k.ApplyGatewayNFTDelegation(ctx, invalid), "invalid Gateway NFT stake")
	invalid = position
	invalid.Lineage.ReceiptNativeTokenID = "unrelated-native"
	require.ErrorContains(t, k.ApplyGatewayNFTDelegation(ctx, invalid), "differs from its native identity")
	require.NoError(t, k.ApplyGatewayNFTDelegation(ctx, position))
	position.Companion.NFTBacked = sdkmath.ZeroInt()
	deadline := time.Unix(2010, 0).UTC()
	require.NoError(t, k.ApplyGatewayNFTWithdrawal(ctx, position, common.Hash{}, deadline))
	_, err := k.GetEVMStakeBinding(ctx, gatewayDELCoinBinding, companion.Gateway[:], companion.StakeID[:])
	require.ErrorIs(t, err, ErrNotFound)
	pending, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, pending.EVMStakeBindings)
	require.Empty(t, pending.Delegations)
	require.Len(t, pending.EVMFrozenBindings, 1)
	ctx = ctx.WithBlockHeight(11).WithBlockTime(deadline)
	result, err := k.SettleMatureWithdrawalsLimit(ctx, 1, 1_000_000, k.ApplyWithdrawalCompletion)
	require.NoError(t, err)
	require.Equal(t, WithdrawalSettlementResult{Settled: 1}, result)
	settled, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, settled.Undelegations)
	require.False(t, settled.EVMFrozenBindings[0].Active)
}
