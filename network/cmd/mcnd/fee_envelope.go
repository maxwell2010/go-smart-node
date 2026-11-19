package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"strings"
	"time"

	storetypes "cosmossdk.io/store/types"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	gethmath "github.com/ethereum/go-ethereum/common/math"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/fees/authorization"
	"github.com/maxwell2010/go-smart-node/network/fees/envelope"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/spf13/cobra"
)

const feeOfflineNotice = "Offline preparation only: no broadcast, signing, live reserve, fee nonce, deadline freshness or block-budget validation."

type feeEnvelopeFlags struct {
	txFile, cosmosID, token, input, minimum, nonce, signatureFile, output string
	evmID, conversionGas, deadline                                        uint64
}

func newFeeEnvelopeCmd() *cobra.Command {
	parent := &cobra.Command{Use: "fee-envelope", Short: "Prepare an offline DRC20 fee authorization or encode its signed envelope"}
	for _, mode := range []string{"payload", "encode"} {
		mode := mode
		f := new(feeEnvelopeFlags)
		cmd := &cobra.Command{Use: mode, Args: cobra.NoArgs, SilenceUsage: true, RunE: func(cmd *cobra.Command, _ []string) error {
			tx, policy, auth, err := readFeeEnvelopeIntent(*f)
			if err != nil {
				return err
			}
			digest, err := authorization.SigningDigest(policy, auth)
			if err != nil {
				return err
			}
			if mode == "payload" {
				result := struct {
					TypedData       apitypes.TypedData `json:"typed_data"`
					Digest          common.Hash        `json:"digest"`
					EthereumTxHash  common.Hash        `json:"ethereum_tx_hash"`
					NativeFeeBudget string             `json:"native_fee_budget"`
					Notice          string             `json:"notice"`
				}{feeEnvelopeTypedData(policy, auth), digest, tx.Hash(), auth.NativeFeeBudget.String(), feeOfflineNotice}
				raw, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return err
				}
				return outputFeeEnvelope(cmd, f.output, append(raw, '\n'))
			}
			sig, err := readFeeHexFile(f.signatureFile, 65)
			if err != nil {
				return fmt.Errorf("fee signature: %w", err)
			}
			// Deliberately use the explicit signed deadline rather than wall clock:
			// offline encode checks signatures, not the live chain's freshness.
			if _, err := authorization.Verify(policy, tx, auth, sig, time.Unix(int64(auth.Deadline), 0), storetypes.NewInfiniteGasMeter()); err != nil {
				return err
			}
			config := ownedapp.MakeEncodingConfig(f.evmID)
			msg := &evmtypes.MsgEthereumTx{}
			if err := msg.FromSignedEthereumTx(tx, ethtypes.LatestSignerForChainID(policy.EVMChainID)); err != nil {
				return err
			}
			inner, err := msg.BuildTx(config.TxConfig.NewTxBuilder(), identity.BaseDenom)
			if err != nil {
				return err
			}
			encoded, err := config.TxConfig.TxEncoder()(inner)
			if err != nil {
				return err
			}
			raw, err := envelope.Marshal(encoded, auth, sig)
			if err != nil {
				return err
			}
			if f.output != "" {
				return outputFeeEnvelope(cmd, f.output, raw)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), base64.StdEncoding.EncodeToString(raw))
			return err
		}}
		cmd.Flags().StringVar(&f.txFile, "tx-file", "", "File containing the original signed Ethereum transaction as 0x hex")
		cmd.Flags().StringVar(&f.cosmosID, "cosmos-chain-id", "", "Separate network Cosmos chain ID")
		cmd.Flags().Uint64Var(&f.evmID, "evm-id", 0, "Matching EVM chain ID")
		cmd.Flags().StringVar(&f.token, "token", "", "Canonical reserve-backed DRC20 address")
		cmd.Flags().StringVar(&f.input, "exact-input", "", "Exact DRC20 atomic amount authorized for sale")
		cmd.Flags().StringVar(&f.minimum, "min-native-output", "", "Minimum native atomic output")
		cmd.Flags().Uint64Var(&f.conversionGas, "conversion-gas", 0, "Signed conversion gas allowance")
		cmd.Flags().Uint64Var(&f.deadline, "deadline", 0, "Explicit Unix deadline in seconds")
		cmd.Flags().StringVar(&f.nonce, "fee-nonce", "", "Explicit dedicated fee nonce")
		cmd.Flags().StringVar(&f.output, "out", "", "Create a new 0600 output file; never overwrite")
		for _, name := range []string{"tx-file", "cosmos-chain-id", "evm-id", "token", "exact-input", "min-native-output", "conversion-gas", "deadline", "fee-nonce"} {
			_ = cmd.MarkFlagRequired(name)
		}
		if mode == "encode" {
			cmd.Flags().StringVar(&f.signatureFile, "fee-signature-file", "", "File with 65-byte EIP-712 signature as 0x hex, v=0/1")
			_ = cmd.MarkFlagRequired("fee-signature-file")
		}
		parent.AddCommand(cmd)
	}
	return parent
}

