package issuetriage

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
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
		Repo:      issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Runner:    runner,
		Forge:     issuex.NewNoOp(),
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

// The write to the forge happens in Go, after the run, and only when the flags
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 9, "html_url": "https://github.com/acme/widgets/issues/9",
		})
	}))
	defer srv.Close()

	ws := newWorkspace(t)
	o := newOptions(t, ws, newRunner(t, ws, toolCall("c1", ToolFileIssue, validIssue())))
	o.DryRun = false
	o.Labels = []string{"af:fix"}
	var err error
	o.Forge, err = issuex.NewWithOptions(issuex.Options{
		BaseURL:   srv.URL,
		Repo:      issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Token:     "t",
		UserAgent: "test",
	})
	if err != nil {
		t.Fatalf("issuex.NewWithOptions: %v", err)
	}

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 {
		t.Errorf("forge calls = %d, want exactly one", calls)
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

	o := newOptions(t, ws, runner)
	o.DryRun = false
	o.Forge = &mockClient{authenticated: false}
	if o.Forge.Authenticated() {
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

type mockClient struct {
	issuex.NoOpClient
	authenticated bool
	createErr     error
	updateErr     error
	createdIssue  issuex.Issue
	updatedIssue  issuex.Issue
	capturedReq   issuex.CreateIssueRequest
	capturedUpd   issuex.UpdateIssueRequest
}

func (m *mockClient) Authenticated() bool {
	return m.authenticated
}

func (m *mockClient) CreateIssue(ctx context.Context, repo issuex.Repo, req issuex.CreateIssueRequest) (issuex.Issue, error) {
	m.capturedReq = req
	if m.createErr != nil {
		return issuex.Issue{}, m.createErr
	}
	if m.createdIssue.URL != "" || m.createdIssue.Number != 0 {
		return m.createdIssue, nil
	}
	return issuex.Issue{Number: 1, URL: "https://example.com/issue/1", Title: req.Title, Body: req.Body}, nil
}

func (m *mockClient) UpdateIssue(ctx context.Context, ref issuex.IssueRef, req issuex.UpdateIssueRequest) (issuex.Issue, error) {
	m.capturedUpd = req
	if m.updateErr != nil {
		return issuex.Issue{}, m.updateErr
	}
	if m.updatedIssue.URL != "" || m.updatedIssue.Number != 0 {
		return m.updatedIssue, nil
	}
	return issuex.Issue{Number: ref.Number, URL: ref.URL(), Title: req.Title, Body: req.Body}, nil
}

// TS-04-23 (unit): issuetriage declares forge-neutral Options and CategoryForge constant
// Verifies: 04-REQ-6.1
func TestTS0423_ForgeNeutralOptions(t *testing.T) {
	var o Options
	var _ issuex.Repo = o.Repo
	var _ issuex.Client = o.Forge
	if CategoryForge != "forge" {
		t.Errorf("CategoryForge = %q, want %q", CategoryForge, "forge")
	}
	if CategoryGitHub != CategoryForge {
		t.Errorf("CategoryGitHub = %q, want %q", CategoryGitHub, CategoryForge)
	}
}

// TS-04-24 (integration): issuetriage resolveTarget returns usage failure when target repository is unresolved
// Verifies: 04-REQ-6.2
func TestTS0424_ResolveTargetUnresolved(t *testing.T) {
	ws := newWorkspace(t) // has no git remote
	opts := Options{
		Workspace: ws,
		DryRun:    false,
	}
	_, err := ResolveTarget(opts)
	if err == nil {
		t.Fatal("expected preflight usage failure, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want preflight", err.StageName())
	}
	if err.CategoryName() != "usage" {
		t.Errorf("CategoryName = %q, want usage", err.CategoryName())
	}
	if !strings.Contains(err.Error(), "--repo") || !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error %q should mention --repo and --dry-run", err.Error())
	}
	if !strings.Contains(err.Error(), "has no origin remote on a recognized forge") {
		t.Errorf("error %q should mention 'has no origin remote on a recognized forge'", err.Error())
	}
}

// TS-04-25 (unit): issuetriage checkWriteCredential returns internal failure when forge client is nil
// Verifies: 04-REQ-6.3
func TestTS0425_CheckWriteCredentialNilForge(t *testing.T) {
	targetRepo := issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}
	opts := Options{
		Forge:  nil,
		DryRun: false,
	}
	err := CheckWriteCredential(opts, targetRepo)
	if err == nil {
		t.Fatal("expected failure when Forge is nil, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want preflight", err.StageName())
	}
	if err.CategoryName() != "internal" {
		t.Errorf("CategoryName = %q, want internal", err.CategoryName())
	}
	if !strings.Contains(err.Error(), "no forge client configured") {
		t.Errorf("error %q should mention 'no forge client configured'", err.Error())
	}
}

