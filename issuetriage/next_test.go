package issuetriage

import (
	"strings"
	"testing"
)

// TS-06-31 (unit): issue suggests fix on the filed issue's URL, naming the
// labels, when the run was not made under --dry-run (06-REQ-6.1).
func TestTS06_31_NextSuggestsFixOnFiledIssue(t *testing.T) {
	r := &Result{
		Action: "created", URL: "https://github.com/o/r/issues/7", Number: 7,
		Labels: []string{"bug", "triaged"},
	}
	n := r.Next()
	if len(n) != 1 {
		t.Fatalf("Next() = %+v, want exactly one entry", n)
	}
	e := n[0]
	if e.Tool != "fix" || e.Input != r.URL {
		t.Errorf("entry = %+v, want tool fix on %s", e, r.URL)
	}
	if e.Flags == nil || len(e.Flags) != 0 {
		t.Errorf("flags = %#v, want a non-nil empty slice", e.Flags)
	}
	if !strings.Contains(e.Why, "bug") || !strings.Contains(e.Why, "triaged") {
		t.Errorf("why = %q, want it to name both labels", e.Why)
	}

	// Without labels the sentence still says the issue was filed.
	r.Labels = nil
	if n := r.Next(); len(n) != 1 || !strings.Contains(n[0].Why, "filed") {
		t.Errorf("unlabelled Next() = %+v, want one entry saying it was filed", n)
	}

	// An update is suggested the same way.
	r.Action = "updated"
	if n := r.Next(); len(n) != 1 {
		t.Errorf("updated Next() = %+v, want one entry", n)
	}

	// Nothing under --dry-run, even if a URL were somehow present.
	r.DryRun = true
	if n := r.Next(); len(n) != 0 {
		t.Errorf("dry-run Next() = %+v, want none", n)
	}
}

// TS-06-39 (unit): issue with Action "none" suggests nothing, and what it
// does suggest does not depend on the model's own diagnosis text (06-REQ-6.8).
func TestTS06_39_IssueNextOmittedAndIndependentOfModelText(t *testing.T) {
	var none *Result
	if n := none.Next(); n != nil {
		t.Errorf("nil Result Next() = %+v, want nil", n)
	}
	if n := (&Result{Action: "none"}).Next(); n != nil {
		t.Errorf("Action none Next() = %+v, want nil", n)
	}

	r := &Result{Action: "created", URL: "https://github.com/o/r/issues/7", Labels: []string{"bug"},
		Problem: "before", RootCause: "before", Body: "before"}
	before := r.Next()
	r.Problem, r.RootCause, r.Body = "after", "after", "after"
	after := r.Next()
	if len(before) != 1 || len(after) != 1 || before[0].Why != after[0].Why || before[0].Input != after[0].Input {
		t.Errorf("Next() changed with the model's text: %+v vs %+v", before, after)
	}
}
