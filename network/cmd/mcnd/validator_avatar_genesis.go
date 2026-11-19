package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec/address"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	validatortypes "github.com/maxwell2010/go-smart-node/network/decimal/validator/types"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/spf13/cobra"
)

const avatarTransferFormat = "mintcandy-validator-avatars-v1"
const maxAvatarTransferOperators = 4096

type validatorAvatarTransfer struct {
	Format          string                           `json:"format"`
	MetadataOnly    bool                             `json:"metadata_only"`
	ChainID         string                           `json:"chain_id"`
	EVMChainID      uint64                           `json:"evm_chain_id"`
	Height          int64                            `json:"height"`
	SourceBlockHash string                           `json:"source_block_hash"`
	Operators       []string                         `json:"operators"`
	Avatars         []validatortypes.ValidatorAvatar `json:"validator_avatars"`
}

type avatarGenesisDocument struct {
	document map[string]json.RawMessage
	state    map[string]json.RawMessage
	native   map[string]json.RawMessage
	chainID  string
	module   validatortypes.GenesisState
}

func validateAvatarTransferJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return errors.New("avatar transfer JSON must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errors.New("avatar transfer JSON nesting exceeds limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate avatar transfer JSON field")
				}
				seen[name] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("invalid avatar JSON object")
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid avatar JSON array")
			}
		default:
			return errors.New("invalid avatar JSON delimiter")
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing avatar JSON data")
	}
	return nil
}

func readAvatarGenesis(raw []byte, evmID uint64) (avatarGenesisDocument, error) {
	var doc avatarGenesisDocument
	if err := validateAvatarTransferJSON(raw); err != nil {
		return doc, err
	}
	if err := json.Unmarshal(raw, &doc.document); err != nil {
		return doc, err
	}
	if err := json.Unmarshal(doc.document["chain_id"], &doc.chainID); err != nil {
		return doc, err
	}
	if err := identity.ValidateChainIDs(doc.chainID, evmID); err != nil {
		return doc, err
	}
	if err := json.Unmarshal(doc.document["app_state"], &doc.state); err != nil {
		return doc, err
	}
	if err := json.Unmarshal(doc.state[validatortypes.ModuleName], &doc.native); err != nil {
		return doc, err
	}
	cdc := ownedapp.MakeEncodingConfig(evmID).Codec
	if err := cdc.UnmarshalJSON(doc.state[validatortypes.ModuleName], &doc.module); err != nil {
		return doc, err
	}
	if err := doc.module.Validate(); err != nil {
		return doc, fmt.Errorf("invalid target validator genesis: %w", err)
	}
	if len(doc.module.Validators) == 0 || len(doc.module.Validators) > maxAvatarTransferOperators {
		return doc, errors.New("avatar transfer requires 1..4096 genesis validators")
	}
	return doc, nil
}

func validateAvatarTransfer(transfer validatorAvatarTransfer) error {
	if transfer.Format != avatarTransferFormat || !transfer.MetadataOnly || transfer.Height <= 0 ||
		len(transfer.Operators) == 0 || len(transfer.Operators) > maxAvatarTransferOperators || len(transfer.Avatars) > len(transfer.Operators) {
		return errors.New("invalid bounded metadata-only avatar artifact")
	}
	if err := identity.ValidateChainIDs(transfer.ChainID, transfer.EVMChainID); err != nil {
		return err
	}
	// A block hash is provenance for a trusted RPC snapshot, not a state proof.
	if len(transfer.SourceBlockHash) != 64 {
		return errors.New("avatar artifact requires a source block hash")
	}
	for _, r := range transfer.SourceBlockHash {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return errors.New("invalid source block hash")
		}
	}
	codec := address.NewBech32Codec(identity.Bech32PrefixValAddr)
	operators := make(map[string]bool, len(transfer.Operators))
	for _, operator := range transfer.Operators {
		raw, err := codec.StringToBytes(operator)
		if err != nil || len(raw) != 20 || operators[operator] {
			return errors.New("invalid or duplicate avatar operator")
		}
		canonical, err := codec.BytesToString(raw)
		if err != nil || canonical != operator {
			return errors.New("noncanonical avatar operator")
		}
		operators[operator] = true
	}
	seen := make(map[string]bool, len(transfer.Avatars))
	for _, avatar := range transfer.Avatars {
		if !operators[avatar.OperatorAddress] || seen[avatar.OperatorAddress] || avatar.AvatarURI == "" {
			return errors.New("avatar must name one included operator and a nonempty URI")
		}
		if err := validatortypes.ValidateValidatorAvatarURI(avatar.AvatarURI); err != nil {
			return err
		}
		seen[avatar.OperatorAddress] = true
	}
	return nil
}

