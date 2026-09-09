package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// specNameRE matches valid spec names: must start with lowercase letter,
// followed by zero or more lowercase letters, digits, or underscores.
var specNameRE = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// newNewCmd creates the "spec new" subcommand which creates a new spec
// from a PRD file.
func newNewCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "new PRD_FILE",
		Short: "Create a new spec from a PRD file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prdPath := args[0]
			specDir, _ := cmd.Flags().GetString("spec-dir")

			// Validate PRD file exists and is a file (before any initialization).
			info, err := os.Stat(prdPath)
			if err != nil {
				return fmt.Errorf("PRD_FILE %q: %w", prdPath, err)
			}
			if info.IsDir() {
				return fmt.Errorf("PRD_FILE %q is a directory, not a file", prdPath)
			}

			// Validate or derive spec name.
			if cmd.Flags().Changed("name") {
				if !specNameRE.MatchString(name) {
					return fmt.Errorf("--name %q: must match [a-z][a-z0-9_]*", name)
				}
			} else {
				name = deriveSnakeCaseName(filepath.Base(prdPath))
			}

			// Auto-initialize spec directory if it does not exist.
			if err := os.MkdirAll(specDir, 0755); err != nil {
				return fmt.Errorf("cannot create spec directory %q: %w", specDir, err)
			}

			// Auto-create campaign.yaml if it does not exist.
			campaignPath := filepath.Join(specDir, "campaign.yaml")
			if _, statErr := os.Stat(campaignPath); os.IsNotExist(statErr) {
				if err := writeCampaignYAML(campaignPath); err != nil {
					return fmt.Errorf("cannot create campaign.yaml: %w", err)
				}
			}

			// Determine the next numeric prefix.
			prefix := nextSpecPrefix(specDir)

			// Derive spec directory name and validate against IsSpecDirName.
			dirName := fmt.Sprintf("%02d_%s", prefix, name)
			if !afspec.IsSpecDirName(dirName) {
				return fmt.Errorf("derived directory name %q is invalid: must match NN_snake_case pattern", dirName)
			}

			// Create the spec directory; fail if it already exists.
			specPath := filepath.Join(specDir, dirName)
			if err := os.Mkdir(specPath, 0755); err != nil {
				if os.IsExist(err) {
					return fmt.Errorf("spec directory %q already exists", specPath)
				}
				return fmt.Errorf("cannot create spec %q: %w", specPath, err)
			}

			// Read the PRD file content (becomes the PRD body).
			prdContent, err := os.ReadFile(prdPath)
			if err != nil {
				os.RemoveAll(specPath)
				return fmt.Errorf("cannot read PRD file %q: %w", prdPath, err)
			}

			// Build a minimal valid Spec and save all four required artifacts.
			specID := fmt.Sprintf("%02d", prefix)
			now := time.Now().UTC().Format(time.RFC3339Nano)

			const (
				reqSchemaURL  = "https://agent-fox.dev/schemas/requirements.v1.json"
				tsSchemaURL   = "https://agent-fox.dev/schemas/test_spec.v1.json"
				taskSchemaURL = "https://agent-fox.dev/schemas/tasks.v1.json"
			)

			spec := &afspec.Spec{
				SpecID:        specID,
				SpecName:      name,
				Title:         "",
				Status:        "draft",
				CreatedAt:     now,
				UpdatedAt:     now,
				Owner:         "",
				Source:        "",
				Supersedes:    []string{},
				Tags:          []string{},
				IntentHash:    nil,
				SchemaVersion: 1,
				PRDBody:       string(prdContent),
				Requirements: &afspec.RequirementsV1Json{
					Schema:                reqSchemaURL,
					SpecId:                specID,
					SpecName:              name,
					SchemaVersion:         1,
					Introduction:          "",
					Glossary:              afspec.RequirementsV1JsonGlossary{},
					Requirements:          []afspec.Requirement{},
					CorrectnessProperties: []afspec.CorrectnessProperty{},
					ExecutionPaths:        []afspec.ExecutionPath{},
					ErrorHandling:         []afspec.ErrorHandlingEntry{},
				},
				TestSpec: &afspec.TestSpecV1Json{
					Schema:        tsSchemaURL,
					SpecId:        specID,
					SpecName:      name,
					SchemaVersion: 1,
					TestCases:     []afspec.TestCase{},
					PropertyTests: []afspec.PropertyTest{},
					EdgeCaseTests: []afspec.EdgeCaseTest{},
					SmokeTests:    []afspec.SmokeTest{},
				},
				Tasks: &afspec.TasksV1Json{
					Schema:        taskSchemaURL,
					SpecId:        specID,
					SpecName:      name,
					SchemaVersion: 1,
					TestCommands:  afspec.TestCommands{},
					Dependencies:  []afspec.TaskDependency{},
					// task_groups requires minItems:1 per Section 8.3; scaffold with
					// the mandatory first ("tests") and last ("wiring_verification") groups.
					TaskGroups: []afspec.TaskGroup{
						{
							Id:       1,
							Kind:     afspec.TaskGroupKindTests,
							Title:    "Write Tests",
							Subtasks: []afspec.Subtask{},
							Verification: afspec.VerificationSubtask{
								Id:     "1.V",
								Checks: []string{"all tests pass"},
							},
						},
						{
							Id:       2,
							Kind:     afspec.TaskGroupKindWiringVerification,
							Title:    "Wiring Verification",
							Subtasks: []afspec.Subtask{},
							Verification: afspec.VerificationSubtask{
								Id:     "2.V",
								Checks: []string{"no stubs remain"},
							},
						},
					},
					Traceability: []afspec.TraceabilityEntry{},
				},
			}

			if err := spec.Save(specPath); err != nil {
				os.RemoveAll(specPath)
				return fmt.Errorf("cannot save spec artifacts: %w", err)
			}

			// Create initial _session.json.
			session := map[string]any{
				"state": "init",
			}
			sessionJSON, _ := json.MarshalIndent(session, "", "  ")
			if err := os.WriteFile(filepath.Join(specPath, "_session.json"), sessionJSON, 0644); err != nil {
				os.RemoveAll(specPath)
				return fmt.Errorf("cannot write _session.json: %w", err)
			}

			// Emit JSON result.
			return emitOKTo(cmd.OutOrStdout(), "spec_dir", specDir, "state", "init")
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "spec name (must match [a-z][a-z0-9_]*)")

	return cmd
}

