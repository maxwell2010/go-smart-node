package launchgenesis

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strings"
	"time"

	sdkmath "cosmossdk.io/math"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	fee "github.com/cosmos/evm/x/feemarket/types"
	evm "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/maxwell2010/go-smart-node/network/app"
	nfttypes "github.com/maxwell2010/go-smart-node/network/decimal/nft/types"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const candidateMaxGas = 24_000_000

type LaunchParameters struct {
	GenesisTime               string            `json:"genesisTimeUtc"`
	MaxValidators             uint32            `json:"maxValidators"`
	MinGasPrice               sdkmath.LegacyDec `json:"minGasPriceAtoms"`
	GovMinDeposit             sdkmath.Int       `json:"govMinDepositAtoms"`
	GovExpeditedMinDeposit    sdkmath.Int       `json:"govExpeditedMinDepositAtoms"`
	GovMinInitialDepositRatio sdkmath.LegacyDec `json:"govMinInitialDepositRatio"`
}

func ReadLaunchParameters(r io.Reader) (LaunchParameters, error) {
	var params LaunchParameters
	const maxSize = 8 * 1024
	body, err := io.ReadAll(io.LimitReader(r, maxSize+1))
	if err != nil {
		return params, err
	}
	if len(body) == 0 || len(body) > maxSize {
		return params, fmt.Errorf("invalid launch parameters byte count")
	}
	if err := decodeLedgerJSON(body, &params); err != nil {
		return params, err
	}
	_, err = params.validate()
	return params, err
}

func (p LaunchParameters) validate() (time.Time, error) {
	genesisTime, err := time.Parse(time.RFC3339Nano, p.GenesisTime)
	if err != nil || !strings.HasSuffix(p.GenesisTime, "Z") || genesisTime.Unix() <= 0 {
		return time.Time{}, fmt.Errorf("genesisTimeUtc must be an explicit UTC RFC3339 timestamp")
	}
	if p.MaxValidators < 4 || p.MaxValidators > validatorkeeper.MaxNativeLiveValidators ||
		p.MinGasPrice.IsNil() || p.MinGasPrice.IsNegative() ||
		p.GovMinDeposit.IsNil() || !p.GovMinDeposit.IsPositive() ||
		p.GovExpeditedMinDeposit.IsNil() || !p.GovExpeditedMinDeposit.GT(p.GovMinDeposit) ||
		p.GovMinInitialDepositRatio.IsNil() || p.GovMinInitialDepositRatio.IsNegative() ||
		p.GovMinInitialDepositRatio.GT(sdkmath.LegacyOneDec()) {
		return time.Time{}, fmt.Errorf("explicit validator limit, fee floor and governance deposits are required")
	}
	return genesisTime.UTC(), nil
}

type DeploymentAddresses struct {
	ChainID          string         `json:"chainId"`
	EVMChainID       uint64         `json:"evmChainId,string"`
	Gateway          common.Address `json:"gateway"`
	WrappedDELWallet string         `json:"wrappedDelWallet"`
}

// DiscoverAddresses runs the same in-memory constructors as Build and reports
// the addresses required by the ledger preparer.
func DiscoverAddresses(chainID string, evmID uint64) (report DeploymentAddresses, err error) {
	seed, err := newIsolatedApp(chainID, evmID)
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, seed.close()) }()
	deployment, err := seed.seedContracts(chainID)
	if err != nil {
		return report, err
	}
	return DeploymentAddresses{chainID, evmID, deployment.profile.Gateway,
		sdk.AccAddress(deployment.profile.WrappedDEL.Bytes()).String()}, nil
}

// Build assembles unsigned CometBFT genesis bytes from a prepared launch ledger
// and explicit parameters. The seed database and temporary accounts are not used
// by the final network. Run VerifyCold in a fresh process on the output bytes.
func Build(ledger Ledger, parameters LaunchParameters) (body []byte, err error) {
	launchTime, err := parameters.validate()
	if err != nil {
		return nil, err
	}
	seed, err := newIsolatedApp(ledger.ChainID, ledger.EVMChainID)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, seed.close()) }()
	deployment, err := seed.seedContracts(ledger.ChainID)
	if err != nil {
		return nil, err
	}
	delay := validatortypes.DefaultParams().UndelegationTime
	if err := ApplyFoundingPositions(seed.app, deployment.ctx, deployment.profile, deployment.sponsor,
		ledger, int64(delay/time.Second)); err != nil {
		return nil, err
	}
	if err := brandFoundingWrapper(seed.app, deployment.ctx, deployment.profile.WrappedDEL); err != nil {
		return nil, err
	}
	state, err := assembleState(seed.app, deployment, ledger, parameters)
	if err != nil {
		return nil, err
	}
	appState, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	doc := cmttypes.GenesisDoc{ChainID: ledger.ChainID, InitialHeight: 1, GenesisTime: launchTime,
		ConsensusParams: cmttypes.DefaultConsensusParams(), AppState: appState}
	doc.ConsensusParams.Block.MaxGas = candidateMaxGas
	doc.ConsensusParams.Evidence.MaxAgeNumBlocks = 100_000
	doc.ConsensusParams.Evidence.MaxAgeDuration = 48 * time.Hour
	if err := doc.ValidateAndComplete(); err != nil {
		return nil, err
	}
	return cmtjson.MarshalIndent(doc, "", "  ")
}

