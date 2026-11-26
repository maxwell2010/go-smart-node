package app

import (
	"bytes"
	"math/big"
	"sync"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

var assetConvertSelector = crypto.Keccak256([]byte("convert(address,address,uint256,uint256,address)"))[:4]
var assetConvertABI = sync.OnceValues(func() (abi.ABI, error) { return contracts.CompiledABI("NewNetworkTokenCenter") })

type assetConvertKey struct{}

type assetConvertSnapshot struct {
	CalldataHash                                    common.Hash
	Sender, Recipient, TokenIn, TokenOut            common.Address
	Input, Minimum, Middle, Output                  *big.Int
	HolderBalance, RecipientBalance, Allowance      *big.Int
	CenterInBalance, CenterOutBalance, CenterNative *big.Int
	In, Out                                         cointypes.Coin
}

func assetConvertSnapshotFrom(ctx sdk.Context) (*assetConvertSnapshot, bool) {
	s, ok := ctx.Value(assetConvertKey{}).(*assetConvertSnapshot)
	return s, ok
}

func (c *withdrawalController) convertSnapshot(ctx sdk.Context, msg core.Message) (*assetConvertSnapshot, bool) {
	r := c.customCollateral
	if r == nil || msg.To == nil || *msg.To != r.profile.TokenCenter || len(msg.Data) != 164 ||
		!bytes.Equal(msg.Data[:4], assetConvertSelector) || msg.Value == nil || msg.Value.Sign() != 0 ||
		len(msg.SetCodeAuthorizations) != 0 {
		return nil, false
	}
	center := *msg.To
	factory, err := assetConvertABI()
	if err != nil {
		return nil, false
	}
	method, err := factory.MethodById(assetConvertSelector)
	if err != nil {
		return nil, false
	}
	args, err := canonicalReservelessArgs(*method, msg.Data)
	if err != nil {
		return nil, false
	}
	s := &assetConvertSnapshot{CalldataHash: crypto.Keccak256Hash(msg.Data), Sender: msg.From, TokenIn: args[0].(common.Address), TokenOut: args[1].(common.Address), Input: args[2].(*big.Int), Minimum: args[3].(*big.Int), Recipient: args[4].(common.Address)}
	if s.TokenIn == s.TokenOut || s.Input.Sign() <= 0 || c.delRecipientAllowed == nil ||
		r.evm.GetState(ctx, center, reservelessPauseSlot) != (common.Hash{}) {
		return nil, false
	}
	for _, address := range []common.Address{s.Sender, s.Recipient} {
		if address == (common.Address{}) || !c.delRecipientAllowed(sdk.AccAddress(address.Bytes())) ||
			r.evm.GetCodeHash(ctx, address) != ethtypes.EmptyCodeHash {
			return nil, false
		}
	}
	// Coin authenticates the registry, factory, proxy and curve parameters.
	// Demand equality with native records before trusting either quote.
	for _, pair := range []struct {
		address common.Address
		coin    *cointypes.Coin
	}{{s.TokenIn, &s.In}, {s.TokenOut, &s.Out}} {
		live, err := r.Coin(ctx, pair.address)
		if err != nil {
			return nil, false
		}
		stored, err := r.keeper.GetCoinByDRC(ctx, pair.address.Hex())
		if err != nil || !stored.Reserve.Equal(live.Reserve) || !stored.Volume.Equal(live.Volume) {
			return nil, false
		}
		*pair.coin = live
	}
	s.Allowance = convertAllowance(ctx, r, s.TokenIn, s.Sender, center)
	s.HolderBalance = reservelessBalance(r, ctx, s.TokenIn, s.Sender)
	if s.Allowance.Cmp(s.Input) < 0 || s.HolderBalance.Cmp(s.Input) < 0 {
		return nil, false
	}
	asset, err := tradeABI()
	if err != nil {
		return nil, false
	}
	quote := func(target common.Address, name string, amount *big.Int) (*big.Int, bool) {
		sel := crypto.Keccak256([]byte(name + "(uint256)"))[:4]
		method, err := asset.MethodById(sel)
		if err != nil {
			return nil, false
		}
		// The getter executes with a fixed maximum of 500k EVM gas, in a
		// discarded cache. Charge this bound plus the metered SDK reads.
		ctx.GasMeter().ConsumeGas(500000, "reserve conversion curve quote")
		out, err := c.userNFTRead(ctx, target, asset, method.Name, amount)
		if err != nil || len(out) != 1 {
			return nil, false
		}
		n, ok := out[0].(*big.Int)
		return n, ok && n.Sign() > 0
	}
	var ok bool
	s.Middle, ok = quote(s.TokenIn, "calculateSellOutput", s.Input)
	if !ok {
		return nil, false
	}
	s.Output, ok = quote(s.TokenOut, "calculateBuyOutput", s.Middle)
	if !ok || s.Output.Cmp(s.Minimum) < 0 {
		return nil, false
	}
	s.RecipientBalance = reservelessBalance(r, ctx, s.TokenOut, s.Recipient)
	s.CenterInBalance = reservelessBalance(r, ctx, s.TokenIn, center)
	s.CenterOutBalance = reservelessBalance(r, ctx, s.TokenOut, center)
	s.CenterNative = r.bank.GetBalance(ctx, sdk.AccAddress(center.Bytes()), identity.BaseDenom).Amount.BigInt()
	return s, true
}
