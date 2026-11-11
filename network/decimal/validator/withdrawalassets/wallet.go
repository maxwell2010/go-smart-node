package withdrawalassets

import (
	"errors"
	"math"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (p *Projector) projectIndexedWalletTransfer(ctx sdk.Context, decoded nftreceipts.Decoded) error {
	// All movements come from one transfer event. A batch may repeat the same
	// token ID, so reconcile that ID once against the final contract balances.
	first := decoded.Transfers[0]
	var assets []nftreceipts.AssetKey
	quantities := make(map[nftreceipts.AssetKey]uint32)
	for _, transfer := range decoded.Transfers {
		if transfer.From != first.From || transfer.To != first.To {
			return errors.New("wallet NFT batch must have a single owner pair")
		}
		if _, seen := quantities[transfer.Asset]; !seen {
			assets = append(assets, transfer.Asset)
		}
		quantities[transfer.Asset] += transfer.Quantity // The decoder bounds the total quantity across the event.
	}
	codec := address.NewBech32Codec(identity.Bech32PrefixAccAddr)
	from, err := codec.BytesToString(first.From.Bytes())
	if err != nil {
		return err
	}
	to, err := codec.BytesToString(first.To.Bytes())
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	for _, key := range assets {
		if err := p.projectIndexedWalletAsset(child, key, decoded.Profiles[key.Contract], first.From, first.To, from, to, quantities[key]); err != nil {
			return err
		}
	}
	commit()
	return nil
}

func (p *Projector) projectIndexedWalletAsset(ctx sdk.Context, key nftreceipts.AssetKey, profile nftreceipts.CollectionProfile, from, to common.Address, nativeFrom, nativeTo string, quantity uint32) error {
	binding, err := p.nft.ResolveExternalNFT(ctx, key.Contract.Bytes(), key.TokenID.Bytes())
	if err != nil {
		return err
	}
	if binding.ChainID != profile.ChainID || binding.EVMChainID != profile.EVMChainID || binding.NFTType != profile.NFTType {
		return errors.New("wallet NFT native binding differs from the approved collection")
	}
	token, err := p.nft.GetToken(ctx, binding.NativeTokenID)
	if err != nil {
		return err
	}
	if token.ReserveLess != profile.ReserveLess {
		return errors.New("wallet NFT reserve profile differs from the approved collection")
	}
	fromCount, err := p.nft.GetActiveSubTokenCount(ctx, binding.NativeTokenID, nativeFrom)
	if err != nil {
		return err
	}
	if quantity > fromCount {
		return errors.New("wallet NFT transfer exceeds native ownership")
	}
	owners := []common.Address{from}
	wantFrom, wantTo := fromCount, uint32(0)
	if from != to {
		toCount, err := p.nft.GetActiveSubTokenCount(ctx, binding.NativeTokenID, nativeTo)
		if err != nil {
			return err
		}
		if quantity > math.MaxUint32-toCount {
			return errors.New("wallet NFT recipient balance exceeds native ownership bounds")
		}
		owners = append(owners, to)
		wantFrom, wantTo = fromCount-quantity, toCount+quantity
	}
	read, _ := ctx.CacheContext()
	actual, err := p.reader.Ownership(read, profile, key, owners)
	if err != nil {
		return err
	}
	if len(actual) != len(owners) || actual[from] != wantFrom || (from != to && actual[to] != wantTo) {
		return errors.New("wallet NFT ownership differs from the final contract balances")
	}
	if quantity == 0 || from == to {
		return nil
	}
	selected := make([]uint32, 0, quantity)
	var after uint32
	for uint32(len(selected)) < quantity {
		limit := min(nftkeeper.MaxIdentityPageSize, quantity-uint32(len(selected)))
		ids, cursor, err := p.nft.ListActiveSubTokenIDs(ctx, binding.NativeTokenID, nativeFrom, after, limit)
		if err != nil {
			return err
		}
		if uint32(len(ids)) != limit || cursor <= after {
			return errors.New("wallet NFT owner index cannot supply the authenticated units")
		}
		selected = append(selected, ids...)
		after = cursor
	}
	return p.nft.TransferSubTokens(ctx, binding.NativeTokenID, selected, nativeFrom, nativeTo)
}
