package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// requiredFiles lists the files that every spec must contain.
var requiredFiles = []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"}

// validationError represents a single validation finding.
type validationError struct {
	File     string `json:"file"`
	Severity string `json:"severity"` // "error" or "warning"
	Message  string `json:"message"`
}

// validationResult holds the aggregated validation output for one or more specs.
type validationResult struct {
	Valid        bool              `json:"valid"`
	ErrorCount   int               `json:"error_count"`
	WarningCount int               `json:"warning_count"`
	Errors       []validationError `json:"errors,omitempty"`
}

// newValidateCmd creates the "spec validate" subcommand which validates
// one or all specs for structural correctness.
func newValidateCmd() *cobra.Command {
	var cross bool
	var short bool
	var trace bool

	cmd := &cobra.Command{
		Use:   "validate [SPEC]",
		Short: "Validate one or all specs for structural correctness",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			quiet, _ := cmd.Flags().GetBool("quiet")
			if isAgentMode() {
				quiet = true
			}
			w := cmd.OutOrStdout()

			if len(args) == 1 {
				if trace {
					return runValidateTrace(specDir, args[0], w)
				}
				return runValidateSingle(specDir, args[0], short, w)
			}

			if trace {
				return fmt.Errorf("--trace needs a SPEC argument: the matrix is per spec")
			}

			// Multi-spec or cross-spec mode.
			if cross {
				return runValidateCross(specDir, short, quiet, w, cmd)
			}
			return runValidateAll(specDir, short, quiet, w, cmd)
		},
	}

	cmd.Flags().BoolVar(&cross, "cross", false, "run cross-spec interface consistency checks")
	cmd.Flags().BoolVar(&short, "short", false, "emit condensed output with only valid/error_count/warning_count")
	cmd.Flags().BoolVar(&trace, "trace", false, "print the derived traceability matrix for one spec instead of validating it")

	return cmd
}

// runValidateSingle validates a single spec by name.
func runValidateSingle(specDir, specName string, short bool, w interface{ Write([]byte) (int, error) }) error {
	specPath, err := resolveSpec(specDir, specName)
	if err != nil {
		return err
	}

	result := validateSpec(specPath)
	return emitValidationResult(w, result, short)
}

// runValidateTrace prints the traceability matrix derived from one spec:
// criterion or path, the tests that verify it, and the tasks that own those
// tests (§8.5). Nothing is read from or written to a stored traceability
// array — there is none — so the matrix cannot drift from the artifacts.
func runValidateTrace(specDir, specName string, w interface{ Write([]byte) (int, error) }) error {
	specPath, err := resolveSpec(specDir, specName)
	if err != nil {
		return err
	}

	spec, err := afspec.LoadSpec(specPath)
	if err != nil {
		return err
	}

	matrix := spec.ComputeTraceability()
	rows := make([]map[string]any, 0, len(matrix.Links))
	uncovered, unowned := 0, 0
	for _, link := range matrix.Links {
		tests := link.Tests
		if tests == nil {
			tests = []string{}
		}
		tasks := link.Tasks
		if tasks == nil {
			tasks = []int{}
		}
		rows = append(rows, map[string]any{
			"kind":    link.Kind,
			"id":      link.ID,
			"tests":   tests,
			"tasks":   tasks,
			"covered": link.Covered,
			"owned":   link.Owned,
		})
		if !link.Covered {
			uncovered++
		}
		if !link.Owned {
			unowned++
		}
	}

	return emitOKTo(w,
		"spec", filepath.Base(specPath),
		"trace", rows,
		"uncovered", uncovered,
		"unowned", unowned,
	)
}

// runValidateAll discovers all specs and validates each, producing
// per-spec results under a "specs" key matching Python's multi-spec format.
func runValidateAll(specDir string, short, quiet bool, w interface{ Write([]byte) (int, error) }, cmd *cobra.Command) error {
	specs, err := discoverSpecDirs(specDir)
	if err != nil {
		return fmt.Errorf("cannot discover specs: %w", err)
	}

	spinner := NewStatusSpinner("Validating specs...", cmd.ErrOrStderr(), quiet, false)
	spinner.Start()
	defer spinner.Stop()

	allValid := true
	specsResults := make(map[string]any, len(specs))
	for _, sp := range specs {
		r := validateSpec(sp)
		dirName := filepath.Base(sp)
		if short {
			specsResults[dirName] = shortenResult(r)
		} else {
			specsResults[dirName] = addCounts(r)
		}
		if !r.Valid {
			allValid = false
		}
	}

	output := map[string]any{
		"valid": allValid,
		"specs": specsResults,
	}

	return emitMultiSpecResult(w, output, allValid)
}

