package issuetriage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// These tests drive the REAL pipeline — the real agent loop, the real
// read-only tool set, the real file_issue handler and its path check — with a
// scripted provider in the vendor's place. No key, no network, and no double
// of this package's own types.

func newWorkspace(t *testing.T) *tools.Workspace {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"session.go":      "package session\n\nfunc Refresh() {}\n",
		"session_test.go": "package session\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func newRunner(t *testing.T, ws *tools.Workspace, turns ...faux.Turn) *agentrun.Runner {
	t.Helper()
	p := faux.New(turns...)
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "issue",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func toolCall(id, name string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxToolCall(id, name, string(raw))},
		StopReason: core.StopReasonToolUse,
	}
}

// validIssue is a file_issue payload the handler accepts against the
// workspace built above.
func validIssue(paths ...string) map[string]any {
	if len(paths) == 0 {
		paths = []string{"session.go"}
	}
	affected := make([]any, 0, len(paths))
	for _, p := range paths {
		affected = append(affected, map[string]any{"path": p, "role": "where the fault is"})
	}
	return map[string]any{
		"title":          "session: token refresh skips the expiry check",
		"problem":        "Cached credentials are reused past their expiry.",
		"reproduction":   "Call Refresh twice within the cache window.",
		"confidence":     "Confirmed",
		"root_cause":     "Refresh returns the cached token without comparing its expiry.",
		"affected_files": affected,
		"suggested_fix": map[string]any{
			"approach": "Compare the cached token's expiry before returning it.",
			"files": []any{
				map[string]any{"path": "session.go", "role": "add the expiry comparison"},
				map[string]any{"path": "session_expiry_test.go", "role": "add the regression test"},
			},
			"risks": "None identified",
		},
		"acceptance_criteria": []any{"Given an expired cached token, when Refresh is called, then a new token is fetched"},
		"severity":            "High",
		"severity_rationale":  "Requests are made with credentials the server rejects.",
	}
}

func newOptions(t *testing.T, ws *tools.Workspace, runner *agentrun.Runner) Options {
	t.Helper()
	return Options{
		Input: toolio.Input{
			Kind: toolio.KindText, Origin: "argument",
			Body: "token refresh returns an expired credential",
		},
		Workspace: ws,
		Repo:      ghapi.Repo{Owner: "acme", Name: "widgets"},
		Runner:    runner,
		Run:       toolio.NewRun("issue", "test"),
		Progress:  toolio.NewProgress(io.Discard, "issue", false, true),
		DryRun:    true,
	}
}

func TestTriageProducesARenderedIssue(t *testing.T) {
	ws := newWorkspace(t)
	o := newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue())))

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Action != "none" {
		t.Errorf("Action = %q, want none under --dry-run", got.Action)
	}
	if got.Severity != "High" || got.Confidence != "Confirmed" {
		t.Errorf("severity/confidence = %q/%q", got.Severity, got.Confidence)
	}
	for _, want := range []string{
		"## Problem", "## Root Cause Analysis", "**Confidence:** Confirmed",
		"## Affected Files", "`session.go`", "## Acceptance Criteria", "**AC-1:**",
		"## Severity", "**High**",
	} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("the rendered body is missing %q:\n%s", want, got.Body)
		}
	}
}

// Render is a pure function of a validated struct, so two runs that reach the
// same diagnosis produce byte-identical documents.
func TestRenderIsDeterministic(t *testing.T) {
	var issue Issue
	raw, _ := json.Marshal(validIssue())
	if err := json.Unmarshal(raw, &issue); err != nil {
		t.Fatal(err)
	}
	first := issue.Render("text", "argument")
	if second := issue.Render("text", "argument"); first != second {
		t.Fatal("Render is not deterministic")
	}
	if !strings.Contains(first, "No related instances found.") {
		t.Error("an empty related-instances list should render as a sentence, not as nothing")
	}
	if !strings.Contains(first, "[`issue`](https://github.com/agent-fox-dev/agent-fox)") {
		t.Errorf("the attribution footer should link the tool name to the agent-fox repo:\n%s", first)
	}
}

// A path the model made up is the most common way a machine-written triage
// issue wastes a reader's time, and it is mechanically detectable. The
// refusal is an error result, so the model corrects itself and the run
// continues.
func TestAnUncitedPathIsRefusedAndRepaired(t *testing.T) {
	ws := newWorkspace(t)
	runner := newRunner(t, ws,
		toolCall("c1", ToolFileIssue, validIssue("session/imagined.go")),
		toolCall("c2", ToolFileIssue, validIssue("session.go")),
	)
	got, err := Run(context.Background(), newOptions(t, ws, runner))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.RejectedPathCalls != 1 {
		t.Errorf("RejectedPathCalls = %d, want 1", got.RejectedPathCalls)
	}
	if len(got.RejectedPaths) != 1 || got.RejectedPaths[0] != "session/imagined.go" {
		t.Errorf("RejectedPaths = %v", got.RejectedPaths)
	}
	if !strings.Contains(got.Body, "`session.go`") {
		t.Error("the repaired issue should cite the real path")
	}
}

