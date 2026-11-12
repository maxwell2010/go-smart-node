package keeper

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cryptoencoding "github.com/cometbft/cometbft/crypto/encoding"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmtstate "github.com/cometbft/cometbft/state"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func initialABCIUpdates(t *testing.T, set *cmttypes.ValidatorSet) []abci.ValidatorUpdate {
	t.Helper()
	updates := make([]abci.ValidatorUpdate, 0, len(set.Validators))
	for _, v := range set.Validators {
		key, err := cryptoencoding.PubKeyToProto(v.PubKey)
		require.NoError(t, err)
		updates = append(updates, abci.ValidatorUpdate{PubKey: key, Power: v.VotingPower})
	}
	return updates
}

func cometPreviousCommit(height, initial int64, previous *cmttypes.ValidatorSet) abci.CommitInfo {
	block := &cmttypes.Block{Header: cmttypes.Header{Height: height}}
	if height != initial {
		block.LastCommit = &cmttypes.Commit{Height: height - 1, Signatures: make([]cmttypes.CommitSig, len(previous.Validators))}
		for i := range block.LastCommit.Signatures {
			block.LastCommit.Signatures[i].BlockIDFlag = cmttypes.BlockIDFlagCommit
		}
	}
	return cmtstate.BuildLastCommitInfo(block, previous, initial)
}

func TestNativeABCIConsensusThroughSDKFinalizeBlockAndRestart(t *testing.T) {
	const initial = int64(5)
	const chain = "native-abci-consensus-1"
	directory := t.TempDir()
	g := genesis(t)
	g.Params.HistoricalEntries = 10
	g.Params.SignedBlocksWindow = 2
	g.Validators[1].Stake = 400
	g.LastValidatorPowers[1].Power, g.LastTotalPower = 400, 500
	old, err := ConsensusSet(g.Validators)
	require.NoError(t, err)
	initialUpdates := initialABCIUpdates(t, old)
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	changed, removed := g.Validators[0], g.Validators[1]
	changed.Stake, removed.Jailed = 150, true

	open := func() (*baseapp.BaseApp, *StateKeeper) {
		database, err := dbm.NewDB("native-abci-consensus", dbm.GoLevelDBBackend, directory)
		require.NoError(t, err)
		app := baseapp.NewBaseApp("native-abci-consensus", log.NewNopLogger(), database, nil, baseapp.SetChainID(chain))
		key := storetypes.NewKVStoreKey("native-validator")
		app.MountStores(key)
		k, err := NewStateKeeper(runtime.NewKVStoreService(key))
		require.NoError(t, err)
		app.SetInitChainer(func(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
			require.Equal(t, initial, ctx.BlockHeight(), "SDK uses nonzero InitialHeight inside InitChain")
			require.NoError(t, k.InitGenesis(ctx, g))
			require.NoError(t, k.BootstrapABCIConsensus(ctx, req.InitialHeight, g.Validators, initialUpdates))
			return &abci.ResponseInitChain{Validators: initialUpdates}, nil
		})
		app.SetBeginBlocker(func(ctx sdk.Context) (sdk.BeginBlock, error) {
			commit, err := k.RecordABCICommit(ctx)
			require.NoError(t, err)
			switch ctx.BlockHeight() {
			case initial:
				require.Empty(t, commit)
			case initial + 1, initial + 2:
				flag := cmtproto.BlockIDFlagAbsent
				if ctx.BlockHeight() == initial+2 {
					flag = cmtproto.BlockIDFlagNil
				}
				require.Equal(t, ctx.BlockHeight()-1, commit.Height)
				require.Equal(t, []VerifiedABCIVote{
					{Operator: removed.OperatorAddress, Power: 400, Flag: cmtproto.BlockIDFlagCommit},
					{Operator: changed.OperatorAddress, Power: 100, Flag: flag},
				}, commit.Votes)
			case initial + 3:
				require.Equal(t, initial+2, commit.Height)
				require.Equal(t, []VerifiedABCIVote{
					{Operator: changed.OperatorAddress, Power: 150, Flag: cmtproto.BlockIDFlagCommit},
				}, commit.Votes)
			}
			return sdk.BeginBlock{}, nil
		})
		app.SetEndBlocker(func(ctx sdk.Context) (sdk.EndBlock, error) {
			require.Empty(t, ctx.BlockHeader().ValidatorsHash, "SDK FinalizeBlock does not carry current ValidatorsHash")
			if ctx.BlockHeight() == initial {
				require.NoError(t, k.SetValidator(ctx, changed))
				require.NoError(t, k.SetValidator(ctx, removed))
			}
			updates, err := k.ScheduledABCIEndBlock(ctx, eligible)
			return sdk.EndBlock{ValidatorUpdates: updates}, err
		})
		require.NoError(t, app.LoadLatestVersion())
		return app, k
	}

	app, k := open()
	genesisTime := time.Unix(2000, 0).UTC()
	response, err := app.InitChain(&abci.RequestInitChain{ChainId: chain, InitialHeight: initial, Time: genesisTime, Validators: initialUpdates})
	require.NoError(t, err)
	require.Equal(t, initialUpdates, response.Validators)
	current, next := old, old.Copy()
	var previous *cmttypes.ValidatorSet
	for height := initial; height <= initial+3; height++ {
		if height == initial+1 {
			require.NoError(t, app.Close())
			app, k = open()
			startup := app.NewUncachedContext(false, cmtproto.Header{ChainID: chain, Height: height - 1})
			require.NoError(t, k.VerifyABCIConsensusStartup(startup, height-1))
		}
		previousCommit := cometPreviousCommit(height, initial, previous)
		if height == initial+1 {
			// The second validator's absence retains its address and power in ABCI.
			previousCommit.Votes[1].BlockIdFlag = cmtproto.BlockIDFlagAbsent
		} else if height == initial+2 {
			previousCommit.Votes[1].BlockIdFlag = cmtproto.BlockIDFlagNil
		}
		blockHash := sha256.Sum256(sdk.Uint64ToBigEndian(uint64(height)))
		block, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{
			Height: height, Time: genesisTime.Add(time.Duration(height-initial) * time.Second),
			Hash: blockHash[:], NextValidatorsHash: next.Hash(), ProposerAddress: current.Validators[0].Address,
			DecidedLastCommit: previousCommit,
		})
		require.NoError(t, err)
		if height == initial {
			require.Len(t, block.ValidatorUpdates, 2)
		} else {
			require.Empty(t, block.ValidatorUpdates)
		}
		_, err = app.Commit()
		require.NoError(t, err)
		committed := app.NewUncachedContext(false, cmtproto.Header{ChainID: chain, Height: height})
		history, err := k.GetHistoricalInfo(committed, height)
		require.NoError(t, err)
		require.Equal(t, current.Hash(), history.Header.ValidatorsHash)
		require.Equal(t, next.Hash(), history.Header.NextValidatorsHash)
		if height <= initial+1 {
			require.Equal(t, old.Hash(), current.Hash())
		} else {
			require.NotEqual(t, old.Hash(), current.Hash())
		}
		require.NoError(t, k.VerifyABCIConsensusStartup(committed, height))

		future := next.Copy()
		if len(block.ValidatorUpdates) != 0 {
			changes, err := cmttypes.PB2TM.ValidatorUpdates(block.ValidatorUpdates)
			require.NoError(t, err)
			require.NoError(t, future.UpdateWithChangeSet(changes))
		}
		previous, current, next = current, next, future
	}
	committed := app.NewUncachedContext(false, cmtproto.Header{ChainID: chain, Height: initial + 3})
	window, err := k.GetSigningWindow(committed, changed.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, SigningWindow{LastHeight: initial + 2, Size: 2, Samples: 2, Missed: 1}, window)
	window, err = k.GetSigningWindow(committed, removed.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, SigningWindow{LastHeight: initial + 1, Size: 2, Samples: 2}, window)
	require.NoError(t, app.Close())
}

