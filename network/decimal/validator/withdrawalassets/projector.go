// Package withdrawalassets projects authenticated Gateway completion receipts
// into the native coin and NFT stores inside the withdrawal attempt cache.
package withdrawalassets

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	nftview "github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nftkeeper "github.com/maxwell2010/go-smart-node/network/decimal/nft/keeper"
	nftreceipts "github.com/maxwell2010/go-smart-node/network/decimal/nft/receipts"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatorreceipts "github.com/maxwell2010/go-smart-node/network/decimal/validator/receipts"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

type Projector struct {
	reserve  *coinkeeper.ReserveHook
	nft      *nftkeeper.StateKeeper
	reader   *nftview.Reader
	bindings *validatorkeeper.StateKeeper
	decoder  *nftreceipts.Decoder
	wallet   *nftreceipts.Decoder
}

func New(reserve *coinkeeper.ReserveHook, nft *nftkeeper.StateKeeper, reader *nftview.Reader, bindings *validatorkeeper.StateKeeper, limits nftreceipts.Limits, enableWallet bool) (*Projector, error) {
	if reserve == nil || nft == nil || reader == nil || bindings == nil {
		return nil, errors.New("withdrawal projection requires native reserve, NFT and binding keepers and the authenticated EVM reader")
	}
	contract721, err := contracts.CompiledABI("NewNetworkDRC721")
	if err != nil {
		return nil, err
	}
	contract1155, err := contracts.CompiledABI("MintCandyIsolatedDRC1155")
	if err != nil {
		return nil, err
	}
	decoder, err := nftreceipts.NewDecoder(reader, contract721, contract1155, limits)
	if err != nil {
		return nil, err
	}
	p := &Projector{reserve: reserve, nft: nft, reader: reader, bindings: bindings, decoder: decoder}
	if enableWallet {
		p.wallet, err = nftreceipts.NewDecoder(reader, contract721, contract1155, nftreceipts.Limits{
			MaxLogs: limits.MaxLogs, MaxBytes: limits.MaxBytes, MaxAssets: 32, MaxIDs: 32, MaxUnits: nfttypes.MaxIsolatedOriginUnits,
		})
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}

// ProjectWalletTransfer uses the same authenticated ownership planner as
// settlement, without allowing an ordinary transfer to affect escrow.
func (p *Projector) ProjectWalletTransfer(ctx sdk.Context, receipt *ethtypes.Receipt, collection, gateway, vault common.Address) error {
	if p.wallet == nil {
		return errors.New("wallet NFT transfer projection is disabled")
	}
	// Each supported direct transfer emits one collection log, including an
	// ERC1155 batch. Extra logs could include changes from a receiver callback.
	if receipt == nil || len(receipt.Logs) != 1 || receipt.Logs[0] == nil || receipt.Logs[0].Address != collection {
		return errors.New("wallet NFT transfer requires one direct collection log")
	}
	capture, err := validatorreceipts.NewStakeCaptureSession(p.bindings, stakeSource{reader: p.reader})
	if err != nil {
		return err
	}
	planner, err := nftreceipts.NewPlanner(p.wallet, p.nft, p.reader, capture)
	if err != nil {
		return err
	}
	plan, err := planner.Plan(ctx, nftreceipts.Execution{EntryPoint: nftreceipts.EntryPointSignedEthereum, Receipt: receipt})
	if err != nil {
		return err
	}
	movements := plan.Movements()
	if len(movements) == 0 {
		return errors.New("wallet NFT transfer must identify the authenticated movement")
	}
	for _, move := range movements {
		transfer := move.Transfer
		if transfer.Asset.Contract != collection || transfer.From == gateway || transfer.To == gateway || transfer.From == vault || transfer.To == vault {
			return errors.New("wallet NFT transfer cannot move Gateway or vault custody or another collection")
		}
	}
	return planner.Apply(ctx, plan)
}

// Project applies only the asset effects of one already authenticated Gateway
// completion. Its caller verifies the canonical receipt marker, completed EVM
// row and coin payout before calling Project, then retires the native binding
// and removes the paired queue entry in the same outer attempt cache.
func (p *Projector) Project(ctx sdk.Context, row validatortypes.EVMFrozenBinding, receipt *ethtypes.Receipt) error {
	if p == nil || p.decoder == nil || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return errors.New("withdrawal asset projection requires an initialized projector and successful EVM receipt")
	}
	if err := row.Validate(); err != nil {
		return err
	}
	if !row.Active || row.FreezeType != 1 || row.ReserveAsset.NativeDenom != identity.BaseDenom {
		return errors.New("withdrawal asset projection requires an active DEL-denominated withdrawal")
	}
	child, commit := ctx.CacheContext()
	stored, err := p.bindings.GetEVMFrozenBinding(child, row.Gateway, row.FrozenIndex)
	if err != nil {
		return err
	}
	if err = requireSameFrozenRow(&stored, &row); err != nil {
		return err
	}
	execution := nftreceipts.Execution{EntryPoint: nftreceipts.EntryPointWithdrawalSettlement, Receipt: receipt}
	var nftPlan *nftreceipts.Plan
	var planner *nftreceipts.Planner
	switch row.SourceKind {
	case validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN:
		if row.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL {
			return errors.New("coin withdrawal asset projection requires native DEL payout")
		}
		decoded, err := p.decoder.Decode(child, execution)
		if err != nil {
			return err
		}
		if len(decoded.Transfers) != 0 {
			return errors.New("coin withdrawal changed registered NFT ownership")
		}
	case validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT:
		if row.NFTPosition.Mode != validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT {
			return errors.New("NFT withdrawal asset projection requires an isolated receipt")
		}
		capture, err := validatorreceipts.NewStakeCaptureSession(p.bindings, stakeSource{reader: p.reader})
		if err != nil {
			return err
		}
		planner, err = nftreceipts.NewPlanner(p.decoder, p.nft, p.reader, capture)
		if err != nil {
			return err
		}
		nftPlan, err = planner.Plan(child, execution)
		if err != nil {
			return err
		}
		if err = requireExactMovement(row, nftPlan.Movements()); err != nil {
			return err
		}
		if err = p.checkIsolatedBacking(child, row, common.BytesToAddress(row.Beneficiary), false); err != nil {
			return err
		}
	default:
		return errors.New("unsupported withdrawal asset kind")
	}

	// Ordinary WDEL unwrap and wallet custody transfer can leave DRC20 reserve
	// metadata unchanged. Still validate and apply any registered reserve effects
	// through the same authenticated planner; a later failure discards both plans.
	reserve, err := p.reserve.Prepare(child, receipt)
	if err != nil {
		return fmt.Errorf("prepare withdrawal reserve effects: %w", err)
	}
	if err = reserve.Apply(child); err != nil {
		return fmt.Errorf("apply withdrawal reserve effects: %w", err)
	}
	if planner != nil {
		if err = planner.Apply(child, nftPlan); err != nil {
			return fmt.Errorf("apply withdrawal NFT ownership: %w", err)
		}
	}
	commit()
	return nil
}

