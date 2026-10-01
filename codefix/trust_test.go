package codefix

import (
	"reflect"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

func fieldTrust(t *testing.T, typ reflect.Type, name string) string {
	t.Helper()
	f, ok := typ.FieldByName(name)
	if !ok {
		t.Fatalf("%s has no field %s", typ, name)
	}
	return f.Tag.Get("trust")
}

// TS-10-3 (unit): codefix.Result's own established facts are tagged fact,
// and checks.Result.Output is tagged external.
//
// Verifies: 10-REQ-1.3
func TestTS10_3_ResultFactsAndCheckOutput(t *testing.T) {
	rt := reflect.TypeOf(Result{})
	for _, name := range []string{"Repo", "IssueURL", "Branch", "BaseBranch", "Commit", "ChangedFiles", "DiffStat", "Verdict", "PullRequestURL", "Comments", "CriteriaOutcome", "Stage", "Land", "Detail"} {
		if got := fieldTrust(t, rt, name); got != "fact" {
			t.Errorf("codefix.Result.%s trust = %q, want fact", name, got)
		}
	}
	for _, name := range []string{"Classification", "Title", "FixSummary", "RootCause", "Approach", "Assumptions"} {
		if got := fieldTrust(t, rt, name); got != "model" {
			t.Errorf("codefix.Result.%s trust = %q, want model", name, got)
		}
	}
	for _, name := range []string{"IssueNumber", "Pushed", "PullRequestNumber", "DryRun"} {
		if got := fieldTrust(t, rt, name); got != "" {
			t.Errorf("codefix.Result.%s trust = %q, want none", name, got)
		}
	}
	if got := fieldTrust(t, reflect.TypeOf(checks.Result{}), "Output"); got != "external" {
		t.Errorf("checks.Result.Output trust = %q, want external", got)
	}
}

// TS-10-5 (unit): Criterion.ID and CriterionVerdict.ID are fact;
// Criterion.Text is external; CriterionVerdict.Verdict/Evidence are model.
//
// Verifies: 10-REQ-1.5
func TestTS10_5_CriterionTrust(t *testing.T) {
	c := reflect.TypeOf(Criterion{})
	if fieldTrust(t, c, "ID") != "fact" || fieldTrust(t, c, "Text") != "external" {
		t.Errorf("Criterion trust wrong: ID=%q Text=%q", fieldTrust(t, c, "ID"), fieldTrust(t, c, "Text"))
	}
	cv := reflect.TypeOf(CriterionVerdict{})
	if fieldTrust(t, cv, "ID") != "fact" || fieldTrust(t, cv, "Verdict") != "model" || fieldTrust(t, cv, "Evidence") != "model" {
		t.Errorf("CriterionVerdict trust wrong")
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(FileChange{}), reflect.TypeOf(Ambiguity{}), reflect.TypeOf(Implementation{})} {
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			k := f.Type.Kind()
			isStr := k == reflect.String || (k == reflect.Slice && f.Type.Elem().Kind() == reflect.String)
			if isStr && f.Tag.Get("trust") != "model" {
				t.Errorf("%s.%s trust = %q, want model", typ.Name(), f.Name, f.Tag.Get("trust"))
			}
		}
	}
}
