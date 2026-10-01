package checks

import (
	"reflect"
	"testing"
)

// TS-10-2 (unit): a field of a non-string/[]string Go type, and a field
// tagged json:"-", carry no trust tag.
//
// Verifies: 10-REQ-1.2
func TestTS10_2_NonStringFieldsCarryNoTrustTag(t *testing.T) {
	ct := reflect.TypeOf(Result{})
	for _, name := range []string{"OK", "ExitCode", "TimedOut", "DurationMS", "Skipped"} {
		f, ok := ct.FieldByName(name)
		if !ok {
			t.Fatalf("checks.Result has no field %s", name)
		}
		if got := f.Tag.Get("trust"); got != "" {
			t.Errorf("checks.Result.%s trust tag = %q, want none", name, got)
		}
	}
	// The string fields are classified.
	for name, want := range map[string]string{"Command": "fact", "Output": "external"} {
		f, _ := ct.FieldByName(name)
		if got := f.Tag.Get("trust"); got != want {
			t.Errorf("checks.Result.%s trust tag = %q, want %q", name, got, want)
		}
	}

	type syn struct {
		Hidden string `json:"-"`
		Flag   bool
		Tagged string `json:"tagged" trust:"model"`
	}
	st := reflect.TypeOf(syn{})
	if got := st.Field(0).Tag.Get("trust"); got != "" {
		t.Errorf("json:\"-\" field trust tag = %q, want none", got)
	}
	if got := st.Field(1).Tag.Get("trust"); got != "" {
		t.Errorf("bool field trust tag = %q, want none", got)
	}
	if got := st.Field(2).Tag.Get("trust"); got != "model" {
		t.Errorf("string field trust tag = %q, want model", got)
	}
}