func requireSameFrozenRow(stored, requested *validatortypes.EVMFrozenBinding) error {
	actual, err := validatortypes.CanonicalBindingBytes(stored)
	if err != nil {
		return err
	}
	expected, err := validatortypes.CanonicalBindingBytes(requested)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("withdrawal asset projection binding differs from current native prestate")
	}
	return nil
}

type stakeSource struct{ reader *nftview.Reader }

func (s stakeSource) AuthenticateStakeSource(ctx sdk.Context, execution nftreceipts.Execution) (validatorreceipts.StakeSourceProfile, error) {
	profile, err := s.reader.AuthenticateStakeSource(ctx, execution)
	if err != nil {
		return validatorreceipts.StakeSourceProfile{}, err
	}
	return validatorreceipts.StakeSourceProfile{
		Domain:  validatortypes.EVMSourceDomain{ChainID: profile.ChainID, EVMChainID: profile.EVMChainID},
		Gateway: profile.Gateway, Fingerprint: profile.Fingerprint,
	}, nil
}

func requireExactMovement(row validatortypes.EVMFrozenBinding, movements []nftreceipts.Movement) error {
	if len(movements) != 1 {
		return errors.New("NFT withdrawal must contain exactly one registered custody transfer")
	}
	custody := row.NFTPosition.Custody
	move := movements[0]
	transfer := move.Transfer
	if transfer.Asset != (nftreceipts.AssetKey{Contract: common.BytesToAddress(custody.Contract), TokenID: common.BytesToHash(custody.TokenID)}) ||
		transfer.From != common.BytesToAddress(row.Gateway) || transfer.To != common.BytesToAddress(row.Beneficiary) ||
		transfer.NFTType != nfttypes.NftType_NFT721 || transfer.Quantity != 1 || move.NativeTokenID != custody.NativeTokenID ||
		!validatortypes.SameNativeUnitIDs(move.SubTokenIDs, custody.NativeSubTokenIDs) {
		return errors.New("NFT withdrawal transfer differs from its exact frozen custody, units or beneficiary")
	}
	return nil
}

// AttestGenesisNFT verifies the existing native NFT unit and lineage against
// the same authenticated EVM position while it is still held by the Gateway.
// It performs only reads and shares the genesis attestation gas meter.
func (p *Projector) AttestGenesisNFT(ctx sdk.Context, row validatortypes.EVMFrozenBinding) error {
	if p == nil || p.nft == nil || p.reader == nil || p.bindings == nil {
		return errors.New("missing native genesis NFT attestor")
	}
	if err := row.Validate(); err != nil {
		return err
	}
	if !row.Active || row.FreezeType != 1 || row.SourceKind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT || row.NFTPosition.Mode != validatortypes.NFTPositionMode_NFT_POSITION_ISOLATED_RECEIPT || row.ReserveAsset.NativeDenom != identity.BaseDenom {
		return errors.New("genesis NFT attestation requires an active isolated DEL-denominated withdrawal")
	}
	stored, err := p.bindings.GetEVMFrozenBinding(ctx, row.Gateway, row.FrozenIndex)
	if err != nil {
		return err
	}
	if err = requireSameFrozenRow(&stored, &row); err != nil {
		return err
	}
	return p.checkIsolatedBacking(ctx, row, common.BytesToAddress(row.Gateway), true)
}

