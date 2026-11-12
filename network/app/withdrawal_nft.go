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
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
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
	return bytes.Equal(msg.Data[:4], c.abi.Methods["delegateNFT1155"].ID) ||
		bytes.Equal(msg.Data[:4], c.abi.Methods["withdrawNFT"].ID) || c.isConvertedNFTDeposit(msg.Data)
}

func (c *withdrawalController) isConvertedNFTDeposit(data []byte) bool {
	method, known := c.abi.Methods["delegateNFT1155WithConversion"]
	return c.customCollateral != nil && known && len(data) >= 4 && bytes.Equal(data[:4], method.ID)
}

func (c *withdrawalController) projectSignedNFT(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	child, commit := ctx.CacheContext()
	if converted := c.isConvertedNFTDeposit(msg.Data); converted || bytes.Equal(msg.Data[:4], c.abi.Methods["delegateNFT1155"].ID) {
		proof, custom, err := c.verifyNFTDeposit(child, msg, receipt, converted)
		if err != nil {
			return fmt.Errorf("verify signed NFT isolation: %w", err)
		}
		p := proof.Position
		isolation := nftkeeper.GatewayIsolation{
			Vault: p.Asset.Contract, Origin: p.Origin.Contract, Owner: msg.From, Gateway: c.profile.Gateway,
			PositionID: p.Asset.TokenID, OriginTokenID: p.Origin.TokenID, NextPositionID: p.NextPositionID,
			Quantity: p.OriginQuantity, OriginSupplyAfter: proof.OriginSupplyAfter,
			Reserve: sdkmath.NewIntFromBigInt(p.ReserveAmount.Big()), OriginReserveAfter: proof.OriginReserveAfter,
		}
		var lineage nfttypes.IsolatedPositionLineage
		if converted {
			lineage, err = c.nft.AdmitGatewayConvertedIsolation(child, isolation, custom)
		} else {
			lineage, err = c.nft.AdmitGatewayIsolation(child, isolation)
		}
		if err != nil {
			return fmt.Errorf("admit native NFT isolation: %w", err)
		}
		if err := c.keeper.ApplyGatewayNFTDelegation(child, validatorkeeper.GatewayNFTPosition{
			Companion: c.delStakePosition(proof.Companion), StakeID: proof.StakeID, Lineage: lineage,
		}); err != nil {
			return fmt.Errorf("project native NFT delegation: %w", err)
		}
	} else {
		stakeID, err := c.executor.SignedNFTWithdrawalStakeID(msg)
		if err != nil {
			return err
		}
		active, err := c.keeper.GetEVMStakeBinding(child, validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT, c.profile.Gateway[:], stakeID[:])
		if err != nil {
			return fmt.Errorf("load active NFT withdrawal binding: %w", err)
		}
		var proof settlement.NFTWithdrawal
		if common.BytesToHash(active.PenaltyCursor) == (common.Hash{}) {
			proof, err = c.executor.VerifySignedNFTWithdrawal(child, msg, receipt)
		} else {
			proof, err = c.executor.VerifySignedPenalizedNFTWithdrawal(child, msg, receipt, active)
		}
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
		position := validatorkeeper.GatewayNFTPosition{
			Companion: c.delStakePosition(proof.Companion), StakeID: proof.StakeID, Lineage: lineage,
		}
		position.Companion.PenaltyCursor = proof.Companion.PenaltyCursor
		if proof.PenaltyCursor == (common.Hash{}) {
			err = c.keeper.ApplyGatewayNFTWithdrawal(child, position, proof.FrozenIndex, proof.Deadline)
		} else {
			err = c.keeper.ApplyGatewayPenalizedNFTWithdrawal(child, position, proof.FrozenIndex, proof.Deadline, proof.PenaltyCursor, proof.CompanionCursorAcknowledged)
		}
		if err != nil {
			return fmt.Errorf("project native NFT withdrawal: %w", err)
		}
	}
	commit()
	return nil
}

func (c *withdrawalController) verifyNFTDeposit(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt,
	converted bool,
) (settlement.NFTIsolation, sdk.Coin, error) {
	if !converted {
		verify := c.executor.VerifySignedNFTIsolation
		if c.profile.EnableNativeNFTDowntime {
			verify = c.executor.VerifySignedNFTIsolationWithDowntime
		}
		proof, err := verify(ctx, msg, receipt)
		return proof, sdk.Coin{}, err
	}
	proof, err := c.executor.VerifySignedNFTIsolationWithConversion(ctx, msg, receipt,
		c.profile.CurveConverter, c.profile.TokenCenter, c.profile.EnableNativeNFTDowntime)
	if err != nil {
		return settlement.NFTIsolation{}, sdk.Coin{}, err
	}
	if err := c.applyConvertedCoin(ctx, proof.InputToken, proof.InputAmount.BigInt(), proof.NativeOutput.BigInt(), proof.ReserveLog); err != nil {
		return settlement.NFTIsolation{}, sdk.Coin{}, err
	}
	coin, err := c.customCollateral.keeper.GetCoinByDRC(ctx, proof.InputToken.Hex())
	if err != nil {
		return settlement.NFTIsolation{}, sdk.Coin{}, err
	}
	return proof.NFTIsolation, sdk.NewCoin(coin.Denom, proof.InputAmount), nil
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
