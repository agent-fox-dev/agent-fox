package afspec

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// TS-05-24: LintFinding struct is exported with all required fields:
// SpecName, File, Rule, Severity, Message, and Line.
// Requirement: 05-REQ-6.1
// ---------------------------------------------------------------------------

func TestLintFinding_AllFields(t *testing.T) {
	f := LintFinding{
		SpecName: "my_spec",
		File:     "requirements.json",
		Rule:     "missing-title",
		Severity: "error",
		Message:  "Requirement has no title",
		Line:     10,
	}

	if f.SpecName != "my_spec" {
		t.Errorf("SpecName = %q, want %q", f.SpecName, "my_spec")
	}
	if f.File != "requirements.json" {
		t.Errorf("File = %q, want %q", f.File, "requirements.json")
	}
	if f.Rule != "missing-title" {
		t.Errorf("Rule = %q, want %q", f.Rule, "missing-title")
	}
	if f.Severity != "error" {
		t.Errorf("Severity = %q, want %q", f.Severity, "error")
	}
	if f.Message != "Requirement has no title" {
		t.Errorf("Message = %q, want %q", f.Message, "Requirement has no title")
	}
	if f.Line != 10 {
		t.Errorf("Line = %d, want %d", f.Line, 10)
	}
}

// ---------------------------------------------------------------------------
// TS-05-25: LintSpecInfo struct is exported with all required fields:
// Name, Prefix, Path, HasTasks, and HasPRD.
// Requirement: 05-REQ-6.2
// ---------------------------------------------------------------------------

func TestLintSpecInfo_AllFields(t *testing.T) {
	info := LintSpecInfo{
		Name:     "my_spec",
		Prefix:   5,
		Path:     "/specs/05-my_spec",
		HasTasks: true,
		HasPRD:   false,
	}

	if info.Name != "my_spec" {
		t.Errorf("Name = %q, want %q", info.Name, "my_spec")
	}
	if info.Prefix != 5 {
		t.Errorf("Prefix = %d, want %d", info.Prefix, 5)
	}
	if info.Path != "/specs/05-my_spec" {
		t.Errorf("Path = %q, want %q", info.Path, "/specs/05-my_spec")
	}
	if info.HasTasks != true {
		t.Errorf("HasTasks = %v, want %v", info.HasTasks, true)
	}
	if info.HasPRD != false {
		t.Errorf("HasPRD = %v, want %v", info.HasPRD, false)
	}
}

// ---------------------------------------------------------------------------
// TS-05-26: LintResult struct is exported with fields Findings
// ([]LintFinding) and ExitCode (int).
// Requirement: 05-REQ-6.3
// ---------------------------------------------------------------------------

func TestLintResult_Fields(t *testing.T) {
	r := LintResult{
		Findings: []LintFinding{{Severity: "warning"}},
		ExitCode: 0,
	}

	if len(r.Findings) != 1 {
		t.Errorf("len(Findings) = %d, want 1", len(r.Findings))
	}
	if r.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", r.ExitCode)
	}
}

// ---------------------------------------------------------------------------
// TS-05-52: LintFinding Severity field only accepts 'error', 'warning', or
// 'hint'; callers treat any other value as 'hint'.
// Requirement: 05-REQ-6.E1
// ---------------------------------------------------------------------------

func TestLintFinding_InvalidSeverityTreatedAsHint(t *testing.T) {
	defer requireImplemented(t)

	// ComputeExitCode should NOT treat an invalid severity like "critical" as
	// an error — only explicit "error" severity should cause exit code 1.
	f := LintFinding{Severity: "critical"}
	code := ComputeExitCode([]LintFinding{f})
	if code != 0 {
		t.Errorf("ComputeExitCode with Severity %q = %d, want 0", f.Severity, code)
	}

	// SortFindings should treat an invalid severity the same as "hint"
	// in sort order: error < warning < hint (where unknown maps to hint).
	sorted := SortFindings([]LintFinding{
		{SpecName: "a", File: "a.json", Severity: "critical"},
		{SpecName: "a", File: "a.json", Severity: "error"},
	})
	if sorted[0].Severity != "error" {
		t.Errorf("SortFindings: first element Severity = %q, want %q", sorted[0].Severity, "error")
	}
}

