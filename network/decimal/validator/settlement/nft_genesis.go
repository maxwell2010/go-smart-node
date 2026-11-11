package settlement

import (
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

// NFTGenesisOrigin describes the complete live census of one origin token.
type NFTGenesisOrigin struct {
	Contract    common.Address
	TokenID     common.Hash
	Supply      uint32
	UnitReserve sdkmath.Int
	Owners      []NFTGenesisOwner
}

type NFTGenesisOwner struct {
	Address common.Address
	Units   uint32
}

func (e *Executor) attestFreshNFTGenesis(ctx sdk.Context, origins []NFTGenesisOrigin) error {
	vault := e.roles.PositionVault
	if err := e.attestGenesisNFTCollection(ctx, vault, nfttypes.NftType_NFT721); err != nil {
		return err
	}
	for _, field := range []struct {
		method string
		want   common.Hash
		args   []interface{}
	}{
		{method: "totalSupply"},
		{method: "nextPositionId", want: smallWord(1)},
		{method: "declaredReserve", args: []interface{}{uint8(nfttypes.IsolatedReserveDEL), common.Address{}}},
		{method: "declaredReserve", args: []interface{}{uint8(nfttypes.IsolatedReserveERC20), e.roles.WrappedDEL}},
	} {
		raw, err := e.read(ctx, vault, e.vault, field.method, common.HashLength, field.args...)
		if err != nil {
			return err
		}
		if common.BytesToHash(raw) != field.want {
			return errors.New("NFT admission genesis requires an empty deployed vault at position ID 1")
		}
	}
	type collectionTotals struct {
		address common.Address
		units   uint64
		reserve big.Int
	}
	var collections []collectionTotals
	byAddress := make(map[common.Address]int)
	seen := make(map[receipts.AssetKey]bool)
	for _, origin := range origins {
		asset := receipts.AssetKey{Contract: origin.Contract, TokenID: origin.TokenID}
		if seen[asset] {
			return errors.New("duplicated NFT origin in genesis attestation")
		}
		seen[asset] = true
		index, found := byAddress[origin.Contract]
		if !found {
			if err := e.attestGenesisNFTCollection(ctx, origin.Contract, nfttypes.NftType_NFT1155); err != nil {
				return err
			}
			index = len(collections)
			byAddress[origin.Contract] = index
			collections = append(collections, collectionTotals{address: origin.Contract})
		}
		pool, err := e.attestGenesisNFTOrigin(ctx, origin)
		if err != nil {
			return fmt.Errorf("attest NFT origin %s: %w", origin.TokenID.Hex(), err)
		}
		collections[index].units += uint64(origin.Supply)
		collections[index].reserve.Add(&collections[index].reserve, pool)
	}
	for _, origin := range collections {
		supply, err := e.read(ctx, origin.address, e.origin, "totalSupply", common.HashLength)
		if err != nil {
			return err
		}
		reserve, err := e.read(ctx, origin.address, e.origin, "declaredReserve", common.HashLength, uint8(nfttypes.IsolatedReserveDEL), common.Address{})
		if err != nil {
			return err
		}
		held := e.evm.GetBalance(ctx, origin.address)
		if new(big.Int).SetBytes(supply).Cmp(new(big.Int).SetUint64(origin.units)) != 0 ||
			new(big.Int).SetBytes(reserve).Cmp(&origin.reserve) != 0 || held == nil || held.ToBig().Cmp(&origin.reserve) < 0 {
			return errors.New("NFT genesis collection has untracked supply or insufficient DEL collateral")
		}
	}
	return nil
}

func (e *Executor) attestGenesisNFTCollection(ctx sdk.Context, contract common.Address, kind nfttypes.NftType) error {
	collection, known, err := e.auth.AuthenticateCollection(ctx, contract, receipts.EntryPointSignedEthereum)
	if err != nil {
		return err
	}
	if !known || collection.NFTType != kind || collection.ReserveLess {
		return errors.New("NFT genesis collection is outside the approved reserve profile")
	}
	if kind == nfttypes.NftType_NFT1155 {
		raw, err := e.read(ctx, contract, e.origin, "isolationVault", common.HashLength)
		if err != nil {
			return err
		}
		if common.BytesToHash(raw) != common.BytesToHash(e.roles.PositionVault[:]) {
			return errors.New("NFT genesis origin names a different vault")
		}
	}
	return nil
}

func (e *Executor) attestGenesisNFTOrigin(ctx sdk.Context, origin NFTGenesisOrigin) (*big.Int, error) {
	if origin.Supply == 0 || origin.UnitReserve.IsNil() || !origin.UnitReserve.IsPositive() || len(origin.Owners) == 0 || len(origin.Owners) > int(origin.Supply) {
		return nil, errors.New("invalid NFT genesis owner list, supply or reserve")
	}
	id := origin.TokenID.Big()
	supply, pool, err := e.isolationOriginPoststate(ctx, origin.Contract, id)
	if err != nil {
		return nil, err
	}
	wantPool := new(big.Int).Mul(new(big.Int).SetUint64(uint64(origin.Supply)), origin.UnitReserve.BigInt())
	if supply != origin.Supply || pool.Cmp(wantPool) != 0 {
		return nil, errors.New("NFT genesis origin supply or aggregate reserve differs from EVM")
	}
	unit, err := e.read(ctx, origin.Contract, e.origin, "getIsolationUnitReserve", 3*common.HashLength, id)
	if err != nil {
		return nil, err
	}
	if !matchesNFTWords(unit, common.Hash{}, common.BigToHash(origin.UnitReserve.BigInt()), smallWord(byte(nfttypes.IsolatedReserveDEL))) {
		return nil, errors.New("NFT genesis origin per-unit reserve differs from EVM")
	}
	var units uint64
	seen := make(map[common.Address]bool)
	for _, owner := range origin.Owners {
		if owner.Address == (common.Address{}) || owner.Address == e.roles.Gateway || owner.Address == e.roles.PositionVault || owner.Address == origin.Contract || owner.Units == 0 || seen[owner.Address] {
			return nil, errors.New("invalid or duplicate NFT genesis origin owner")
		}
		seen[owner.Address] = true
		raw, err := e.read(ctx, origin.Contract, e.origin, "balanceOf", common.HashLength, owner.Address, id)
		if err != nil {
			return nil, err
		}
		if common.BytesToHash(raw) != common.BytesToHash(sdk.Uint64ToBigEndian(uint64(owner.Units))) {
			return nil, errors.New("NFT genesis origin owner balance differs from EVM")
		}
		units += uint64(owner.Units)
	}
	if units != uint64(origin.Supply) {
		return nil, errors.New("NFT genesis owner counts do not cover the live supply")
	}
	return pool, nil
}
