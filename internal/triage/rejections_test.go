package triage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var rejectionTime = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), RejectionsFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFormatRejectionIsOneLineOfThreeKeys guards 08-REQ-6.1: a refused call is
// one line {ts, problems, unresolved_paths}.
func TestFormatRejectionIsOneLineOfThreeKeys(t *testing.T) {
	line := FormatRejection(rejectionTime, []Problem{
		{Pointer: "/severity", Message: "is not one of critical, high, medium, low"},
		{Pointer: "/affected_files/0/path", Message: `unresolved path "x.go": no such file`, Unresolved: true, Path: "x.go"},
	})

	if !strings.HasSuffix(string(line), "\n") || strings.Count(string(line), "\n") != 1 {
		t.Fatalf("line = %q, want one line ending in a newline", line)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(line, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["ts"] == nil || got["problems"] == nil || got["unresolved_paths"] == nil {
		t.Errorf("keys = %v, want ts, problems and unresolved_paths", got)
	}
	if string(got["unresolved_paths"]) != `["x.go"]` || string(got["ts"]) != `"2026-10-20T08:00:00Z"` {
		t.Errorf("ts %s, unresolved_paths %s", got["ts"], got["unresolved_paths"])
	}

	none := FormatRejection(rejectionTime, []Problem{{Pointer: "/title", Message: "is required"}})
	if !strings.Contains(string(none), `"unresolved_paths":[]`) {
		t.Errorf("a refusal with no unresolved path: %s, want unresolved_paths as an empty list", none)
	}
}

// TestRejectionCountsSeparatePathRefusalsFromAllRefusals_TS_08_40 verifies TS-08-40:
// rejected_path_calls counts the lines with an unresolved path, rejected_submissions
// all of them, and an absent file is 0 and 0 with no warning.
// Verifies: 08-REQ-6.2
func TestRejectionCountsSeparatePathRefusalsFromAllRefusals_TS_08_40(t *testing.T) {
	content := string(FormatRejection(rejectionTime, []Problem{{Pointer: "/affected_files/0/path", Message: `unresolved path "x"`, Unresolved: true, Path: "x"}})) +
		string(FormatRejection(rejectionTime, []Problem{{Pointer: "/severity", Message: "is not one of the four"}})) +
		"\n" // a blank line is not a refusal

	pathCalls, total, warning := CountRejections(writeFile(t, content))

	if pathCalls != 1 || total != 2 || warning != "" {
		t.Errorf("CountRejections = (%d, %d, %q), want (1, 2, none)", pathCalls, total, warning)
	}
	pathCalls, total, warning = CountRejections(filepath.Join(t.TempDir(), "absent.jsonl"))
	if pathCalls != 0 || total != 0 || warning != "" {
		t.Errorf("an absent file: (%d, %d, %q), want (0, 0, none)", pathCalls, total, warning)
	}
}

// TestAnUnreadableRejectionsFileCountsZeroWithAWarning_TS_08_41 verifies TS-08-41:
// a malformed line or a path that cannot be read gives 0 and 0 and the warning
// rejections_unreadable.
// Verifies: 08-REQ-6.3
func TestAnUnreadableRejectionsFileCountsZeroWithAWarning_TS_08_41(t *testing.T) {
	good := string(FormatRejection(rejectionTime, []Problem{{Pointer: "/x", Message: "m", Unresolved: true, Path: "p"}}))
	for name, path := range map[string]string{
		"a malformed line":         writeFile(t, good+"{not json\n"+good),
		"a line that is no object": writeFile(t, good+"[1,2]\n"),
		"a directory":              t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			pathCalls, total, warning := CountRejections(path)
			if pathCalls != 0 || total != 0 || warning != WarnRejectionsUnreadable {
				t.Errorf("CountRejections = (%d, %d, %q), want (0, 0, %s)", pathCalls, total, warning, WarnRejectionsUnreadable)
			}
		})
	}
}
