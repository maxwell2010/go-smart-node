package evmview

import (
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

type PositionStatus uint8

const (
	PositionUnknown PositionStatus = iota
	PositionActive
	PositionClosed
)

// PositionSnapshot contains fixed-width detached values, not approval to mint
// native state. Pending financial liabilities and lineage admission are separate.
type PositionSnapshot struct {
	Asset                receipts.AssetKey
	Origin               receipts.AssetKey
	OriginQuantity       uint32
	ParentPositionID     common.Hash
	Owner                common.Address
	Status               PositionStatus
	ReserveKind          uint32
	ReserveToken         common.Address
	ReserveAmount        common.Hash
	Refundable, Burnable bool
	NextPositionID       common.Hash
	// Currency-wide declared collateral and actual holdings. Surplus is explicit
	// and never distributed or treated as additional position backing.
	DeclaredCurrencyReserve, HeldCurrencyReserve common.Hash
	WrappedSupply, WrappedNativeBacking          common.Hash
}

type positionABIReserve struct {
	Token       common.Address
	Amount      *big.Int
	ReserveType uint8
}
type positionABIView struct {
	OriginContract       common.Address
	OriginTokenId        *big.Int
	OriginStandard       uint8
	OriginQuantity       *big.Int
	Reserve              positionABIReserve
	ParentPositionId     *big.Int
	PhysicalOwner        common.Address
	Status               uint8
	Refundable, Burnable bool
}

func (r *Reader) authenticateVault(ctx sdk.Context) error {
	if r.config.Protocol != ProfileIsolatedPositionsV1 {
		return errors.New("isolated vault requires explicit isolated protocol profile")
	}
	if err := r.system(ctx, r.config.PositionVault, r.vault); err != nil {
		return err
	}
	for _, binding := range []struct {
		label    string
		expected common.Address
	}{{"nft-position-vault", r.config.PositionVault}, {"wdel", r.config.WrappedDEL}} {
		actual, err := r.address(ctx, r.config.Registry, r.registry.ABI, "getContractAddress", binding.label)
		if err != nil {
			return err
		}
		if actual != binding.expected {
			return errors.New("isolated registry role differs from configured manifest")
		}
	}
	for _, binding := range []struct {
		method   string
		expected common.Address
	}{{"getContractCenter", r.config.Registry}, {"nftCenter", r.config.NFTCenter}} {
		actual, err := r.address(ctx, r.config.PositionVault, r.vault.ABI, binding.method)
		if err != nil {
			return err
		}
		if actual != binding.expected {
			return errors.New("isolated vault registry/factory differs")
		}
	}
	max, err := r.integer(ctx, r.config.PositionVault, r.vault.ABI, "maxOriginQuantity")
	if err != nil {
		return err
	}
	if max.Cmp(new(big.Int).SetUint64(uint64(types.MaxIsolatedOriginUnits))) != 0 {
		return errors.New("isolated contract and native quantity limits differ")
	}
	return nil
}

func (r *Reader) integer(ctx sdk.Context, target common.Address, contract abi.ABI, method string, args ...interface{}) (*big.Int, error) {
	values, err := r.read(ctx, target, contract, method, 32, args...)
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, errors.New("invalid isolated integer getter")
	}
	value, ok := values[0].(*big.Int)
	if !ok || value == nil || value.Sign() < 0 || value.BitLen() > 256 {
		return nil, errors.New("isolated integer exceeds uint256")
	}
	return new(big.Int).Set(value), nil
}

