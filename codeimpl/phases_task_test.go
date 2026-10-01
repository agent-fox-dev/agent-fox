package codeimpl

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
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