// TS-04-26 (unit): issuetriage checkWriteCredential returns auth failure when forge client is unauthenticated
// Verifies: 04-REQ-6.4
func TestTS0426_CheckWriteCredentialUnauthenticated(t *testing.T) {
	targetRepo := issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}
	opts := Options{
		Forge:  &mockClient{authenticated: false},
		DryRun: false,
	}
	err := CheckWriteCredential(opts, targetRepo)
	if err == nil {
		t.Fatal("expected failure when Forge is unauthenticated, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want preflight", err.StageName())
	}
	if err.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want auth", err.CategoryName())
	}
	if !strings.Contains(err.Error(), "needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run") {
		t.Errorf("error %q should mention 'needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run'", err.Error())
	}
}

// TS-04-27 (integration): issuetriage creates new issue or updates existing issue via Forge client
// Verifies: 04-REQ-6.5, 04-REQ-6.6, 04-REQ-10.4
func TestTS0427_CreateAndModifyIssue(t *testing.T) {
	var created struct {
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		Labels []string `json:"labels"`
	}
	var updated struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/issues" {
			_ = json.NewDecoder(r.Body).Decode(&created)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   101,
				"html_url": "https://github.com/acme/widgets/issues/101",
				"title":    created.Title,
				"body":     created.Body,
			})
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/widgets/issues/42" {
			_ = json.NewDecoder(r.Body).Decode(&updated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   42,
				"html_url": "https://github.com/acme/widgets/issues/42",
				"title":    updated.Title,
				"body":     updated.Body,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx := context.Background()
	client, err := issuex.NewWithOptions(issuex.Options{
		BaseURL:   server.URL,
		Repo:      issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Token:     "tok",
		UserAgent: "test",
	})
	if err != nil {
		t.Fatalf("issuex.NewWithOptions: %v", err)
	}

	target := issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}

	// Overwrite == false -> CreateIssue
	optsCreate := Options{
		Forge:     client,
		Overwrite: false,
		Labels:    []string{"bug"},
	}
	outCreate := &Result{Title: "New issue", Body: "details"}
	errCreate := Write(ctx, optsCreate, target, outCreate)
	if errCreate != nil {
		t.Fatalf("Write (create) failed: %v", errCreate)
	}
	if outCreate.Action != "created" || outCreate.URL != "https://github.com/acme/widgets/issues/101" || outCreate.Number != 101 {
		t.Errorf("outCreate = %+v, want action created, URL https://github.com/acme/widgets/issues/101, number 101", outCreate)
	}
	if created.Title != "New issue" || created.Body != "details" || len(created.Labels) != 1 || created.Labels[0] != "bug" {
		t.Errorf("created payload = %+v", created)
	}

	// Overwrite == true -> UpdateIssue
	issueRef := issuex.IssueRef{Repo: target, Number: 42}
	optsUpdate := Options{
		Forge:     client,
		Overwrite: true,
		Input:     toolio.Input{Issue: &issueRef},
	}
	outUpdate := &Result{Title: "Updated title", Body: "updated details"}
	errUpdate := Write(ctx, optsUpdate, target, outUpdate)
	if errUpdate != nil {
		t.Fatalf("Write (update) failed: %v", errUpdate)
	}
	if outUpdate.Action != "updated" || outUpdate.URL != "https://github.com/acme/widgets/issues/42" || outUpdate.Number != 42 {
		t.Errorf("outUpdate = %+v, want action updated, URL https://github.com/acme/widgets/issues/42, number 42", outUpdate)
	}
	if updated.Title != "Updated title" || updated.Body != "updated details" {
		t.Errorf("updated payload = %+v", updated)
	}
}

// TS-04-28 (unit): issuetriage write classifies forge errors via issuex.IsNoToken
// Verifies: 04-REQ-6.7
func TestTS0428_WriteErrorClassification(t *testing.T) {
	ctx := context.Background()
	target := issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}

	// Test missing token error classification
	optsNoToken := Options{
		Forge: &mockClient{createErr: issuex.ErrNoToken},
	}
	outNoToken := &Result{Title: "Title", Body: "Body"}
	errNoToken := Write(ctx, optsNoToken, target, outNoToken)
	if errNoToken == nil {
		t.Fatal("expected write auth error, got nil")
	}
	if errNoToken.StageName() != "write" {
		t.Errorf("StageName = %q, want write", errNoToken.StageName())
	}
	if errNoToken.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want auth", errNoToken.CategoryName())
	}

	// Test general forge error classification
	optsGeneral := Options{
		Forge: &mockClient{createErr: errors.New("500 internal server error")},
	}
	outGeneral := &Result{Title: "Title", Body: "Body"}
	errGeneral := Write(ctx, optsGeneral, target, outGeneral)
	if errGeneral == nil {
		t.Fatal("expected write forge error, got nil")
	}
	if errGeneral.StageName() != "write" {
		t.Errorf("StageName = %q, want write", errGeneral.StageName())
	}
	if errGeneral.CategoryName() != "forge" {
		t.Errorf("CategoryName = %q, want forge", errGeneral.CategoryName())
	}
}
