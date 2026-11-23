package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/maxwell2010/go-smart-node/network/launchgenesis"
	"github.com/spf13/cobra"
)

func founderProofCommand() *cobra.Command {
	var plan, digest, challenge, output, signature string
	var proof launchgenesis.FounderOwnership
	root := &cobra.Command{Use: "founder-proof", Short: "Prepare and verify an offline founder wallet control proof"}
	root.PersistentFlags().StringVar(&plan, "plan", "", "Reviewed launch allocation JSON")
	root.PersistentFlags().StringVar(&digest, "approved-plan-sha256", "", "Independently approved allocation plan SHA256")
	root.PersistentFlags().StringVar(&challenge, "challenge", "", "Fresh 32-byte lowercase hex challenge issued by the verifier")
	root.PersistentPreRunE = func(_ *cobra.Command, _ []string) error {
		if plan == "" || digest == "" || challenge == "" {
			return fmt.Errorf("--plan, --approved-plan-sha256 and --challenge are required")
		}
		raw, err := readInput(plan, func(source io.Reader) ([]byte, error) {
			return io.ReadAll(io.LimitReader(source, launchgenesis.MaxAllocationPlanBytes+1))
		})
		if err != nil {
			return err
		}
		proof, err = launchgenesis.NewFounderOwnership(raw, digest, challenge)
		return err
	}

	payload := &cobra.Command{Use: "payload", Short: "Write the exact wallet message", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			body := proof.Payload()
			if output != "" {
				return writeNewFile(output, body)
			}
			_, err := cmd.OutOrStdout().Write(body)
			return err
		}}
	payload.Flags().StringVar(&output, "output", "", "Create a new message file instead of writing to stdout")
	verify := &cobra.Command{Use: "verify", Short: "Verify a founder wallet signature", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			encoded, err := readInput(signature, launchgenesis.ReadFounderSignature)
			if err != nil {
				return err
			}
			report, err := proof.Verify(encoded)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		}}
	verify.Flags().StringVar(&signature, "signature", "", "File containing a single 0x-prefixed signature")
	_ = verify.MarkFlagRequired("signature")
	root.AddCommand(payload, verify)
	return root
}