func TestRecordABCICommitRejectsReplayAndRollsBackWindowsOnCheckpointError(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	set, err := ConsensusSet(g.Validators)
	require.NoError(t, err)
	require.NoError(t, k.BootstrapABCIConsensus(ctx, 1, g.Validators, initialABCIUpdates(t, set)))
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	block := func(height int64) sdk.Context {
		return ctx.WithExecMode(sdk.ExecModeFinalize).WithBlockHeader(cmtproto.Header{
			Height: height, ChainID: "native-observation-1", Time: time.Unix(2000+height, 0).UTC(),
			ProposerAddress: set.Validators[0].Address, NextValidatorsHash: set.Hash(),
		}).WithHeaderHash(bytes.Repeat([]byte{byte(height)}, 32)).WithVoteInfos(cometPreviousCommit(height, 1, set).Votes)
	}
	h1 := block(1)
	_, err = k.RecordABCICommit(h1.WithExecMode(sdk.ExecModeProcessProposal))
	require.ErrorContains(t, err, "requires FinalizeBlock")
	first, err := k.RecordABCICommit(h1)
	require.NoError(t, err)
	require.Empty(t, first)
	_, err = k.RecordABCICommit(h1)
	require.ErrorContains(t, err, "replayed or skipped")
	_, err = k.ScheduledABCIEndBlock(h1, eligible)
	require.NoError(t, err)
	h2 := block(2)
	wrongVotes := append([]abci.VoteInfo(nil), h2.VoteInfos()...)
	wrongVotes[0].Validator.Power++
	_, err = k.RecordABCICommit(h2.WithVoteInfos(wrongVotes))
	require.ErrorContains(t, err, "prior validator address or power")
	broken, err := NewStateKeeper(failingService{service: service, prefix: signingProgressKey})
	require.NoError(t, err)
	_, err = broken.RecordABCICommit(h2)
	require.ErrorContains(t, err, "injected write failure")
	for _, operator := range eligible {
		_, err := k.GetSigningWindow(h2, operator)
		require.ErrorIs(t, err, ErrNotFound)
	}
	observed, err := k.RecordABCICommit(h2)
	require.NoError(t, err)
	require.EqualValues(t, 1, observed.Height)
	require.Len(t, observed.Votes, 2)
	_, err = k.RecordABCICommit(h2)
	require.ErrorContains(t, err, "replayed or skipped")
	for _, operator := range eligible {
		window, err := k.GetSigningWindow(h2, operator)
		require.NoError(t, err)
		require.Equal(t, SigningWindow{LastHeight: 1, Size: 24, Samples: 1}, window)
	}
}

