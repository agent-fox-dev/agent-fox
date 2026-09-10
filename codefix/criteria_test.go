package codefix

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// The body the `issue` tool writes, and the one people write by hand: a
// heading, labelled items, and prose after the list that is commentary rather
// than a criterion.
const issueWithCriteria = `## Problem

Deleting an org ignores the registered hooks.

## Acceptance Criteria

- **AC-1:** Given a before-org-delete hook is registered and returns an error, when DELETE /orgs/:id is called, then the org row is not deleted and an appropriate error response is returned
- **AC-2:** Given an after-org-delete hook is registered, when DELETE /orgs/:id succeeds, then the hook is invoked with the deleted org's ID before the response is returned to the client

make sure that when working on the fix, these are explicitly addressed.

## Severity

**high** — data loss.
`

func TestParseCriteriaReadsTheIssueToolsOwnFormat(t *testing.T) {
	got := ParseCriteria(issueWithCriteria)
	want := []Criterion{
		{ID: "AC-1", Text: "Given a before-org-delete hook is registered and returns an error, " +
			"when DELETE /orgs/:id is called, then the org row is not deleted and an appropriate " +
			"error response is returned"},
		{ID: "AC-2", Text: "Given an after-org-delete hook is registered, when DELETE /orgs/:id " +
			"succeeds, then the hook is invoked with the deleted org's ID before the response is " +
			"returned to the client"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParseCriteria (-want +got):\n%s", diff)
	}
}

func TestParseCriteriaAcceptsTheSpellingsReportsUse(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want []Criterion
	}{
		"bold label with the colon outside": {
			body: "### Acceptance criteria\n\n- **AC-1**: the widget renders\n",
			want: []Criterion{{ID: "AC-1", Text: "the widget renders"}},
		},
		"checkboxes and no labels": {
			body: "## Acceptance Criteria\n\n- [ ] the widget renders\n- [x] the counter resets\n",
			want: []Criterion{
				{ID: "AC-1", Text: "the widget renders"},
				{ID: "AC-2", Text: "the counter resets"},
			},
		},
		"an ordered list": {
			body: "Acceptance criteria:\n\n1. the widget renders\n2. the counter resets\n",
			want: []Criterion{
				{ID: "AC-1", Text: "the widget renders"},
				{ID: "AC-2", Text: "the counter resets"},
			},
		},
		"labels that are not AC-n": {
			body: "## Acceptance Criteria\n\n- NS-REQ-1: envelopes carry a type\n- NS-REQ-2: rebuild rejects the wrong mode\n",
			want: []Criterion{
				{ID: "NS-REQ-1", Text: "envelopes carry a type"},
				{ID: "NS-REQ-2", Text: "rebuild rejects the wrong mode"},
			},
		},
		"a wrapped item with a nested bullet": {
			body: "## Acceptance criteria\n\n- **AC-1:** the widget renders\n  even when the cache is cold\n  - and the spinner is removed\n- **AC-2:** the counter resets\n",
			want: []Criterion{
				{ID: "AC-1", Text: "the widget renders even when the cache is cold - and the spinner is removed"},
				{ID: "AC-2", Text: "the counter resets"},
			},
		},
		"the section ends at the next heading of the same level": {
			body: "## Acceptance Criteria\n\n- AC-1: the widget renders\n\n## Notes\n\n- this is not a criterion\n",
			want: []Criterion{{ID: "AC-1", Text: "the widget renders"}},
		},
		"AC-3 keeps its own number": {
			body: "## Acceptance Criteria\n\n- **AC-3:** the third one, listed first\n- the unlabelled one\n",
			want: []Criterion{
				{ID: "AC-3", Text: "the third one, listed first"},
				{ID: "AC-2", Text: "the unlabelled one"},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, ParseCriteria(tc.body)); diff != "" {
				t.Errorf("ParseCriteria (-want +got):\n%s", diff)
			}
		})
	}
}

// Most reports state no criteria, and nothing about a run may depend on them.
func TestParseCriteriaFindsNothingWhereThereIsNothing(t *testing.T) {
	for _, body := range []string{
		"",
		"the widget counter double-counts on retry",
		"## Steps to reproduce\n\n- open the page\n- click twice\n",
		"## Acceptance Criteria\n\nNone yet — this needs discussion first.\n",
	} {
		if got := ParseCriteria(body); len(got) != 0 {
			t.Errorf("ParseCriteria(%q) = %v, want none", body, got)
		}
	}
}

