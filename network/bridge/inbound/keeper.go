// Package inbound composes stored light-client authority, exact EVM storage
// membership and transactional voucher accounting. It has no message server,
// genesis registration, daemon wiring, RPC, or external deployment path yet.
package inbound

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"time"

	corestore "cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmt "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/maxwell2010/go-smart-node/network/bridge/client"
	"github.com/maxwell2010/go-smart-node/network/bridge/ledger"
	"github.com/maxwell2010/go-smart-node/network/bridge/protocol"
	"github.com/maxwell2010/go-smart-node/network/bridge/storage"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const routePrefix = "bridge/route/v1/"

var (
	ErrRouteNotFound = errors.New("bridge inbound route not found")
	ErrRouteExists   = errors.New("bridge inbound route already registered")
	idPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

// Route is registered only by trusted module/genesis code. It pins the source
// client and audited non-upgradeable escrow, destination identity, activation
// height and liveness window. Registering an address does not itself prove its
// code: an authenticated deployment/code-policy procedure remains mandatory.
// This profile issues Decimal DEL vouchers only; it cannot mint native DEL.
type Route struct {
	ClientID              string
	SourceChainID         string
	SourceEVMChainID      uint64
	SourceEscrow          [20]byte
	DestinationChainID    string
	DestinationEVMChainID uint64
	DestinationBridgeID   [32]byte
	MinimumStateHeight    int64
	MaxHeaderAge          time.Duration
}

type routeRecord struct {
	Version int    `json:"version"`
	RouteID string `json:"route_id"`
	Route   Route  `json:"route"`
}

type Keeper struct {
	service corestore.KVStoreService
	clients *client.Keeper
	ledger  *ledger.Keeper
}

func NewKeeper(service corestore.KVStoreService, clients *client.Keeper, deposits *ledger.Keeper) (*Keeper, error) {
	if service == nil || clients == nil || deposits == nil {
		return nil, errors.New("inbound requires store, stored light client and deposit ledger")
	}
	return &Keeper{service: service, clients: clients, ledger: deposits}, nil
}

func routeKey(id string) ([]byte, error) {
	if !idPattern.MatchString(id) {
		return nil, errors.New("invalid inbound route ID")
	}
	return []byte(routePrefix + id), nil
}

func (route Route) validate() error {
	if !idPattern.MatchString(route.ClientID) || route.SourceChainID != ledger.OriginChainID ||
		route.SourceEVMChainID != 75 || route.SourceEscrow == ([20]byte{}) ||
		route.DestinationBridgeID == ([32]byte{}) || route.MinimumStateHeight <= 0 || route.MaxHeaderAge <= 0 {
		return errors.New("invalid pinned Decimal DEL inbound route")
	}
	return identity.ValidateChainIDs(route.DestinationChainID, route.DestinationEVMChainID)
}

// InitRoute must never be exposed as a permissionless transaction or HTTP API.
// Reinitialization, including replacement of malformed existing data, fails.
func (k *Keeper) InitRoute(ctx sdk.Context, id string, route Route) error {
	key, err := routeKey(id)
	if err != nil {
		return err
	}
	if err := route.validate(); err != nil {
		return err
	}
	if ctx.ChainID() != route.DestinationChainID {
		return errors.New("route destination differs from the local SDK chain")
	}
	policy, err := k.clients.ClientPolicy(ctx, route.ClientID)
	if err != nil {
		return err
	}
	if policy.ChainID != route.SourceChainID || route.MaxHeaderAge >= policy.TrustingPeriod {
		return errors.New("route source or liveness window differs from the stored client policy")
	}
	child, write := ctx.CacheContext()
	store := k.service.OpenKVStore(child)
	exists, err := store.Has(key)
	if err != nil {
		return err
	}
	if exists {
		return ErrRouteExists
	}
	data, err := json.Marshal(routeRecord{Version: 1, RouteID: id, Route: route})
	if err != nil {
		return err
	}
	if err := store.Set(key, data); err != nil {
		return err
	}
	write()
	return nil
}

func (k *Keeper) GetRoute(ctx sdk.Context, id string) (Route, error) {
	var zero Route
	key, err := routeKey(id)
	if err != nil {
		return zero, err
	}
	data, err := k.service.OpenKVStore(ctx).Get(key)
	if err != nil {
		return zero, err
	}
	if data == nil {
		return zero, ErrRouteNotFound
	}
	if len(data) == 0 || len(data) > 4096 {
		return zero, errors.New("invalid inbound route record size")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record routeRecord
	if err := decoder.Decode(&record); err != nil {
		return zero, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return zero, errors.New("trailing inbound route data")
	}
	canonical, err := json.Marshal(record)
	if err != nil || !bytes.Equal(canonical, data) || record.Version != 1 || record.RouteID != id {
		return zero, errors.New("noncanonical or invalid inbound route record")
	}
	if err := record.Route.validate(); err != nil {
		return zero, err
	}
	return record.Route, nil
}

// Receive accepts an operation and proof, never a caller-supplied checkpoint.
// Only the latest authenticated root is accepted. A changed latest root means
// the relayer must obtain a fresh proof; replay state and balances stay intact.
func (k *Keeper) Receive(ctx sdk.Context, routeID string, deposit protocol.Deposit,
	stateHeight int64, ops *cmtcrypto.ProofOps) error {
	child, write := ctx.CacheContext()
	if err := k.receive(child, routeID, deposit, stateHeight, ops); err != nil {
		return err
	}
	write()
	return nil
}

// ReceiveWithUpdate makes header advancement, membership, bank effects, events
// and replay state one SDK transaction. Failure leaves the previous client
// checkpoint in place as well as reverting any voucher accounting changes.
func (k *Keeper) ReceiveWithUpdate(ctx sdk.Context, routeID string, candidate *cmt.LightBlock,
	deposit protocol.Deposit, stateHeight int64, ops *cmtcrypto.ProofOps) error {
	route, err := k.GetRoute(ctx, routeID)
	if err != nil {
		return err
	}
	if ctx.ChainID() != route.DestinationChainID {
		return errors.New("unexpected local bridge chain")
	}
	child, write := ctx.CacheContext()
	if _, err := k.clients.Update(child, route.ClientID, candidate); err != nil {
		return err
	}
	if err := k.receive(child, routeID, deposit, stateHeight, ops); err != nil {
		return err
	}
	write()
	return nil
}

func (k *Keeper) receive(ctx sdk.Context, routeID string, deposit protocol.Deposit,
	stateHeight int64, ops *cmtcrypto.ProofOps) error {
	route, err := k.GetRoute(ctx, routeID)
	if err != nil {
		return err
	}
	if ctx.ChainID() != route.DestinationChainID || deposit.SourceEVMChainID != route.SourceEVMChainID ||
		deposit.DestinationEVMChainID != route.DestinationEVMChainID || deposit.SourceEscrow != route.SourceEscrow ||
		deposit.DestinationBridgeID != route.DestinationBridgeID || stateHeight < route.MinimumStateHeight {
		return errors.New("deposit differs from the registered source, destination or activation height")
	}
	id, err := protocol.OperationID(deposit)
	if err != nil {
		return err
	}
	checkpoint, err := k.clients.LatestCheckpoint(ctx, route.ClientID)
	if err != nil {
		return err
	}
	if checkpoint.ChainID != route.SourceChainID || ctx.BlockTime().Sub(checkpoint.Time) > route.MaxHeaderAge {
		return errors.New("source header is stale or belongs to a different chain")
	}
	claim := storage.StorageClaim{ChainID: route.SourceChainID, StateHeight: stateHeight,
		Address: route.SourceEscrow, Slot: protocol.CommitmentSlot(id), Value: id}
	if err := storage.VerifyStorageMembership(checkpoint, claim, ops); err != nil {
		return fmt.Errorf("deposit commitment: %w", err)
	}
	amount := sdkmath.NewIntFromBigInt(new(big.Int).SetBytes(deposit.Amount[:]))
	return k.ledger.ExecuteVerifiedDeposit(ctx, id, sdk.AccAddress(deposit.Recipient[:]), amount)
}
