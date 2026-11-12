package app

import (
	"bytes"
	"testing"
	"time"

	"cosmossdk.io/core/comet"
	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func withdrawalPayoutFixture(t *testing.T) (*withdrawalController, sdk.Context, validatortypes.EVMFrozenBinding, sdk.ConsAddress) {
	t.Helper()
	app := newWithdrawalSnapshotTestApp(t)
	const chain = "mintcandy_990001-1"
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: chain, Height: 1, Time: time.Unix(2000, 0).UTC()})
	keeper, err := validatorkeeper.NewStateKeeperWithDomain(runtime.NewKVStoreService(app.keys[validatortypes.StoreKey]), chain, 990001)
	require.NoError(t, err)
	require.NoError(t, keeper.InitGenesis(ctx, validatortypes.DefaultGenesisState()))
	position := validatorkeeper.GatewayDELStake{
		Gateway: common.Address{19: 1}, WrappedDEL: common.Address{19: 2},
		Beneficiary: common.Address{19: 3}, Validator: common.Address{19: 4}, Amount: sdkmath.NewInt(20),
	}
	owner, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(position.Beneficiary[:])
	require.NoError(t, err)
	operator, err := keeper.ValidatorAddressCodec().BytesToString(position.Validator[:])
	require.NoError(t, err)
	publicKey := &ed25519.PubKey{Key: bytes.Repeat([]byte{0x42}, ed25519.PubKeySize)}
	validator, err := validatortypes.NewValidator(operator, owner, publicKey, validatortypes.Description{Moniker: "withdrawal-finality"}, sdkmath.LegacyZeroDec())
	require.NoError(t, err)
	require.NoError(t, keeper.SetValidator(ctx, validator))
	consensus, err := validator.GetConsAddr()
	require.NoError(t, err)
	position.StakeID = crypto.Keccak256Hash(position.Validator[:], position.Beneficiary[:], position.WrappedDEL[:], make([]byte, 64))
	require.NoError(t, keeper.ApplyGatewayDELDelegation(ctx, position, position.Amount))
	require.NoError(t, keeper.InitializeDELProvenance(ctx.WithBlockHeight(0), position.Gateway, position.WrappedDEL))
	require.NoError(t, keeper.RecordNativeEvidenceBlock(ctx))

	params := cmtproto.ConsensusParams{Evidence: &cmtproto.EvidenceParams{MaxAgeNumBlocks: 1, MaxAgeDuration: 2 * time.Second}}
	ctx = ctx.WithBlockHeight(2).WithBlockTime(time.Unix(2001, 123).UTC()).
		WithExecMode(sdk.ExecModeFinalize).WithConsensusParams(params)
	position.Amount = position.Amount.SubRaw(3)
	index := common.Hash{31: 1}
	require.NoError(t, keeper.ApplyGatewayDELWithdrawal(ctx, position, index, sdkmath.NewInt(3), time.Unix(2002, 0).UTC()))
	require.NoError(t, keeper.RecordNativeEvidenceBlock(ctx))
	row, err := keeper.GetEVMFrozenBinding(ctx, position.Gateway[:], index[:])
	require.NoError(t, err)
	controller := &withdrawalController{keeper: keeper, consensusParams: func(sdk.Context) cmtproto.ConsensusParams { return params }}
	return controller, ctx, row, consensus
}

