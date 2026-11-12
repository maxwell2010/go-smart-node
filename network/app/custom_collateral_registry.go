package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"

	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	"github.com/cosmos/gogoproto/proto"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	coinkeeper "github.com/maxwell2010/go-smart-node/network/decimal/coin/keeper"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

const (
	customCollateralManifestKey     byte = 0x6e
	customCollateralManifestVersion byte = 1
	maxCustomCollateralCoins             = 4
	customCollateralEntrySize            = common.AddressLength + common.HashLength
)

// These are the ERC1967 and namespaced storage locations in the pinned
// DecimalContractCenter and DecimalTokenCenter sources. Read them only after
// checking the exact compiler runtime. Startup/state sync have no block
// proposer or initialized EVM denomination for a simulated EVM message.
var (
	customImplementationSlot = common.HexToHash("0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc")
	customBeaconSlot         = common.HexToHash("0xa3f0ad74e5423aebfd80d3ef4346578335a9a72aeaee59ff6cb3582b35133d50")
	customOwnableSlot        = common.HexToHash("0x9016d09d72d40fdae2fd8ceac6b6234c7706214fd39c1cd1e609a0528c199300")
	customRegistrySlot       = common.HexToHash("0x00e2f18f4aa7606d53e2e7b6172fb5f051be6ee8f80cf63eec8ca91bb44b3100")
	customTokenCenterSlot    = common.HexToHash("0xc505efeb1b151d8e7a9297d5f8aae79af810c94de3fc8fc2e27ad1b30c968200")
	customTokenSlot          = common.HexToHash("0xb76f77bacf50c2c0746ca0d07482543304b44684f1a10a9d69fece929ef6b900")
	customERC20Slot          = common.HexToHash("0x52c63247e1f47db19d5ce0460030c497f067ca4cebf71ba98eeadabe20bace00")
)

type customCollateralRegistry struct {
	profile WithdrawalProfile
	keeper  *coinkeeper.StateKeeper
	bank    bankkeeper.Keeper
	evm     *evmkeeper.Keeper
	store   *storetypes.KVStoreKey

	proxyCode, registryCode, centerCode, converterCode, coinProxyCode, coinCode []byte
}

func (p WithdrawalProfile) customCollateralEnabled() bool {
	return p.TokenCenter != (common.Address{})
}

func validateCustomCollateralProfile(profile WithdrawalProfile) error {
	if !profile.customCollateralEnabled() && profile.CurveConverter == (common.Address{}) {
		return nil
	}
	if !profile.customCollateralEnabled() || profile.CurveConverter == (common.Address{}) ||
		profile.TokenCenter == profile.CurveConverter || !profile.EnableNativeConsensus || !profile.EnableDELDelegation {
		return errors.New("DRC20 collateral requires distinct TokenCenter and CurveConverter roles and native DEL consensus")
	}
	for _, other := range []common.Address{profile.Registry, profile.Gateway, profile.Library, profile.NFTCenter,
		profile.PositionVault, profile.MasterValidator, profile.WrappedDEL} {
		if profile.TokenCenter == other || profile.CurveConverter == other {
			return errors.New("DRC20 collateral roles overlap existing withdrawal system addresses")
		}
	}
	return nil
}

func validateCustomCollateralGenesis(genesis cointypes.GenesisState) error {
	if len(genesis.Coins) == 0 || len(genesis.Coins) > maxCustomCollateralCoins || len(genesis.Checks) != 0 {
		return errors.New("DRC20 genesis requires one to four approved coins and no redeemed checks")
	}
	for _, coin := range genesis.Coins {
		if !cointypes.ValidContractAddress(coin.DRC20Contract) || coin.MinVolume.LT(coinconfig.MinCoinSupply) ||
			coin.Reserve.LT(coinconfig.MinCoinReserve) {
			return errors.New("DRC20 genesis requires a backed token with valid minimum supply and DEL reserve")
		}
	}
	return nil
}

