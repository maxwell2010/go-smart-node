package settlement

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/nativeburn"
)

// OrdinaryDELPenaltyProof binds the projected amount and cursor to a realized
// Gateway penalty. A zero remaining balance deactivates the native binding.
type OrdinaryDELPenaltyProof struct {
	Before, After validatortypes.EVMStakeBinding
	Burned        sdkmath.Int
}

type ordinaryDELPenaltyKey struct{}
type ordinaryDELPenaltyCall struct {
	executor *Executor
	height   int64
	id       common.Hash
}

// AuthorizeOrdinaryDELPenalty is for the internal EVM commit guard only. A
// signed call or a different executor cannot obtain this cache capability.
func (e *Executor) AuthorizeOrdinaryDELPenalty(ctx sdk.Context, msg core.Message) bool {
	call, ok := ctx.Value(ordinaryDELPenaltyKey{}).(ordinaryDELPenaltyCall)
	if !ok || e == nil || call.executor != e || call.height != ctx.BlockHeight() ||
		ctx.ExecMode() != sdk.ExecModeFinalize || msg.From != Sender() ||
		msg.To == nil || *msg.To != e.roles.Gateway || msg.Value == nil ||
		msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return false
	}
	data, err := e.gateway.Pack("applyPenaltiesToStakes", [][32]byte{call.id})
	return err == nil && bytes.Equal(msg.Data, data)
}

// ApplyOrdinaryDELPenalty realizes one already announced MasterValidator
// penalty against one ordinary position. This cannot append a penalty, update
// validator eligibility, or settle held, NFT-backed or frozen balances. The
// caller must reconcile all affected positions under its policy before making
// an announcement durable. Rejection of the projection rolls back the burn.
func (e *Executor) ApplyOrdinaryDELPenalty(ctx sdk.Context, row validatortypes.EVMStakeBinding, percentage uint16,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, OrdinaryDELPenaltyProof) error,
) error {
	if e == nil || e.evm == nil || nativeSupply == nil || project == nil ||
		ctx.ExecMode() != sdk.ExecModeFinalize || ctx.BlockHeight() <= 0 || percentage == 0 || percentage > 100 {
		return errors.New("ordinary DEL penalty requires finalized block, approved percentage, supply and native projection")
	}
	if err := row.Validate(); err != nil {
		return err
	}
	id, validator, owner, err := e.ordinaryPenaltyIdentity(ctx, row)
	if err != nil {
		return err
	}
	beforeCursor := common.BytesToHash(row.PenaltyCursor)
	next := new(big.Int).Add(beforeCursor.Big(), big.NewInt(1))
	if next.BitLen() > 256 {
		return errors.New("ordinary DEL penalty cursor overflow")
	}
	afterCursor := common.BigToHash(next)
	child, write := ctx.CacheContext()
	source, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	if err := e.requireInert(child, Sender()); err != nil {
		return err
	}
	if err := e.verifyAnnouncedPenalty(child, validator, beforeCursor, afterCursor, percentage); err != nil {
		return err
	}
	before, err := e.ordinaryPenaltyState(child, validator, owner, id, row.RawAmount, beforeCursor, nativeSupply)
	if err != nil {
		return err
	}
	burned := new(big.Int).Mul(row.RawAmount.BigInt(), new(big.Int).SetUint64(uint64(percentage)))
	burned.Quo(burned, big.NewInt(100))
	remaining := row.RawAmount.Sub(sdkmath.NewIntFromBigInt(burned))
	data, err := e.gateway.Pack("applyPenaltiesToStakes", [][32]byte{id})
	if err != nil {
		return err
	}
	authorized := child.WithValue(ordinaryDELPenaltyKey{}, ordinaryDELPenaltyCall{executor: e, height: child.BlockHeight(), id: id})
	result, _, err := e.invoke(authorized, e.roles.Gateway, data, true)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Gateway penalty returned unexpected data")
	}
	after, err := e.ordinaryPenaltyState(child, validator, owner, id, remaining, afterCursor, nativeSupply)
	if err != nil {
		return err
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
	if err := e.verifyOrdinaryPenaltyLogs(receipt, id, afterCursor, after.stake, burned); err != nil {
		return err
	}
	if err := verifyOrdinaryPenaltyDeltas(before, after, burned); err != nil {
		return err
	}
	afterSource, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	if afterSource != source {
		return errors.New("Gateway source changed during ordinary DEL penalty")
	}
	if err := e.verifyAnnouncedPenalty(child, validator, beforeCursor, afterCursor, percentage); err != nil {
		return err
	}
	post := proto.Clone(&row).(*validatortypes.EVMStakeBinding)
	post.RawAmount = remaining
	post.Active = remaining.IsPositive()
	post.PenaltyCursor = afterCursor.Bytes()
	post.UpdatedHeight = child.BlockHeight()
	proof := OrdinaryDELPenaltyProof{Before: *proto.Clone(&row).(*validatortypes.EVMStakeBinding), After: *post, Burned: sdkmath.NewIntFromBigInt(burned)}
	if err := project(child, proof); err != nil {
		return fmt.Errorf("project ordinary DEL penalty: %w", err)
	}
	write()
	return nil
}