func TestWithdrawalPayoutUsesCommittedEvidenceClock(t *testing.T) {
	c, created, row, _ := withdrawalPayoutFixture(t)
	ctx := created.WithBlockHeight(3).WithBlockTime(created.BlockTime().Add(2 * time.Second))
	require.NoError(t, c.keeper.RecordNativeEvidenceBlock(ctx))
	next := ctx.WithBlockHeight(4).WithBlockTime(ctx.BlockTime().Add(time.Nanosecond))
	require.ErrorIs(t, c.checkWithdrawalPayout(next, row), validatorkeeper.ErrWithdrawalAwaitingEvidenceFinality)
	// The due automatic claim waits before the EVM executor is called.
	require.NoError(t, c.settle(next))
	stillFrozen, err := c.keeper.GetEVMFrozenBinding(next, row.Gateway, row.FrozenIndex)
	require.NoError(t, err)
	require.True(t, stillFrozen.Active)

	for _, simulation := range []sdk.Context{
		ctx.WithExecMode(sdk.ExecModeSimulate),
		ctx.WithIsCheckTx(true).WithConsensusParams(cmtproto.ConsensusParams{}),
	} {
		require.ErrorIs(t, c.checkWithdrawalPayout(simulation, row), validatorkeeper.ErrWithdrawalAwaitingEvidenceFinality)
	}
	require.NoError(t, c.keeper.RecordNativeEvidenceBlock(next))
	require.NoError(t, c.checkWithdrawalPayout(next.WithExecMode(sdk.ExecModeSimulate), row))
	require.NoError(t, c.checkWithdrawalPayout(next.WithIsCheckTx(true).WithConsensusParams(cmtproto.ConsensusParams{}), row))
	require.NoError(t, c.checkWithdrawalPayout(next.WithBlockHeight(5).WithBlockTime(next.BlockTime().Add(time.Nanosecond)), row))
	badSnapshot := next.WithExecMode(sdk.ExecModeSimulate).WithBlockTime(next.BlockTime().Add(time.Nanosecond))
	require.ErrorContains(t, c.checkWithdrawalPayout(badSnapshot, row), "disagrees with the committed snapshot")
}

func TestWithdrawalPayoutWaitsForPendingValidatorPenalty(t *testing.T) {
	c, created, row, consensus := withdrawalPayoutFixture(t)
	ctx := created.WithBlockHeight(3).WithBlockTime(created.BlockTime().Add(time.Second)).
		WithCometInfo(payoutTestBlock{evidence: payoutTestEvidenceList{{
			at: validatorkeeper.CommittedEvidenceBlock{Height: created.BlockHeight(), Time: created.BlockTime()}, consensus: consensus,
		}}})
	count, err := c.keeper.EnqueueNativeDuplicateVotePenalties(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.NoError(t, c.keeper.RecordNativeEvidenceBlock(ctx))
	ctx = ctx.WithBlockHeight(4).WithBlockTime(ctx.BlockTime().Add(2 * time.Second))
	require.NoError(t, c.keeper.RecordNativeEvidenceBlock(ctx))
	ctx = ctx.WithBlockHeight(5).WithBlockTime(ctx.BlockTime().Add(time.Second))
	require.ErrorContains(t, c.checkWithdrawalPayout(ctx, row), "validator has an unsettled penalty")
	jobs, err := c.keeper.PendingNativeDuplicateVotePenalties(ctx, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.NoError(t, c.keeper.CompleteNativeDuplicateVotePenalty(ctx, jobs[0]))
	require.NoError(t, c.checkWithdrawalPayout(ctx, row))
}

type payoutTestBlock struct {
	comet.BlockInfo
	evidence payoutTestEvidenceList
}

func (b payoutTestBlock) GetEvidence() comet.EvidenceList { return b.evidence }

type payoutTestEvidenceList []payoutTestEvidence

func (l payoutTestEvidenceList) Len() int                 { return len(l) }
func (l payoutTestEvidenceList) Get(i int) comet.Evidence { return l[i] }

type payoutTestEvidence struct {
	at        validatorkeeper.CommittedEvidenceBlock
	consensus sdk.ConsAddress
}

func (e payoutTestEvidence) Type() comet.MisbehaviorType { return comet.DuplicateVote }
func (e payoutTestEvidence) Height() int64               { return e.at.Height }
func (e payoutTestEvidence) Time() time.Time             { return e.at.Time }
func (e payoutTestEvidence) TotalVotingPower() int64     { return 1 }
func (e payoutTestEvidence) Validator() comet.Validator  { return payoutTestValidator(e.consensus) }

type payoutTestValidator sdk.ConsAddress

func (v payoutTestValidator) Address() []byte { return v }
func (v payoutTestValidator) Power() int64    { return 1 }