// Position reads actual same-context EVM state with authenticated code and role
// bindings. Closed state requires a valid high-watermark and an exactly cleared
// row. No generic ownerOf/revert fallback can manufacture a close proof.
func (r *Reader) Position(ctx sdk.Context, id common.Hash) (snapshot PositionSnapshot, err error) {
	if r == nil || r.config.Protocol != ProfileIsolatedPositionsV1 || id == (common.Hash{}) {
		return snapshot, errors.New("invalid isolated position reader or ID")
	}
	err = r.bounded(ctx, func(child sdk.Context) error {
		if e := r.domain(child); e != nil {
			return e
		}
		if e := r.systems(child); e != nil {
			return e
		}
		if e := r.authenticateVault(child); e != nil {
			return e
		}
		next, e := r.integer(child, r.config.PositionVault, r.vault.ABI, "nextPositionId")
		if e != nil {
			return e
		}
		if next.Sign() == 0 || id.Big().Cmp(next) >= 0 {
			return errors.New("isolated position is outside authenticated high-watermark")
		}
		values, e := r.read(child, r.config.PositionVault, r.vault.ABI, "getPosition", 384, id.Big())
		if e != nil {
			return e
		}
		if len(values) != 1 {
			return errors.New("invalid isolated position tuple")
		}
		position := *abi.ConvertType(values[0], new(positionABIView)).(*positionABIView)
		snapshot = PositionSnapshot{Asset: receipts.AssetKey{Contract: r.config.PositionVault, TokenID: id}, NextPositionID: common.BigToHash(next), Status: PositionStatus(position.Status)}
		if position.OriginStandard != 1 {
			return errors.New("isolated origin standard must be ERC1155")
		}
		if snapshot.Status == PositionClosed {
			if position.OriginContract != (common.Address{}) || position.OriginTokenId.Sign() != 0 || position.OriginQuantity.Sign() != 0 || position.Reserve.Token != (common.Address{}) || position.Reserve.Amount.Sign() != 0 || position.Reserve.ReserveType != 0 || position.ParentPositionId.Sign() != 0 || position.PhysicalOwner != (common.Address{}) || position.Refundable || position.Burnable {
				return errors.New("closed isolated position is not an exactly cleared row")
			}
			return nil
		}
		if snapshot.Status != PositionActive || position.PhysicalOwner == (common.Address{}) || position.OriginContract == (common.Address{}) || position.OriginContract == r.config.PositionVault || position.OriginQuantity.Sign() <= 0 || position.OriginQuantity.BitLen() > 32 || position.OriginQuantity.Uint64() > uint64(types.MaxIsolatedOriginUnits) || position.ParentPositionId.Cmp(id.Big()) >= 0 {
			return errors.New("invalid active isolated position state")
		}
		if _, e = r.checkChild(child, position.OriginContract, 1, true, 3); e != nil {
			return e
		}
		snapshot.Origin = receipts.AssetKey{Contract: position.OriginContract, TokenID: common.BigToHash(position.OriginTokenId)}
		snapshot.OriginQuantity = uint32(position.OriginQuantity.Uint64())
		snapshot.ParentPositionID = common.BigToHash(position.ParentPositionId)
		snapshot.Owner = position.PhysicalOwner
		snapshot.ReserveKind = uint32(position.Reserve.ReserveType)
		snapshot.ReserveToken = position.Reserve.Token
		snapshot.ReserveAmount = common.BigToHash(position.Reserve.Amount)
		snapshot.Refundable, snapshot.Burnable = position.Refundable, position.Burnable
		return r.currency(child, &snapshot)
	})
	if err != nil {
		return PositionSnapshot{}, err
	}
	return snapshot, nil
}

func (r *Reader) currency(ctx sdk.Context, p *PositionSnapshot) error {
	declared, err := r.integer(ctx, r.config.PositionVault, r.vault.ABI, "declaredReserve", uint8(p.ReserveKind), p.ReserveToken)
	if err != nil {
		return err
	}
	if declared.Cmp(p.ReserveAmount.Big()) < 0 {
		return errors.New("isolated position exceeds its declared currency collateral")
	}
	var held *big.Int
	switch p.ReserveKind {
	case types.IsolatedReserveDEL:
		if p.ReserveToken != (common.Address{}) {
			return errors.New("native position cannot name an ERC20")
		}
		held = r.evm.GetBalance(ctx, r.config.PositionVault).ToBig()
	case types.IsolatedReserveERC20:
		// Custom currency valuation/admission must be composed with the separately
		// authenticated native coin reader. This initial profile admits only WDEL.
		if p.ReserveToken != r.config.WrappedDEL {
			return errors.New("isolated currency has no approved backing reader")
		}
		wrapper, e := contracts.Load("MintCandyWrappedDEL")
		if e != nil {
			return e
		}
		if e = r.code(ctx, p.ReserveToken, wrapper.Runtime); e != nil {
			return e
		}
		held, e = r.integer(ctx, p.ReserveToken, wrapper.ABI, "balanceOf", r.config.PositionVault)
		if e != nil {
			return e
		}
		supply, e := r.integer(ctx, p.ReserveToken, wrapper.ABI, "totalSupply")
		if e != nil {
			return e
		}
		backing := r.evm.GetBalance(ctx, p.ReserveToken).ToBig()
		if backing.Sign() < 0 || backing.BitLen() > 256 || backing.Cmp(supply) < 0 {
			return errors.New("isolated WDEL currency is not natively backed")
		}
		p.WrappedSupply, p.WrappedNativeBacking = common.BigToHash(supply), common.BigToHash(backing)
	default:
		return errors.New("isolated position lost its backed currency kind")
	}
	if held == nil || held.Sign() < 0 || held.BitLen() > 256 || held.Cmp(declared) < 0 {
		return errors.New("isolated declared currency reserve exceeds actual holdings")
	}
	p.DeclaredCurrencyReserve, p.HeldCurrencyReserve = common.BigToHash(declared), common.BigToHash(held)
	return nil
}

// Reserve returns a detached protobuf snapshot for an admitted active lineage.
// It cannot turn a closed or unknown position into a zero-backed active one.
func (p PositionSnapshot) Reserve() (types.IsolatedReserveSnapshot, error) {
	if p.Status != PositionActive {
		return types.IsolatedReserveSnapshot{}, errors.New("only an active isolated position has a reserve snapshot")
	}
	snapshot := types.IsolatedReserveSnapshot{Kind: p.ReserveKind, Token: append([]byte(nil), p.ReserveToken.Bytes()...), Amount: append([]byte(nil), p.ReserveAmount.Bytes()...)}
	if err := snapshot.Validate(); err != nil {
		return types.IsolatedReserveSnapshot{}, err
	}
	return snapshot, nil
}
