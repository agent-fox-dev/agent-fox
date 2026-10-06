package main

import (
	"testing"

	"github.com/agentfox/agentkit-go/codesearch"

	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/envtest"
)

func indexHarness() indextest.Harness {
	return indextest.Harness{
		NewApp: newApp,
		Setup: func(t *testing.T) []string {
			envtest.Clean(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			t.Setenv("GITHUB_TOKEN", "test-token")
			return []string{"--dir", t.TempDir(), "--repo", "acme/widgets", "a report"}
		},
	}
}

// TS-16-10 (unit): Index.Close is called exactly once on a successful run.
//
// Verifies: 16-REQ-3.2, 16-REQ-3.3
func TestTS16_10_TriageClosesTheIndexOnSuccess(t *testing.T) { indextest.TS10(t, indexHarness()) }

// TS-16-11 (unit): Index.Close is called exactly once when the run fails.
//
// Verifies: 16-REQ-3.3
func TestTS16_11_TriageClosesTheIndexOnFailure(t *testing.T) { indextest.TS11(t, indexHarness()) }

// TS-16-12 (unit): Index.Close is called exactly once on cancellation.
//
// Verifies: 16-REQ-3.3
func TestTS16_12_TriageClosesTheIndexOnCancellation(t *testing.T) {
	indextest.TS12(t, indexHarness())
}

// TS-16-13 (unit): the run continues, with a low warning, when the index
// cannot be built.
//
// Verifies: 16-REQ-3.4
func TestTS16_13_TriageRunsWithoutAnIndexThatCannotBeBuilt(t *testing.T) {
	indextest.TS13(t, indexHarness(), codesearch.ErrUnsupported)
}

// TS-16-14 (unit): the index is built once and the one instance reaches the
// pipeline.
//
// Verifies: 16-REQ-3.1, 16-REQ-3.5
func TestTS16_14_TriageBuildsOneIndex(t *testing.T) { indextest.TS14(t, indexHarness()) }
