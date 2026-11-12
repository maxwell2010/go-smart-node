package main

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	cmted25519 "github.com/cometbft/cometbft/crypto/ed25519"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	"github.com/cometbft/cometbft/privval"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec/address"
	ownedapp "github.com/maxwell2010/go-smart-node/network/app"
	"github.com/maxwell2010/go-smart-node/network/identity"
	"github.com/spf13/cobra"
)

const nativeValidatorRegistrationMetadataCommandName = "validator-registration-metadata"

func nativeValidatorRegistrationMetadataCmd() *cobra.Command {
	var chainID, operator, keyPath string
	var expiresHeight int64
	var commissionPercent uint8
	cmd := &cobra.Command{
		Use:   nativeValidatorRegistrationMetadataCommandName,
		Short: "Generate native validator registration metadata offline",
		Long: "Generate metadata offline for a native validator registration proposal using the applicant's new Ed25519 consensus key.\n" +
			"Set the proposal title to the validator moniker, use an empty message list and submit it from the operator account.\n" +
			"Set the proof expiry after the expected vote tally. The command reads only the specified key file and prints one line for the proposal metadata field.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			evmDomain, _, _ := strings.Cut(strings.TrimPrefix(chainID, "mintcandy_"), "-")
			evmID, _ := strconv.ParseUint(evmDomain, 10, 64)
			if err := identity.ValidateChainIDs(chainID, evmID); err != nil {
				return err
			}
			proposer, err := address.NewBech32Codec(identity.Bech32PrefixAccAddr).StringToBytes(operator)
			if err != nil || len(proposer) != 20 {
				return errors.New("operator must be a 20-byte d0 account address")
			}
			if expiresHeight <= 1 || commissionPercent > 100 {
				return errors.New("expiry must be after height 1 and commission must be between 0 and 100")
			}
			key, err := readRegistrationConsensusKey(keyPath)
			if err != nil {
				return err
			}
			defer clear(key)
			metadata, err := ownedapp.BuildNativeValidatorRegistrationMetadata(chainID, evmID, proposer, expiresHeight, commissionPercent, key)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), metadata)
			return err
		},
	}
	f := cmd.Flags()
	f.StringVar(&chainID, flags.FlagChainID, "", "Fresh network chain ID in the form mintcandy_<evm-id>-<revision>")
	f.StringVar(&operator, "operator", "", "d0 account address that will submit the proposal and receive validator rewards")
	f.Int64Var(&expiresHeight, "expires-height", 0, "First block height at which the ownership proof is invalid")
	f.Uint8Var(&commissionPercent, "commission-percent", 0, "Whole validator commission percentage from 0 to 100")
	f.StringVar(&keyPath, "priv-validator-key-file", "", "Path to the local CometBFT priv_validator_key.json")
	for _, flag := range []string{flags.FlagChainID, "operator", "expires-height", "commission-percent", "priv-validator-key-file"} {
		_ = cmd.MarkFlagRequired(flag)
	}
	return cmd
}

func readRegistrationConsensusKey(path string) (ed25519.PrivateKey, error) {
	const maxFileSize = 16 * 1024
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect consensus key file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, errors.New("consensus key file must be a regular file of at most 16 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open consensus key file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	defer clear(data)
	if err != nil {
		return nil, fmt.Errorf("read consensus key file: %w", err)
	}
	var record privval.FilePVKey
	if len(data) > maxFileSize || cmtjson.Unmarshal(data, &record) != nil {
		return nil, errors.New("invalid CometBFT consensus key file")
	}
	privateKey, privateOK := record.PrivKey.(cmted25519.PrivKey)
	publicKey, publicOK := record.PubKey.(cmted25519.PubKey)
	if !privateOK || len(privateKey) != ed25519.PrivateKeySize ||
		!publicOK || len(publicKey) != ed25519.PublicKeySize ||
		!bytes.Equal(privateKey[ed25519.SeedSize:], publicKey) ||
		!bytes.Equal(record.Address, publicKey.Address()) {
		clear(privateKey)
		return nil, errors.New("consensus key file must contain a matching Ed25519 key pair and consensus address")
	}
	return ed25519.PrivateKey(privateKey), nil
}
