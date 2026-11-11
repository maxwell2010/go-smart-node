package app

import (
	"bytes"
	"errors"
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
)

var (
	nftApprovalSelector  = crypto.Keccak256([]byte("setApprovalForAll(address,bool)"))[:4]
	nftApprovalTopic     = crypto.Keccak256Hash([]byte("ApprovalForAll(address,address,bool)"))
	nftTransferSelectors = [][]byte{
		crypto.Keccak256([]byte("transferFrom(address,address,uint256)"))[:4],
		crypto.Keccak256([]byte("safeTransferFrom(address,address,uint256)"))[:4],
		crypto.Keccak256([]byte("safeTransferFrom(address,address,uint256,bytes)"))[:4],
		crypto.Keccak256([]byte("safeTransferFrom(address,address,uint256,uint256,bytes)"))[:4],
		crypto.Keccak256([]byte("safeBatchTransferFrom(address,address,uint256[],uint256[],bytes)"))[:4],
	}
)

func (c *withdrawalController) isNFTStakeCall(msg core.Message) bool {
	if !c.profile.EnableNFTDelegation || msg.To == nil || *msg.To != c.profile.Gateway || len(msg.Data) < 4 {
		return false
	}
	return bytes.Equal(msg.Data[:4], c.abi.Methods["delegateNFT1155"].ID) || bytes.Equal(msg.Data[:4], c.abi.Methods["withdrawNFT"].ID)
}

func (c *withdrawalController) projectSignedNFT(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	child, commit := ctx.CacheContext()
	if bytes.Equal(msg.Data[:4], c.abi.Methods["delegateNFT1155"].ID) {
		proof, err := c.executor.VerifySignedNFTIsolation(child, msg, receipt)
		if err != nil {
			return fmt.Errorf("verify signed NFT isolation: %w", err)
		}
		p := proof.Position
		lineage, err := c.nft.AdmitGatewayIsolation(child, nftkeeper.GatewayIsolation{
			Vault: p.Asset.Contract, Origin: p.Origin.Contract, Owner: msg.From, Gateway: c.profile.Gateway,
			PositionID: p.Asset.TokenID, OriginTokenID: p.Origin.TokenID, NextPositionID: p.NextPositionID,
			Quantity: p.OriginQuantity, OriginSupplyAfter: proof.OriginSupplyAfter,
			Reserve: sdkmath.NewIntFromBigInt(p.ReserveAmount.Big()), OriginReserveAfter: proof.OriginReserveAfter,
		})
		if err != nil {
			return fmt.Errorf("admit native NFT isolation: %w", err)
		}
		if err := c.keeper.ApplyGatewayNFTDelegation(child, validatorkeeper.GatewayNFTPosition{
			Companion: c.delStakePosition(proof.Companion), StakeID: proof.StakeID, Lineage: lineage,
		}); err != nil {
			return fmt.Errorf("project native NFT delegation: %w", err)
		}
	} else {
		proof, err := c.executor.VerifySignedNFTWithdrawal(child, msg, receipt)
		if err != nil {
			return fmt.Errorf("verify signed NFT withdrawal: %w", err)
		}
		p := proof.Position
		lineage, err := c.nft.GetIsolatedLineage(child, p.Asset.Contract[:], p.Asset.TokenID[:])
		if err != nil {
			return err
		}
		vault, err := c.nft.GetIsolatedVaultState(child, p.Asset.Contract[:])
		if err != nil {
			return err
		}
		unit, err := c.nft.GetSubToken(child, lineage.ReceiptNativeTokenID, lineage.ReceiptSubTokenID)
		if err != nil {
			return err
		}
		owner, err := sdk.AccAddressFromBech32(unit.Owner)
		if err != nil {
			return err
		}
		reserve, err := p.Reserve()
		if err != nil {
			return err
		}
		if !bytes.Equal(owner, c.profile.Gateway[:]) || !bytes.Equal(vault.NextPositionID, p.NextPositionID[:]) ||
			!bytes.Equal(lineage.OriginContract, p.Origin.Contract[:]) || !bytes.Equal(lineage.OriginTokenID, p.Origin.TokenID[:]) ||
			len(lineage.OriginSubTokenIDs) != int(p.OriginQuantity) || !bytes.Equal(lineage.ParentPositionID, p.ParentPositionID[:]) || !lineage.Reserve.Equal(&reserve) {
			return errors.New("NFT withdrawal custody and backing differ from native lineage")
		}
		if err := c.keeper.ApplyGatewayNFTWithdrawal(child, validatorkeeper.GatewayNFTPosition{
			Companion: c.delStakePosition(proof.Companion), StakeID: proof.StakeID, Lineage: lineage,
		}, proof.FrozenIndex, proof.Deadline); err != nil {
			return fmt.Errorf("project native NFT withdrawal: %w", err)
		}
	}
	commit()
	return nil
}

