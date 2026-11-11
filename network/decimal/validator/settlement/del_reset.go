package settlement

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const delStakeBytes = 8 * common.HashLength

// VerifySignedDELHoldReset proves the move from one expired hold bucket to the
// beneficiary's ordinary position. The keeper reconciles both native prestates.
func (e *Executor) VerifySignedDELHoldReset(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) (held, ordinary DELTransition, err error) {
	method := e.gateway.Methods["resetHolds"]
	if msg.To == nil || *msg.To != e.roles.Gateway || msg.Value == nil || msg.Value.Sign() != 0 ||
		len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) != 4+6*common.HashLength || !bytes.Equal(msg.Data[:4], method.ID) {
		return held, ordinary, errors.New("DEL hold reset requires a direct call for one deadline and zero value")
	}
	args, err := method.Inputs.Unpack(msg.Data[4:])
	if err != nil {
		return held, ordinary, err
	}
	canonical, err := method.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(canonical, msg.Data[4:]) {
		return held, ordinary, errors.New("noncanonical DEL hold reset call")
	}
	validator, beneficiary := args[0].(common.Address), args[1].(common.Address)
	deadlines := args[3].([]*big.Int)
	if validator == (common.Address{}) || beneficiary != msg.From || beneficiary == Sender() ||
		args[2].(common.Address) != e.roles.WrappedDEL || len(deadlines) != 1 ||
		!deadlines[0].IsInt64() || deadlines[0].Sign() <= 0 || deadlines[0].Int64() > ctx.BlockTime().Unix() {
		return held, ordinary, errors.New("DEL hold reset requires the beneficiary, WDEL and one expired deadline")
	}
	if err := e.verifyDELStakeReceipt(ctx, receipt, validator, beneficiary, 5); err != nil {
		return held, ordinary, err
	}
	var tokenID, noHold common.Hash
	holdWord := common.BigToHash(deadlines[0])
	heldID := crypto.Keccak256Hash(validator[:], beneficiary[:], e.roles.WrappedDEL[:], tokenID[:], holdWord[:])
	ordinaryID := crypto.Keccak256Hash(validator[:], beneficiary[:], e.roles.WrappedDEL[:], tokenID[:], noHold[:])
	for _, id := range []common.Hash{heldID, ordinaryID} {
		if err := e.verifyDELStakeCursor(ctx, id); err != nil {
			return held, ordinary, err
		}
	}
	heldPost, err := e.read(ctx, e.roles.Gateway, e.gateway, "getHoldStake", delStakeBytes, validator, beneficiary, e.roles.WrappedDEL, deadlines[0])
	if err != nil {
		return held, ordinary, err
	}
	ordinaryPost, err := e.read(ctx, e.roles.Gateway, e.gateway, "getStake", delStakeBytes, validator, beneficiary, e.roles.WrappedDEL)
	if err != nil {
		return held, ordinary, err
	}
	heldAmount := common.BytesToHash(heldPost[3*common.HashLength : 4*common.HashLength])
	ordinaryAmount := new(big.Int).SetBytes(ordinaryPost[3*common.HashLength : 4*common.HashLength])
	startWord := common.BytesToHash(heldPost[7*common.HashLength:])
	start := startWord.Big()
	if heldAmount != (common.Hash{}) || !start.IsInt64() || start.Sign() <= 0 || start.Cmp(deadlines[0]) >= 0 {
		return held, ordinary, errors.New("Gateway did not clear the identified DEL hold bucket")
	}
	if _, _, err := e.directDELPoststate(ctx, validator, beneficiary, deadlines[0].Int64(), new(big.Int)); err != nil {
		return held, ordinary, err
	}
	ordinaryDirect, ordinaryBacked, err := e.directDELPoststate(ctx, validator, beneficiary, 0, ordinaryAmount)
	if err != nil {
		return held, ordinary, err
	}
	base := [8]common.Hash{common.BytesToHash(validator[:]), common.BytesToHash(beneficiary[:]), common.BytesToHash(e.roles.WrappedDEL[:]),
		common.BigToHash(ordinaryAmount), {}, smallWord(tokenDEL), {}, {}}
	for i, word := range base {
		if !bytes.Equal(word[:], ordinaryPost[i*common.HashLength:(i+1)*common.HashLength]) {
			return held, ordinary, errors.New("Gateway returned an unexpected ordinary DEL position after hold reset")
		}
	}
	base[3], base[6], base[7] = heldAmount, holdWord, startWord
	for i, word := range base {
		if !bytes.Equal(word[:], heldPost[i*common.HashLength:(i+1)*common.HashLength]) {
			return held, ordinary, errors.New("Gateway returned an unexpected held DEL position after hold reset")
		}
	}
	reset := receipt.Logs[2]
	if reset.Address != e.roles.Gateway || len(reset.Topics) != 1 || reset.Topics[0] != e.gateway.Events["StakeReset"].ID ||
		len(reset.Data) != common.HashLength+delStakeBytes {
		return held, ordinary, errors.New("DEL hold reset requires the exact Gateway StakeReset event")
	}
	amount := new(big.Int).SetBytes(reset.Data[4*common.HashLength : 5*common.HashLength])
	if amount.Sign() <= 0 || amount.BitLen() > 255 || ordinaryDirect.Cmp(amount) < 0 {
		return held, ordinary, errors.New("invalid amount moved from held to ordinary DEL")
	}
	expectedReset := append(heldID.Bytes(), heldPost...)
	amount.FillBytes(expectedReset[4*common.HashLength : 5*common.HashLength])
	if !bytes.Equal(reset.Data, expectedReset) {
		return held, ordinary, errors.New("DEL hold reset event differs from the selected bucket")
	}
	if err := e.checkDELStakeLogs(receipt.Logs[:2], heldID, heldPost, new(big.Int).Neg(amount), false); err != nil {
		return held, ordinary, err
	}
	if err := e.checkDELStakeLogs(receipt.Logs[3:], ordinaryID, ordinaryPost, amount, ordinaryAmount.Cmp(amount) == 0); err != nil {
		return held, ordinary, err
	}
	state, err := e.payoutState(ctx, payout{asset: e.roles.WrappedDEL, beneficiary: beneficiary}, common.Address{})
	if err != nil {
		return held, ordinary, err
	}
	if state.wrapped.Cmp(ordinaryDirect) < 0 {
		return held, ordinary, errors.New("Gateway has insufficient backed WDEL for the reset DEL position")
	}
	held = DELTransition{Validator: validator, Beneficiary: beneficiary, StakeID: heldID, Amount: sdkmath.NewIntFromBigInt(amount),
		Remaining: sdkmath.ZeroInt(), NFTBacked: sdkmath.ZeroInt(), HoldDeadline: deadlines[0].Int64(), HoldStartTime: start.Int64()}
	ordinary = DELTransition{Validator: validator, Beneficiary: beneficiary, StakeID: ordinaryID,
		Remaining: sdkmath.NewIntFromBigInt(ordinaryDirect), NFTBacked: sdkmath.NewIntFromBigInt(ordinaryBacked)}
	return held, ordinary, nil
}
