package toolio_test

import (
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func anyHasPrefix(ss []string, prefix string) bool {
	for _, s := range ss {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func count(ss []string, want string) int {
	n := 0
	for _, s := range ss {
		if s == want {
			n++
		}
	}
	return n
}

// TS-10-16 (unit): Run.Envelope stores toolio.UntrustedFields(result).
// Verifies: 10-REQ-4.1
func TestTS10_16_EnvelopeStoresUntrustedFields(t *testing.T) {
	run := toolio.NewRun("fix", "test")
	r := &codefix.Result{RootCause: "x"}
	env := run.Envelope(toolio.ExitOK, r, nil)
	want := toolio.UntrustedFields(r)
	if len(want) == 0 {
		t.Fatalf("UntrustedFields returned nothing for a populated model field")
	}
	if !reflect.DeepEqual(env.UntrustedFields, want) {
		t.Fatalf("env.UntrustedFields = %v, want %v", env.UntrustedFields, want)
	}
	f, ok := reflect.TypeOf(env).FieldByName("UntrustedFields")
	if !ok {
		t.Fatal("Envelope has no UntrustedFields field")
	}
	if got := f.Tag.Get("json"); got != "untrusted_fields,omitempty" {
		t.Fatalf("json tag = %q", got)
	}
}

// TS-10-17 (unit): a present model field is listed, a fact field is not, in
// declaration order.
// Verifies: 10-REQ-4.2
func TestTS10_17_ModelFieldListedFactNot(t *testing.T) {
	r := codefix.Result{RootCause: "the parser mishandled nil", Branch: "fix/123-parser", Title: "t"}
	got := toolio.UntrustedFields(&r)
	if !slices.Contains(got, "/result/root_cause") {
		t.Fatalf("missing /result/root_cause in %v", got)
	}
	if slices.Contains(got, "/result/branch") {
		t.Fatalf("fact field listed: %v", got)
	}
	// Title is declared before RootCause.
	if i, j := slices.Index(got, "/result/title"), slices.Index(got, "/result/root_cause"); i < 0 || i > j {
		t.Fatalf("declaration order violated: %v", got)
	}
	// A pointer and a plain value behave alike.
	if got2 := toolio.UntrustedFields(r); !reflect.DeepEqual(got, got2) {
		t.Fatalf("value %v != pointer %v", got2, got)
	}
}

// TS-10-18 (unit): []string is one pointer; []struct is indexed.
// Verifies: 10-REQ-4.3
func TestTS10_18_StringSliceOnePointerStructSliceIndexed(t *testing.T) {
	r := codefix.Result{
		Assumptions:        []string{"a", "b", "c"},
		AcceptanceCriteria: []codefix.Criterion{{ID: "AC-1", Text: "must handle nil"}, {ID: "AC-2", Text: ""}},
	}
	got := toolio.UntrustedFields(&r)
	if n := count(got, "/result/assumptions"); n != 1 {
		t.Fatalf("assumptions pointer count = %d in %v", n, got)
	}
	if anyHasPrefix(got, "/result/assumptions/") {
		t.Fatalf("per-element pointer emitted: %v", got)
	}
	if !slices.Contains(got, "/result/acceptance_criteria/0/text") {
		t.Fatalf("missing indexed pointer in %v", got)
	}
	if anyHasPrefix(got, "/result/acceptance_criteria/1") {
		t.Fatalf("pointer under empty element: %v", got)
	}
}

// TS-10-19 (unit): a nil optional object contributes nothing.
// Verifies: 10-REQ-4.4
func TestTS10_19_NilOptionalObjectContributesNothing(t *testing.T) {
	r1 := codeimpl.Result{Survey: nil}
	if got := toolio.UntrustedFields(&r1); anyHasPrefix(got, "/result/survey") {
		t.Fatalf("nil survey listed: %v", got)
	}
	r2 := codeimpl.Result{Survey: &codeimpl.Survey{Summary: "read the auth package first"}}
	if got := toolio.UntrustedFields(&r2); !slices.Contains(got, "/result/survey/summary") {
		t.Fatalf("missing /result/survey/summary in %v", got)
	}
}

// TS-10-20 (property): no pointer ever names a fact-classified field.
// Verifies: 10-REQ-4.5
func TestTS10_20_NeverNamesFactField(t *testing.T) {
	for i := 0; i < 20; i++ {
		s := func(name string) string { return name + "-" + string(rune('a'+i)) }
		r := codefix.Result{
			Classification: s("c"), Title: s("t"), FixSummary: s("s"), RootCause: s("rc"), Approach: s("ap"),
			Assumptions: []string{s("as")},
			Repo:        s("repo"), IssueURL: s("url"), Branch: s("br"), BaseBranch: s("bb"), Commit: s("co"),
			ChangedFiles: []string{s("f")}, DiffStat: s("ds"), Verdict: s("v"), PullRequestURL: s("pr"),
			Comments: []string{s("cm")}, CriteriaOutcome: s("out"),
			AcceptanceCriteria: []codefix.Criterion{{ID: s("id"), Text: s("tx")}},
			Verification:       checks.Result{Command: s("cmd"), Output: s("o")},
			Baseline:           checks.Result{Command: s("cmd"), Output: s("o")},
		}
		got := toolio.UntrustedFields(&r)
		if len(got) == 0 {
			t.Fatal("no pointers for a fully populated result")
		}
		for _, p := range got {
			if trustOfPointer(t, reflect.TypeOf(r), p) == "fact" {
				t.Fatalf("fact field listed: %s", p)
			}
		}
		for _, bad := range []string{"/result/branch", "/result/commit", "/result/verdict", "/result/changed_files", "/result/verification/command", "/result/acceptance_criteria/0/id"} {
			if slices.Contains(got, bad) {
				t.Fatalf("%s listed", bad)
			}
		}
		if !slices.Contains(got, "/result/verification/output") {
			t.Fatalf("verification output missing: %v", got)
		}
	}
}

// trustOfPointer resolves a /result/... pointer against type t and returns
// the trust tag of the field it names ("" when unresolved).
func trustOfPointer(t *testing.T, typ reflect.Type, ptr string) string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(ptr, "/result/"), "/")
	tag := ""
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err == nil {
			continue // an index into a slice
		}
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		found := false
		for _, f := range reflect.VisibleFields(typ) {
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == part {
				typ, tag, found = f.Type, f.Tag.Get("trust"), true
				break
			}
		}
		if !found {
			t.Fatalf("pointer %s: no field %q", ptr, part)
		}
	}
	return tag
}

