package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentfox/agentkit-go/codesearch"

	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/envtest"
	"github.com/agent-fox-dev/agentfox/specgen"
)

func indexHarness() indextest.Harness {
	return indextest.Harness{
		NewApp:   newApp,
		Indexed:  indexed,
		NewIndex: &newIndex,
		Setup: func(t *testing.T) []string {
			envtest.Clean(t)
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("ANTHROPIC_API_KEY", "test-key")
			t.Setenv(specgen.SpecDirEnv, "")
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return []string{"--dir", dir, "a library that does a thing"}
		},
	}
}

// TS-16-10 (unit): Index.Close is called exactly once on a successful run.
//
// Verifies: 16-REQ-3.2, 16-REQ-3.3
func TestTS16_10_SpecClosesTheIndexOnSuccess(t *testing.T) { indextest.TS10(t, indexHarness()) }

// TS-16-11 (unit): Index.Close is called exactly once when the run fails.
//
// Verifies: 16-REQ-3.3
func TestTS16_11_SpecClosesTheIndexOnFailure(t *testing.T) { indextest.TS11(t, indexHarness()) }

// TS-16-12 (unit): Index.Close is called exactly once on cancellation.
//
// Verifies: 16-REQ-3.3
func TestTS16_12_SpecClosesTheIndexOnCancellation(t *testing.T) {
	indextest.TS12(t, indexHarness())
}

// TS-16-13 (unit): the run continues, with a low warning, when the index
// cannot be built.
//
// Verifies: 16-REQ-3.4
func TestTS16_13_SpecRunsWithoutAnIndexThatCannotBeBuilt(t *testing.T) {
	indextest.TS13(t, indexHarness(), codesearch.ErrUnsupported)
}

// TS-16-14 (unit): the index is built once and the one instance reaches the
// pipeline.
//
// Verifies: 16-REQ-3.1, 16-REQ-3.5
func TestTS16_14_SpecBuildsOneIndex(t *testing.T) { indextest.TS14(t, indexHarness()) }