// A directory is refused too: "affected file: session/" is the model naming
// the neighbourhood rather than the file it read.
func TestADirectoryIsNotAnAffectedFile(t *testing.T) {
	ws := newWorkspace(t)
	if err := os.Mkdir(filepath.Join(ws.Root, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newRunner(t, ws,
		toolCall("c1", ToolFileIssue, validIssue("session")),
		toolCall("c2", ToolFileIssue, validIssue("session.go")),
	)
	got, err := Run(context.Background(), newOptions(t, ws, runner))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.RejectedPathCalls != 1 {
		t.Errorf("a directory was accepted as an affected file")
	}
}

// suggested_fix.files is checked differently: a fix legitimately adds a file
// that does not exist yet, so the requirement there is containment.
func TestASuggestedNewFileIsAllowedButAnEscapeIsNot(t *testing.T) {
	ws := newWorkspace(t)

	// The valid payload already proposes session_expiry_test.go, which does
	// not exist. It must be accepted.
	got, err := Run(context.Background(), newOptions(t, ws,
		newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue()))))
	if err != nil {
		t.Fatalf("a proposed new file was refused: %v", err)
	}
	if got.RejectedPathCalls != 0 {
		t.Errorf("RejectedPathCalls = %d for a proposed new file", got.RejectedPathCalls)
	}

	escaping := validIssue()
	escaping["suggested_fix"].(map[string]any)["files"] = []any{
		map[string]any{"path": "/etc/passwd", "role": "no"},
	}
	runner := newRunner(t, ws,
		toolCall("c1", ToolFileIssue, escaping),
		toolCall("c2", ToolFileIssue, validIssue()),
	)
	got, err = Run(context.Background(), newOptions(t, ws, runner))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.RejectedPathCalls != 1 {
		t.Error("a path outside the workspace was accepted")
	}
}

// A run that ends without file_issue produced no diagnosis, and that has to
// be distinguishable from one that did.
func TestARunWithoutFileIssueFails(t *testing.T) {
	ws := newWorkspace(t)
	runner := newRunner(t, ws, faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxText("The bug is probably in the parser.")},
		StopReason: core.StopReasonStop,
	})
	_, err := Run(context.Background(), newOptions(t, ws, runner))
	if err == nil {
		t.Fatal("want an error")
	}
	var f *Failure
	if !asFailure(err, &f) {
		t.Fatalf("want a *Failure, got %T", err)
	}
	if f.Stage != "analyse" || f.Category != agentrun.CategoryNoResult {
		t.Errorf("stage/category = %s/%s", f.Stage, f.Category)
	}
	if !strings.Contains(err.Error(), ToolFileIssue) {
		t.Errorf("the error should name the tool that was never called: %v", err)
	}
}

// The read-only mandate is the resolved tool set. Nothing the model can do
// reaches a write tool, a shell, or the network.
func TestTheTriagePhaseDeclaresOnlyReadToolsAndTheTerminator(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	runner, err := agentrun.NewRunner(agentrun.Config{
		Model:     faux.Model(),
		Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace: ws,
		Bounds:    agentrun.Bounds{MaxTurns: 4, MaxBudgetUSD: 1, MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), newOptions(t, ws, runner)); err != nil {
		t.Fatal(err)
	}

	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("nothing reached the wire")
	}
	declared := map[string]bool{}
	for _, tool := range reqs[0].Tools {
		declared[tool.Name] = true
	}
	for _, banned := range append(append([]string(nil), agentrun.MutatingTools...), "fetch_url") {
		if declared[banned] {
			t.Errorf("%s was offered to the model", banned)
		}
	}
	if !declared[ToolFileIssue] {
		t.Error("file_issue was not declared")
	}
}

// The write to GitHub happens in Go, after the run, and only when the flags
// allow it.
func TestFilingTheIssueIsAGoCallAfterTheRun(t *testing.T) {
	var created struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/widgets/issues" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ghapi.Issue{
			Number: 9, HTMLURL: "https://github.com/acme/widgets/issues/9",
		})
	}))
	defer srv.Close()

	ws := newWorkspace(t)
	o := newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue())))
	o.DryRun = false
	o.Labels = []string{"af:fix"}
	o.GitHub = ghapi.NewWithOptions(ghapi.Options{BaseURL: srv.URL, Token: "t", UserAgent: "test"})

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 {
		t.Errorf("GitHub calls = %d, want exactly one", calls)
	}
	if got.Action != "created" || got.Number != 9 {
		t.Errorf("result = %+v", got)
	}
	if created.Title != "session: token refresh skips the expiry check" {
		t.Errorf("title = %q", created.Title)
	}
	if len(created.Labels) != 1 || created.Labels[0] != "af:fix" {
		t.Errorf("labels = %v", created.Labels)
	}
}

// A run that will write checks for the credential BEFORE the model is called.
// Discovering it after a ten-minute analysis costs the analysis.
func TestAMissingTokenFailsBeforeTheModelIsCalled(t *testing.T) {
	ws := newWorkspace(t)
	p := faux.New(toolCall("c1", ToolFileIssue, validIssue()))
	runner, err := agentrun.NewRunner(agentrun.Config{
		Model:     faux.Model(),
		Providers: core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace: ws,
		Bounds:    agentrun.Bounds{MaxTurns: 4, MaxBudgetUSD: 1, MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	// An empty Token means "consult the environment", so the environment has
	// to be emptied for the client to be genuinely unauthenticated.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	o := newOptions(t, ws, runner)
	o.DryRun = false
	o.GitHub = ghapi.NewWithOptions(ghapi.Options{BaseURL: "http://127.0.0.1:1", UserAgent: "t"})
	if o.GitHub.Authenticated() {
		t.Fatal("the test client should have no credential")
	}

	if _, err := Run(context.Background(), o); err == nil {
		t.Fatal("want an error")
	}
	if p.Calls() != 0 {
		t.Errorf("the model was called %d times before the credential check", p.Calls())
	}
}

func asFailure(err error, target **Failure) bool {
	for e := err; e != nil; {
		if f, ok := e.(*Failure); ok {
			*target = f
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}
