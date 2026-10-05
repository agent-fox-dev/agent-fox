package toolio_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/specgen"
)

type trustCheckSub struct {
	Tagged   string `json:"tagged" trust:"model"`
	Untagged string `json:"untagged"`
}

type trustCheckBad struct {
	Sub trustCheckSub `json:"sub"`
}

// TS-10-9 (unit): CheckTrust names the dotted path of a nested field left
// without a trust tag.
//
// Verifies: 10-REQ-2.1
func TestTS10_9_CheckTrustNamesNestedUntaggedField(t *testing.T) {
	got := toolio.CheckTrust(reflect.TypeOf(trustCheckBad{}))
	want := []string{"Sub.Untagged"}
	if !slices.Equal(got, want) {
		t.Fatalf("CheckTrust = %v, want %v", got, want)
	}
}

type trustCheckSlices struct {
	Names  []string        `json:"names"`
	Items  []trustCheckSub `json:"items"`
	Ptr    *trustCheckSub  `json:"ptr"`
	Count  int             `json:"count"`
	Hidden string          `json:"-"`
	Fine   []string        `json:"fine" trust:"fact"`
}

// CheckTrust reaches through slices and pointers, flags []string, and skips
// json:"-" fields and non-string kinds.
func TestTS10_9_CheckTrustWalksSlicesAndPointers(t *testing.T) {
	got := toolio.CheckTrust(reflect.TypeOf(&trustCheckSlices{}))
	want := []string{"Names", "Items.Untagged", "Ptr.Untagged"}
	if !slices.Equal(got, want) {
		t.Fatalf("CheckTrust = %v, want %v", got, want)
	}
}

type trustCheckCycle struct {
	Next *trustCheckCycle `json:"next"`
	Text string           `json:"text"`
}

// The walk terminates on a recursive type.
func TestTS10_9_CheckTrustTerminatesOnCycle(t *testing.T) {
	got := toolio.CheckTrust(reflect.TypeOf(trustCheckCycle{}))
	if !slices.Equal(got, []string{"Text"}) {
		t.Fatalf("CheckTrust = %v, want [Text]", got)
	}
}

// TS-10-10 (unit): CheckTrust reports no missing field for any of the four
// real Result types. A field added later without a trust tag fails here,
// named by its dotted path.
//
// Verifies: 10-REQ-2.2
func TestTS10_10_FourResultTreesFullyClassified(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(codefix.Result{}),
		reflect.TypeOf(codeimpl.Result{}),
		reflect.TypeOf(issuetriage.Result{}),
		reflect.TypeOf(specgen.Result{}),
	} {
		if missing := toolio.CheckTrust(typ); len(missing) != 0 {
			t.Errorf("%s: fields without a trust tag: %v", typ, missing)
		}
	}
}

// TS-10-10 (unit, summary views): the default envelope's untrusted_fields is
// computed over the --detail summary view, which App.emit swaps in after the
// full Result is built, so the views are outside the four Result trees TS-10-10
// walks. Each view's string fields must be classified too; a model-authored
// string added to a summary view without a trust tag would otherwise be absent
// from the default view's untrusted_fields with no test failing (10-REQ-2.3).
//
// Verifies: 10-REQ-2.2, 10-REQ-2.3
func TestTS10_10_SummaryViewsFullyClassified(t *testing.T) {
	for name, sample := range map[string]toolio.Summarizable{
		"codefix":     &codefix.Result{},
		"codeimpl":    &codeimpl.Result{},
		"issuetriage": &issuetriage.Result{},
		"specgen":     &specgen.Result{},
	} {
		view := sample.SummaryView()
		typ := reflect.TypeOf(view)
		if typ == nil {
			t.Errorf("%s: SummaryView() returned nil", name)
			continue
		}
		if missing := toolio.CheckTrust(typ); len(missing) != 0 {
			t.Errorf("%s: %s has string fields without a trust tag: %v", name, typ, missing)
		}
	}
}

// A trust tag's value is one of the three the label knows (10-REQ-1.1): the
// untrusted_fields list is built from "model" and "external", so a misspelled
// value would classify a field as neither and leave it out without a test
// failing. Checked over every Result and every summary view, nested types
// included; this is the one walker the per-package tests used to carry copies of.
func TestTS10_TrustTagValuesAreLegal(t *testing.T) {
	legal := map[string]bool{"fact": true, "model": true, "external": true}
	var walk func(typ reflect.Type, path string, seen map[reflect.Type]bool)
	walk = func(typ reflect.Type, path string, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		defer delete(seen, typ)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() || f.Tag.Get("json") == "-" {
				continue
			}
			fieldPath := path + "." + f.Name
			if v, tagged := f.Tag.Lookup(toolio.TrustTag); tagged && !legal[v] {
				t.Errorf("%s: trust tag %q is not fact, model or external", fieldPath, v)
			}
			walk(f.Type, fieldPath, seen)
		}
	}
	results := map[string]toolio.Summarizable{
		"codefix":     &codefix.Result{},
		"codeimpl":    &codeimpl.Result{},
		"issuetriage": &issuetriage.Result{},
		"specgen":     &specgen.Result{},
	}
	for name, r := range results {
		walk(reflect.TypeOf(r), name+".Result", map[reflect.Type]bool{})
		walk(reflect.TypeOf(r.SummaryView()), name+".SummaryView", map[reflect.Type]bool{})
	}
}

// TS-10-11 (unit): InputInfo's string fields are deliberately untagged;
// CheckTrust would report them, so the shipped test must never walk it.
//
// Verifies: 10-REQ-2.4
func TestTS10_11_InputInfoWouldBeReported(t *testing.T) {
	missing := toolio.CheckTrust(reflect.TypeOf(toolio.InputInfo{}))
	for _, want := range []string{"Kind", "Origin"} {
		if !slices.Contains(missing, want) {
			t.Errorf("CheckTrust(InputInfo) = %v, want it to contain %q", missing, want)
		}
	}
}
