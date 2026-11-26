package app

import (
	"errors"
	"math/big"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethmath "github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
)

// OpenZeppelin ERC20Storage._allowances is a nested mapping in the word after _balances.
func convertAllowance(ctx sdk.Context, r *customCollateralRegistry, token, owner, spender common.Address) *big.Int {
	slot := customSlotOffset(customERC20Slot, 1)
	ownersAllowances := crypto.Keccak256Hash(customAddressWord(owner).Bytes(), slot.Bytes())
	return r.evm.GetState(ctx, token, crypto.Keccak256Hash(customAddressWord(spender).Bytes(), ownersAllowances.Bytes())).Big()
}

func drc20ReserveUpdated(contract abi.ABI, token common.Address, coin cointypes.Coin, eventReserve *big.Int) (*ethtypes.Log, error) {
	if !coin.Volume.IsPositive() || coin.CRR == 0 {
		return nil, errors.New("invalid supply or CRR for ReserveUpdated")
	}
	volume := coin.Volume.BigInt()
	price := new(big.Int).Mul(eventReserve, big.NewInt(1_000_000_000_000_000_000))
	price.Quo(price, volume).Mul(price, big.NewInt(100)).Quo(price, new(big.Int).SetUint64(uint64(coin.CRR)))
	event := contract.Events["ReserveUpdated"]
	data, err := event.Inputs.NonIndexed().Pack(eventReserve, volume, price)
	if err != nil {
		return nil, err
	}
	return &ethtypes.Log{Address: token, Topics: []common.Hash{event.ID}, Data: data}, nil
}

func (c *withdrawalController) admitAssetConvert(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) error {
	r := c.customCollateral
	s, ok := assetConvertSnapshotFrom(ctx)
	center := r.profile.TokenCenter
	if !ok || s == nil || sender == (common.Address{}) || sender != msg.From || sender != s.Sender || msg.To == nil || *msg.To != center ||
		msg.Value == nil || msg.Value.Sign() != 0 || len(msg.Data) != 4+5*32 || crypto.Keccak256Hash(msg.Data) != s.CalldataHash || len(msg.SetCodeAuthorizations) != 0 ||
		receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful || len(receipt.Logs) != 5 {
		return errors.New("MC_CONVERT_CALL")
	}
	if s.TokenIn == s.TokenOut || s.Input.Sign() <= 0 || s.Middle.Sign() <= 0 || s.Output.Sign() <= 0 || s.Output.Cmp(s.Minimum) < 0 {
		return errors.New("MC_CONVERT_QUOTE")
	}
	in, err := r.Coin(ctx, s.TokenIn)
	if err != nil {
		return err
	}
	out, err := r.Coin(ctx, s.TokenOut)
	if err != nil {
		return err
	}
	if in.Denom == out.Denom || !in.Volume.IsPositive() || in.Volume.LT(in.MinVolume) ||
		out.Volume.GT(out.LimitVolume) || in.Reserve.LT(coinconfig.MinCoinReserve) ||
		new(big.Int).Sub(s.In.Volume.BigInt(), in.Volume.BigInt()).Cmp(s.Input) != 0 ||
		new(big.Int).Sub(s.In.Reserve.BigInt(), in.Reserve.BigInt()).Cmp(s.Middle) != 0 ||
		new(big.Int).Sub(out.Volume.BigInt(), s.Out.Volume.BigInt()).Cmp(s.Output) != 0 ||
		new(big.Int).Sub(out.Reserve.BigInt(), s.Out.Reserve.BigInt()).Cmp(s.Middle) != 0 {
		return errors.New("MC_CONVERT_CURVE_DELTA")
	}
	// The pinned ERC20 does not decrement the max-uint approval. Finite
	// approvals are consumed exactly once without emitting another Approval.
	expectedAllowance := s.Allowance
	if expectedAllowance.Cmp(ethmath.MaxBig256) != 0 {
		expectedAllowance = new(big.Int).Sub(s.Allowance, s.Input)
	}
	if new(big.Int).Sub(s.HolderBalance, reservelessBalance(r, ctx, s.TokenIn, sender)).Cmp(s.Input) != 0 ||
		new(big.Int).Sub(reservelessBalance(r, ctx, s.TokenOut, s.Recipient), s.RecipientBalance).Cmp(s.Output) != 0 ||
		convertAllowance(ctx, r, s.TokenIn, sender, center).Cmp(expectedAllowance) != 0 {
		return errors.New("MC_CONVERT_WALLET_DELTA")
	}
	native, err := r.spendableReserve(ctx, center)
	if err != nil || native.BigInt().Cmp(s.CenterNative) != 0 ||
		reservelessBalance(r, ctx, s.TokenIn, center).Cmp(s.CenterInBalance) != 0 ||
		reservelessBalance(r, ctx, s.TokenOut, center).Cmp(s.CenterOutBalance) != 0 {
		return errors.New("MC_CONVERT_CENTER_DELTA")
	}

	contract, err := tradeABI()
	if err != nil {
		return err
	}
	// Burning A emits its reserve event before the payout to TokenCenter.
	// Minting B emits after the same native amount arrives at B.
	sellEvent, err := drc20ReserveUpdated(contract, s.TokenIn, in, s.In.Reserve.BigInt())
	if err != nil {
		return err
	}
	buyEvent, err := drc20ReserveUpdated(contract, s.TokenOut, out, out.Reserve.BigInt())
	if err != nil {
		return err
	}
	expected := []*ethtypes.Log{
		reservelessLog(s.TokenIn, "Transfer", sender, center, s.Input),
		reservelessLog(s.TokenIn, "Transfer", center, common.Address{}, s.Input),
		sellEvent,
		reservelessLog(s.TokenOut, "Transfer", common.Address{}, s.Recipient, s.Output),
		buyEvent,
	}
	if !equalReservelessLogs(receipt.Logs, expected) {
		return errors.New("MC_CONVERT_RECEIPT")
	}
	child, commit := ctx.CacheContext()
	if err := r.keeper.UpdateCoinVR(child, in.Denom, in.Volume, in.Reserve); err != nil {
		return err
	}
	if err := r.keeper.UpdateCoinVR(child, out.Denom, out.Volume, out.Reserve); err != nil {
		return err
	}
	child.EventManager().EmitEvent(sdk.NewEvent("mintcandy_drc20_convert",
		sdk.NewAttribute("payer", sender.Hex()), sdk.NewAttribute("recipient", s.Recipient.Hex()),
		sdk.NewAttribute("token_in", s.TokenIn.Hex()), sdk.NewAttribute("token_out", s.TokenOut.Hex()),
		sdk.NewAttribute("input_atoms", s.Input.String()), sdk.NewAttribute("native_atoms", s.Middle.String()),
		sdk.NewAttribute("output_atoms", s.Output.String()), sdk.NewAttribute("min_output_atoms", s.Minimum.String()),
		sdk.NewAttribute("in_volume", in.Volume.String()), sdk.NewAttribute("in_reserve", in.Reserve.String()),
		sdk.NewAttribute("out_volume", out.Volume.String()), sdk.NewAttribute("out_reserve", out.Reserve.String())))
	commit()
	return nil
}
