package receipts

import (
	"errors"
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/stretchr/testify/require"
)

type finalOwnershipFunc func(sdk.Context, CollectionProfile, AssetKey, []common.Address) (map[common.Address]uint32, error)

func (f finalOwnershipFunc) Ownership(c sdk.Context, p CollectionProfile, k AssetKey, o []common.Address) (map[common.Address]uint32, error) {
	return f(c, p, k, o)
}

type captureControl struct {
	capture  func(sdk.Context, Execution, []Transfer) (CapturedStakes, error)
	validate func(sdk.Context, CapturedStakes) error
}

func (c captureControl) Capture(ctx sdk.Context, e Execution, m []Transfer) (CapturedStakes, error) {
	if c.capture != nil {
		return c.capture(ctx, e, m)
	}
	return CapturedStakes{}, nil
}
func (c captureControl) ValidateCapture(ctx sdk.Context, s CapturedStakes) error {
	if c.validate != nil {
		return c.validate(ctx, s)
	}
	return nil
}

func ownershipFixture(t *testing.T) (sdk.Context, *nftkeeper.StateKeeper, *Decoder, abi.ABI, profileReader, common.Address, common.Address, common.Address, *big.Int) {
	t.Helper()
	_, decoder, _, a1155, profiles, _, contract := decoderFixture(t)
	key := storetypes.NewKVStoreKey("nft-plan")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient")).WithChainID("receipt-network-1")
	k, err := nftkeeper.NewStateKeeperWithDomain(runtime.NewKVStoreService(key), ctx.ChainID(), 777)
	require.NoError(t, err)
	from := common.HexToAddress("0x4444444444444444444444444444444444444444")
	to := common.HexToAddress("0x5555555555555555555555555555555555555555")
	fromText, err := nativeOwner(from)
	require.NoError(t, err)
	toText, err := nativeOwner(to)
	require.NoError(t, err)
	id := new(big.Int).Lsh(big.NewInt(1), 200)
	token := &types.Token{Creator: fromText, Denom: "bound", ID: "native_token", URI: "ipfs://shared", Reserve: sdk.NewCoin("del", sdkmath.OneInt()), Minted: 6}
	for index := uint32(1); index <= 6; index++ {
		owner := fromText
		if index > 3 {
			owner = toText
		}
		token.SubTokens = append(token.SubTokens, &types.SubToken{ID: index, Owner: owner})
	}
	genesis := types.NewGenesisState(types.DefaultParams(), []types.Collection{{Creator: fromText, Denom: "bound", TypeNft: types.NftType_NFT1155, AddressDRC: contract.Hex(), Supply: 1, Tokens: types.Tokens{token}}})
	genesis.ExternalBindings = []types.ExternalNFTBinding{{ChainID: ctx.ChainID(), EVMChainID: 777, Contract: contract.Bytes(), TokenID: common.BigToHash(id).Bytes(), NativeTokenID: token.ID, NFTType: types.NftType_NFT1155, NextSubTokenID: 7}}
	require.NoError(t, k.InitGenesis(ctx, genesis))
	return ctx, k, decoder, a1155, profiles, contract, from, to, id
}

func transferReceipt(t *testing.T, contract, from, to common.Address, id *big.Int, quantity int64, event abi.Event) *ethtypes.Log {
	t.Helper()
	data, err := event.Inputs.NonIndexed().Pack(id, big.NewInt(quantity))
	require.NoError(t, err)
	return &ethtypes.Log{Address: contract, Topics: []common.Hash{event.ID, common.BytesToHash(from.Bytes()), common.BytesToHash(from.Bytes()), common.BytesToHash(to.Bytes())}, Data: data}
}
func finalCounts(values map[common.Address]uint32) FinalReader {
	return finalOwnershipFunc(func(_ sdk.Context, _ CollectionProfile, _ AssetKey, owners []common.Address) (map[common.Address]uint32, error) {
		copy := map[common.Address]uint32{}
		for _, owner := range owners {
			copy[owner] = values[owner]
		}
		return copy, nil
	})
}
func unitOwner(t *testing.T, ctx sdk.Context, k *nftkeeper.StateKeeper, id uint32) common.Address {
	t.Helper()
	sub, err := k.GetSubToken(ctx, "native_token", id)
	require.NoError(t, err)
	owner, err := nativeAddress(sub.Owner)
	require.NoError(t, err)
	return owner
}

