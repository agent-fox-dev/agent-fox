package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/agentspec"
	"github.com/spf13/cobra"
)

// artifactFiles lists the spec artifact file basenames.
var artifactFiles = []string{"requirements.json", "test_spec.json", "tasks.json"}

// generateFunc is the function used to generate spec artifacts from an
// accepted PRD. It can be replaced in tests with a mock that avoids real
// AI calls.
var generateFunc = agentspec.GenerateSpec

// artifactKeyToFile maps agentspec artifact keys to on-disk filenames.
var artifactKeyToFile = map[string]string{
	"requirements": "requirements.json",
	"test_spec":    "test_spec.json",
	"tasks":        "tasks.json",
}

// newGenerateCmd creates the "spec generate" subcommand which generates
// specification artifacts from an accepted PRD.
func newGenerateCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "generate SPEC",
		Short: "Generate specification artifacts from an accepted PRD",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			specName := args[0]

			// Resolve spec to its directory path.
			specPath, err := resolveSpec(specDir, specName)
			if err != nil {
				return err
			}

			// Load session from _session.json.
			sessionPath := filepath.Join(specPath, "_session.json")
			session, err := loadSession(sessionPath)
			if err != nil {
				return fmt.Errorf("cannot load session: %w", err)
			}

			// If --force, delete existing artifact files before generating.
			if force {
				if err := deleteArtifacts(specPath); err != nil {
					return err
				}
			}

			// Auto-accept if session state is ASSESSING or REFINING.
			state, _ := session["state"].(string)
			if state == "assessing" || state == "refining" {
				if err := acceptPRD(session); err != nil {
					return fmt.Errorf("auto-accept failed: %w", err)
				}
				if err := saveSession(sessionPath, session); err != nil {
					return fmt.Errorf("cannot save session: %w", err)
				}
			}

			ctx := cmd.Context()
			quiet, _ := cmd.Flags().GetBool("quiet")
			if isAgentMode() {
				quiet = true
			}

			// Run Generate with a spinner.
			spinner := NewStatusSpinner("Generating spec artifacts...", cmd.ErrOrStderr(), quiet, false)
			spinner.Start()
			artifacts, warnings, genErr := generateArtifacts(ctx, specPath, session)
			spinner.Stop()

			if genErr != nil {
				// Clean up any partially written artifact files.
				cleanupPartialArtifacts(specPath)
				return genErr
			}

			// Update session with generated artifacts.
			session["generated_artifacts"] = artifacts
			if err := saveSession(sessionPath, session); err != nil {
				return fmt.Errorf("cannot save session: %w", err)
			}

			// Emit validation warnings to stderr so the user can see them.
			for _, w := range warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}

			// Validation errors are a hard failure: exit non-zero so callers
			// and CI pipelines know the generated spec has integrity issues.
			if len(warnings) > 0 {
				return fmt.Errorf("spec generated but post-generation validation found %d error(s); see warnings above", len(warnings))
			}

			return emitOKTo(cmd.OutOrStdout(), "artifacts", artifacts)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "delete existing artifacts before regenerating")

	return cmd
}

// acceptPRD transitions the session from ASSESSING or REFINING to prd_accepted.
// The session map is modified in-place.
func acceptPRD(session map[string]any) error {
	session["state"] = "prd_accepted"
	return nil
}

// deleteArtifacts removes existing artifact files from the spec directory.
// Returns an error if a file exists but cannot be deleted (e.g. permission denied).
func deleteArtifacts(specPath string) error {
	for _, artifact := range artifactFiles {
		p := filepath.Join(specPath, artifact)
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("cannot delete %s: %w", artifact, err)
		}
	}
	return nil
}

// cleanupPartialArtifacts removes any artifact files that may have been
// partially written during a failed generation attempt.
func cleanupPartialArtifacts(specPath string) {
	for _, artifact := range artifactFiles {
		p := filepath.Join(specPath, artifact)
		_ = os.Remove(p)
	}
}

// generateArtifacts calls the agentspec AI pipeline to produce specification
// artifacts from the accepted PRD. Returns the list of generated artifact
// filenames, any post-generation validation warnings, and an error.
// When SPEC_TEST_BLOCK_AI=1, blocks until context cancellation.
func generateArtifacts(ctx context.Context, specPath string, _ map[string]any) ([]string, []string, error) {
	// Test hook: block until context cancellation to simulate a long-running
	// AI call. Activated by SPEC_TEST_BLOCK_AI=1 environment variable.
	// If SPEC_TEST_READY_FILE is also set, the file is created before blocking
	// so tests can wait for readiness before sending signals, avoiding timing
	// races on cold binary startup.
	if os.Getenv("SPEC_TEST_BLOCK_AI") == "1" {
		if rf := os.Getenv("SPEC_TEST_READY_FILE"); rf != "" {
			_ = os.WriteFile(rf, []byte("ready"), 0600)
		}
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}

	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	result, err := generateFunc(ctx, specPath)
	if err != nil {
		return nil, nil, err
	}

	// Map agentspec artifact keys (e.g. "requirements") to filenames
	// (e.g. "requirements.json") for CLI output.
	artifacts := make([]string, 0, len(result.Artifacts))
	for _, name := range result.Artifacts {
		if fileName, ok := artifactKeyToFile[name]; ok {
			artifacts = append(artifacts, fileName)
		} else {
			artifacts = append(artifacts, name)
		}
	}

	// Fallback: if the result contained no artifacts, return the standard set.
	if len(artifacts) == 0 {
		artifacts = append(artifacts, artifactFiles...)
	}

	return artifacts, result.Warnings, nil
}
