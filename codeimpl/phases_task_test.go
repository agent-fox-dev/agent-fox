package codeimpl

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// TS-07-31: only the per-task implement phase carries a Task label.
func TestTS07_31_ImplementPhaseCarriesTask(t *testing.T) {
	b := &agentBrain{protected: "/spec"}
	var out sink[Submission]
	in := taskInput{Spec: &afspec.Spec{}, Task: afspec.Task{Id: 7, Title: "seven"}}

	p := b.implementPhase(in, &out)
	if p.Task != "7" {
		t.Errorf("implement Phase.Task = %q, want %q", p.Task, "7")
	}
	if p.Name != PhaseImplement {
		t.Errorf("implement Phase.Name = %q, want the stable %q", p.Name, PhaseImplement)
	}
}

// The red-first requirement follows the --no-test-first opt-out through the
// phase the brain builds.
func TestImplementPhaseHonoursNoTestFirst(t *testing.T) {
	task := afspec.Task{Id: 4, Title: "four", Tests: []string{"TS-01-1"}}
	in := taskInput{Spec: &afspec.Spec{}, Task: task}
	raw := json.RawMessage(`{"summary":"s","commit_subject":"x","changes":[{"path":"a.go","change":"b"}],
		"test_verdicts":[{"id":"TS-01-1","verdict":"pass","evidence":"a_test.go TestOne passes under go test"}]}`)
	for _, c := range []struct {
		noTestFirst bool
		wantOK      bool
	}{{false, false}, {true, true}} {
		b := &agentBrain{protected: "/spec", noTestFirst: c.noTestFirst}
		var out sink[Submission]
		p := b.implementPhase(in, &out)
		if len(p.Custom) != 1 {
			t.Fatalf("custom tools = %d", len(p.Custom))
		}
		if res := p.Custom[0].Execute(context.Background(), raw); res.OK != c.wantOK {
			t.Errorf("noTestFirst=%v: ok=%v (%s), want %v", c.noTestFirst, res.OK, res.Detail, c.wantOK)
		}
	}
}

// AC-2: the prompt tells the model to stub a new API, see the tests fail on
// behaviour, and only then implement.
func TestImplementPromptSaysToStubNewAPIs(t *testing.T) {
	flat := strings.Join(strings.Fields(implementSystemPrompt), " ")
	for _, want := range []string{
		"function signature", "interface method", "stub", "fail on behaviour, not on a compile error",
		"red_evidence", "test_first_deviation",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("implementSystemPrompt lacks %q", want)
		}
	}
	if !strings.Contains(taskPrompt(taskInput{Spec: &afspec.Spec{}, Task: afspec.Task{Id: 1}}), "stub") {
		t.Error("the task prompt does not mention stubbing the API")
	}
}

// Issue #219: the writing phases point the model at its own language's
// targeted test run — in the shell's refusal and in the prompt — not Go's.
func TestWritingPhasesNameTheProjectsTargetedRun(t *testing.T) {
	rust := project.Profile{Language: "rust", Manifest: "Cargo.toml", AllTests: "cargo test", StubMarker: "todo!()"}
	b := &agentBrain{protected: "/spec"}
	var out sink[Submission]
	p := b.implementPhase(taskInput{Spec: &afspec.Spec{}, Task: afspec.Task{Id: 1}, Suite: "cargo test",
		Profile: rust}, &out)
	if p.TargetedRun != "cargo test <name>" {
		t.Errorf("implement Phase.TargetedRun = %q", p.TargetedRun)
	}
	if strings.Contains(p.System, "go test ./pkg") || !strings.Contains(p.User, "`cargo test <name>`") {
		t.Errorf("the implement prompt names Go's form, or not the project's:\nsystem: %s\nuser: %s", p.System, p.User)
	}
	var rout sink[ResolveSubmission]
	r := b.resolvePhase(resolveInput{Spec: &afspec.Spec{}, Suite: "cargo test", Profile: rust}, &rout)
	if r.TargetedRun != "cargo test <name>" || !strings.Contains(r.User, "`cargo test <name>`") {
		t.Errorf("resolve Phase.TargetedRun = %q", r.TargetedRun)
	}
}
