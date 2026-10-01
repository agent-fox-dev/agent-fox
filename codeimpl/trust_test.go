package codeimpl

import (
	"reflect"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
)

func trustOf(t *testing.T, typ reflect.Type, name string) string {
	t.Helper()
	f, ok := typ.FieldByName(name)
	if !ok {
		t.Fatalf("%s has no field %s", typ, name)
	}
	return f.Tag.Get("trust")
}

// TS-10-4 (unit): the model's-own-account types are tagged model on every
// one of their string and []string fields.
//
// Verifies: 10-REQ-1.4
func TestTS10_4_ModelAccountTypesTaggedModel(t *testing.T) {
	cases := []struct {
		typ    reflect.Type
		fields []string
	}{
		{reflect.TypeOf(codefix.Implementation{}), []string{"Summary", "CommitSubject", "Tests", "Notes"}},
		{reflect.TypeOf(codefix.Ambiguity{}), []string{"Question", "InterpretationA", "InterpretationB"}},
		{reflect.TypeOf(codefix.FileChange{}), []string{"Path", "Change"}},
		{reflect.TypeOf(Survey{}), []string{"Summary", "Conventions"}},
		{reflect.TypeOf(Blocker{}), []string{"Reason", "Needed"}},
		{reflect.TypeOf(Drift{}), []string{"SpecRef", "Finding", "Resolution", "Kind"}},
		{reflect.TypeOf(Location{}), []string{"Name", "Path", "Note"}},
		{reflect.TypeOf(Submission{}), []string{"Summary", "CommitSubject", "Notes", "Gotchas", "TestFirstDeviation"}},
		{reflect.TypeOf(RepairSubmission{}), []string{"Cause", "Summary", "CommitSubject", "Notes"}},
		{reflect.TypeOf(FileChange{}), []string{"Path", "Change"}},
		{reflect.TypeOf(Verdict{}), []string{"Verdict", "Evidence", "RedEvidence"}},
	}
	for _, c := range cases {
		for _, name := range c.fields {
			if got := trustOf(t, c.typ, name); got != "model" {
				t.Errorf("%s.%s trust = %q, want model", c.typ, name, got)
			}
		}
	}
}

// The rest of codeimpl's Result tree: program-established fields are fact,
// and no string-typed field is left without a tag.
//
// Verifies: 10-REQ-1.1, 10-REQ-1.2
func TestTS10_4_FactsAndCompleteness(t *testing.T) {
	facts := []struct {
		typ    reflect.Type
		fields []string
	}{
		{reflect.TypeOf(Verdict{}), []string{"ID"}},
		{reflect.TypeOf(RepairReport{}), []string{"Outcome", "Model", "Commit", "ChangedFiles", "DiffStat", "Error"}},
		{reflect.TypeOf(TaskReport{}), []string{"Kind", "Title", "Outcome", "Commit", "ChangedFiles", "DiffStat", "Verdict", "TestsOutcome", "Error"}},
		{reflect.TypeOf(Result{}), []string{"Stage", "SpecDir", "SpecID", "SpecName", "Title", "Status", "Repo", "Branch", "BaseBranch", "Gate", "Verdict", "PullRequestURL", "Land", "Detail"}},
	}
	for _, c := range facts {
		for _, name := range c.fields {
			if got := trustOf(t, c.typ, name); got != "fact" {
				t.Errorf("%s.%s trust = %q, want fact", c.typ, name, got)
			}
		}
	}
	// TaskReport.ID is an int: it carries no tag.
	if got := trustOf(t, reflect.TypeOf(TaskReport{}), "ID"); got != "" {
		t.Errorf("TaskReport.ID trust = %q, want none", got)
	}

	for _, typ := range []reflect.Type{
		reflect.TypeOf(Result{}), reflect.TypeOf(summaryResult{}),
		reflect.TypeOf(summaryTask{}),
	} {
		for _, p := range untaggedStrings(typ, typ.Name(), map[reflect.Type]bool{}) {
			t.Errorf("string field without trust tag: %s", p)
		}
	}
}

// untaggedStrings lists the dotted path of every string or []string field
// reachable from typ that carries no trust tag.
func untaggedStrings(typ reflect.Type, prefix string, seen map[reflect.Type]bool) []string {
	for typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || seen[typ] {
		return nil
	}
	seen[typ] = true
	defer delete(seen, typ)
	var out []string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() || f.Tag.Get("json") == "-" {
			continue
		}
		path := prefix + "." + f.Name
		ft := f.Type
		isStr := ft.Kind() == reflect.String || (ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.String)
		if isStr {
			switch f.Tag.Get("trust") {
			case "fact", "model", "external":
			default:
				out = append(out, path)
			}
			continue
		}
		out = append(out, untaggedStrings(ft, path, seen)...)
	}
	return out
}
