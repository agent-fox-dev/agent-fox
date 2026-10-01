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

type descNested struct {
	Inner string `json:"inner"`
}

type descBad struct {
	A string `json:"a" description:"has one"`
	B string `json:"b"`
}

type descDeep struct {
	Tagged  string       `json:"tagged" description:"has one"`
	List    []descNested `json:"list" description:"a list"`
	Ptr     *descNested  `json:"ptr,omitempty" description:"a pointer"`
	Skipped string       `json:"-"`
	hidden  string
}

type descEmbedded struct {
	descBad
	C string `json:"c" description:"has one"`
}

// TS-09-39 (unit): The exhaustiveness checker names every field reachable
// from a type that lacks the description tag.
func TestTS09_39_CheckDescriptionsNamesUntaggedField(t *testing.T) {
	got := toolio.CheckDescriptions(reflect.TypeOf(descBad{}))
	if !slices.Equal(got, []string{"B"}) {
		t.Fatalf("CheckDescriptions(descBad) = %v, want [B]", got)
	}
}

// TS-09-39 (unit): the checker follows slices and pointers, skips json:"-"
// and unexported fields, and reports a nested field by its path.
func TestTS09_39_CheckDescriptionsFollowsNestedTypes(t *testing.T) {
	_ = descDeep{}.hidden
	got := toolio.CheckDescriptions(reflect.TypeOf(descDeep{}))
	want := []string{"List.Inner", "Ptr.Inner"}
	if !slices.Equal(got, want) {
		t.Fatalf("CheckDescriptions(descDeep) = %v, want %v", got, want)
	}
}

// An embedded struct's own fields are checked as promoted ones.
func TestTS09_39_CheckDescriptionsPromotesEmbeddedFields(t *testing.T) {
	got := toolio.CheckDescriptions(reflect.TypeOf(descEmbedded{}))
	if !slices.Equal(got, []string{"B"}) {
		t.Fatalf("CheckDescriptions(descEmbedded) = %v, want [B]", got)
	}
}

// TS-09-39 (unit): the envelope and the four Result types are fully tagged.
func TestTS09_39_EnvelopeAndResultsFullyTagged(t *testing.T) {
	for _, sample := range []any{toolio.Envelope{}, codefix.Result{}, codeimpl.Result{}, issuetriage.Result{}, specgen.Result{}} {
		typ := reflect.TypeOf(sample)
		if missing := toolio.CheckDescriptions(typ); missing != nil {
			t.Errorf("%s: fields without a description tag: %v", typ, missing)
		}
	}
}
