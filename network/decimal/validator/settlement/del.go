package settlement

import (
	"bytes"
	"errors"
	"math/big"
	"time"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

// DELTransition is the verified result of a direct, signed DEL call.
type DELTransition struct {
	Validator, Beneficiary      common.Address
	StakeID, FrozenIndex        common.Hash
	PenaltyCursor               common.Hash // Nonzero only for an explicitly admitted penalized withdrawal.
	Amount, Remaining           sdkmath.Int // Call delta and direct DEL remaining after the call.
	NFTBacked                   sdkmath.Int // Unchanged reserve included in the Gateway's gross coin position.
	HoldDeadline, HoldStartTime int64       // Unix seconds, or zero for ordinary DEL.
	Deadline                    time.Time   // Zero for a deposit.
}

// VerifySignedDEL proves the actual poststate and its exact logs in the signed
// transaction cache. The keeper checks the corresponding native prestate before
// adding to the active position or moving part or all of it to the queue.
func (e *Executor) VerifySignedDEL(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (DELTransition, error) {
	return e.verifySignedDEL(ctx, msg, receipt, nil)
}

// VerifySignedPenalizedDELWithdrawal proves a direct DEL withdrawal against
// an exact active native binding whose penalty has already been realized.
func (e *Executor) VerifySignedPenalizedDELWithdrawal(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, before validatortypes.EVMStakeBinding) (DELTransition, error) {
	if e == nil || e.evm == nil {
		return DELTransition{}, errors.New("penalized DEL withdrawal requires a Gateway executor")
	}
	if err := before.Validate(); err != nil {
		return DELTransition{}, err
	}
	if common.BytesToHash(before.PenaltyCursor) == (common.Hash{}) {
		return DELTransition{}, errors.New("penalized DEL withdrawal requires a nonzero realized native penalty cursor")
	}
	if _, _, _, err := e.activePenaltyIdentity(ctx, before, before.NativeTarget.StakeType == validatortypes.StakeType_CoinHold); err != nil {
		return DELTransition{}, err
	}
	return e.verifySignedDEL(ctx, msg, receipt, &before)
}

func (e *Executor) verifySignedDEL(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt, penalized *validatortypes.EVMStakeBinding) (DELTransition, error) {
	var out DELTransition
	if msg.To == nil || *msg.To != e.roles.Gateway || msg.Value == nil || len(msg.Data) < 4 || len(msg.SetCodeAuthorizations) != 0 {
		return out, errors.New("DEL stake requires a direct signed Gateway call")
	}
	method, err := e.gateway.MethodById(msg.Data[:4])
	if err != nil {
		return out, errors.New("DEL profile admits only direct DEL delegation and withdrawal requests")
	}
	deposit := false
	holdArg := -1
	switch method.Sig {
	case "delegateDEL(address)":
		deposit = true
	case "delegateHoldDEL(address,uint256)":
		deposit, holdArg = true, 1
	case "withdraw(address,address,uint256)":
	case "withdrawHold(address,address,uint256,uint256)":
		holdArg = 3
	default:
		return out, errors.New("DEL profile admits only direct DEL delegation and withdrawal requests")
	}
	if penalized != nil && (deposit || (holdArg >= 0) != (penalized.NativeTarget.StakeType == validatortypes.StakeType_CoinHold)) {
		return out, errors.New("realized penalty path requires a withdrawal matching the active DEL stake type")
	}
	args, err := method.Inputs.Unpack(msg.Data[4:])
	if err != nil {
		return out, err
	}
	canonical, err := method.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(canonical, msg.Data[4:]) {
		return out, errors.New("noncanonical DEL stake call")
	}
	out.Validator, out.Beneficiary = args[0].(common.Address), msg.From
	amount := msg.Value
	if !deposit {
		if msg.Value.Sign() != 0 || args[1].(common.Address) != e.roles.WrappedDEL {
			return out, errors.New("withdrawal request requires the approved WDEL asset and zero value")
		}
		amount = args[2].(*big.Int)
	}
	if amount.Sign() <= 0 || amount.BitLen() > 255 || out.Validator == (common.Address{}) || out.Beneficiary == Sender() {
		return out, errors.New("invalid DEL stake amount or source identity")
	}
	var holdWord, tokenID common.Hash
	if holdArg >= 0 {
		hold := args[holdArg].(*big.Int)
		now := ctx.BlockTime().Unix()
		if !hold.IsInt64() || hold.Sign() <= 0 || (deposit && hold.Int64() <= now) || (!deposit && hold.Int64() >= now) {
			return out, errors.New("DEL hold deadline is invalid for this transition")
		}
		out.HoldDeadline = hold.Int64()
		holdWord = common.BigToHash(hold)
	}
	out.Amount = sdkmath.NewIntFromBigInt(amount)
	out.StakeID = crypto.Keccak256Hash(out.Validator.Bytes(), out.Beneficiary.Bytes(), e.roles.WrappedDEL.Bytes(), tokenID[:], holdWord[:])
	var logs []*ethtypes.Log
	if penalized == nil {
		if err := e.verifyDELStakeReceipt(ctx, receipt, out.Validator, out.Beneficiary, 3); err != nil {
			return out, err
		}
		if err := e.verifyDELStakeCursor(ctx, out.StakeID); err != nil {
			return out, err
		}
		logs = receipt.Logs
	} else {
		if common.BytesToHash(penalized.StakeID) != out.StakeID {
			return out, errors.New("penalized DEL withdrawal differs from the bound position")
		}
		out.PenaltyCursor = common.BytesToHash(penalized.PenaltyCursor)
		logs, err = e.verifyPenalizedDELWithdrawalReceipt(ctx, receipt, out)
		if err != nil {
			return out, err
		}
	}
	stake := [8]common.Hash{
		common.BytesToHash(out.Validator.Bytes()), common.BytesToHash(out.Beneficiary.Bytes()), common.BytesToHash(e.roles.WrappedDEL.Bytes()),
		{}, {}, smallWord(tokenDEL), holdWord, {},
	}
	getter := "getStake"
	getterArgs := []interface{}{out.Validator, out.Beneficiary, e.roles.WrappedDEL}
	if holdArg >= 0 {
		getter = "getHoldStake"
		getterArgs = append(getterArgs, big.NewInt(out.HoldDeadline))
	}
	post, err := e.read(ctx, e.roles.Gateway, e.gateway, getter, len(stake)*common.HashLength, getterArgs...)
	if err != nil {
		return out, err
	}
	if holdArg >= 0 {
		start := new(big.Int).SetBytes(post[7*common.HashLength:])
		if !start.IsInt64() || start.Sign() <= 0 || start.Int64() >= out.HoldDeadline || start.Int64() > ctx.BlockTime().Unix() {
			return out, errors.New("Gateway returned invalid DEL hold start time")
		}
		if penalized != nil && common.BigToHash(start) != common.BytesToHash(penalized.RawHoldStartTime) {
			return out, errors.New("penalized DEL withdrawal differs from the native hold start time")
		}
		out.HoldStartTime = start.Int64()
		stake[7] = common.BigToHash(start)
	}
	for i, word := range stake {
		if i == 3 {
			continue // The amount is compared with the stored native prestate.
		}
		if !bytes.Equal(word[:], post[i*common.HashLength:(i+1)*common.HashLength]) {
			return out, errors.New("Gateway position differs from the direct DEL call")
		}
	}
	gross := new(big.Int).SetBytes(post[3*common.HashLength : 4*common.HashLength])
	remaining, backed, err := e.directDELPoststate(ctx, out.Validator, out.Beneficiary, out.HoldDeadline, gross)
	if err != nil {
		return out, err
	}
	if penalized != nil && (backed.Cmp(penalized.RawNftBackedAmount.BigInt()) != 0 ||
		new(big.Int).Add(gross, amount).Cmp(penalized.RawAmount.BigInt()) != 0) {
		return out, errors.New("penalized DEL withdrawal differs from the native active amount or its NFT backing")
	}
	if deposit && remaining.Cmp(amount) < 0 {
		return out, errors.New("Gateway position is smaller than the DEL deposit")
	}
	isNew := deposit && gross.Cmp(amount) == 0
	if isNew && holdArg >= 0 && out.HoldStartTime != ctx.BlockTime().Unix() {
		return out, errors.New("new DEL hold must start in the deposit block")
	}
	out.Remaining = sdkmath.NewIntFromBigInt(remaining)
	out.NFTBacked = sdkmath.NewIntFromBigInt(backed)
	state, err := e.payoutState(ctx, payout{asset: e.roles.WrappedDEL, beneficiary: out.Beneficiary}, common.Address{})
	if err != nil {
		return out, err
	}
	held := new(big.Int).Set(remaining)
	if !deposit {
		held.Add(held, amount)
	}
	if state.wrapped.Cmp(held) < 0 {
		return out, errors.New("Gateway has insufficient backed WDEL for the affected DEL position and claim")
	}
	delta := new(big.Int).Set(amount)
	if !deposit {
		delta.Neg(delta)
	}
	if err := e.checkDELStakeLogs(logs, out.StakeID, post, delta, isNew); err != nil {
		return out, err
	}
	last := logs[2]
	if deposit {
		if last.Address != e.roles.WrappedDEL || len(last.Topics) != 3 || last.Topics[0] != e.wrapper.Events["Transfer"].ID ||
			last.Topics[1] != (common.Hash{}) || last.Topics[2] != common.BytesToHash(e.roles.Gateway.Bytes()) ||
			len(last.Data) != common.HashLength || common.BytesToHash(last.Data) != common.BigToHash(amount) {
			return out, errors.New("DEL delegation did not mint the exact WDEL amount to Gateway")
		}
		return out, nil
	}
	return e.verifyDELRequest(ctx, out, last)
}

func (e *Executor) directDELPoststate(ctx sdk.Context, validator, beneficiary common.Address, deadline int64, gross *big.Int) (direct, backed *big.Int, err error) {
	raw, err := e.read(ctx, e.roles.Gateway, e.gateway, "getNFTBackedAmount", common.HashLength,
		validator, beneficiary, e.roles.WrappedDEL, big.NewInt(deadline))
	if err != nil {
		return nil, nil, err
	}
	backed = new(big.Int).SetBytes(raw)
	if backed.Cmp(gross) > 0 || (deadline != 0 && backed.Sign() != 0) {
		return nil, nil, errors.New("Gateway reports an unsupported NFT reserve for the DEL position")
	}
	return new(big.Int).Sub(gross, backed), backed, nil
}

func (e *Executor) verifyDELStakeReceipt(ctx sdk.Context, receipt *ethtypes.Receipt, validator, beneficiary common.Address, count int) error {
	if err := e.verifyDELReceiptEnvelope(ctx, receipt, beneficiary, count, count); err != nil {
		return err
	}
	return e.verifyUnpenalizedValidator(ctx, validator)
}

func (e *Executor) verifyDELReceiptEnvelope(ctx sdk.Context, receipt *ethtypes.Receipt, beneficiary common.Address, minLogs, maxLogs int) error {
	markers, err := e.completions(receipt)
	if err != nil {
		return err
	}
	if len(markers) != 0 || len(receipt.Logs) < minLogs || len(receipt.Logs) > maxLogs {
		return errors.New("unexpected DEL stake receipt logs")
	}
	if err := signedMetadata(ctx, receipt); err != nil {
		return err
	}
	if _, err := e.auth.AuthenticateStakeProfile(ctx); err != nil {
		return err
	}
	if err := e.requireInert(ctx, beneficiary); err != nil {
		return err
	}
	return nil
}

func (e *Executor) verifyDELStakeCursor(ctx sdk.Context, id common.Hash) error {
	cursor, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStakePenaltyIndex", common.HashLength, id)
	if err != nil {
		return err
	}
	if common.BytesToHash(cursor) != (common.Hash{}) {
		return errors.New("DEL position has an unsupported penalty cursor")
	}
	return nil
}

func (e *Executor) checkDELStakeLogs(logs []*ethtypes.Log, id common.Hash, post []byte, delta *big.Int, isNew bool) error {
	var flag common.Hash
	if isNew {
		flag = smallWord(1)
	}
	changed := e.gateway.Events["StakeAmountUpdated"]
	changeData, err := changed.Inputs.NonIndexed().Pack(id, delta)
	if err != nil {
		return err
	}
	updated := e.gateway.Events["StakeUpdated"]
	updateData := append(append(id.Bytes(), post...), flag[:]...)
	for i, expected := range []struct {
		topic common.Hash
		data  []byte
	}{{changed.ID, changeData}, {updated.ID, updateData}} {
		if logs[i].Address != e.roles.Gateway || len(logs[i].Topics) != 1 || logs[i].Topics[0] != expected.topic || !bytes.Equal(logs[i].Data, expected.data) {
			return errors.New("DEL stake receipt differs from the exact amount change and position")
		}
	}
	return nil
}

func (e *Executor) verifyDELRequest(ctx sdk.Context, out DELTransition, log *ethtypes.Log) (DELTransition, error) {
	if log.Address != e.roles.Gateway || len(log.Topics) != 1 || log.Topics[0] != e.gateway.Events["WithdrawRequest"].ID ||
		len(log.Data) != (2+frozenWordCount)*common.HashLength || common.BytesToHash(log.Data[:common.HashLength]) != out.StakeID {
		return out, errors.New("DEL withdrawal requires the exact Gateway request event")
	}
	out.FrozenIndex = common.BytesToHash(log.Data[common.HashLength : 2*common.HashLength])
	if out.FrozenIndex == common.MaxHash {
		return out, errors.New("invalid frozen DEL withdrawal index")
	}
	words, err := e.readFrozen(ctx, out.FrozenIndex)
	if err != nil {
		return out, err
	}
	for i, word := range words {
		start := (2 + i) * common.HashLength
		if !bytes.Equal(word[:], log.Data[start:start+common.HashLength]) {
			return out, errors.New("DEL request receipt differs from the frozen Gateway position")
		}
	}
	if words[0] != common.BytesToHash(out.Validator.Bytes()) || words[1] != common.BytesToHash(out.Beneficiary.Bytes()) ||
		words[2] != common.BytesToHash(e.roles.WrappedDEL.Bytes()) || words[3] != common.BigToHash(out.Amount.BigInt()) ||
		words[4] != (common.Hash{}) || words[5] != smallWord(tokenDEL) || words[6] != common.BigToHash(big.NewInt(out.HoldDeadline)) ||
		words[7] != common.BigToHash(big.NewInt(out.HoldStartTime)) ||
		words[8] != smallWord(statusFrozen) || words[9] != smallWord(freezeWithdraw) {
		return out, errors.New("frozen request differs from the direct DEL withdrawal")
	}
	deadline := words[10].Big()
	if !deadline.IsInt64() || deadline.Int64() <= ctx.BlockTime().Unix() {
		return out, errors.New("DEL withdrawal deadline must follow the request block")
	}
	out.Deadline = time.Unix(deadline.Int64(), 0).UTC()
	return out, nil
}

func (e *Executor) verifyUnpenalizedValidator(ctx sdk.Context, validator common.Address) error {
	state, err := e.read(ctx, e.roles.MasterValidator, e.master, "getValidatorState", 3*common.HashLength, validator)
	if err != nil {
		return err
	}
	status := common.BytesToHash(state[:common.HashLength])
	if (status != smallWord(1) && status != smallWord(2)) || !bytes.Equal(state[common.HashLength:], make([]byte, 2*common.HashLength)) {
		return errors.New("DEL profile requires an active unpaused validator without penalties")
	}
	return nil
}
