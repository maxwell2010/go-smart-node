package launchgenesis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	fee "github.com/cosmos/evm/x/feemarket/types"
	"github.com/maxwell2010/go-smart-node/network/app"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
)

const MaxCandidateBytes = 24 * 1024 * 1024

type ColdReport struct {
	Status                string `json:"status"`
	ChainID               string `json:"chainId"`
	EVMChainID            uint64 `json:"evmChainId,string"`
	GenesisSHA256         string `json:"genesisSha256"`
	FirstBlockAppHash     string `json:"firstBlockAppHash"`
	Validators            int    `json:"validators"`
	Power                 int64  `json:"votingPower"`
	BankBalances          int    `json:"bankBalances"`
	InitialSupplyAtoms    string `json:"initialSupplyAtoms"`
	WrappedTokenSymbol    string `json:"wrappedTokenSymbol"`
	PublicGenesisApproved bool   `json:"publicGenesisApproved"`
}

// VerifyCold admits the byte-exact candidate into a fresh in-memory App,
// checks its roster and balances against the ledger, then commits block one.
// Build and VerifyCold run in separate processes to isolate global EVM config.
func VerifyCold(body []byte, ledger Ledger, parameters LaunchParameters) (report ColdReport, err error) {
	if len(body) == 0 || len(body) > MaxCandidateBytes {
		return report, fmt.Errorf("invalid genesis byte count")
	}
	timestamp, err := parameters.validate()
	if err != nil {
		return report, err
	}
	doc, err := cmttypes.GenesisDocFromJSON(body)
	if err != nil {
		return report, err
	}
	if doc.ChainID != ledger.ChainID || doc.InitialHeight != 1 || !doc.GenesisTime.Equal(timestamp) ||
		len(doc.Validators) != 0 || len(doc.AppHash) != 0 || doc.ConsensusParams.Block.MaxGas != candidateMaxGas ||
		doc.ConsensusParams.Evidence.MaxAgeNumBlocks != 100_000 || doc.ConsensusParams.Evidence.MaxAgeDuration != 48*time.Hour {
		return report, fmt.Errorf("invalid cold launch envelope")
	}
	var state app.GenesisState
	if err := json.Unmarshal(doc.AppState, &state); err != nil {
		return report, err
	}
	var profile app.WithdrawalProfile
	if err := json.Unmarshal(state[app.WithdrawalGenesisKey], &profile); err != nil {
		return report, err
	}
	if !(profile.SettlementOnly && profile.EnableDELDelegation && profile.EnableDELHold && profile.EnableDELHoldReset &&
		profile.EnableNFTDelegation && profile.EnableNativeConsensus && profile.EnableNativeIBCV1 && profile.EnableNativeFeeRewards &&
		profile.EnableNativeGovernance && profile.EnableNativeUpgradeGovernance && profile.EnableNativeOrdinaryDELDowntime &&
		profile.EnableNativeHeldDELDowntime && profile.EnableNativeNFTDowntime && profile.EnableNativeDuplicateVote) ||
		profile.TestGraceUntilHeight != 0 {
		return report, fmt.Errorf("candidate is missing the founding application profile")
	}
	positions, _, err := ledger.verify(profile)
	if err != nil {
		return report, err
	}
	fresh, err := newIsolatedApp(ledger.ChainID, ledger.EVMChainID)
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, fresh.close()) }()
	if err := app.NewBasicModuleManager().ValidateGenesis(fresh.app.AppCodec(), fresh.app.TxConfig(), state); err != nil {
		return report, err
	}
	if err := verifyParameters(fresh.app, state, parameters); err != nil {
		return report, err
	}
	var bank banktypes.GenesisState
	if err := fresh.app.AppCodec().UnmarshalJSON(state[banktypes.ModuleName], &bank); err != nil {
		return report, err
	}
	if len(bank.Balances) != len(ledger.Bank.Balances) {
		return report, fmt.Errorf("candidate has different bank recipients")
	}
	params := doc.ConsensusParams.ToProto()
	initial, err := fresh.app.InitChain(&abci.RequestInitChain{ChainId: doc.ChainID, InitialHeight: 1, Time: doc.GenesisTime,
		ConsensusParams: &params, AppStateBytes: doc.AppState})
	if err != nil {
		return report, fmt.Errorf("authenticate candidate InitChain: %w", err)
	}
	updates, err := cmttypes.PB2TM.ValidatorUpdates(initial.Validators)
	if err != nil {
		return report, err
	}
	if len(updates) != len(positions) {
		return report, fmt.Errorf("InitChain validator count differs")
	}
	set := cmttypes.NewValidatorSet(updates)
	for _, position := range positions {
		key, err := position.validator.ConsensusPubKey()
		if err != nil {
			return report, err
		}
		_, actual := set.GetByAddress(key.Address())
		if actual == nil || !bytes.Equal(actual.PubKey.Bytes(), key.Bytes()) || actual.VotingPower != 10_000 {
			return report, fmt.Errorf("InitChain consensus key or power differs")
		}
	}
	// A real Comet block supplies its own hash. This probe reuses a deterministic
	// header for repeated offline results, without claiming an on-chain AppHash.
	blockHash := sha256.Sum256(append([]byte("mintcandy genesis rehearsal block 1\x00"), body...))
	block, err := fresh.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: doc.GenesisTime, Hash: blockHash[:],
		NextValidatorsHash: set.Hash(), ProposerAddress: set.Validators[0].Address})
	if err != nil {
		return report, err
	}
	if _, err := fresh.app.Commit(); err != nil {
		return report, err
	}
	if len(block.AppHash) != sha256.Size || !bytes.Equal(block.AppHash, fresh.app.LastCommitID().Hash) {
		return report, fmt.Errorf("first rehearsal block AppHash differs from the commit")
	}
	ctx := fresh.app.NewUncachedContext(false, cmtproto.Header{ChainID: doc.ChainID, Height: 1})
	for _, row := range ledger.Bank.Balances {
		addr, err := nativeAddress(row.Address, identity.Bech32PrefixAccAddr)
		if err != nil {
			return report, err
		}
		actual := fresh.app.BankKeeper.GetBalance(ctx, sdk.AccAddress(addr.Bytes()), identity.BaseDenom)
		if !actual.Equal(row.Coins[0]) {
			return report, fmt.Errorf("committed balance differs for %s", row.Address)
		}
	}
	supply := fresh.app.BankKeeper.GetSupply(ctx, identity.BaseDenom)
	if !supply.Equal(ledger.Bank.Supply[0]) {
		return report, fmt.Errorf("committed supply differs")
	}
	wrappedSymbol, err := foundingWrapperSymbol(fresh.app, ctx, profile.WrappedDEL)
	if err != nil {
		return report, err
	}
	for _, position := range positions {
		declared := position.validator
		actual, err := fresh.app.NativeValidatorKeeper.GetValidator(ctx, declared.OperatorAddress)
		if err != nil {
			return report, err
		}
		if actual.RewardAddress != declared.RewardAddress || actual.Description != declared.Description ||
			!actual.Commission.Equal(declared.Commission) || !actual.Online {
			return report, fmt.Errorf("committed validator metadata differs")
		}
		funder := sdk.AccAddress(position.funding.Beneficiary.Bytes()).String()
		stake, err := fresh.app.NativeValidatorKeeper.GetDelegation(ctx, funder, declared.OperatorAddress,
			validatortypes.StakeType_Coin, identity.BaseDenom)
		if err != nil {
			return report, err
		}
		if !stake.Stake.Stake.Amount.Equal(position.funding.Amount) {
			return report, fmt.Errorf("committed founder-owned delegation differs")
		}
	}
	hash := sha256.Sum256(body)
	return ColdReport{
		Status: "CANDIDATE_INIT_CHAIN_VERIFIED", ChainID: doc.ChainID, EVMChainID: ledger.EVMChainID,
		GenesisSHA256: hex.EncodeToString(hash[:]), FirstBlockAppHash: hex.EncodeToString(block.AppHash),
		Validators: len(set.Validators), Power: set.TotalVotingPower(), BankBalances: len(bank.Balances), InitialSupplyAtoms: supply.Amount.String(),
		WrappedTokenSymbol: wrappedSymbol,
	}, nil
}

func verifyParameters(a *app.App, state app.GenesisState, parameters LaunchParameters) error {
	cdc := a.AppCodec()
	var market fee.GenesisState
	if err := cdc.UnmarshalJSON(state[fee.ModuleName], &market); err != nil {
		return err
	}
	var gov govv1.GenesisState
	if err := cdc.UnmarshalJSON(state[govtypes.ModuleName], &gov); err != nil {
		return err
	}
	var validators validatortypes.GenesisState
	if err := cdc.UnmarshalJSON(state[validatortypes.ModuleName], &validators); err != nil {
		return err
	}
	if validators.Params.MaxValidators != parameters.MaxValidators ||
		!market.Params.MinGasPrice.Equal(parameters.MinGasPrice) ||
		!sdk.Coins(gov.Params.MinDeposit).Equal(sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, parameters.GovMinDeposit))) ||
		!sdk.Coins(gov.Params.ExpeditedMinDeposit).Equal(sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, parameters.GovExpeditedMinDeposit))) ||
		gov.Params.MinInitialDepositRatio != parameters.GovMinInitialDepositRatio.String() {
		return fmt.Errorf("candidate parameters differ from the supplied launch parameters")
	}
	return nil
}