// runValidateCross discovers all specs, loads them via the library,
// builds a dependency graph, runs cross-spec validation via
// afspec.ValidateCrossSpec, and produces per-spec results under a
// "specs" key matching Python's multi-spec format.
func runValidateCross(specDir string, short, quiet bool, w interface{ Write([]byte) (int, error) }, cmd *cobra.Command) error {
	specPaths, err := discoverSpecDirs(specDir)
	if err != nil {
		return fmt.Errorf("cannot discover specs: %w", err)
	}

	spinner := NewStatusSpinner("Running cross-spec validation...", cmd.ErrOrStderr(), quiet, false)
	spinner.Start()
	defer spinner.Stop()

	// First run per-spec structural validation.
	allValid := true
	specsResults := make(map[string]any, len(specPaths))
	for _, sp := range specPaths {
		r := validateSpec(sp)
		dirName := filepath.Base(sp)
		if short {
			specsResults[dirName] = shortenResult(r)
		} else {
			specsResults[dirName] = addCounts(r)
		}
		if !r.Valid {
			allValid = false
		}
	}

	// Load all specs via the library and build a dependency graph.
	var loadedSpecs []*afspec.Spec
	var metas []afspec.SpecMeta
	for _, sp := range specPaths {
		spec, loadErr := afspec.LoadSpec(sp)
		if loadErr != nil {
			// Surface load failure as a validation error in the per-spec result.
			dirName := filepath.Base(sp)
			specsResults[dirName] = map[string]any{
				"valid":         false,
				"error_count":   1,
				"warning_count": 0,
				"errors": []validationError{{
					File:     dirName,
					Severity: "error",
					Message:  fmt.Sprintf("cannot load spec %s: %s", dirName, loadErr),
				}},
			}
			allValid = false
			continue
		}
		loadedSpecs = append(loadedSpecs, spec)
		metas = append(metas, afspec.SpecMeta{
			SpecID:   spec.SpecID,
			SpecName: spec.SpecName,
			Status:   spec.Status,
			Dir:      sp,
		})
	}

	// Build dependency graph and run library cross-spec validation.
	if len(loadedSpecs) > 0 {
		graph, _ := afspec.BuildDependencyGraph(metas, specDir)
		libResult := afspec.ValidateCrossSpec(loadedSpecs, graph)

		// A CLI-level duplicate-requirement-ID check used to run here too. It
		// is unreachable under format v2: rule C1 ties spec_id to the folder
		// prefix and rule C2 requires every requirement ID to carry that
		// prefix, so two valid specs cannot share one — and for a spec that
		// is invalid, C1 or C2 names the real problem instead.
		var crossFindings []validationError
		for _, entry := range libResult.Errors {
			crossFindings = append(crossFindings, validationError{
				File:     entry.Artifact,
				Severity: "error",
				Message:  entry.Message,
			})
		}
		// Glossary disagreements between specs are warnings (§10.4): they are
		// worth telling the operator about but must not fail the run.
		for _, entry := range libResult.Warnings {
			crossFindings = append(crossFindings, validationError{
				File:     entry.Artifact,
				Severity: "warning",
				Message:  entry.Message,
			})
		}

		if len(crossFindings) > 0 {
			errorCount := len(libResult.Errors)
			if errorCount > 0 {
				allValid = false
			}
			result := map[string]any{
				"valid":         errorCount == 0,
				"error_count":   errorCount,
				"warning_count": len(libResult.Warnings),
			}
			if !short {
				result["errors"] = crossFindings
			}
			specsResults["_cross_spec"] = result
		}
	}

	output := map[string]any{
		"valid": allValid,
		"specs": specsResults,
	}

	return emitMultiSpecResult(w, output, allValid)
}

