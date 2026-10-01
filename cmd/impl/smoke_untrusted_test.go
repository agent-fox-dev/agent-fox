package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-10-24 (smoke, entry point): impl's own App — the one main runs — labels
// the survey's prose "model" on its --schema result document, and an ordinary
// envelope that carries no classified text prints no untrusted_fields key.
// The scripted-model --detail summary / --detail full runs are
// TestTS10_24_* in internal/toolio/untrusted_smoke_test.go, where the
// faux-model harness lives.
//
// Verifies: 10-PATH-2, 10-REQ-4.6
func TestTS10_24_ImplEntryPointSchemaAndEnvelope_Smoke(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := newApp().Main(context.Background(), []string{"--schema"}, strings.NewReader(""), &stdout, &stderr); code != toolio.ExitOK {
		t.Fatalf("--schema: code %d; stderr:\n%s", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("--schema output is not JSON: %v", err)
	}
	node := doc["result"].(map[string]any)
	for _, k := range []string{"result", "survey", "summary"} {
		props, _ := node["properties"].(map[string]any)
		next, ok := props[k].(map[string]any)
		if !ok {
			t.Fatalf("result document has no %q node", k)
		}
		node = next
	}
	if node["x-trust"] != "model" {
		t.Errorf("survey.summary x-trust = %v, want model", node["x-trust"])
	}

	// A usage error has no result: nothing to classify, so no key at all.
	stdout.Reset()
	stderr.Reset()
	code := newApp().Main(context.Background(), []string{"--detail", "bogus", "some input"}, strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitUsage {
		t.Fatalf("code = %d, want %d", code, toolio.ExitUsage)
	}
	var env map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v", err)
	}
	if _, ok := env["untrusted_fields"]; ok {
		t.Errorf("a result-less envelope carries untrusted_fields: %s", stdout.String())
	}
}
