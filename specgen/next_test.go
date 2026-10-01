package specgen

import (
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

// TS-06-32 (unit): spec suggests impl on the first written package that
// validates (06-REQ-6.2).
func TestTS06_32_NextSuggestsImplOnFirstValidPackage(t *testing.T) {
	r := &Result{
		Package: Package{SpecDir: ".specs/01_a", SpecID: "01_a"},
		FollowOnSpecs: []Package{
			{SpecDir: ".specs/02_b", SpecID: "02_b", Validation: ValidationReport{Valid: true}},
			{SpecDir: ".specs/03_c", SpecID: "03_c", Validation: ValidationReport{Valid: true}},
		},
	}
	impl := findNext(r.Next(), "impl")
	if len(impl) != 1 {
		t.Fatalf("impl entries = %+v, want exactly one", impl)
	}
	e := impl[0]
	if e.Input != ".specs/02_b" {
		t.Errorf("input = %q, want the first validating package's spec_dir", e.Input)
	}
	if e.Why != "the package validates and is ready to implement" {
		t.Errorf("why = %q", e.Why)
	}
	if e.Flags == nil || len(e.Flags) != 0 {
		t.Errorf("flags = %#v, want a non-nil empty slice", e.Flags)
	}

	// The first package, when it validates, is the one suggested.
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
