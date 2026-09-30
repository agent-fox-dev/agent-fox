package specgen

import (
	"encoding/json"
	"testing"
)

// TS-06-19 (unit): spec's summary view reduces validation and traceability
// and drops follow_on_specs and split_plan (06-REQ-3.4).
func TestTS06_19_SummaryViewReducesValidationAndTraceabilityAndDropsFollowOnAndSplitPlan(t *testing.T) {
	r := &Result{
		Package: Package{
			SpecDir:        ".specs/12_thing",
			SpecID:         "12_thing",
			SpecName:       "thing",
			Title:          "Thing",
			Status:         "draft",
			Source:         "text",
			Artifacts:      []string{"prd.md", "requirements.json"},
			Requirements:   4,
			Criteria:       8,
			ExecutionPaths: 2,
			Tests:          10,
			Tasks:          5,
			Validation: ValidationReport{
				Valid: false, ErrorCount: 1, WarningCount: 2,
				Errors:   []ValidationItem{{Check: "x", Message: "missing y"}},
				Warnings: []ValidationItem{{Check: "w", Message: "minor"}},
			},
			Traceability: TraceReport{
				CriteriaCovered: 6, CriteriaUncovered: []string{"C-1"},
				PathsCovered: 3, PathsUncovered: []string{"P-2"},
				TestsUnowned: []string{"TS-1"},
			},
			OpenQuestions: []OpenQuestion{{Question: "what about x?", Decision: "assume x", Why: "no reply"}},
			CommentURL:    "https://github.com/o/r/issues/1#comment",
		},
		FollowOnSpecs: []Package{{SpecDir: ".specs/13_other"}},
		Split:         []ScopeReport{{Name: "scope-a", Status: ScopeDone}},
		SplitPlan:     "plan text",
	}

	sv := r.SummaryView()
	b, err := json.Marshal(sv)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	valAny, ok := m["validation"].(map[string]any)
	if !ok {
		t.Fatalf("validation is not an object: %v", m["validation"])
	}
	wantVal := map[string]bool{"valid": true, "error_count": true, "errors": true}
	for k := range valAny {
		if !wantVal[k] {
			t.Errorf("unexpected key %q in validation summary: %v", k, valAny)
		}
	}
	for k := range wantVal {
		if _, ok := valAny[k]; !ok {
			t.Errorf("validation summary is missing %q: %v", k, valAny)
		}
	}

	traceAny, ok := m["traceability"].(map[string]any)
	if !ok {
		t.Fatalf("traceability is not an object: %v", m["traceability"])
	}
	wantTrace := map[string]bool{"criteria_uncovered": true, "paths_uncovered": true, "tests_unowned": true}
	for k := range traceAny {
		if !wantTrace[k] {
			t.Errorf("unexpected key %q in traceability summary: %v", k, traceAny)
		}
	}
	for k := range wantTrace {
		if _, ok := traceAny[k]; !ok {
			t.Errorf("traceability summary is missing %q: %v", k, traceAny)
		}
	}

	if v, ok := m["follow_on_specs"]; ok {
		t.Errorf("expected no follow_on_specs in summary view, got %v", v)
	}
	if v, ok := m["split_plan"]; ok {
		t.Errorf("expected no split_plan in summary view, got %v", v)
	}

	for _, key := range []string{
		"spec_dir", "spec_id", "spec_name", "status", "artifacts",
		"open_questions", "split", "detail",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("summary view is missing %q: %v", key, m)
		}
	}
}
