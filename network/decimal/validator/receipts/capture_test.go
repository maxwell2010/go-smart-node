package receipts

import (
	"bytes"
	"errors"
	"math/big"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	nativekeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

const captureChain = "mintcandy_990001-1"

func captureWord(n int64) []byte   { return big.NewInt(n).FillBytes(make([]byte, 32)) }
func captureAddress(n byte) []byte { return bytes.Repeat([]byte{n}, 20) }

type captureAuth struct {
	profile StakeSourceProfile
	calls   int
	err     error
}

func (a *captureAuth) AuthenticateStakeSource(_ sdk.Context, _ nftreceipts.Execution) (StakeSourceProfile, error) {
	a.calls++
	return a.profile, a.err
}
func captureFixture(t *testing.T, frozen bool) (sdk.Context, *nativekeeper.StateKeeper, *captureAuth, types.EVMStakeBinding) {
	t.Helper()
	key := storetypes.NewKVStoreKey("native-capture")
	ctx := testutil.DefaultContext(key, storetypes.NewTransientStoreKey("transient")).WithChainID(captureChain).WithBlockHeight(10).WithBlockTime(time.Unix(2000, 0).UTC()).WithTxBytes([]byte("capture-tx"))
	k, err := nativekeeper.NewStateKeeperWithDomain(runtime.NewKVStoreService(key), captureChain, 990001)
	require.NoError(t, err)
	operator, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(captureAddress(1))
	require.NoError(t, err)
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(captureAddress(2))
	require.NoError(t, err)
	validator, err := types.NewValidator(operator, owner, ed25519.GenPrivKeyFromSecret([]byte("capture-consensus")).PubKey(), types.Description{Moniker: "capture"}, sdkmath.LegacyMustNewDecFromStr("0.1"))
	require.NoError(t, err)
	positionID := captureWord(1)
	positionID[5] = 1
	origin := &types.EVMNFTReference{Contract: captureAddress(7), TokenID: captureWord(0), NativeTokenID: "origin-native", NativeSubTokenIDs: []uint32{5, 9}, Standard: 1155}
	custody := &types.EVMNFTReference{Contract: captureAddress(8), TokenID: positionID, NativeTokenID: "receipt-native", NativeSubTokenIDs: []uint32{73}, Standard: 721}
	binding := types.EVMStakeBinding{Kind: types.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, Gateway: captureAddress(3), StakeID: captureWord(30), Validator: captureAddress(1), Beneficiary: captureAddress(2), TokenContract: captureAddress(8), TokenID: bytes.Clone(positionID), HoldTimestamp: captureWord(0), PenaltyCursor: captureWord(0), RawAmount: sdkmath.OneInt(), RawNftBackedAmount: sdkmath.ZeroInt(), ReportedReserveAmount: sdkmath.NewInt(20), ReserveAsset: &types.EVMReserveAsset{ReserveKind: 1, Token: make([]byte, 20), NativeDenom: identity.BaseDenom}, NFTPosition: &types.NFTPositionIdentity{Mode: types.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT, Origin: origin, Custody: custody, OriginQuantity: sdkmath.NewInt(2), ReceiptQuantity: sdkmath.OneInt(), Vault: captureAddress(8), PositionID: bytes.Clone(positionID), ParentPositionID: captureWord(0)}, NativeTarget: &types.NativeStakeTarget{StakeType: types.StakeType_NFT, ID: "receipt-native", SubTokenIDs: []uint32{73}}, Active: true, CreatedHeight: 1, UpdatedHeight: 1}
	g := types.DefaultGenesisState()
	g.BindingDomain = &types.EVMSourceDomain{ChainID: captureChain, EVMChainID: 990001}
	g.Validators = []types.Validator{validator}
	stake := types.NewStakeNFT("receipt-native", []uint32{73}, sdk.NewCoin(identity.BaseDenom, sdkmath.NewInt(20)))
	if !frozen {
		g.EVMStakeBindings = []types.EVMStakeBinding{binding}
		g.Delegations = []types.Delegation{{Delegator: owner, Validator: operator, Stake: stake}}
	} else {
		entry := types.UndelegationEntry{CreationHeight: 1, CompletionTime: time.Unix(3000, 0).UTC(), Stake: stake}
		hash, err := types.NativeQueueEntryHash(1, owner, operator, "", &entry)
		require.NoError(t, err)
		g.Undelegations = []types.Undelegation{{Delegator: owner, Validator: operator, Entries: []types.UndelegationEntry{entry}}}
		g.EVMFrozenBindings = []types.EVMFrozenBinding{{Gateway: binding.Gateway, FrozenIndex: captureWord(0), SourceKind: binding.Kind, SourceStakeID: binding.StakeID, SourceValidator: binding.Validator, Beneficiary: binding.Beneficiary, FreezeType: 1, CompletionTimestamp: captureWord(3000), RawAmount: sdkmath.OneInt(), ReportedReserveAmount: binding.ReportedReserveAmount, ReserveAsset: binding.ReserveAsset, NFTPosition: binding.NFTPosition, NativeTarget: binding.NativeTarget, NativeEntryHash: hash, Active: true, CreatedHeight: 1, UpdatedHeight: 1}}
	}
	require.NoError(t, k.InitGenesis(ctx, g))
	auth := &captureAuth{profile: StakeSourceProfile{Domain: types.EVMSourceDomain{ChainID: captureChain, EVMChainID: 990001}, Gateway: common.BytesToAddress(binding.Gateway), Fingerprint: crypto.Keccak256Hash([]byte("explicit source profile fixture, not an EVM authenticity proof"))}}
	return ctx, k, auth, binding
}
func captureMovement(binding types.EVMStakeBinding) (nftreceipts.Execution, []nftreceipts.Transfer) {
	gateway, owner, contract := common.BytesToAddress(binding.Gateway), common.BytesToAddress(binding.Beneficiary), common.BytesToAddress(binding.TokenContract)
	id := common.BytesToHash(binding.TokenID)
	log := &ethtypes.Log{Address: contract, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)")), common.BytesToHash(gateway.Bytes()), common.BytesToHash(owner.Bytes()), id}, BlockNumber: 10, TxHash: crypto.Keccak256Hash([]byte("capture"))}
	execution := nftreceipts.Execution{EntryPoint: nftreceipts.EntryPointSignedEthereum, Receipt: &ethtypes.Receipt{Status: 1, Logs: []*ethtypes.Log{log}}}
	moves := []nftreceipts.Transfer{{Ref: nftreceipts.MoveRef{LogPosition: 0, ItemPosition: 0}, Asset: nftreceipts.AssetKey{Contract: contract, TokenID: id}, NFTType: nfttypes.NftType_NFT721, Operator: gateway, From: gateway, To: owner, Quantity: 1}}
	return execution, moves
}
func TestCaptureUsesPersistedActiveAndFrozenReceiptUnits(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "frozen-index-zero"}[frozen], func(t *testing.T) {
			ctx, k, auth, binding := captureFixture(t, frozen)
			execution, moves := captureMovement(binding)
			before, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			session, err := NewStakeCaptureSession(k, auth)
			require.NoError(t, err)
			captured, err := session.Capture(ctx, execution, moves)
			require.NoError(t, err)
			require.Equal(t, []uint32{73}, captured.Selections[moves[0].Ref], "must not choose lowest wallet/escrow unit or origin IDs")
			require.NotEqual(t, common.Hash{}, captured.PrestateFingerprint)
			require.NoError(t, session.ValidateCapture(ctx, captured))
			execution.Receipt.Logs[0].Topics[3] = common.Hash{} // Session owns an independent receipt snapshot.
			require.NoError(t, session.ValidateCapture(ctx, captured))
			captured.Selections[moves[0].Ref][0] = 1
			require.Error(t, session.ValidateCapture(ctx, captured))
			captured.Selections[moves[0].Ref][0] = 73
			require.NoError(t, session.ValidateCapture(ctx, captured))
			require.Error(t, session.ValidateCapture(ctx.WithBlockHeight(11), captured))
			require.Error(t, session.ValidateCapture(ctx.WithTxBytes([]byte("another-tx")), captured))
			auth.profile.Fingerprint[0] ^= 1
			require.Error(t, session.ValidateCapture(ctx, captured))
			auth.profile.Fingerprint[0] ^= 1
			after, err := k.ExportGenesis(ctx)
			require.NoError(t, err)
			a, err := proto.Marshal(before)
			require.NoError(t, err)
			b, err := proto.Marshal(after)
			require.NoError(t, err)
			require.Equal(t, a, b, "capture and validation are read-only")
			if !frozen {
				binding.UpdatedHeight = 2
				plan, err := k.PrepareBindingPlan(ctx, []nativekeeper.BindingMutation{{Stake: &binding}})
				require.NoError(t, err)
				require.NoError(t, k.ApplyBindingPlan(ctx, plan))
				require.Error(t, session.ValidateCapture(ctx, captured))
			}
		})
	}
}
func TestCaptureRejectsForeignDomainQuantityDuplicatesAndUnboundedData(t *testing.T) {
	ctx, k, auth, binding := captureFixture(t, false)
	tests := map[string]func(*captureAuth, *nftreceipts.Execution, *[]nftreceipts.Transfer){
		"domain": func(a *captureAuth, _ *nftreceipts.Execution, _ *[]nftreceipts.Transfer) {
			a.profile.Domain.EVMChainID = 1
		},
		"noapprovalfingerprint": func(a *captureAuth, _ *nftreceipts.Execution, _ *[]nftreceipts.Transfer) {
			a.profile.Fingerprint = common.Hash{}
		},
		"quantity":  func(_ *captureAuth, _ *nftreceipts.Execution, m *[]nftreceipts.Transfer) { (*m)[0].Quantity = 2 },
		"duplicate": func(_ *captureAuth, _ *nftreceipts.Execution, m *[]nftreceipts.Transfer) { *m = append(*m, (*m)[0]) },
		"foreigncontract": func(_ *captureAuth, e *nftreceipts.Execution, _ *[]nftreceipts.Transfer) {
			e.Receipt.Logs[0].Address = common.Address{}
		},
		"unknownlog": func(_ *captureAuth, _ *nftreceipts.Execution, m *[]nftreceipts.Transfer) { (*m)[0].Ref.LogPosition = 1 },
		"oversized": func(_ *captureAuth, e *nftreceipts.Execution, _ *[]nftreceipts.Transfer) {
			e.Receipt.Logs[0].Data = make([]byte, maxCaptureReceiptBytes+1)
		},
		"unsupportedIBC": func(_ *captureAuth, e *nftreceipts.Execution, _ *[]nftreceipts.Transfer) {
			e.EntryPoint = nftreceipts.EntryPointIBC
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			execution, moves := captureMovement(binding)
			copied := *auth
			mutate(&copied, &execution, &moves)
			session, err := NewStakeCaptureSession(k, &copied)
			require.NoError(t, err)
			_, err = session.Capture(ctx, execution, moves)
			require.Error(t, err)
		})
	}
}
func TestCaptureNeverInventsUnitsForNewOrUnattributedEscrowTransfers(t *testing.T) {
	ctx, k, auth, binding := captureFixture(t, false)
	execution, moves := captureMovement(binding)
	moves[0].Asset.TokenID = common.BytesToHash(captureWord(99))
	execution.Receipt.Logs[0].Topics[3] = moves[0].Asset.TokenID
	session, err := NewStakeCaptureSession(k, auth)
	require.NoError(t, err)
	captured, err := session.Capture(ctx, execution, moves)
	require.NoError(t, err)
	require.Empty(t, captured.Selections)
	require.NoError(t, session.ValidateCapture(ctx, captured), "NFT planner must independently admit a typed new/transient position or reject this escrow move")
	_, err = session.Capture(ctx, execution, moves)
	require.Error(t, err)
}

