package codefix

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-37 (unit): fix's ambiguity next entry renders character-for-character
// as needs_human.resume (06-REQ-6.6).
func TestTS06_37_AmbiguityNextMatchesNeedsHumanResume(t *testing.T) {
	r := &Result{Stage: "stopped", Ambiguity: &Ambiguity{Question: "q?", InterpretationA: "a", InterpretationB: "b"}}
	n := r.Next()
	if len(n) != 1 {
		t.Fatalf("Next() = %+v, want exactly one entry", n)
	}
	e := n[0]
	if e.Tool != "fix" {
		t.Errorf("tool = %q", e.Tool)
	}
	hasCtx := false
	for _, f := range e.Flags {
		if f == "--context" {
			hasCtx = true
		}
	}
	if !hasCtx {
		t.Errorf("flags = %v, want --context", e.Flags)
	}
	if e.Input != toolio.SameInputPlaceholder {
		t.Errorf("input = %q, want the placeholder", e.Input)
	}

	// The envelope's own needs_human.resume is built by the same Run, from
	// the same Result; the entry rendered as one command line equals it.
	run := toolio.NewRun("fix", "test")
	env := run.Envelope(toolio.ExitNeedsHuman, r, nil)
	if env.NeedsHuman == nil {
		t.Fatal("envelope has no needs_human")
	}
	if got := e.Command(); got != env.NeedsHuman.Resume {
		t.Errorf("next rendered %q != needs_human.resume %q", got, env.NeedsHuman.Resume)
	}
}

// TS-06-38 (unit): fix produces no next entry on a run that lands (06-REQ-6.7).
func TestTS06_38_NoNextOnSuccessfulLanding(t *testing.T) {
	r := &Result{Stage: "landed", Branch: "fix/x", Commit: "abc1234", PullRequestURL: "https://github.com/o/r/pull/3", Pushed: true}
	if n := r.Next(); len(n) != 0 {
		t.Errorf("Next() = %+v, want none", n)
	}
	var nilRes *Result
	if n := nilRes.Next(); n != nil {
		t.Errorf("nil Result Next() = %+v, want nil", n)
	}
}

// TS-06-39 (unit): fix's next is derived from fact fields, not the model's
// report (06-REQ-6.8).
func TestTS06_39_FixNextIndependentOfModelText(t *testing.T) {
	r := &Result{Stage: "stopped", Ambiguity: &Ambiguity{Question: "q?"}, Implementation: &Implementation{Summary: "before"}}
	before := r.Next()
	r.Implementation.Summary = "different text"
	after := r.Next()
	if len(before) != 1 || len(after) != 1 || before[0].Command() != after[0].Command() || before[0].Why != after[0].Why {
		t.Errorf("Next() changed with the model's text: %+v vs %+v", before, after)
	}
}
