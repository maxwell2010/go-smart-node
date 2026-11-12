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
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/nativeburn"
)

// ActiveDELPenaltyProof binds the projected amount, hold identity and cursor
// to a realized Gateway penalty. Zero remaining DEL deactivates the binding.
type ActiveDELPenaltyProof struct {
	Before, After validatortypes.EVMStakeBinding
	Burned        sdkmath.Int
}

type OrdinaryDELPenaltyProof = ActiveDELPenaltyProof

type activeDELPenaltyKey struct{}
type activeDELPenaltyCall struct {
	executor *Executor
	height   int64
	id       common.Hash
	held     bool
}

// AuthorizeOrdinaryDELPenalty is for the internal EVM commit guard only. A
// signed call or a different executor cannot obtain this cache capability.
func (e *Executor) AuthorizeOrdinaryDELPenalty(ctx sdk.Context, msg core.Message) bool {
	return e.authorizeActiveDELPenalty(ctx, msg, false)
}

// AuthorizeHeldDELPenalty scopes the internal capability to the held route.
func (e *Executor) AuthorizeHeldDELPenalty(ctx sdk.Context, msg core.Message) bool {
	return e.authorizeActiveDELPenalty(ctx, msg, true)
}

func (e *Executor) authorizeActiveDELPenalty(ctx sdk.Context, msg core.Message, held bool) bool {
	call, ok := ctx.Value(activeDELPenaltyKey{}).(activeDELPenaltyCall)
	if !ok || e == nil || call.executor != e || call.height != ctx.BlockHeight() || call.held != held ||
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
// validator eligibility, or settle held, NFT or frozen balances. The
// caller must reconcile all affected positions under its policy before making
// an announcement durable. Rejection of the projection rolls back the burn.
func (e *Executor) ApplyOrdinaryDELPenalty(ctx sdk.Context, row validatortypes.EVMStakeBinding, percentage uint16,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, OrdinaryDELPenaltyProof) error,
) error {
	return e.applyActiveDELPenalty(ctx, row, percentage, nativeSupply, project, false)
}

// ApplyHeldDELPenalty realizes one announced penalty in the exact active
// ordinary DEL hold bucket. The native projector must reconcile the lots in
// that bucket and preserve all unrelated holds in the same cache.
func (e *Executor) ApplyHeldDELPenalty(ctx sdk.Context, row validatortypes.EVMStakeBinding, percentage uint16,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, ActiveDELPenaltyProof) error,
) error {
	return e.applyActiveDELPenalty(ctx, row, percentage, nativeSupply, project, true)
}

