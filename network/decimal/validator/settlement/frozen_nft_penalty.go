package settlement

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/decimal/nft/evmview"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

// FrozenNFTPenaltyProof binds one checkpoint advance to the reserve of the
// isolated receipt. The receipt quantity and withdrawal deadline are fixed.
type FrozenNFTPenaltyProof struct {
	Index                     common.Hash
	BeforeCursor, AfterCursor common.Hash
	RemainingReserve, Burned  sdkmath.Int
	InfractionHeight          int64
	Eligible                  bool
}

// ApplyFrozenNFTPenalty realizes or checkpoints one announced penalty in
// the exact frozen NFT reserve. The caller reconciles the native queue and
// receipt in the same cache before the native burn can be committed.
func (e *Executor) ApplyFrozenNFTPenalty(ctx sdk.Context, row validatortypes.EVMFrozenBinding, infractionHeight int64,
	percentage uint16, nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, FrozenNFTPenaltyProof) error,
) error {
	if nativeSupply == nil || project == nil || ctx.ExecMode() != sdk.ExecModeFinalize ||
		infractionHeight <= 0 || infractionHeight > ctx.BlockHeight() || percentage == 0 || percentage > 100 {
		return errors.New("frozen NFT penalty requires finalized block, approved infraction, supply and native projection")
	}
	index, beneficiary, err := e.withdrawalIdentity(row)
	if err != nil {
		return err
	}
	now := ctx.BlockTime().Unix()
	if !row.Active || row.SourceKind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_NFT ||
		row.NativeTarget.StakeType != validatortypes.StakeType_NFT || row.ReserveAsset.ReserveKind != nfttypes.IsolatedReserveDEL ||
		row.ReserveAsset.NativeDenom != identity.BaseDenom || row.CreatedHeight <= 0 || row.UpdatedHeight > ctx.BlockHeight() || now <= 0 ||
		common.BytesToHash(row.CompletionTimestamp).Big().Cmp(big.NewInt(now)) <= 0 {
		return errors.New("penalty requires an unmatured ordinary isolated NFT claim backed by native DEL")
	}
	child, commit := ctx.CacheContext()
	source, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	sender := masterPenaltySender()
	if err := e.requireInert(child, sender); err != nil {
		return err
	}
	words, err := e.readFrozen(child, index)
	if err != nil {
		return err
	}
	if _, err := e.matchFrozen(words, row, statusFrozen); err != nil {
		return err
	}
	if words[6] != (common.Hash{}) || words[7] != (common.Hash{}) {
		return errors.New("ordinary frozen NFT penalty cannot consume a held receipt")
	}
	checkpoint, err := e.readFrozenDELCheckpoint(child, index)
	if err != nil {
		return err
	}
	if checkpoint.creationHeight != common.BigToHash(big.NewInt(row.CreatedHeight)) {
		return errors.New("frozen NFT checkpoint differs from the native creation height")
	}
	next := new(big.Int).Add(checkpoint.cursor.Big(), big.NewInt(1))
	if next.BitLen() > 256 {
		return errors.New("frozen NFT penalty cursor overflow")
	}
	validator := common.BytesToAddress(row.SourceValidator)
	proof := FrozenNFTPenaltyProof{Index: index, BeforeCursor: checkpoint.cursor, AfterCursor: common.BigToHash(next),
		InfractionHeight: infractionHeight, Eligible: row.CreatedHeight >= infractionHeight}
	status, err := e.verifyAnnouncedPenalty(child, validator, checkpoint.cursor, proof.AfterCursor, percentage)
	if err != nil {
		return err
	}
	before, err := e.readFrozenNFTPenaltyState(child, row, beneficiary, row.ReportedReserveAmount, nativeSupply)
	if err != nil {
		return err
	}
	burned := new(big.Int)
	if proof.Eligible {
		burned.Mul(row.ReportedReserveAmount.BigInt(), new(big.Int).SetUint64(uint64(percentage)))
		burned.Quo(burned, big.NewInt(100))
	}
	proof.Burned = sdkmath.NewIntFromBigInt(burned)
	proof.RemainingReserve = row.ReportedReserveAmount.Sub(proof.Burned)
	data, err := e.gateway.Pack("applyFrozenDELPenalty", index.Big(), big.NewInt(infractionHeight), row.ReportedReserveAmount.BigInt(), checkpoint.cursor.Big())
	if err != nil {
		return err
	}
	authorized := child.WithValue(frozenDELPenaltyKey{}, frozenDELPenaltyCall{executor: e, height: child.BlockHeight(), data: bytes.Clone(data)})
	result, err := e.invokePenaltyWithGas(authorized, sender, e.roles.Gateway, data, nftPenaltyEVMGas)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Gateway frozen NFT penalty returned unexpected data")
	}
	newWords, err := e.readFrozen(child, index)
	if err != nil {
		return err
	}
	if newWords != words {
		return errors.New("frozen NFT penalty changed the claim identity, deadline or receipt quantity")
	}
	recorded, err := e.readFrozenDELCheckpoint(child, index)
	if err != nil {
		return err
	}
	if recorded.creationHeight != checkpoint.creationHeight || recorded.cursor != proof.AfterCursor {
		return errors.New("frozen NFT penalty did not advance the exact checkpoint once")
	}
	after, err := e.readFrozenNFTPenaltyState(child, row, beneficiary, proof.RemainingReserve, nativeSupply)
	if err != nil {
		return err
	}
	if !bytes.Equal(before.activeReserve, after.activeReserve) {
		return errors.New("frozen NFT penalty changed the active validator reserve")
	}
	if err := verifyNFTVaultPenaltyDeltas(before.position, after.position, before.payoutState, after.payoutState,
		before.bankSupply, after.bankSupply, proof.Burned); err != nil {
		return err
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
	if err := e.verifyFrozenNFTPenaltyLogs(receipt, row.ReportedReserveAmount, after.position, percentage, proof); err != nil {
		return err
	}
	afterSource, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	afterStatus, err := e.verifyAnnouncedPenalty(child, validator, checkpoint.cursor, proof.AfterCursor, percentage)
	if err != nil {
		return err
	}
	if afterSource != source || afterStatus != status {
		return errors.New("authenticated source or Master status changed during frozen NFT penalty")
	}
	if err := project(child, proof); err != nil {
		return fmt.Errorf("project frozen NFT penalty: %w", err)
	}
	commit()
	return nil
}

