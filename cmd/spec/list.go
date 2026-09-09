package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"

	"github.com/spf13/cobra"
)

// specDirPattern matches spec subdirectory names: one or more digits
// followed by an underscore and at least one more character.
var specDirPattern = regexp.MustCompile(`^\d+_`)

// newListCmd creates the "spec list" subcommand which lists all specs
// and their current states.
func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all specs and their current states",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")

			specs := listSpecs(specDir)

			return emitOKTo(cmd.OutOrStdout(), "spec_dir", specDir, "specs", specs)
		},
	}

	return cmd
}

// specEntry represents a single spec in the list output.
type specEntry struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

// listSpecs scans the spec directory for subdirectories matching the
// spec naming pattern (NN_name), reads each _session.json for the state
// field, and returns the list. Defaults state to "no_session" if
// _session.json is missing or malformed. Returns an empty slice (not nil)
// if the directory doesn't exist or has no matching entries.
func listSpecs(specDir string) []specEntry {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		// Directory doesn't exist or can't be read — return empty list.
		return []specEntry{}
	}

	var specs []specEntry
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !specDirPattern.MatchString(name) {
			continue
		}

		state := readSpecState(filepath.Join(specDir, name))
		specs = append(specs, specEntry{Name: name, State: state})
	}

	if specs == nil {
		specs = []specEntry{}
	}
	return specs
}

// readSpecState reads the "state" field from _session.json in the given
// spec directory. Returns "no_session" if the file is missing, unreadable,
// or contains malformed JSON.
func readSpecState(specPath string) string {
	data, err := os.ReadFile(filepath.Join(specPath, "_session.json"))
	if err != nil {
		return "no_session"
	}

	var session map[string]any
	if err := json.Unmarshal(data, &session); err != nil {
		return "no_session"
	}

	state, ok := session["state"].(string)
	if !ok || state == "" {
		return "no_session"
	}
	return state
}
