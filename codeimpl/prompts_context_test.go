package codeimpl

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// 05-REQ-3.7 for impl: the caller's --context block is appended to the survey's
// and each task's prompt, after the spec they describe, and a run without
// --context sends the prompt it always did.
func TestContextBlockIsAppendedToTheSurveyAndTaskPrompts(t *testing.T) {
	const note = "Prefer the existing helper in util.go."
	const block = "## Additional context from the caller\n\n" + note + "\n"
	spec := &afspec.Spec{}

	survey := surveyPrompt(surveyInput{Spec: spec, Root: "/r", Context: block})
	task := taskPrompt(taskInput{Spec: spec, Task: afspec.Task{Id: 1}, Root: "/r", Context: block})
	for name, got := range map[string]string{"survey": survey, "task": task} {
		if !strings.Contains(got, note) {
			t.Errorf("%s prompt lacks the caller's context:\n%s", name, got)
		}
	}
	if i, j := strings.Index(survey, "## The specification"), strings.Index(survey, note); i < 0 || j < i {
		t.Errorf("the survey prompt should show the context after the specification (spec at %d, context at %d)", i, j)
	}

	for name, got := range map[string]string{
		"survey": surveyPrompt(surveyInput{Spec: spec, Root: "/r"}),
		"task":   taskPrompt(taskInput{Spec: spec, Task: afspec.Task{Id: 1}, Root: "/r"}),
	} {
		if strings.Contains(got, "Additional context") || strings.Contains(got, note) {
			t.Errorf("%s prompt with no --context mentions one", name)
		}
	}
}
