package toolio

import (
	"slices"
	"testing"
)

// TestAQuestionMayNameTheCommandThatAnswersIt_TS_16_72 verifies, for the shell, the contract that
// TS-16-72 relies on: a needs_human outcome whose question carries its own resume argv has it as
// needs_human.resume.argv, in place of the invocation as it was received, and the flag stays
// --context; a question that carries none keeps the invocation.
// Verifies: 16-REQ-10.4
func TestAQuestionMayNameTheCommandThatAnswersIt_TS_16_72(t *testing.T) {
	for name, c := range map[string]struct {
		resume []string
		want   []string
	}{
		"its own":  {[]string{"af", "jobs", "answer", "j", "--wait"}, []string{"af", "jobs", "answer", "j", "--wait"}},
		"the call": {nil, []string{"af", "ask", "--dry-run"}},
	} {
		app := newTestApp()
		app.Register(Command{Path: "ask", MaxArgs: NoArgs, DryRun: true, Run: func(r *Run) (Outcome, error) {
			return Outcome{Status: StatusNeedsHuman, Summary: "asks", NeedsHuman: &Question{Text: "Which?", Options: []string{"a", "b"}, ResumeArgv: c.resume}}, nil
		}})
		code, stdout, _ := invoke(t, app, "ask", "--dry-run")
		if code != 3 {
			t.Fatalf("%s: exit %d, want 3", name, code)
		}
		resume, _ := at(decode(t, stdout), "needs_human", "resume").(map[string]any)
		var argv []string
		for _, a := range resume["argv"].([]any) {
			argv = append(argv, a.(string))
		}
		if !slices.Equal(argv, c.want) || resume["flag"] != "--context" {
			t.Errorf("%s: resume = %v, want argv %v and flag --context", name, resume, c.want)
		}
	}
}
