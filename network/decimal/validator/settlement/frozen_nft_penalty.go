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
	"github.com/ethereum/go-ethereum/core"
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

type frozenHistoricalNFTPenalty struct {
	master   MasterPenaltyProof
	eligible bool
}

// AuthorizeFrozenHistoricalNFTPenalty scopes the exact typed Gateway call
// produced for one NFT still in withdrawal custody.
func (e *Executor) AuthorizeFrozenHistoricalNFTPenalty(ctx sdk.Context, msg core.Message) bool {
	return e.authorizeFrozenPenalty(ctx, msg, frozenPenaltyHistoricalNFT)
}

// ApplyFrozenNFTPenalty realizes or checkpoints one announced penalty in
// the exact frozen NFT reserve. The caller reconciles the native queue and
// receipt in the same cache before the native burn can be committed.
func (e *Executor) ApplyFrozenNFTPenalty(ctx sdk.Context, row validatortypes.EVMFrozenBinding, infractionHeight int64,
	percentage uint16, nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, FrozenNFTPenaltyProof) error,
) error {
	return e.applyFrozenNFTPenalty(ctx, row, infractionHeight, percentage, nativeSupply, project, nil)
}

// ApplyFrozenHistoricalNFTPenalty realizes a typed Master penalty using the
// native planner's eligibility result. A mature claim remains slashable while
// its exact receipt is still frozen in Gateway custody.
func (e *Executor) ApplyFrozenHistoricalNFTPenalty(ctx sdk.Context, row validatortypes.EVMFrozenBinding, master MasterPenaltyProof,
	eligible bool, nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, FrozenNFTPenaltyProof) error,
) error {
	if !e.hasHistoricalPenaltyScope(ctx, master, common.BytesToAddress(row.SourceValidator)) {
		return errors.New("frozen historical NFT penalty requires the active typed Master reconciliation")
	}
	return e.applyFrozenNFTPenalty(ctx, row, master.InfractionHeight, master.Percentage, nativeSupply, project,
		&frozenHistoricalNFTPenalty{master: master, eligible: eligible})
}

