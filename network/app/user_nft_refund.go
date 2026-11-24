package app

import (
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const userNFTRefundActivationKey byte = 0x84

// The authenticated origin implementation sends the refund during the burn.
// This hook verifies the exact receipt and retires the corresponding native
// wallet units in the same transaction cache.
func (c *withdrawalController) admitUserNFTRefund(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, asset abi.ABI, family byte, args []interface{}) error {
	target := *msg.To
	owner := sdk.AccAddress(msg.From[:])
	evm := c.customCollateral.evm
	if msg.Value.Sign() != 0 || c.delRecipientAllowed == nil || !c.delRecipientAllowed(owner) ||
		len(evm.GetCode(ctx, evm.GetCodeHash(ctx, msg.From))) != 0 {
		return errors.New("NFT refund requires a signed ordinary wallet and zero call value")
	}
	flags, err := c.userNFTRead(ctx, target, asset, "getRefundable")
	if err != nil {
		return err
	}
	if !flags[0].(bool) {
		return errors.New("native wallet burn requires a refundable collection")
	}
	id := common.BigToHash(args[0].(*big.Int))
	quantity := uint32(1)
	multi := family == 3
	nftType := nfttypes.NftType_NFT721
	if multi {
		nftType = nfttypes.NftType_NFT1155
		units := args[1].(*big.Int)
		if !units.IsUint64() || units.Uint64() == 0 || units.Uint64() > 128 {
			return errors.New("NFT refund quantity exceeds bound")
		}
		quantity = uint32(units.Uint64())
	}
	binding, err := c.nft.ResolveExternalNFT(ctx, target[:], id[:])
	if err != nil {
		return err
	}
	token, err := c.nft.GetToken(ctx, binding.NativeTokenID)
	if err != nil {
		return err
	}
	supply := token.Minted - token.Burnt
	if binding.NFTType != nftType || token.IsolatedReceipt || token.ReserveLess ||
		token.Reserve.Denom != identity.BaseDenom || !token.Reserve.IsPositive() ||
		quantity > supply || (!multi && supply != 1) {
		return errors.New("NFT refund differs from active native origin units")
	}
	count, err := c.nft.GetActiveSubTokenCount(ctx, binding.NativeTokenID, owner.String())
	if err != nil {
		return err
	}
	if count < quantity {
		return errors.New("NFT refund exceeds the signer wallet units")
	}
	remaining := uint64(supply - quantity)
	pool := userNFTReserve{Amount: new(big.Int)}
	if multi {
		pool.ReserveType = 1
		pool.Amount.Mul(token.Reserve.Amount.BigInt(), new(big.Int).SetUint64(remaining))
	}
	expectedLogs, err := userNFTRefundLogs(asset, target, msg.From, id, quantity, remaining, pool, multi)
	if err != nil {
		return err
	}
	if err := requireUserNFTLogs(receipt.Logs, expectedLogs); err != nil {
		return err
	}
	if err := c.checkUserNFTRefundState(ctx, target, msg.From, id, asset, multi, remaining, count-quantity, pool); err != nil {
		return err
	}
	units, err := c.nft.RetireUserNFTUnits(ctx, binding.NativeTokenID, owner, quantity)
	if err != nil {
		return err
	}
	refund := sdk.NewCoin(identity.BaseDenom, token.Reserve.Amount.MulRaw(int64(quantity)))
	return ctx.EventManager().EmitTypedEvent(&nfttypes.EventBurnToken{
		Sender: owner.String(), ID: binding.NativeTokenID, Return: refund.String(), SubTokenIDs: units,
	})
}

func (c *withdrawalController) checkUserNFTRefundState(ctx sdk.Context, target, owner common.Address, id common.Hash,
	asset abi.ABI, multi bool, supply uint64, owned uint32, pool userNFTReserve,
) error {
	values, err := c.userNFTRead(ctx, target, asset, "getReserve", id.Big())
	if err != nil {
		return err
	}
	actual := abi.ConvertType(values[0], new(userNFTReserve)).(*userNFTReserve)
	if actual.Token != pool.Token || actual.ReserveType != pool.ReserveType || actual.Amount.Cmp(pool.Amount) != 0 {
		return errors.New("NFT refund leaves a different EVM reserve")
	}
	if !multi {
		return nil
	}
	method := userNFTSupplyMethod(asset)
	if method == "" {
		return errors.New("missing pinned ERC1155 supply getter")
	}
	values, err = c.userNFTRead(ctx, target, asset, method, id.Big())
	if err != nil {
		return err
	}
	if values[0].(*big.Int).Cmp(new(big.Int).SetUint64(supply)) != 0 {
		return errors.New("NFT refund leaves a different ERC1155 supply")
	}
	values, err = c.userNFTRead(ctx, target, asset, "balanceOf", owner, id.Big())
	if err != nil {
		return err
	}
	if values[0].(*big.Int).Cmp(new(big.Int).SetUint64(uint64(owned))) != 0 {
		return errors.New("NFT refund leaves a different ERC1155 wallet balance")
	}
	values, err = c.userNFTRead(ctx, target, asset, "declaredReserve", pool.ReserveType, common.Address{})
	if err != nil {
		return err
	}
	declared := values[0].(*big.Int)
	held := c.customCollateral.evm.SpendableCoin(ctx, target)
	if held == nil || declared.Cmp(pool.Amount) < 0 || held.ToBig().Cmp(declared) < 0 {
		return errors.New("NFT refund leaves insufficient native backing in the collection")
	}
	return nil
}

func userNFTRefundLogs(asset abi.ABI, target, sender common.Address, id common.Hash, quantity uint32,
	postSupply uint64, pool userNFTReserve, multi bool,
) ([]*ethtypes.Log, error) {
	owner := customAddressWord(sender)
	var transfer, reserve *ethtypes.Log
	var err error
	if multi {
		transfer, err = userNFTEvent(asset, target, "TransferSingle", []common.Hash{owner, owner, {}},
			id.Big(), new(big.Int).SetUint64(uint64(quantity)))
	} else {
		transfer, err = userNFTEvent(asset, target, "Transfer", []common.Hash{owner, {}, id})
	}
	if err != nil {
		return nil, err
	}
	if multi {
		reserve, err = userNFTEvent(asset, target, "ReserveUpdated", nil, id.Big(), new(big.Int).SetUint64(postSupply), pool)
	} else {
		reserve, err = userNFTEvent(asset, target, "ReserveUpdated", nil, id.Big(), pool)
	}
	if err != nil {
		return nil, err
	}
	return []*ethtypes.Log{transfer, reserve}, nil
}
