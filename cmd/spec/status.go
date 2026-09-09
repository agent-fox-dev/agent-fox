package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// newStatusCmd creates the "spec status" subcommand which checks the
// current state of a spec session.
func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status SPEC",
		Short: "Check the current state of a spec session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			w := cmd.OutOrStdout()

			// Resolve the spec directory.
			specPath, err := resolveSpec(specDir, args[0])
			if err != nil {
				return err
			}

			// Read _session.json (read-only — no mutations).
			sessionPath := filepath.Join(specPath, "_session.json")
			data, err := os.ReadFile(sessionPath)
			if err != nil {
				// Missing _session.json → report no_session.
				return emitStatusNoSession(w)
			}

			var session map[string]any
			if jsonErr := json.Unmarshal(data, &session); jsonErr != nil {
				// Malformed _session.json → report no_session.
				return emitStatusNoSession(w)
			}

			return emitStatusFromSession(w, session)
		},
	}

	return cmd
}

// emitStatusNoSession emits the no-session status response:
// {ok:true, state:"no_session", has_assessment:false, generated_artifacts:[]}.
func emitStatusNoSession(w interface{ Write([]byte) (int, error) }) error {
	result := map[string]any{
		"ok":                  true,
		"state":               "no_session",
		"has_assessment":      false,
		"generated_artifacts": []string{},
	}
	return emitTo(w, result)
}

// emitStatusFromSession builds the status JSON from a parsed _session.json.
// Required fields: ok, state, has_assessment, generated_artifacts.
// Optional fields: last_error, quality (included only when present).
func emitStatusFromSession(w interface{ Write([]byte) (int, error) }, session map[string]any) error {
	// Extract state (default to "no_session" if missing).
	state, _ := session["state"].(string)
	if state == "" {
		state = "no_session"
	}

	// Determine has_assessment from assessment_history.
	hasAssessment := false
	if ah, ok := session["assessment_history"].([]any); ok && len(ah) > 0 {
		hasAssessment = true
	}

	// Extract generated_artifacts.
	var artifacts []string
	if ga, ok := session["generated_artifacts"].([]any); ok {
		for _, item := range ga {
			if s, ok := item.(string); ok {
				artifacts = append(artifacts, s)
			}
		}
	}
	if artifacts == nil {
		artifacts = []string{}
	}

	// Build the result.
	result := map[string]any{
		"ok":                  true,
		"state":               state,
		"has_assessment":      hasAssessment,
		"generated_artifacts": artifacts,
	}

	// Include optional fields only when present in session.
	if lastError, ok := session["last_error"]; ok {
		if le, ok := lastError.(string); ok && le != "" {
			result["last_error"] = le
		}
	}
	if quality, ok := session["quality"]; ok {
		if q, ok := quality.(string); ok && q != "" {
			result["quality"] = q
		}
	}

	return emitTo(w, result)
}