func importValidatorAvatars(genesis, artifact []byte, evmID uint64) ([]byte, error) {
	doc, err := readAvatarGenesis(genesis, evmID)
	if err != nil {
		return nil, err
	}
	var transfer validatorAvatarTransfer
	if err := validateAvatarTransferJSON(artifact); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(artifact))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&transfer); err != nil {
		return nil, err
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, errors.New("trailing avatar artifact data")
	}
	if err := validateAvatarTransfer(transfer); err != nil {
		return nil, err
	}
	if transfer.ChainID != doc.chainID || transfer.EVMChainID != evmID {
		return nil, errors.New("avatar artifact and target genesis domains differ")
	}
	known := make(map[string]bool, len(doc.module.Validators))
	for _, validator := range doc.module.Validators {
		known[validator.OperatorAddress] = true
	}
	avatars := make(map[string]string, len(doc.module.ValidatorAvatars))
	for _, avatar := range doc.module.ValidatorAvatars {
		avatars[avatar.OperatorAddress] = avatar.AvatarURI
	}
	for _, operator := range transfer.Operators {
		if !known[operator] {
			return nil, errors.New("avatar artifact references a validator absent from target genesis")
		}
		// An included operator with no record explicitly has no avatar at the source height.
		delete(avatars, operator)
	}
	for _, avatar := range transfer.Avatars {
		avatars[avatar.OperatorAddress] = avatar.AvatarURI
	}
	doc.module.ValidatorAvatars = nil
	for operator, uri := range avatars {
		doc.module.ValidatorAvatars = append(doc.module.ValidatorAvatars, validatortypes.ValidatorAvatar{OperatorAddress: operator, AvatarURI: uri})
	}
	sort.Slice(doc.module.ValidatorAvatars, func(i, j int) bool {
		return doc.module.ValidatorAvatars[i].OperatorAddress < doc.module.ValidatorAvatars[j].OperatorAddress
	})
	if err := doc.module.Validate(); err != nil {
		return nil, err
	}
	doc.native["validator_avatars"], err = json.Marshal(doc.module.ValidatorAvatars)
	if err != nil {
		return nil, err
	}
	doc.state[validatortypes.ModuleName], err = json.Marshal(doc.native)
	if err != nil {
		return nil, err
	}
	doc.document["app_state"], err = json.Marshal(doc.state)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(doc.document, "", "  ")
}