// ---------------------------------------------------------------------------
// TS-05-27: DiscoverLintSpecs with a valid specsDir and empty filterSpec
// returns LintSpecInfo entries for all subdirectories containing
// requirements.json, with HasTasks and HasPRD set correctly.
// Requirement: 05-REQ-7.1
// ---------------------------------------------------------------------------

func TestDiscoverLintSpecs_AllSpecs(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()

	// 05_my_spec: has requirements.json and tasks.json, no prd.md
	dir05 := filepath.Join(specsDir, "05_my_spec")
	mustMkdir(t, dir05)
	mustWriteFile(t, filepath.Join(dir05, "requirements.json"), "{}")
	mustWriteFile(t, filepath.Join(dir05, "tasks.json"), "{}")

	// 06_other_spec: has requirements.json and prd.md, no tasks.json
	dir06 := filepath.Join(specsDir, "06_other_spec")
	mustMkdir(t, dir06)
	mustWriteFile(t, filepath.Join(dir06, "requirements.json"), "{}")
	mustWriteFile(t, filepath.Join(dir06, "prd.md"), "")

	// not_a_spec: has requirements.json but invalid dir name format
	dirBad := filepath.Join(specsDir, "not_a_spec")
	mustMkdir(t, dirBad)
	mustWriteFile(t, filepath.Join(dirBad, "requirements.json"), "{}")

	infos, err := DiscoverLintSpecs(specsDir, "")
	if err != nil {
		t.Fatalf("DiscoverLintSpecs returned unexpected error: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("len(infos) = %d, want 2", len(infos))
	}

	// Check that we have my_spec and other_spec entries with correct flags.
	foundMySpec := false
	foundOther := false
	for _, info := range infos {
		switch info.Name {
		case "my_spec":
			foundMySpec = true
			if !info.HasTasks {
				t.Errorf("my_spec: HasTasks = false, want true")
			}
			if info.HasPRD {
				t.Errorf("my_spec: HasPRD = true, want false")
			}
		case "other_spec":
			foundOther = true
			if info.HasTasks {
				t.Errorf("other_spec: HasTasks = true, want false")
			}
			if !info.HasPRD {
				t.Errorf("other_spec: HasPRD = false, want true")
			}
		default:
			t.Errorf("unexpected spec name: %q", info.Name)
		}
	}
	if !foundMySpec {
		t.Error("did not find entry with Name 'my_spec'")
	}
	if !foundOther {
		t.Error("did not find entry with Name 'other_spec'")
	}
}

// ---------------------------------------------------------------------------
// TS-05-28: DiscoverLintSpecs with a non-empty filterSpec returns only the
// matching LintSpecInfo entry, or an error if no match is found.
// Requirement: 05-REQ-7.2
// ---------------------------------------------------------------------------

func TestDiscoverLintSpecs_FilterSpec(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()

	dir05 := filepath.Join(specsDir, "05_my_spec")
	mustMkdir(t, dir05)
	mustWriteFile(t, filepath.Join(dir05, "requirements.json"), "{}")

	dir06 := filepath.Join(specsDir, "06_other_spec")
	mustMkdir(t, dir06)
	mustWriteFile(t, filepath.Join(dir06, "requirements.json"), "{}")

	// Filter for "my_spec" — should return exactly one entry.
	infos, err := DiscoverLintSpecs(specsDir, "my_spec")
	if err != nil {
		t.Fatalf("DiscoverLintSpecs with filterSpec 'my_spec' returned error: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len(infos) = %d, want 1", len(infos))
	}
	if infos[0].Name != "my_spec" {
		t.Errorf("infos[0].Name = %q, want %q", infos[0].Name, "my_spec")
	}

	// Filter for "nonexistent" — should return error.
	infos2, err2 := DiscoverLintSpecs(specsDir, "nonexistent")
	if err2 == nil {
		t.Fatal("DiscoverLintSpecs with filterSpec 'nonexistent' returned nil error, want non-nil")
	}
	if infos2 != nil {
		t.Errorf("infos should be nil for non-matching filter, got %v", infos2)
	}
}

// ---------------------------------------------------------------------------
// TS-05-53: DiscoverLintSpecs returns nil and a descriptive error when
// specsDir does not exist or is not a directory.
// Requirement: 05-REQ-7.E1
// ---------------------------------------------------------------------------

func TestDiscoverLintSpecs_NonexistentDir(t *testing.T) {
	defer requireImplemented(t)

	infos, err := DiscoverLintSpecs("/nonexistent/path/to/specs", "")
	if err == nil {
		t.Fatal("DiscoverLintSpecs with nonexistent dir returned nil error, want non-nil")
	}
	if infos != nil {
		t.Errorf("infos should be nil, got %v", infos)
	}
}

// ---------------------------------------------------------------------------
// TS-05-54: DiscoverLintSpecs returns nil and a descriptive error when
// specsDir exists but contains no subdirectories with requirements.json.
// Requirement: 05-REQ-7.E2
// ---------------------------------------------------------------------------

func TestDiscoverLintSpecs_EmptyDir(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()

	infos, err := DiscoverLintSpecs(specsDir, "")
	if err == nil {
		t.Fatal("DiscoverLintSpecs with empty dir returned nil error, want non-nil")
	}
	if infos != nil {
		t.Errorf("infos should be nil, got %v", infos)
	}
}

// ---------------------------------------------------------------------------
// TS-05-55: DiscoverLintSpecs skips a subdirectory where IsSpecDirName
// returns true but ParseSpecDirName returns ok=false, and continues scanning
// without returning an error.
// Requirement: 05-REQ-7.E3
// ---------------------------------------------------------------------------

func TestDiscoverLintSpecs_UnparseableDirSkipped(t *testing.T) {
	defer requireImplemented(t)

	specsDir := t.TempDir()

	// Valid spec dir that will parse successfully.
	dir05 := filepath.Join(specsDir, "05_my_spec")
	mustMkdir(t, dir05)
	mustWriteFile(t, filepath.Join(dir05, "requirements.json"), "{}")

	// The spec says: "IF a subdirectory name passes IsSpecDirName but
	// ParseSpecDirName returns ok=false, THEN skip." In practice with the
	// current dirname.go implementation, IsSpecDirName and ParseSpecDirName
	// use the same regex so this case cannot naturally occur. We still verify
	// that a directory not passing IsSpecDirName is skipped and the valid
	// spec is returned.
	dirBad := filepath.Join(specsDir, "bad-name")
	mustMkdir(t, dirBad)
	mustWriteFile(t, filepath.Join(dirBad, "requirements.json"), "{}")

	infos, err := DiscoverLintSpecs(specsDir, "")
	if err != nil {
		t.Fatalf("DiscoverLintSpecs returned unexpected error: %v", err)
	}
	if len(infos) != 1 {
		t.Fatalf("len(infos) = %d, want 1", len(infos))
	}
	if infos[0].Name != "my_spec" {
		t.Errorf("infos[0].Name = %q, want %q", infos[0].Name, "my_spec")
	}
}

// ---------------------------------------------------------------------------
// TS-05-29: SortFindings returns a new slice sorted by SpecName ascending,
// then File ascending, then Severity in order error < warning < hint,
// without modifying the input.
// Requirement: 05-REQ-8.1
// ---------------------------------------------------------------------------

func TestSortFindings_Order(t *testing.T) {
	defer requireImplemented(t)

	input := []LintFinding{
		{SpecName: "b_spec", File: "a.json", Severity: "hint"},
		{SpecName: "a_spec", File: "z.json", Severity: "warning"},
		{SpecName: "a_spec", File: "a.json", Severity: "hint"},
		{SpecName: "a_spec", File: "a.json", Severity: "error"},
	}

	// Save a copy to verify input is unchanged.
	inputCopy := make([]LintFinding, len(input))
	copy(inputCopy, input)

	sorted := SortFindings(input)

	// Expected sort order:
	// 1. a_spec, a.json, error   (error=0)
	// 2. a_spec, a.json, hint    (hint=2)
	// 3. a_spec, z.json, warning (warning=1)
	// 4. b_spec, a.json, hint    (hint=2)
	expected := []struct {
		specName string
		file     string
		severity string
	}{
		{"a_spec", "a.json", "error"},
		{"a_spec", "a.json", "hint"},
		{"a_spec", "z.json", "warning"},
		{"b_spec", "a.json", "hint"},
	}

	if len(sorted) != len(expected) {
		t.Fatalf("len(sorted) = %d, want %d", len(sorted), len(expected))
	}

	for i, want := range expected {
		got := sorted[i]
		if got.SpecName != want.specName || got.File != want.file || got.Severity != want.severity {
			t.Errorf("sorted[%d] = {%q, %q, %q}, want {%q, %q, %q}",
				i, got.SpecName, got.File, got.Severity,
				want.specName, want.file, want.severity)
		}
	}

	// Verify input was not modified.
	for i := range input {
		if input[i] != inputCopy[i] {
			t.Errorf("input[%d] was modified: got %v, want %v", i, input[i], inputCopy[i])
		}
	}
}

// ---------------------------------------------------------------------------
// TS-05-30: ComputeExitCode returns 1 when at least one finding has
// Severity 'error'.
// Requirement: 05-REQ-8.2
// ---------------------------------------------------------------------------

func TestComputeExitCode_WithError(t *testing.T) {
	defer requireImplemented(t)

	findings := []LintFinding{
		{Severity: "warning"},
		{Severity: "error"},
		{Severity: "hint"},
	}

	code := ComputeExitCode(findings)
	if code != 1 {
		t.Errorf("ComputeExitCode = %d, want 1", code)
	}
}

// ---------------------------------------------------------------------------
// TS-05-31: ComputeExitCode returns 0 when no finding has Severity 'error'.
// Requirement: 05-REQ-8.3
// ---------------------------------------------------------------------------

func TestComputeExitCode_NoError(t *testing.T) {
	defer requireImplemented(t)

	findings := []LintFinding{
		{Severity: "warning"},
		{Severity: "hint"},
	}

	code := ComputeExitCode(findings)
	if code != 0 {
		t.Errorf("ComputeExitCode = %d, want 0", code)
	}
}

// ---------------------------------------------------------------------------
// TS-05-56: SortFindings called with an empty slice returns an empty slice
// without panicking.
// Requirement: 05-REQ-8.E1
// ---------------------------------------------------------------------------

func TestSortFindings_Empty(t *testing.T) {
	defer requireImplemented(t)

	result := SortFindings([]LintFinding{})
	if len(result) != 0 {
		t.Errorf("len(SortFindings(empty)) = %d, want 0", len(result))
	}
}

// ---------------------------------------------------------------------------
// TS-05-57: ComputeExitCode called with an empty slice returns 0.
// Requirement: 05-REQ-8.E2
// ---------------------------------------------------------------------------

func TestComputeExitCode_Empty(t *testing.T) {
	defer requireImplemented(t)

	code := ComputeExitCode([]LintFinding{})
	if code != 0 {
		t.Errorf("ComputeExitCode(empty) = %d, want 0", code)
	}
}

// ---------------------------------------------------------------------------
// Test helpers for lint tests
// ---------------------------------------------------------------------------

// mustMkdir creates a directory and fails the test if it cannot.
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("failed to create directory %q: %v", path, err)
	}
}

// mustWriteFile writes content to a file and fails the test if it cannot.
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write file %q: %v", path, err)
	}
}