type unreadableBindings struct{}

func (unreadableBindings) GetEVMSourceDomain(sdk.Context) (types.EVMSourceDomain, error) {
	panic("unexpected noop domain read")
}
func (unreadableBindings) LookupCustodyNFTBinding(sdk.Context, []byte, []byte) (nativekeeper.BindingAssignment, error) {
	panic("unexpected noop custody read")
}
func (unreadableBindings) CaptureBindingState(sdk.Context, []nativekeeper.BindingReference) ([]byte, error) {
	panic("unexpected noop capture read")
}
func (unreadableBindings) ValidateBindingCapture(sdk.Context, []byte) error {
	panic("unexpected noop validation read")
}
func TestEmptyDecodedMovementCaptureNeedsNoStoreOrEVMRead(t *testing.T) {
	ctx, _, auth, _ := captureFixture(t, false)
	auth.err = errors.New("unexpected noop EVM authentication")
	session, err := NewStakeCaptureSession(unreadableBindings{}, auth)
	require.NoError(t, err)
	before := ctx.GasMeter().GasConsumed()
	captured, err := session.Capture(ctx, nftreceipts.Execution{EntryPoint: nftreceipts.EntryPointSignedEthereum, Receipt: &ethtypes.Receipt{Status: 1}}, nil)
	require.NoError(t, err)
	require.Empty(t, captured.Selections)
	require.Equal(t, common.Hash{}, captured.PrestateFingerprint)
	require.NoError(t, session.ValidateCapture(ctx, captured))
	require.Zero(t, auth.calls)
	require.Equal(t, before, ctx.GasMeter().GasConsumed())
	var absent *captureAuth
	_, err = NewStakeCaptureSession(unreadableBindings{}, absent)
	require.Error(t, err)
	var missing *nativekeeper.StateKeeper
	_, err = NewStakeCaptureSession(missing, &captureAuth{})
	require.Error(t, err)
}

func TestCaptureFingerprintIncludesExecutionReceipt(t *testing.T) {
	ctx, k, auth, binding := captureFixture(t, false)
	firstExecution, moves := captureMovement(binding)
	secondExecution, _ := captureMovement(binding)
	firstExecution.Receipt.CumulativeGasUsed = 1
	secondExecution.Receipt.CumulativeGasUsed = 2
	first, err := NewStakeCaptureSession(k, auth)
	require.NoError(t, err)
	a, err := first.Capture(ctx, firstExecution, moves)
	require.NoError(t, err)
	second, err := NewStakeCaptureSession(k, auth)
	require.NoError(t, err)
	b, err := second.Capture(ctx, secondExecution, moves)
	require.NoError(t, err)
	require.Equal(t, a.Selections, b.Selections)
	require.NotEqual(t, a.PrestateFingerprint, b.PrestateFingerprint)
	require.Error(t, second.ValidateCapture(ctx, a))
	require.NoError(t, second.ValidateCapture(ctx, b))
}
