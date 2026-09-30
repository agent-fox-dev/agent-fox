package toolio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findWarning(ws []Warning, code WarnCode) (Warning, bool) {
	for _, w := range ws {
		if w.Code == code {
			return w, true
		}
	}
	return Warning{}, false
}

// TS-06-40 (unit): A single-line, whitespace-free, path-shaped argument that
// does not exist is flagged with a high-severity warning naming it.
func TestTS06_40_MistypedPathIsFlagged(t *testing.T) {
	for _, arg := range []string{"widget/report.txt", "report.txt", "nope/"} {
		run := NewRun("tool", "test")
		in, err := Resolve(context.Background(), arg, nil, nil, run)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", arg, err)
		}
		if in.Kind != KindText {
			t.Fatalf("Resolve(%q).Kind = %s, want text", arg, in.Kind)
		}
		w, ok := findWarning(run.Warnings(), WarnInputLooksLikePath)
		if !ok {
			t.Fatalf("Resolve(%q): no input_looks_like_path warning: %+v", arg, run.Warnings())
		}
		if w.Severity != "high" || w.Stage != "input" {
			t.Errorf("warning = %+v, want severity high, stage input", w)
		}
		if !strings.Contains(w.Message, arg) {
			t.Errorf("message %q does not name %q", w.Message, arg)
		}
	}
}

// TS-06-40 (unit): prose, multi-line input and whitespace never look like a
// path.
func TestTS06_40_ProseIsNotFlagged(t *testing.T) {
	for _, arg := range []string{
		"the widget counter double-counts on retry",
		"see widget/report.txt for details",
		"widget/report.txt\nsecond line",
		"version.1234567",
		"plainword",
		"end.",
	} {
		run := NewRun("tool", "test")
		if _, err := Resolve(context.Background(), arg, nil, nil, run); err != nil {
			t.Fatalf("Resolve(%q): %v", arg, err)
		}
		if _, ok := findWarning(run.Warnings(), WarnInputLooksLikePath); ok {
			t.Errorf("Resolve(%q) flagged as a path", arg)
		}
	}
}

// TS-06-41 (unit): An existing directory argument does not trigger the
// mistyped-path warning.
func TestTS06_41_ExistingDirectoryIsNotFlagged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "src")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{dir, dir + "/"} {
		run := NewRun("tool", "test")
		in, err := Resolve(context.Background(), arg, nil, nil, run)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", arg, err)
		}
		if in.Kind != KindText {
			t.Fatalf("Kind = %s, want text", in.Kind)
		}
		if _, ok := findWarning(run.Warnings(), WarnInputLooksLikePath); ok {
			t.Errorf("existing directory %q was flagged", arg)
		}
	}
}

// TS-06-42 (unit): The warning is in top-level warnings whatever --detail
// says, and appends the deterministic summary clause on an ok run.
func TestTS06_42_WarningPresentUnderEveryDetail(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	for _, detail := range []string{"summary", "full"} {
		app, _ := newApp(t, nil)
		env, code, _ := runApp(t, app, []string{
			"--dir", t.TempDir(), "--detail", detail,
			"--report-file", filepath.Join(t.TempDir(), "r.json"),
			"widget/report.txt",
		}, "")
		if code != ExitOK || !env.OK {
			t.Fatalf("detail %s: code=%d env=%+v", detail, code, env)
		}
		w, ok := findWarning(env.Warnings, WarnInputLooksLikePath)
		if !ok {
			t.Fatalf("detail %s: no input_looks_like_path in %+v", detail, env.Warnings)
		}
		if w.Severity != "high" || !strings.Contains(w.Message, "widget/report.txt") {
			t.Errorf("detail %s: warning = %+v", detail, w)
		}
		if !strings.Contains(env.Summary, "high-severity warning") {
			t.Errorf("detail %s: summary %q lacks the high-severity clause", detail, env.Summary)
		}
	}
}