func TestNativeABCIConsensusRejectsMismatchedInputsAndAnchorWriteFailure(t *testing.T) {
	ctx, service, k := environment(t)
	g := genesis(t)
	require.NoError(t, k.InitGenesis(ctx, g))
	set, err := ConsensusSet(g.Validators)
	require.NoError(t, err)
	updates := initialABCIUpdates(t, set)
	badInit := append([]abci.ValidatorUpdate(nil), updates...)
	badInit[0].Power++
	require.ErrorContains(t, k.BootstrapABCIConsensus(ctx, 1, g.Validators, badInit), "key or power")
	require.NoError(t, k.BootstrapABCIConsensus(ctx, 1, g.Validators, updates))
	eligible := []string{g.Validators[0].OperatorAddress, g.Validators[1].OperatorAddress}
	h1 := cmtproto.Header{
		ChainID: "native-abci-consensus-1", Height: 1, Time: time.Unix(2000, 0).UTC(),
		NextValidatorsHash: set.Hash(), ProposerAddress: set.Validators[0].Address,
	}
	current := ctx.WithBlockHeader(h1).WithHeaderHash(bytes.Repeat([]byte{1}, 32))
	_, err = k.ScheduledEndBlock(current, eligible)
	require.ErrorContains(t, err, "requires the ABCI scheduled end block")
	wrongHeader := h1
	wrongHeader.NextValidatorsHash = bytes.Repeat([]byte{2}, 32)
	_, err = k.ScheduledABCIEndBlock(current.WithBlockHeader(wrongHeader), eligible)
	require.ErrorContains(t, err, "next validators hash")
	wrongHeader = h1
	wrongHeader.ProposerAddress = bytes.Repeat([]byte{9}, 20)
	_, err = k.ScheduledABCIEndBlock(current.WithBlockHeader(wrongHeader), eligible)
	require.ErrorContains(t, err, "effective proposer")
	_, err = k.ScheduledABCIEndBlock(current, eligible)
	require.NoError(t, err)

	previous := cometPreviousCommit(2, 1, set)
	h2 := h1
	h2.Height = 2
	h2.Time = h1.Time.Add(time.Second)
	next := current.WithBlockHeader(h2).WithVoteInfos(previous.Votes)
	wrongVotes := append([]abci.VoteInfo(nil), previous.Votes...)
	wrongVotes[0].Validator.Power++
	_, err = k.ScheduledABCIEndBlock(next.WithVoteInfos(wrongVotes), eligible)
	require.ErrorContains(t, err, "prior validator address or power")
	_, err = k.ScheduledABCIEndBlock(next.WithVoteInfos(nil), eligible)
	require.ErrorContains(t, err, "prior validator count")
	before, err := k.ExportGenesis(current)
	require.NoError(t, err)
	broken, err := NewStateKeeper(failingService{service: service, prefix: abciConsensusAnchorKey})
	require.NoError(t, err)
	_, err = broken.ScheduledABCIEndBlock(next, eligible)
	require.ErrorContains(t, err, "injected write failure")
	after, err := k.ExportGenesis(current)
	require.NoError(t, err)
	assertGenesisBytes(t, before, after)
	require.NoError(t, k.VerifyABCIConsensusStartup(current, 1))
	_, err = k.GetHistoricalInfo(current, 2)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = k.ScheduledABCIEndBlock(next, eligible)
	require.NoError(t, err)

	data, err := service.OpenKVStore(ctx).Get([]byte{abciConsensusAnchorKey})
	require.NoError(t, err)
	badAnchor := bytes.Clone(data)
	badAnchor[9] ^= 1
	require.NoError(t, service.OpenKVStore(ctx).Set([]byte{abciConsensusAnchorKey}, badAnchor))
	require.ErrorContains(t, k.VerifyABCIConsensusStartup(next, 2), "prior ABCI validator hash")
}
