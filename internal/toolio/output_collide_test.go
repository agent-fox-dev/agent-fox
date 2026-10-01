package toolio

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-08-19 (unit): --output and --report-file are compared as resolved,
// absolute, cleaned paths, without resolving symlinks.
func TestTS08_19_CollisionComparesCleanedAbsolutePaths(t *testing.T) {
	a, b := "a/../a/out.json", "a/out.json"
	if !configuredDestinationsCollide(a, b) {
		t.Errorf("%q and %q should collide", a, b)
	}
	if configuredDestinationsCollide("a/out.json", "a/other.json") {
		t.Error("different files must not collide")
	}
	if configuredDestinationsCollide("", "a/out.json") || configuredDestinationsCollide("a/out.json", "") || configuredDestinationsCollide("", "") {
		t.Error("an empty destination never collides")
	}

	// Symlinks are not resolved: a link and its target are two destinations.
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "link.json")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if configuredDestinationsCollide(target, link) {
		t.Error("a symlink and its target must not be reported as colliding")
	}
}

// TS-08-20 (property): the collision verdict depends only on the two
// configured, cleaned paths, never on whether either write succeeds.
func TestTS08_20_CollisionDependsOnlyOnConfiguredPaths(t *testing.T) {
	rng := rand.New(rand.NewSource(8))
	parts := []string{"a", "b", "..", ".", "out.json", "x"}
	gen := func() string {
		n := 1 + rng.Intn(4)
		segs := make([]string, n)
		for i := range segs {
			segs[i] = parts[rng.Intn(len(parts))]
		}
		p := strings.Join(segs, "/")
		if rng.Intn(3) == 0 {
			p = "/" + p
		}
		return p
	}
	abs := func(p string) string {
		x, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Clean(x)
	}
	collisions := 0
	for i := 0; i < 500; i++ {
		a, b := gen(), gen()
		if i%5 == 0 {
			b = a // guarantee plenty of colliding pairs
		}
		want := abs(a) == abs(b)
		if want {
			collisions++
		}
		if got := configuredDestinationsCollide(a, b); got != want {
			t.Fatalf("collide(%q, %q) = %v, want %v", a, b, got, want)
		}
	}
	if collisions == 0 {
		t.Fatal("generator never produced a colliding pair")
	}

	// The verdict reported by a whole run is the same whether the shared
	// destination's write succeeds or fails.
	good := filepath.Join(t.TempDir(), "p.json")
	for name, p := range map[string]string{"write ok": good, "write fails": unwritableOutput(t)} {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		app, _ := newApp(t, nil)
		env, _, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--output", p, "--report-file", p, "x"}, "")
		if outputWarning(env.Warnings, "output_matches_report_file") == nil {
			t.Errorf("%s: no output_matches_report_file warning", name)
		}
	}
	// And a run with distinct destinations never reports one.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	app, _ := newApp(t, nil)
	env, _, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--output", filepath.Join(t.TempDir(), "o.json"), "--report-file", filepath.Join(t.TempDir(), "r.json"), "x"}, "")
	if outputWarning(env.Warnings, "output_matches_report_file") != nil {
		t.Error("distinct destinations reported a collision")
	}
}

// collideResult has a trimmed summary view distinct from its full value, so a
// test can tell which of the two landed in a file.
type collideResult struct {
	Stage string `json:"stage"`
	Full  string `json:"full_only,omitempty"`
}

func (r *collideResult) SetDetail(string) {}
func (r *collideResult) SummaryView() any {
	return map[string]string{"stage": r.Stage}
}

// TS-08-21 (integration): on a configured collision --output's own write is
// skipped and the complete report lands, with a warning naming the collision.
func TestTS08_21_CollisionSkipsOutputWriteAndKeepsFullReport(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := filepath.Join(t.TempDir(), "shared.json")

	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, &collideResult{Stage: "done", Full: "FULL-ONLY-MARKER"}, nil
	})
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--detail", "summary", "--output", p, "--report-file", p, "x"}, "")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "FULL-ONLY-MARKER") {
		t.Errorf("the file at the shared path is not the complete report:\n%s", b)
	}
	rb, _ := json.Marshal(env.Result)
	if strings.Contains(string(rb), "FULL-ONLY-MARKER") {
		t.Errorf("stdout should still carry the summary view: %+v", env.Result)
	}
	w := outputWarning(env.Warnings, "output_matches_report_file")
	if w == nil {
		t.Fatalf("no output_matches_report_file warning: %+v", env.Warnings)
	}
	if w.Severity != "low" || w.Stage != "emit" {
		t.Errorf("warning = %+v, want low/emit", *w)
	}
	if !strings.Contains(w.Message, p) {
		t.Errorf("message %q does not name the path", w.Message)
	}
	if outputWarning(env.Warnings, "output_not_written") != nil {
		t.Error("an output_not_written warning appeared: a write was attempted")
	}
	// No leftover temp files: only the shared file is in the directory.
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("directory has %d entries, want 1", len(entries))
	}
}

// TS-08-22 (integration): when the collided destination's report-file write
// fails, nothing lands there and both causes are named.
func TestTS08_22_CollisionWithFailedReportWriteNamesBothCauses(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := unwritableOutput(t)

	app, _ := newApp(t, nil)
	env, code, _ := runApp(t, app, []string{"--dir", t.TempDir(), "--output", p, "--report-file", p, "x"}, "")
	if _, err := os.Stat(p); err == nil {
		t.Error("a file exists at the shared path")
	}
	if outputWarning(env.Warnings, "output_matches_report_file") == nil {
		t.Errorf("no output_matches_report_file warning: %+v", env.Warnings)
	}
	if outputWarning(env.Warnings, "report_file_not_written") == nil {
		t.Errorf("no report_file_not_written warning: %+v", env.Warnings)
	}
	if outputWarning(env.Warnings, "output_not_written") != nil {
		t.Error("--output's own write was attempted")
	}
	if code != ExitOK || !env.OK || env.ExitCode != ExitOK {
		t.Errorf("code=%d ok=%v exit_code=%d, want unaffected", code, env.OK, env.ExitCode)
	}
}

// TS-08-23 (unit): the central WarnCode table maps output_matches_report_file
// to stage emit.
func TestTS08_23_WarnTableMapsOutputMatchesReportFile(t *testing.T) {
	stage, ok := WarnStage(WarnCode("output_matches_report_file"))
	if !ok {
		t.Fatal("the WarnCode table has no output_matches_report_file entry")
	}
	if stage != "emit" {
		t.Errorf("stage = %q, want emit", stage)
	}
	found := false
	for _, c := range DeclaredWarnCodes() {
		if c == WarnCode("output_matches_report_file") {
			found = true
		}
	}
	if !found {
		t.Error("DeclaredWarnCodes() does not list output_matches_report_file")
	}
}
