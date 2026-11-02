//go:build experimental_ibc

package app

import (
	"math/big"
	"os"
	"testing"

	cmdcfg "bitbucket.org/decimalteam/go-smart-node/cmd/config"
	"bitbucket.org/decimalteam/go-smart-node/contracts"
	validatorkeeper "bitbucket.org/decimalteam/go-smart-node/x/validator/keeper"
	ibctesting "github.com/cosmos/ibc-go/v6/testing"
	evmtypes "github.com/decimalteam/ethermint/x/evm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// This regression records the bootstrap limitation rather than installing mock
// system contracts. The opt-in release gate requires a usable fresh EVM network.
func TestExperimentalFreshGenesisEVMBootstrap(t *testing.T) {
	c := &ibctesting.Coordinator{T: t, CurrentTime: reliabilityTime, Chains: map[string]*ibctesting.TestChain{}}
	chain := newDSCChainAccount(t, c, "decimal_900001-1", true)
	dsc := chain.App.(dscIBCApp)
	old := validatorkeeper.ReliabilityFixActivationHeights
	validatorkeeper.ReliabilityFixActivationHeights = map[string]int64{chain.ChainID: 1}
	t.Cleanup(func() { validatorkeeper.ReliabilityFixActivationHeights = old })
	ctx := chain.GetContext()
	center := common.HexToAddress(contracts.GetContractCenter(chain.ChainID))
	require.Nil(t, dsc.EvmKeeper.GetAccount(ctx, center))
	_, lookupErr := contracts.GetAddressFromContractCenter(ctx, &dsc.EvmKeeper, contracts.NameOfSlugForGetAddressMasterValidator)
	require.Error(t, lookupErr, "fresh genesis must not contain fabricated system contracts")
	key, err := crypto.ToECDSA(chain.SenderPrivKey.Bytes())
	require.NoError(t, err)
	from := crypto.PubkeyToAddress(key.PublicKey)
	to := common.HexToAddress("0x1234567890123456789012345678901234567890")
	before := dsc.EvmKeeper.GetBalance(ctx, from)
	receiverBefore := dsc.EvmKeeper.GetBalance(ctx, to)
	gasPrice := dsc.FeeKeeper.GetParams(ctx).MinGasPrice.Ceil().TruncateInt().BigInt()
	gasPrice.Add(gasPrice, big.NewInt(1))
	raw := ethtypes.NewTransaction(dsc.EvmKeeper.GetNonce(ctx, from), to, big.NewInt(100), 100000, gasPrice, nil)
	signed, err := ethtypes.SignTx(raw, ethtypes.NewEIP155Signer(big.NewInt(900001)), key)
	require.NoError(t, err)
	msg := &evmtypes.MsgEthereumTx{}
	require.NoError(t, msg.FromEthereumTx(signed))
	tx, err := msg.BuildTx(chain.TxConfig.NewTxBuilder(), cmdcfg.BaseDenom)
	require.NoError(t, err)
	gas, result, err := dsc.BaseApp.SimDeliver(chain.TxConfig.TxEncoder(), tx)
	require.NoError(t, err, "the real Ethereum signature and outer ante must succeed")
	require.Positive(t, gas.GasUsed)
	require.Len(t, result.MsgResponses, 1)
	response := &evmtypes.MsgEthereumTxResponse{}
	require.NoError(t, response.Unmarshal(result.MsgResponses[0].Value))
	after := dsc.EvmKeeper.GetBalance(chain.GetContext(), from)
	require.Positive(t, new(big.Int).Sub(before, after).Sign(), "failed EVM execution must still pay the real native fee")
	require.Equal(t, receiverBefore, dsc.EvmKeeper.GetBalance(chain.GetContext(), to), "failed hook must roll back the transfer")
	t.Logf("fresh genesis registry lookup: %v; signed transfer VmError=%q PostTxError=%q gas=%d fee debit=%s", lookupErr, response.VmError, response.PostTxError, gas.GasUsed, new(big.Int).Sub(before, after))
	if os.Getenv("DSC_REQUIRE_FRESH_EVM_BOOTSTRAP") == "1" {
		require.Empty(t, response.VmError, "release blocked: deploy and authorize the real system contracts before enabling strict EVM hooks")
	} else {
		require.Equal(t, evmtypes.ErrPostTxProcessing.Error(), response.VmError)
	}
}
