package toolio_test

import (
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-05-35 (unit): every declared WarnCode constant has an entry in the stage table.
func TestTS05_35_EveryDeclaredWarnCodeHasStage(t *testing.T) {
	for _, code := range toolio.DeclaredWarnCodes() {
		if _, ok := toolio.WarnStage(code); !ok {
			t.Errorf("WarnCode %q has no entry in the stage table", code)
		}
	}
}

// TS-13-11 (unit): The effort_clamped WarnCode constant exists and maps to
// stage preflight in warnStages.
func TestTS13_11_EffortClampedWarnCodeMapsToPreflight(t *testing.T) {
	stage, ok := toolio.WarnStage(toolio.WarnEffortClamped)
	if !ok {
		t.Fatal("WarnEffortClamped has no entry in the stage table")
	}
	if stage != "preflight" {
		t.Errorf("WarnEffortClamped stage = %q, want %q", stage, "preflight")
	}
}

// TS-05-38 (unit): the WarnCode stage table carries every code-to-stage pair the spec lists.
func TestTS05_38_StageTableCarriesEveryListedPair(t *testing.T) {
	pairs := map[string]string{
		"input_truncated":            "input",
		"comments_unreadable":        "input",
		"no_verify_command":          "preflight",
		"criteria_unmet":             "implement",
		"commit_not_parked":          "park",
		"checkout_not_restored":      "park",
		"pull_request_not_opened":    "land",
		"comment_not_posted":         "report",
		"spec_edit_reverted":         "task",
		"state_not_saved":            "park",
		"draft_package":              "preflight",
		"upstream_missing":           "preflight",
		"parked_attempt_discarded":   "preflight",
		"spec_validation_warning":    "preflight",
		"specs_dir_unreadable":       "preflight",
		"project_language_unknown":   "preflight",
		"name_flag_ignored":          "usage",
		"scope_renamed":              "prd",
		"scope_count_mismatch":       "prd",
		"split_plan_foreign":         "split",
		"split_plan_unreadable":      "split",
		"split_plan_stale":           "split",
		"split_plan_update_failed":   "split",
		"split_plan_not_removed":     "split",
		"architecture_not_written":   "write",
		"activation_failed":          "activate",
		"rejected_path_calls":        "analyse",
		"report_file_not_written":    "report",
		"input_looks_like_path":      "input",
		"gate_edited":                "task",
		"docs_not_updated":           "task",
		"tool_errors":                "task",
		"scratch_file_removed":       "task",
		"scratch_file_suspected":     "task",
		"output_not_written":         "emit",
		"output_matches_report_file": "emit",
		"events_file_not_written":    "report",
		"effort_clamped":             "preflight",
		"review_not_run":             "review",
		"unmet_requirements":         "review",
		"deviation_not_tracked":      "land",
		"fix_not_proven":             "verify",
		"relevant_files_unavailable": "prd",
		"repo_map_build_failed":      "triage",
	}
	for code, wantStage := range pairs {
		got, ok := toolio.WarnStage(toolio.WarnCode(code))
		if !ok {
			t.Errorf("WarnCode %q: no table entry", code)
			continue
		}
		if got != wantStage {
			t.Errorf("WarnCode %q: stage = %q, want %q", code, got, wantStage)
		}
	}
	// And no declared constant is missing from this list, so the test does
	// not silently drift from warncode.go.
	if got, want := len(toolio.DeclaredWarnCodes()), len(pairs); got != want {
		t.Errorf("DeclaredWarnCodes() has %d entries, the spec's table has %d", got, want)
	}
}
