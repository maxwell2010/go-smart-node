package app

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sync"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

const convertedDELMethod = "convertAndDelegate(address,address,uint256,uint256,uint256)"

var (
	convertedDELSelector  = crypto.Keccak256([]byte(convertedDELMethod))[:4]
	curveApprovalSelector = crypto.Keccak256([]byte("approve(address,uint256)"))[:4]
	curveApprovalTopic    = crypto.Keccak256Hash([]byte("Approval(address,address,uint256)"))
	curveTransferTopic    = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))
	curveReserveTopic     = crypto.Keccak256Hash([]byte("ReserveUpdated(uint256,uint256,uint256)"))
)

var signedDELConversionABI = sync.OnceValues(func() (abi.ABI, error) {
	compiled, err := contracts.CompiledABI("MintCandyCurveConverter")
	if err != nil {
		return abi.ABI{}, err
	}
	if compiled.Methods["convertAndDelegate"].Sig != convertedDELMethod ||
		compiled.Events["CollateralConverted"].Sig != "CollateralConverted(address,address,address,uint256,uint256,uint256,uint256,bool)" {
		return abi.ABI{}, errors.New("compiler DEL conversion ABI differs")
	}
	return compiled, nil
})

func (c *withdrawalController) isCollateralConversionCall(msg core.Message) bool {
	return c.customCollateral != nil && msg.To != nil && (*msg.To == c.profile.CurveConverter ||
		(len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], curveApprovalSelector)))
}

func (c *withdrawalController) projectCollateralConversion(ctx sdk.Context, msg core.Message, receipt *ethtypes.Receipt) error {
	if msg.From == (common.Address{}) || msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 {
		return errors.New("DRC20 collateral admission requires a direct zero-value signed call")
	}
	markers, err := c.executor.CompletionIDs(receipt)
	if err != nil {
		return err
	}
	if len(markers) != 0 {
		return errors.New("DRC20 collateral admission cannot complete a withdrawal")
	}
	if *msg.To != c.profile.CurveConverter {
		return c.verifyCurveApproval(ctx, msg, receipt.Logs)
	}
	compiled, err := signedDELConversionABI()
	if err != nil {
		return err
	}
	method := compiled.Methods["convertAndDelegate"]
	if len(msg.Data) != 4+5*common.HashLength || !bytes.Equal(msg.Data[:4], convertedDELSelector) || len(receipt.Logs) < 7 {
		return errors.New("only the signed DRC20-to-DEL delegation call is admitted on the converter")
	}
	args, err := method.Inputs.Unpack(msg.Data[4:])
	if err != nil {
		return err
	}
	encoded, err := method.Inputs.Pack(args...)
	if err != nil || !bytes.Equal(encoded, msg.Data[4:]) {
		return errors.New("noncanonical DRC20 delegation call")
	}
	validator, token := args[0].(common.Address), args[1].(common.Address)
	input, minimum, deadline := args[2].(*big.Int), args[3].(*big.Int), args[4].(*big.Int)
	if validator == (common.Address{}) || token == (common.Address{}) || token == c.profile.WrappedDEL ||
		input.Sign() <= 0 || minimum.Sign() <= 0 || !deadline.IsInt64() || deadline.Int64() < ctx.BlockTime().Unix() {
		return errors.New("invalid DRC20 delegation asset, amount, minOut or deadline")
	}
	logs := receipt.Logs
	converted := logs[len(logs)-1]
	event := compiled.Events["CollateralConverted"]
	if converted.Address != c.profile.CurveConverter || len(converted.Topics) != 4 || converted.Topics[0] != event.ID ||
		converted.Topics[1] != common.BytesToHash(msg.From[:]) || converted.Topics[2] != common.BytesToHash(token[:]) ||
		converted.Topics[3] != common.BytesToHash(c.profile.TokenCenter[:]) || len(converted.Data) != 5*common.HashLength {
		return errors.New("DRC20 delegation is missing its exact conversion event")
	}
	output := new(big.Int).SetBytes(converted.Data[common.HashLength : 2*common.HashLength])
	want, err := event.Inputs.NonIndexed().Pack(input, output, minimum, deadline, false)
	if err != nil || output.Sign() <= 0 || output.Cmp(minimum) < 0 || output.BitLen() > 255 || !bytes.Equal(converted.Data, want) {
		return errors.New("DRC20 conversion result differs from the signed limits")
	}
	if !curveTransfer(logs[0], token, msg.From, c.profile.CurveConverter, input) ||
		!curveTransfer(logs[1], token, c.profile.CurveConverter, common.Address{}, input) {
		return errors.New("DRC20 conversion did not debit and burn the exact signed input")
	}
	if err := c.applyConvertedCoin(ctx, token, input, output, logs[2]); err != nil {
		return err
	}
	// The pinned converter invokes delegateDELTo for the authenticated caller.
	// Verify the inner Gateway receipt with the same ordinary DEL projection.
	data, err := c.abi.Pack("delegateDEL", validator)
	if err != nil {
		return err
	}
	gateway := c.profile.Gateway
	inner := *receipt
	inner.Logs = logs[3 : len(logs)-1]
	return c.projectSignedDEL(ctx, core.Message{From: msg.From, To: &gateway, Value: output, Data: data}, &inner)
}

