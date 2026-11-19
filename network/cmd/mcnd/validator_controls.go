package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/evm/crypto/hd"
	evmtypes "github.com/cosmos/evm/x/vm/types"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	contracts "github.com/maxwell2010/go-smart-node/network/systemcontracts"
	"github.com/spf13/cobra"
	"io"
	"math/big"
	"os"
	"strings"
)

const validatorControlNotice = "Offline preparation/signing only. Verify Master address, chain, nonce and fees independently; broadcast is a separate explicit command."

type validatorControlIntent struct {
	Version     int    `json:"version"`
	ChainID     string `json:"chain_id"`
	EVMChainID  uint64 `json:"evm_chain_id"`
	Operator    string `json:"operator"`
	Master      string `json:"master"`
	Action      string `json:"action"`
	UnsignedTx  string `json:"unsigned_tx"`
	SigningHash string `json:"signing_hash"`
	Notice      string `json:"notice"`
}

func validatorControlAddress(value string) (common.Address, error) {
	if !common.IsHexAddress(value) || len(value) != 42 || common.HexToAddress(value) == (common.Address{}) {
		return common.Address{}, errors.New("a nonzero 20-byte EVM address is required")
	}
	return common.HexToAddress(value), nil
}

func buildValidatorControlIntent(chain string, evm uint64, operator, master, action string, nonce, gas uint64, price string, metadata []byte) (validatorControlIntent, error) {
	var intent validatorControlIntent
	if err := identity.ValidateChainIDs(chain, evm); err != nil {
		return intent, err
	}
	op, err := validatorControlAddress(operator)
	if err != nil {
		return intent, err
	}
	target, err := validatorControlAddress(master)
	if err != nil {
		return intent, err
	}
	fee, ok := new(big.Int).SetString(price, 10)
	if !ok || fee.Sign() <= 0 || fee.BitLen() > 256 || gas < 21000 || gas > 24000000 {
		return intent, errors.New("invalid finite gas limit or gas price")
	}
	masterABI, err := contracts.CompiledABI("NewNetworkMasterValidator")
	if err != nil {
		return intent, err
	}
	var data []byte
	switch action {
	case "enable":
		data, err = masterABI.Pack("unpauseSelf")
	case "disable":
		data, err = masterABI.Pack("pauseSelf")
	case "metadata":
		canonical, e := ownedapp.CanonicalValidatorControlMetadata(metadata)
		if e != nil {
			return intent, e
		}
		data, err = masterABI.Pack("updateValidatorMeta", op, string(canonical))
	default:
		return intent, errors.New("action must be enable, disable or metadata")
	}
	if err != nil {
		return intent, err
	}
	tx := ethtypes.NewTx(&ethtypes.LegacyTx{Nonce: nonce, To: &target, Value: new(big.Int), Gas: gas, GasPrice: fee, Data: data})
	raw, err := tx.MarshalBinary()
	if err != nil {
		return intent, err
	}
	digest := ethtypes.NewEIP155Signer(new(big.Int).SetUint64(evm)).Hash(tx)
	return validatorControlIntent{1, chain, evm, op.Hex(), target.Hex(), action, "0x" + hex.EncodeToString(raw), digest.Hex(), validatorControlNotice}, nil
}

