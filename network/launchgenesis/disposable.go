package launchgenesis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	srvflags "github.com/cosmos/evm/server/flags"
	"github.com/cosmos/evm/x/vm/statedb"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/maxwell2010/go-smart-node/network/systemcontracts"
)

type appOptions map[string]any

func (o appOptions) Get(key string) any { return o[key] }

type isolatedApp struct {
	app  *app.App
	home string
}

func newIsolatedApp(chainID string, evmID uint64) (*isolatedApp, error) {
	if err := identity.ValidateProfileChainIDs(chainID, evmID); err != nil {
		return nil, err
	}
	home, err := os.MkdirTemp("", "mcnd-genesis-")
	if err != nil {
		return nil, err
	}
	a := app.NewApp(log.NewNopLogger(), dbm.NewMemDB(), true, appOptions{
		flags.FlagHome: home, flags.FlagChainID: chainID, srvflags.EVMChainID: evmID,
	})
	return &isolatedApp{app: a, home: home}, nil
}

func (s *isolatedApp) close() error {
	return errors.Join(s.app.Close(), os.RemoveAll(s.home))
}

type seedDeployment struct {
	ctx     sdk.Context
	profile app.WithdrawalProfile
	sponsor common.Address
}

func roleAddress(chainID, label string) common.Address {
	return common.BytesToAddress(crypto.Keccak256([]byte("mintcandy launch " + label + " v1\x00" + chainID)))
}

