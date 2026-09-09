package toolio

import (
	"bytes"
	"encoding/json"
	"flag"
	"strings"
	"testing"
)

func TestEnvelopeOKOnlyWhenExitIsZero(t *testing.T) {
	r := NewRun("issue", "v1")
	if env := r.Envelope(ExitOK, map[string]string{"a": "b"}, nil); !env.OK {
		t.Error("exit 0 must be ok")
	}
	for _, code := range []int{ExitFailed, ExitUsage, ExitNeedsHuman, ExitUnverified} {
		if env := r.Envelope(code, nil, &ErrorInfo{Stage: "s", Category: "c", Message: "m"}); env.OK {
			t.Errorf("exit %d reported ok", code)
		}
	}
}

// An envelope that says ok and carries an error is a contradiction the caller
// should never have to resolve.
func TestEnvelopeRefusesOKWithAnError(t *testing.T) {
	r := NewRun("fix", "v1")
	env := r.Envelope(ExitOK, nil, &ErrorInfo{Stage: "verify", Category: "git", Message: "boom"})
	if env.OK || env.ExitCode != ExitFailed {
		t.Errorf("got ok=%v exit=%d, want a failure", env.OK, env.ExitCode)
	}
}

func TestEnvelopeSumsPhases(t *testing.T) {
	r := NewRun("spec", "v1")
	r.AddPhase(PhaseInfo{Name: "prd", Turns: 3, InputTokens: 100, OutputTokens: 20, CostUSD: 0.5})
	r.AddPhase(PhaseInfo{Name: "requirements", Turns: 2, InputTokens: 50, OutputTokens: 10, CostUSD: 0.25})

	env := r.Envelope(ExitOK, nil, nil)
	if env.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if env.Usage.Turns != 5 || env.Usage.InputTokens != 150 || env.Usage.OutputTokens != 30 {
		t.Errorf("Usage = %+v", env.Usage)
	}
	if env.Usage.CostUSD != 0.75 {
		t.Errorf("CostUSD = %v", env.Usage.CostUSD)
	}
	if len(env.Usage.Phases) != 2 {
		t.Errorf("phases = %d", len(env.Usage.Phases))
	}
}

// Exactly one JSON object, on every path, is the contract: a caller that has
// to parse two formats and guess which one it got is not being served.
func TestEmitWritesOneJSONObject(t *testing.T) {
	var buf bytes.Buffer
	r := NewRun("issue", "v1")
	r.Warn("something to note")

	code := Emit(&buf, r.Envelope(ExitFailed, nil, &ErrorInfo{
		Stage: "analyse", Category: "budget", Message: "over budget",
	}))
	if code != ExitFailed {
		t.Errorf("Emit returned %d", code)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, buf.String())
	}
	if env.Tool != "issue" || env.OK || env.ExitCode != ExitFailed {
		t.Errorf("envelope = %+v", env)
	}
	if env.Error == nil || env.Error.Category != "budget" {
		t.Errorf("Error = %+v", env.Error)
	}
	if len(env.Warnings) != 1 {
		t.Errorf("Warnings = %v", env.Warnings)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("the object should end with a newline")
	}
}

// A result that cannot be marshalled must not lose the exit code.
func TestEmitFallsBackWhenTheResultCannotEncode(t *testing.T) {
	var buf bytes.Buffer
	r := NewRun("fix", "v1")
	code := Emit(&buf, r.Envelope(ExitOK, map[string]any{"bad": make(chan int)}, nil))
	if code != ExitFailed {
		t.Errorf("Emit returned %d, want a failure", code)
	}
	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("the fallback is not valid JSON: %v", err)
	}
	if env.Error == nil || env.Error.Stage != "emit" {
		t.Errorf("Error = %+v", env.Error)
	}
}

func TestSortedUnique(t *testing.T) {
	got := SortedUnique([]string{"b", "a", "b", "", "  ", " c "})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A report is frequently a multi-word string, and requiring it last is a
// papercut a caller hits every time.
func TestSplitArgsAcceptsFlagsOnEitherSide(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *bool) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(bytes.NewBuffer(nil))
		repo := fs.String("repo", "", "")
		dry := fs.Bool("dry-run", false, "")
		return fs, repo, dry
	}

	cases := [][]string{
		{"--repo", "a/b", "--dry-run", "the report"},
		{"the report", "--repo", "a/b", "--dry-run"},
		{"--repo=a/b", "the report", "--dry-run"},
		{"--dry-run", "the report", "--repo", "a/b"},
	}
	for _, argv := range cases {
		fs, repo, dry := newFS()
		got, err := SplitArgs(fs, argv)
		if err != nil {
			t.Fatalf("SplitArgs(%v): %v", argv, err)
		}
		if got != "the report" || *repo != "a/b" || !*dry {
			t.Errorf("SplitArgs(%v) = %q, repo=%q, dry=%v", argv, got, *repo, *dry)
		}
	}
}

func TestSplitArgsRejectsTwoPositionals(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	if _, err := SplitArgs(fs, []string{"one", "two"}); err == nil {
		t.Fatal("want an error naming the extra input")
	}
}

func TestSplitArgsKeepsBareDashAsStdin(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	got, err := SplitArgs(fs, []string{"--dry-run", "-"})
	if err != nil {
		t.Fatalf("SplitArgs: %v", err)
	}
	if got != "-" {
		t.Errorf("got %q, want -", got)
	}
}

// Everything after -- is the input, even when it looks like a flag.
func TestSplitArgsHonoursDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	got, err := SplitArgs(fs, []string{"--dry-run", "--", "--not-a-flag"})
	if err != nil {
		t.Fatalf("SplitArgs: %v", err)
	}
	if got != "--not-a-flag" {
		t.Errorf("got %q", got)
	}
}