func newCustomCollateralRegistry(app *App, profile WithdrawalProfile) (*customCollateralRegistry, error) {
	if err := validateCustomCollateralProfile(profile); err != nil {
		return nil, err
	}
	if !profile.customCollateralEnabled() {
		return nil, nil
	}
	r := &customCollateralRegistry{profile: profile, keeper: app.NativeCoinKeeper, bank: app.BankKeeper, evm: app.EVMKeeper,
		store: app.keys[validatortypes.StoreKey]}
	for _, code := range []struct {
		name string
		want *[]byte
	}{{"DecimalContractCenterProxy", &r.proxyCode}, {"DecimalContractCenter", &r.registryCode},
		{"NewNetworkTokenCenter", &r.centerCode}, {"DRC20", &r.coinCode}} {
		value, err := contracts.ExpectedRuntime(code.name, nil, nil)
		if err != nil {
			return nil, err
		}
		*code.want = value
	}
	var err error
	r.coinProxyCode, err = contracts.PauseAwareProxyRuntime(profile.TokenCenter, profile.TokenCenter, 0)
	if err != nil {
		return nil, err
	}
	r.converterCode, err = contracts.ExpectedRuntime("MintCandyCurveConverter", nil, map[string]common.Hash{
		"MintCandyCurveConverter.sol:MintCandyCurveConverter.REGISTRY": customAddressWord(profile.Registry),
	})
	if err != nil {
		return nil, err
	}
	return r, nil
}

func customAddressWord(value common.Address) common.Hash { return common.BytesToHash(value.Bytes()) }

func customSlotOffset(slot common.Hash, offset int64) common.Hash {
	return common.BigToHash(new(big.Int).Add(slot.Big(), big.NewInt(offset)))
}

func (r *customCollateralRegistry) exactCode(ctx sdk.Context, target common.Address, code []byte) bool {
	return target != (common.Address{}) && bytes.Equal(r.evm.GetCode(ctx, r.evm.GetCodeHash(ctx, target)), code)
}

func (r *customCollateralRegistry) authenticateRoles(ctx sdk.Context) error {
	owner := customAddressWord(common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName)))
	for _, role := range []struct {
		address common.Address
		code    []byte
	}{{r.profile.Registry, r.registryCode}, {r.profile.TokenCenter, r.centerCode}} {
		implementation := r.evm.GetState(ctx, role.address, customImplementationSlot)
		target := common.BytesToAddress(implementation[12:])
		if implementation != customAddressWord(target) ||
			!r.exactCode(ctx, role.address, r.proxyCode) || !r.exactCode(ctx, target, role.code) ||
			r.evm.GetState(ctx, role.address, customOwnableSlot) != owner {
			return errors.New("DRC20 collateral requires the compiler-pinned governance-owned registry and TokenCenter")
		}
	}
	for _, role := range []struct {
		label   string
		address common.Address
	}{{"token-center", r.profile.TokenCenter}, {"curve-converter", r.profile.CurveConverter}} {
		if r.evm.GetState(ctx, r.profile.Registry, crypto.Keccak256Hash([]byte(role.label), customRegistrySlot[:])) != customAddressWord(role.address) {
			return fmt.Errorf("DRC20 collateral registry role %s differs", role.label)
		}
	}
	// TokenCenterStorage starts with _tokenImpl and has _contractCenter
	// at offset four; the offset-three mapping holds the canonical tokens.
	if r.evm.GetState(ctx, r.profile.TokenCenter, customSlotOffset(customTokenCenterSlot, 4)) != customAddressWord(r.profile.Registry) ||
		!r.exactCode(ctx, r.profile.CurveConverter, r.converterCode) {
		return errors.New("DRC20 TokenCenter backlink or CurveConverter runtime differs")
	}
	word := r.evm.GetState(ctx, r.profile.TokenCenter, customTokenCenterSlot)
	logic := common.BytesToAddress(word[12:])
	if word != customAddressWord(logic) || !r.exactCode(ctx, logic, r.coinCode) {
		return errors.New("DRC20 TokenCenter backed implementation differs from the compiler runtime")
	}
	return nil
}