func parseValidatorControlIntent(raw []byte) (validatorControlIntent, *ethtypes.Transaction, error) {
	var value validatorControlIntent
	if len(raw) == 0 || len(raw) > 32768 {
		return value, nil, errors.New("invalid control intent size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, nil, err
	}
	var tail interface{}
	if decoder.Decode(&tail) != io.EOF {
		return value, nil, errors.New("trailing intent data")
	}
	if value.Version != 1 || value.Notice != validatorControlNotice {
		return value, nil, errors.New("unsupported intent version")
	}
	rawTx, err := hex.DecodeString(strings.TrimPrefix(value.UnsignedTx, "0x"))
	if err != nil {
		return value, nil, err
	}
	tx := new(ethtypes.Transaction)
	if err := tx.UnmarshalBinary(rawTx); err != nil {
		return value, nil, err
	}
	if tx.Type() != ethtypes.LegacyTxType || tx.Value().Sign() != 0 || tx.To() == nil {
		return value, nil, errors.New("control intent must be a zero-value legacy Master call")
	}
	var metadata []byte
	if value.Action == "metadata" {
		masterABI, err := contracts.CompiledABI("NewNetworkMasterValidator")
		if err != nil {
			return value, nil, err
		}
		method := masterABI.Methods["updateValidatorMeta"]
		if len(tx.Data()) < 4 || !bytes.Equal(method.ID, tx.Data()[:4]) {
			return value, nil, errors.New("metadata selector differs")
		}
		args, err := method.Inputs.Unpack(tx.Data()[4:])
		if err != nil {
			return value, nil, err
		}
		op, err := validatorControlAddress(value.Operator)
		if err != nil {
			return value, nil, err
		}
		if args[0].(common.Address) != op {
			return value, nil, errors.New("metadata operator differs")
		}
		metadata = []byte(args[1].(string))
	}
	expected, err := buildValidatorControlIntent(value.ChainID, value.EVMChainID, value.Operator, value.Master, value.Action, tx.Nonce(), tx.Gas(), tx.GasPrice().String(), metadata)
	if err != nil {
		return value, nil, err
	}
	if expected != value {
		return value, nil, errors.New("intent identity, calldata or signing hash differs")
	}
	return value, tx, nil
}

func signValidatorControlIntent(intent validatorControlIntent, tx *ethtypes.Transaction, sig []byte) (*ethtypes.Transaction, error) {
	if len(sig) != 65 {
		return nil, errors.New("65-byte Ethereum recoverable signature required")
	}
	signer := ethtypes.NewEIP155Signer(new(big.Int).SetUint64(intent.EVMChainID))
	digest := signer.Hash(tx)
	public, err := crypto.SigToPub(digest[:], sig)
	if err != nil {
		return nil, err
	}
	if crypto.PubkeyToAddress(*public) != common.HexToAddress(intent.Operator) {
		return nil, errors.New("signature is not from this validator operator")
	}
	signed, err := tx.WithSignature(signer, sig)
	if err != nil {
		return nil, err
	}
	sender, err := ethtypes.Sender(signer, signed)
	if err != nil || sender != common.HexToAddress(intent.Operator) {
		return nil, errors.New("signed operator differs")
	}
	return signed, nil
}

func newValidatorControlCmd() *cobra.Command {
	parent := &cobra.Command{Use: "validator-control", Short: "Query your validator or prepare/sign its own Master controls; no automatic broadcast"}
	var chain, op, master, action, price, metaFile, output string
	var evm, nonce, gas uint64
	prepare := &cobra.Command{Use: "prepare", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var metadata []byte
		var err error
		if metaFile != "" {
			metadata, err = readValidatorControlFile(metaFile, 2048)
			if err != nil {
				return err
			}
			if len(metadata) > 2048 {
				return errors.New("metadata file exceeds limit")
			}
		}
		intent, err := buildValidatorControlIntent(chain, evm, op, master, action, nonce, gas, price, metadata)
		if err != nil {
			return err
		}
		payload, err := json.MarshalIndent(intent, "", "  ")
		if err != nil {
			return err
		}
		return outputFeeEnvelope(cmd, output, append(payload, '\n'))
	}}
	f := prepare.Flags()
	f.StringVar(&chain, "chain-id", "", "Exact Cosmos chain ID")
	f.Uint64Var(&evm, "evm-chain-id", 0, "Exact EVM chain ID")
	f.StringVar(&op, "operator", "", "Your operator EVM address")
	f.StringVar(&master, "master", "", "Approved MasterValidator address")
	f.StringVar(&action, "action", "", "enable, disable or metadata")
	f.Uint64Var(&nonce, "nonce", 0, "Explicit operator EVM nonce")
	f.Uint64Var(&gas, "gas", 250000, "Finite EVM gas limit")
	f.StringVar(&price, "gas-price", "", "Price in native atoms per gas")
	f.StringVar(&metaFile, "metadata-file", "", "Local JSON metadata patch; no network download")
	f.StringVar(&output, "output-file", "", "Exclusive new output file; default stdout")
	for _, name := range []string{"chain-id", "evm-chain-id", "operator", "master", "action", "nonce", "gas-price"} {
		_ = prepare.MarkFlagRequired(name)
	}
	var intentFile, signatureFile, signedOutput string
	sign := &cobra.Command{Use: "sign", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		raw, err := readValidatorControlFile(intentFile, 32768)
		if err != nil {
			return err
		}
		intent, tx, err := parseValidatorControlIntent(raw)
		if err != nil {
			return err
		}
		var signature []byte
		if signatureFile != "" {
			signature, err = readFeeHexFile(signatureFile, 65)
		} else {
			if err := validatorControlClientContext(cmd, intent.EVMChainID, intent.ChainID); err != nil {
				return err
			}
			ctx, e := client.GetClientTxContext(cmd)
			if e != nil {
				return e
			}
			if ctx.FromName == "" || ctx.Keyring == nil {
				return errors.New("choose --from keyring name or --signature-file")
			}
			digest := ethtypes.NewEIP155Signer(new(big.Int).SetUint64(intent.EVMChainID)).Hash(tx)
			signature, key, e := ctx.Keyring.Sign(ctx.FromName, digest[:], signing.SignMode_SIGN_MODE_DIRECT)
			if e != nil {
				return e
			}
			if key.Type() != "eth_secp256k1" {
				return errors.New("Ethereum secp256k1 key required")
			}
			signed, err := signValidatorControlIntent(intent, tx, signature)
			if err != nil {
				return err
			}
			return emitValidatorControlSigned(cmd, signedOutput, intent, signed)
		}
		if err != nil {
			return err
		}
		signed, err := signValidatorControlIntent(intent, tx, signature)
		if err != nil {
			return err
		}
		return emitValidatorControlSigned(cmd, signedOutput, intent, signed)
	}}
	sign.Flags().StringVar(&intentFile, "intent-file", "", "Prepared offline JSON intent")
	sign.Flags().StringVar(&signatureFile, "signature-file", "", "External recoverable Ethereum signature hex")
	sign.Flags().StringVar(&signedOutput, "output-file", "", "Exclusive new signed output file")
	flags.AddTxFlagsToCmd(sign)
	_ = sign.MarkFlagRequired("intent-file")
	status := &cobra.Command{Use: "status [operator EVM address]", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		operator, err := validatorControlAddress(args[0])
		if err != nil {
			return err
		}
		if err := validatorControlClientContext(cmd, 990001, ""); err != nil {
			return err
		}
		ctx, err := client.GetClientQueryContext(cmd)
		if err != nil {
			return err
		}
		avatar, height, err := ctx.QueryWithData("/store/"+validatortypes.StoreKey+"/key", append([]byte{0x82}, operator[:]...))
		if err != nil {
			return err
		}
		name, err := address.NewBech32Codec(identity.Bech32PrefixValAddr).BytesToString(operator[:])
		if err != nil {
			return err
		}
		response, err := validatortypes.NewQueryClient(ctx.WithHeight(height)).Validator(cmd.Context(), &validatortypes.QueryValidatorRequest{Validator: name})
		if err != nil {
			return err
		}
		if response == nil {
			return errors.New("empty validator query response")
		}
		payload, err := marshalValidatorControlStatus(ctx.Codec, height, response.Validator, string(avatar))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(payload))
		return err
	}}
	flags.AddQueryFlagsToCmd(status)
	parent.AddCommand(prepare, sign, status)
	return parent
}