// checkNFTAdmission prevents an unrelated EVM operation from changing a known
// origin or the vault without its native projection. Direct ERC1155/721
// operator approvals carry no ownership or reserve delta.
func (c *withdrawalController) checkNFTAdmission(ctx sdk.Context, msg core.Message, logs []*ethtypes.Log, signed *ethtypes.Receipt) error {
	if !c.profile.EnableNFTDelegation {
		return nil
	}
	if msg.To != nil {
		managed, err := c.isManagedNFT(ctx, *msg.To)
		if err != nil {
			return err
		}
		if managed {
			if signed != nil {
				if isDirectNFTApproval(msg, logs) {
					return nil
				}
				if isDirectNFTTransfer(msg) {
					return c.assets.ProjectWalletTransfer(ctx, signed, *msg.To, c.profile.Gateway, c.profile.PositionVault)
				}
			}
			return errors.New("NFT operation requires a supported native projection")
		}
	}
	seen := make(map[common.Address]bool)
	for _, log := range logs {
		if log == nil || seen[log.Address] {
			continue
		}
		seen[log.Address] = true
		managed, err := c.isManagedNFT(ctx, log.Address)
		if err != nil {
			return err
		}
		if managed {
			return errors.New("indirect NFT operation requires a supported native projection")
		}
	}
	return nil
}

func isDirectNFTTransfer(msg core.Message) bool {
	if msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) < 4 {
		return false
	}
	for _, selector := range nftTransferSelectors {
		if bytes.Equal(msg.Data[:4], selector) {
			return true
		}
	}
	return false
}

func (c *withdrawalController) isManagedNFT(ctx sdk.Context, addr common.Address) (bool, error) {
	if addr == (common.Address{}) {
		return false, nil
	}
	if addr == c.profile.PositionVault {
		return true, nil
	}
	_, err := c.nft.GetCollectionByDRC(ctx, addr.Hex())
	if errors.Is(err, nftkeeper.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func isDirectNFTApproval(msg core.Message, logs []*ethtypes.Log) bool {
	if msg.To == nil || msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) != 4+2*common.HashLength ||
		!bytes.Equal(msg.Data[:4], nftApprovalSelector) || len(logs) != 1 || logs[0] == nil {
		return false
	}
	operator := msg.Data[4 : 4+common.HashLength]
	approved := msg.Data[4+common.HashLength:]
	var falseWord, trueWord common.Hash
	trueWord[common.HashLength-1] = 1
	log := logs[0]
	return bytes.Equal(operator[:common.HashLength-common.AddressLength], falseWord[:common.HashLength-common.AddressLength]) &&
		(bytes.Equal(approved, falseWord[:]) || bytes.Equal(approved, trueWord[:])) && log.Address == *msg.To && !log.Removed &&
		len(log.Topics) == 3 && log.Topics[0] == nftApprovalTopic && log.Topics[1] == common.BytesToHash(msg.From[:]) &&
		log.Topics[2] == common.BytesToHash(operator) && bytes.Equal(log.Data, approved)
}
