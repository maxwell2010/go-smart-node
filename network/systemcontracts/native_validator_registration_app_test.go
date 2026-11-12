package systemcontracts_test

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"testing"
	"time"

	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmttypes "github.com/cometbft/cometbft/types"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	validatorkeeper "github.com/maxwell2010/go-smart-node/network/decimal/validator/keeper"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

func configureNativeRegistrationGovernance(t *testing.T, e isolatedBackendEnv, genesis map[string]json.RawMessage) {
	t.Helper()
	cdc := e.app.AppCodec()
	var gov govv1.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[govtypes.ModuleName], &gov))
	period, expedited := 3*time.Second, time.Second
	gov.Params.VotingPeriod, gov.Params.ExpeditedVotingPeriod = &period, &expedited
	gov.Params.MinDeposit = sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(1, 18)))
	gov.Params.ExpeditedMinDeposit = sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(2, 18)))
	require.NoError(t, govv1.ValidateGenesis(&gov))
	genesis[govtypes.ModuleName] = cdc.MustMarshalJSON(&gov)
}

func (l nativeHistoricalLifecycle) testWeightedValidatorRegistration() {
	t := l.t
	t.Helper()
	keeper := l.app.NativeValidatorKeeper
	const commission uint8 = 7
	const moniker = "Isolated replacement validator"
	proposerKey, err := registryFixtureKey(4, true)
	require.NoError(t, err)
	voterKey, err := registryFixtureKey(3, true)
	require.NoError(t, err)
	proposer := sdk.AccAddress(proposerKey.PubKey().Address())
	voter := sdk.AccAddress(voterKey.PubKey().Address())
	require.Equal(t, l.expect.Actor, common.BytesToAddress(voter))
	operatorAddress := common.BytesToAddress(proposer)
	operator, err := keeper.ValidatorAddressCodec().BytesToString(operatorAddress.Bytes())
	require.NoError(t, err)
	old := cmted25519.GenPrivKeyFromSecret([]byte(nativeHistoricalConsensusKeyScope + "c"))
	oldAddress := sdk.ConsAddress(old.PubKey().Address())
	require.Equal(t, common.FromHex(l.target.Consensus), []byte(oldAddress))
	fresh := cmted25519.GenPrivKeyFromSecret([]byte(nativeHistoricalConsensusKeyScope + "replacement"))
	freshAddress := sdk.ConsAddress(fresh.PubKey().Address())
	initialSupply := l.supply()
	deposit := sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(1, 18)))

	propose := func(metadata string) (govv1.Proposal, *abci.ResponseFinalizeBlock) {
		t.Helper()
		id, err := l.app.GovKeeper.ProposalID.Peek(l.snapshot())
		require.NoError(t, err)
		msg, err := govv1.NewMsgSubmitProposal(nil, deposit, proposer.String(), metadata, moniker,
			"Vote on the operator's request in the isolated network.", false)
		require.NoError(t, err)
		l.signedGovernance(proposerKey, msg)
		proposal, err := l.app.GovKeeper.Proposals.Get(l.snapshot(), id)
		require.NoError(t, err)
		require.Equal(t, govv1.StatusVotingPeriod, proposal.Status)
		require.Equal(t, proposer.String(), proposal.Proposer)
		require.Equal(t, metadata, proposal.Metadata)
		l.signedGovernance(voterKey, govv1.NewMsgVoteWeighted(voter, id, govv1.WeightedVoteOptions{
			{Option: govv1.OptionYes, Weight: "0.75"},
			{Option: govv1.OptionNo, Weight: "0.25"},
		}, ""))
		var closeBlock *abci.ResponseFinalizeBlock
		for l.snapshot().BlockTime().Before(*proposal.VotingEndTime) {
			closeBlock = l.finalize(nil, nil)
			l.commit(closeBlock)
		}
		require.NotNil(t, closeBlock)
		proposal, err = l.app.GovKeeper.Proposals.Get(l.snapshot(), id)
		require.NoError(t, err)
		require.NotNil(t, proposal.FinalTallyResult)
		require.Equal(t, sdkmath.NewIntWithDecimal(6, 18).String(), proposal.FinalTallyResult.YesCount)
		require.Equal(t, sdkmath.NewIntWithDecimal(2, 18).String(), proposal.FinalTallyResult.NoCount)
		return proposal, closeBlock
	}
	register := func(key cmted25519.PrivKey) (govv1.Proposal, *abci.ResponseFinalizeBlock) {
		return propose(nativeRegistrationMetadata(t, l.doc.ChainID, operatorAddress, key, l.app.LastBlockHeight()+20, commission))
	}

	rejected, rejectedBlock := register(old)
	require.Equal(t, govv1.StatusFailed, rejected.Status)
	require.Contains(t, rejected.FailedReason, "unused consensus key")
	require.Equal(t, strconv.FormatUint(rejected.Id, 10), registrationEvent(t, rejectedBlock, "decimal_native_validator_registration_failed")["proposal_id"])
	require.Empty(t, rejectedBlock.ValidatorUpdates)
	_, err = keeper.GetValidator(l.snapshot(), operator)
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	status, paused, count := l.masterValidatorState(operatorAddress)
	require.Equal(t, uint8(0), status)
	require.False(t, paused)
	require.Zero(t, count)

	approved, approvedBlock := register(fresh)
	require.Equal(t, govv1.StatusPassed, approved.Status)
	attributes := registrationEvent(t, approvedBlock, "decimal_native_validator_registered")
	require.Equal(t, strconv.FormatUint(approved.Id, 10), attributes["proposal_id"])
	require.Equal(t, operator, attributes["operator"])
	require.Empty(t, approvedBlock.ValidatorUpdates, "registration alone cannot add voting power")
	validator, err := keeper.GetValidatorByConsAddr(l.snapshot(), freshAddress)
	require.NoError(t, err)
	require.Equal(t, operator, validator.OperatorAddress)
	require.Equal(t, proposer.String(), validator.RewardAddress)
	require.Equal(t, moniker, validator.Description.Moniker)
	require.True(t, sdkmath.LegacyNewDecWithPrec(int64(commission), 2).Equal(validator.Commission))
	require.True(t, validator.Online)
	require.False(t, validator.Jailed)
	require.Equal(t, validatortypes.BondStatus_Unbonded, validator.Status)
	require.Zero(t, validator.Stake)
	reserve, err := keeper.GetActiveDELReserve(l.snapshot(), operator)
	require.NoError(t, err)
	require.True(t, reserve.IsZero())
	status, paused, count = l.masterValidatorState(operatorAddress)
	require.Equal(t, uint8(2), status)
	require.False(t, paused)
	require.Zero(t, count)
	require.True(t, initialSupply.Equal(l.supply()))

	amount := sdkmath.NewIntWithDecimal(3, 18)
	staked := l.signedGateway("delegateDEL", amount, operatorAddress)
	stakeHeight := l.app.LastBlockHeight()
	changes, err := cmttypes.PB2TM.ValidatorUpdates(staked.ValidatorUpdates)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, []byte(freshAddress), []byte(changes[0].Address))
	require.Equal(t, int64(3), changes[0].VotingPower)
	reserve, err = keeper.GetActiveDELReserve(l.snapshot(), operator)
	require.NoError(t, err)
	require.True(t, amount.Equal(reserve))
	for height := stakeHeight; height <= stakeHeight+2; height++ {
		if height > stakeHeight {
			l.commit(l.finalize(nil, nil))
		}
		history, err := keeper.GetHistoricalInfo(l.snapshot(), height)
		require.NoError(t, err)
		set, err := validatorkeeper.ConsensusSet(history.Valset)
		require.NoError(t, err)
		_, member := set.GetByAddress(freshAddress)
		if height == stakeHeight+2 {
			require.NotNil(t, member)
			require.Equal(t, int64(3), member.VotingPower)
		} else {
			require.Nil(t, member, "the new stake cannot vote before H+2")
		}
		require.False(t, set.HasAddress(oldAddress))
	}
	exclusion, err := keeper.GetNativeDuplicateVoteExclusion(l.snapshot(), oldAddress)
	require.NoError(t, err)
	require.Equal(t, validatorkeeper.NativeDuplicateVoteExclusion{InfractionHeight: 2, SettledHeight: 5}, exclusion)

	// A matured, settled payout and a governance vote are separate from
	// the operator's pending frozen custody.
	l.signedGateway("withdraw", sdkmath.ZeroInt(), operatorAddress, l.expect.Wrapped, amount.BigInt())
	const retirement = "mintcandy-validator-retirement/v1"
	blocked, blockedBlock := propose(retirement)
	require.Equal(t, govv1.StatusFailed, blocked.Status)
	require.Contains(t, blocked.FailedReason, "requires no active stake or frozen custody")
	validator, err = keeper.GetValidatorByConsAddr(l.snapshot(), freshAddress)
	require.NoError(t, err)
	require.True(t, validator.Online)
	require.Equal(t, strconv.FormatUint(blocked.Id, 10), registrationEvent(t, blockedBlock, "decimal_native_validator_retirement_failed")["proposal_id"])
	status, paused, count = l.masterValidatorState(operatorAddress)
	require.Equal(t, uint8(2), status)
	require.False(t, paused, "failed native retirement rolls back the Master pause")
	require.Zero(t, count)

	actor := sdk.AccAddress(l.expect.Actor[:]).String()
	for range 12 {
		_, err = keeper.GetUndelegation(l.snapshot(), actor, operator)
		if !errors.Is(err, validatorkeeper.ErrNotFound) {
			require.NoError(t, err)
			l.commit(l.finalize(nil, nil))
			continue
		}
		break
	}
	_, err = keeper.GetUndelegation(l.snapshot(), actor, operator)
	require.ErrorIs(t, err, validatorkeeper.ErrNotFound)
	live, err := keeper.NativeLiveValidatorOperators(l.snapshot())
	require.NoError(t, err)
	require.Contains(t, live, operator)
	retired, retiredBlock := propose(retirement)
	require.Equal(t, govv1.StatusPassed, retired.Status, retired.FailedReason)
	require.Equal(t, operator, registrationEvent(t, retiredBlock, "decimal_native_validator_retired")["operator"])
	require.Empty(t, retiredBlock.ValidatorUpdates)
	active, err := keeper.NativeLiveValidatorOperators(l.snapshot())
	require.NoError(t, err)
	require.Len(t, active, len(live)-1)
	require.NotContains(t, active, operator)
	tombstone, err := keeper.IsNativeValidatorRetired(l.snapshot(), operator)
	require.NoError(t, err)
	require.True(t, tombstone)
	validator, err = keeper.GetValidatorByConsAddr(l.snapshot(), freshAddress)
	require.NoError(t, err)
	require.Equal(t, operator, validator.OperatorAddress)
	require.False(t, validator.Online)
	require.False(t, validator.Jailed, "ordinary retirement preserves the historical penalty route")
	require.ErrorIs(t, keeper.ReleaseNativeDowntimeJail(l.snapshot(), operator), validatorkeeper.ErrNativeValidatorRetired)
	status, paused, count = l.masterValidatorState(operatorAddress)
	require.Equal(t, uint8(2), status)
	require.True(t, paused)
	require.Zero(t, count)

	revert := crypto.Keccak256([]byte("InvalidValidator()"))[:4]
	assertRejects := func(method string, value sdkmath.Int, args ...interface{}) {
		t.Helper()
		block, result := l.finalizeSignedEthereum(l.signedGatewayTx(method, value, args...))
		require.NotEmpty(t, result.VmError)
		require.Equal(t, revert, result.Ret)
		l.commit(block)
	}
	assertRejects("delegateDEL", amount, operatorAddress)
	assertRejects("delegateHoldDEL", amount, operatorAddress, big.NewInt(l.snapshot().BlockTime().Unix()+3600))
	assertRejects("delegateNFT1155", sdkmath.ZeroInt(), operatorAddress, l.expect.Wrapped, big.NewInt(1), big.NewInt(1))
	require.True(t, initialSupply.Equal(l.supply()))

	reusedOperator, _ := register(cmted25519.GenPrivKeyFromSecret([]byte(nativeHistoricalConsensusKeyScope + "another-key")))
	require.Equal(t, govv1.StatusFailed, reusedOperator.Status)
	require.Contains(t, reusedOperator.FailedReason, "unused operator address")
}