// deriveSnakeCaseName converts a filename (with extension) to a snake_case
// spec name by stripping the extension and converting CamelCase to snake_case.
func deriveSnakeCaseName(filename string) string {
	// Strip extension.
	ext := filepath.Ext(filename)
	if ext != "" {
		filename = filename[:len(filename)-len(ext)]
	}

	// Convert CamelCase to snake_case.
	var result []rune
	for i, r := range filename {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := rune(filename[i-1])
				if !unicode.IsUpper(prev) && prev != '_' {
					result = append(result, '_')
				}
			}
			result = append(result, unicode.ToLower(r))
		} else {
			result = append(result, r)
		}
	}

	return string(result)
}

// nextSpecPrefix scans the spec directory for existing spec subdirectories
// and returns the next numeric prefix (max existing + 1, starting at 1).
func nextSpecPrefix(specDir string) int {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return 1
	}

	maxPrefix := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		idx := strings.Index(name, "_")
		if idx <= 0 {
			continue
		}
		n, err := strconv.Atoi(name[:idx])
		if err != nil {
			continue
		}
		if n > maxPrefix {
			maxPrefix = n
		}
	}
	return maxPrefix + 1
}

// writeCampaignYAML writes a default campaign.yaml file.
func writeCampaignYAML(path string) error {
	content := "name: default\ndescription: default campaign\n"
	return os.WriteFile(path, []byte(content), 0644)
}
