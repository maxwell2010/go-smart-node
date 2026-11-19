package types

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	abci "github.com/cometbft/cometbft/abci/types"

	evmtypes "github.com/cosmos/evm/x/vm/types"
)

func feeResultFixture() *abci.ExecTxResult {
	hash := common.BigToHash(big.NewInt(1)).Hex()
	return &abci.ExecTxResult{GasUsed: 521000, Events: []abci.Event{
		{Type: "mintcandy_drc20_fee", Attributes: []abci.EventAttribute{
			{Key: "payer", Value: "0x1111111111111111111111111111111111111111"},
			{Key: "token", Value: "0x2222222222222222222222222222222222222222"},
			{Key: "transaction", Value: hash}, {Key: "fee_nonce", Value: "0"},
			{Key: "token_input", Value: "1000000000000000000"}, {Key: "native_output", Value: "1000000000000000000"},
			{Key: "conversion_fee", Value: "500000000000000"}, {Key: "conversion_gas", Value: "500000"},
		}},
		{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
			{Key: "ethereumTxHash", Value: hash}, {Key: "txIndex", Value: "0"},
			{Key: "txGasUsed", Value: "21000"}, {Key: "amount", Value: "0"},
		}},
	}}
}

func TestMintCandyReserveFeeKeepsEthereumGasSeparateFromSDK(t *testing.T) {
	for _, reverted := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverted=%t", reverted), func(t *testing.T) {
			result := feeResultFixture()
			if reverted {
				result.Events[1].Attributes = append(result.Events[1].Attributes, abci.EventAttribute{Key: "ethereumTxFailed", Value: "execution reverted"})
			}
			parsed, err := ParseTxResult(result, nil)
			require.NoError(t, err)
			require.Len(t, parsed.Txs, 1)
			require.EqualValues(t, 21000, parsed.Txs[0].GasUsed)
			require.Equal(t, reverted, parsed.Txs[0].Failed)
			require.EqualValues(t, 521000, result.GasUsed, "header/block accounting must retain conversion work exactly once")
			fee, err := ParseMintCandyFeeEvent(result)
			require.NoError(t, err)
			require.EqualValues(t, 500000, fee.ConversionGas)
			require.Equal(t, "500000000000000", fee.ConversionFee)
			result.Events = result.Events[1:]
			ordinary, err := ParseTxResult(result, nil)
			require.NoError(t, err)
			require.EqualValues(t, 521000, ordinary.Txs[0].GasUsed, "preserve old non-fee fallback unchanged")
		})
	}
}

func TestMintCandyReserveFeeMalformedMetadataFailsClosed(t *testing.T) {
	for name, change := range map[string]func(*abci.ExecTxResult){
		"duplicate_event":       func(r *abci.ExecTxResult) { r.Events = append(r.Events, r.Events[0]) },
		"duplicate_attribute":   func(r *abci.ExecTxResult) { r.Events[0].Attributes[0] = r.Events[0].Attributes[1] },
		"unknown_attribute":     func(r *abci.ExecTxResult) { r.Events[0].Attributes[0].Key = "unexpected" },
		"wrong_transaction":     func(r *abci.ExecTxResult) { r.Events[0].Attributes[2].Value = common.BigToHash(big.NewInt(2)).Hex() },
		"invalid_transaction":   func(r *abci.ExecTxResult) { r.Events[0].Attributes[2].Value = "garbage" },
		"missing_evm":           func(r *abci.ExecTxResult) { r.Events = r.Events[:1] },
		"negative_sdk_gas":      func(r *abci.ExecTxResult) { r.GasUsed = -1 },
		"excess_conversion_gas": func(r *abci.ExecTxResult) { r.GasUsed = 1 },
		"gas_mismatch":          func(r *abci.ExecTxResult) { r.Events[0].Attributes[7].Value = "500001" },
		"overflow_gas":          func(r *abci.ExecTxResult) { r.Events[0].Attributes[7].Value = "18446744073709551616" },
		"noncanonical_gas":      func(r *abci.ExecTxResult) { r.Events[0].Attributes[7].Value = "0500000" },
		"zero_gas":              func(r *abci.ExecTxResult) { r.Events[0].Attributes[7].Value = "0" },
		"excess_fee":            func(r *abci.ExecTxResult) { r.Events[0].Attributes[6].Value = "2000000000000000000" },
		"zero_payer":            func(r *abci.ExecTxResult) { r.Events[0].Attributes[0].Value = (common.Address{}).Hex() },
	} {
		t.Run(name, func(t *testing.T) {
			r := feeResultFixture()
			change(r)
			_, err := ParseTxResult(r, nil)
			require.Error(t, err)
		})
	}
}

