package main

import (
	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// newSealCmd creates the "spec seal" subcommand which transitions an
// active spec to the sealed state and emits JSON confirmation.
func newSealCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seal SPEC",
		Short: "Transition an active spec to sealed state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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

			// Transition to sealed state.
			updated, err := spec.Transition("sealed", specPath)
			if err != nil {
				return err
			}

			return emitOKTo(w, "spec", updated.SpecName, "status", updated.Status)
		},
	}

	return cmd
}
