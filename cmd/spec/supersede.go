package main

import (
	"fmt"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// newSupersedeCmd creates the "spec supersede" subcommand which transitions
// a sealed spec to the superseded state, prepends a deprecation banner to
// the PRD body, and emits JSON confirmation.
func newSupersedeCmd() *cobra.Command {
	var by string

	cmd := &cobra.Command{
		Use:   "supersede SPEC",
		Short: "Transition a sealed spec to superseded state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if by == "" {
				return fmt.Errorf("--by flag is required: specify the ID of the superseding spec")
			}

			specDir, _ := cmd.Flags().GetString("spec-dir")
			w := cmd.OutOrStdout()

			// Resolve the spec directory.
			specPath, err := resolveSpec(specDir, args[0])
			if err != nil {
				return err
			}

			// Load the spec.
			spec, err := afspec.LoadSpec(specPath)
			if err != nil {
				return err
			}

			// Transition to superseded state with deprecation banner.
			updated, err := spec.Supersede(by, specPath)
			if err != nil {
				return err
			}

			return emitOKTo(w, "spec", updated.SpecName, "status", updated.Status, "superseded_by", by)
		},
	}

	cmd.Flags().StringVar(&by, "by", "", "ID of the superseding spec (required)")

	return cmd
}
