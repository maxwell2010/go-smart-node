package app

import (
	"bytes"
	"errors"
	"math/big"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

const backedTokenMembershipPrefix byte = 0x80

func backedTokenMembershipKey(token common.Address) []byte {
	return append([]byte{backedTokenMembershipPrefix}, token[:]...)
}

type backedTokenMeta struct {
	InitialMint    *big.Int
	MinTotalSupply *big.Int
	MaxTotalSupply *big.Int
	Creator        common.Address
	Crr            uint8
	Identity       string
	Symbol         string
	Name           string
}

// This hook admits only creation through the authenticated pinned factory. All
// metadata, balance and membership writes remain in the outer EVM transaction.
func (c *withdrawalController) admitBackedToken(ctx sdk.Context, sender common.Address, msg core.Message, receipt *ethtypes.Receipt) (bool, error) {
	r := c.customCollateral
	if r == nil || msg.To == nil || len(msg.Data) < 4 {
		return false, nil
	}
	if *msg.To != r.profile.TokenCenter {
		if !ctx.KVStore(r.store).Has(backedTokenMembershipKey(*msg.To)) {
			return false, nil
		}
		if sender == (common.Address{}) || sender != msg.From || msg.Value == nil || msg.Value.Sign() != 0 || len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) > 2048 || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful {
			return true, errors.New("backed ERC20 operation requires a bounded signed zero-value call")
		}
		tokenABI, err := contracts.CompiledABI("DRC20")
		if err != nil {
			return true, err
		}
		method, err := tokenABI.MethodById(msg.Data[:4])
		if err != nil {
			return true, err
		}
		switch method.Sig {
		case "transfer(address,uint256)", "approve(address,uint256)", "transferFrom(address,address,uint256)":
		default:
			return true, errors.New("backed operation is outside approved ERC20 transfers")
		}
		if r.evm.GetState(ctx, r.profile.TokenCenter, reservelessPauseSlot) != (common.Hash{}) {
			return true, errors.New("backed TokenCenter is paused")
		}
		live, err := r.Coin(ctx, *msg.To)
		if err != nil {
			return true, err
		}
		stored, err := r.keeper.GetCoinByDRC(ctx, msg.To.Hex())
		if err != nil {
			return true, err
		}
		if !live.Volume.Equal(stored.Volume) || !live.Reserve.Equal(stored.Reserve) {
			return true, errors.New("backed transfer unexpectedly changed supply or reserve")
		}
		return true, c.admitReservelessOperation(ctx, msg, receipt)
	}
	center, err := contracts.CompiledABI("NewNetworkTokenCenter")
	if err != nil {
		return true, err
	}
	method := center.Methods["createToken"]
	if !bytes.Equal(msg.Data[:4], method.ID) {
		return false, nil
	}
	if sender == (common.Address{}) || sender != msg.From || msg.Value == nil || msg.Value.Sign() <= 0 || len(msg.SetCodeAuthorizations) != 0 || len(msg.Data) > 2048 || receipt == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return true, errors.New("backed creation requires a bounded signed direct factory payment")
	}
	if err := r.authenticateRoles(ctx); err != nil {
		return true, err
	}
	if r.evm.GetState(ctx, r.profile.TokenCenter, reservelessPauseSlot) != (common.Hash{}) {
		return true, errors.New("backed TokenCenter is paused")
	}
	args, err := canonicalReservelessArgs(method, msg.Data)
	if err != nil {
		return true, err
	}
	meta := abi.ConvertType(args[0], new(backedTokenMeta)).(*backedTokenMeta)
	if meta.Creator != msg.From || !validPublicTokenName(meta.Name, coinconfig.MaxCoinTitleLength) || !identity.ValidPublicText(meta.Identity, 256) || !coinconfig.CoinDenomValidator.MatchString(meta.Symbol) || reservedPublicTokenSymbol(meta.Symbol) {
		return true, errors.New("invalid backed token creator or bounded display metadata")
	}
	commission := backedSymbolCommission(len(meta.Symbol))
	reserve := new(big.Int).Sub(msg.Value, commission)
	if reserve.Cmp(coinconfig.MinCoinReserve.BigInt()) < 0 {
		return true, errors.New("backed token payment does not cover commission and minimum reserve")
	}
	coin := cointypes.Coin{Denom: meta.Symbol, Title: meta.Name, Creator: sdk.AccAddress(msg.From.Bytes()).String(), CRR: uint32(meta.Crr), Identity: meta.Identity,
		Volume: sdkmath.NewIntFromBigInt(meta.InitialMint), MinVolume: sdkmath.NewIntFromBigInt(meta.MinTotalSupply), LimitVolume: sdkmath.NewIntFromBigInt(meta.MaxTotalSupply), Reserve: sdkmath.NewIntFromBigInt(reserve)}
	if err := coin.Validate(); err != nil {
		return true, err
	}
	if coin.MinVolume.LT(coinconfig.MinCoinSupply) || coin.Volume.LT(coinconfig.MinCoinSupply) {
		return true, errors.New("backed token initial supply is below minimum")
	}
	if _, err := r.keeper.GetCoin(ctx, coin.Denom); !errors.Is(err, coinkeeper.ErrNotFound) {
		return true, errors.New("backed token symbol already has native metadata")
	}
	tokenABI, err := contracts.CompiledABI("DRC20")
	if err != nil {
		return true, err
	}
	initialization, err := tokenABI.Pack("initialize", meta.Name, meta.Symbol, meta.Creator, meta.Crr, meta.InitialMint, meta.MinTotalSupply, meta.MaxTotalSupply, meta.Identity)
	if err != nil {
		return true, err
	}
	proxy, err := contracts.Load("NewNetworkTokenReadableProxy")
	if err != nil {
		return true, err
	}
	constructor, err := proxy.ABI.Constructor.Inputs.Pack(r.profile.TokenCenter, initialization, r.profile.TokenCenter, uint8(0))
	if err != nil {
		return true, err
	}
	salt := crypto.Keccak256Hash([]byte(meta.Symbol))
	token := crypto.CreateAddress2(r.profile.TokenCenter, [32]byte(salt), crypto.Keccak256(append(proxy.Creation, constructor...)))
	coin.DRC20Contract = token.Hex()
	key := backedTokenMembershipKey(token)
	if ctx.KVStore(r.store).Has(key) {
		return true, errors.New("backed token was already admitted")
	}
	live, err := r.authenticateCoin(ctx, token, coin)
	if err != nil {
		return true, err
	}
	if !live.Volume.Equal(coin.Volume) || !live.Reserve.Equal(coin.Reserve) ||
		!r.storageStringEquals(ctx, token, customSlotOffset(customERC20Slot, 3), coin.Title) ||
		!r.storageStringEquals(ctx, token, customSlotOffset(customERC20Slot, 4), coin.Denom) ||
		!r.storageStringEquals(ctx, token, customSlotOffset(customTokenSlot, 3), coin.Identity) ||
		r.evm.GetState(ctx, token, crypto.Keccak256Hash(customAddressWord(msg.From).Bytes(), customERC20Slot[:])).Big().Cmp(meta.InitialMint) != 0 {
		return true, errors.New("backed token supply, reserve, owner balance or display metadata differs")
	}
	deployed := center.Events["TokenDeployed"]
	eventData, err := deployed.Inputs.NonIndexed().Pack(token, args[0])
	if err != nil {
		return true, err
	}
	// DRC20._mint emits the curve price after Transfer. Match Solidity's
	// sequential UD60x18 divisions, including their integer rounding.
	price := new(big.Int).Mul(reserve, big.NewInt(1_000_000_000_000_000_000))
	price.Quo(price, meta.InitialMint).Mul(price, big.NewInt(100)).Quo(price, new(big.Int).SetUint64(uint64(meta.Crr)))
	reserveEvent := tokenABI.Events["ReserveUpdated"]
	reserveData, err := reserveEvent.Inputs.NonIndexed().Pack(reserve, meta.InitialMint, price)
	if err != nil {
		return true, err
	}
	expected := []*ethtypes.Log{
		{Address: r.profile.TokenCenter, Topics: []common.Hash{deployed.ID}, Data: eventData},
		{Address: token, Topics: []common.Hash{crypto.Keccak256Hash([]byte("BeaconUpgraded(address)")), customAddressWord(r.profile.TokenCenter)}},
		reservelessLog(token, "Transfer", common.Address{}, msg.From, meta.InitialMint),
		{Address: token, Topics: []common.Hash{reserveEvent.ID}, Data: reserveData},
		{Address: token, Topics: []common.Hash{crypto.Keccak256Hash([]byte("Initialized(uint64)"))}, Data: common.BigToHash(big.NewInt(1)).Bytes()},
	}
	if !equalReservelessLogs(receipt.Logs, expected) {
		return true, errors.New("backed creation events differ from exact factory initialization")
	}
	entry, err := customCollateralEntry(coin)
	if err != nil {
		return true, err
	}
	if err := r.reservePublicTokenName(ctx, meta.Name); err != nil {
		return true, err
	}
	if err := r.keeper.SetCoin(ctx, coin); err != nil {
		return true, err
	}
	ctx.KVStore(r.store).Set(key, entry[:])
	return true, nil
}

// Matches the pinned legacy factory. The pending public tariff proposal is not
// applied here; changing prices requires a separately reviewed contract release.
func backedSymbolCommission(length int) *big.Int {
	amount := big.NewInt(2500_000_000_000_000_000)
	switch length {
	case 3:
		return new(big.Int).Mul(amount, big.NewInt(1000))
	case 4:
		return new(big.Int).Mul(amount, big.NewInt(100))
	case 5:
		return new(big.Int).Mul(amount, big.NewInt(10))
	case 6:
		return amount
	default:
		return big.NewInt(250_000_000_000_000_000)
	}
}