func TestParseTxResult(t *testing.T) {
	address := "0x57f96e6B86CdeFdB3d412547816a82E3E0EbF9D2"
	txHash := common.BigToHash(big.NewInt(1))
	txHash2 := common.BigToHash(big.NewInt(2))

	testCases := []struct {
		name     string
		response abci.ExecTxResult
		expTxs   []*ParsedTx // expected parse result, nil means expect error.
	}{
		{
			"format 1 events",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: "coin_received", Attributes: []abci.EventAttribute{
						{Key: "receiver", Value: "ethm12luku6uxehhak02py4rcz65zu0swh7wjun6msa"},
						{Key: "amount", Value: "1252860basetcro"},
					}},
					{Type: "coin_spent", Attributes: []abci.EventAttribute{
						{Key: "spender", Value: "ethm17xpfvakm2amg962yls6f84z3kell8c5lthdzgl"},
						{Key: "amount", Value: "1252860basetcro"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "10"},
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "21000"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
					{Type: "message", Attributes: []abci.EventAttribute{
						{Key: "action", Value: "/ehermint.evm.v1.MsgEthereumTx"},
						{Key: "key", Value: "ethm17xpfvakm2amg962yls6f84z3kell8c5lthdzgl"},
						{Key: "module", Value: "evm"},
						{Key: "sender", Value: address},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash2.Hex()},
						{Key: "txIndex", Value: "11"},
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "21000"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
						{Key: "ethereumTxFailed", Value: "contract everted"},
					}},
				},
			},
			[]*ParsedTx{
				{
					MsgIndex:   0,
					Hash:       txHash,
					EthTxIndex: 10,
					GasUsed:    21000,
					Failed:     false,
				},
				{
					MsgIndex:   1,
					Hash:       txHash2,
					EthTxIndex: 11,
					GasUsed:    21000,
					Failed:     true,
				},
			},
		},
		{
			"format 2 events",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: "coin_received", Attributes: []abci.EventAttribute{
						{Key: "receiver", Value: "ethm12luku6uxehhak02py4rcz65zu0swh7wjun6msa"},
						{Key: "amount", Value: "1252860basetcro"},
					}},
					{Type: "coin_spent", Attributes: []abci.EventAttribute{
						{Key: "spender", Value: "ethm17xpfvakm2amg962yls6f84z3kell8c5lthdzgl"},
						{Key: "amount", Value: "1252860basetcro"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "0"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "amount", Value: "1000"},
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "0"},
						{Key: "txGasUsed", Value: "21000"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
					{Type: "message", Attributes: []abci.EventAttribute{
						{Key: "action", Value: "/ehermint.evm.v1.MsgEthereumTx"},
						{Key: "key", Value: "ethm17xpfvakm2amg962yls6f84z3kell8c5lthdzgl"},
						{Key: "module", Value: "evm"},
						{Key: "sender", Value: address},
					}},
				},
			},
			[]*ParsedTx{
				{
					MsgIndex:   0,
					Hash:       txHash,
					EthTxIndex: 0,
					GasUsed:    21000,
					Failed:     false,
				},
			},
		},
		{
			"format 1 events, failed",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "10"},
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "21000"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash2.Hex()},
						{Key: "txIndex", Value: "10"},
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "0x01"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
						{Key: "ethereumTxFailed", Value: "contract everted"},
					}},
				},
			},
			nil,
		},
		{
			"format 2 events failed",
			abci.ExecTxResult{
				GasUsed: 21000,
				Events: []abci.Event{
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "ethereumTxHash", Value: txHash.Hex()},
						{Key: "txIndex", Value: "10"},
					}},
					{Type: evmtypes.EventTypeEthereumTx, Attributes: []abci.EventAttribute{
						{Key: "amount", Value: "1000"},
						{Key: "txGasUsed", Value: "0x01"},
						{Key: "txHash", Value: "14A84ED06282645EFBF080E0B7ED80D8D8D6A36337668A12B5F229F81CDD3F57"},
						{Key: "recipient", Value: "0x775b87ef5D82ca211811C1a02CE0fE0CA3a455d7"},
					}},
				},
			},
			nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseTxResult(&tc.response, nil) //#nosec G601 -- fine for tests
			if tc.expTxs == nil {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				for msgIndex, expTx := range tc.expTxs {
					require.Equal(t, expTx, parsed.GetTxByMsgIndex(msgIndex))
					require.Equal(t, expTx, parsed.GetTxByHash(expTx.Hash))
					require.Equal(t, expTx, parsed.GetTxByTxIndex(int(expTx.EthTxIndex)))
				}
				// non-exists tx hash
				require.Nil(t, parsed.GetTxByHash(common.Hash{}))
				// out of range
				require.Nil(t, parsed.GetTxByMsgIndex(len(tc.expTxs)))
				require.Nil(t, parsed.GetTxByTxIndex(99999999))
			}
		})
	}
}
