package specgen

import (
	"reflect"
	"testing"
)

func trustOf(t *testing.T, typ reflect.Type, name string) string {
	t.Helper()
	f, ok := typ.FieldByName(name)
	if !ok {
		t.Fatalf("%s has no field %s", typ, name)
	}
	return f.Tag.Get("trust")
}

// TS-10-7 (unit): Package, ValidationItem, TraceReport, OpenQuestion and
// ScopeReport carry the split of fact and model the table declares.
//
// Verifies: 10-REQ-1.7
func TestTS10_7_SpecgenTrustSplit(t *testing.T) {
	cases := []struct {
		typ    reflect.Type
		want   string
		fields []string
	}{
		{reflect.TypeOf(Package{}), "model", []string{"Title"}},
		{reflect.TypeOf(Package{}), "fact", []string{"SpecDir", "SpecID", "SpecName", "Status", "Source", "Artifacts", "CommentURL", "Detail"}},
		{reflect.TypeOf(ScopeReport{}), "model", []string{"Name", "Scope"}},
		{reflect.TypeOf(ScopeReport{}), "fact", []string{"Status", "SpecID", "SpecDir"}},
		{reflect.TypeOf(ValidationItem{}), "fact", []string{"Check", "Artifact", "Entity", "Message"}},
		{reflect.TypeOf(TraceReport{}), "fact", []string{"CriteriaUncovered", "PathsUncovered", "TestsUnowned"}},
		{reflect.TypeOf(OpenQuestion{}), "model", []string{"Question", "Decision", "Why"}},
		{reflect.TypeOf(Result{}), "fact", []string{"SplitPlan"}},
	}
	for _, c := range cases {
		for _, name := range c.fields {
			if got := trustOf(t, c.typ, name); got != c.want {
				t.Errorf("%s.%s trust = %q, want %s", c.typ, name, got, c.want)
			}
		}
	}
	// Non-string fields carry no tag.
	if got := trustOf(t, reflect.TypeOf(Result{}), "DryRun"); got != "" {
		t.Errorf("Result.DryRun trust = %q, want none", got)
	}
}

// The summary view reuses the tagged nested types and tags its own string
// fields; no string-typed field is left without a tag.
//
// Verifies: 10-REQ-1.1
func TestTS10_7_SummaryViewTagged(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeOf(Result{}), reflect.TypeOf(summaryResult{}),
	} {
		for _, p := range untaggedStrings(typ, typ.Name(), map[reflect.Type]bool{}) {
			t.Errorf("string field without trust tag: %s", p)
		}
	}
}

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
