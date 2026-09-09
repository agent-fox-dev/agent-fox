package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/afspec/legacy"
	"github.com/spf13/cobra"
)

// newMigrateCmd creates the "spec migrate" subcommand, which converts a
// format version 1.3 spec package to version 2 in place.
func newMigrateCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "migrate SPEC",
		Short: "Convert a format version 1 spec package to version 2",
		Long: "Convert a format version 1.3 spec package to version 2, following the\n" +
			"mapping in the format specification. Everything in that mapping is\n" +
			"mechanical except task ownership of tests: version 1 had no such rule, and\n" +
			"in practice no version 1 spec owned its edge-case or property tests. The\n" +
			"converter attaches each orphaned test to the task that owns its parent\n" +
			"requirement and reports which ones, so the result is a starting point for\n" +
			"review rather than a finished plan.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			w := cmd.OutOrStdout()

			specPath, err := resolveSpec(specDir, args[0])
			if err != nil {
				return err
			}

			specID, specName, err := afspec.ParseSpecDirName(filepath.Base(specPath))
			if err != nil {
				return fmt.Errorf("spec directory %q is not named {NN}_{snake_case_name}: %w",
					filepath.Base(specPath), err)
			}

			// Refuse a spec that is already version 2, so that a second run
			// cannot rewrite a migrated spec through the v1 mapping.
			if version, versionErr := readSchemaVersion(specPath); versionErr == nil && version >= afspec.SchemaVersion {
				return fmt.Errorf("spec %s already declares schema_version %d; nothing to migrate", args[0], version)
			}

			src, err := legacy.LoadSpec(specPath)
			if err != nil {
				return fmt.Errorf("cannot read %s as a version 1 spec: %w", args[0], err)
			}

			requirements, testSpec, tasks, report, err := afspec.Migrate(src, specID, specName)
			if err != nil {
				return err
			}

			migrated := &afspec.Spec{
				SpecID:        specID,
				SpecName:      specName,
				SchemaVersion: afspec.SchemaVersion,
				Requirements:  requirements,
				TestSpec:      testSpec,
				Tasks:         tasks,
			}
			if err := copyPRDFields(specPath, migrated); err != nil {
				return err
			}

			result := migrated.Validate()
			errorMessages := make([]string, 0, len(result.Errors))
			for _, e := range result.Errors {
				errorMessages = append(errorMessages, formatMigrationEntry(e))
			}

			if dryRun {
				return emitOKTo(w, "spec", filepath.Base(specPath), "dry_run", true,
					"notes", report.Notes, "attached_tests", report.Attached,
					"valid", result.Valid, "errors", errorMessages)
			}

			if err := migrated.Save(specPath); err != nil {
				return fmt.Errorf("cannot write the migrated spec: %w", err)
			}

			return emitOKTo(w, "spec", filepath.Base(specPath), "dry_run", false,
				"notes", report.Notes, "attached_tests", report.Attached,
				"valid", result.Valid, "errors", errorMessages)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what the conversion would do without writing anything")

	return cmd
}

// formatMigrationEntry renders a validation entry as one line naming its rule.
func formatMigrationEntry(e afspec.ValidationEntry) string {
	if e.Check == "" {
		return e.Message
	}
	return "[" + e.Check + "] " + e.Message
}

// readSchemaVersion reads schema_version from a spec's requirements.json
// without imposing a format version on it.
func readSchemaVersion(specPath string) (int, error) {
	data, err := os.ReadFile(filepath.Join(specPath, "requirements.json"))
	if err != nil {
		return 0, err
	}
	var doc struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := jsonUnmarshal(data, &doc); err != nil {
		return 0, err
	}
	return doc.SchemaVersion, nil
}

// copyPRDFields reads the existing prd.md and carries its frontmatter and body
// onto the migrated spec, bumping schema_version to 2.
func copyPRDFields(specPath string, migrated *afspec.Spec) error {
	// The v1 and v2 frontmatter differ only in schema_version, so the v2
	// loader reads a v1 prd.md; only its identity fields are then trusted.
	existing, err := afspec.LoadSpec(specPath)
	if err == nil {
		migrated.Title = existing.Title
		migrated.Status = existing.Status
		migrated.CreatedAt = existing.CreatedAt
		migrated.UpdatedAt = existing.UpdatedAt
		migrated.Owner = existing.Owner
		migrated.Source = existing.Source
		migrated.Supersedes = existing.Supersedes
		migrated.Tags = existing.Tags
		migrated.IntentHash = existing.IntentHash
		migrated.PRDBody = existing.PRDBody
		migrated.Architecture = existing.Architecture
		return nil
	}

	// LoadSpec failed on the v1 JSON, which is expected: read prd.md alone.
	data, readErr := os.ReadFile(filepath.Join(specPath, "prd.md"))
	if readErr != nil {
		return fmt.Errorf("cannot read prd.md: %w", readErr)
	}
	migrated.PRDBody = stripFrontmatter(string(data))
	migrated.Status = "draft"
	migrated.Title = migrated.SpecName
	return nil
}
