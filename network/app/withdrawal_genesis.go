package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	abci "github.com/cometbft/cometbft/abci/types"
	cryptoenc "github.com/cometbft/cometbft/crypto/encoding"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/ethereum/go-ethereum/common"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	"github.com/maxwell2010/go-smart-node/network/decimal/validator/settlement"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
)

const (
	// WithdrawalGenesisKey is an application genesis envelope, not a module.
	WithdrawalGenesisKey     = "withdrawal_settlement"
	maxWithdrawalGenesisRows = 16
	withdrawalGenesisGas     = 192_000_000
)

func parseWithdrawalGenesisProfile(raw json.RawMessage) (*WithdrawalProfile, error) {
	if raw == nil {
		return nil, nil
	}
	if len(raw) > 1024 {
		return nil, errors.New("withdrawal genesis profile exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var profile WithdrawalProfile
	if err := decoder.Decode(&profile); err != nil {
		return nil, fmt.Errorf("decode withdrawal genesis profile: %w", err)
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, errors.New("withdrawal genesis profile has trailing data")
	}
	if profile.EnableDELHoldReset && !profile.EnableDELHold {
		return nil, errors.New("DEL hold reset genesis requires DEL hold admission")
	}
	if profile.EnableDELHold && !profile.EnableDELDelegation {
		return nil, errors.New("DEL hold genesis requires DEL delegation admission")
	}
	seen := make(map[common.Address]bool)
	for _, addr := range []common.Address{profile.Registry, profile.Gateway, profile.Library, profile.NFTCenter,
		profile.PositionVault, profile.MasterValidator, profile.WrappedDEL} {
		if addr == (common.Address{}) || seen[addr] {
			return nil, errors.New("withdrawal genesis requires distinct nonzero system addresses")
		}
		seen[addr] = true
	}
	return &profile, nil
}

// The cold profile imports only claims already frozen in the approved Gateway.
// It does not admit active staking power or register unrelated native assets.
func validateWithdrawalGenesisSnapshot(profile WithdrawalProfile, nft *nfttypes.GenesisState, validators *validatortypes.GenesisState) error {
	if validators.BindingDomain == nil || validators.BindingRevision != 0 || len(validators.Validators) > maxWithdrawalGenesisRows ||
		len(validators.EVMFrozenBindings) > maxWithdrawalGenesisRows ||
		len(validators.Undelegations) > maxWithdrawalGenesisRows || len(validators.Delegations) != 0 || len(validators.Redelegations) != 0 ||
		len(validators.EVMStakeBindings) != 0 || len(validators.ScheduledValidatorSets) != 0 ||
		len(validators.ValidatorOfflineSince) != 0 || len(validators.LastValidatorPowers) != 0 || validators.LastTotalPower != 0 {
		return errors.New("withdrawal genesis is limited to a bounded pending settlement snapshot")
	}
	for _, val := range validators.Validators {
		if val.Stake != 0 || val.Status != validatortypes.BondStatus_Unbonded || val.Online {
			return errors.New("withdrawal genesis cannot activate native validator power")
		}
	}
	if profile.EnableDELDelegation {
		if len(validators.Validators) == 0 || len(validators.EVMFrozenBindings) != 0 || len(validators.Undelegations) != 0 ||
			len(nft.Collections) != 0 || len(nft.ExternalBindings) != 0 || len(nft.IsolatedVaults) != 0 || len(nft.IsolatedPositions) != 0 {
			return errors.New("DEL delegation genesis requires declared source validators and empty native positions and queues")
		}
		return nil
	}
	if len(validators.EVMFrozenBindings) == 0 {
		return errors.New("withdrawal genesis requires pending settlement claims")
	}
	tokenIDs := make(map[string]bool)
	nftRows := 0
	for _, row := range validators.EVMFrozenBindings {
		if !row.Active || row.FreezeType != 1 || common.BytesToAddress(row.Gateway) != profile.Gateway {
			return errors.New("withdrawal genesis contains a nonpending or differently scoped claim")
		}
		if row.NFTPosition != nil {
			nftRows++
			if common.BytesToAddress(row.NFTPosition.Vault) != profile.PositionVault {
				return errors.New("withdrawal genesis uses a different NFT vault")
			}
			tokenIDs[row.NFTPosition.Origin.NativeTokenID] = true
			tokenIDs[row.NFTPosition.Custody.NativeTokenID] = true
		}
	}
	vaults := 0
	if nftRows > 0 {
		vaults = 1
	}
	if len(nft.IsolatedPositions) != nftRows || len(nft.IsolatedVaults) != vaults ||
		len(nft.ExternalBindings) != len(tokenIDs) || len(nft.Collections) > len(tokenIDs) {
		return errors.New("withdrawal genesis native NFT state differs from its pending claims")
	}
	remaining := len(tokenIDs)
	for _, collection := range nft.Collections {
		if len(collection.Tokens) == 0 {
			return errors.New("withdrawal genesis includes an unrelated NFT collection")
		}
		for _, token := range collection.Tokens {
			if token == nil || !tokenIDs[token.ID] {
				return errors.New("withdrawal genesis includes an unrelated NFT token")
			}
			delete(tokenIDs, token.ID)
			remaining--
		}
	}
	if remaining != 0 {
		return errors.New("withdrawal genesis is missing native NFT token metadata")
	}
	return nil
}

func (app *App) initWithdrawalGenesis(ctx sdk.Context, req *abci.RequestInitChain, genesis GenesisState, raw json.RawMessage) (*abci.ResponseInitChain, error) {
	if req.Time.Unix() <= 0 {
		return nil, errors.New("invalid initial context for withdrawal genesis")
	}
	profile, err := parseWithdrawalGenesisProfile(raw)
	if err != nil || profile == nil {
		return nil, errors.New("missing or invalid withdrawal genesis profile")
	}
	profile.SettlementOnly = true
	controller := app.withdrawals
	if app.stateSync.prepared {
		if controller == nil || controller.profile != *profile {
			return nil, errors.New("withdrawal genesis profile differs from the prepared state sync profile")
		}
	} else {
		if controller != nil {
			return nil, errors.New("withdrawal genesis profile is already installed")
		}
		controller, err = app.newWithdrawalController(*profile)
		if err != nil {
			return nil, err
		}
	}
	var validators validatortypes.GenesisState
	if err := app.appCodec.UnmarshalJSON(genesis[validatortypes.ModuleName], &validators); err != nil {
		return nil, err
	}
	for _, row := range validators.EVMFrozenBindings {
		if row.UpdatedHeight > max(req.InitialHeight, 1) {
			return nil, errors.New("withdrawal genesis binding height exceeds the initial height")
		}
	}
	child, commit := ctx.CacheContext()
	if err := app.UpgradeKeeper.SetModuleVersionMap(child, app.ModuleManager.GetVersionMap()); err != nil {
		return nil, err
	}
	response, err := app.ModuleManager.InitGenesis(child, app.appCodec, genesis)
	if err != nil {
		return nil, err
	}
	// InitChain has no proposer. A deterministic initialized SDK validator
	// supplies COINBASE for discarded EVM getter calls only.
	var proposer []byte
	for _, val := range response.Validators {
		key, err := cryptoenc.PubKeyFromProto(val.PubKey)
		if err != nil {
			return nil, fmt.Errorf("decode withdrawal genesis proposer: %w", err)
		}
		addr := key.Address()
		if _, err := app.SlashingKeeper.GetValidatorSigningInfo(child, sdk.ConsAddress(addr)); err != nil {
			return nil, fmt.Errorf("withdrawal genesis consensus validator signing state: %w", err)
		}
		if len(proposer) == 0 || bytes.Compare(addr, proposer) < 0 {
			proposer = bytes.Clone(addr)
		}
	}
	if len(proposer) == 0 {
		return nil, errors.New("withdrawal genesis has no initialized consensus validator")
	}
	header := child.BlockHeader()
	header.ProposerAddress = proposer
	header.Height = max(req.InitialHeight, 1)
	read, _ := child.CacheContext()
	read = read.WithBlockHeader(header)
	gasBefore := read.GasMeter().GasConsumed()
	limits := settlement.GenesisLimits{
		MaxRows: maxWithdrawalGenesisRows, SDKGas: withdrawalGenesisGas,
	}
	if profile.EnableDELDelegation {
		for _, val := range validators.Validators {
			operator, err := app.NativeValidatorKeeper.ValidatorAddressCodec().StringToBytes(val.OperatorAddress)
			if err != nil || len(operator) != common.AddressLength {
				return nil, errors.New("invalid DEL genesis source validator address")
			}
			limits.DELValidators = append(limits.DELValidators, common.BytesToAddress(operator))
		}
	}
	if err := controller.executor.AttestGenesis(read, validators.EVMFrozenBindings, limits, controller.assets.AttestGenesisNFT); err != nil {
		return nil, fmt.Errorf("authenticate withdrawal genesis: %w", err)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	child.KVStore(app.keys[validatortypes.StoreKey]).Set([]byte{WithdrawalProfileKey}, encoded)
	commit()
	if app.withdrawals == nil {
		app.installWithdrawalController(controller)
	}
	ctx.Logger().Info("authenticated withdrawal genesis admitted", "pending", len(validators.EVMFrozenBindings),
		"gas", read.GasMeter().GasConsumed()-gasBefore)
	return response, nil
}