func nativeRegistrationMetadata(t *testing.T, chain string, operator common.Address, key cmted25519.PrivKey, expires int64, commission uint8) string {
	t.Helper()
	// This domain is solely the ownership proof for registration. No
	// consensus vote, block ID or raw evidence is ever signed by the fixture.
	message := append([]byte("mintcandy:validator-registration-pop:v1\x00"), byte(len(chain)))
	message = append(message, chain...)
	message = binary.BigEndian.AppendUint64(message, 990001)
	message = append(message, operator[:]...)
	wire := binary.BigEndian.AppendUint64(nil, uint64(expires))
	wire = append(wire, commission)
	wire = append(wire, key.PubKey().Bytes()...)
	signature, err := key.Sign(append(message, wire...))
	require.NoError(t, err)
	wire = append(wire, signature...)
	return "mintcandy-validator-registration/v1:" + base64.RawURLEncoding.EncodeToString(wire)
}

func (l nativeHistoricalLifecycle) signedGovernance(key *ethsecp256k1.PrivKey, msg sdk.Msg) {
	t := l.t
	t.Helper()
	address := sdk.AccAddress(key.PubKey().Address())
	account := l.app.AccountKeeper.GetAccount(l.snapshot(), address)
	require.NotNil(t, account)
	sequence := account.GetSequence()
	config := l.app.GetTxConfig()
	builder := config.NewTxBuilder()
	require.NoError(t, builder.SetMsgs(msg))
	builder.SetGasLimit(500_000)
	builder.SetFeeAmount(sdk.NewCoins(sdk.NewCoin(identity.BaseDenom, sdkmath.NewIntWithDecimal(1, 18))))
	mode := signing.SignMode_SIGN_MODE_DIRECT
	require.NoError(t, builder.SetSignatures(signing.SignatureV2{PubKey: key.PubKey(), Sequence: sequence,
		Data: &signing.SingleSignatureData{SignMode: mode}}))
	sig, err := clienttx.SignWithPrivKey(t.Context(), mode, authsigning.SignerData{Address: address.String(), ChainID: l.doc.ChainID,
		AccountNumber: account.GetAccountNumber(), Sequence: sequence, PubKey: key.PubKey()}, builder, key, config, sequence)
	require.NoError(t, err)
	require.NoError(t, builder.SetSignatures(sig))
	encoded, err := config.TxEncoder()(builder.GetTx())
	require.NoError(t, err)
	block := l.finalize([][]byte{encoded}, nil)
	require.Len(t, block.TxResults, 1)
	require.Zero(t, block.TxResults[0].Code, block.TxResults[0].Log)
	l.commit(block)
}

func registrationEvent(t *testing.T, block *abci.ResponseFinalizeBlock, eventType string) map[string]string {
	t.Helper()
	var attributes map[string]string
	for _, event := range block.Events {
		if event.Type != eventType {
			continue
		}
		require.Nil(t, attributes)
		attributes = make(map[string]string, len(event.Attributes))
		for _, attribute := range event.Attributes {
			attributes[attribute.Key] = attribute.Value
		}
	}
	require.NotEmpty(t, attributes, eventType)
	return attributes
}
