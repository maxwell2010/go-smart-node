package app

import (
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func (c *withdrawalController) walletOrigin(ctx sdk.Context, target common.Address, id common.Hash, multi bool) (nfttypes.Token, error) {
	b, err := c.nft.ResolveExternalNFT(ctx, target[:], id[:])
	if err != nil {
		return nfttypes.Token{}, err
	}
	kind := nfttypes.NftType_NFT721
	if multi {
		kind = nfttypes.NftType_NFT1155
	}
	if b.NFTType != kind {
		return nfttypes.Token{}, errors.New("NFT origin family mismatch")
	}
	token, err := c.nft.GetToken(ctx, b.NativeTokenID)
	if err != nil {
		return nfttypes.Token{}, err
	}
	if token.IsolatedReceipt || token.Minted <= token.Burnt || (!multi && token.Minted-token.Burnt != 1) || token.Reserve.Denom != identity.BaseDenom {
		return nfttypes.Token{}, errors.New("expected active native NFT origin")
	}
	return token, nil
}

func (c *withdrawalController) admitReservelessNFTBurn(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, asset abi.ABI, family byte, args []interface{}) error {
	if !testIssuanceActive(ctx.ChainID()) || (family != 4 && family != 5) || msg.Value.Sign() != 0 {
		return errors.New("invalid reserveless wallet burn")
	}
	multi := family == 5
	target := *msg.To
	id := common.BigToHash(args[0].(*big.Int))
	token, err := c.walletOrigin(ctx, target, id, multi)
	if err != nil {
		return err
	}
	if !token.ReserveLess || !token.Reserve.IsZero() {
		return errors.New("expected reserveless origin")
	}
	burnable, err := c.userNFTRead(ctx, target, asset, "getBurnable")
	if err != nil {
		return err
	}
	if !burnable[0].(bool) {
		return errors.New("NFT burn is disabled")
	}
	qty := big.NewInt(1)
	if multi {
		qty = args[1].(*big.Int)
	}
	owner := sdk.AccAddress(msg.From.Bytes())
	count, err := c.nft.GetActiveSubTokenCount(ctx, token.ID, owner.String())
	if err != nil {
		return err
	}
	supply := token.Minted - token.Burnt
	if !qty.IsUint64() || qty.Sign() == 0 || qty.Uint64() > 128 || qty.Uint64() > uint64(count) || qty.Uint64() > uint64(supply) {
		return errors.New("NFT burn exceeds wallet or protocol limit")
	}
	who := customAddressWord(msg.From)
	var transfer *ethtypes.Log
	if multi {
		transfer, err = userNFTEvent(asset, target, "TransferSingle", []common.Hash{who, who, {}}, id.Big(), qty)
	} else {
		transfer, err = userNFTEvent(asset, target, "Transfer", []common.Hash{who, {}, id})
	}
	if err != nil {
		return err
	}
	if err = requireUserNFTLogs(receipt.Logs, []*ethtypes.Log{transfer}); err != nil {
		return err
	}
	if multi {
		actual, err := c.userNFTRead(ctx, target, asset, userNFTSupplyMethod(asset), id.Big())
		if err != nil {
			return err
		}
		if actual[0].(*big.Int).Cmp(new(big.Int).SetUint64(uint64(supply)-qty.Uint64())) != 0 {
			return errors.New("NFT burn final supply mismatch")
		}
		actual, err = c.userNFTRead(ctx, target, asset, "balanceOf", msg.From, id.Big())
		if err != nil {
			return err
		}
		if actual[0].(*big.Int).Cmp(new(big.Int).SetUint64(uint64(count)-qty.Uint64())) != 0 {
			return errors.New("NFT burn final balance mismatch")
		}
	}
	units, err := c.nft.BurnReservelessUnits(ctx, token.ID, owner, uint32(qty.Uint64()))
	if err != nil {
		return err
	}
	return ctx.EventManager().EmitTypedEvent(&nfttypes.EventBurnToken{Sender: owner.String(), ID: token.ID, Return: token.Reserve.String(), SubTokenIDs: units})
}

func (c *withdrawalController) admitNFTNativeTopup(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, asset abi.ABI, family byte, args []interface{}) error {
	if !testIssuanceActive(ctx.ChainID()) || (family != 2 && family != 3) || msg.Value.Sign() <= 0 {
		return errors.New("NFT top-up requires a positive native payment")
	}
	multi := family == 3
	target := *msg.To
	id := common.BigToHash(args[0].(*big.Int))
	token, err := c.walletOrigin(ctx, target, id, multi)
	if err != nil {
		return err
	}
	if token.ReserveLess || !token.Reserve.IsPositive() {
		return errors.New("expected backed NFT origin")
	}
	supply := new(big.Int).SetUint64(uint64(token.Minted - token.Burnt))
	increase, rem := new(big.Int).QuoRem(msg.Value, supply, new(big.Int))
	if rem.Sign() != 0 {
		return errors.New("NFT top-up must divide the active supply")
	}
	unit := new(big.Int).Add(token.Reserve.Amount.BigInt(), increase)
	pool := userNFTReserve{Amount: new(big.Int).Mul(unit, supply), ReserveType: 1}
	if pool.Amount.BitLen() > 256 {
		return errors.New("NFT top-up exceeds supported reserve")
	}
	getter := func(method string, want *big.Int) error {
		values, err := c.userNFTRead(ctx, target, asset, method, id.Big())
		if err != nil {
			return err
		}
		actual := abi.ConvertType(values[0], new(userNFTReserve)).(*userNFTReserve)
		if actual.Token != (common.Address{}) || actual.ReserveType != pool.ReserveType || actual.Amount.Cmp(want) != 0 {
			return errors.New("NFT top-up final reserve mismatch")
		}
		return nil
	}
	if err = getter("getReserve", pool.Amount); err != nil {
		return err
	}
	held := c.customCollateral.evm.SpendableCoin(ctx, target)
	if held == nil || held.ToBig().Cmp(pool.Amount) < 0 {
		return errors.New("insufficient native balance for NFT reserve")
	}
	var event *ethtypes.Log
	if multi {
		if err = getter("getIsolationUnitReserve", unit); err != nil {
			return err
		}
		actual, err := c.userNFTRead(ctx, target, asset, userNFTSupplyMethod(asset), id.Big())
		if err != nil {
			return err
		}
		if actual[0].(*big.Int).Cmp(supply) != 0 {
			return errors.New("NFT top-up final supply mismatch")
		}
		total, err := c.userNFTRead(ctx, target, asset, "declaredReserve", uint8(1), common.Address{})
		if err != nil {
			return err
		}
		declared := total[0].(*big.Int)
		if declared.Cmp(pool.Amount) < 0 || held.ToBig().Cmp(declared) < 0 {
			return errors.New("insufficient collection reserve after top-up")
		}
		event, err = userNFTEvent(asset, target, "ReserveUpdated", nil, id.Big(), supply, pool)
		if err != nil {
			return err
		}
	} else {
		actual, err := c.userNFTRead(ctx, target, asset, "ownerOf", id.Big())
		if err != nil {
			return err
		}
		wallet := actual[0].(common.Address)
		count, err := c.nft.GetActiveSubTokenCount(ctx, token.ID, sdk.AccAddress(wallet.Bytes()).String())
		if err != nil {
			return err
		}
		if count != 1 || len(c.customCollateral.evm.GetCode(ctx, c.customCollateral.evm.GetCodeHash(ctx, wallet))) != 0 {
			return errors.New("NFT top-up requires an active wallet owner")
		}
		event, err = userNFTEvent(asset, target, "ReserveUpdated", nil, id.Big(), pool)
		if err != nil {
			return err
		}
	}
	if err = requireUserNFTLogs(receipt.Logs, []*ethtypes.Log{event}); err != nil {
		return err
	}
	next := sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(unit))
	if err = c.nft.IncreaseUserNFTReserve(ctx, token.ID, token.Reserve, next, sdk.AccAddress(c.profile.Gateway.Bytes()), sdk.AccAddress(c.profile.PositionVault.Bytes())); err != nil {
		return err
	}
	return ctx.EventManager().EmitTypedEvent(&nfttypes.EventUpdateReserve{Sender: sdk.AccAddress(msg.From.Bytes()).String(), ID: token.ID, Reserve: next.String(), Refill: sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntFromBigInt(msg.Value)).String()})
}