// TS-10-21 (unit): an already-trimmed value lists nothing beneath what was
// trimmed.
// Verifies: 10-REQ-4.6
func TestTS10_21_TrimmedValueOmitsTrimmedFields(t *testing.T) {
	trimmed := codeimpl.Result{Stage: "complete", SpecID: "10", Survey: nil}
	if got := toolio.UntrustedFields(&trimmed); anyHasPrefix(got, "/result/survey") {
		t.Fatalf("trimmed survey listed: %v", got)
	}
}

// TS-10-22 (unit): no untrusted_fields key without a result or when every
// classified field is absent.
// Verifies: 10-REQ-4.7
func TestTS10_22_OmittedWhenEmpty(t *testing.T) {
	run := toolio.NewRun("impl", "test")
	b1, err := json.Marshal(run.Envelope(toolio.ExitUsage, nil, &toolio.ErrorInfo{Stage: "usage"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b1), "untrusted_fields") {
		t.Fatalf("nil result carries the key: %s", b1)
	}
	r := codeimpl.Result{Stage: "complete", SpecID: "10"}
	b2, err := json.Marshal(run.Envelope(toolio.ExitOK, &r, nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b2), "untrusted_fields") {
		t.Fatalf("all-absent result carries the key: %s", b2)
	}
}

// Non-struct results never panic.
func TestUntrustedFieldsNonStruct(t *testing.T) {
	for _, v := range []any{nil, 3, "x", map[string]any{"a": "b"}, (*codefix.Result)(nil), []string{"a"}} {
		if got := toolio.UntrustedFields(v); got != nil {
			t.Fatalf("UntrustedFields(%#v) = %v", v, got)
		}
	}
}
