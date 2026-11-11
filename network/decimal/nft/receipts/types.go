// Package receipts stages authenticated NFT ownership planning. Nothing in this
// package installs a public transaction route or admits financial reserve state.
package receipts

import (
	"errors"
	"reflect"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

func nilDependency(value interface{}) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return v.IsNil()
	}
	return false
}

type EntryPoint uint8

const (
	EntryPointUnspecified EntryPoint = iota
	EntryPointSignedEthereum
	EntryPointGovernance
	// EntryPointIBC is reserved but unsupported until the shared native pipeline
	// has actual callback entry-point coverage. It must never silently fall back.
	EntryPointIBC
	// EntryPointWithdrawalSettlement is restricted to completion receipts after
	// the Gateway withdrawal executor verifies the frozen row and payout.
	EntryPointWithdrawalSettlement
	// EntryPointWalletTransfer is restricted to direct approved NFT calls in
	// the application hook. Gas estimation uses the same hook without a tx hash.
	EntryPointWalletTransfer
)

func (e EntryPoint) Validate() error {
	if e != EntryPointSignedEthereum && e != EntryPointGovernance && e != EntryPointWithdrawalSettlement && e != EntryPointWalletTransfer {
		return errors.New("unsupported NFT receipt entry point")
	}
	return nil
}

type Execution struct {
	EntryPoint EntryPoint
	Receipt    *ethtypes.Receipt
}

type Limits struct {
	MaxLogs   uint32
	MaxBytes  uint32
	MaxAssets uint32
	MaxIDs    uint32
	MaxUnits  uint32
}

func (l Limits) Validate() error {
	if l.MaxLogs == 0 || l.MaxLogs > 1024 || l.MaxBytes == 0 || l.MaxBytes > 1024*1024 || l.MaxAssets == 0 || l.MaxAssets > 256 || l.MaxIDs == 0 || l.MaxIDs > 1024 || l.MaxUnits == 0 || l.MaxUnits > 10000 {
		return errors.New("invalid NFT receipt work limits")
	}
	return nil
}

type AssetKey struct {
	Contract common.Address
	TokenID  common.Hash // Full uint256 bytes, including leading zeros.
}

// MoveRef uses receipt slice position and batch item position. Embedded log
// metadata is retained for audit, but cannot reorder execution or deduplicate it.
type MoveRef struct{ LogPosition, ItemPosition uint32 }

type LogMetadata struct {
	BlockNumber uint64
	TxHash      common.Hash
	TxIndex     uint
	Index       uint
}

type Transfer struct {
	Ref      MoveRef
	Metadata LogMetadata
	Asset    AssetKey
	NFTType  types.NftType
	Operator common.Address
	From     common.Address
	To       common.Address
	Quantity uint32
}

// CollectionProfile must come from independently approved registry state, with
// current factory/domain/implementation/proxy code checks. An observed runtime
// hash is never its own approval. Constructor immutables are verified against
// compiler templates and explicit known arguments by the concrete authenticator.
type CollectionProfile struct {
	ChainID                string
	EVMChainID             uint64
	Contract               common.Address
	Factory                common.Address
	NFTType                types.NftType
	ReserveLess            bool
	ProxyCodeHash          common.Hash
	ImplementationCodeHash common.Hash
	Escrows                []common.Address
}

type Authenticator interface {
	AuthenticateCollection(sdk.Context, common.Address, EntryPoint) (CollectionProfile, bool, error)
}

// FinalReader must use bounded actual ApplyMessage gas and charge all EVM/read
// work to the inherited SDK meter. It reads same-context final state in discarded
// caches; RPC quotes, stale cached supply and CallEVM's unused gasCap are invalid.
type FinalReader interface {
	Ownership(sdk.Context, CollectionProfile, AssetKey, []common.Address) (map[common.Address]uint32, error)
}

// CapturedStakes belongs to the validator ledger. NFT state stores no second
// copy of stake bindings. Captured escrow selections refer to persisted units,
// particularly on withdraw/redelegate; ordinary owners use ascending unit IDs.
type CapturedStakes struct {
	Selections          map[MoveRef][]uint32
	PrestateFingerprint common.Hash
}

type StakeCapture interface {
	Capture(sdk.Context, Execution, []Transfer) (CapturedStakes, error)
	ValidateCapture(sdk.Context, CapturedStakes) error
}

type NativeState interface {
	GetExternalNFTBinding(sdk.Context, string) (types.ExternalNFTBinding, error)
	ResolveExternalNFT(sdk.Context, []byte, []byte) (types.ExternalNFTBinding, error)
	GetToken(sdk.Context, string) (types.Token, error)
	ListActiveSubTokens(sdk.Context, string, string, uint32, uint32) ([]types.SubToken, uint32, error)
	TransferSubTokens(sdk.Context, string, []uint32, string, string) error
}
