package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"sync"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

type withdrawalStateSync struct {
	mu       sync.Mutex
	prepared bool
	offer    *withdrawalSnapshotOffer
}

type withdrawalSnapshotOffer struct {
	height uint64
	hash   []byte
	chunks uint32
	next   uint32
}

// PrepareStateSyncGenesis installs the local genesis profile before an embedded
// CometBFT node starts. Snapshot restoration must attest the same profile.
func (app *App) PrepareStateSyncGenesis(state GenesisState) error {
	syncState := &app.stateSync
	syncState.mu.Lock()
	defer syncState.mu.Unlock()

	if syncState.prepared || syncState.offer != nil || app.withdrawals != nil || app.LastBlockHeight() != 0 {
		return errors.New("state sync genesis must be prepared once on an empty application")
	}
	if err := ValidateNativeGenesisAllocation(app.appCodec, state); err != nil {
		return err
	}
	if err := app.validateNativeBindingGenesis(state); err != nil {
		return err
	}
	profile, err := parseWithdrawalGenesisProfile(state[WithdrawalGenesisKey])
	if err != nil {
		return err
	}
	if profile != nil {
		profile.SettlementOnly = true
		controller, err := app.newWithdrawalController(*profile)
		if err != nil {
			return err
		}
		app.installWithdrawalController(controller)
	}
	syncState.prepared = true
	return nil
}

func (app *App) OfferSnapshot(req *abci.RequestOfferSnapshot) (*abci.ResponseOfferSnapshot, error) {
	syncState := &app.stateSync
	syncState.mu.Lock()
	defer syncState.mu.Unlock()

	if req == nil {
		return &abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_REJECT}, nil
	}
	if syncState.prepared && req.Snapshot != nil && (app.LastBlockHeight() != 0 ||
		req.Snapshot.Height == 0 || req.Snapshot.Height > math.MaxInt64 || len(req.AppHash) != sha256.Size) {
		app.Logger().Error("state sync requires an empty application and a trusted snapshot height and app hash")
		return &abci.ResponseOfferSnapshot{Result: abci.ResponseOfferSnapshot_ABORT}, nil
	}
	response, err := app.BaseApp.OfferSnapshot(req)
	if err == nil && response.Result == abci.ResponseOfferSnapshot_ACCEPT {
		syncState.offer = &withdrawalSnapshotOffer{
			height: req.Snapshot.Height, hash: bytes.Clone(req.AppHash), chunks: req.Snapshot.Chunks,
		}
	}
	return response, err
}

func (app *App) ApplySnapshotChunk(req *abci.RequestApplySnapshotChunk) (*abci.ResponseApplySnapshotChunk, error) {
	syncState := &app.stateSync
	syncState.mu.Lock()
	defer syncState.mu.Unlock()

	offer := syncState.offer
	if req == nil || offer == nil || req.Index != offer.next || req.Index >= offer.chunks {
		syncState.offer = nil
		app.Logger().Error("received unexpected state sync snapshot chunk")
		return &abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_ABORT}, nil
	}
	response, err := app.BaseApp.ApplySnapshotChunk(req)
	if err != nil || response.Result == abci.ResponseApplySnapshotChunk_ABORT {
		syncState.offer = nil
		return response, err
	}
	if response.Result != abci.ResponseApplySnapshotChunk_ACCEPT {
		return response, nil
	}
	offer.next++
	if offer.next == offer.chunks {
		// The SDK returns ACCEPT for the last valid chunk only after the
		// multistore and all registered snapshot extensions finish restoring.
		syncState.offer = nil
		if err := app.verifyRestoredWithdrawalProfile(offer); err != nil {
			app.Logger().Error("state sync snapshot does not match the configured application", "err", err)
			return &abci.ResponseApplySnapshotChunk{Result: abci.ResponseApplySnapshotChunk_ABORT}, nil
		}
	}
	return response, nil
}

func (app *App) verifyRestoredWithdrawalProfile(offer *withdrawalSnapshotOffer) error {
	commit := app.LastCommitID()
	if app.stateSync.prepared && (commit.Version < 0 || uint64(commit.Version) != offer.height || !bytes.Equal(commit.Hash, offer.hash)) {
		return errors.New("restored application height or hash differs from the trusted snapshot")
	}
	ctx := app.NewUncachedContext(false, cmtproto.Header{ChainID: app.chainID, Height: commit.Version})
	raw := ctx.KVStore(app.keys[validatortypes.StoreKey]).Get([]byte{WithdrawalProfileKey})
	if app.withdrawals == nil {
		if raw != nil {
			return errors.New("restored withdrawal profile was not prepared before state sync")
		}
		return nil
	}
	expected, err := json.Marshal(app.withdrawals.profile)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, expected) {
		return errors.New("restored withdrawal profile differs from the configured withdrawal profile")
	}
	return nil
}