type frozenNFTPenaltyState struct {
	position      evmview.PositionSnapshot
	activeReserve []byte
	payoutState
	bankSupply sdkmath.Int
}

func (e *Executor) readFrozenNFTPenaltyState(ctx sdk.Context, row validatortypes.EVMFrozenBinding, beneficiary common.Address,
	amount sdkmath.Int, nativeSupply func(sdk.Context) sdkmath.Int,
) (frozenNFTPenaltyState, error) {
	var out frozenNFTPenaltyState
	p := row.NFTPosition
	positionID := common.BytesToHash(p.PositionID)
	position, err := e.auth.Position(ctx, positionID)
	if err != nil {
		return out, err
	}
	if position.Status != evmview.PositionActive || position.Owner != e.roles.Gateway ||
		position.Asset.Contract != e.roles.PositionVault || position.Asset.TokenID != positionID ||
		position.ParentPositionID != (common.Hash{}) || common.BytesToHash(p.ParentPositionID) != position.ParentPositionID ||
		p.Origin.Standard != 1155 || position.Origin.Contract != common.BytesToAddress(p.Origin.Contract) ||
		position.Origin.TokenID != common.BytesToHash(p.Origin.TokenID) || position.OriginQuantity != uint32(len(p.Origin.NativeSubTokenIDs)) ||
		position.ReserveKind != nfttypes.IsolatedReserveDEL || position.ReserveToken != (common.Address{}) ||
		position.ReserveAmount != common.BigToHash(amount.BigInt()) {
		return out, errors.New("frozen NFT penalty differs from exact live Vault custody and DEL reserve")
	}
	activeReserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength,
		common.BytesToAddress(row.SourceValidator), e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	balances, err := e.payoutState(ctx, payout{asset: e.roles.WrappedDEL, beneficiary: beneficiary}, common.Address{})
	if err != nil {
		return out, err
	}
	supply := nativeSupply(ctx)
	if supply.IsNil() || supply.IsNegative() {
		return out, errors.New("invalid bank supply for frozen NFT penalty")
	}
	return frozenNFTPenaltyState{position: position, activeReserve: activeReserve, payoutState: balances, bankSupply: supply}, nil
}

func (e *Executor) verifyFrozenNFTPenaltyLogs(receipt *ethtypes.Receipt, before sdkmath.Int, position evmview.PositionSnapshot,
	percentage uint16, proof FrozenNFTPenaltyProof,
) error {
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	vaultLogs := 0
	if proof.Eligible && before.IsPositive() {
		vaultLogs = 2
		if proof.Burned.IsPositive() {
			vaultLogs++
		}
	}
	if len(markers) != 0 || len(receipt.Logs) != 1+vaultLogs {
		return errors.New("unexpected logs for one frozen NFT penalty")
	}
	if vaultLogs > 0 {
		if err := e.verifyNFTPenaltyVaultLogs(receipt.Logs[:vaultLogs], position, percentage, proof.Burned.BigInt()); err != nil {
			return err
		}
	}
	topic := crypto.Keccak256Hash([]byte("FrozenNFTPenaltyApplied(uint256,uint256,uint256,uint256,uint256,bool)"))
	if !matchesNFTEvent(receipt.Logs[vaultLogs], e.roles.Gateway, []common.Hash{topic, proof.Index, proof.AfterCursor},
		common.BigToHash(big.NewInt(proof.InfractionHeight)), common.BigToHash(before.BigInt()),
		common.BigToHash(proof.RemainingReserve.BigInt()), boolWord(proof.Eligible)) {
		return errors.New("Gateway did not emit the exact frozen NFT checkpoint and reserve")
	}
	return nil
}