func assembleState(a *app.App, deployment seedDeployment, ledger Ledger, parameters LaunchParameters) (app.GenesisState, error) {
	cdc := a.AppCodec()
	state := a.DefaultGenesis()
	exported, err := a.ModuleManager.ExportGenesisForModules(deployment.ctx, cdc, []string{evm.ModuleName, validatortypes.ModuleName})
	if err != nil {
		return nil, err
	}
	state[evm.ModuleName] = exported[evm.ModuleName]
	var vm evm.GenesisState
	if err := cdc.UnmarshalJSON(state[evm.ModuleName], &vm); err != nil {
		return nil, err
	}
	auth, err := candidateAccounts(a, deployment.ctx, vm, ledger.Bank, deployment.profile.WrappedDEL)
	if err != nil {
		return nil, err
	}
	state[authtypes.ModuleName] = cdc.MustMarshalJSON(auth)

	var bank banktypes.GenesisState
	if err := cdc.UnmarshalJSON(state[banktypes.ModuleName], &bank); err != nil {
		return nil, err
	}
	bank.Supply, bank.Balances = ledger.Bank.Supply, ledger.Bank.Balances
	state[banktypes.ModuleName] = cdc.MustMarshalJSON(&bank)

	var validators validatortypes.GenesisState
	if err := cdc.UnmarshalJSON(exported[validatortypes.ModuleName], &validators); err != nil {
		return nil, err
	}
	validators.Params.MaxValidators = parameters.MaxValidators
	validators.Params.SignedBlocksWindow = 1000
	validators.Params.DowntimeJailDuration = time.Hour
	validators.BindingRevision = 0
	for i := range validators.EVMStakeBindings {
		validators.EVMStakeBindings[i].CreatedHeight, validators.EVMStakeBindings[i].UpdatedHeight = 1, 1
	}
	state[validatortypes.ModuleName] = cdc.MustMarshalJSON(&validators)

	const vaultDenom = "mintcandy_receipts"
	profile := deployment.profile
	creator := authtypes.NewModuleAddress(govtypes.ModuleName).String()
	nft := nfttypes.DefaultGenesisState()
	nft.Collections = []nfttypes.Collection{{
		Creator: creator, Denom: vaultDenom, TypeNft: nfttypes.NftType_NFT721, AddressDRC: profile.PositionVault.Hex(),
	}}
	nft.IsolatedVaults = []nfttypes.IsolatedVaultState{{
		ChainID: ledger.ChainID, EVMChainID: ledger.EVMChainID, Vault: profile.PositionVault.Bytes(),
		NextPositionID: common.BigToHash(big.NewInt(1)).Bytes(), ReceiptCreator: creator, ReceiptDenom: vaultDenom,
	}}
	state[nfttypes.ModuleName] = cdc.MustMarshalJSON(nft)
	state[app.WithdrawalGenesisKey], err = json.Marshal(profile)
	if err != nil {
		return nil, err
	}
	if err := applyEconomicParameters(a, state, parameters); err != nil {
		return nil, err
	}
	if err := app.ReconcileNativeGenesisAllocation(cdc, state); err != nil {
		return nil, err
	}
	if err := app.ValidateNativeGenesisAllocation(cdc, state); err != nil {
		return nil, err
	}
	return state, app.NewBasicModuleManager().ValidateGenesis(cdc, a.TxConfig(), state)
}

func applyEconomicParameters(a *app.App, state app.GenesisState, parameters LaunchParameters) error {
	cdc := a.AppCodec()
	var market fee.GenesisState
	if err := cdc.UnmarshalJSON(state[fee.ModuleName], &market); err != nil {
		return err
	}
	market.Params.MinGasPrice = parameters.MinGasPrice
	state[fee.ModuleName] = cdc.MustMarshalJSON(&market)
	var governance govv1.GenesisState
	if err := cdc.UnmarshalJSON(state[govtypes.ModuleName], &governance); err != nil {
		return err
	}
	governance.Params.MinDeposit = sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, parameters.GovMinDeposit))
	governance.Params.ExpeditedMinDeposit = sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, parameters.GovExpeditedMinDeposit))
	governance.Params.MinInitialDepositRatio = parameters.GovMinInitialDepositRatio.String()
	state[govtypes.ModuleName] = cdc.MustMarshalJSON(&governance)
	return nil
}

func candidateAccounts(a *app.App, ctx sdk.Context, vm evm.GenesisState, bank BankAllocation, wrapper common.Address) (*authtypes.GenesisState, error) {
	contracts := make(map[common.Address]uint64, len(vm.Accounts))
	for _, account := range vm.Accounts {
		addr := common.HexToAddress(account.Address)
		account := a.AccountKeeper.GetAccount(ctx, sdk.AccAddress(addr.Bytes()))
		if account == nil || account.GetPubKey() != nil {
			return nil, fmt.Errorf("invalid exported EVM contract account")
		}
		contracts[addr] = account.GetSequence()
	}
	if _, ok := contracts[wrapper]; !ok {
		return nil, fmt.Errorf("assembled EVM genesis is missing WDEL")
	}
	all := make([]common.Address, 0, len(contracts)+len(bank.Balances)-1)
	for addr := range contracts {
		all = append(all, addr)
	}
	for _, row := range bank.Balances {
		addr, err := nativeAddress(row.Address, identity.Bech32PrefixAccAddr)
		if err != nil {
			return nil, err
		}
		if addr == wrapper {
			continue
		}
		_, isContract := contracts[addr]
		if isContract || a.BankKeeper.BlockedAddr(sdk.AccAddress(addr.Bytes())) {
			return nil, fmt.Errorf("funded user address conflicts with deployed contract")
		}
		all = append(all, addr)
	}
	slices.SortFunc(all, func(a, b common.Address) int { return a.Cmp(b) })
	accounts := make(authtypes.GenesisAccounts, 0, len(all))
	for i, addr := range all {
		accounts = append(accounts, authtypes.NewBaseAccount(sdk.AccAddress(addr.Bytes()), nil, uint64(i), contracts[addr]))
	}
	return authtypes.NewGenesisState(authtypes.DefaultParams(), accounts), nil
}
