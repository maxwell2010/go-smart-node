package app

import (
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

const (
	userNFTBatchMaxEntries = 32
	userNFTBatchMaxUnits   = 128
)

// The dispatcher authenticates the registered implementation and canonical ABI
// arguments. An operator may act for the sender only as allowed by that ERC1155.
func (c *withdrawalController) admitUserNFTBatch(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, asset abi.ABI, family byte, args []interface{}) error {
	if (family != 3 && family != 5) || msg.Value.Sign() != 0 || len(args[4].([]byte)) != 0 {
		return errors.New("NFT batch requires DRC1155, zero payment and empty receiver data")
	}
	from, to := args[0].(common.Address), args[1].(common.Address)
	evm := c.customCollateral.evm
	if from == (common.Address{}) || to == (common.Address{}) || c.delRecipientAllowed == nil ||
		!c.delRecipientAllowed(sdk.AccAddress(from[:])) || !c.delRecipientAllowed(sdk.AccAddress(to[:])) ||
		len(evm.GetCode(ctx, evm.GetCodeHash(ctx, to))) != 0 {
		return errors.New("NFT batch requires wallet endpoints without receiver callbacks")
	}
	ids, amounts := args[2].([]*big.Int), args[3].([]*big.Int)
	expected, err := userNFTBatchEvent(asset, *msg.To, msg.From, from, to, ids, amounts)
	if err != nil {
		return err
	}
	if err := requireUserNFTLogs(receipt.Logs, []*ethtypes.Log{expected}); err != nil {
		return err
	}
	return c.assets.ProjectWalletTransfer(ctx, receipt, *msg.To, c.profile.Gateway, c.profile.PositionVault)
}

func userNFTBatchEvent(asset abi.ABI, target, operator, from, to common.Address, ids, amounts []*big.Int) (*ethtypes.Log, error) {
	if len(ids) == 0 || len(ids) > userNFTBatchMaxEntries || len(ids) != len(amounts) {
		return nil, errors.New("NFT batch must have 1 to 32 matching IDs and amounts")
	}
	var units uint64
	for i, amount := range amounts {
		if ids[i] == nil || ids[i].Sign() < 0 || ids[i].BitLen() > 256 || amount == nil ||
			!amount.IsUint64() || amount.Sign() <= 0 || amount.Uint64() > userNFTBatchMaxUnits-units {
			return nil, errors.New("NFT batch has invalid IDs or exceeds 128 positive units")
		}
		units += amount.Uint64()
	}
	indexed := []common.Hash{customAddressWord(operator), customAddressWord(from), customAddressWord(to)}
	// OpenZeppelin emits TransferSingle for one item even when the wallet
	// explicitly called safeBatchTransferFrom.
	if len(ids) == 1 {
		return userNFTEvent(asset, target, "TransferSingle", indexed, ids[0], amounts[0])
	}
	return userNFTEvent(asset, target, "TransferBatch", indexed, ids, amounts)
}