func feeAtomic(s string, positive bool) (*big.Int, error) {
	if len(s) == 0 || len(s) > 78 || (len(s) > 1 && s[0] == '0') {
		return nil, errors.New("amount must be a canonical uint256 decimal")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return nil, errors.New("amount must be decimal atomic units")
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.BitLen() > 256 || (positive && n.Sign() == 0) {
		return nil, errors.New("amount exceeds uint256 or is zero")
	}
	return n, nil
}

func readFeeHexFile(path string, max int) ([]byte, error) {
	if path == "" {
		return nil, errors.New("input file is required")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(2*max+4) {
		return nil, errors.New("input must be a bounded regular hex file")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(2*max+5)))
	if err != nil {
		return nil, err
	}
	s := strings.TrimSpace(string(data))
	if !strings.HasPrefix(s, "0x") || len(s)%2 != 0 || len(s) > 2*max+2 {
		return nil, errors.New("input requires bounded 0x-prefixed hex")
	}
	raw, err := hex.DecodeString(s[2:])
	if err != nil || len(raw) == 0 {
		return nil, errors.New("invalid or empty input hex")
	}
	return raw, nil
}

func readFeeEnvelopeIntent(f feeEnvelopeFlags) (*ethtypes.Transaction, authorization.Policy, authorization.Authorization, error) {
	var policy authorization.Policy
	var auth authorization.Authorization
	fail := func(err error) (*ethtypes.Transaction, authorization.Policy, authorization.Authorization, error) {
		return nil, policy, auth, err
	}
	if err := identity.ValidateChainIDs(f.cosmosID, f.evmID); err != nil {
		return fail(err)
	}
	if !common.IsHexAddress(f.token) || common.HexToAddress(f.token) == (common.Address{}) {
		return fail(errors.New("token must be a nonzero EVM address"))
	}
	if f.conversionGas <= ownedapp.DRC20FeeSettlementGas || f.conversionGas > ownedapp.DRC20FeeMaxConversionGas {
		return fail(errors.New("conversion gas is outside the supported range"))
	}
	raw, err := readFeeHexFile(f.txFile, authorization.MaxSignedTransactionBytes)
	if err != nil {
		return fail(err)
	}
	tx := new(ethtypes.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return fail(err)
	}
	if !tx.Protected() || tx.ChainId().Cmp(new(big.Int).SetUint64(f.evmID)) != 0 {
		return fail(errors.New("Ethereum transaction must be protected and signed for the configured EVM chain"))
	}
	if tx.Type() != ethtypes.LegacyTxType && tx.Type() != ethtypes.AccessListTxType && tx.Type() != ethtypes.DynamicFeeTxType {
		return fail(errors.New("unsupported Ethereum transaction type"))
	}
	if tx.Gas() == 0 || tx.GasFeeCap().Sign() <= 0 {
		return fail(errors.New("transaction requires positive gas limit and fee cap"))
	}
	if tx.Gas() > math.MaxUint64-f.conversionGas || len(tx.AccessList()) > authorization.MaxAccessListEntries {
		return fail(errors.New("transaction gas or access-list bound exceeded"))
	}
	keys := 0
	for _, entry := range tx.AccessList() {
		if len(entry.StorageKeys) > authorization.MaxAccessListKeys-keys {
			return fail(errors.New("transaction access-list key bound exceeded"))
		}
		keys += len(entry.StorageKeys)
	}
	payer, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return fail(err)
	}
	input, err := feeAtomic(f.input, true)
	if err != nil {
		return fail(err)
	}
	minimum, err := feeAtomic(f.minimum, true)
	if err != nil {
		return fail(err)
	}
	nonce, err := feeAtomic(f.nonce, false)
	if err != nil {
		return fail(err)
	}
	conversionFee := new(big.Int).Mul(new(big.Int).SetUint64(f.conversionGas), tx.GasFeeCap())
	budget := new(big.Int).Add(new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasFeeCap()), conversionFee)
	policy = authorization.Policy{CosmosChainID: f.cosmosID, EVMChainID: tx.ChainId(), VerifyingModule: ownedapp.DRC20FeeModuleAddress(), MaxConversionGasLimit: ownedapp.DRC20FeeMaxConversionGas, ConversionNativeFee: conversionFee}
	auth = authorization.Authorization{TxHash: tx.Hash(), Payer: payer, Token: common.HexToAddress(f.token), ExactInput: input, MinimumNativeOutput: minimum, NativeFeeBudget: budget, ConversionGasLimit: f.conversionGas, Deadline: f.deadline, FeeNonce: nonce}
	if _, err := authorization.SigningDigest(policy, auth); err != nil {
		return fail(err)
	}
	return tx, policy, auth, nil
}