func (r *customCollateralRegistry) authenticateCoin(ctx sdk.Context, token common.Address, metadata cointypes.Coin) (cointypes.Coin, error) {
	mapping := customSlotOffset(customTokenCenterSlot, 3)
	if !r.exactCode(ctx, token, r.coinProxyCode) ||
		r.evm.GetState(ctx, token, customBeaconSlot) != customAddressWord(r.profile.TokenCenter) ||
		r.evm.GetState(ctx, token, customImplementationSlot) != (common.Hash{}) ||
		r.evm.GetState(ctx, r.profile.TokenCenter, crypto.Keccak256Hash([]byte(metadata.Denom), mapping[:])) != customAddressWord(token) {
		return cointypes.Coin{}, errors.New("DRC20 coin is not the exact approved backed TokenCenter asset")
	}
	if err := r.verifyCurveMetadata(ctx, token, metadata); err != nil {
		return cointypes.Coin{}, err
	}
	// ERC20Storage._totalSupply is the third word, after the two mappings.
	metadata.Volume = sdkmath.NewIntFromBigInt(r.evm.GetState(ctx, token, customSlotOffset(customERC20Slot, 2)).Big())
	metadata.Reserve = r.bank.GetBalance(ctx, sdk.AccAddress(token.Bytes()), identity.BaseDenom).Amount
	if err := metadata.Validate(); err != nil {
		return cointypes.Coin{}, err
	}
	if metadata.Reserve.LT(coinconfig.MinCoinReserve) {
		return cointypes.Coin{}, errors.New("DRC20 live bank DEL reserve is below the approved curve floor")
	}
	return metadata, nil
}

// Coin returns verified live values. The caller checks the exact delta
// against keeper metadata before persisting a permitted conversion.
func (r *customCollateralRegistry) Coin(ctx sdk.Context, token common.Address) (cointypes.Coin, error) {
	metadata, err := r.keeper.GetCoinByDRC(ctx, token.Hex())
	if err != nil {
		return cointypes.Coin{}, err
	}
	manifest, err := r.manifest(ctx)
	if err != nil {
		return cointypes.Coin{}, err
	}
	entry, err := customCollateralEntry(metadata)
	if err != nil {
		return cointypes.Coin{}, err
	}
	for pos := 1; pos < len(manifest); pos += customCollateralEntrySize {
		if bytes.Equal(manifest[pos:pos+customCollateralEntrySize], entry[:]) {
			if err := r.authenticateRoles(ctx); err != nil {
				return cointypes.Coin{}, err
			}
			return r.authenticateCoin(ctx, token, metadata)
		}
	}
	return cointypes.Coin{}, errors.New("DRC20 asset is outside the approved genesis manifest")
}

func customCollateralEntry(coin cointypes.Coin) ([customCollateralEntrySize]byte, error) {
	var entry [customCollateralEntrySize]byte
	token := common.HexToAddress(coin.DRC20Contract)
	coin.Volume, coin.Reserve = sdkmath.ZeroInt(), sdkmath.ZeroInt()
	data, err := proto.Marshal(&coin)
	if err != nil {
		return entry, err
	}
	digest := crypto.Keccak256Hash(data)
	copy(entry[:common.AddressLength], token[:])
	copy(entry[common.AddressLength:], digest[:])
	return entry, nil
}

func customCollateralManifest(coins []cointypes.Coin) ([]byte, error) {
	entries := make([][customCollateralEntrySize]byte, 0, len(coins))
	for _, coin := range coins {
		entry, err := customCollateralEntry(coin)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b [customCollateralEntrySize]byte) int {
		return bytes.Compare(a[:common.AddressLength], b[:common.AddressLength])
	})
	result := []byte{customCollateralManifestVersion}
	for _, entry := range entries {
		result = append(result, entry[:]...)
	}
	return result, nil
}

func (r *customCollateralRegistry) manifest(ctx sdk.Context) ([]byte, error) {
	raw := ctx.KVStore(r.store).Get([]byte{customCollateralManifestKey})
	if len(raw) < 1+customCollateralEntrySize || len(raw) > 1+maxCustomCollateralCoins*customCollateralEntrySize ||
		raw[0] != customCollateralManifestVersion || (len(raw)-1)%customCollateralEntrySize != 0 {
		return nil, errors.New("missing or invalid approved DRC20 genesis manifest")
	}
	return raw, nil
}

// The native ledger is the DRC20 reserve. A direct SDK/IBC credit would
// change the curve with no corresponding authenticated reserve event.
func (r *customCollateralRegistry) restrictReserveCredit(ctx context.Context, _, recipient sdk.AccAddress, coins sdk.Coins) (sdk.AccAddress, error) {
	if len(recipient) != common.AddressLength || !coins.AmountOf(identity.BaseDenom).IsPositive() {
		return recipient, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	manifest, err := r.manifest(sdkCtx)
	if err != nil {
		return nil, err
	}
	for pos := 1; pos < len(manifest); pos += customCollateralEntrySize {
		if bytes.Equal(recipient, manifest[pos:pos+common.AddressLength]) {
			return nil, errors.New("direct DEL credits to approved DRC20 reserves are not admitted")
		}
	}
	return recipient, nil
}
