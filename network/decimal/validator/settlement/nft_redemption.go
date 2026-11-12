package settlement

import (
	"bytes"
	"errors"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
)

type NFTPositionRedemption struct {
	PositionID, NextPositionID common.Hash
	Owner, Recipient           common.Address
	Reserve                    sdkmath.Int
}

// SignedNFTPositionRedemptionID parses the exact direct vault call so the
// application can resolve the active native receipt before verifying it.
func (e *Executor) SignedNFTPositionRedemptionID(msg core.Message) (common.Hash, error) {
	id, _, err := e.signedNFTPositionRedemptionArgs(msg)
	return id, err
}

func (e *Executor) signedNFTPositionRedemptionArgs(msg core.Message) (common.Hash, common.Address, error) {
	if e == nil || msg.To == nil || *msg.To != e.roles.PositionVault ||
		msg.From == (common.Address{}) || msg.From == e.roles.Gateway || msg.From == Sender() ||
		msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return common.Hash{}, common.Address{}, errors.New("NFT redemption requires a direct zero-value vault call by its wallet owner")
	}
	method := e.vault.Methods["redeemPosition"]
	if method.Sig != "redeemPosition(uint256,address)" || len(msg.Data) != 4+2*common.HashLength || !bytes.Equal(msg.Data[:4], method.ID) {
		return common.Hash{}, common.Address{}, errors.New("NFT redemption requires canonical redeemPosition calldata")
	}
	id := common.BytesToHash(msg.Data[4 : 4+common.HashLength])
	recipientWord := common.BytesToHash(msg.Data[4+common.HashLength:])
	recipient := common.BytesToAddress(recipientWord[12:])
	if id == (common.Hash{}) || recipient == (common.Address{}) || recipient == e.roles.PositionVault ||
		recipientWord != common.BytesToHash(recipient[:]) {
		return common.Hash{}, common.Address{}, errors.New("NFT redemption has an invalid position or recipient")
	}
	return id, recipient, nil
}

// VerifySignedNFTPositionRedemption proves the signed wallet burn and refund
// against the exact native reserve and authenticated, fully closed vault row.
// The pinned vault requires the saved burnable flag and a successful DEL
// payout before emitting its refunded event. The caller commits the native
// closure in the same post-transaction cache.
func (e *Executor) VerifySignedNFTPositionRedemption(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt,
	before nfttypes.IsolatedPositionLineage, owner common.Address,
) (NFTPositionRedemption, error) {
	var out NFTPositionRedemption
	id, recipient, err := e.signedNFTPositionRedemptionArgs(msg)
	if err != nil {
		return out, err
	}
	if err := before.Validate(); err != nil {
		return out, err
	}
	if owner != msg.From || !bytes.Equal(before.Vault, e.roles.PositionVault[:]) ||
		!bytes.Equal(before.PositionID, id[:]) || before.Reserve.Kind != nfttypes.IsolatedReserveDEL {
		return out, errors.New("NFT redemption does not match an active DEL-backed native wallet receipt")
	}
	if err := e.requireInert(ctx, owner); err != nil {
		return out, err
	}
	if err := e.requireInert(ctx, recipient); err != nil {
		return out, err
	}
	markers, err := e.completions(receipt)
	if err != nil {
		return out, err
	}
	if len(markers) != 0 || len(receipt.Logs) != 2 {
		return out, errors.New("NFT redemption requires exactly the vault burn and refund logs")
	}
	if err := signedMetadata(ctx, receipt); err != nil {
		return out, err
	}
	burn, refund := receipt.Logs[0], receipt.Logs[1]
	transferEvent, refundEvent := e.vault.Events["Transfer"], e.vault.Events["PositionRedeemed"]
	if transferEvent.Sig != "Transfer(address,address,uint256)" ||
		refundEvent.Sig != "PositionRedeemed(uint256,address,uint256,bool)" {
		return out, errors.New("compiler NFT redemption event ABI differs")
	}
	if burn.Address != e.roles.PositionVault || len(burn.Topics) != 4 ||
		burn.Topics[0] != transferEvent.ID || burn.Topics[1] != common.BytesToHash(owner[:]) ||
		burn.Topics[2] != (common.Hash{}) || burn.Topics[3] != id || len(burn.Data) != 0 {
		return out, errors.New("NFT redemption burn differs from the native wallet receipt")
	}
	amount := common.BytesToHash(before.Reserve.Amount).Big()
	want, err := refundEvent.Inputs.NonIndexed().Pack(amount, true)
	if err != nil {
		return out, err
	}
	if refund.Address != e.roles.PositionVault || len(refund.Topics) != 3 ||
		refund.Topics[0] != refundEvent.ID || refund.Topics[1] != id ||
		refund.Topics[2] != common.BytesToHash(recipient[:]) || !bytes.Equal(refund.Data, want) {
		return out, errors.New("NFT redemption refund differs from the signed recipient or native DEL reserve")
	}
	closed, err := e.auth.Position(ctx, id)
	if err != nil {
		return out, err
	}
	if closed.Status != evmview.PositionClosed {
		return out, errors.New("NFT redemption did not clear the vault position")
	}
	declared, err := e.read(ctx, e.roles.PositionVault, e.vault, "declaredReserve", common.HashLength, uint8(nfttypes.IsolatedReserveDEL), common.Address{})
	if err != nil {
		return out, err
	}
	held := e.evm.SpendableCoin(ctx, e.roles.PositionVault)
	if held == nil || held.ToBig().Cmp(common.BytesToHash(declared).Big()) < 0 {
		return out, errors.New("NFT redemption left insufficient DEL backing for remaining vault positions")
	}
	return NFTPositionRedemption{PositionID: id, NextPositionID: closed.NextPositionID, Owner: owner,
		Recipient: recipient, Reserve: sdkmath.NewIntFromBigInt(amount)}, nil
}
