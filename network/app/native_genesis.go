package app

import (
	"encoding/json"
	"errors"
	"fmt"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/codec"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	coinconfig "github.com/maxwell2010/go-smart-node/network/decimal/coin/config"
	cointypes "github.com/maxwell2010/go-smart-node/network/decimal/coin/types"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	nativevalidatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

func nativeAllocation(cdc codec.JSONCodec, genesis map[string]json.RawMessage) (cointypes.GenesisState, sdkmath.Int, error) {
	var coin cointypes.GenesisState
	var bank banktypes.GenesisState
	if len(genesis[cointypes.ModuleName]) == 0 || len(genesis[banktypes.ModuleName]) == 0 {
		return coin, sdkmath.Int{}, errors.New("native allocation requires explicit coin and bank genesis")
	}
	if err := cdc.UnmarshalJSON(genesis[cointypes.ModuleName], &coin); err != nil {
		return coin, sdkmath.Int{}, err
	}
	if err := cdc.UnmarshalJSON(genesis[banktypes.ModuleName], &bank); err != nil {
		return coin, sdkmath.Int{}, err
	}
	if err := bank.Validate(); err != nil {
		return coin, sdkmath.Int{}, err
	}
	if coin.Params.BaseDenom != identity.BaseDenom {
		return coin, sdkmath.Int{}, errors.New("native base denomination must be del")
	}
	total := sdkmath.ZeroInt()
	for _, balance := range bank.Balances {
		for _, value := range balance.Coins {
			if value.Denom != identity.BaseDenom {
				continue
			}
			if value.Amount.IsNil() || value.Amount.IsNegative() || value.Amount.GT(coinconfig.MaxCoinSupply.Sub(total)) {
				return coin, sdkmath.Int{}, errors.New("native genesis bank allocation exceeds monetary bounds")
			}
			total = total.Add(value.Amount)
		}
	}
	// SDK permits omitted supply and derives it from balances. A supplied DEL
	// supply must agree; it cannot replace the balance-based allocation proof.
	if len(bank.Supply) > 0 && !bank.Supply.AmountOf(identity.BaseDenom).Equal(total) {
		return coin, sdkmath.Int{}, errors.New("declared bank DEL supply differs from genesis balances")
	}
	return coin, total, nil
}

// ReconcileNativeGenesisAllocation is an explicit genesis-builder operation.
// It changes only the supplied map; it never mints or writes runtime state.
func ReconcileNativeGenesisAllocation(cdc codec.JSONCodec, genesis map[string]json.RawMessage) error {
	coin, total, err := nativeAllocation(cdc, genesis)
	if err != nil {
		return err
	}
	coin.Params.BaseVolume = total
	if err := coin.Validate(); err != nil {
		return err
	}
	data, err := cdc.MarshalJSON(&coin)
	if err != nil {
		return err
	}
	genesis[cointypes.ModuleName] = data
	return nil
}

// ValidateNativeGenesisAllocation never repairs an arbitrary genesis at startup.
func ValidateNativeGenesisAllocation(cdc codec.JSONCodec, genesis map[string]json.RawMessage) error {
	coin, total, err := nativeAllocation(cdc, genesis)
	if err != nil {
		return err
	}
	if err := coin.Validate(); err != nil {
		return err
	}
	if !coin.Params.BaseVolume.Equal(total) {
		return fmt.Errorf("native declared base allocation differs from bank balances; explicitly run genesis reconcile-native-allocation")
	}
	var nft nfttypes.GenesisState
	if len(genesis[nfttypes.ModuleName]) == 0 {
		return errors.New("native NFT genesis must be explicit")
	}
	if err := cdc.UnmarshalJSON(genesis[nfttypes.ModuleName], &nft); err != nil {
		return err
	}
	if err := nft.Validate(); err != nil {
		return err
	}
	if len(coin.Coins) != 0 || len(coin.Checks) != 0 || len(nft.Collections) != 0 {
		return errors.New("nonempty native asset genesis requires authenticated contract bootstrap and backing reconciliation, not yet enabled")
	}
	var validators nativevalidatortypes.GenesisState
	if len(genesis[nativevalidatortypes.ModuleName]) == 0 {
		return errors.New("native validator genesis must be explicit")
	}
	if err := cdc.UnmarshalJSON(genesis[nativevalidatortypes.ModuleName], &validators); err != nil {
		return err
	}
	if err := validators.Validate(); err != nil {
		return err
	}
	if validators.Params.BaseDenom != identity.BaseDenom {
		return errors.New("native validator base denomination must be del")
	}
	if len(validators.Validators) != 0 || len(validators.Delegations) != 0 || len(validators.Undelegations) != 0 || len(validators.Redelegations) != 0 || len(validators.LastValidatorPowers) != 0 || validators.LastTotalPower != 0 || len(validators.ValidatorOfflineSince) != 0 || len(validators.ScheduledValidatorSets) != 0 || len(validators.EVMStakeBindings) != 0 || len(validators.EVMFrozenBindings) != 0 || validators.BindingRevision != 0 {
		return errors.New("nonempty native validator genesis requires authenticated financial bootstrap and consensus-controller activation, not yet enabled")
	}
	return nil
}

// Check the optional exported domain before any module writes. Omitted domains
// in fresh defaults are initialized by the configured keeper; exported domains
// may never silently retarget their source network.
func (app *App) validateNativeBindingGenesis(genesis map[string]json.RawMessage) error {
	var validators nativevalidatortypes.GenesisState
	if err := app.appCodec.UnmarshalJSON(genesis[nativevalidatortypes.ModuleName], &validators); err != nil {
		return err
	}
	expected, ok := app.NativeValidatorKeeper.ConfiguredEVMSourceDomain()
	if !ok || expected.ChainID != app.chainID || expected.EVMChainID != app.evmChainID {
		return errors.New("native validator binding domain is not configured for this application")
	}
	if validators.BindingDomain != nil && !nativevalidatortypes.SameEVMSourceDomain(validators.BindingDomain, &expected) {
		return errors.New("native validator genesis binding domain differs from configured application")
	}
	return nil
}
