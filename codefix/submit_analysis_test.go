package codefix

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

func submitAnalysis(t *testing.T, fields map[string]any) (ok bool, errCode string, dest *analysis) {
	t.Helper()
	dest = &analysis{}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	res := submitAnalysisTool(dest).Execute(context.Background(), b)
	return res.OK, res.Error, dest
}

func validAnalysis() map[string]any {
	return map[string]any{
		"classification": "bug", "title": "stop the double count", "summary": "It counts twice.",
		"root_cause": "The loop adds twice.", "approach": "Add once.",
		"files": []any{map[string]any{"path": "internal/x/x.go", "change": "add once"}},
	}
}

// The plan is required of a diagnosis and not of a question; the paths in it
// stay inside the repository (#68).
func TestSubmitAnalysisValidation(t *testing.T) {
	if ok, code, _ := submitAnalysis(t, validAnalysis()); !ok {
		t.Fatalf("a complete analysis was refused: %s", code)
	}

	for name, mutate := range map[string]func(m map[string]any){
		"blank summary":    func(m map[string]any) { m["summary"] = "  " },
		"blank root cause": func(m map[string]any) { m["root_cause"] = "" },
		"blank approach":   func(m map[string]any) { delete(m, "approach") },
		"no files":         func(m map[string]any) { m["files"] = []any{} },
		"absolute path": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": "/etc/passwd", "change": "x"}}
		},
		"parent path": func(m map[string]any) {
			m["files"] = []any{map[string]any{"path": "../x/y.go", "change": "x"}}
		},
	} {
		m := validAnalysis()
		mutate(m)
		if ok, _, _ := submitAnalysis(t, m); ok {
			t.Errorf("%s: accepted", name)
		}
	}

	// With an ambiguity the run stops, so no plan is demanded.
	ask := map[string]any{
		"classification": "bug", "title": "which reading", "summary": "The report reads two ways.",
		"ambiguity": map[string]any{"question": "A or B?", "interpretation_a": "a", "interpretation_b": "b"},
	}
	if ok, code, dest := submitAnalysis(t, ask); !ok {
		t.Errorf("an ambiguity without a plan was refused: %s", code)
	} else if got, done := dest.get(); !done || got.Ambiguity == nil {
		t.Errorf("the ambiguity was not recorded: %+v", got)
	}

	schema := analysisSchema()
	for _, f := range []string{"approach", "files", "root_cause"} {
		for _, req := range schema.Required {
			if req == f {
				t.Errorf("%s is still required by the tool schema", f)
			}
		}
	}
}

func TestRejectionsReachTheError(t *testing.T) {
	var rej rejections
	dest := &analysis{}
	tool := rej.track(submitAnalysisTool(dest))
	tool.Execute(context.Background(), []byte(`{"classification":"bug","title":"t","summary":"s"}`))
	tool.Execute(context.Background(), []byte(`{"classification":"bug","title":"t","summary":"s","root_cause":"r"}`))
	err := rej.wrap(errors.New("ended without calling submit_analysis"))
	for _, want := range []string{"ended without calling", "2 submission(s) were rejected", "missing_approach"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if (&rejections{}).wrap(errors.New("x")).Error() != "x" {
		t.Error("an unrejected phase's error was changed")
	}
}

func TestAnalysisPromptNamesTheFields(t *testing.T) {
	for _, want := range []string{"root_cause", "approach", "files", "ambiguity"} {
		if !strings.Contains(analysisSystemPrompt, want) {
			t.Errorf("the analysis prompt does not name %q", want)
		}
	}
}

func TestAnalysisCommentLeadsWithTitleAndSummary(t *testing.T) {
	got := analysisComment(Analysis{Title: "stop the double count", Summary: "It counts twice.",
		Classification: "bug", RootCause: "r", Approach: "a"}, nil, "fix/x", "make test", checks.Result{Command: "make test", OK: true})
	if !strings.Contains(got, "**stop the double count**") || !strings.Contains(got, "It counts twice.") {
		t.Errorf("comment = %s", got)
	}
}

func TestBranchPrefixIsConfigurable(t *testing.T) {
	if got := branchPrefix(Options{}, ClassBug); got != "fix" {
		t.Errorf("default bug prefix = %q", got)
	}
	if got := branchPrefix(Options{BranchPrefix: "feature"}, ClassBug); got != "feature" {
		t.Errorf("configured prefix = %q", got)
	}
	for _, ok := range []string{"feature", "agent/fix", "team-a/hotfix_1.0"} {
		if !ValidBranchPrefix(ok) {
			t.Errorf("%q was refused", ok)
		}
	}
	for _, bad := range []string{"", "/feature", "feature/", "a//b", "a b", "../x", "a/../b", "-x", "feat~1"} {
		if ValidBranchPrefix(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
}