func (e *Executor) applyActiveDELPenalty(ctx sdk.Context, row validatortypes.EVMStakeBinding, percentage uint16,
	nativeSupply func(sdk.Context) sdkmath.Int, project func(sdk.Context, ActiveDELPenaltyProof) error, held bool,
) error {
	if e == nil || e.evm == nil || nativeSupply == nil || project == nil ||
		ctx.ExecMode() != sdk.ExecModeFinalize || ctx.BlockHeight() <= 0 || percentage == 0 || percentage > 100 {
		return errors.New("active DEL penalty requires finalized block, approved percentage, supply and native projection")
	}
	if err := row.Validate(); err != nil {
		return err
	}
	id, validator, _, err := e.activePenaltyIdentity(ctx, row, held)
	if err != nil {
		return err
	}
	beforeCursor := common.BytesToHash(row.PenaltyCursor)
	next := new(big.Int).Add(beforeCursor.Big(), big.NewInt(1))
	if next.BitLen() > 256 {
		return errors.New("active DEL penalty cursor overflow")
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
	status, err := e.verifyAnnouncedPenalty(child, validator, beforeCursor, afterCursor, percentage)
	if err != nil {
		return err
	}
	before, err := e.activePenaltyState(child, row, row.RawAmount, beforeCursor, nativeSupply)
	if err != nil {
		return err
	}
	direct := row.RawAmount.Sub(row.RawNftBackedAmount)
	burned := new(big.Int).Mul(direct.BigInt(), new(big.Int).SetUint64(uint64(percentage)))
	burned.Quo(burned, big.NewInt(100))
	remaining := row.RawAmount.Sub(sdkmath.NewIntFromBigInt(burned))
	data, err := e.gateway.Pack("applyPenaltiesToStakes", [][32]byte{id})
	if err != nil {
		return err
	}
	authorized := child.WithValue(activeDELPenaltyKey{}, activeDELPenaltyCall{executor: e, height: child.BlockHeight(), id: id, held: held})
	result, err := e.invokePenalty(authorized, Sender(), e.roles.Gateway, data)
	if err != nil {
		return err
	}
	if len(result.Ret) != 0 {
		return errors.New("Gateway penalty returned unexpected data")
	}
	after, err := e.activePenaltyState(child, row, remaining, afterCursor, nativeSupply)
	if err != nil {
		return err
	}
	receipt := &ethtypes.Receipt{Status: ethtypes.ReceiptStatusSuccessful, Logs: evmtypes.LogsToEthereum(result.Logs)}
	if err := e.verifyActiveDELPenaltyLogs(receipt, id, afterCursor, after.stake, burned); err != nil {
		return err
	}
	if err := verifyActiveDELPenaltyDeltas(before, after, burned); err != nil {
		return err
	}
	afterSource, err := e.auth.AuthenticateStakeProfile(child)
	if err != nil {
		return err
	}
	if afterSource != source {
		return errors.New("Gateway source changed during active DEL penalty")
	}
	afterStatus, err := e.verifyAnnouncedPenalty(child, validator, beforeCursor, afterCursor, percentage)
	if err != nil {
		return err
	}
	if afterStatus != status {
		return errors.New("active DEL penalty changed the Master validator status")
	}
	encoded, err := validatortypes.CanonicalBindingBytes(&row)
	if err != nil {
		return err
	}
	var pre, post validatortypes.EVMStakeBinding
	if err := pre.Unmarshal(encoded); err != nil {
		return err
	}
	if err := post.Unmarshal(encoded); err != nil {
		return err
	}
	post.RawAmount = remaining
	post.Active = remaining.IsPositive()
	post.PenaltyCursor = afterCursor.Bytes()
	post.UpdatedHeight = child.BlockHeight()
	proof := ActiveDELPenaltyProof{Before: pre, After: post, Burned: sdkmath.NewIntFromBigInt(burned)}
	if err := project(child, proof); err != nil {
		return fmt.Errorf("project active DEL penalty: %w", err)
	}
	write()
	return nil
}

func (e *Executor) activePenaltyIdentity(ctx sdk.Context, row validatortypes.EVMStakeBinding, held bool) (common.Hash, common.Address, common.Address, error) {
	id := common.BytesToHash(row.StakeID)
	validator, owner := common.BytesToAddress(row.Validator), common.BytesToAddress(row.Beneficiary)
	deadline, start := common.BytesToHash(row.HoldTimestamp), common.BytesToHash(row.RawHoldStartTime)
	stakeType := validatortypes.StakeType_Coin
	if held {
		stakeType = validatortypes.StakeType_CoinHold
		until, since := deadline.Big(), start.Big()
		if !until.IsInt64() || !since.IsInt64() || since.Sign() <= 0 || until.Cmp(since) <= 0 ||
			since.Int64() > ctx.BlockTime().Unix() || since.Int64() != row.NativeHoldStartTime {
			return id, validator, owner, errors.New("held DEL penalty requires the exact valid hold start and deadline")
		}
	} else if deadline != (common.Hash{}) || start != (common.Hash{}) {
		return id, validator, owner, errors.New("ordinary DEL penalty cannot consume a held position")
	}
	var tokenID common.Hash
	if !row.Active || !row.RawAmount.IsPositive() || row.RawAmount.BigInt().BitLen() > 255 ||
		row.Kind != validatortypes.EVMStakeBindingKind_EVM_STAKE_BINDING_COIN ||
		row.NativeTarget.StakeType != stakeType || row.NativeTarget.ID != identity.BaseDenom ||
		row.ReserveAsset.ReserveKind != 1 || row.ReserveAsset.NativeDenom != identity.BaseDenom ||
		(held && !row.RawNftBackedAmount.IsZero()) ||
		common.BytesToAddress(row.Gateway) != e.roles.Gateway || common.BytesToAddress(row.TokenContract) != e.roles.WrappedDEL ||
		owner == Sender() || row.UpdatedHeight > ctx.BlockHeight() ||
		id != crypto.Keccak256Hash(validator[:], owner[:], e.roles.WrappedDEL[:], tokenID[:], deadline[:]) {
		return id, validator, owner, errors.New("penalty requires one active DEL binding with the exact Gateway asset and hold identity")
	}
	return id, validator, owner, nil
}

func (e *Executor) verifyAnnouncedPenalty(ctx sdk.Context, validator common.Address, before, after common.Hash, percentage uint16) (common.Hash, error) {
	state, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return common.Hash{}, err
	}
	status := common.BytesToHash(state[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) ||
		common.BytesToHash(state[common.HashLength:2*common.HashLength]) != smallWord(1) ||
		common.BytesToHash(state[2*common.HashLength:]) != after {
		return common.Hash{}, errors.New("DEL penalty requires a paused validator and exactly one pending MasterValidator entry")
	}
	page, err := e.read(ctx, e.roles.MasterValidator, e.master, "getPenaltiesRange", 3*common.HashLength, validator, before.Big(), big.NewInt(1))
	if err != nil {
		return common.Hash{}, err
	}
	if common.BytesToHash(page[:common.HashLength]) != smallWord(common.HashLength) ||
		common.BytesToHash(page[common.HashLength:2*common.HashLength]) != smallWord(1) ||
		common.BytesToHash(page[2*common.HashLength:]) != common.BigToHash(new(big.Int).SetUint64(uint64(percentage))) {
		return common.Hash{}, errors.New("announced MasterValidator penalty differs from the approved percentage")
	}
	return status, nil
}

type activeDELPenaltyState struct {
	payoutState
	stake               []byte
	reserve, bankSupply *big.Int
	reserveCursor       common.Hash
}

func (e *Executor) activePenaltyState(ctx sdk.Context, row validatortypes.EVMStakeBinding,
	amount sdkmath.Int, cursor common.Hash, nativeSupply func(sdk.Context) sdkmath.Int,
) (activeDELPenaltyState, error) {
	var out activeDELPenaltyState
	validator, owner := common.BytesToAddress(row.Validator), common.BytesToAddress(row.Beneficiary)
	deadline, start := common.BytesToHash(row.HoldTimestamp), common.BytesToHash(row.RawHoldStartTime)
	id := common.BytesToHash(row.StakeID)
	want := [8]common.Hash{common.BytesToHash(validator[:]), common.BytesToHash(owner[:]),
		common.BytesToHash(e.roles.WrappedDEL[:]), common.BigToHash(amount.BigInt()), {}, smallWord(tokenDEL), deadline, start}
	getter := "getStake"
	args := []interface{}{validator, owner, e.roles.WrappedDEL}
	if deadline != (common.Hash{}) {
		getter = "getHoldStake"
		args = append(args, deadline.Big())
	}
	stake, err := e.read(ctx, e.roles.Gateway, e.gateway, getter, len(want)*common.HashLength, args...)
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
	_, nft, err := e.directDELPoststate(ctx, validator, owner, deadline.Big().Int64(), amount.BigInt())
	if err != nil {
		return out, err
	}
	if common.BytesToHash(gotCursor) != cursor || nft.Cmp(row.RawNftBackedAmount.BigInt()) != 0 {
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
	if supply.IsNil() || supply.IsNegative() || reserveAmount.Cmp(amount.BigInt()) < 0 ||
		balance.wrapped.Cmp(amount.Sub(row.RawNftBackedAmount).BigInt()) < 0 {
		return out, errors.New("invalid reserve, WDEL custody or native DEL supply for penalty")
	}
	return activeDELPenaltyState{payoutState: balance, stake: stake, reserve: reserveAmount,
		bankSupply: supply.BigInt(), reserveCursor: common.BytesToHash(reserve[:common.HashLength])}, nil
}

func verifyActiveDELPenaltyDeltas(before, after activeDELPenaltyState, burned *big.Int) error {
	if before.reserveCursor != after.reserveCursor || new(big.Int).Sub(before.reserve, after.reserve).Cmp(burned) != 0 {
		return errors.New("active DEL penalty reserve amount or cursor changed unexpectedly")
	}
	return verifyDELBurnDeltas(before.payoutState, after.payoutState, before.bankSupply, after.bankSupply, burned)
}

func verifyDELBurnDeltas(before, after payoutState, bankBefore, bankAfter, burned *big.Int) error {
	if before.beneficiary.Cmp(after.beneficiary) != 0 || before.gateway.Cmp(after.gateway) != 0 {
		return errors.New("DEL penalty changed the beneficiary or Gateway native balance")
	}
	for _, pair := range [][2]*big.Int{{before.supply, after.supply}, {before.wrapped, after.wrapped},
		{before.backing, after.backing}, {bankBefore, bankAfter}} {
		if new(big.Int).Sub(pair[0], pair[1]).Cmp(burned) != 0 {
			return errors.New("actual WDEL and native supply deltas differ from the realized DEL penalty")
		}
	}
	return nil
}

func (e *Executor) verifyActiveDELPenaltyLogs(receipt *ethtypes.Receipt, id, cursor common.Hash, stake []byte, burned *big.Int) error {
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	expected := 3
	if burned.Sign() > 0 {
		expected += 2
	}
	if len(markers) != 0 || len(receipt.Logs) != expected {
		return errors.New("unexpected logs for one active DEL penalty")
	}
	logs := receipt.Logs
	if burned.Sign() > 0 {
		if err := e.verifyDELBurnLogs(logs[:2], burned); err != nil {
			return err
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

func (e *Executor) verifyDELBurnLogs(logs []*ethtypes.Log, burned *big.Int) error {
	amount := common.BigToHash(burned)
	transfer, native := logs[0], logs[1]
	if transfer.Address != e.roles.WrappedDEL || len(transfer.Topics) != 3 || transfer.Topics[0] != e.wrapper.Events["Transfer"].ID ||
		transfer.Topics[1] != common.BytesToHash(e.roles.Gateway[:]) || transfer.Topics[2] != (common.Hash{}) || !bytes.Equal(transfer.Data, amount[:]) ||
		native.Address != common.HexToAddress(nativeburn.AddressString) || len(native.Topics) != 2 ||
		native.Topics[0] != crypto.Keccak256Hash([]byte("NativeBurn(address,uint256)")) ||
		native.Topics[1] != common.BytesToHash(e.roles.WrappedDEL[:]) || !bytes.Equal(native.Data, amount[:]) {
		return errors.New("DEL penalty must burn matching WDEL and native backing")
	}
	return nil
}
