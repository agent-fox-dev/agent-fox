package afcmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestReadPlaced_TakesTheBriefAndTheRunFromRunJSON(t *testing.T) {
	const nonce = "0123456789abcdef"
	for name, tc := range map[string]struct {
		runFile func(id string) string // no run.json when nil
		brief   string                 // no brief.md when it is "-"
		hook    string                 // no hook.json when empty; a directory when it is "-"
		ok      bool
		usage   bool
	}{
		"no run.json":                     {nil, "PT", "", false, false},
		"no prefix_bytes":                 {func(id string) string { return runJSON(id, "triage", 1, nonce, 0) }, "PT", "", false, false},
		"a run.json of none":              {func(string) string { return "not json" }, "PT", "", false, false},
		"another run":                     {func(string) string { return runJSON("20260101_000000_abcdef", "triage", 1, nonce, 1) }, "PT", "", false, true},
		"another kind":                    {func(id string) string { return runJSON(id, "fix_report", 1, nonce, 1) }, "PT", "", false, true},
		"attempt 0":                       {func(id string) string { return runJSON(id, "triage", 0, nonce, 1) }, "PT", "", false, true},
		"a nonce that is none":            {func(id string) string { return runJSON(id, "triage", 1, "not hex", 1) }, "PT", "", false, true},
		"no brief.md":                     {func(id string) string { return runJSON(id, "triage", 1, nonce, 1) }, "-", "", false, true},
		"a prefix past the brief":         {func(id string) string { return runJSON(id, "triage", 1, nonce, 9) }, "PT", "", false, true},
		"a prefix cut inside a character": {func(id string) string { return runJSON(id, "triage", 1, nonce, 1) }, "éT", "", false, true},
		"a hook.json that cannot be read": {func(id string) string { return runJSON(id, "triage", 1, nonce, 1) }, "PT", "-", false, true},
		"a placed run":                    {func(id string) string { return runJSON(id, "triage", 2, nonce, 1) }, "PT", `{"run_id":"r","protected_paths":[".agent-fox/"]}`, true, false},
	} {
		t.Run(name, func(t *testing.T) {
			rd, err := toolio.NewRunDir(t.TempDir(), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.runFile != nil {
				testutil.WriteFile(t, rd.Path, "run.json", tc.runFile(rd.ID))
			}
			if tc.brief != "-" {
				testutil.WriteFile(t, rd.Path, "brief.md", tc.brief)
			}
			switch tc.hook {
			case "":
			case "-":
				if err := os.Mkdir(filepath.Join(rd.Path, "hook.json"), 0o755); err != nil {
					t.Fatal(err)
				}
			default:
				testutil.WriteFile(t, rd.Path, "hook.json", tc.hook)
			}

			placed, ok, err := readPlaced(rd, "triage")

			var failure *toolio.Failure
			if ok != tc.ok || tc.usage != (errors.As(err, &failure) && failure.Category == toolio.CategoryUsage) || !tc.usage && err != nil {
				t.Fatalf("placed %v, error %v; want placed %v and a usage failure: %v", ok, err, tc.ok, tc.usage)
			}
			if tc.ok && (placed.Prefix != "P" || placed.Task != "T" || placed.Run.Attempt != 2 || placed.Hook == nil || placed.Hook.RunID != "r") {
				t.Errorf("placed = %+v, want the prefix P, the task T, attempt 2 and the hook", placed)
			}
		})
	}
}

// runJSON is a run.json.
func runJSON(id, kind string, attempt int, nonce string, prefix int) string {
	return fmt.Sprintf(`{"run_id":%q,"kind":%q,"attempt":%d,"nonce":%q,"prefix_bytes":%d}`, id, kind, attempt, nonce, prefix)
}