func (e *Executor) applyFrozenNFTPenalty(ctx sdk.Context, row validatortypes.EVMFrozenBinding, infractionHeight int64,
	percentage uint16, nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, FrozenNFTPenaltyProof) error,
	historical *frozenHistoricalNFTPenalty,
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
		(historical == nil && common.BytesToHash(row.CompletionTimestamp).Big().Cmp(big.NewInt(now)) <= 0) {
		if historical != nil {
			return errors.New("historical penalty requires a custodied ordinary isolated NFT claim backed by native DEL")
		}
		return errors.New("penalty requires an unmatured ordinary isolated NFT claim backed by native DEL")
	}
	child, commit := ctx.CacheContext()
	validator := common.BytesToAddress(row.SourceValidator)
	var source *evmview.StakeSource
	if historical == nil {
		source, err = e.authenticatePenaltySource(child, validator, percentage)
		if err != nil {
			return err
		}
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
	proof := FrozenNFTPenaltyProof{Index: index, BeforeCursor: checkpoint.cursor, AfterCursor: common.BigToHash(next),
		InfractionHeight: infractionHeight, Eligible: row.CreatedHeight >= infractionHeight}
	announcement := MasterPenaltyProof{Validator: validator, BeforeCursor: proof.BeforeCursor, AfterCursor: proof.AfterCursor, Percentage: percentage}
	if historical != nil {
		announcement.InfractionHeight = infractionHeight
		if announcement != historical.master || (historical.eligible && !proof.Eligible) {
			return errors.New("frozen NFT claim differs from the typed Master entry or eligible exposure")
		}
		proof.Eligible = historical.eligible
	}
	status, err := e.verifyMasterPenaltyAppend(child, announcement)
	if err != nil {
		return err
	}
	position, err := e.readFrozenNFTPenaltyPosition(child, row)
	if err != nil {
		return err
	}
	before, err := e.readFrozenNFTPenaltyState(child, row, beneficiary, position, nativeSupply)
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
	var data []byte
	route := frozenPenaltyRegular
	if historical == nil {
		data, err = e.gateway.Pack("applyFrozenDELPenalty", index.Big(), big.NewInt(infractionHeight), row.ReportedReserveAmount.BigInt(), checkpoint.cursor.Big())
	} else {
		route = frozenPenaltyHistoricalNFT
		data, err = e.gateway.Pack("applyHistoricalPenaltyToNFT", true, index, big.NewInt(infractionHeight), proof.Eligible)
	}
	if err != nil {
		return err
	}
	authorized := child.WithValue(frozenPenaltyKey{}, frozenPenaltyCall{
		executor: e, height: child.BlockHeight(), data: bytes.Clone(data), route: route,
	})
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
	postPosition, err := e.readFrozenNFTPenaltyPostPosition(child, before.position, proof.RemainingReserve)
	if err != nil {
		return err
	}
	after, err := e.readFrozenNFTPenaltyState(child, row, beneficiary, postPosition, nativeSupply)
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
	if err := e.confirmPenaltySource(child, source); err != nil {
		return err
	}
	afterStatus, err := e.verifyMasterPenaltyAppend(child, announcement)
	if err != nil {
		return err
	}
	if afterStatus != status {
		return errors.New("Master status changed during frozen NFT penalty")
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

func (e *Executor) readFrozenNFTPenaltyPosition(ctx sdk.Context, row validatortypes.EVMFrozenBinding) (evmview.PositionSnapshot, error) {
	p := row.NFTPosition
	positionID := common.BytesToHash(p.PositionID)
	position, err := e.auth.Position(ctx, positionID)
	if err != nil {
		return evmview.PositionSnapshot{}, err
	}
	if position.Status != evmview.PositionActive || position.Owner != e.roles.Gateway ||
		position.Asset.Contract != e.roles.PositionVault || position.Asset.TokenID != positionID ||
		position.ParentPositionID != (common.Hash{}) || common.BytesToHash(p.ParentPositionID) != position.ParentPositionID ||
		p.Origin.Standard != 1155 || position.Origin.Contract != common.BytesToAddress(p.Origin.Contract) ||
		position.Origin.TokenID != common.BytesToHash(p.Origin.TokenID) || position.OriginQuantity != uint32(len(p.Origin.NativeSubTokenIDs)) ||
		position.ReserveKind != nfttypes.IsolatedReserveDEL || position.ReserveToken != (common.Address{}) ||
		position.ReserveAmount != common.BigToHash(row.ReportedReserveAmount.BigInt()) {
		return evmview.PositionSnapshot{}, errors.New("frozen NFT penalty differs from exact live Vault custody and DEL reserve")
	}
	return position, nil
}

// The before-read authenticated the full origin profile. The same pinned
// Vault implements this exact twelve-word read after the reserve-only call.
// Rechecking all returned words and collateral avoids repeating the factory
// and origin proxy discovery just to read the changed balance.
func (e *Executor) readFrozenNFTPenaltyPostPosition(ctx sdk.Context, before evmview.PositionSnapshot, remaining sdkmath.Int) (evmview.PositionSnapshot, error) {
	var out evmview.PositionSnapshot
	vault := e.roles.PositionVault
	position, err := e.read(ctx, vault, e.vault, "getPosition", 12*common.HashLength, before.Asset.TokenID.Big())
	if err != nil {
		return out, err
	}
	amount := common.BigToHash(remaining.BigInt())
	quantity := common.BigToHash(new(big.Int).SetUint64(uint64(before.OriginQuantity)))
	if !matchesNFTWords(position, common.BytesToHash(before.Origin.Contract[:]), before.Origin.TokenID, smallWord(1), quantity,
		common.Hash{}, amount, smallWord(byte(nfttypes.IsolatedReserveDEL)), before.ParentPositionID,
		common.BytesToHash(before.Owner[:]), smallWord(byte(before.Status)), boolWord(before.Refundable), boolWord(before.Burnable)) {
		return out, errors.New("frozen NFT penalty changed the exact Vault receipt or reserve")
	}
	next, err := e.read(ctx, vault, e.vault, "nextPositionId", common.HashLength)
	if err != nil {
		return out, err
	}
	if common.BytesToHash(next) != before.NextPositionID {
		return out, errors.New("frozen NFT penalty changed the Vault position cursor")
	}
	declared, err := e.read(ctx, vault, e.vault, "declaredReserve", common.HashLength, uint8(nfttypes.IsolatedReserveDEL), common.Address{})
	if err != nil {
		return out, err
	}
	held := e.evm.GetBalance(ctx, vault)
	declaredWord := common.BytesToHash(declared)
	if held == nil || declaredWord.Big().Cmp(remaining.BigInt()) < 0 || held.ToBig().Cmp(declaredWord.Big()) < 0 {
		return out, errors.New("frozen NFT penalty left insufficient Vault collateral")
	}
	out = before
	out.ReserveAmount, out.DeclaredCurrencyReserve, out.HeldCurrencyReserve = amount, declaredWord, common.BigToHash(held.ToBig())
	return out, nil
}

func (e *Executor) readFrozenNFTPenaltyState(ctx sdk.Context, row validatortypes.EVMFrozenBinding, beneficiary common.Address,
	position evmview.PositionSnapshot, nativeSupply func(sdk.Context) sdkmath.Int,
) (frozenNFTPenaltyState, error) {
	var out frozenNFTPenaltyState
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