func (p *Projector) checkIsolatedBacking(ctx sdk.Context, row validatortypes.EVMFrozenBinding, expectedOwner common.Address, checkNativeEscrow bool) error {
	position := row.NFTPosition
	lineage, err := p.nft.GetIsolatedLineage(ctx, position.Vault, position.PositionID)
	if err != nil {
		return err
	}
	vault, err := p.nft.GetIsolatedVaultState(ctx, position.Vault)
	if err != nil {
		return err
	}
	actual, err := p.reader.Position(ctx, common.BytesToHash(position.PositionID))
	if err != nil {
		return err
	}
	if err = requireExactBacking(row, lineage, vault, actual, expectedOwner); err != nil {
		return err
	}
	token, err := p.nft.GetToken(ctx, lineage.ReceiptNativeTokenID)
	if err != nil {
		return err
	}
	unit, err := p.nft.GetSubToken(ctx, lineage.ReceiptNativeTokenID, lineage.ReceiptSubTokenID)
	if err != nil {
		return err
	}
	if !matchesNativeReserve(token.Reserve, row) || (unit.Reserve != nil && !matchesNativeReserve(*unit.Reserve, row)) {
		return errors.New("native isolated receipt reserve differs from authenticated backing")
	}
	if checkNativeEscrow {
		gateway, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).BytesToString(row.Gateway)
		if err != nil {
			return err
		}
		if unit.Owner != gateway {
			return errors.New("native genesis NFT unit is not in Gateway escrow")
		}
	}
	return nil
}

func matchesNativeReserve(coin sdk.Coin, row validatortypes.EVMFrozenBinding) bool {
	return coin.Denom == row.ReserveAsset.NativeDenom && !coin.Amount.IsNil() && coin.Amount.Equal(row.ReportedReserveAmount)
}

func requireExactBacking(row validatortypes.EVMFrozenBinding, lineage nfttypes.IsolatedPositionLineage, vault nfttypes.IsolatedVaultState, actual nftview.PositionSnapshot, expectedOwner common.Address) error {
	position := row.NFTPosition
	if position.Origin.Standard != 1155 {
		return errors.New("isolated position origin standard differs from the approved ERC1155 profile")
	}
	if actual.Status != nftview.PositionActive || actual.Owner != expectedOwner ||
		actual.Asset.Contract != common.BytesToAddress(position.Vault) || actual.Asset.TokenID != common.BytesToHash(position.PositionID) ||
		actual.Origin.Contract != common.BytesToAddress(position.Origin.Contract) || actual.Origin.TokenID != common.BytesToHash(position.Origin.TokenID) ||
		actual.OriginQuantity != uint32(len(position.Origin.NativeSubTokenIDs)) || actual.ParentPositionID != common.BytesToHash(position.ParentPositionID) ||
		actual.NextPositionID != common.BytesToHash(vault.NextPositionID) {
		return errors.New("isolated position differs from approved vault, lineage or expected owner")
	}
	if !bytes.Equal(lineage.Vault, position.Vault) || !bytes.Equal(lineage.PositionID, position.PositionID) || !bytes.Equal(lineage.ParentPositionID, position.ParentPositionID) ||
		lineage.ReceiptNativeTokenID != position.Custody.NativeTokenID || lineage.ReceiptSubTokenID != position.Custody.NativeSubTokenIDs[0] ||
		!bytes.Equal(lineage.OriginContract, position.Origin.Contract) || !bytes.Equal(lineage.OriginTokenID, position.Origin.TokenID) ||
		lineage.OriginNativeTokenID != position.Origin.NativeTokenID || !validatortypes.SameNativeUnitIDs(lineage.OriginSubTokenIDs, position.Origin.NativeSubTokenIDs) {
		return errors.New("withdrawal binding differs from native isolated lineage")
	}
	reserve, err := actual.Reserve()
	if err != nil {
		return err
	}
	if !lineage.Reserve.Equal(&reserve) || reserve.Kind != row.ReserveAsset.ReserveKind || !bytes.Equal(reserve.Token, row.ReserveAsset.Token) ||
		actual.ReserveAmount.Big().Cmp(row.ReportedReserveAmount.BigInt()) != 0 {
		return errors.New("isolated withdrawal changed the bound reserve currency or amount")
	}
	return nil
}
