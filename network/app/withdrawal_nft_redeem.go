package app

import (
	"bytes"
	"errors"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

var nftRedeemSelector = crypto.Keccak256([]byte("redeemPosition(uint256,address)"))[:4]

func (c *withdrawalController) isNFTReceiptRedemption(msg core.Message) bool {
	return c.profile.EnableNFTDelegation && msg.To != nil && *msg.To == c.profile.PositionVault &&
		len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], nftRedeemSelector)
}

func (c *withdrawalController) projectNFTReceiptRedemption(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	positionID, err := c.executor.SignedNFTPositionRedemptionID(msg)
	if err != nil {
		return err
	}
	child, commit := ctx.CacheContext()
	lineage, err := c.nft.GetIsolatedLineage(child, c.profile.PositionVault[:], positionID[:])
	if err != nil {
		return fmt.Errorf("load wallet receipt for redemption: %w", err)
	}
	unit, err := c.nft.GetSubToken(child, lineage.ReceiptNativeTokenID, lineage.ReceiptSubTokenID)
	if err != nil {
		return err
	}
	owner, err := sdk.AccAddressFromBech32(unit.Owner)
	if err != nil || !bytes.Equal(owner, msg.From[:]) {
		return errors.New("NFT redemption requires the authenticated native receipt owner")
	}
	proof, err := c.executor.VerifySignedNFTPositionRedemption(child, msg, receipt, lineage, msg.From)
	if err != nil {
		return fmt.Errorf("verify signed NFT redemption: %w", err)
	}
	if c.delRecipientAllowed == nil || !c.delRecipientAllowed(sdk.AccAddress(proof.Recipient[:])) {
		return errors.New("NFT redemption recipient is a reserved DEL account")
	}
	if err := c.nft.RetireIsolatedDELReceipt(child, c.profile.Gateway, proof.Owner, lineage, proof.NextPositionID); err != nil {
		return fmt.Errorf("retire redeemed native NFT receipt: %w", err)
	}
	if err := child.EventManager().EmitTypedEvent(&nfttypes.EventBurnToken{
		Sender: unit.Owner, ID: lineage.ReceiptNativeTokenID,
		Return: sdk.NewCoin(identity.BaseDenom, proof.Reserve).String(), SubTokenIDs: []uint32{lineage.ReceiptSubTokenID},
	}); err != nil {
		return fmt.Errorf("emit native NFT redemption event: %w", err)
	}
	commit()
	return nil
}
