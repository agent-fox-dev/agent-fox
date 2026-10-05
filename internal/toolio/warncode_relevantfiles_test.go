package toolio_test

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-13-32 (unit): WarnRelevantFilesUnavailable constant is declared with
// value "relevant_files_unavailable" and stage "prd".
func TestTS_13_32_WarnRelevantFilesUnavailableConstant(t *testing.T) {
	if string(toolio.WarnRelevantFilesUnavailable) != "relevant_files_unavailable" {
		t.Errorf("WarnRelevantFilesUnavailable = %q, want %q",
			string(toolio.WarnRelevantFilesUnavailable), "relevant_files_unavailable")
	}
	stage, ok := toolio.WarnStage(toolio.WarnRelevantFilesUnavailable)
	if !ok {
		t.Fatal("WarnRelevantFilesUnavailable has no entry in the stage table")
	}
	if stage != "prd" {
		t.Errorf("stage = %q, want %q", stage, "prd")
	}
}