// validateSpec checks a single spec directory for required files,
// JSON readability, and structural validity. After pre-flight checks
// pass, it loads the spec via afspec.LoadSpec and runs library
// validation (schema + cross-file + warnings) via spec.Validate().
func validateSpec(specPath string) *validationResult {
	result := &validationResult{Valid: true}

	// --- Pre-flight: file existence and JSON readability ---
	preflightFailed := false
	for _, f := range requiredFiles {
		p := filepath.Join(specPath, f)
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				result.Errors = append(result.Errors, validationError{
					File:     f,
					Severity: "error",
					Message:  fmt.Sprintf("required file %s is missing", f),
				})
				result.ErrorCount++
				result.Valid = false
				preflightFailed = true
				continue
			}
			result.Errors = append(result.Errors, validationError{
				File:     f,
				Severity: "error",
				Message:  fmt.Sprintf("cannot read %s: %v", f, err),
			})
			result.ErrorCount++
			result.Valid = false
			preflightFailed = true
			continue
		}

		// prd.md is not JSON — skip JSON parse check.
		if f == "prd.md" {
			if len(data) == 0 {
				result.Errors = append(result.Errors, validationError{
					File:     f,
					Severity: "warning",
					Message:  "prd.md is empty",
				})
				result.WarningCount++
			}
			continue
		}

		// Verify the JSON artifact is valid JSON.
		if !json.Valid(data) {
			result.Errors = append(result.Errors, validationError{
				File:     f,
				Severity: "error",
				Message:  fmt.Sprintf("%s contains malformed JSON", f),
			})
			result.ErrorCount++
			result.Valid = false
			preflightFailed = true
		}
	}

	// If pre-flight failed, return early with clear file-level errors
	// before attempting a full library load.
	if preflightFailed {
		return result
	}

	// --- Library validation: schema + cross-file + warnings ---
	spec, loadErr := afspec.LoadSpec(specPath)
	if loadErr != nil {
		result.Errors = append(result.Errors, validationError{
			File:     filepath.Base(specPath),
			Severity: "error",
			Message:  fmt.Sprintf("cannot load spec: %s", loadErr),
		})
		result.ErrorCount++
		result.Valid = false
		return result
	}

	libResult := spec.Validate()

	// Map library errors to CLI format.
	for _, entry := range libResult.Errors {
		result.Errors = append(result.Errors, validationError{
			File:     entry.Artifact,
			Severity: "error",
			Message:  entry.Message,
		})
		result.ErrorCount++
	}
	if len(libResult.Errors) > 0 {
		result.Valid = false
	}

	// Map library warnings to CLI format.
	for _, entry := range libResult.Warnings {
		result.Errors = append(result.Errors, validationError{
			File:     entry.Artifact,
			Severity: "warning",
			Message:  entry.Message,
		})
		result.WarningCount++
	}

	return result
}

// discoverSpecDirs scans the spec directory for subdirectories matching the
// spec naming pattern (NN_name) and returns their full paths.
func discoverSpecDirs(specDir string) ([]string, error) {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return nil, fmt.Errorf("cannot read spec directory %q: %w", specDir, err)
	}

	var specs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if specDirPattern.MatchString(e.Name()) {
			specs = append(specs, filepath.Join(specDir, e.Name()))
		}
	}
	return specs, nil
}

// shortenResult returns a condensed copy with only valid, error_count, warning_count.
func shortenResult(r *validationResult) map[string]any {
	return map[string]any{
		"valid":         r.Valid,
		"error_count":   r.ErrorCount,
		"warning_count": r.WarningCount,
	}
}

// addCounts returns a result map with error_count and warning_count added.
func addCounts(r *validationResult) map[string]any {
	result := map[string]any{
		"valid":         r.Valid,
		"error_count":   r.ErrorCount,
		"warning_count": r.WarningCount,
	}
	if r.Errors != nil {
		result["errors"] = r.Errors
	} else {
		result["errors"] = []validationError{}
	}
	return result
}

// emitMultiSpecResult writes a multi-spec result with the "specs" wrapper.
// Includes "ok": true only when allValid is true (matching Python emit_ok behavior).
func emitMultiSpecResult(w interface{ Write([]byte) (int, error) }, output map[string]any, allValid bool) error {
	if allValid {
		output["ok"] = true
	}
	if err := emitTo(w, output); err != nil {
		return err
	}
	if !allValid {
		return fmt.Errorf("validation failed")
	}
	return nil
}

// emitValidationResult writes a single-spec validation result to the writer.
// If short is true, only the condensed fields are emitted.
// Includes "ok": true only when validation passes (matching Python emit_ok behavior).
// Returns a non-nil error (for cobra exit code 1) if the result contains errors.
func emitValidationResult(w interface{ Write([]byte) (int, error) }, result *validationResult, short bool) error {
	if short {
		condensed := map[string]any{
			"valid":         result.Valid,
			"error_count":   result.ErrorCount,
			"warning_count": result.WarningCount,
		}
		if result.Valid {
			condensed["ok"] = true
		}
		if err := emitTo(w, condensed); err != nil {
			return err
		}
	} else {
		full := map[string]any{
			"valid":         result.Valid,
			"error_count":   result.ErrorCount,
			"warning_count": result.WarningCount,
			"errors":        result.Errors,
		}
		// Ensure errors is always an array, never null.
		if result.Errors == nil {
			full["errors"] = []validationError{}
		}
		if result.Valid {
			full["ok"] = true
		}
		if err := emitTo(w, full); err != nil {
			return err
		}
	}

	// Exit code 1 (return error) if there are validation errors.
	if result.ErrorCount > 0 {
		return fmt.Errorf("validation failed: %d error(s) found", result.ErrorCount)
	}
	return nil
}
