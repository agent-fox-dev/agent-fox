package issuetriage

import (
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func artifactAsMap(t *testing.T, a toolio.Artifact) map[string]any {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("Marshal artifact: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal artifact: %v", err)
	}
	return m
}

// TS-06-23 (unit, issue half): under --dry-run, no issue was actually
// filed, so the issue artifact carries dry_run: true, with an empty url
// and number (06-REQ-4.3).
func TestTS0623_DryRunIssueMarkedHypotheticalWithEmptyURLAndNumber(t *testing.T) {
	r := &Result{
		Action: "none",
		DryRun: true,
	}
	artifacts := r.Artifacts()
	if len(artifacts) != 1 {
		t.Fatalf("Artifacts() = %d entries, want 1: %+v", len(artifacts), artifacts)
	}
	m := artifactAsMap(t, artifacts[0])
	if m["kind"] != string(toolio.ArtifactIssue) {
		t.Fatalf("kind = %v, want issue", m["kind"])
	}
	if dr, _ := m["dry_run"].(bool); !dr {
		t.Errorf("dry_run = %v, want true", m["dry_run"])
	}
	if url, _ := m["url"].(string); url != "" {
		t.Errorf("url = %q, want empty", url)
	}
	if n, _ := m["number"].(float64); n != 0 {
		t.Errorf("number = %v, want 0", n)
	}
}

// A run that actually filed an issue reports it without a dry_run marker,
// and the entry carries the real url and number (06-REQ-4.1, 06-REQ-4.5).
func TestArtifactsReportsAFiledIssueWithoutDryRunMarker(t *testing.T) {
	r := &Result{
		Action: "created",
		URL:    "https://github.com/o/r/issues/9",
		Number: 9,
	}
	artifacts := r.Artifacts()
	if len(artifacts) != 1 {
		t.Fatalf("Artifacts() = %d entries, want 1", len(artifacts))
	}
	m := artifactAsMap(t, artifacts[0])
	if _, ok := m["dry_run"]; ok {
		t.Errorf("a filed issue should carry no dry_run marker: %v", m)
	}
	if got, want := m["url"], r.URL; got != want {
		t.Errorf("artifacts issue.url = %v, want result.url = %v", got, want)
	}
	if got, want := m["number"], float64(r.Number); got != want {
		t.Errorf("artifacts issue.number = %v, want result.number = %v", got, want)
	}
}