func (c *withdrawalController) verifyCurveApproval(ctx sdk.Context, msg core.Message, logs []*ethtypes.Log) error {
	spender := common.BytesToHash(c.profile.CurveConverter[:])
	if len(msg.Data) != 4+2*common.HashLength || !bytes.Equal(msg.Data[4:4+common.HashLength], spender[:]) || len(logs) != 1 {
		return errors.New("DRC20 collateral approval must name the configured converter")
	}
	log := logs[0]
	if log.Address != *msg.To || len(log.Topics) != 3 || log.Topics[0] != curveApprovalTopic ||
		log.Topics[1] != common.BytesToHash(msg.From[:]) || log.Topics[2] != spender || !bytes.Equal(log.Data, msg.Data[4+common.HashLength:]) {
		return errors.New("DRC20 collateral approval differs from its signed allowance")
	}
	before, err := c.customCollateral.keeper.GetCoinByDRC(ctx, msg.To.Hex())
	if err != nil {
		return err
	}
	after, err := c.customCollateral.Coin(ctx, *msg.To)
	if err != nil {
		return err
	}
	if !before.Volume.Equal(after.Volume) || !before.Reserve.Equal(after.Reserve) {
		return errors.New("DRC20 collateral approval changed the token supply or DEL backing")
	}
	return nil
}

func curveTransfer(log *ethtypes.Log, token, from, to common.Address, amount *big.Int) bool {
	return log.Address == token && len(log.Topics) == 3 && log.Topics[0] == curveTransferTopic &&
		log.Topics[1] == common.BytesToHash(from[:]) && log.Topics[2] == common.BytesToHash(to[:]) &&
		len(log.Data) == common.HashLength && common.BytesToHash(log.Data) == common.BigToHash(amount)
}

func (c *withdrawalController) applyConvertedCoin(ctx sdk.Context, token common.Address, input, output *big.Int, log *ethtypes.Log) error {
	if c.customCollateral == nil || input == nil || output == nil || input.Sign() <= 0 || output.Sign() <= 0 ||
		input.BitLen() > 256 || output.BitLen() > 256 {
		return errors.New("DRC20 conversion is outside the admitted collateral profile")
	}
	before, err := c.customCollateral.keeper.GetCoinByDRC(ctx, token.Hex())
	if err != nil {
		return err
	}
	amount, received := sdkmath.NewIntFromBigInt(input), sdkmath.NewIntFromBigInt(output)
	if before.Volume.LT(amount) || before.Reserve.LT(received) {
		return errors.New("DRC20 conversion exceeds its tracked supply or DEL reserve")
	}
	wantVolume, wantReserve := before.Volume.Sub(amount), before.Reserve.Sub(received)
	// The approved DRC20 emits ReserveUpdated inside _burn, before it sends
	// the DEL. The event contains the old reserve and the new supply.
	if log == nil || log.Removed || log.Address != token || len(log.Topics) != 1 || log.Topics[0] != curveReserveTopic ||
		len(log.Data) != 3*common.HashLength || common.BytesToHash(log.Data[:common.HashLength]) != common.BigToHash(before.Reserve.BigInt()) ||
		common.BytesToHash(log.Data[common.HashLength:2*common.HashLength]) != common.BigToHash(wantVolume.BigInt()) {
		return errors.New("DRC20 reserve event differs from its pre-transfer reserve and post-burn supply")
	}
	after, err := c.customCollateral.Coin(ctx, token)
	if err != nil {
		return fmt.Errorf("verify converted DRC20 state: %w", err)
	}
	if !after.Volume.Equal(wantVolume) || !after.Reserve.Equal(wantReserve) {
		return errors.New("DRC20 conversion differs from the exact final supply and DEL backing")
	}
	return c.customCollateral.keeper.UpdateCoinVR(ctx, before.Denom, after.Volume, after.Reserve)
}