func (e *Executor) ordinaryPenaltyIdentity(ctx sdk.Context, row validatortypes.EVMStakeBinding) (common.Hash, common.Address, common.Address, error) {
	id := common.BytesToHash(row.StakeID)
	validator, owner := common.BytesToAddress(row.Validator), common.BytesToAddress(row.Beneficiary)
	var zero [2 * common.HashLength]byte
	if !row.Active || !row.RawAmount.IsPositive() || row.RawAmount.BigInt().BitLen() > 255 ||
		row.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN ||
		row.NativeTarget.StakeType != validatortypes.StakeType_Coin || row.NativeTarget.ID != identity.BaseDenom ||
		row.ReserveAsset.ReserveKind != 1 || row.ReserveAsset.NativeDenom != identity.BaseDenom ||
		!row.RawNftBackedAmount.IsZero() || common.BytesToHash(row.HoldTimestamp) != (common.Hash{}) ||
		common.BytesToHash(row.RawHoldStartTime) != (common.Hash{}) ||
		common.BytesToAddress(row.Gateway) != e.roles.Gateway || common.BytesToAddress(row.TokenContract) != e.roles.WrappedDEL ||
		owner == Sender() || row.UpdatedHeight > ctx.BlockHeight() ||
		id != crypto.Keccak256Hash(validator[:], owner[:], e.roles.WrappedDEL[:], zero[:]) {
		return id, validator, owner, errors.New("penalty requires one active ordinary unshared DEL binding")
	}
	return id, validator, owner, nil
}

func (e *Executor) verifyAnnouncedPenalty(ctx sdk.Context, validator common.Address, before, after common.Hash, percentage uint16) error {
	state, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return err
	}
	status := common.BytesToHash(state[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) ||
		common.BytesToHash(state[common.HashLength:2*common.HashLength]) != smallWord(1) ||
		common.BytesToHash(state[2*common.HashLength:]) != after {
		return errors.New("ordinary DEL penalty requires a paused validator and exactly one pending MasterValidator entry")
	}
	page, err := e.read(ctx, e.roles.MasterValidator, e.master, "getPenaltiesRange", 3*common.HashLength, validator, before.Big(), big.NewInt(1))
	if err != nil {
		return err
	}
	if common.BytesToHash(page[:common.HashLength]) != smallWord(common.HashLength) ||
		common.BytesToHash(page[common.HashLength:2*common.HashLength]) != smallWord(1) ||
		common.BytesToHash(page[2*common.HashLength:]) != common.BigToHash(new(big.Int).SetUint64(uint64(percentage))) {
		return errors.New("announced MasterValidator penalty differs from the approved percentage")
	}
	return nil
}

type ordinaryDELPenaltyState struct {
	payoutState
	stake               []byte
	reserve, bankSupply *big.Int
	reserveCursor       common.Hash
}

