package toolio_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// keyOrder reports the keys of a marshalled JSON object's top level, in order.
func keyOrder(t *testing.T, raw string) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("not an object: %s", raw)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

// TS-10-12 (unit): SchemaFor sets x-trust on a tagged field's node
// immediately after description.
//
// Verifies: 10-REQ-3.1
func TestTS10_12_SchemaForSetsXTrustAfterDescription(t *testing.T) {
	type T struct {
		RootCause string   `json:"root_cause,omitempty" trust:"model" description:"why it happened"`
		Notes     []string `json:"notes,omitempty" trust:"external" description:"copied text"`
		Bare      string   `json:"bare,omitempty" trust:"fact"`
	}
	s := toolio.SchemaFor(reflect.TypeOf(T{}))
	props := obj(t, field(t, s, "properties"))

	node := obj(t, field(t, props, "root_cause"))
	if got := field(t, node, "x-trust"); got != "model" {
		t.Errorf("x-trust = %v, want model", got)
	}
	want := []string{"type", "description", "x-trust"}
	if got := keyOrder(t, mustJSON(t, node)); !reflect.DeepEqual(got, want) {
		t.Errorf("key order = %v, want %v", got, want)
	}

	// A []string carries the label on the array node, not its items.
	arr := obj(t, field(t, props, "notes"))
	if got := field(t, arr, "x-trust"); got != "external" {
		t.Errorf("array x-trust = %v, want external", got)
	}
	if _, ok := obj(t, field(t, arr, "items")).Get("x-trust"); ok {
		t.Errorf("array items carry x-trust: %s", mustJSON(t, arr))
	}

	// Without a description, x-trust follows type.
	bare := obj(t, field(t, props, "bare"))
	if got := keyOrder(t, mustJSON(t, bare)); !reflect.DeepEqual(got, []string{"type", "x-trust"}) {
		t.Errorf("bare key order = %v", got)
	}
}

// TS-10-13 (unit): A property whose field carries no trust tag gets no
// x-trust key at all.
//
// Verifies: 10-REQ-3.2
func TestTS10_13_NoTrustTagMeansNoXTrustKey(t *testing.T) {
	type T struct {
		N int    `json:"n" description:"a count"`
		S string `json:"s" trust:"fact" description:"d"`
	}
	s := toolio.SchemaFor(reflect.TypeOf(T{}))
	props := obj(t, field(t, s, "properties"))

	n := obj(t, field(t, props, "n"))
	if _, ok := n.Get("x-trust"); ok {
		t.Errorf("an untagged int carries x-trust: %s", mustJSON(t, n))
	}
	if strings.Contains(mustJSON(t, n), "x-trust") {
		t.Errorf("x-trust rendered for an untagged field: %s", mustJSON(t, n))
	}
	if got := field(t, obj(t, field(t, props, "s")), "x-trust"); got != "fact" {
		t.Errorf("s x-trust = %v, want fact", got)
	}
}