func feeEnvelopeTypedData(p authorization.Policy, a authorization.Authorization) apitypes.TypedData {
	chain := gethmath.HexOrDecimal256(*new(big.Int).Set(p.EVMChainID))
	return apitypes.TypedData{Types: apitypes.Types{
		"EIP712Domain":     {{Name: "name", Type: "string"}, {Name: "version", Type: "string"}, {Name: "chainId", Type: "uint256"}, {Name: "verifyingContract", Type: "address"}, {Name: "salt", Type: "bytes32"}},
		"FeeAuthorization": {{Name: "txHash", Type: "bytes32"}, {Name: "payer", Type: "address"}, {Name: "token", Type: "address"}, {Name: "exactInput", Type: "uint256"}, {Name: "minimumNativeOutput", Type: "uint256"}, {Name: "nativeFeeBudget", Type: "uint256"}, {Name: "conversionGasLimit", Type: "uint256"}, {Name: "deadline", Type: "uint256"}, {Name: "feeNonce", Type: "uint256"}}}, PrimaryType: "FeeAuthorization",
		Domain:  apitypes.TypedDataDomain{Name: authorization.DomainName, Version: authorization.DomainVersion, ChainId: &chain, VerifyingContract: p.VerifyingModule.Hex(), Salt: crypto.Keccak256Hash([]byte(p.CosmosChainID)).Hex()},
		Message: apitypes.TypedDataMessage{"txHash": a.TxHash.Hex(), "payer": a.Payer.Hex(), "token": a.Token.Hex(), "exactInput": a.ExactInput.String(), "minimumNativeOutput": a.MinimumNativeOutput.String(), "nativeFeeBudget": a.NativeFeeBudget.String(), "conversionGasLimit": new(big.Int).SetUint64(a.ConversionGasLimit).String(), "deadline": new(big.Int).SetUint64(a.Deadline).String(), "feeNonce": a.FeeNonce.String()}}
}

func outputFeeEnvelope(cmd *cobra.Command, path string, data []byte) error {
	if path == "" {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
