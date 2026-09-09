package main

import (
	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// newActivateCmd creates the "spec activate" subcommand which transitions a
// draft spec to the active state and emits JSON confirmation.
func newActivateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "activate SPEC",
		Short: "Transition a draft spec to active state",
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

			// Transition to active state.
			updated, err := spec.Transition("active", specPath)
			if err != nil {
				return err
			}

			return emitOKTo(w, "spec", updated.SpecName, "status", updated.Status)
		},
	}

	return cmd
}
