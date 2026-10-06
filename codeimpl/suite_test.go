package codeimpl

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// Issue #196: the implement and resolve phases name the gate's test suite,
// which their shell then refuses; the repair phase, whose work is making that
// suite pass, does not.
func TestWritingPhasesNameTheSuiteTheyMayNotRun(t *testing.T) {
	b := &agentBrain{protected: "/spec"}
	spec := &afspec.Spec{}
	var task sink[Submission]
	if p := b.implementPhase(taskInput{Spec: spec, Task: afspec.Task{Id: 1}, Suite: "make test"}, &task); len(p.Suite) != 1 ||
		p.Suite[0] != "make test" {
		t.Errorf("implement Phase.Suite = %q", p.Suite)
	}
	var resolve sink[ResolveSubmission]
	if p := b.resolvePhase(resolveInput{Spec: spec, Suite: "make test"}, &resolve); len(p.Suite) != 1 ||
		p.Suite[0] != "make test" {
		t.Errorf("resolve Phase.Suite = %q", p.Suite)
	}
	var repair sink[RepairSubmission]
	if p := b.repairPhase(repairInput{Spec: spec}, &repair); len(p.Suite) != 0 {
		t.Errorf("repair Phase.Suite = %q, want none", p.Suite)
	}
	if p := b.implementPhase(taskInput{Spec: spec, Task: afspec.Task{Id: 1}}, &task); len(p.Suite) != 0 {
		t.Errorf("a run with no gate names a suite: %q", p.Suite)
	}
}

// The suite is the gate's all_tests command, or the --verify-command that
// replaces the gate; the linter is not.
func TestSuiteCommand(t *testing.T) {
	tc := afspec.TestCommands{Linter: "make lint", AllTests: "make test"}
	for _, c := range []struct {
		override string
		none     bool
		want     string
	}{
		{"", false, "make test"},
		{"make check", false, "make check"},
		{"", true, ""},
	} {
		if got := suiteCommand(tc, c.override, c.none); got != c.want {
			t.Errorf("suiteCommand(%q, %v) = %q, want %q", c.override, c.none, got, c.want)
		}
	}
	if got := suiteCommand(afspec.TestCommands{Linter: "make lint"}, "", false); got != "" {
		t.Errorf("a gate with only a linter names %q as the suite", got)
	}
}