func marshalValidatorControlStatus(cdc codec.Codec, height int64, validator validatortypes.Validator, avatar string) ([]byte, error) {
	if cdc == nil || len(avatar) > 512 {
		return nil, errors.New("invalid validator status codec or avatar bound")
	}
	encoded, err := cdc.MarshalJSON(&validator)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(struct {
		Height    int64           `json:"height"`
		Validator json.RawMessage `json:"validator"`
		Avatar    string          `json:"avatar_uri"`
	}{height, encoded, avatar}, "", "  ")
}

// Only client flags/keyring are loaded. This command never intercepts server
// configs, starts a daemon or broadcasts; prepare requires no client context.
func validatorControlClientContext(cmd *cobra.Command, evm uint64, chain string) error {
	enc := ownedapp.MakeEncodingConfig(evm)
	ctx := client.GetClientContextFromCmd(cmd)
	ctx = ctx.WithCodec(enc.Codec).WithInterfaceRegistry(enc.InterfaceRegistry).WithTxConfig(enc.TxConfig).WithLegacyAmino(enc.Amino).
		WithInput(cmd.InOrStdin()).WithOutput(cmd.OutOrStdout()).WithCmdContext(cmd.Context()).
		WithAccountRetriever(authtypes.AccountRetriever{}).WithKeyringOptions(hd.EthSecp256k1Option())
	if ctx.HomeDir == "" {
		ctx = ctx.WithHomeDir(defaultNodeHome())
	}
	if selected, _ := cmd.Flags().GetString(flags.FlagChainID); chain != "" && selected != "" && selected != chain {
		return errors.New("keyring command chain differs from signed intent")
	}
	if chain != "" {
		ctx = ctx.WithChainID(chain)
	}
	return client.SetCmdClientContextHandler(ctx, cmd)
}

func readValidatorControlFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("validator control input must be a bounded regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("validator control input exceeds limit")
	}
	return raw, nil
}

func emitValidatorControlSigned(cmd *cobra.Command, path string, intent validatorControlIntent, tx *ethtypes.Transaction) error {
	raw, err := tx.MarshalBinary()
	if err != nil {
		return err
	}
	encoding := ownedapp.MakeEncodingConfig(intent.EVMChainID)
	msg := &evmtypes.MsgEthereumTx{}
	if err := msg.FromSignedEthereumTx(tx, ethtypes.NewEIP155Signer(new(big.Int).SetUint64(intent.EVMChainID))); err != nil {
		return err
	}
	cosmos, err := msg.BuildTxWithEvmParams(encoding.TxConfig.NewTxBuilder(), evmtypes.Params{EvmDenom: identity.BaseDenom, ExtendedDenomOptions: &evmtypes.ExtendedDenomOptions{ExtendedDenom: identity.BaseDenom}})
	if err != nil {
		return err
	}
	encoded, err := encoding.TxConfig.TxEncoder()(cosmos)
	if err != nil {
		return err
	}
	encodedJSON, err := encoding.TxConfig.TxJSONEncoder()(cosmos)
	if err != nil {
		return err
	}
	result := map[string]interface{}{"operator": intent.Operator, "ethereum_tx_hash": tx.Hash().Hex(), "raw_ethereum_tx": "0x" + hex.EncodeToString(raw), "cosmos_tx_base64": base64.StdEncoding.EncodeToString(encoded), "cosmos_tx_json": json.RawMessage(encodedJSON), "notice": validatorControlNotice}
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode signed control: %w", err)
	}
	return outputFeeEnvelope(cmd, path, append(payload, '\n'))
}
