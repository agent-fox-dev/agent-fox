package toolio

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// TS-12-36 (unit): The text event has keys phase, turn, text after the header
// and no delta.
func TestTS12_36_TextEventKeysPhaseAndTurnAndTextNoDelta(t *testing.T) {
	var buf bytes.Buffer
	s := newEventsSink("impl", &buf)
	p := NewProgress(nil, "impl", false, false)
	p.SetEvents(s)

	// Call the per-turn text method.
	p.Text("p", 2, "hi")

	objs := decodeLines(t, buf.String())
	if len(objs) != 1 {
		t.Fatalf("got %d events, want 1", len(objs))
	}
	obj := objs[0]

	// Check key order: ts, tool, session_id, type, phase, turn, text
	raw := buf.Bytes()
	var orderedKeys []string
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token() // opening {
	if err != nil || tok != json.Delim('{') {
		t.Fatalf("expected opening brace, got %v %v", tok, err)
	}
	for dec.More() {
		tok, err = dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		key, ok := tok.(string)
		if !ok {
			t.Fatalf("expected string key, got %T", tok)
		}
		orderedKeys = append(orderedKeys, key)
		// skip value
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}

	wantKeys := []string{"ts", "tool", "session_id", "type", "phase", "turn", "text"}
	if len(orderedKeys) != len(wantKeys) {
		t.Fatalf("keys = %v, want %v", orderedKeys, wantKeys)
	}
	for i, k := range wantKeys {
		if orderedKeys[i] != k {
			t.Errorf("key[%d] = %q, want %q; all keys: %v", i, orderedKeys[i], k, orderedKeys)
		}
	}

	// type is "text"
	if obj["type"] != "text" {
		t.Errorf("type = %v, want text", obj["type"])
	}

	// no delta key
	if _, has := obj["delta"]; has {
		t.Errorf("text event has a delta key: %v", obj)
	}

	// phase, turn, text values
	if obj["phase"] != "p" {
		t.Errorf("phase = %v, want p", obj["phase"])
	}
	if obj["turn"] != float64(2) {
		t.Errorf("turn = %v, want 2", obj["turn"])
	}
	if obj["text"] != "hi" {
		t.Errorf("text = %v, want hi", obj["text"])
	}
}

// TS-12-41 (unit): Observer has a per-turn text method that Progress
// implements by emitting a text event.
func TestTS12_41_ObserverTextMethodProgressEmits(t *testing.T) {
	// Compile-time check: Progress satisfies Observer.
	var _ agentrun.Observer = (*Progress)(nil)

	var buf bytes.Buffer
	s := newEventsSink("t", &buf)
	p := NewProgress(nil, "t", false, false)
	p.SetEvents(s)

	p.Text("ph", 3, "body")

	objs := decodeLines(t, buf.String())
	if len(objs) != 1 {
		t.Fatalf("got %d events, want 1", len(objs))
	}
	ev := objs[0]
	if ev["type"] != "text" {
		t.Errorf("type = %v, want text", ev["type"])
	}
	if ev["phase"] != "ph" {
		t.Errorf("phase = %v, want ph", ev["phase"])
	}
	if ev["turn"] != float64(3) {
		t.Errorf("turn = %v, want 3", ev["turn"])
	}
	if ev["text"] != "body" {
		t.Errorf("text = %v, want body", ev["text"])
	}
}
