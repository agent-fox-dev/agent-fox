package specgen

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func findNext(ns []toolio.Next, tool string) []toolio.Next {
	var out []toolio.Next
	for _, n := range ns {
		if n.Tool == tool {
			out = append(out, n)
		}
	}
	return out
}

// TS-06-32 (unit): spec suggests impl on every written package that
// validates, in split order (06-REQ-6.2, widened by #57: the first package
// alone left the rest of a split unmentioned).
func TestTS06_32_NextSuggestsImplOnEveryValidPackage(t *testing.T) {
	r := &Result{
		Package: Package{SpecDir: ".specs/01_a", SpecID: "01_a"},
		FollowOnSpecs: []Package{
			{SpecDir: ".specs/02_b", SpecID: "02_b", Validation: ValidationReport{Valid: true}},
			{SpecDir: ".specs/03_c", SpecID: "03_c", Validation: ValidationReport{Valid: true}},
			{SpecDir: ".specs/04_d", SpecID: "04_d"},
		},
	}
	impl := findNext(r.Next(), "impl")
	if len(impl) != 2 || impl[0].Input != ".specs/02_b" || impl[1].Input != ".specs/03_c" {
		t.Fatalf("impl entries = %+v, want 02_b then 03_c", impl)
	}
	if !strings.Contains(impl[0].Why, "2 of this run's packages are ready") {
		t.Errorf("why = %q, want the ready count", impl[0].Why)
	}
	if impl[0].Flags == nil || len(impl[0].Flags) != 0 {
		t.Errorf("flags = %#v, want a non-nil empty slice", impl[0].Flags)
	}

	// A single ready package keeps the plain reason.
	r.FollowOnSpecs = r.FollowOnSpecs[:1]
	r.Package.Validation.Valid = false
	one := findNext(r.Next(), "impl")
	if len(one) != 1 || one[0].Why != "the package validates and is ready to implement" {
		t.Errorf("single ready package = %+v", one)
	}

	// The first package, when it validates, comes first.
	r.Package.Validation.Valid = true
	if got := findNext(r.Next(), "impl")[0].Input; got != ".specs/01_a" {
		t.Errorf("input = %q, want .specs/01_a", got)
	}
}

// TS-06-33 (unit): an unfinished split suggests spec again on the literal
// origin when the input had a file or issue origin (06-REQ-6.3).
func TestTS06_33_NextSuggestsSpecAgainOnLiteralOrigin(t *testing.T) {
	r := &Result{SplitPlan: ".specs/01_a.split.json", inputRef: "docs/brief.md"}
	n := findNext(r.Next(), "spec")
	if len(n) != 1 {
		t.Fatalf("spec entries = %+v, want exactly one", n)
	}
	if n[0].Input != "docs/brief.md" {
		t.Errorf("input = %q, want the literal file path", n[0].Input)
	}
}

// TS-06-34 (unit): the resume suggestion uses the needs_human.resume
// placeholder for raw text or stdin input (06-REQ-6.3).
func TestTS06_34_NextSpecAgainUsesPlaceholderForTextInput(t *testing.T) {
	for _, in := range []toolio.Input{
		{Kind: toolio.KindText, Origin: "argument", Body: "a long brief"},
		{Kind: toolio.KindStdin, Origin: "stdin", Body: "a long brief"},
	} {
		r := &Result{SplitPlan: ".specs/01_a.split.json", inputRef: toolio.ResumePlaceholder(in)}
		n := findNext(r.Next(), "spec")
		if len(n) != 1 || n[0].Input != "<same input>" {
			t.Errorf("%s input: spec entries = %+v, want input <same input>", in.Kind, n)
		}
	}
	// A Result that never recorded an origin falls back to the placeholder.
	r := &Result{SplitPlan: ".specs/01_a.split.json"}
	if n := findNext(r.Next(), "spec"); len(n) != 1 || n[0].Input != "<same input>" {
		t.Errorf("unrecorded origin: %+v", n)
	}
}

// TS-06-39 (unit): spec omits next when nothing validates and no split is
// unfinished (06-REQ-6.8).
func TestTS06_39_SpecNextOmittedWhenNothingToSuggest(t *testing.T) {
	r := &Result{Package: Package{SpecDir: ".specs/01_a"}}
	if n := r.Next(); n != nil {
		t.Errorf("Next() = %+v, want nil", n)
	}
	var nilRes *Result
	if n := nilRes.Next(); n != nil {
		t.Errorf("nil Result Next() = %+v, want nil", n)
	}
}