func validatorAvatarGenesisCommands() []*cobra.Command {
	var sourceGenesis, exportOutput string
	var exportEVM uint64
	export := &cobra.Command{Use: "export-avatars", Short: "Read avatar metadata at one trusted RPC height; does not export financial state", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		raw, err := readValidatorControlFile(sourceGenesis, 32<<20)
		if err != nil {
			return err
		}
		doc, err := readAvatarGenesis(raw, exportEVM)
		if err != nil {
			return err
		}
		if err := validatorControlClientContext(cmd, exportEVM, doc.chainID); err != nil {
			return err
		}
		ctx, err := client.GetClientQueryContext(cmd)
		if err != nil {
			return err
		}
		status, err := ctx.Client.Status(cmd.Context())
		if err != nil {
			return err
		}
		if status == nil || status.NodeInfo.Network != doc.chainID {
			return errors.New("RPC network differs from the supplied genesis")
		}
		height := ctx.Height
		if height == 0 {
			height = status.SyncInfo.LatestBlockHeight
		}
		if height <= 0 || height > status.SyncInfo.LatestBlockHeight {
			return errors.New("invalid avatar snapshot height")
		}
		block, err := ctx.Client.Block(cmd.Context(), &height)
		if err != nil {
			return err
		}
		if block == nil || block.Block == nil || block.Block.Height != height || block.Block.ChainID != doc.chainID {
			return errors.New("RPC block differs from the requested snapshot")
		}
		transfer := validatorAvatarTransfer{Format: avatarTransferFormat, MetadataOnly: true, ChainID: doc.chainID, EVMChainID: exportEVM, Height: height, SourceBlockHash: fmt.Sprintf("%X", block.BlockID.Hash)}
		operatorCodec := address.NewBech32Codec(identity.Bech32PrefixValAddr)
		query := validatortypes.NewQueryClient(ctx.WithHeight(height))
		for _, validator := range doc.module.Validators {
			response, err := query.Validator(cmd.Context(), &validatortypes.QueryValidatorRequest{Validator: validator.OperatorAddress})
			if err != nil {
				return err
			}
			if response == nil || response.Validator.OperatorAddress != validator.OperatorAddress {
				return errors.New("RPC validator differs from the genesis operator")
			}
			operator, err := operatorCodec.StringToBytes(validator.OperatorAddress)
			if err != nil {
				return err
			}
			uri, gotHeight, err := ctx.WithHeight(height).QueryWithData("/store/"+validatortypes.StoreKey+"/key", append([]byte{0x82}, operator...))
			if err != nil {
				return err
			}
			if gotHeight != height {
				return errors.New("RPC avatar height differs from the snapshot")
			}
			if err := validatortypes.ValidateValidatorAvatarURI(string(uri)); err != nil {
				return err
			}
			transfer.Operators = append(transfer.Operators, validator.OperatorAddress)
			if len(uri) != 0 {
				transfer.Avatars = append(transfer.Avatars, validatortypes.ValidatorAvatar{OperatorAddress: validator.OperatorAddress, AvatarURI: string(uri)})
			}
		}
		sort.Strings(transfer.Operators)
		sort.Slice(transfer.Avatars, func(i, j int) bool { return transfer.Avatars[i].OperatorAddress < transfer.Avatars[j].OperatorAddress })
		if err := validateAvatarTransfer(transfer); err != nil {
			return err
		}
		payload, err := json.MarshalIndent(transfer, "", "  ")
		if err != nil {
			return err
		}
		return outputFeeEnvelope(cmd, exportOutput, append(payload, '\n'))
	}}
	flags.AddQueryFlagsToCmd(export)
	export.Flags().StringVar(&sourceGenesis, "genesis-file", "", "Read-only valid genesis providing operator list and chain identity")
	export.Flags().Uint64Var(&exportEVM, "evm-chain-id", 0, "Matching EVM network identity")
	export.Flags().StringVar(&exportOutput, "output-file", "", "New metadata-only artifact file, or stdout")
	_ = export.MarkFlagRequired("genesis-file")
	_ = export.MarkFlagRequired("evm-chain-id")

	var targetGenesis, artifactFile, importOutput string
	var importEVM uint64
	importCmd := &cobra.Command{Use: "import-avatars", Short: "Write a new genesis file changing only validator avatar metadata", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		genesis, err := readValidatorControlFile(targetGenesis, 32<<20)
		if err != nil {
			return err
		}
		artifact, err := readValidatorControlFile(artifactFile, 4<<20)
		if err != nil {
			return err
		}
		payload, err := importValidatorAvatars(genesis, artifact, importEVM)
		if err != nil {
			return err
		}
		if importOutput == "" {
			return errors.New("a separate new output genesis file is required")
		}
		return outputFeeEnvelope(cmd, importOutput, append(payload, '\n'))
	}}
	importCmd.Flags().StringVar(&targetGenesis, "genesis-file", "", "Read-only independently prepared target genesis")
	importCmd.Flags().StringVar(&artifactFile, "avatar-file", "", "Trusted metadata-only avatar artifact")
	importCmd.Flags().Uint64Var(&importEVM, "evm-chain-id", 0, "Matching EVM network identity")
	importCmd.Flags().StringVar(&importOutput, "output-file", "", "New genesis file; existing paths are never overwritten")
	for _, name := range []string{"genesis-file", "avatar-file", "evm-chain-id", "output-file"} {
		_ = importCmd.MarkFlagRequired(name)
	}
	return []*cobra.Command{export, importCmd}
}
