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
