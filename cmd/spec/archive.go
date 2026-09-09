package main

import (
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// newArchiveCmd creates the "spec archive" subcommand which moves a spec
// directory to the archive subdirectory and emits JSON confirmation.
func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive SPEC",
		Short: "Move a spec to the archive directory",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			w := cmd.OutOrStdout()

			// Resolve the spec directory.
			specPath, err := resolveSpec(specDir, args[0])
			if err != nil {
				return err
			}

			// Capture the base name before archiving (the directory will be moved).
			specName := filepath.Base(specPath)

			// Move the spec to the archive directory.
			if err := afspec.MoveToArchive(specPath, specDir); err != nil {
				return err
			}

			return emitOKTo(w, "archived", specName)
		},
	}

	return cmd
}
