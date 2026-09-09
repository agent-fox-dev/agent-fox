package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// lintFinding represents a single lint diagnostic result.
type lintFinding struct {
	Spec     string `json:"spec"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
}

// newLintCmd creates the "spec lint" subcommand which runs lint checks
// across all specs and reports findings as JSON.
func newLintCmd() *cobra.Command {
	var all bool

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Lint all specs for quality and consistency issues",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			quiet, _ := cmd.Flags().GetBool("quiet")
			if isAgentMode() {
				quiet = true
			}
			w := cmd.OutOrStdout()

			return runLint(specDir, all, quiet, w, cmd)
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "include fully-implemented specs in the lint run")

	return cmd
}

// runLint runs lint checks across all specs in the spec directory.
func runLint(specDir string, lintAll, quiet bool, w interface{ Write([]byte) (int, error) }, cmd *cobra.Command) error {
	// Handle non-existent spec directory: emit no-specs finding with error severity.
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		findings := []lintFinding{{
			Spec:     "",
			Rule:     "no-specs",
			Severity: "error",
			Message:  "Specs directory not found",
		}}
		result := map[string]any{
			"ok":        false,
			"findings":  findings,
			"exit_code": 1,
		}
		if err := emitTo(w, result); err != nil {
			return err
		}
		return fmt.Errorf("lint found 1 issue(s)")
	}

	spinner := NewStatusSpinner("Linting specs...", cmd.ErrOrStderr(), quiet, false)
	spinner.Start()
	defer spinner.Stop()

	findings, exitCode, err := runLintSpecs(specDir, lintAll)
	if err != nil {
		return err
	}

	// Emit findings as JSON.
	result := map[string]any{
		"ok":        exitCode == 0,
		"findings":  findings,
		"exit_code": exitCode,
	}
	if err := emitTo(w, result); err != nil {
		return err
	}

	if exitCode != 0 {
		return fmt.Errorf("lint found %d issue(s)", len(findings))
	}
	return nil
}

// runLintSpecs uses the library's DiscoverLintSpecs to find spec directories,
// then LoadSpec + Validate for structural validation, and appends CLI-only
// extras (empty-artifact, missing-session). Returns findings, exit code, and
// any fatal error (e.g. unreadable directory).
func runLintSpecs(specDir string, lintAll bool) ([]lintFinding, int, error) {
	// Use library discovery to find spec directories.
	infos, err := afspec.DiscoverLintSpecs(specDir, "")
	if err != nil {
		// "no specs found" → emit no-specs finding (not a fatal error).
		if strings.Contains(err.Error(), "no specs found") {
			findings := []lintFinding{{
				Spec:     "",
				Rule:     "no-specs",
				Severity: "error",
				Message:  "Specs directory not found",
			}}
			return findings, 1, nil
		}
		// Other errors (unreadable directory) → propagate as fatal.
		return nil, 0, fmt.Errorf("cannot read spec directory %q: %w", specDir, err)
	}

	var libFindings []afspec.LintFinding

	for _, info := range infos {
		// Skip fully-implemented specs unless --all is set.
		if !lintAll && isSpecFullyImplemented(info) {
			continue
		}

		// Library structural validation: LoadSpec + Validate.
		spec, loadErr := afspec.LoadSpec(info.Path)
		if loadErr != nil {
			libFindings = append(libFindings, afspec.LintFinding{
				SpecName: info.Name,
				File:     "requirements.json",
				Rule:     "load-failure",
				Severity: "error",
				Message:  loadErr.Error(),
			})
		} else {
			result := spec.Validate()
			for _, e := range result.Errors {
				libFindings = append(libFindings, convertValidationEntry(info.Name, e))
			}
			for _, w := range result.Warnings {
				libFindings = append(libFindings, convertValidationEntry(info.Name, w))
			}
		}

		// CLI-only extras: empty-artifact and missing-session.
		libFindings = append(libFindings, cliLintExtras(info.Path, info.Name)...)
	}

	sorted := afspec.SortFindings(libFindings)
	exitCode := afspec.ComputeExitCode(sorted)

	// Convert library findings to CLI format.
	findings := make([]lintFinding, len(sorted))
	for i, f := range sorted {
		findings[i] = lintFinding{
			Spec:     f.SpecName,
			Rule:     f.Rule,
			Severity: f.Severity,
			Message:  f.Message,
			File:     f.File,
		}
	}

	// Ensure findings is never nil (always an array in JSON).
	if len(findings) == 0 {
		findings = []lintFinding{}
	}

	return findings, exitCode, nil
}

// convertValidationEntry converts a library ValidationEntry into a
// LintFinding. Severity is "warning" for warning-category entries,
// "error" for all others. Rule is the Check name if present, otherwise
// the Category.
func convertValidationEntry(specName string, entry afspec.ValidationEntry) afspec.LintFinding {
	severity := "error"
	if entry.Category == "warning" {
		severity = "warning"
	}
	rule := entry.Check
	if rule == "" {
		rule = entry.Category
	}
	return afspec.LintFinding{
		SpecName: specName,
		File:     entry.Artifact,
		Rule:     rule,
		Severity: severity,
		Message:  entry.Message,
	}
}

// isSpecFullyImplemented checks whether all subtasks in a spec's tasks.json
// are in state "done" or "dropped". Returns false if tasks.json is absent,
// unreadable, or has any subtask in another state.
func isSpecFullyImplemented(info afspec.LintSpecInfo) bool {
	if !info.HasTasks {
		return false
	}

	tasksPath := filepath.Join(info.Path, "tasks.json")
	data, err := os.ReadFile(tasksPath)
	if err != nil {
		return false
	}

	var tasks struct {
		Tasks []struct {
			State string `json:"state"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(data, &tasks); err != nil {
		return false
	}

	// A spec with no tasks at all is a scaffold, not a finished spec.
	if len(tasks.Tasks) == 0 {
		return false
	}

	for _, task := range tasks.Tasks {
		if task.State != "done" && task.State != "dropped" {
			return false
		}
	}

	return true
}

// cliLintExtras runs CLI-only lint checks on a spec directory and returns
// findings for empty JSON artifacts and missing _session.json.
func cliLintExtras(specPath, specName string) []afspec.LintFinding {
	var findings []afspec.LintFinding

	// Check JSON artifact files for empty objects.
	jsonFiles := []string{"requirements.json", "test_spec.json", "tasks.json"}
	for _, f := range jsonFiles {
		p := filepath.Join(specPath, f)
		data, err := os.ReadFile(p)
		if err != nil {
			continue // Missing/unreadable files are handled by library validation.
		}
		var parsed map[string]any
		if err := json.Unmarshal(data, &parsed); err == nil {
			if len(parsed) == 0 {
				findings = append(findings, afspec.LintFinding{
					SpecName: specName,
					Rule:     "empty-artifact",
					Severity: "warning",
					Message:  fmt.Sprintf("%s has no content", f),
					File:     f,
				})
			}
		}
	}

	// Check for missing _session.json.
	sessionPath := filepath.Join(specPath, "_session.json")
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		findings = append(findings, afspec.LintFinding{
			SpecName: specName,
			Rule:     "missing-session",
			Severity: "warning",
			Message:  "_session.json is missing",
			File:     "_session.json",
		})
	}

	return findings
}