func TestNFTPlanVirtualOrderImmutableCopyAndOneUse(t *testing.T) {
	ctx, k, decoder, a1155, _, contract, from, to, id := ownershipFixture(t)
	capturedBeforeVirtual := false
	capture := captureControl{capture: func(_ sdk.Context, e Execution, moves []Transfer) (CapturedStakes, error) {
		capturedBeforeVirtual = unitOwner(t, ctx, k, 1) == from
		require.Len(t, moves, 2)
		e.Receipt.Logs[0].Data[0] ^= 1
		return CapturedStakes{}, nil
	}}
	planner, err := NewPlanner(decoder, k, finalCounts(map[common.Address]uint32{from: 2, to: 4}), capture)
	require.NoError(t, err)
	receipt := &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transferReceipt(t, contract, from, to, id, 2, a1155.Events["TransferSingle"]), transferReceipt(t, contract, to, from, id, 1, a1155.Events["TransferSingle"])}}
	plan, err := planner.Plan(ctx, Execution{EntryPoint: EntryPointGovernance, Receipt: receipt})
	require.NoError(t, err)
	require.True(t, capturedBeforeVirtual)
	moves := plan.Movements()
	require.Equal(t, []uint32{1, 2}, moves[0].SubTokenIDs)
	require.Equal(t, []uint32{1}, moves[1].SubTokenIDs)
	moves[0].SubTokenIDs[0] = 999
	receipt.Logs[0].Data = nil
	require.Equal(t, []uint32{1, 2}, plan.Movements()[0].SubTokenIDs)
	require.Equal(t, from, unitOwner(t, ctx, k, 2), "planning cannot mutate native ownership")
	require.NoError(t, planner.Apply(ctx, plan))
	require.Equal(t, from, unitOwner(t, ctx, k, 1))
	require.Equal(t, to, unitOwner(t, ctx, k, 2))
	copyPlan := *plan
	require.ErrorContains(t, planner.Apply(ctx, &copyPlan), "already applied")
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestNFTPlanEscrowUsesPersistedStakeUnits(t *testing.T) {
	ctx, k, decoder, a1155, profiles, contract, from, escrow, id := ownershipFixture(t)
	profile := profiles[contract]
	profile.Escrows = []common.Address{escrow}
	profiles[contract] = profile
	selection := []uint32{6, 4}
	capture := captureControl{capture: func(sdk.Context, Execution, []Transfer) (CapturedStakes, error) {
		return CapturedStakes{Selections: map[MoveRef][]uint32{{LogPosition: 0}: selection}, PrestateFingerprint: common.HexToHash("0x01")}, nil
	}}
	planner, err := NewPlanner(decoder, k, finalCounts(map[common.Address]uint32{from: 5, escrow: 1}), capture)
	require.NoError(t, err)
	e := Execution{EntryPoint: EntryPointGovernance, Receipt: &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transferReceipt(t, contract, escrow, from, id, 2, a1155.Events["TransferSingle"])}}}
	plan, err := planner.Plan(ctx, e)
	require.NoError(t, err)
	require.Equal(t, []uint32{6, 4}, plan.Movements()[0].SubTokenIDs, "withdraw must not choose arbitrary lowest escrow units")
	selection[0] = 5
	require.NoError(t, planner.Apply(ctx, plan))
	require.Equal(t, from, unitOwner(t, ctx, k, 6))
	require.Equal(t, from, unitOwner(t, ctx, k, 4))
	require.Equal(t, escrow, unitOwner(t, ctx, k, 5))
	ctx, k, decoder, a1155, profiles, contract, from, escrow, id = ownershipFixture(t)
	profile = profiles[contract]
	profile.Escrows = []common.Address{escrow}
	profiles[contract] = profile
	planner, err = NewPlanner(decoder, k, finalCounts(map[common.Address]uint32{from: 5, escrow: 1}), captureControl{})
	require.NoError(t, err)
	e.Receipt.Logs = []*ethtypes.Log{transferReceipt(t, contract, escrow, from, id, 2, a1155.Events["TransferSingle"])}
	_, err = planner.Plan(ctx, e)
	require.ErrorContains(t, err, "exact persisted stake")
}

func TestNFTPlanRejectsStaleStateAndFinalMismatch(t *testing.T) {
	ctx, k, decoder, a1155, _, contract, from, to, id := ownershipFixture(t)
	e := Execution{EntryPoint: EntryPointGovernance, Receipt: &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transferReceipt(t, contract, from, to, id, 1, a1155.Events["TransferSingle"])}}}
	planner, err := NewPlanner(decoder, k, finalCounts(map[common.Address]uint32{from: 3, to: 3}), captureControl{})
	require.NoError(t, err)
	_, err = planner.Plan(ctx, e)
	require.ErrorContains(t, err, "final actual contract state")
	planner, err = NewPlanner(decoder, k, finalCounts(map[common.Address]uint32{from: 2, to: 4}), captureControl{})
	require.NoError(t, err)
	plan, err := planner.Plan(ctx, e)
	require.NoError(t, err)
	fromText, _ := nativeOwner(from)
	toText, _ := nativeOwner(to)
	require.NoError(t, k.TransferSubTokens(ctx, "native_token", []uint32{3}, fromText, toText))
	require.ErrorContains(t, planner.Apply(ctx, plan), "prestate changed")
	require.Equal(t, from, unitOwner(t, ctx, k, 1))
}

