package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-10-23 (smoke, entry point, schema half): fix's own App — the one main
// runs — prints x-trust on the verification.output and branch nodes of its
// result document, and its ordinary envelope document describes the
// untrusted_fields array. The run half, against a scripted model and a
// failing check, is TestTS10_23_* in internal/toolio/untrusted_smoke_test.go,
// where the faux-model harness lives.
//
// Verifies: 10-PATH-1, 10-REQ-3.1
func TestTS10_23_FixEntryPointSchemaCarriesTrustLabels_Smoke(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := newApp().Main(context.Background(), []string{"--schema"}, strings.NewReader(""), &stdout, &stderr); code != toolio.ExitOK {
		t.Fatalf("--schema: code %d; stderr:\n%s", code, stderr.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("--schema output is not JSON: %v", err)
	}
	envProps := child(t, doc, "result", "properties")
	if _, ok := envProps["untrusted_fields"]; !ok {
		t.Error("the envelope document does not describe untrusted_fields")
	}
	res := child(t, envProps, "result", "properties")
	if got := child(t, res, "verification", "properties", "output")["x-trust"]; got != "external" {
		t.Errorf("verification.output x-trust = %v, want external", got)
	}
	if got := child(t, res, "branch")["x-trust"]; got != "fact" {
		t.Errorf("branch x-trust = %v, want fact", got)
	}
}
