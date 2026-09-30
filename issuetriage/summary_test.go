package issuetriage

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// TS-06-14 (unit): issue's summary view keeps exactly the documented subset,
// with affected_files reduced to paths only (06-REQ-3.1).
func TestTS06_14_SummaryViewKeepsDocumentedSubset(t *testing.T) {
	r := &Result{
		Action:            "created",
		Repo:              "owner/repo",
		URL:               "https://github.com/owner/repo/issues/1",
		Number:            1,
		UpstreamURL:       "https://github.com/owner/repo/issues/2",
		Title:             "widget double-counts on retry",
		Body:              "the full rendered body of the issue",
		Severity:          "High",
		SeverityRationale: "user-facing data loss",
		Confidence:        "Confirmed",
		Problem:           "the widget package panics",
		Reproduction:      "retry the widget three times",
		RootCause:         "double increment",
		RelatedInstances:  []string{"widget/other.go"},
		AffectedFiles: []FileRef{
			{Path: "widget/count.go", Role: "increments twice"},
			{Path: "widget/retry.go", Role: "calls the guard"},
		},
		SuggestedFix:       Fix{Approach: "move the increment inside the guard"},
		AcceptanceCriteria: []string{"retrying once does not double count"},
		Labels:             []string{"bug", "widget"},
		RejectedPathCalls:  2,
		RejectedPaths:      []string{"does/not/exist.go"},
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

	want := []string{
		"action", "repo", "url", "number", "title", "severity", "confidence",
		"affected_files", "labels", "rejected_path_calls", "detail",
	}
	var got []string
	for k := range m {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("summary keys = %v, want %v", got, want)
	}

	filesAny, ok := m["affected_files"].([]any)
	if !ok {
		t.Fatalf("affected_files is not an array: %T", m["affected_files"])
	}
	if len(filesAny) != 2 {
		t.Fatalf("affected_files = %v, want 2 entries", filesAny)
	}
	for _, f := range filesAny {
		if _, ok := f.(string); !ok {
			t.Errorf("affected_files entry is not a bare path string: %#v", f)
		}
	}
	if filesAny[0] != "widget/count.go" || filesAny[1] != "widget/retry.go" {
		t.Errorf("affected_files = %v", filesAny)
	}

	if m["detail"] != "summary" {
		t.Errorf("detail = %v, want %q", m["detail"], "summary")
	}
}