// A criterion the model is told about is the criterion the report stated —
// not one the model paraphrased — so the ids it must answer are fixed before
// it reads anything.
func TestCheckVerdictsRefusesAnIncompleteAnswer(t *testing.T) {
	criteria := ParseCriteria(issueWithCriteria)
	good := CriterionVerdict{ID: "AC-1", Verdict: CriterionPass,
		Evidence: "orgs.go DeleteOrg runs the before hook first; TestDeleteOrg_BeforeHookError passes"}

	for name, tc := range map[string]struct {
		got  []CriterionVerdict
		want string
	}{
		"a criterion left unanswered": {
			got:  []CriterionVerdict{good},
			want: "missing AC-2",
		},
		"a criterion that was never asked for": {
			got: []CriterionVerdict{good, {ID: "AC-9", Verdict: CriterionPass,
				Evidence: "orgs.go DeleteOrg runs the after hook; TestDeleteOrg_AfterHook passes"}},
			want: "not one of the acceptance criteria",
		},
		"a verdict outside the enum": {
			got: []CriterionVerdict{good, {ID: "AC-2", Verdict: "partial",
				Evidence: "orgs.go DeleteOrg runs the after hook; TestDeleteOrg_AfterHook passes"}},
			want: `the verdict for AC-2 is "partial"`,
		},
		"evidence that is not evidence": {
			got:  []CriterionVerdict{good, {ID: "AC-2", Verdict: CriterionPass, Evidence: "yes"}},
			want: "the evidence for AC-2",
		},
		"the same criterion twice": {
			got:  []CriterionVerdict{good, good},
			want: "two verdicts for AC-1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkVerdicts(criteria, tc.got)
			if err == nil {
				t.Fatalf("checkVerdicts accepted %+v", tc.got)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want it to mention %q", err, tc.want)
			}
		})
	}

	// A complete answer, in any spelling of the ids, is accepted and folded
	// back onto the report's own labels and order.
	full := []CriterionVerdict{
		{ID: "ac2", Verdict: "FAIL", Evidence: "  the after hook is still not called on the " +
			"delete path; orgs.go DeleteOrg returns before it  "},
		good,
	}
	if err := checkVerdicts(criteria, full); err != nil {
		t.Fatalf("checkVerdicts rejected a complete answer: %v", err)
	}
	norm := normalizeVerdicts(criteria, full)
	if len(norm) != 2 || norm[0].ID != "AC-1" || norm[1].ID != "AC-2" {
		t.Fatalf("normalizeVerdicts = %+v", norm)
	}
	if norm[1].Verdict != CriterionFail || strings.HasPrefix(norm[1].Evidence, " ") {
		t.Errorf("normalizeVerdicts did not canonicalise: %+v", norm[1])
	}
	if criteriaOutcome(criteria, norm) != CriterionFail {
		t.Error("one fail makes the outcome a fail")
	}
	if criteriaOutcome(nil, nil) != "" {
		t.Error("a report with no criteria has no outcome")
	}
}

// The rule lives in the tool rather than in the prompt: the phase cannot end
// until every criterion has a verdict, and the model is told which are
// missing.
func TestSubmitImplementationRefusesUntilEveryCriterionIsAnswered(t *testing.T) {
	criteria := ParseCriteria(issueWithCriteria)
	var dest implementation
	tool := submitImplementationTool(&dest, criteria)

	args := func(verdicts string) json.RawMessage {
		return json.RawMessage(`{"summary":"done","commit_subject":"call the org hooks",` +
			`"changes":[{"path":"orgs.go","change":"call the hooks"}]` + verdicts + `}`)
	}

	res := tool.Execute(context.Background(), args(""))
	if res.OK || res.Terminate {
		t.Fatalf("a report with no verdicts was accepted: %+v", res)
	}
	if !strings.Contains(res.Detail, "AC-1, AC-2") {
		t.Errorf("the refusal does not name the criteria: %q", res.Detail)
	}
	if _, done := dest.get(); done {
		t.Fatal("the phase ended on a refused submission")
	}

	res = tool.Execute(context.Background(), args(`,"criteria_verdicts":[`+
		`{"id":"AC-1","verdict":"pass","evidence":"orgs.go DeleteOrg runs the before hook first; TestDeleteOrg_BeforeHookError passes"},`+
		`{"id":"AC-2","verdict":"pass","evidence":"orgs.go DeleteOrg calls afterDelete(id) before writing the response; TestDeleteOrg_AfterHook passes"}]`))
	if !res.OK || !res.Terminate {
		t.Fatalf("a complete report was refused: %+v", res)
	}
	got, done := dest.get()
	if !done || len(got.CriteriaVerdicts) != 2 {
		t.Fatalf("implementation = %+v", got)
	}
}

// A run whose report states no criteria is the common case, and the tool must
// not invent a requirement for it.
func TestSubmitImplementationIsUnchangedWithoutCriteria(t *testing.T) {
	var dest implementation
	tool := submitImplementationTool(&dest, nil)
	res := tool.Execute(context.Background(), json.RawMessage(
		`{"summary":"done","commit_subject":"fix it","changes":[{"path":"a.go","change":"fixed"}]}`))
	if !res.OK || !res.Terminate {
		t.Fatalf("res = %+v", res)
	}
	if got, _ := dest.get(); len(got.CriteriaVerdicts) != 0 {
		t.Errorf("CriteriaVerdicts = %+v", got.CriteriaVerdicts)
	}
}

// Both phases are told about the criteria, and told different things: the
// analysis phase plans for them, the implementation phase answers for them.
func TestBothPromptsCarryTheCriteria(t *testing.T) {
	criteria := ParseCriteria(issueWithCriteria)

	analysis := analysisPrompt(analysisInput{Root: "/repo", Criteria: criteria})
	implement := implementPrompt(implementInput{Root: "/repo", Branch: "fix/x", Criteria: criteria})
	for _, want := range []string{"AC-1", "AC-2", "the org row is not deleted"} {
		if !strings.Contains(analysis, want) {
			t.Errorf("the analysis prompt is missing %q", want)
		}
		if !strings.Contains(implement, want) {
			t.Errorf("the implementation prompt is missing %q", want)
		}
	}
	if !strings.Contains(implement, "criteria_verdicts") &&
		!strings.Contains(implement, "verdict and the evidence") {
		t.Error("the implementation prompt does not ask for a verdict per criterion")
	}
	if strings.Contains(analysisPrompt(analysisInput{Root: "/repo"}), "Acceptance criteria") {
		t.Error("a report without criteria must not grow a criteria section")
	}
}
