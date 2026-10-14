package toolio_test

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-14-34 (unit): WarnRepoMapBuildFailed is declared with value
// "repo_map_build_failed", has a stage in the table and is in
// DeclaredWarnCodes().
func TestTS_14_34_WarnRepoMapBuildFailedConstant(t *testing.T) {
	if toolio.WarnRepoMapBuildFailed != toolio.WarnCode("repo_map_build_failed") {
		t.Errorf("WarnRepoMapBuildFailed = %q, want %q",
			string(toolio.WarnRepoMapBuildFailed), "repo_map_build_failed")
	}
	stage, ok := toolio.WarnStage(toolio.WarnRepoMapBuildFailed)
	if !ok {
		t.Fatal("WarnRepoMapBuildFailed has no entry in the stage table")
	}
	if stage == "" {
		t.Error("WarnRepoMapBuildFailed has an empty stage")
	}
	found := false
	for _, c := range toolio.DeclaredWarnCodes() {
		if c == toolio.WarnRepoMapBuildFailed {
			found = true
		}
	}
	if !found {
		t.Error("WarnRepoMapBuildFailed is missing from DeclaredWarnCodes()")
	}
}
