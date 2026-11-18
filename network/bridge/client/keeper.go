// Package client owns persistent, latest-only Comet light-client state in an SDK
// KV store. Initial trust is provisioned through trusted module/genesis APIs.
// There are deliberately no messages, HTTP inputs, RPC, or application wiring.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	corestore "cosmossdk.io/core/store"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/maxwell2010/go-smart-node/network/bridge/finality"
)

const (
	maxRecordBytes = 2 * 1024 * 1024
	maxBlockBytes  = 1024 * 1024
	recordVersion  = 1
	clientPrefix   = "bridge/client/v1/"
)

var (
	ErrNotFound           = errors.New("bridge light client not found")
	ErrAlreadyInitialized = errors.New("bridge light client already initialized")
	ErrCorruptStore       = errors.New("corrupt bridge light-client state")
	ErrInvalidClientID    = errors.New("invalid bridge client ID")
	clientIDPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

// Keeper owns its client-prefixed records in the module-scoped store service.
// The application must never expose InitClient to an arbitrary
// transaction or untrusted external checkpoint. Update accepts only a candidate
// block, and loads all authority, policy and current trust from this store.
type Keeper struct{ service corestore.KVStoreService }

// Each client is one versioned record. Updating it replaces the previous block
// and root; historical proof roots are intentionally NOT retained or accepted.
type persistedRecord struct {
	Version         int                         `json:"version"`
	ClientID        string                      `json:"client_id"`
	BootstrapAnchor finality.Checkpoint         `json:"bootstrap_anchor"`
	Policy          finality.Policy             `json:"policy"`
	Latest          finality.VerifiedCheckpoint `json:"latest"`
	LightBlock      []byte                      `json:"light_block_proto"`
}

func NewKeeper(service corestore.KVStoreService) (*Keeper, error) {
	if service == nil {
		return nil, errors.New("bridge client requires a KV store service")
	}
	return &Keeper{service: service}, nil
}

func clientKey(id string) ([]byte, error) {
	if !clientIDPattern.MatchString(id) {
		return nil, ErrInvalidClientID
	}
	return append([]byte(clientPrefix), []byte(id)...), nil
}

func blockBytes(block *types.LightBlock) ([]byte, error) {
	pb, err := block.ToProto()
	if err != nil {
		return nil, err
	}
	if pb.Size() > maxBlockBytes {
		return nil, errors.New("light block exceeds persistent bound")
	}
	return pb.Marshal()
}

func sameCheckpoint(a, b finality.VerifiedCheckpoint) bool {
	return a.Checkpoint == b.Checkpoint && a.Time.Equal(b.Time) && a.AppHash == b.AppHash &&
		a.NextValidatorsHash == b.NextValidatorsHash
}

func encodeRecord(record persistedRecord) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(data) > maxRecordBytes {
		return nil, errors.New("client record exceeds persistent bound")
	}
	return data, nil
}

