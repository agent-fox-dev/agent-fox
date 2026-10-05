package conform

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func submitReview(t *testing.T, root string, scope ReviewScope, r Review) (Review, string) {
	t.Helper()
	var sink reviewSink
	tool := SubmitReviewTool(root, scope, &sink)
	in, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Execute(context.Background(), in)
	if !res.OK {
		return Review{}, res.Error + ": " + res.Detail
	}
	got, ok := sink.get()
	if !ok || !res.Terminate {
		t.Fatal("an accepted review did not end the phase")
	}
	return got, ""
}

// The review is refused until it answers every id in scope, once, with
// evidence that can be looked up. That is what a program can hold an
// independent reviewer to.
func TestSubmitReviewRefusesAnIncompleteOrUncitedReview(t *testing.T) {
	_, root, _ := newRepo(t, map[string]string{"cmd/x/main.go": "package main\n\nfunc main() {\n\tos.Exit(2)\n}\n",
		"cmd/x/main_test.go": "package main\n\nfunc TestExit(t *testing.T) {}\n"})
	scope := ReviewScope{Requirements: []string{"20-REQ-1.1", "20-REQ-1.2"}, Tests: []string{"TS-20-1"},
		Decisions: []Decision{{ID: "D-1", Text: "extract a shared helper"}}}
	good := Review{
		Summary: "Exit code differs.",
		Requirements: []RequirementRow{
			{ID: "20-req-1.1", Status: StatusDifferent, Evidence: "cmd/x/main.go:4 exits 2, the spec says 3"},
			{ID: "20-REQ-1.2", Status: StatusMissing, Evidence: "nothing in the diff handles the retry"},
		},
		Tests: []TestRow{{ID: "TS-20-1", Test: "cmd/x/main_test.go:3", Assessment: AssessNoAssertions,
			Evidence: "TestExit has an empty body and asserts nothing"}},
		Decisions: []DecisionRow{{ID: "D-1", Status: DecisionNotFollowed,
			Evidence: "the helper was copied into two packages instead"}},
	}

	got, refused := submitReview(t, root, scope, good)
	if refused != "" {
		t.Fatalf("a complete review was refused: %s", refused)
	}
	if got.Requirements[0].ID != "20-REQ-1.1" {
		t.Errorf("ids are not normalized to the scope's spelling: %q", got.Requirements[0].ID)
	}
	keys := []string{}
	for _, b := range got.Blockers() {
		keys = append(keys, b.Key)
	}
	if strings.Join(keys, ",") != "20-REQ-1.1,20-REQ-1.2,TS-20-1,D-1" {
		t.Errorf("blockers = %v", keys)
	}

	for name, mutate := range map[string]func(r *Review){
		"a requirement skipped": func(r *Review) { r.Requirements = r.Requirements[:1] },
		"an id not in scope": func(r *Review) {
			r.Tests = append(r.Tests, TestRow{ID: "TS-99-1", Assessment: AssessMissing, Evidence: "no such test exists anywhere"})
		},
		"an id answered twice":  func(r *Review) { r.Requirements = append(r.Requirements, r.Requirements[1]) },
		"a status off the enum": func(r *Review) { r.Requirements[0].Status = "mostly" },
		"a citation that does not resolve": func(r *Review) {
			r.Requirements[0].Status = StatusImplemented
			r.Requirements[0].Evidence = "cmd/x/main.go:400 returns exit code three"
		},
		"a decision skipped":      func(r *Review) { r.Decisions = nil },
		"a doc finding uncited":   func(r *Review) { r.Docs = []DocFinding{{Doc: "docs/cli.md:9", Code: "cmd/x/main.go:4"}} },
		"a one-word evidence row": func(r *Review) { r.Requirements[1].Evidence = "nope" },
	} {
		r := good
		r.Requirements = append([]RequirementRow(nil), good.Requirements...)
		r.Tests = append([]TestRow(nil), good.Tests...)
		r.Decisions = append([]DecisionRow(nil), good.Decisions...)
		mutate(&r)
		if _, refused := submitReview(t, root, scope, r); refused == "" {
			t.Errorf("%s: the review was accepted", name)
		}
	}
}

func TestShortfallsAreUnmetButDoNotBlock(t *testing.T) {
	r := Review{
		Requirements: []RequirementRow{{ID: "R-1", Status: StatusPartial, Evidence: "x.go:1 handles one of two cases"}},
		Tests:        []TestRow{{ID: "TS-1", Assessment: AssessWeaker, Evidence: "asserts CLIError.Code, not the exit code"}},
	}
	if len(r.Blockers()) != 0 {
		t.Errorf("blockers = %+v", r.Blockers())
	}
	got := r.Shortfalls()
	if len(got) != 2 || got[0].Requirement != "R-1" || got[1].Test != "TS-1" || got[0].Source != SourceReview {
		t.Errorf("shortfalls = %+v", got)
	}
	md := RenderUnmet(got)
	if !strings.HasPrefix(md, "## ⚠️ Unmet requirements") || !strings.Contains(md, "**not tracked**") {
		t.Errorf("unmet block:\n%s", md)
	}
	if RenderUnmet(nil) != "" {
		t.Error("an empty list renders a block")
	}
}

func TestPromptsCarryWhatTheReviewerNeedsAndNothingElse(t *testing.T) {
	p := ReviewPrompt(ReviewInput{Root: "/r", Base: "abc123", Spec: "SPEC BODY",
		Scope:        ReviewScope{Requirements: []string{"R-1"}, Tests: []string{"TS-1"}, Decisions: []Decision{{ID: "D-1", Text: "use a per-request loader"}}},
		ChangedFiles: []string{"a.go"}, TestCommands: []string{"make test"}})
	for _, want := range []string{"git diff abc123", "SPEC BODY", "R-1", "TS-1", "D-1", "use a per-request loader", "`a.go`"} {
		if !strings.Contains(p, want) {
			t.Errorf("the review prompt lacks %q", want)
		}
	}
}

func TestDocSourcesAreCheckedAgainstTheCode(t *testing.T) {
	_, root, _ := newRepo(t, map[string]string{
		"cmd/x/main.go": "package main\n\nconst exitDiverged = 2\n",
		"docs/cli.md":   "exit code 3\n",
	})
	ok := DocSource{Claim: "--fail-on-diverged exits 2", Source: "cmd/x/main.go:3", Quote: "exitDiverged = 2"}
	if err := VerifyDocSource(root, ok); err != nil {
		t.Errorf("a quote on the cited line was refused: %v", err)
	}
	for name, s := range map[string]DocSource{
		"a quote not in the code":   {Claim: "exits 3", Source: "cmd/x/main.go:3", Quote: "exitDiverged = 3"},
		"a line past the end":       {Claim: "exits 2", Source: "cmd/x/main.go:40", Quote: "exitDiverged = 2"},
		"documentation as a source": {Claim: "exits 3", Source: "docs/cli.md:1", Quote: "exit code 3"},
		"no line":                   {Claim: "exits 2", Source: "cmd/x/main.go", Quote: "exitDiverged = 2"},
		"outside the repository":    {Claim: "exits 2", Source: "../x/main.go:3", Quote: "exitDiverged = 2"},
	} {
		if err := VerifyDocSource(root, s); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
