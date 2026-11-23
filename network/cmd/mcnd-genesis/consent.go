package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/maxwell2010/go-smart-node/network/launchgenesis"
	"github.com/spf13/cobra"
)

func launchConsentCommand() *cobra.Command {
	var manifest, genesis, approvedHash string
	var operator, output, signatures string
	parent := &cobra.Command{Use: "launch-consent", Short: "Prepare and verify offline founding operator confirmations"}
	parent.PersistentFlags().StringVar(&manifest, "manifest", "", "OpenPGP-verified genesis manifest")
	parent.PersistentFlags().StringVar(&genesis, "genesis", "", "Verified genesis JSON")
	parent.PersistentFlags().StringVar(&approvedHash, "approved-manifest-sha256", "", "Manifest SHA256 obtained from the approved channel")
	parent.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if manifest == "" || genesis == "" || approvedHash == "" {
			return fmt.Errorf("--manifest, --genesis and --approved-manifest-sha256 are required")
		}
		return nil
	}
	payload := &cobra.Command{Use: "payload", Short: "Write an exact wallet message for a registered operator", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			consent, err := readLaunchConsent(manifest, genesis, approvedHash)
			if err != nil {
				return err
			}
			body, err := consent.Payload(operator)
			if err != nil {
				return err
			}
			if output != "" {
				return writeNewFile(output, body)
			}
			_, err = cmd.OutOrStdout().Write(body)
			return err
		}}
	payload.Flags().StringVar(&operator, "operator", "", "Operator 0x address from the launch application")
	payload.Flags().StringVar(&output, "output", "", "Create a new message file instead of writing to stdout")
	verify := &cobra.Command{Use: "verify", Short: "Verify one wallet signature from each founding operator", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			consent, err := readLaunchConsent(manifest, genesis, approvedHash)
			if err != nil {
				return err
			}
			rows, err := readInput(signatures, launchgenesis.ReadOperatorConsents)
			if err != nil {
				return err
			}
			report, err := consent.Verify(rows)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		}}
	verify.Flags().StringVar(&signatures, "signatures", "", "JSON array of operatorAddress and signature entries")
	_ = payload.MarkFlagRequired("operator")
	_ = verify.MarkFlagRequired("signatures")
	parent.AddCommand(payload, verify)
	return parent
}

func readLaunchConsent(manifest, genesis, approvedHash string) (launchgenesis.LaunchConsent, error) {
	manifestBytes, err := readInput(manifest, func(reader io.Reader) ([]byte, error) {
		return io.ReadAll(io.LimitReader(reader, launchgenesis.MaxConsentBytes+1))
	})
	if err != nil {
		return launchgenesis.LaunchConsent{}, err
	}
	genesisBytes, err := readInput(genesis, func(reader io.Reader) ([]byte, error) {
		return io.ReadAll(io.LimitReader(reader, launchgenesis.MaxCandidateBytes+1))
	})
	if err != nil {
		return launchgenesis.LaunchConsent{}, err
	}
	return launchgenesis.NewLaunchConsent(manifestBytes, genesisBytes, approvedHash)
}
