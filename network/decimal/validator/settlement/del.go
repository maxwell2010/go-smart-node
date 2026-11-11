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
)

// DELTransition is the verified result of a direct, signed, unheld DEL call.
type DELTransition struct {
	Validator, Beneficiary common.Address
	StakeID, FrozenIndex   common.Hash
	Amount, Remaining      sdkmath.Int // Call delta and active position after the call.
	Deadline               time.Time   // Zero for a deposit.
}

// VerifySignedDEL proves the actual poststate and its exact logs in the signed
// transaction cache. The keeper checks the corresponding native prestate before
// adding to the active position or moving part or all of it to the queue.
func (e *Executor) VerifySignedDEL(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (DELTransition, error) {
	var out DELTransition
	if msg.To == nil || *msg.To != e.roles.Gateway || msg.Value == nil || len(msg.Data) < 4 || len(msg.SetCodeAuthorizations) != 0 {
		return out, errors.New("DEL stake requires a direct signed Gateway call")
	}
	method, err := e.gateway.MethodById(msg.Data[:4])
	if err != nil || (method.Sig != "delegateDEL(address)" && method.Sig != "withdraw(address,address,uint256)") {
		return out, errors.New("DEL profile admits only direct DEL delegation and withdrawal requests")
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
	deposit := method.Sig == "delegateDEL(address)"
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
	out.Amount = sdkmath.NewIntFromBigInt(amount)
	out.StakeID = crypto.Keccak256Hash(out.Validator.Bytes(), out.Beneficiary.Bytes(), e.roles.WrappedDEL.Bytes(), make([]byte, 2*common.HashLength))
	markers, err := e.completions(receipt)
	if err != nil {
		return out, err
	}
	if len(markers) != 0 || len(receipt.Logs) != 3 {
		return out, errors.New("unexpected DEL stake receipt logs")
	}
	if err := signedMetadata(ctx, receipt); err != nil {
		return out, err
	}
	if _, err := e.auth.AuthenticateStakeProfile(ctx); err != nil {
		return out, err
	}
	if err := e.requireInert(ctx, out.Beneficiary); err != nil {
		return out, err
	}
	if err := e.verifyUnpenalizedValidator(ctx, out.Validator); err != nil {
		return out, err
	}
	cursor, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStakePenaltyIndex", common.HashLength, out.StakeID)
	if err != nil {
		return out, err
	}
	if common.BytesToHash(cursor) != (common.Hash{}) {
		return out, errors.New("DEL position has an unsupported penalty cursor")
	}
	stake := [8]common.Hash{
		common.BytesToHash(out.Validator.Bytes()), common.BytesToHash(out.Beneficiary.Bytes()), common.BytesToHash(e.roles.WrappedDEL.Bytes()),
		{}, {}, smallWord(tokenDEL), {}, {},
	}
	post, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStake", len(stake)*common.HashLength, out.Validator, out.Beneficiary, e.roles.WrappedDEL)
	if err != nil {
		return out, err
	}
	for i, word := range stake {
		if i == 3 {
			continue // The amount is compared with the stored native prestate.
		}
		if !bytes.Equal(word[:], post[i*common.HashLength:(i+1)*common.HashLength]) {
			return out, errors.New("Gateway position is not ordinary unheld DEL")
		}
	}
	remaining := new(big.Int).SetBytes(post[3*common.HashLength : 4*common.HashLength])
	if deposit && remaining.Cmp(amount) < 0 {
		return out, errors.New("Gateway position is smaller than the DEL deposit")
	}
	out.Remaining = sdkmath.NewIntFromBigInt(remaining)
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
	if err := e.checkDELStakeLogs(receipt.Logs, out.StakeID, post, delta, deposit && remaining.Cmp(amount) == 0); err != nil {
		return out, err
	}
	last := receipt.Logs[2]
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
		words[4] != (common.Hash{}) || words[5] != smallWord(tokenDEL) || words[6] != (common.Hash{}) || words[7] != (common.Hash{}) ||
		words[8] != smallWord(statusFrozen) || words[9] != smallWord(freezeWithdraw) {
		return out, errors.New("frozen request differs from an ordinary DEL withdrawal")
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