// load verifies stored syntax, canonical encoding and cryptographic consistency.
// It does not treat persisted historical data as fresh: public getters and
// Update separately enforce the current SDK BlockTime against the trust period.
func (k *Keeper) load(ctx sdk.Context, id string) (persistedRecord, *types.LightBlock, error) {
	var zero persistedRecord
	key, err := clientKey(id)
	if err != nil {
		return zero, nil, err
	}
	if k == nil || k.service == nil {
		return zero, nil, errors.New("nil bridge client keeper")
	}
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return zero, nil, err
	}
	if data == nil {
		return zero, nil, ErrNotFound
	}
	corrupt := func(err error) (persistedRecord, *types.LightBlock, error) {
		return zero, nil, fmt.Errorf("%w: %v", ErrCorruptStore, err)
	}
	if len(data) == 0 || len(data) > maxRecordBytes {
		return corrupt(errors.New("record size"))
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record persistedRecord
	if err := decoder.Decode(&record); err != nil {
		return corrupt(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return corrupt(errors.New("trailing record input"))
	}
	canonical, err := encodeRecord(record)
	if err != nil || !bytes.Equal(canonical, data) {
		return corrupt(errors.New("noncanonical record encoding"))
	}
	if record.Version != recordVersion || record.ClientID != id || record.BootstrapAnchor.ChainID != record.Policy.ChainID ||
		record.BootstrapAnchor.Height <= 0 || record.BootstrapAnchor.Height > record.Latest.Height ||
		record.BootstrapAnchor.Hash == ([32]byte{}) || len(record.LightBlock) == 0 || len(record.LightBlock) > maxBlockBytes {
		return corrupt(errors.New("invalid record identity, version or bounds"))
	}
	var pb cmtproto.LightBlock
	if err := pb.Unmarshal(record.LightBlock); err != nil {
		return corrupt(err)
	}
	block, err := types.LightBlockFromProto(&pb)
	if err != nil {
		return corrupt(err)
	}
	// Validate at the stored signed header time only to check integrity of old
	// data. This is not an authorization at the current block time.
	verified, err := finality.VerifyBootstrap(record.Latest.Checkpoint, block, record.Latest.Time, record.Policy)
	if err != nil || !sameCheckpoint(verified, record.Latest) {
		return corrupt(errors.New("stored checkpoint/block/policy mismatch"))
	}
	canonicalBlock, err := blockBytes(block)
	if err != nil || !bytes.Equal(canonicalBlock, record.LightBlock) {
		return corrupt(errors.New("noncanonical light block"))
	}
	if record.BootstrapAnchor.Height == record.Latest.Height && record.BootstrapAnchor != record.Latest.Checkpoint {
		return corrupt(errors.New("bootstrap anchor differs from initial checkpoint"))
	}
	return record, block, nil
}

// InitClient is a TRUSTED module/genesis entry point, not a transaction handler.
// The external anchor and immutable source policy must be authenticated before
// calling. Existing rows, including corrupt rows, may never be reinitialized.
func (k *Keeper) InitClient(ctx sdk.Context, id string, anchor finality.Checkpoint,
	block *types.LightBlock, policy finality.Policy) error {
	key, err := clientKey(id)
	if err != nil {
		return err
	}
	if k == nil || k.service == nil {
		return errors.New("nil bridge client keeper")
	}
	child, commit := ctx.CacheContext()
	current, err := k.service.OpenKVStore(child).Get(key)
	if err != nil {
		return err
	}
	if current != nil {
		return ErrAlreadyInitialized
	}
	verified, err := finality.VerifyBootstrap(anchor, block, child.BlockTime(), policy)
	if err != nil {
		return err
	}
	encoded, err := blockBytes(block)
	if err != nil {
		return err
	}
	data, err := encodeRecord(persistedRecord{Version: recordVersion, ClientID: id, BootstrapAnchor: anchor,
		Policy: policy, Latest: verified, LightBlock: encoded})
	if err != nil {
		return err
	}
	if err := k.service.OpenKVStore(child).Set(key, data); err != nil {
		return err
	}
	commit()
	return nil
}

// Update derives trust exclusively from the latest persisted client state.
// The SDK caller must commit its surrounding context for this write to persist.
func (k *Keeper) Update(ctx sdk.Context, id string, candidate *types.LightBlock) (finality.VerifiedCheckpoint, error) {
	var zero finality.VerifiedCheckpoint
	child, commit := ctx.CacheContext()
	record, trusted, err := k.load(child, id)
	if err != nil {
		return zero, err
	}
	verified, err := finality.VerifyUpdate(record.Latest.Checkpoint, trusted, candidate, child.BlockTime(), record.Policy)
	if err != nil {
		return zero, err
	}
	encoded, err := blockBytes(candidate)
	if err != nil {
		return zero, err
	}
	record.Latest, record.LightBlock = verified, encoded
	data, err := encodeRecord(record)
	if err != nil {
		return zero, err
	}
	key, _ := clientKey(id) // validated by load
	if err := k.service.OpenKVStore(child).Set(key, data); err != nil {
		return zero, err
	}
	commit()
	return verified, nil
}

func (k *Keeper) fresh(ctx sdk.Context, id string) (persistedRecord, *types.LightBlock, error) {
	record, block, err := k.load(ctx, id)
	if err != nil {
		return persistedRecord{}, nil, err
	}
	if _, err := finality.VerifyBootstrap(record.Latest.Checkpoint, block, ctx.BlockTime(), record.Policy); err != nil {
		return persistedRecord{}, nil, err
	}
	return record, block, nil
}

// LatestCheckpoint returns only the latest stored, unexpired authenticated root.
// A proof for an older root cannot be checked through this getter after Update.
func (k *Keeper) LatestCheckpoint(ctx sdk.Context, id string) (finality.VerifiedCheckpoint, error) {
	record, _, err := k.fresh(ctx, id)
	if err != nil {
		return finality.VerifiedCheckpoint{}, err
	}
	return record.Latest, nil
}

// LatestLightBlock returns a newly decoded copy, not mutable keeper state.
func (k *Keeper) LatestLightBlock(ctx sdk.Context, id string) (*types.LightBlock, error) {
	_, block, err := k.fresh(ctx, id)
	return block, err
}

func (k *Keeper) ClientPolicy(ctx sdk.Context, id string) (finality.Policy, error) {
	record, _, err := k.fresh(ctx, id)
	if err != nil {
		return finality.Policy{}, err
	}
	return record.Policy, nil
}
