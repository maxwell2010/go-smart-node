// Package ledger implements trusted internal inbound bridge accounting only.
// It does not verify a proof or expose a message/HTTP route. The caller must
// authenticate and bind transfer ID, recipient and amount before calling it.
// Module permissions and the bank keeper must be provisioned by trusted app
// wiring. No native DEL mint, reverse transfer or escrow release is implemented.
package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"

	"cosmossdk.io/core/store"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	ModuleName    = "bridge"
	VoucherDenom  = "bridge/decimal/del"
	OriginChainID = "decimal_75-1"
	OriginDenom   = "del"
	depositPrefix = "bridge/deposit/v1/"
	markerDomain  = OriginChainID + "\x00" + OriginDenom + "\x00" + VoucherDenom + "\x00"
	markerBytes   = 1 + len(markerDomain) + 32 + 20 + 32
)

var (
	ErrAlreadyProcessed = errors.New("bridge deposit already processed")
	ErrNotFound         = errors.New("bridge deposit not found")
)

// BankKeeper is deliberately limited to supply and the two fixed bank actions.
// Implementations must use the supplied context; external side effects cannot
// participate in the SDK cache transaction and are outside this contract.
type BankKeeper interface {
	GetSupply(context.Context, string) sdk.Coin
	MintCoins(context.Context, string, sdk.Coins) error
	SendCoinsFromModuleToAccount(context.Context, string, sdk.AccAddress, sdk.Coins) error
}

type Keeper struct {
	service store.KVStoreService
	bank    BankKeeper
	emit    func(sdk.Context, *EventVerifiedDeposit) error
}

// DepositRecord binds a durable replay marker to its origin and exact payout.
// Returned addresses and amounts do not alias committed store bytes.
type DepositRecord struct {
	TransferID    [32]byte
	OriginChainID string
	OriginDenom   string
	VoucherDenom  string
	Recipient     sdk.AccAddress
	Amount        sdkmath.Int
}

func NewKeeper(service store.KVStoreService, bank BankKeeper) (*Keeper, error) {
	if service == nil || bank == nil {
		return nil, errors.New("bridge ledger requires store service and bank keeper")
	}
	return &Keeper{service: service, bank: bank, emit: func(ctx sdk.Context, event *EventVerifiedDeposit) error {
		return ctx.EventManager().EmitTypedEvent(event)
	}}, nil
}

func depositKey(id [32]byte) []byte {
	key := make([]byte, len(depositPrefix)+32)
	copy(key, depositPrefix)
	copy(key[len(depositPrefix):], id[:])
	return key
}

func (k *Keeper) HasDeposit(ctx sdk.Context, id [32]byte) (bool, error) {
	if id == ([32]byte{}) {
		return false, errors.New("empty bridge transfer ID")
	}
	return k.service.OpenKVStore(ctx).Has(depositKey(id))
}

func (k *Keeper) GetDeposit(ctx sdk.Context, id [32]byte) (DepositRecord, error) {
	var zero DepositRecord
	if id == ([32]byte{}) {
		return zero, errors.New("empty bridge transfer ID")
	}
	body, err := k.service.OpenKVStore(ctx).Get(depositKey(id))
	if err != nil {
		return zero, err
	}
	if body == nil {
		return zero, ErrNotFound
	}
	start := 1 + len(markerDomain)
	if len(body) != markerBytes || body[0] != 1 || string(body[1:start]) != markerDomain ||
		!bytes.Equal(body[start:start+32], id[:]) {
		return zero, errors.New("invalid durable bridge deposit record")
	}
	recipient := append(sdk.AccAddress(nil), body[start+32:start+52]...)
	amount := sdkmath.NewIntFromBigInt(new(big.Int).SetBytes(body[start+52:]))
	if bytes.Equal(recipient, make([]byte, 20)) || !amount.IsPositive() {
		return zero, errors.New("invalid durable bridge payout payload")
	}
	return DepositRecord{TransferID: id, OriginChainID: OriginChainID, OriginDenom: OriginDenom,
		VoucherDenom: VoucherDenom, Recipient: recipient, Amount: amount}, nil
}

// ExecuteVerifiedDeposit atomically mints a distinct voucher, pays its recipient,
// records the transfer ID and emits its typed event in one SDK CacheContext.
// The cache is merged into the caller only on success; an outer transaction or
// block rollback still discards it. Disk durability requires the caller's
// normal multistore Commit. No in-memory replay state is retained.
func (k *Keeper) ExecuteVerifiedDeposit(ctx sdk.Context, id [32]byte, recipient sdk.AccAddress, amount sdkmath.Int) error {
	if id == ([32]byte{}) || len(recipient) != 20 || bytes.Equal(recipient, make([]byte, 20)) ||
		amount.IsNil() || !amount.IsPositive() || amount.BigInt().BitLen() > 256 {
		return errors.New("require nonzero ID, 20-byte recipient and positive uint256 amount")
	}
	child, write := ctx.CacheContext()
	state := k.service.OpenKVStore(child)
	seen, err := state.Has(depositKey(id))
	if err != nil {
		return err
	}
	if seen {
		return ErrAlreadyProcessed
	}
	// SDK bank adds math.Int supply internally. Reject overflow before its
	// mutation instead of allowing a panic after partially staged minting.
	supply := k.bank.GetSupply(child, VoucherDenom)
	if supply.Denom != VoucherDenom || supply.Amount.IsNil() || supply.Amount.IsNegative() {
		return errors.New("invalid existing bridge voucher supply")
	}
	quantity := amount.BigInt()
	if new(big.Int).Add(supply.Amount.BigInt(), quantity).BitLen() > 256 {
		return errors.New("bridge voucher supply exceeds uint256")
	}
	body := make([]byte, markerBytes)
	body[0] = 1
	copy(body[1:], markerDomain)
	start := 1 + len(markerDomain)
	copy(body[start:], id[:])
	copy(body[start+32:], recipient)
	quantity.FillBytes(body[start+52:])
	if err := state.Set(depositKey(id), body); err != nil {
		return fmt.Errorf("persist bridge replay marker: %w", err)
	}
	// Reserve the ID in this cache before calling bank hooks. Reentrant use of
	// the same SDK context sees the marker, while parent state remains untouched
	// until both payment and event succeed. Failure discards this reservation.
	coins := sdk.NewCoins(sdk.NewCoin(VoucherDenom, sdkmath.NewIntFromBigInt(quantity)))
	if err := k.bank.MintCoins(child, ModuleName, coins); err != nil {
		return fmt.Errorf("mint bridge voucher: %w", err)
	}
	if err := k.bank.SendCoinsFromModuleToAccount(child, ModuleName, recipient, coins); err != nil {
		return fmt.Errorf("pay bridge voucher: %w", err)
	}
	if err := k.emit(child, newDepositEvent(id, recipient, amount)); err != nil {
		return fmt.Errorf("emit verified bridge deposit: %w", err)
	}
	write()
	return nil
}