// seedContracts is shared by address discovery and assembly. The temporary SDK
// validator and budgets exist only to initialize a deterministic EVM environment.
// The addresses come from hashes and this procedure uses no signing keys.
func (s *isolatedApp) seedContracts(chainID string) (seedDeployment, error) {
	var empty seedDeployment
	a, cdc := s.app, s.app.AppCodec()
	state := a.DefaultGenesis()
	deployer := roleAddress(chainID, "constructor sender")
	sponsor := roleAddress(chainID, "collateral sponsor")
	pubkey := &ed25519.PubKey{Key: crypto.Keccak256([]byte("mintcandy launch temporary proposer v1\x00" + chainID))}
	proposer := pubkey.Address()
	var staking stakingtypes.GenesisState
	if err := cdc.UnmarshalJSON(state[stakingtypes.ModuleName], &staking); err != nil {
		return empty, err
	}
	operator := sdk.ValAddress(deployer.Bytes()).String()
	validator, err := stakingtypes.NewValidator(operator, pubkey, stakingtypes.Description{Moniker: "Offline EVM bootstrap"})
	if err != nil {
		return empty, err
	}
	validator.Status, validator.Tokens, validator.DelegatorShares = stakingtypes.Bonded, sdk.DefaultPowerReduction, sdkmath.LegacyOneDec()
	staking.Validators = []stakingtypes.Validator{validator}
	staking.Delegations = []stakingtypes.Delegation{
		stakingtypes.NewDelegation(sdk.AccAddress(deployer.Bytes()).String(), operator, validator.DelegatorShares),
	}
	state[stakingtypes.ModuleName] = cdc.MustMarshalJSON(&staking)

	var bank banktypes.GenesisState
	if err := cdc.UnmarshalJSON(state[banktypes.ModuleName], &bank); err != nil {
		return empty, err
	}
	backing := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(40_000, 18)))
	bonded := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, validator.Tokens))
	bank.Balances = []banktypes.Balance{
		{Address: sdk.AccAddress(sponsor.Bytes()).String(), Coins: backing},
		{Address: authtypes.NewModuleAddress(stakingtypes.BondedPoolName).String(), Coins: bonded},
	}
	bank.Supply = backing.Add(bonded...)
	state[banktypes.ModuleName] = cdc.MustMarshalJSON(&bank)
	auth := authtypes.NewGenesisState(authtypes.DefaultParams(), authtypes.GenesisAccounts{
		authtypes.NewBaseAccount(sdk.AccAddress(deployer.Bytes()), nil, 0, 0),
		authtypes.NewBaseAccount(sdk.AccAddress(sponsor.Bytes()), nil, 1, 0),
	})
	state[authtypes.ModuleName] = cdc.MustMarshalJSON(auth)
	if err := app.ReconcileNativeGenesisAllocation(cdc, state); err != nil {
		return empty, err
	}
	body, err := json.Marshal(state)
	if err != nil {
		return empty, err
	}
	// A fixed seed timestamp makes constructors independent of the wall clock.
	timestamp := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	consensus := cmttypes.DefaultConsensusParams().ToProto()
	initial, err := a.InitChain(&abci.RequestInitChain{ChainId: chainID, InitialHeight: 1, Time: timestamp,
		AppStateBytes: body, ConsensusParams: &consensus})
	if err != nil {
		return empty, fmt.Errorf("initialize temporary EVM seed: %w", err)
	}
	updates, err := cmttypes.PB2TM.ValidatorUpdates(initial.Validators)
	if err != nil {
		return empty, err
	}
	set := cmttypes.NewValidatorSet(updates)
	if _, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: timestamp,
		NextValidatorsHash: set.Hash(), ProposerAddress: proposer}); err != nil {
		return empty, err
	}
	if _, err := a.Commit(); err != nil {
		return empty, err
	}
	ctx := a.NewUncachedContext(false, cmtproto.Header{ChainID: chainID, Height: 2, Time: timestamp, ProposerAddress: proposer})
	authority := common.BytesToAddress(authtypes.NewModuleAddress(govtypes.ModuleName))
	contracts, err := systemcontracts.DeployEmptyLaunchContracts(deployer, authority, a.EVMKeeper.GetNonce(ctx, deployer),
		func(artifact systemcontracts.Artifact, args ...any) (common.Address, error) {
			creation, err := artifact.Deployment(args...)
			if err != nil {
				return common.Address{}, err
			}
			execution := ctx.WithGasMeter(storetypes.NewGasMeter(50_000_000))
			nonce := a.EVMKeeper.GetNonce(execution, deployer)
			target := crypto.CreateAddress(deployer, nonce)
			call := core.Message{From: deployer, Nonce: nonce, Data: creation, Value: new(big.Int),
				GasLimit: 24_000_000, GasPrice: new(big.Int), GasFeeCap: new(big.Int), GasTipCap: new(big.Int),
				SkipNonceChecks: true, SkipFromEOACheck: true}
			result, err := a.EVMKeeper.ApplyMessage(execution,
				statedb.New(execution, a.EVMKeeper, statedb.NewEmptyTxConfig()), call, nil, true, false, true)
			if err != nil {
				return common.Address{}, err
			}
			if result.VmError != "" {
				return common.Address{}, fmt.Errorf("constructor failed: %s", result.VmError)
			}
			if !bytes.Equal(artifact.Runtime, a.EVMKeeper.GetCode(execution, a.EVMKeeper.GetCodeHash(execution, target))) {
				return common.Address{}, fmt.Errorf("constructor runtime differs")
			}
			return target, nil
		})
	if err != nil {
		return empty, err
	}
	profile := app.WithdrawalProfile{
		Registry: contracts.Registry, Gateway: contracts.Gateway, Library: contracts.Library,
		NFTCenter: contracts.NFTCenter, PositionVault: contracts.PositionVault, MasterValidator: contracts.MasterValidator,
		WrappedDEL: contracts.WrappedDEL, TokenCenter: contracts.TokenCenter, CurveConverter: contracts.CurveConverter,
		SettlementOnly: true, EnableDELDelegation: true, EnableDELHold: true, EnableDELHoldReset: true, EnableNFTDelegation: true,
		EnableNativeConsensus: true, EnableNativeFeeRewards: true, EnableNativeIBCV1: true, EnableNativeGovernance: true,
		EnableNativeUpgradeGovernance: true, EnableNativeOrdinaryDELDowntime: true, EnableNativeHeldDELDowntime: true,
		EnableNativeNFTDowntime: true, EnableNativeDuplicateVote: true,
	}
	return seedDeployment{ctx, profile, sponsor}, nil
}
