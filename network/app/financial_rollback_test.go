package app

import (
	"crypto/ecdsa"
	"math/big"
	"testing"
	"time"

	"cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	upstreamcontracts "github.com/cosmos/evm/precompiles/testutil/contracts"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/stretchr/testify/require"
)

// Ordinary bounded regression: a successful staking call establishes a control,
// then one caught subcall and one reverted parent must not leak financial state.
// This uses the pinned upstream public StakingReverter fixture and no network.
func checkBoundedFinancialRollback(t *testing.T, a *App, key *ecdsa.PrivateKey, vals *cmttypes.ValidatorSet, start time.Time) {
	t.Helper()
	_, err := a.Commit()
	require.NoError(t, err)
	fixture, err := upstreamcontracts.LoadStakingReverterContract()
	require.NoError(t, err)
	from := crypto.PubkeyToAddress(key.PublicKey)
	price := big.NewInt(1_000_000_000_000)
	ctx := func() sdk.Context {
		return a.NewContextLegacy(false, cmtproto.Header{ChainID: a.chainID, Height: a.LastBlockHeight() + 1, Time: start.Add(time.Duration(a.LastBlockHeight()+1) * time.Second)})
	}
	send := func(to *common.Address, value *big.Int, input []byte, success bool) (*evmtypes.MsgEthereumTxResponse, *abci.ExecTxResult) {
		height := a.LastBlockHeight() + 1
		header := cmtproto.Header{ChainID: a.chainID, Height: height, Time: start.Add(time.Duration(height) * time.Second)}
		current := a.NewNextBlockContext(header)
		before := a.BankKeeper.GetBalance(current, sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount
		raw := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: a.EVMKeeper.GetNonce(current, from), To: to, Value: value, Gas: 3_000_000, GasPrice: price, Data: input})
		signer := ethtypes.NewEIP155Signer(new(big.Int).SetUint64(a.evmChainID))
		signed, signErr := ethtypes.SignTx(raw, signer, key)
		require.NoError(t, signErr)
		msg := &evmtypes.MsgEthereumTx{}
		require.NoError(t, msg.FromSignedEthereumTx(signed, signer))
		tx, buildErr := msg.BuildTx(a.TxConfig().NewTxBuilder(), identity.BaseDenom)
		require.NoError(t, buildErr)
		bytes, encodeErr := a.TxConfig().TxEncoder()(tx)
		require.NoError(t, encodeErr)
		block, finalizeErr := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: header.Time, Txs: [][]byte{bytes}, NextValidatorsHash: vals.Hash(), ProposerAddress: vals.Proposer.Address})
		require.NoError(t, finalizeErr)
		require.Len(t, block.TxResults, 1)
		result := block.TxResults[0]
		require.Zero(t, result.Code, result.Log)
		var data sdk.TxMsgData
		require.NoError(t, a.AppCodec().Unmarshal(result.Data, &data))
		require.Len(t, data.MsgResponses, 1)
		response := &evmtypes.MsgEthereumTxResponse{}
		require.NoError(t, response.Unmarshal(data.MsgResponses[0].Value))
		if success {
			reason, _ := abi.UnpackRevert(response.Ret)
			require.Empty(t, response.VmError, "revert reason: %s", reason)
		} else {
			require.NotEmpty(t, response.VmError)
		}
		fee := math.NewIntFromBigInt(new(big.Int).Mul(price, new(big.Int).SetUint64(response.GasUsed)))
		expectedDebit := fee
		if success {
			expectedDebit = expectedDebit.Add(math.NewIntFromBigInt(value))
		}
		require.Equal(t, expectedDebit, before.Sub(a.BankKeeper.GetBalance(ctx(), sdk.AccAddress(from.Bytes()), identity.BaseDenom).Amount), "every outer transaction must pay exactly its receipt gas fee plus committed value")
		_, commitErr := a.Commit()
		require.NoError(t, commitErr)
		// Prepare the next block context for read-only assertions.
		a.NewNextBlockContext(cmtproto.Header{ChainID: a.chainID, Height: a.LastBlockHeight() + 1, Time: start.Add(time.Duration(a.LastBlockHeight()+1) * time.Second)})
		return response, result
	}
	contract := crypto.CreateAddress(from, 1)
	funding := big.NewInt(1_000_000_000_000_000_000)
	send(nil, funding, fixture.Bin, true)
	contractAccount := sdk.AccAddress(contract.Bytes())
	validatorAddress := sdk.ValAddress(vals.Validators[0].Address)
	validatorBefore, err := a.StakingKeeper.GetValidator(ctx(), validatorAddress)
	require.NoError(t, err)
	input, err := fixture.ABI.Pack("callPrecompileBeforeAndAfterRevert", big.NewInt(0), validatorAddress.String())
	require.NoError(t, err)
	control, _ := send(&contract, big.NewInt(0), input, true)
	require.Len(t, control.Logs, 2, "two successful control delegations must really execute")
	delegationBefore, err := a.StakingKeeper.GetDelegation(ctx(), contractAccount, validatorAddress)
	require.NoError(t, err)
	validatorControl, err := a.StakingKeeper.GetValidator(ctx(), validatorAddress)
	require.NoError(t, err)
	require.Equal(t, validatorBefore.Tokens.Add(math.NewInt(20)), validatorControl.Tokens)
	expectedContractBalance := math.NewIntFromBigInt(funding).Sub(math.NewInt(20))
	checkNoFinancialLeak := func() {
		require.Equal(t, expectedContractBalance, a.BankKeeper.GetBalance(ctx(), contractAccount, identity.BaseDenom).Amount)
		require.Equal(t, expectedContractBalance.BigInt(), a.EVMKeeper.GetBalance(ctx(), contract).ToBig())
		delegationAfter, getErr := a.StakingKeeper.GetDelegation(ctx(), contractAccount, validatorAddress)
		require.NoError(t, getErr)
		require.Equal(t, delegationBefore, delegationAfter)
		validatorAfter, getErr := a.StakingKeeper.GetValidator(ctx(), validatorAddress)
		require.NoError(t, getErr)
		require.Equal(t, validatorControl.Tokens, validatorAfter.Tokens)
	}
	checkEvents := func(response *evmtypes.MsgEthereumTxResponse, result *abci.ExecTxResult) {
		require.Empty(t, response.Logs, "reverted staking subcall must not leak EVM financial logs")
		for _, event := range result.Events {
			if event.Type != "delegate" && event.Type != "coin_spent" && event.Type != "coin_received" && event.Type != "transfer" {
				continue
			}
			for _, attribute := range event.Attributes {
				require.NotEqual(t, contractAccount.String(), attribute.Value, "reverted scope leaked SDK financial event")
				require.NotEqual(t, contract.Hex(), attribute.Value, "reverted scope leaked EVM-address financial event")
			}
		}
	}
	input, err = fixture.ABI.Pack("run", big.NewInt(1), validatorAddress.String())
	require.NoError(t, err)
	caught, caughtResult := send(&contract, big.NewInt(0), input, true)
	checkNoFinancialLeak()
	checkEvents(caught, caughtResult)
	require.Equal(t, big.NewInt(1), a.EVMKeeper.GetState(ctx(), contract, common.Hash{}).Big(), "outer contract counter must commit while its caught child is rolled back")
	input, err = fixture.ABI.Pack("performDelegation", validatorAddress.String())
	require.NoError(t, err)
	reverted, revertedResult := send(&contract, big.NewInt(0), input, false)
	checkNoFinancialLeak()
	checkEvents(reverted, revertedResult)
	require.Equal(t, big.NewInt(1), a.EVMKeeper.GetState(ctx(), contract, common.Hash{}).Big())
}