func (e *Executor) ordinaryPenaltyState(ctx sdk.Context, validator, owner common.Address, id common.Hash,
	amount sdkmath.Int, cursor common.Hash, nativeSupply func(sdk.Context) sdkmath.Int,
) (ordinaryDELPenaltyState, error) {
	var out ordinaryDELPenaltyState
	want := [8]common.Hash{common.BytesToHash(validator[:]), common.BytesToHash(owner[:]),
		common.BytesToHash(e.roles.WrappedDEL[:]), common.BigToHash(amount.BigInt()), {}, smallWord(tokenDEL), {}, {}}
	stake, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStake", len(want)*common.HashLength, validator, owner, e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	for i, word := range want {
		if !bytes.Equal(word[:], stake[i*common.HashLength:(i+1)*common.HashLength]) {
			return out, errors.New("Gateway DEL penalty position differs from the expected getter state")
		}
	}
	gotCursor, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStakePenaltyIndex", common.HashLength, id)
	if err != nil {
		return out, err
	}
	_, nft, err := e.directDELPoststate(ctx, validator, owner, 0, amount.BigInt())
	if err != nil {
		return out, err
	}
	if common.BytesToHash(gotCursor) != cursor || nft.Sign() != 0 {
		return out, errors.New("Gateway DEL penalty cursor or isolated collateral differs")
	}
	reserve, err := e.read(ctx, e.roles.Gateway, e.gateway, "getValidatorReserve", 2*common.HashLength, validator, e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	reserveAmount := new(big.Int).SetBytes(reserve[common.HashLength:])
	balance, err := e.payoutState(ctx, payout{asset: e.roles.WrappedDEL, beneficiary: owner}, common.Address{})
	if err != nil {
		return out, err
	}
	supply := nativeSupply(ctx)
	if supply.IsNil() || supply.IsNegative() || reserveAmount.Cmp(amount.BigInt()) < 0 || balance.wrapped.Cmp(amount.BigInt()) < 0 {
		return out, errors.New("invalid reserve, WDEL custody or native DEL supply for penalty")
	}
	return ordinaryDELPenaltyState{payoutState: balance, stake: stake, reserve: reserveAmount,
		bankSupply: supply.BigInt(), reserveCursor: common.BytesToHash(reserve[:common.HashLength])}, nil
}

func verifyOrdinaryPenaltyDeltas(before, after ordinaryDELPenaltyState, burned *big.Int) error {
	if before.beneficiary.Cmp(after.beneficiary) != 0 || before.gateway.Cmp(after.gateway) != 0 ||
		before.reserveCursor != after.reserveCursor {
		return errors.New("ordinary DEL penalty changed an unrelated balance or reserve cursor")
	}
	for _, pair := range [][2]*big.Int{{before.reserve, after.reserve}, {before.supply, after.supply},
		{before.wrapped, after.wrapped}, {before.backing, after.backing}, {before.bankSupply, after.bankSupply}} {
		if new(big.Int).Sub(pair[0], pair[1]).Cmp(burned) != 0 {
			return errors.New("actual reserve, WDEL and native supply deltas differ from the realized DEL penalty")
		}
	}
	return nil
}

func (e *Executor) verifyOrdinaryPenaltyLogs(receipt *ethtypes.Receipt, id, cursor common.Hash, stake []byte, burned *big.Int) error {
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	expected := 3
	if burned.Sign() > 0 {
		expected += 2
	}
	if len(markers) != 0 || len(receipt.Logs) != expected {
		return errors.New("unexpected logs for one ordinary DEL penalty")
	}
	logs := receipt.Logs
	if burned.Sign() > 0 {
		amount := common.BigToHash(burned)
		transfer, native := logs[0], logs[1]
		if transfer.Address != e.roles.WrappedDEL || len(transfer.Topics) != 3 || transfer.Topics[0] != e.wrapper.Events["Transfer"].ID ||
			transfer.Topics[1] != common.BytesToHash(e.roles.Gateway[:]) || transfer.Topics[2] != (common.Hash{}) || !bytes.Equal(transfer.Data, amount[:]) ||
			native.Address != common.HexToAddress(nativeburn.AddressString) || len(native.Topics) != 2 ||
			native.Topics[0] != crypto.Keccak256Hash([]byte("NativeBurn(address,uint256)")) ||
			native.Topics[1] != common.BytesToHash(e.roles.WrappedDEL[:]) || !bytes.Equal(native.Data, amount[:]) {
			return errors.New("ordinary DEL penalty must burn matching WDEL and native backing")
		}
		logs = logs[2:]
	}
	if err := e.checkDELStakeLogs(logs, id, stake, new(big.Int).Neg(burned), false); err != nil {
		return err
	}
	penalty := logs[2]
	if penalty.Address != e.roles.Gateway || len(penalty.Topics) != 2 ||
		penalty.Topics[0] != e.gateway.Events["PenaltyAppliedToStake"].ID || penalty.Topics[1] != id || !bytes.Equal(penalty.Data, cursor[:]) {
		return errors.New("Gateway did not emit the exact realized penalty cursor")
	}
	return nil
}