type failLaterTransfer struct {
	NativeState
	calls int
}

func (s *failLaterTransfer) TransferSubTokens(ctx sdk.Context, id string, ids []uint32, from, to string) error {
	s.calls++
	if s.calls == 2 {
		return errors.New("ordinary later native transfer validation failed")
	}
	return s.NativeState.TransferSubTokens(ctx, id, ids, from, to)
}

func TestNFTPlanLaterNativeErrorRollsBackAllOwnersAndEvents(t *testing.T) {
	ctx, k, decoder, a1155, _, contract, from, to, id := ownershipFixture(t)
	state := &failLaterTransfer{NativeState: k}
	planner, err := NewPlanner(decoder, state, finalCounts(map[common.Address]uint32{from: 1, to: 5}), captureControl{})
	require.NoError(t, err)
	e := Execution{EntryPoint: EntryPointGovernance, Receipt: &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transferReceipt(t, contract, from, to, id, 1, a1155.Events["TransferSingle"]), transferReceipt(t, contract, from, to, id, 1, a1155.Events["TransferSingle"])}}}
	plan, err := planner.Plan(ctx, e)
	require.NoError(t, err)
	require.ErrorContains(t, planner.Apply(ctx, plan), "later native transfer")
	require.Equal(t, from, unitOwner(t, ctx, k, 1))
	require.Equal(t, from, unitOwner(t, ctx, k, 2))
	require.Empty(t, ctx.EventManager().Events())
	_, err = k.ExportGenesis(ctx)
	require.NoError(t, err)
}

func TestNFTPlanRevalidatesApprovalsStakeCaptureAndFinalOwnership(t *testing.T) {
	for _, kind := range []string{"approval", "stake", "final ownership"} {
		t.Run(kind, func(t *testing.T) {
			ctx, k, decoder, a1155, profiles, contract, from, to, id := ownershipFixture(t)
			counts := map[common.Address]uint32{from: 2, to: 4}
			staleStake := false
			capture := captureControl{validate: func(sdk.Context, CapturedStakes) error {
				if staleStake {
					return errors.New("stake fingerprint changed")
				}
				return nil
			}}
			planner, err := NewPlanner(decoder, k, finalCounts(counts), capture)
			require.NoError(t, err)
			e := Execution{EntryPoint: EntryPointGovernance, Receipt: &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transferReceipt(t, contract, from, to, id, 1, a1155.Events["TransferSingle"])}}}
			plan, err := planner.Plan(ctx, e)
			require.NoError(t, err)
			switch kind {
			case "approval":
				profile := profiles[contract]
				profile.ImplementationCodeHash = common.HexToHash("0x03")
				profiles[contract] = profile
			case "stake":
				staleStake = true
			case "final ownership":
				counts[from] = 3
			}
			require.Error(t, planner.Apply(ctx, plan))
			require.Equal(t, from, unitOwner(t, ctx, k, 1))
			require.Empty(t, ctx.EventManager().Events())
		})
	}
}

func TestNFTPlanZeroInternalHashDoesNotCollideAndFailedReceiptSkipsReaders(t *testing.T) {
	ctx, k, decoder, a1155, _, contract, from, to, id := ownershipFixture(t)
	counts := map[common.Address]uint32{from: 2, to: 4}
	planner, err := NewPlanner(decoder, k, finalCounts(counts), captureControl{})
	require.NoError(t, err)
	e := Execution{EntryPoint: EntryPointGovernance, Receipt: &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{transferReceipt(t, contract, from, to, id, 1, a1155.Events["TransferSingle"])}}}
	first, err := planner.Plan(ctx, e)
	require.NoError(t, err)
	require.NoError(t, planner.Apply(ctx, first))
	counts[from], counts[to] = 1, 5
	second, err := planner.Plan(ctx, e)
	require.NoError(t, err)
	require.Equal(t, []uint32{2}, second.Movements()[0].SubTokenIDs)
	require.NoError(t, planner.Apply(ctx, second), "separate internal executions can share a zero transaction hash")
	guard := captureControl{capture: func(sdk.Context, Execution, []Transfer) (CapturedStakes, error) {
		t.Fatal("failed receipt cannot capture stake bindings")
		return CapturedStakes{}, nil
	}}
	planner, err = NewPlanner(decoder, k, finalOwnershipFunc(func(sdk.Context, CollectionProfile, AssetKey, []common.Address) (map[common.Address]uint32, error) {
		t.Fatal("failed receipt cannot read final ownership")
		return nil, nil
	}), guard)
	require.NoError(t, err)
	e.Receipt.Status = 0
	failed, err := planner.Plan(ctx, e)
	require.NoError(t, err)
	require.NoError(t, planner.Apply(ctx, failed))
	require.Empty(t, failed.Movements())
}
