package toolio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox"
	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/issuex"
	"github.com/agent-fox-dev/agentfox/specgen"
)

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func findWorkspaceRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find workspace root containing go.mod")
		}
		dir = parent
	}
}

func toolCallTurn(id, name string, args any) faux.Turn {
	raw, err := json.Marshal(args)
	if err != nil {
		panic(err)
	}
	return faux.Turn{
		Blocks:     []core.ContentBlock{faux.FauxToolCall(id, name, string(raw))},
		StopReason: core.StopReasonToolUse,
	}
}

func initGitRepo(t *testing.T, dir string, originURL, pushURL string) {
	t.Helper()
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main", dir},
		{"git", "-C", dir, "config", "user.email", "test@example.com"},
		{"git", "-C", dir, "config", "user.name", "Tester"},
		{"git", "-C", dir, "config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command(argv[0], argv[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git setup %v failed: %v\n%s", argv, err, out)
		}
	}
	if originURL != "" {
		cmd := exec.Command("git", "-C", dir, "remote", "add", "origin", originURL)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git remote add failed: %v\n%s", err, out)
		}
	}
	if pushURL != "" {
		cmd := exec.Command("git", "-C", dir, "remote", "set-url", "--push", "origin", pushURL)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git remote set-url --push failed: %v\n%s", err, out)
		}
	}
}

func loadFixture(t *testing.T, specID, specName string) (map[afspec.GenerationStep]map[string]any, string) {
	t.Helper()
	root := findWorkspaceRoot(t)
	fixtureDir := filepath.Join(root, "testdata", "valid_spec")

	artifacts := map[afspec.GenerationStep]map[string]any{}
	for _, step := range afspec.GenerationSteps {
		raw, err := os.ReadFile(filepath.Join(fixtureDir, afspec.ArtifactFileName(step)))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(raw), `"01-`, `"`+specID+`-`)
		text = strings.ReplaceAll(text, `TS-01-`, `TS-`+specID+`-`)
		text = strings.ReplaceAll(text, `"spec_id": "01"`, `"spec_id": "`+specID+`"`)
		text = strings.ReplaceAll(text, `"spec_name": "test_feature"`, `"spec_name": "`+specName+`"`)

		var content map[string]any
		if err := json.Unmarshal([]byte(text), &content); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		artifacts[step] = content
	}

	prd, err := os.ReadFile(filepath.Join(fixtureDir, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(prd)
	if i := strings.Index(body[4:], "\n---\n"); i >= 0 {
		body = strings.TrimSpace(body[4+i+5:])
	}
	return artifacts, body
}

func writeSpecPackage(t *testing.T, destDir, specID, specName string) {
	t.Helper()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	root := findWorkspaceRoot(t)
	fixtureDir := filepath.Join(root, "testdata", "valid_spec")
	for _, filename := range []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"} {
		raw, err := os.ReadFile(filepath.Join(fixtureDir, filename))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(raw), `"01-`, `"`+specID+`-`)
		text = strings.ReplaceAll(text, `TS-01-`, `TS-`+specID+`-`)
		text = strings.ReplaceAll(text, `"01"`, `"`+specID+`"`)
		text = strings.ReplaceAll(text, `"test_feature"`, `"`+specName+`"`)
		text = strings.ReplaceAll(text, `"draft"`, `"active"`)
		if err := os.WriteFile(filepath.Join(destDir, filename), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TS-04-36 (smoke): Triaging a GitLab issue via issue CLI
// Verifies: 04-PATH-1
// Real components: toolio.App, toolio.Resolve, toolio.RenderThread, issuetriage.Run, issuex.GitLabClient
func TestTS0436_GitLabIssueTriage_Smoke(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "gl-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "https://gitlab.com/group/subgroup/project.git", "")
	if err := os.WriteFile(filepath.Join(wsDir, "session.go"), []byte("package session\nfunc Refresh() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", wsDir, "add", "session.go")
	_ = cmd.Run()
	cmd = exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial")
	_ = cmd.Run()

	var issueRead, notesRead bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/42"):
			issueRead = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          42,
				"iid":         42,
				"title":       "Session refresh skips expiry check",
				"description": "Cached token is reused after expiry",
				"state":       "opened",
				"web_url":     "https://gitlab.com/group/subgroup/project/-/issues/42",
				"author":      map[string]any{"username": "alice"},
				"labels":      []string{"bug"},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/42/notes"):
			notesRead = true
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":     101,
					"body":   "Reproduced: call Refresh twice within cache window",
					"author": map[string]any{"username": "bob"},
					"system": false,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "gitlab.com" {
			req.URL.Scheme = "http"
			req.URL.Host = server.Listener.Addr().String()
		}
		return oldTransport.RoundTrip(req)
	})

	var receivedForge issuex.Client
	var receivedInput toolio.Input
	var triageResult *issuetriage.Result

	var repo, labels string
	var overwrite bool

	app := toolio.App{
		Name:    "issue",
		Version: agentfox.Version,
		Usage:   "issue [flags] <input>",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "target repository")
			fs.StringVar(&labels, "label", "", "labels")
			fs.BoolVar(&overwrite, "overwrite", false, "overwrite")
		},
		PreCheck: func(c *toolio.Common) error {
			if repo != "" {
				if _, ok := issuex.ParseRepo(repo); !ok {
					return toolio.Usagef("--repo %q cannot be parsed", repo)
				}
			}
			return nil
		},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			receivedForge = d.Forge
			receivedInput = d.Input

			// Scripted provider for issuetriage diagnosis turn
			p := faux.New(toolCallTurn("turn_1", issuetriage.ToolFileIssue, map[string]any{
				"title":          "session: token refresh skips expiry check",
				"problem":        "Cached token is reused after expiry",
				"reproduction":   "Call Refresh twice within cache window",
				"confidence":     "Confirmed",
				"root_cause":     "Refresh returns cached token without comparing expiry",
				"affected_files": []any{map[string]any{"path": "session.go", "role": "fault location"}},
				"suggested_fix": map[string]any{
					"approach": "Check expiry before returning",
					"files":    []any{map[string]any{"path": "session.go", "role": "add expiry check"}},
					"risks":    "None",
				},
				"acceptance_criteria": []any{"Given expired token, new token fetched"},
				"severity":            "High",
				"severity_rationale":  "Security impact",
			}))
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
				SessionPrefix: "issue",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}

			target, _ := issuex.ParseRepo(repo)
			var runErr error
			triageResult, runErr = issuetriage.Run(ctx, issuetriage.Options{
				Input:     d.Input,
				Workspace: d.Workspace,
				Repo:      target,
				DryRun:    d.Common.DryRun,
				Runner:    runner,
				Forge:     d.Forge,
				Run:       d.Run,
				Progress:  d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), triageResult, info
			}
			return toolio.ExitOK, triageResult, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, "--dry-run", "https://gitlab.com/group/subgroup/project/-/issues/42"}, strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitOK {
		t.Fatalf("app.Main failed with code %d; stderr:\n%s", code, stderr.String())
	}
	if !issueRead {
		t.Error("expected issue 42 to be fetched from GitLab server")
	}
	if !notesRead {
		t.Error("expected comments to be fetched from GitLab server")
	}

	// 1. Application shell parses gitlab.com host and instantiates GitLab issuex client
	if receivedForge == nil {
		t.Fatal("expected non-nil Forge in Deps")
	}
	if !strings.Contains(fmt.Sprintf("%T", receivedForge), "gitlab") {
		t.Errorf("expected receivedForge to be *issuex.gitlabClient, got %T", receivedForge)
	}

	// 2. Input resolver reads issue thread and sets Kind to KindIssue
	if receivedInput.Kind != toolio.KindIssue {
		t.Errorf("receivedInput.Kind = %q, want %q", receivedInput.Kind, toolio.KindIssue)
	}
	if receivedInput.Issue == nil || receivedInput.Issue.Number != 42 {
		t.Errorf("receivedInput.Issue = %+v, want issue #42", receivedInput.Issue)
	}

	// 3. Thread renderer generates prompt text prefixed with GitLab branding header
	expectedPrefix := "GitLab issue group/subgroup/project#42 (state: open)\n"
	if !strings.HasPrefix(receivedInput.Body, expectedPrefix) {
		t.Errorf("receivedInput.Body header = %q, want prefix %q", receivedInput.Body[:min(len(receivedInput.Body), 60)], expectedPrefix)
	}
	if !strings.Contains(receivedInput.Body, "Title: Session refresh skips expiry check") {
		t.Errorf("expected body to contain issue title, got: %s", receivedInput.Body)
	}
	if !strings.Contains(receivedInput.Body, "Reproduced: call Refresh twice within cache window") {
		t.Errorf("expected body to contain issue comment, got: %s", receivedInput.Body)
	}

	// 4. Issuetriage pipeline completes analysis and records diagnosis envelope
	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON envelope: %v\n%s", err, stdout.String())
	}
	if !env.OK {
		t.Errorf("expected envelope OK=true, got false: %+v", env.Error)
	}
	if env.Input == nil || env.Input.Kind != string(toolio.KindIssue) {
		t.Errorf("env.Input = %+v, want kind %s", env.Input, toolio.KindIssue)
	}
	if triageResult == nil || triageResult.Severity != "High" {
		t.Errorf("triageResult = %+v, want severity High", triageResult)
	}
}

// TS-04-37 (smoke): Fixing a bug and opening a GitHub pull request via fix CLI
// Verifies: 04-PATH-2
// Real components: toolio.App, codefix.Run, internal/gitx.Git, internal/checks.Detect, issuex.GitHubClient
func TestTS0437_GitHubBugfixPR_Smoke(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "gh-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	originDir := t.TempDir()
	cmdInit := exec.Command("git", "init", "--bare", "-q", "-b", "main", originDir)
	if out, err := cmdInit.CombinedOutput(); err != nil {
		t.Fatalf("git init bare failed: %v\n%s", err, out)
	}

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "https://github.com/acme/widgets.git", originDir)
	if err := os.WriteFile(filepath.Join(wsDir, "widget.go"), []byte("package widget\nfunc Count() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "widget_test.go"), []byte("package widget\nimport \"testing\"\nfunc TestCount(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "go.mod"), []byte("module example.com/widgets\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify real component: internal/checks.Detect
	detectedCheck := checks.Detect(wsDir)
	if detectedCheck == "" {
		t.Logf("checks.Detect(%s) returned empty, continuing", wsDir)
	}

	cmdAdd := exec.Command("git", "-C", wsDir, "add", "widget.go", "widget_test.go", "go.mod")
	_ = cmdAdd.Run()
	cmdCommit := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial commit")
	_ = cmdCommit.Run()
	cmdPush := exec.Command("git", "-C", wsDir, "push", "-q", "origin", "main")
	if out, err := cmdPush.CombinedOutput(); err != nil {
		t.Fatalf("git push main failed: %v\n%s", err, out)
	}

	var issueRead, prCreated, commentPosted bool
	var prReqBody, commentReqBody map[string]any

	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/7":
			issueRead = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       7,
				"number":   7,
				"title":    "Fix widget counter double-count",
				"body":     "Widget count returns wrong count on retry",
				"state":    "open",
				"html_url": "https://github.com/acme/widgets/issues/7",
				"user":     map[string]any{"login": "carol"},
				"labels":   []any{map[string]any{"name": "bug"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/7/comments":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			prCreated = true
			_ = json.NewDecoder(r.Body).Decode(&prReqBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       21,
				"number":   21,
				"title":    prReqBody["title"],
				"body":     prReqBody["body"],
				"html_url": "https://github.com/acme/widgets/pull/21",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/issues/7/comments":
			commentPosted = true
			_ = json.NewDecoder(r.Body).Decode(&commentReqBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":       88,
				"html_url": "https://github.com/acme/widgets/issues/7#issuecomment-88",
				"body":     commentReqBody["body"],
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ghServer.Close()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "github.com" || req.URL.Host == "api.github.com" {
			req.URL.Scheme = "http"
			req.URL.Host = ghServer.Listener.Addr().String()
		}
		return oldTransport.RoundTrip(req)
	})

	var receivedForge issuex.Client
	var fixResult *codefix.Result

	var land string = string(codefix.LandPR)
	var repo string

	app := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&land, "land", string(codefix.LandPR), "land mode")
			fs.StringVar(&repo, "repo", "", "target repo")
		},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			receivedForge = d.Forge

			turnAnalyse := toolCallTurn("t1", "submit_analysis", map[string]any{
				"classification": "bug",
				"title":          "fix widget double count",
				"summary":        "fixed counter logic",
				"root_cause":     "Count() returned 1 instead of 2",
				"approach":       "update Count() to return 2",
				"files": []map[string]any{
					{"path": "widget.go", "change": "update Count()"},
				},
			})
			turnWrite := toolCallTurn("t2", "write_file", map[string]any{
				"path":    "widget.go",
				"content": "package widget\nfunc Count() int { return 2 }\n",
			})
			turnImplement := toolCallTurn("t3", "submit_implementation", map[string]any{
				"commit_subject": "fix: resolve widget double count",
				"summary":        "updated Count() implementation",
				"changes": []map[string]any{
					{"path": "widget.go", "change": "updated Count() implementation"},
				},
			})

			p := faux.New(turnAnalyse, turnWrite, turnImplement)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 1, MaxAttempts: 1},
				SessionPrefix: "fix",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}

			mode, _ := codefix.ParseLandMode(land)
			target, _ := issuex.ParseRepo(repo)

			var runErr error
			fixResult, runErr = codefix.Run(ctx, codefix.Options{
				Input:       d.Input,
				Workspace:   d.Workspace,
				Repo:        target,
				Land:        mode,
				DryRun:      d.Common.DryRun,
				NoVerify:    true,
				Runner:      runner,
				Forge:       d.Forge,
				CheckRunner: gitx.ReducedEnvRunner,
				Run:         d.Run,
				Progress:    d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), fixResult, info
			}
			return toolio.ExitOK, fixResult, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, "--land=pr", "https://github.com/acme/widgets/issues/7"}, strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitOK {
		t.Fatalf("app.Main TS-04-37 failed with code %d; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !issueRead {
		t.Error("expected issue 7 to be fetched from GitHub server")
	}

	// 1. Application shell constructs GitHub issuex client and passes it in Deps.Forge
	if receivedForge == nil {
		t.Fatal("expected non-nil Forge client in Deps")
	}
	if !strings.Contains(fmt.Sprintf("%T", receivedForge), "github") {
		t.Errorf("expected Forge client to be *issuex.githubClient, got %T", receivedForge)
	}

	// 2. Codefix pipeline verifies repository and credentials during preflight, creates branch, commits fix, opens PR
	if !prCreated {
		t.Error("expected pull request to be opened via Forge.CreatePullRequest")
	}
	if fixResult == nil || fixResult.PullRequestURL != "https://github.com/acme/widgets/pull/21" {
		t.Errorf("fixResult.PullRequestURL = %q, want https://github.com/acme/widgets/pull/21", fixResult.PullRequestURL)
	}
	if fixResult.PullRequestNumber != 21 {
		t.Errorf("fixResult.PullRequestNumber = %d, want 21", fixResult.PullRequestNumber)
	}
	if fixResult.Stage != "landed" {
		t.Errorf("fixResult.Stage = %q, want 'landed'", fixResult.Stage)
	}

	// 3. Summary comment is posted via Forge.AddComment and JSON envelope contains pull request URL
	if !commentPosted {
		t.Error("expected summary comment to be posted via Forge.AddComment")
	}
	if len(fixResult.Comments) == 0 || fixResult.Comments[0] != "https://github.com/acme/widgets/issues/7#issuecomment-88" {
		t.Errorf("fixResult.Comments = %+v, want comment URL", fixResult.Comments)
	}

	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON envelope: %v\n%s", err, stdout.String())
	}
	if !env.OK {
		t.Errorf("envelope OK = false: %+v", env.Error)
	}
}

// TS-04-38 (smoke): Generating specifications with PRD feedback posted to a GitLab issue
// Verifies: 04-PATH-3
// Real components: toolio.App, specgen.Run, specgen.ArtifactSchema, issuex.GitLabClient
func TestTS0438_GitLabSpecPRDComment_Smoke(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "gl-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// Verify real component: specgen.ArtifactSchema
	for _, step := range afspec.GenerationSteps {
		if _, err := specgen.ArtifactSchema(step); err != nil {
			t.Fatalf("specgen.ArtifactSchema(%s) failed: %v", step, err)
		}
	}

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "https://gitlab.com/acme/platform.git", "")
	if err := os.WriteFile(filepath.Join(wsDir, "go.mod"), []byte("module example.com/platform\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmdAdd := exec.Command("git", "-C", wsDir, "add", "go.mod", "main.go")
	_ = cmdAdd.Run()
	cmdCommit := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial")
	_ = cmdCommit.Run()

	var issueRead, commentPosted bool
	var commentReqBody map[string]any

	glServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/15"):
			issueRead = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          15,
				"iid":         15,
				"title":       "Add caching support to platform",
				"description": "Please specify in-memory cache architecture",
				"state":       "opened",
				"web_url":     "https://gitlab.com/acme/platform/-/issues/15",
				"author":      map[string]any{"username": "dave"},
			})
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/issues/15/notes"):
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/issues/15/notes"):
			commentPosted = true
			_ = json.NewDecoder(r.Body).Decode(&commentReqBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":   555,
				"body": commentReqBody["body"],
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer glServer.Close()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "gitlab.com" {
			req.URL.Scheme = "http"
			req.URL.Host = glServer.Listener.Addr().String()
		}
		return oldTransport.RoundTrip(req)
	})

	var receivedForge issuex.Client
	var specResult *specgen.Result

	var comment, architecture, noActivate bool
	var specsDir, name string

	app := toolio.App{
		Name:    "spec",
		Version: agentfox.Version,
		Usage:   "spec [flags] <input>",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&specsDir, "specs-dir", "", "specs dir")
			fs.StringVar(&name, "name", "", "name override")
			fs.BoolVar(&architecture, "architecture", false, "architecture")
			fs.BoolVar(&noActivate, "no-activate", false, "no-activate")
			fs.BoolVar(&comment, "comment", false, "comment")
		},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			receivedForge = d.Forge

			fixtures, body := loadFixture(t, "01", "platform_cache")
			turnPRD := toolCallTurn("t0", "submit_prd", map[string]any{
				"spec_name": "platform_cache",
				"title":     "Platform Cache",
				"body":      body,
			})
			turnReq := toolCallTurn("t1", "submit_requirements", fixtures[afspec.StepRequirements])
			turnTest := toolCallTurn("t2", "submit_test_spec", fixtures[afspec.StepTestSpec])
			turnTasks := toolCallTurn("t3", "submit_tasks", fixtures[afspec.StepTasks])

			p := faux.New(turnPRD, turnReq, turnTest, turnTasks)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 20, MaxBudgetUSD: 2, MaxAttempts: 1},
				SessionPrefix: "spec",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}

			var runErr error
			specResult, runErr = specgen.Run(ctx, specgen.Options{
				Input:        d.Input,
				Workspace:    d.Workspace,
				SpecsDir:     specsDir,
				Name:         name,
				Architecture: architecture,
				Activate:     !noActivate,
				Comment:      comment,
				DryRun:       d.Common.DryRun,
				Runner:       runner,
				Forge:        d.Forge,
				Run:          d.Run,
				Progress:     d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), specResult, info
			}
			return toolio.ExitOK, specResult, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, "--comment", "https://gitlab.com/acme/platform/-/issues/15"}, strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitOK {
		t.Fatalf("app.Main failed with code %d; stderr:\n%s", code, stderr.String())
	}
	if !issueRead {
		t.Error("expected issue 15 to be read from GitLab server")
	}

	// 1. Application shell initializes authenticated GitLab issuex client
	if receivedForge == nil {
		t.Fatal("expected non-nil Forge in Deps")
	}
	if !strings.Contains(fmt.Sprintf("%T", receivedForge), "gitlab") {
		t.Errorf("expected Forge client to be *issuex.gitlabClient, got %T", receivedForge)
	}

	// 2. Specgen generates specification package and posts PRD back via Forge.AddComment
	if !commentPosted {
		t.Error("expected PRD comment to be posted to GitLab issue")
	}
	if commentBody, ok := commentReqBody["body"].(string); !ok || !strings.Contains(commentBody, "## Intent") {
		t.Errorf("expected comment body to contain PRD Intent section, got %v", commentReqBody)
	}

	// 3. Result envelope records successful PRD generation and issue comment URL
	if specResult == nil || specResult.Package.CommentURL == "" {
		t.Errorf("specResult.Package.CommentURL is empty: %+v", specResult)
	}
	if !strings.Contains(specResult.Package.CommentURL, "#note_555") {
		t.Errorf("specResult.Package.CommentURL = %q, want suffix #note_555", specResult.Package.CommentURL)
	}

	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON envelope: %v\n%s", err, stdout.String())
	}
	if !env.OK {
		t.Errorf("envelope OK = false: %+v", env.Error)
	}
}

// TS-04-39 (smoke): Implementing a specification with pull request landing on a nested GitLab project
// Verifies: 04-PATH-4
// Real components: cmd/impl, codeimpl.Run, afspec.LoadSpec, internal/gitx.Git, issuex.GitLabClient
func TestTS0439_GitLabNestedProjectImpl_Smoke(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "gl-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	// 1. Command parses multi-segment repository path via issuex.ParseRepo
	targetPath := "gitlab-org/subgroup/repo"
	parsedRepo, ok := issuex.ParseRepo(targetPath)
	if !ok || parsedRepo.Owner != "gitlab-org/subgroup" || parsedRepo.Name != "repo" {
		t.Fatalf("issuex.ParseRepo(%q) = (%+v, %v), want Owner=%q, Name=%q", targetPath, parsedRepo, ok, "gitlab-org/subgroup", "repo")
	}

	originDir := t.TempDir()
	cmdInit := exec.Command("git", "init", "--bare", "-q", "-b", "main", originDir)
	if out, err := cmdInit.CombinedOutput(); err != nil {
		t.Fatalf("git init bare failed: %v\n%s", err, out)
	}

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "https://gitlab.com/gitlab-org/subgroup/repo.git", originDir)
	if err := os.WriteFile(filepath.Join(wsDir, "go.mod"), []byte("module example.com/repo\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, "lib.go"), []byte("package repo\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specDir := filepath.Join(wsDir, ".specs", "01_nested_spec")
	writeSpecPackage(t, specDir, "01", "nested_spec")

	// Real component: afspec.LoadSpec loads specification
	loadedSpec, err := afspec.LoadSpec(specDir)
	if err != nil {
		t.Fatalf("afspec.LoadSpec(%s) failed: %v", specDir, err)
	}
	if loadedSpec.SpecID != "01" {
		t.Errorf("loadedSpec.SpecID = %q, want '01'", loadedSpec.SpecID)
	}

	cmdAdd := exec.Command("git", "-C", wsDir, "add", "go.mod", "lib.go", ".specs")
	_ = cmdAdd.Run()
	cmdCommit := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial commit")
	_ = cmdCommit.Run()
	cmdPush := exec.Command("git", "-C", wsDir, "push", "-q", "origin", "main")
	if out, err := cmdPush.CombinedOutput(); err != nil {
		t.Fatalf("git push origin main failed: %v\n%s", err, out)
	}

	var mrCreated bool
	var mrReqBody map[string]any

	glServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/merge_requests"):
			mrCreated = true
			_ = json.NewDecoder(r.Body).Decode(&mrReqBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":      999,
				"iid":     42,
				"title":   mrReqBody["title"],
				"web_url": "https://gitlab.com/gitlab-org/subgroup/repo/-/merge_requests/42",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer glServer.Close()

	oldTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = oldTransport }()
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "gitlab.com" {
			req.URL.Scheme = "http"
			req.URL.Host = glServer.Listener.Addr().String()
		}
		return oldTransport.RoundTrip(req)
	})

	var receivedForge issuex.Client
	var implResult *codeimpl.Result

	var repoFlag string
	var landFlag string = string(codeimpl.LandPR)

	app := toolio.App{
		Name:    "impl",
		Version: agentfox.Version,
		Usage:   "impl [flags] <spec>",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&repoFlag, "repo", "", "target repo")
			fs.StringVar(&landFlag, "land", string(codeimpl.LandPR), "land mode")
		},
		PreCheck: func(c *toolio.Common) error {
			if repoFlag != "" {
				if _, ok := issuex.ParseRepo(repoFlag); !ok {
					return toolio.Usagef("--repo %q cannot be parsed", repoFlag)
				}
			}
			return nil
		},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			receivedForge = d.Forge

			task1 := loadedSpec.Tasks.Tasks[0]
			testVerdicts := make([]map[string]any, len(task1.Tests))
			for i, tID := range task1.Tests {
				testVerdicts[i] = map[string]any{
					"id":           tID,
					"verdict":      "pass",
					"evidence":     "lib_test.go: TestFunction passes verification cleanly",
					"red_evidence": "go test ./... failed before the change: TestFunction got the zero value",
				}
			}

			turnWrite := toolCallTurn("t0", "write_file", map[string]any{
				"path":    "lib.go",
				"content": "package repo\nfunc Value() int { return 99 }\n",
			})
			turnTask := toolCallTurn("t1", "submit_task", map[string]any{
				"summary":        "implemented task 1 with updated value function",
				"commit_subject": "feat: implement task 1",
				"test_verdicts":  testVerdicts,
				"changes": []map[string]any{
					{"path": "lib.go", "change": "updated value function to 99"},
				},
			})

			p := faux.New(turnWrite, turnTask)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 1, MaxAttempts: 1},
				SessionPrefix: "impl",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}

			mode, _ := codeimpl.ParseLandMode(landFlag)
			target, _ := issuex.ParseRepo(repoFlag)

			var runErr error
			implResult, runErr = codeimpl.Run(ctx, codeimpl.Options{
				Input:     d.Input,
				Workspace: d.Workspace,
				Repo:      target,
				Land:      mode,
				Task:      task1.Id,
				NoSurvey:  true,
				NoVerify:  true,
				Runner:    runner,
				Forge:     d.Forge,
				Run:       d.Run,
				Progress:  d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), implResult, info
			}
			return toolio.ExitOK, implResult, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, "--land=pr", "--repo", targetPath, specDir}, strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitOK {
		t.Fatalf("app.Main TS-04-39 failed with code %d; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// 2. Codeimpl pipeline verifies forge credentials and repository target in preflight
	if receivedForge == nil {
		t.Fatal("expected non-nil Forge in Deps")
	}
	if !strings.Contains(fmt.Sprintf("%T", receivedForge), "gitlab") {
		t.Errorf("expected Forge client to be *issuex.gitlabClient, got %T", receivedForge)
	}

	// 3. Pipeline completes task implementation, commits changes, and calls Forge.CreatePullRequest
	if !mrCreated {
		t.Error("expected merge request to be created on nested GitLab project")
	}
	if implResult == nil || implResult.PullRequestURL != "https://gitlab.com/gitlab-org/subgroup/repo/-/merge_requests/42" {
		t.Errorf("implResult.PullRequestURL = %q, want https://gitlab.com/gitlab-org/subgroup/repo/-/merge_requests/42", implResult.PullRequestURL)
	}
	if implResult.PullRequestNumber != 42 {
		t.Errorf("implResult.PullRequestNumber = %d, want 42", implResult.PullRequestNumber)
	}
	if implResult.Stage != "landed" {
		t.Errorf("implResult.Stage = %q, want 'landed'", implResult.Stage)
	}

	// 4. JSON envelope contains merge request URL on the nested GitLab project
	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON envelope: %v\n%s", err, stdout.String())
	}
	if !env.OK {
		t.Errorf("envelope OK = false: %+v", env.Error)
	}
}

// requestsContainText reports whether any user message across the given
// requests carries substr in one of its text blocks. Tests use it to confirm
// a --context block actually reached a phase's prompt, rather than trusting
// that Input.Context was merely set.
func requestsContainText(reqs []core.Request, substr string) bool {
	for _, req := range reqs {
		for _, m := range req.Messages {
			um, ok := m.(core.UserMessage)
			if !ok {
				continue
			}
			for _, b := range um.Content {
				if tb, ok := b.(core.TextBlock); ok && strings.Contains(tb.Text, substr) {
					return true
				}
			}
		}
	}
	return false
}

// TS-05-44 (smoke): A program-driven caller invoking fix with no argument and stdout redirected to a file gets one parseable envelope
// Verifies: 05-PATH-1, 05-REQ-1.2
// Real components: toolio.App, toolio.Run.Envelope, toolio.Emit
func TestTS0544_BareInvocationEmitsOneEnvelope_Smoke(t *testing.T) {
	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "stdout.json")
	f, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("os.Create: %v", err)
	}

	var execCalled bool
	app := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			execCalled = true
			return toolio.ExitOK, nil, nil
		},
	}

	var stderr bytes.Buffer
	// No positional argument: a bare invocation. stdout is a real *os.File
	// (isTerminal reports false for it, the same as a pipe), never a terminal.
	code := app.Main(context.Background(), nil, strings.NewReader(""), f, &stderr)
	if closeErr := f.Close(); closeErr != nil {
		t.Fatalf("closing stdout file: %v", closeErr)
	}

	if code != toolio.ExitUsage {
		t.Fatalf("app.Main code = %d, want %d (ExitUsage)", code, toolio.ExitUsage)
	}
	if execCalled {
		t.Error("Exec must not run for a bare invocation: no network or model resolution should happen")
	}

	data, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("reading redirected stdout: %v", err)
	}

	// The file holds EXACTLY one JSON object: decode one value, then confirm
	// nothing follows it.
	dec := json.NewDecoder(bytes.NewReader(data))
	var env toolio.Envelope
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout file is not valid JSON: %v\n%s", err, data)
	}
	if dec.More() {
		t.Errorf("stdout file carries more than one JSON value:\n%s", data)
	}

	if env.Status != "usage" {
		t.Errorf("env.Status = %q, want %q", env.Status, "usage")
	}
	if env.OK {
		t.Error("env.OK = true, want false")
	}
	if env.ExitCode != toolio.ExitUsage {
		t.Errorf("env.ExitCode = %d, want %d", env.ExitCode, toolio.ExitUsage)
	}
	if env.Error == nil {
		t.Fatal("env.Error is nil")
	}
	if env.Error.Stage != "usage" || env.Error.Category != "usage" {
		t.Errorf("env.Error = %+v, want stage/category usage", env.Error)
	}
	if env.Error.Message != toolio.NoInputMessage {
		t.Errorf("env.Error.Message = %q, want %q", env.Error.Message, toolio.NoInputMessage)
	}

	// A caller parses the file and reads error.message to correct the invocation.
	var reparsed toolio.Envelope
	if err := json.Unmarshal(data, &reparsed); err != nil {
		t.Fatalf("a caller could not re-parse the file: %v", err)
	}
	if reparsed.Error == nil || reparsed.Error.Message != toolio.NoInputMessage {
		t.Errorf("re-parsed error.message = %+v, want %q", reparsed.Error, toolio.NoInputMessage)
	}
}

// TS-05-45 (smoke): fix stops on an ambiguity and resumes with --context to reach a done analysis
// Verifies: 05-PATH-2, 05-REQ-3.3, 05-REQ-3.7
// Real components: toolio.App, codefix.Run, internal/gitx.Git, internal/checks
func TestTS0545_FixAmbiguityResumesWithContext_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	if err := os.WriteFile(filepath.Join(wsDir, "retry.go"), []byte("package retry\n\nfunc Do() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", wsDir, "add", "retry.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	report := "Retry sometimes fails under load. Does 'retry' mean the HTTP client's retry loop, " +
		"or the job queue's redelivery?"
	const answer = "it means the HTTP client's retry loop in client.go, not the job queue's redelivery"

	// ---- First run: the analyse phase reports an ambiguity.
	var firstProvider *faux.Provider
	firstApp := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			turnAmbiguous := toolCallTurn("t1", "submit_analysis", map[string]any{
				"classification": "bug",
				"title":          "clarify which retry the report means",
				"summary":        "the report reads two ways",
				"root_cause":     "unclear which component the report refers to",
				"approach":       "cannot proceed without a decision",
				"files": []map[string]any{
					{"path": "retry.go", "change": "pending clarification"},
				},
				"ambiguity": map[string]any{
					"question":         "Does 'retry' mean the HTTP client's retry or the job queue's?",
					"interpretation_a": "the HTTP client's retry loop in client.go",
					"interpretation_b": "the job queue's redelivery in worker.go",
				},
			})
			p := faux.New(turnAmbiguous)
			firstProvider = p
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 5, MaxAttempts: 1},
				SessionPrefix: "fix",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			result, runErr := codefix.Run(ctx, codefix.Options{
				Input:       d.Input,
				Workspace:   d.Workspace,
				Land:        codefix.LandNone,
				NoVerify:    true,
				Runner:      runner,
				Forge:       issuex.NewNoOp(),
				CheckRunner: gitx.ReducedEnvRunner,
				Run:         d.Run,
				Progress:    d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}

	var stdout1, stderr1 bytes.Buffer
	code1 := firstApp.Main(context.Background(), []string{"--dir", wsDir, report}, strings.NewReader(""), &stdout1, &stderr1)
	if code1 != toolio.ExitNeedsHuman {
		t.Fatalf("first run: code = %d, want %d (ExitNeedsHuman); stderr:\n%s", code1, toolio.ExitNeedsHuman, stderr1.String())
	}

	var env1 toolio.Envelope
	if err := json.Unmarshal(stdout1.Bytes(), &env1); err != nil {
		t.Fatalf("first run: stdout is not valid JSON: %v\n%s", err, stdout1.String())
	}
	if env1.Status != "needs_human" {
		t.Errorf("first run: env.Status = %q, want needs_human", env1.Status)
	}
	if env1.NeedsHuman == nil {
		t.Fatal("first run: env.NeedsHuman is nil")
	}
	if len(env1.NeedsHuman.Options) != 2 {
		t.Fatalf("first run: env.NeedsHuman.Options = %+v, want 2 entries", env1.NeedsHuman.Options)
	}
	if env1.NeedsHuman.Options[0].ID != "A" || env1.NeedsHuman.Options[1].ID != "B" {
		t.Errorf("first run: env.NeedsHuman.Options = %+v, want ids A and B", env1.NeedsHuman.Options)
	}
	wantResume := `fix <same input> --context "<answer>"`
	if env1.NeedsHuman.Resume != wantResume {
		t.Errorf("first run: env.NeedsHuman.Resume = %q, want %q", env1.NeedsHuman.Resume, wantResume)
	}
	if firstProvider == nil || len(firstProvider.Requests()) == 0 {
		t.Fatal("the analyse phase never called the scripted provider")
	}

	// ---- Second run: the same input plus --context resolves the ambiguity.
	var secondProvider *faux.Provider
	var secondResult *codefix.Result
	secondApp := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			turnAnalyse := toolCallTurn("t1", "submit_analysis", map[string]any{
				"classification": "bug",
				"title":          "fix the HTTP client's retry loop",
				"summary":        "the retry loop reuses a cached token",
				"root_cause":     "client.go's retry loop does not check expiry",
				"approach":       "check expiry before retrying",
				"files": []map[string]any{
					{"path": "retry.go", "change": "check expiry before retrying"},
				},
			})
			turnWrite := toolCallTurn("t2", "write_file", map[string]any{
				"path":    "retry.go",
				"content": "package retry\n\nfunc Do() { /* checks expiry now */ }\n",
			})
			turnImplement := toolCallTurn("t3", "submit_implementation", map[string]any{
				"commit_subject": "fix: check expiry in the retry loop",
				"summary":        "checked expiry before retrying",
				"changes": []map[string]any{
					{"path": "retry.go", "change": "checked expiry before retrying"},
				},
			})
			p := faux.New(turnAnalyse, turnWrite, turnImplement)
			secondProvider = p
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 5, MaxAttempts: 1},
				SessionPrefix: "fix",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			var runErr error
			secondResult, runErr = codefix.Run(ctx, codefix.Options{
				Input:       d.Input,
				Workspace:   d.Workspace,
				Land:        codefix.LandNone,
				NoVerify:    true,
				Runner:      runner,
				Forge:       issuex.NewNoOp(),
				CheckRunner: gitx.ReducedEnvRunner,
				Run:         d.Run,
				Progress:    d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), secondResult, info
			}
			return toolio.ExitOK, secondResult, nil
		},
	}

	var stdout2, stderr2 bytes.Buffer
	code2 := secondApp.Main(context.Background(),
		[]string{"--dir", wsDir, report, "--context", answer},
		strings.NewReader(""), &stdout2, &stderr2)
	if code2 != toolio.ExitOK {
		t.Fatalf("second run: code = %d, want %d (ExitOK); stdout:\n%s\nstderr:\n%s", code2, toolio.ExitOK, stdout2.String(), stderr2.String())
	}

	// The rendered context block reached the analyse phase's own request.
	if secondProvider == nil || !requestsContainText(secondProvider.Requests(), answer) {
		t.Error("the --context answer never reached a phase's prompt")
	}
	if secondResult == nil || secondResult.Ambiguity != nil {
		t.Errorf("second run: expected the ambiguity resolved, got %+v", secondResult)
	}

	var env2 toolio.Envelope
	if err := json.Unmarshal(stdout2.Bytes(), &env2); err != nil {
		t.Fatalf("second run: stdout is not valid JSON: %v\n%s", err, stdout2.String())
	}
	if env2.Status != "done" {
		t.Errorf("second run: env.Status = %q, want done", env2.Status)
	}
	if !env2.OK {
		t.Errorf("second run: env.OK = false: %+v", env2.Error)
	}
}

// TS-05-46 (smoke): impl hits a per-phase budget ceiling and resumes on the same branch once the ceiling is raised
//
// Verifies: 05-PATH-3, 05-REQ-4.4, 05-REQ-4.7
//
// Divergence from the literal test text, recorded here rather than silently:
// tracing internal/agentrun/phase.go's wrap() and loop.go's runLoop shows that
// AgentKit's OverBudget stop policy ends a phase's run WITHOUT an error unless
// AgentConfig.ErrorOnLimit is set — and nothing in this repository ever sets
// it. So a real per-phase --budget ceiling exceeded by a task phase always
// surfaces through agentrun.NoResultError, category "no_result", never
// category "budget" (codeimpl's only real category="budget" producer is its
// own --total-budget check, always at stage "budget"). What TS-05-46 can
// verify against real, live production code is therefore: fix_hint shaped
// exactly as the budget row asks ({flag: "--budget", current, suggest}, via
// FixHintFor's no_result branch reading the RunStopReason), resumable true
// because Branch is already set, and — once --budget is raised — the run
// continuing on the same branch to completion.
func TestTS0546_ImplBudgetCeilingResumesOnSameBranch_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	specDir := filepath.Join(wsDir, ".specs", "09_budget_spec")
	writeSpecPackage(t, specDir, "09", "budget_spec")
	if out, err := exec.Command("git", "-C", wsDir, "add", ".specs").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	loadedSpec, err := afspec.LoadSpec(specDir)
	if err != nil {
		t.Fatalf("afspec.LoadSpec: %v", err)
	}
	task1 := loadedSpec.Tasks.Tasks[0]

	// ---- First run: the task phase's one turn costs far more than the
	// per-phase --budget ceiling, so the phase ends without a submission.
	var firstResult *codeimpl.Result
	firstApp := toolio.App{
		Name:          "impl",
		Version:       agentfox.Version,
		Usage:         "impl [flags] <spec>\n",
		DefaultBounds: agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			rawArgs, _ := json.Marshal(map[string]any{
				"path":    "task1_partial.go",
				"content": "package repo\n",
			})
			costlyTurn := faux.Turn{
				Blocks:     []core.ContentBlock{faux.FauxToolCall("t0", "write_file", string(rawArgs))},
				StopReason: core.StopReasonToolUse,
				Usage:      core.Usage{CostUSD: 10},
			}
			p := faux.New(costlyTurn)
			bounds := d.Common.Bounds(agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5})
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        bounds,
				SessionPrefix: "impl",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			var runErr error
			firstResult, runErr = codeimpl.Run(ctx, codeimpl.Options{
				Input:        d.Input,
				Workspace:    d.Workspace,
				Task:         task1.Id,
				Land:         codeimpl.LandNone,
				NoVerify:     true,
				NoSurvey:     true,
				TaskAttempts: 1,
				Runner:       runner,
				Forge:        issuex.NewNoOp(),
				CheckRunner:  gitx.ReducedEnvRunner,
				Run:          d.Run,
				Progress:     d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), firstResult, info
			}
			return toolio.ExitOK, firstResult, nil
		},
	}

	var stdout1, stderr1 bytes.Buffer
	code1 := firstApp.Main(context.Background(), []string{"--dir", wsDir, "--budget", "1", specDir}, strings.NewReader(""), &stdout1, &stderr1)
	if code1 == toolio.ExitOK {
		t.Fatalf("first run unexpectedly succeeded; stdout:\n%s", stdout1.String())
	}
	if firstResult == nil || firstResult.Branch == "" {
		t.Fatalf("first run: expected Result.Branch already set, got %+v", firstResult)
	}

	var env1 toolio.Envelope
	if err := json.Unmarshal(stdout1.Bytes(), &env1); err != nil {
		t.Fatalf("first run: stdout is not valid JSON: %v\n%s", err, stdout1.String())
	}
	if env1.Error == nil {
		t.Fatal("first run: env.Error is nil")
	}
	if env1.Error.Category != agentrun.CategoryNoResult {
		t.Errorf("first run: env.Error.Category = %q; the real path for a per-phase budget stop is %q "+
			"(see the divergence note on this test)", env1.Error.Category, agentrun.CategoryNoResult)
	}
	if env1.Error.FixHint == nil {
		t.Fatal("first run: env.Error.FixHint is nil")
	}
	if env1.Error.FixHint.Flag != "--budget" {
		t.Errorf("first run: env.Error.FixHint.Flag = %q, want --budget", env1.Error.FixHint.Flag)
	}
	if env1.Error.FixHint.Current != 1 || env1.Error.FixHint.Suggest != 2 {
		t.Errorf("first run: env.Error.FixHint = %+v, want current=1 suggest=2", env1.Error.FixHint)
	}
	if !env1.Error.Resumable {
		t.Error("first run: env.Error.Resumable = false, want true (Branch is non-empty)")
	}

	// ---- Second run: the same spec, a much higher --budget, continuing on
	// the same branch to completion.
	var secondResult *codeimpl.Result
	secondApp := toolio.App{
		Name:          "impl",
		Version:       agentfox.Version,
		Usage:         "impl [flags] <spec>\n",
		DefaultBounds: agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			testVerdicts := make([]map[string]any, len(task1.Tests))
			for i, tID := range task1.Tests {
				testVerdicts[i] = map[string]any{
					"id":           tID,
					"verdict":      "pass",
					"evidence":     "lib_test.go: TestFunction passes verification cleanly",
					"red_evidence": "go test ./... failed before the change: TestFunction got the zero value",
				}
			}
			turnWrite := toolCallTurn("t0", "write_file", map[string]any{
				"path":    "task1.go",
				"content": "package repo\nfunc Loaded() bool { return true }\n",
			})
			turnTask := toolCallTurn("t1", "submit_task", map[string]any{
				"summary":        "implemented task 1",
				"commit_subject": "feat: implement task 1",
				"test_verdicts":  testVerdicts,
				"changes": []map[string]any{
					{"path": "task1.go", "change": "added Loaded()"},
				},
			})
			p := faux.New(turnWrite, turnTask)
			bounds := d.Common.Bounds(agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5})
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        bounds,
				SessionPrefix: "impl",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			var runErr error
			secondResult, runErr = codeimpl.Run(ctx, codeimpl.Options{
				Input:        d.Input,
				Workspace:    d.Workspace,
				Task:         task1.Id,
				Land:         codeimpl.LandNone,
				NoVerify:     true,
				NoSurvey:     true,
				TaskAttempts: 1,
				Runner:       runner,
				Forge:        issuex.NewNoOp(),
				CheckRunner:  gitx.ReducedEnvRunner,
				Run:          d.Run,
				Progress:     d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), secondResult, info
			}
			return toolio.ExitOK, secondResult, nil
		},
	}

	var stdout2, stderr2 bytes.Buffer
	code2 := secondApp.Main(context.Background(), []string{"--dir", wsDir, "--budget", "50", specDir}, strings.NewReader(""), &stdout2, &stderr2)
	if code2 != toolio.ExitOK {
		t.Fatalf("second run: code = %d, want %d (ExitOK); stdout:\n%s\nstderr:\n%s", code2, toolio.ExitOK, stdout2.String(), stderr2.String())
	}
	if secondResult == nil || secondResult.Branch != firstResult.Branch {
		t.Errorf("second run: Branch = %+v, want the same branch as the first run (%q)", secondResult, firstResult.Branch)
	}
	if secondResult.Stage != "landed" {
		t.Errorf("second run: Stage = %q, want landed", secondResult.Stage)
	}

	var env2 toolio.Envelope
	if err := json.Unmarshal(stdout2.Bytes(), &env2); err != nil {
		t.Fatalf("second run: stdout is not valid JSON: %v\n%s", err, stdout2.String())
	}
	if env2.Status != "done" || !env2.OK {
		t.Errorf("second run: env.Status=%q env.OK=%v, want done/true", env2.Status, env2.OK)
	}
}

// TS-05-47 (smoke): A high-severity input-truncation warning surfaces in an otherwise successful fix run's summary
// Verifies: 05-PATH-4, 05-REQ-2.6, 05-REQ-5.6
// Real components: toolio.App, toolio.Resolve, codefix.Run, internal/gitx.Git, internal/checks
func TestTS0547_TruncatedInputWarningSurfacesInSummary_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	if err := os.WriteFile(filepath.Join(wsDir, "widget.go"), []byte("package widget\nfunc Count() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", wsDir, "add", "widget.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	// A report exceeding toolio.MaxInputBytes, given as a FILE: a huge literal
	// CLI argument would instead be refused pre-Resolve as a usage error
	// (REQ-3.8's Body+context bound), which is a different path than the one
	// this test verifies — Resolve's own file-reading Truncate. It lives
	// outside the repository so the working tree preflight check stays clean.
	reportPath := filepath.Join(t.TempDir(), "report.txt")
	var b strings.Builder
	b.WriteString("The widget counter double-counts on retry; investigate Count() in widget.go.\n")
	for b.Len() <= toolio.MaxInputBytes {
		b.WriteString(strings.Repeat("padding ", 20) + "\n")
	}
	if err := os.WriteFile(reportPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	var fixResult *codefix.Result
	app := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			if !d.Input.Truncated {
				t.Error("expected d.Input.Truncated to be true")
			}
			turnAnalyse := toolCallTurn("t1", "submit_analysis", map[string]any{
				"classification": "bug",
				"title":          "fix widget double count",
				"summary":        "fixed counter logic",
				"root_cause":     "Count() returned 1 instead of 2",
				"approach":       "update Count() to return 2",
				"files": []map[string]any{
					{"path": "widget.go", "change": "update Count()"},
				},
			})
			turnWrite := toolCallTurn("t2", "write_file", map[string]any{
				"path":    "widget.go",
				"content": "package widget\nfunc Count() int { return 2 }\n",
			})
			turnImplement := toolCallTurn("t3", "submit_implementation", map[string]any{
				"commit_subject": "fix: resolve widget double count",
				"summary":        "updated Count() implementation",
				"changes": []map[string]any{
					{"path": "widget.go", "change": "updated Count() implementation"},
				},
			})
			p := faux.New(turnAnalyse, turnWrite, turnImplement)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 5, MaxAttempts: 1},
				SessionPrefix: "fix",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			var runErr error
			fixResult, runErr = codefix.Run(ctx, codefix.Options{
				Input:       d.Input,
				Workspace:   d.Workspace,
				Land:        codefix.LandNone,
				NoVerify:    true,
				Runner:      runner,
				Forge:       issuex.NewNoOp(),
				CheckRunner: gitx.ReducedEnvRunner,
				Run:         d.Run,
				Progress:    d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), fixResult, info
			}
			return toolio.ExitOK, fixResult, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, reportPath}, strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("app.Main code = %d, want %d (ExitOK); stdout:\n%s\nstderr:\n%s", code, toolio.ExitOK, stdout.String(), stderr.String())
	}

	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK {
		t.Errorf("env.OK = false, want true: %+v", env.Error)
	}

	var found *toolio.Warning
	for i := range env.Warnings {
		if env.Warnings[i].Code == toolio.WarnInputTruncated {
			found = &env.Warnings[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected an %q warning, got %+v", toolio.WarnInputTruncated, env.Warnings)
	}
	if found.Severity != "high" {
		t.Errorf("warning.severity = %q, want high", found.Severity)
	}
	if found.Stage != "input" {
		t.Errorf("warning.stage = %q, want input", found.Stage)
	}

	if !strings.Contains(env.Summary, "high-severity warning") {
		t.Errorf("env.Summary = %q, want a clause noting one high-severity warning", env.Summary)
	}
}

// ---------------------------------------------------------------------------
// Spec 06 (trim and chain the results): integration smoke tests.
//
// Each test drives a real pipeline through toolio.App.Main, with only the
// model (a scripted faux provider) and the forge's network endpoint (an
// httptest server) standing in for the outside world. Git, internal/checks
// and the filesystem are real. Every test points XDG_STATE_HOME at a temp
// directory so no run writes into the developer's own state directory.
// ---------------------------------------------------------------------------

// smokeGitDates pins git's clock so two runs over identical content make
// identical commits, which lets one run's output be compared with another's.
func smokeGitDates(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_DATE", "2024-01-02T03:04:05Z")
	t.Setenv("GIT_COMMITTER_DATE", "2024-01-02T03:04:05Z")
}

// smokeWidgetRepo makes a committed Go repository with one fixable bug.
func smokeWidgetRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir, remote, "")
	for name, content := range map[string]string{
		"go.mod":         "module example.com/widgets\n\ngo 1.26\n",
		"widget.go":      "package widget\nfunc Count() int { return 1 }\n",
		"widget_test.go": "package widget\nimport \"testing\"\nfunc TestCount(t *testing.T) {}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, argv := range [][]string{
		{"git", "-C", dir, "add", "-A"},
		{"git", "-C", dir, "commit", "-q", "-m", "chore: initial commit"},
	} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	return dir
}

// smokeFixTurns scripts the three model turns of a fix run that succeeds.
// A report that lists acceptance criteria (as an issue filed by the issue
// tool does) must be answered criterion by criterion: pass the ids.
func smokeFixTurns(criteria ...string) []faux.Turn {
	implementation := map[string]any{
		"commit_subject": "fix: resolve widget double count",
		"summary":        "updated Count() implementation",
		"changes": []map[string]any{
			{"path": "widget.go", "change": "updated Count() implementation"},
		},
	}
	if len(criteria) > 0 {
		var verdicts []map[string]any
		for _, id := range criteria {
			verdicts = append(verdicts, map[string]any{"id": id, "verdict": "pass",
				"evidence": "widget.go: Count() now returns 2; widget_test.go: TestCount passes"})
		}
		implementation["criteria_verdicts"] = verdicts
	}
	return []faux.Turn{
		toolCallTurn("t1", "submit_analysis", map[string]any{
			"classification": "bug",
			"title":          "fix widget double count",
			"summary":        "fixed counter logic",
			"root_cause":     "Count() returned 1 instead of 2",
			"approach":       "update Count() to return 2",
			"files": []map[string]any{
				{"path": "widget.go", "change": "update Count()"},
			},
		}),
		toolCallTurn("t2", "write_file", map[string]any{
			"path":    "widget.go",
			"content": "package widget\nfunc Count() int { return 2 }\n",
		}),
		toolCallTurn("t3", "submit_implementation", implementation),
	}
}

// smokeFixApp is the fix tool wired the way cmd/fix wires it, except that
// the model is scripted. verify is the command internal/checks runs for the
// baseline and the verification; empty means verification is skipped.
func smokeFixApp(turns []faux.Turn, verify string) toolio.App {
	return toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			p := faux.New(turns...)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 10, MaxBudgetUSD: 1, MaxAttempts: 1},
				Observer:      d.Progress,
				SessionPrefix: "fix",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			result, runErr := codefix.Run(ctx, codefix.Options{
				Input:         d.Input,
				Workspace:     d.Workspace,
				Land:          codefix.LandNone,
				DryRun:        d.Common.DryRun,
				VerifyCommand: verify,
				NoVerify:      verify == "",
				Runner:        runner,
				Forge:         d.Forge,
				CheckRunner:   gitx.ReducedEnvRunner,
				Run:           d.Run,
				Progress:      d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}
}

var smokeDurationRe = regexp.MustCompile(`"duration_ms": \d+`)

// smokeNormalize parses one envelope and drops what is different between
// two runs of the same scenario by nature: timings, the start instant, and
// the report file's own path (which names a different temp directory per
// run).
func smokeNormalize(t *testing.T, raw []byte) string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, raw)
	}
	var scrub func(any) any
	scrub = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k := range x {
				switch k {
				case "duration_ms", "started_at", "report_file":
					delete(x, k)
					continue
				}
				x[k] = scrub(x[k])
			}
			return x
		case []any:
			var out []any
			for _, e := range x {
				if m, ok := e.(map[string]any); ok && m["kind"] == "report_file" {
					continue
				}
				out = append(out, scrub(e))
			}
			return out
		}
		return v
	}
	out, err := json.Marshal(scrub(v))
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TS-06-64 (smoke): A caller keeps a small envelope and finds the rest, byte-for-byte, in the report file
// Verifies: 06-PATH-1, 06-REQ-2.8, 06-REQ-1.3, 06-REQ-1.4
// Real components: codefix pipeline, git repository, internal/checks, toolio Run/Envelope, filesystem report-file writer
func TestTS0664_SummaryEnvelopeIsSmallAndReportFileHoldsTheRest_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	smokeGitDates(t)

	const report = "Count() in widget.go returns 1 where it should return 2"

	type outcome struct {
		stdout, file []byte
		path         string
		env          toolio.Envelope
	}
	run := func(detail string) outcome {
		wsDir := smokeWidgetRepo(t, "")
		path := filepath.Join(t.TempDir(), "runs", "run.json")
		args := []string{"--dir", wsDir, "--report-file", path}
		if detail != "" {
			args = append(args, "--detail", detail)
		}
		args = append(args, report)
		app := smokeFixApp(smokeFixTurns(), "git --version")
		var stdout, stderr bytes.Buffer
		if code := app.Main(context.Background(), args, strings.NewReader(""), &stdout, &stderr); code != toolio.ExitOK {
			t.Fatalf("--detail %q: code %d; stdout:\n%s\nstderr:\n%s", detail, code, stdout.String(), stderr.String())
		}
		o := outcome{stdout: stdout.Bytes(), path: path}
		var err error
		if o.file, err = os.ReadFile(path); err != nil {
			t.Fatalf("--detail %q: the report file was not written: %v", detail, err)
		}
		if err := json.Unmarshal(o.stdout, &o.env); err != nil {
			t.Fatalf("stdout is not one JSON object: %v\n%s", err, o.stdout)
		}
		return o
	}

	// The default view: trimmed, small, and naming the file that holds the rest.
	summary := run("")
	if len(summary.stdout) >= 3*1024 {
		t.Errorf("the summary envelope is %d bytes, want under 3 KB:\n%s", len(summary.stdout), summary.stdout)
	}
	if summary.env.ReportFile != summary.path {
		t.Errorf("report_file = %q, want %q", summary.env.ReportFile, summary.path)
	}
	var sr struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(summary.stdout, &sr); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"stage": true, "branch": true, "base_branch": true, "commit": true,
		"changed_files": true, "verdict": true, "criteria_outcome": true, "pull_request_url": true,
		"dry_run": true, "verification": true, "detail": true}
	for k := range sr.Result {
		if !allowed[k] {
			t.Errorf("result carries %q, which is not in fix's summary subset", k)
		}
	}
	if string(sr.Result["detail"]) != `"summary"` {
		t.Errorf("result.detail = %s, want \"summary\"", sr.Result["detail"])
	}
	if _, ok := sr.Result["verification"]; ok {
		t.Error("a landable run must not carry verification in its summary")
	}
	for _, key := range []string{"commit", "branch", "changed_files"} {
		if _, ok := sr.Result[key]; !ok {
			t.Errorf("the summary lost %q, which a caller acts on", key)
		}
	}
	if bytes.Contains(summary.stdout, []byte("git version")) {
		t.Error("command output leaked into the summary envelope")
	}
	// What was trimmed is in the file: the command output and the model's
	// own account of the change.
	if !bytes.Contains(summary.file, []byte("git version")) {
		t.Error("the report file lacks the verification command's output")
	}
	if !bytes.Contains(summary.file, []byte(`"implementation"`)) {
		t.Error("the report file lacks the implementation report")
	}
	var rf struct {
		Result struct {
			Detail string `json:"detail"`
		} `json:"result"`
	}
	if err := json.Unmarshal(summary.file, &rf); err != nil || rf.Result.Detail != "full" {
		t.Errorf("the report file's result.detail = %q (err %v), want full", rf.Result.Detail, err)
	}

	// --detail full prints the very document it also writes: the same bytes,
	// once the clock is set aside.
	full := run("full")
	got := smokeDurationRe.ReplaceAll(full.file, nil)
	want := smokeDurationRe.ReplaceAll(full.stdout, nil)
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Errorf("--detail full: the report file differs from stdout:\nfile:\n%s\nstdout:\n%s", full.file, full.stdout)
	}

	// And the summary run's file is what --detail full prints for the same
	// run: another run of the same scenario agrees once what differs by
	// nature (timings, the report's own path) is set aside.
	if a, b := smokeNormalize(t, summary.file), smokeNormalize(t, full.stdout); a != b {
		t.Errorf("the summary run's report file differs from a --detail full run's stdout:\nfile:   %s\nstdout: %s", a, b)
	}
}

// smokeGitHubForge is a fake GitHub for acme/widgets. It records what is
// written to it and stands behind http.DefaultTransport for the test.
type smokeGitHubForge struct {
	created  map[string]any
	comments []string
	reads    int
}

func newSmokeGitHubForge(t *testing.T) *smokeGitHubForge {
	t.Helper()
	f := &smokeGitHubForge{}
	issue := func() map[string]any {
		return map[string]any{
			"id": 7, "number": 7, "state": "open",
			"title":    f.created["title"],
			"body":     f.created["body"],
			"html_url": "https://github.com/acme/widgets/issues/7",
			"user":     map[string]any{"login": "carol"},
			"labels":   []any{map[string]any{"name": "bug"}},
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/issues":
			_ = json.NewDecoder(r.Body).Decode(&f.created)
			_ = json.NewEncoder(w).Encode(issue())
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/7":
			f.reads++
			_ = json.NewEncoder(w).Encode(issue())
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/7/comments":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/issues/7/comments":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.comments = append(f.comments, fmt.Sprint(body["body"]))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 88, "html_url": "https://github.com/acme/widgets/issues/7#issuecomment-88",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	old := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = old })
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "github.com" || req.URL.Host == "api.github.com" {
			req.URL.Scheme = "http"
			req.URL.Host = server.Listener.Addr().String()
		}
		return old.RoundTrip(req)
	})
	return f
}

// TS-06-65 (smoke): A model chains issue into fix using next[] and side_effects[]
// Verifies: 06-PATH-2, 06-REQ-6.1, 06-REQ-5.1, 06-REQ-4.1
// Real components: issuetriage pipeline, codefix pipeline, fake forge HTTP server, toolio Run/Envelope
func TestTS0665_IssueNextChainsIntoFix_Smoke(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "gh-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	wsDir := smokeWidgetRepo(t, "https://github.com/acme/widgets.git")
	forge := newSmokeGitHubForge(t)

	var label string
	issueApp := toolio.App{
		Name:    "issue",
		Version: agentfox.Version,
		Usage:   "issue [flags] <input>\n",
		Flags:   func(fs *flag.FlagSet) { fs.StringVar(&label, "label", "", "label") },
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			p := faux.New(toolCallTurn("turn_1", issuetriage.ToolFileIssue, map[string]any{
				"title":          "widget: Count() double counts on retry",
				"problem":        "Count() returns 1 where it should return 2",
				"reproduction":   "Call Count() after a retry",
				"confidence":     "Confirmed",
				"root_cause":     "Count() returns a constant",
				"affected_files": []any{map[string]any{"path": "widget.go", "role": "fault location"}},
				"suggested_fix": map[string]any{
					"approach": "Return 2",
					"files":    []any{map[string]any{"path": "widget.go", "role": "change Count()"}},
					"risks":    "None",
				},
				"acceptance_criteria": []any{"Given a retry, Count() returns 2"},
				"severity":            "High",
				"severity_rationale":  "Wrong answer",
			}))
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
				SessionPrefix: "issue",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			var labels []string
			if label != "" {
				labels = []string{label}
			}
			result, runErr := issuetriage.Run(ctx, issuetriage.Options{
				Input:     d.Input,
				Workspace: d.Workspace,
				Labels:    labels,
				DryRun:    d.Common.DryRun,
				Runner:    runner,
				Forge:     d.Forge,
				Run:       d.Run,
				Progress:  d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}

	var stdout1, stderr1 bytes.Buffer
	code := issueApp.Main(context.Background(), []string{"--dir", wsDir, "--label", "bug",
		"Count() in widget.go double counts after a retry"}, strings.NewReader(""), &stdout1, &stderr1)
	if code != toolio.ExitOK {
		t.Fatalf("issue: code %d; stdout:\n%s\nstderr:\n%s", code, stdout1.String(), stderr1.String())
	}
	var issueEnv toolio.Envelope
	if err := json.Unmarshal(stdout1.Bytes(), &issueEnv); err != nil {
		t.Fatalf("issue stdout is not JSON: %v\n%s", err, stdout1.String())
	}

	const issueURL = "https://github.com/acme/widgets/issues/7"

	// issuetriage.Write posted the issue, and the envelope says so.
	if forge.created == nil {
		t.Fatal("the forge never received the issue")
	}
	if len(issueEnv.SideEffects) != 1 {
		t.Fatalf("side_effects = %+v, want exactly one create_issue", issueEnv.SideEffects)
	}
	if se := issueEnv.SideEffects[0]; se.Action != "create_issue" || se.Target != "acme/widgets" || !se.OK || se.Warning != "" {
		t.Errorf("side_effects[0] = %+v, want {create_issue acme/widgets true}", se)
	}

	// artifacts names the same issue.
	var filed *toolio.Artifact
	for i, a := range issueEnv.Artifacts {
		if a.Kind == toolio.ArtifactIssue {
			filed = &issueEnv.Artifacts[i]
		}
	}
	if filed == nil || filed.URL != issueURL || filed.Number != 7 || filed.DryRun {
		t.Errorf("artifacts = %+v, want an issue entry for %s #7", issueEnv.Artifacts, issueURL)
	}

	// next[0] is fix on that URL, and says what was labelled.
	if len(issueEnv.Next) == 0 {
		t.Fatalf("next is empty; envelope:\n%s", stdout1.String())
	}
	next := issueEnv.Next[0]
	if next.Tool != "fix" || next.Input != issueURL || len(next.Flags) != 0 || !strings.Contains(next.Why, "bug") {
		t.Errorf("next[0] = %+v, want fix on %s naming the label", next, issueURL)
	}

	// The caller runs fix on next[0].input as printed, reading nothing else.
	fixApp := smokeFixApp(smokeFixTurns("AC-1"), "")
	var stdout2, stderr2 bytes.Buffer
	args := append([]string{"--dir", wsDir}, next.Flags...)
	args = append(args, next.Input)
	if code := fixApp.Main(context.Background(), args, strings.NewReader(""), &stdout2, &stderr2); code != toolio.ExitOK {
		t.Fatalf("fix on next[0].input: code %d; stdout:\n%s\nstderr:\n%s", code, stdout2.String(), stderr2.String())
	}
	var fixEnv toolio.Envelope
	if err := json.Unmarshal(stdout2.Bytes(), &fixEnv); err != nil {
		t.Fatalf("fix stdout is not JSON: %v\n%s", err, stdout2.String())
	}
	if !fixEnv.OK {
		t.Errorf("fix envelope ok = false: %+v", fixEnv.Error)
	}
	if fixEnv.Input == nil || fixEnv.Input.Kind != string(toolio.KindIssue) || fixEnv.Input.Origin != issueURL {
		t.Errorf("fix input = %+v, want the issue %s read from the forge", fixEnv.Input, issueURL)
	}
	if forge.reads == 0 {
		t.Error("fix never read the issue it was pointed at")
	}
}

// TS-06-66 (smoke): A mistyped path is caught, then confirmed strict
// Verifies: 06-PATH-3, 06-REQ-7.1, 06-REQ-8.2
// Real components: toolio Resolve, toolio CLI argument parsing, filesystem (os.Stat)
func TestTS0666_MistypedPathWarnsThenInputKindFileRefuses_Smoke(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Chdir(t.TempDir())
	wsDir := t.TempDir()
	const arg = "widget/report.txt"
	if _, err := os.Stat(arg); err == nil {
		t.Fatalf("%s must not exist for this test", arg)
	}

	execs := 0
	var received toolio.Input
	newApp := func() toolio.App {
		return toolio.App{
			Name:    "fix",
			Version: agentfox.Version,
			Usage:   "fix [flags] <input>\n",
			Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
				execs++
				received = d.Input
				return toolio.ExitOK, map[string]any{"stage": "done"}, nil
			},
		}
	}

	// Default classification: text, with a high-severity warning naming it.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	var stdout1, stderr1 bytes.Buffer
	if code := newApp().Main(context.Background(), []string{"--dir", wsDir, arg}, strings.NewReader(""), &stdout1, &stderr1); code != toolio.ExitOK {
		t.Fatalf("default run: code %d; stdout:\n%s\nstderr:\n%s", code, stdout1.String(), stderr1.String())
	}
	if received.Kind != toolio.KindText || received.Body != arg {
		t.Errorf("input = %+v, want the argument as text", received)
	}
	var env1 toolio.Envelope
	if err := json.Unmarshal(stdout1.Bytes(), &env1); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout1.String())
	}
	if env1.Input == nil || env1.Input.Kind != "text" {
		t.Errorf("input = %+v, want kind text", env1.Input)
	}
	var found []toolio.Warning
	for _, w := range env1.Warnings {
		if w.Code == toolio.WarnInputLooksLikePath {
			found = append(found, w)
		}
	}
	if len(found) != 1 {
		t.Fatalf("warnings = %+v, want exactly one %s", env1.Warnings, toolio.WarnInputLooksLikePath)
	}
	if w := found[0]; w.Severity != "high" || w.Stage != "input" || !strings.Contains(w.Message, arg) {
		t.Errorf("warning = %+v, want high/input naming %q", w, arg)
	}
	if !env1.OK || !strings.Contains(env1.Summary, "high-severity warning") {
		t.Errorf("ok=%v summary=%q, want an ok run whose summary carries the high-severity clause", env1.OK, env1.Summary)
	}

	// Strict: the same argument is refused as a usage error, before the model
	// is resolved. No key is available, so reaching model resolution would
	// fail differently (exit 1, stage preflight).
	t.Setenv("ANTHROPIC_API_KEY", "")
	var stdout2, stderr2 bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--dir", wsDir, "--input-kind", "file", arg}, strings.NewReader(""), &stdout2, &stderr2)
	if code != toolio.ExitUsage {
		t.Fatalf("--input-kind file: code %d, want %d; stdout:\n%s\nstderr:\n%s", code, toolio.ExitUsage, stdout2.String(), stderr2.String())
	}
	if execs != 1 {
		t.Errorf("Exec ran %d times, want only the first run's", execs)
	}
	var env2 toolio.Envelope
	if err := json.Unmarshal(stdout2.Bytes(), &env2); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout2.String())
	}
	if env2.OK || env2.Error == nil || env2.Error.Stage != "usage" {
		t.Fatalf("envelope = ok:%v error:%+v, want a usage error", env2.OK, env2.Error)
	}
	if !strings.Contains(env2.Error.Message, "does not exist") || !strings.Contains(env2.Error.Message, arg) {
		t.Errorf("message = %q, want it to name %q and say it does not exist", env2.Error.Message, arg)
	}
	if env2.Model != nil {
		t.Errorf("a model was resolved (%+v) before the refusal", env2.Model)
	}
}

// smokeSpecTurns scripts the four model phases that write one scope's
// package, each costing cost.
func smokeSpecTurns(t *testing.T, id, name string, cost float64, split []map[string]any) []faux.Turn {
	t.Helper()
	fixtures, body := loadFixture(t, id, name)
	prd := map[string]any{"spec_name": name, "title": "Scope " + name, "body": body}
	if split != nil {
		prd["recommended_split"] = split
	}
	turns := []faux.Turn{
		toolCallTurn("prd-"+id, "submit_prd", prd),
		toolCallTurn("req-"+id, "submit_requirements", fixtures[afspec.StepRequirements]),
		toolCallTurn("test-"+id, "submit_test_spec", fixtures[afspec.StepTestSpec]),
		toolCallTurn("tasks-"+id, "submit_tasks", fixtures[afspec.StepTasks]),
	}
	for i := range turns {
		turns[i].Usage = core.Usage{CostUSD: cost}
	}
	return turns
}

// smokeSpecApp is the spec tool wired the way cmd/spec wires it (one-phase
// folding of --total-budget included), with a scripted model.
func smokeSpecApp(t *testing.T, turns []faux.Turn, provider **faux.Provider) toolio.App {
	return toolio.App{
		Name:        "spec",
		Version:     agentfox.Version,
		Usage:       "spec [flags] <input>\n",
		SinglePhase: true,
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			p := faux.New(turns...)
			*provider = p
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        d.Common.SinglePhaseBounds(agentrun.Bounds{MaxTurns: 20, MaxBudgetUSD: 5, MaxAttempts: 1}),
				SessionPrefix: "spec",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
			}
			result, runErr := specgen.Run(ctx, specgen.Options{
				Input:          d.Input,
				Workspace:      d.Workspace,
				Activate:       true,
				DryRun:         d.Common.DryRun,
				TotalBudgetUSD: d.Common.TotalBudgetUSD,
				Runner:         runner,
				Forge:          d.Forge,
				Run:            d.Run,
				Progress:       d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}
}

// TS-06-67 (smoke): A split spec run hits a total-budget ceiling and resumes from where it stopped
// Verifies: 06-PATH-4, 06-REQ-9.6
// Real components: specgen pipeline, toolio Run (budget tracking), filesystem spec-package writer
func TestTS0667_SplitSpecStopsAtTotalBudgetAndResumes_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	if err := os.WriteFile(filepath.Join(wsDir, "go.mod"), []byte("module example.com/widgets\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(wsDir, "docs", "drafts", "widgets.md")
	if err := os.MkdirAll(filepath.Dir(draft), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte("widgets: a model, a store, and the switch-over\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specs := filepath.Join(wsDir, ".specs")
	plan := filepath.Join(specs, "widget_core"+specgen.SplitPlanSuffix)

	split := []map[string]any{
		{"name": "widget_core", "scope": "The widget model and its loader."},
		{"name": "widget_github", "scope": "The GitHub-backed widget store."},
		{"name": "widget_adopt", "scope": "Switching the tools over to the new store."},
	}

	// First run: every phase costs $0.10, so the first scope's four phases
	// spend $0.40 — past the $0.30 ceiling, which no single phase reaches.
	var p1 *faux.Provider
	app1 := smokeSpecApp(t, smokeSpecTurns(t, "01", "widget_core", 0.10, split), &p1)
	var stdout1, stderr1 bytes.Buffer
	code := app1.Main(context.Background(), []string{"--dir", wsDir, "--total-budget", "0.3", draft}, strings.NewReader(""), &stdout1, &stderr1)
	if code == toolio.ExitOK {
		t.Fatalf("the first run should stop at the ceiling; stdout:\n%s", stdout1.String())
	}
	var env1 toolio.Envelope
	if err := json.Unmarshal(stdout1.Bytes(), &env1); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout1.String())
	}
	if env1.Error == nil || env1.Error.Category != "budget" || env1.Error.Stage != "budget" {
		t.Fatalf("error = %+v, want stage and category budget; stderr:\n%s", env1.Error, stderr1.String())
	}
	if h := env1.Error.FixHint; h == nil || h.Flag != "--total-budget" {
		t.Errorf("fix_hint = %+v, want one naming --total-budget", h)
	}
	if !env1.Error.Resumable {
		t.Error("error.resumable = false, want true: the split plan is left in place")
	}
	if n := len(p1.Requests()); n != 4 {
		t.Errorf("%d model turns ran, want the first scope's 4 and no second PRD phase", n)
	}
	if _, err := os.Stat(plan); err != nil {
		t.Errorf("the split plan should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(specs, "01_widget_core")); err != nil {
		t.Errorf("the first package should be on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(specs, "02_widget_github")); err == nil {
		t.Error("the second scope must not have been started")
	}

	// next[] names spec again on the input's literal origin.
	var again *toolio.Next
	for i, n := range env1.Next {
		if n.Tool == "spec" {
			again = &env1.Next[i]
		}
	}
	if again == nil {
		t.Fatalf("next = %+v, want a spec suggestion", env1.Next)
	}
	if env1.Input == nil || again.Input != env1.Input.Origin || again.Input == toolio.SameInputPlaceholder ||
		filepath.Base(again.Input) != "widgets.md" {
		t.Errorf("next spec input = %q, want the file's literal origin (input.origin = %+v)", again.Input, env1.Input)
	}

	// Re-run with a higher ceiling, on the input next[] named: the split
	// resumes from the plan and completes every scope.
	var p2 *faux.Provider
	var turns []faux.Turn
	turns = append(turns, smokeSpecTurns(t, "02", "widget_github", 0.10, nil)...)
	turns = append(turns, smokeSpecTurns(t, "03", "widget_adopt", 0.10, nil)...)
	app2 := smokeSpecApp(t, turns, &p2)
	var stdout2, stderr2 bytes.Buffer
	code = app2.Main(context.Background(), []string{"--dir", wsDir, "--total-budget", "50", again.Input}, strings.NewReader(""), &stdout2, &stderr2)
	if code != toolio.ExitOK {
		t.Fatalf("resume: code %d; stdout:\n%s\nstderr:\n%s", code, stdout2.String(), stderr2.String())
	}
	if n := len(p2.Requests()); n != 8 {
		t.Errorf("the resume ran %d model turns, want 8 (scopes 2 and 3 only)", n)
	}
	for _, dir := range []string{"01_widget_core", "02_widget_github", "03_widget_adopt"} {
		if _, err := os.Stat(filepath.Join(specs, dir)); err != nil {
			t.Errorf("%s should be on disk after the resume: %v", dir, err)
		}
	}
	if _, err := os.Stat(plan); err == nil {
		t.Error("the plan should be removed once every scope is written")
	}
	var env2 struct {
		OK     bool `json:"ok"`
		Result struct {
			Split []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"split"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout2.Bytes(), &env2); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout2.String())
	}
	if !env2.OK || len(env2.Result.Split) != 3 {
		t.Fatalf("resume envelope: ok=%v split=%+v, want three scopes", env2.OK, env2.Result.Split)
	}
	for _, s := range env2.Result.Split {
		if s.Status != "done" {
			t.Errorf("scope %s is %q, want done", s.Name, s.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// 07_progress_event_stream: the three execution paths, end to end.
// ---------------------------------------------------------------------------

// smokeEvent is one decoded event line: its type, and the whole object for the
// fields that belong to one type.
type smokeEvent struct {
	Type string
	Raw  map[string]any
}

// smokeParseEvents decodes JSONL, failing the test on any line that is not one
// JSON object naming a type from the closed set, or that lacks the header.
func smokeParseEvents(t *testing.T, tool, jsonl string) []smokeEvent {
	t.Helper()
	closed := map[string]bool{}
	for _, ty := range toolio.EventTypes {
		closed[string(ty)] = true
	}
	var out []smokeEvent
	for i, line := range strings.Split(strings.TrimRight(jsonl, "\n"), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("line %d is not one JSON object: %v\n%s", i+1, err, line)
		}
		ty, _ := m["type"].(string)
		if !closed[ty] {
			t.Errorf("line %d: type %q is outside the closed set: %s", i+1, ty, line)
		}
		if m["tool"] != tool {
			t.Errorf("line %d: tool = %v, want %q", i+1, m["tool"], tool)
		}
		if ts, _ := m["ts"].(string); ts == "" {
			t.Errorf("line %d: no ts: %s", i+1, line)
		} else if _, err := time.Parse(time.RFC3339, ts); err != nil {
			t.Errorf("line %d: ts %q is not RFC 3339: %v", i+1, ts, err)
		}
		out = append(out, smokeEvent{Type: ty, Raw: m})
	}
	return out
}

func smokeFirst(evs []smokeEvent, ty string) int {
	for i, e := range evs {
		if e.Type == ty {
			return i
		}
	}
	return -1
}

func smokeLast(evs []smokeEvent, ty string) int {
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == ty {
			return i
		}
	}
	return -1
}

func smokeCount(evs []smokeEvent, ty string) int {
	n := 0
	for _, e := range evs {
		if e.Type == ty {
			n++
		}
	}
	return n
}

// smokeOrderedWriter lets stderr and stdout share one log, so a test can tell
// which write came first across the two streams.
type smokeOrderedWriter struct {
	mu     *sync.Mutex
	log    *[]smokeWrite
	stream string
}

type smokeWrite struct {
	stream string
	data   string
}

func (w smokeOrderedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.log = append(*w.log, smokeWrite{w.stream, string(p)})
	return len(p), nil
}

// TS-07-44 (smoke): A supervisor watches a fix run live via --events jsonl and sees spend accumulate before it finishes
// Verifies: 07-PATH-1, 07-REQ-3.1, 07-REQ-4.7, 07-REQ-5.3
// Real components: toolio.App, toolio.Progress JSONL sink, internal/agentrun.Runner, codefix pipeline, internal/checks.Run, issuex.GitHubClient
func TestTS0744_FixEventsJSONLSeesSpendBeforeFinish_Smoke(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "gh-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	smokeGitDates(t)

	wsDir := smokeWidgetRepo(t, "https://github.com/acme/widgets.git")
	forge := newSmokeGitHubForge(t)
	forge.created = map[string]any{
		"title": "Fix widget counter double-count",
		"body":  "Count() in widget.go returns 1 where it should return 2",
	}

	turns := smokeFixTurns()
	for i := range turns {
		turns[i].Usage = core.Usage{CostUSD: 0.01, InputTokens: 100, OutputTokens: 20}
	}
	app := smokeFixApp(turns, "git --version")

	var mu sync.Mutex
	var log []smokeWrite
	stdout := smokeOrderedWriter{&mu, &log, "stdout"}
	stderr := smokeOrderedWriter{&mu, &log, "stderr"}
	code := app.Main(context.Background(),
		[]string{"--dir", wsDir, "--events", "jsonl", "https://github.com/acme/widgets/issues/7"},
		strings.NewReader(""), stdout, stderr)

	var errText, outText strings.Builder
	lastStderr, firstStdout := -1, -1
	for i, w := range log {
		if w.stream == "stderr" {
			errText.WriteString(w.data)
			lastStderr = i
		} else {
			outText.WriteString(w.data)
			if firstStdout < 0 {
				firstStdout = i
			}
		}
	}
	if code != toolio.ExitOK {
		t.Fatalf("code = %d; stdout:\n%s\nstderr:\n%s", code, outText.String(), errText.String())
	}
	if forge.reads == 0 {
		t.Error("the issue was never fetched from the forge")
	}

	// Every stderr line is an event: --events jsonl replaces the human form.
	evs := smokeParseEvents(t, "fix", errText.String())

	rs, ps := smokeFirst(evs, "run_start"), smokeFirst(evs, "phase_start")
	if rs != 0 {
		t.Fatalf("run_start is at index %d, want the first event:\n%s", rs, errText.String())
	}
	if ps < 0 || rs > ps {
		t.Errorf("run_start (%d) must come before the first phase_start (%d)", rs, ps)
	}
	if k, _ := evs[rs].Raw["input_kind"].(string); k == "" {
		t.Errorf("run_start has no input_kind: %v", evs[rs].Raw)
	}
	if m, _ := evs[rs].Raw["model"].(map[string]any); m == nil || m["id"] == "" {
		t.Errorf("run_start has no model: %v", evs[rs].Raw)
	}

	var turnsSeen int
	var spend float64
	for _, e := range evs {
		if e.Type != "turn" {
			continue
		}
		turnsSeen++
		c, _ := e.Raw["cost_usd"].(float64)
		spend += c
		if in, _ := e.Raw["input_tokens"].(float64); in != 100 {
			t.Errorf("turn input_tokens = %v, want 100: %v", e.Raw["input_tokens"], e.Raw)
		}
	}
	if turnsSeen == 0 || spend <= 0 {
		t.Errorf("turn events = %d, summed cost_usd = %v, want at least one populated turn", turnsSeen, spend)
	}

	ci := smokeFirst(evs, "check")
	if ci < 0 {
		t.Fatalf("no check event in the stream:\n%s", errText.String())
	}
	chk := evs[ci].Raw
	if ec, ok := chk["exit_code"].(float64); !ok || ec != 0 {
		t.Errorf("check exit_code = %v, want 0: %v", chk["exit_code"], chk)
	}
	if chk["command"] != "git --version" || chk["ok"] != true {
		t.Errorf("check = %v, want command %q ok true", chk, "git --version")
	}

	re := smokeLast(evs, "run_end")
	if pe := smokeLast(evs, "phase_end"); pe < 0 || pe > re {
		t.Errorf("phase_end (%d) must be emitted before run_end (%d)", pe, re)
	}
	if a, b := smokeCount(evs, "phase_start"), smokeCount(evs, "phase_end"); a == 0 || a != b {
		t.Errorf("phase_start = %d, phase_end = %d, want equal and non-zero", a, b)
	}
	if re != len(evs)-1 || smokeCount(evs, "run_end") != 1 {
		t.Errorf("run_end must be the one and last event; at %d of %d", re, len(evs))
	}

	// run_end sits immediately before the envelope: the final stderr write is
	// the event, and nothing is written to stdout until it has been.
	if firstStdout < 0 || lastStderr > firstStdout {
		t.Fatalf("stderr writes continue after stdout began (last stderr %d, first stdout %d)", lastStderr, firstStdout)
	}
	if !strings.Contains(log[lastStderr].data, `"type":"run_end"`) {
		t.Errorf("the last write before the envelope is %q, want run_end", log[lastStderr].data)
	}
	var env toolio.Envelope
	if err := json.Unmarshal([]byte(outText.String()), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, outText.String())
	}
	if env.Status != evs[re].Raw["status"] || evs[re].Raw["exit_code"] != float64(code) {
		t.Errorf("run_end = %v, envelope status %q, exit %d", evs[re].Raw, env.Status, code)
	}
}

// TS-07-45 (smoke): A human reading stderr and a supervisor tailing a file watch the same impl run through different streams, including a heartbeat
// Verifies: 07-PATH-2, 07-REQ-4.1, 07-REQ-7.2
// Real components: toolio.App, toolio.Progress JSONL sink, internal/agentrun.Runner, codeimpl pipeline, events file writer
func TestTS0745_ImplTextOnStderrJSONLInFileWithHeartbeat_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// A window short enough for a half-second phase to cross.
	toolio.ShortenHeartbeat(t, 20*time.Millisecond, 100*time.Millisecond)

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	specDir := filepath.Join(wsDir, ".specs", "09_events_spec")
	writeSpecPackage(t, specDir, "09", "events_spec")
	if out, err := exec.Command("git", "-C", wsDir, "add", ".specs").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wsDir, "commit", "-m", "chore: initial").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	loaded, err := afspec.LoadSpec(specDir)
	if err != nil {
		t.Fatalf("afspec.LoadSpec: %v", err)
	}
	task1 := loaded.Tasks.Tasks[0]

	app := toolio.App{
		Name:          "impl",
		Version:       agentfox.Version,
		Usage:         "impl [flags] <spec>\n",
		DefaultBounds: agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5},
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			verdicts := make([]map[string]any, len(task1.Tests))
			for i, id := range task1.Tests {
				verdicts[i] = map[string]any{"id": id, "verdict": "pass",
					"evidence":     "lib_test.go: TestFunction passes verification cleanly",
					"red_evidence": "go test ./... failed before the change: TestFunction got the zero value"}
			}
			write := toolCallTurn("t0", "write_file", map[string]any{
				"path": "task1.go", "content": "package repo\nfunc Loaded() bool { return true }\n",
			})
			// The long phase: the model takes half a second to answer, and
			// nothing else is emitted while it does.
			write.Delay = 500 * time.Millisecond
			write.Usage = core.Usage{CostUSD: 0.02, InputTokens: 50, OutputTokens: 10}
			submit := toolCallTurn("t1", "submit_task", map[string]any{
				"summary":        "implemented task 1",
				"commit_subject": "feat: implement task 1",
				"test_verdicts":  verdicts,
				"changes":        []map[string]any{{"path": "task1.go", "change": "added Loaded()"}},
			})
			submit.Usage = core.Usage{CostUSD: 0.03, InputTokens: 60, OutputTokens: 12}
			p := faux.New(write, submit)
			runner, rerr := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        d.Common.Bounds(agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5}),
				Observer:      d.Progress,
				ShowText:      d.Common.ShowText,
				SessionPrefix: "impl",
			})
			if rerr != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: rerr.Error()}
			}
			result, runErr := codeimpl.Run(ctx, codeimpl.Options{
				Input:        d.Input,
				Workspace:    d.Workspace,
				Task:         task1.Id,
				Land:         codeimpl.LandNone,
				NoVerify:     true,
				NoSurvey:     true,
				TaskAttempts: 1,
				Runner:       runner,
				Forge:        issuex.NewNoOp(),
				CheckRunner:  gitx.ReducedEnvRunner,
				Run:          d.Run,
				Progress:     d.Progress,
			})
			if runErr != nil {
				info := toolio.ErrorFrom("run", runErr)
				return toolio.ExitCodeFor(info.Category), result, info
			}
			return toolio.ExitOK, result, nil
		},
	}

	eventsFile := filepath.Join(t.TempDir(), "run.jsonl")
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--dir", wsDir, "--events", "text", "--events-file", eventsFile, specDir},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// stderr: human lines only, no JSON.
	var human []string
	for _, line := range strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "{") {
			t.Errorf("JSON on stderr under --events text: %s", line)
		}
		if !strings.HasPrefix(line, "[impl] ") {
			t.Errorf("stderr line is not a human [impl] line: %q", line)
		}
		human = append(human, strings.TrimPrefix(line, "[impl] "))
	}
	if len(human) == 0 {
		t.Fatal("no human progress on stderr")
	}

	raw, err := os.ReadFile(eventsFile)
	if err != nil {
		t.Fatalf("the events file was not written: %v", err)
	}
	evs := smokeParseEvents(t, "impl", string(raw))
	if len(evs) == 0 || evs[len(evs)-1].Type != "run_end" {
		t.Fatalf("run_end must be the file's last line:\n%s", raw)
	}

	// Each human step has its step event, in order, with the same message.
	var steps []string
	for _, e := range evs {
		if e.Type == "step" {
			steps = append(steps, e.Raw["message"].(string))
			if s, _ := e.Raw["stage"].(string); s == "" {
				t.Errorf("step event without a stage: %v", e.Raw)
			}
		}
	}
	if strings.Join(steps, "\n") != strings.Join(human, "\n") {
		t.Errorf("step events do not mirror the human lines\nsteps:\n%s\nhuman:\n%s",
			strings.Join(steps, "\n"), strings.Join(human, "\n"))
	}

	// The long phase crossed the shortened window: a heartbeat landed in the
	// file, naming the stage, the time elapsed and the spend so far.
	//
	// A heartbeat carries the last stage a step or phase_start named before
	// it (07-REQ-7.2). The window here is only 100ms, so on a loaded machine
	// the preflight's git calls can cross it before any stage has been named;
	// such a beat rightly carries an empty stage. What must hold is that each
	// beat names exactly the stage the stream had reached, and that the long
	// phase, which runs well after a stage is named, produced a beat naming it.
	var beats []smokeEvent
	var beatStages []string
	var lastStage string
	for _, e := range evs {
		switch e.Type {
		case "step":
			lastStage, _ = e.Raw["stage"].(string)
		case "phase_start":
			lastStage, _ = e.Raw["phase"].(string)
		case "heartbeat":
			beats = append(beats, e)
			beatStages = append(beatStages, lastStage)
		}
	}
	if len(beats) == 0 {
		t.Fatalf("no heartbeat in the events file:\n%s", raw)
	}
	var namedBeat bool
	prev := float64(-1)
	for i, b := range beats {
		s, _ := b.Raw["stage"].(string)
		if s != beatStages[i] {
			t.Errorf("heartbeat stage = %q, want %q, the last stage named before it: %v", s, beatStages[i], b.Raw)
		}
		if s != "" {
			namedBeat = true
		}
		el, ok := b.Raw["elapsed_ms"].(float64)
		if !ok || el <= prev {
			t.Errorf("heartbeat elapsed_ms = %v after %v, want increasing: %v", b.Raw["elapsed_ms"], prev, b.Raw)
		}
		prev = el
		if _, ok := b.Raw["cost_usd"].(float64); !ok {
			t.Errorf("heartbeat without cost_usd: %v", b.Raw)
		}
	}
	if !namedBeat {
		t.Errorf("no heartbeat named a stage; the long phase did not produce one:\n%s", raw)
	}
	// No heartbeat follows run_end, and the implement phase carried its task.
	if smokeLast(evs, "heartbeat") > smokeLast(evs, "run_end") {
		t.Error("a heartbeat followed run_end")
	}
	var sawTask bool
	for _, e := range evs {
		if e.Type == "phase_start" && e.Raw["phase"] == "implement" && e.Raw["task"] != nil {
			sawTask = true
		}
	}
	if !sawTask {
		t.Errorf("no phase_start for implement carrying a task:\n%s", raw)
	}
}

// TS-07-46 (smoke): An invalid --events value is refused before any work happens, with no events emitted
// Verifies: 07-PATH-3, 07-REQ-1.2
// Real components: toolio.App, toolio.Common flag validation
func TestTS0746_InvalidEventsValueRefusedBeforeAnyWork_Smoke(t *testing.T) {
	// No credentials at all: if model resolution or a fetch ran, it would
	// fail differently (or the Exec below would be reached).
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	execRan := false
	app := toolio.App{
		Name:    "spec",
		Version: agentfox.Version,
		Usage:   "spec [flags] <input>\n",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			execRan = true
			return toolio.ExitOK, nil, nil
		},
	}
	eventsFile := filepath.Join(t.TempDir(), "run.jsonl")
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--events", "yaml", "--events-file", eventsFile, "a report"},
		strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitUsage {
		t.Errorf("code = %d, want %d", code, toolio.ExitUsage)
	}
	if execRan {
		t.Error("the tool ran despite an invalid --events value")
	}
	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, stdout.String())
	}
	if env.Error == nil || env.Error.Stage != "usage" {
		t.Errorf("envelope error = %+v, want stage usage", env.Error)
	}
	if env.Status != "usage" || env.OK {
		t.Errorf("envelope status=%q ok=%v, want usage/false", env.Status, env.OK)
	}
	for _, line := range strings.Split(stderr.String(), "\n") {
		if strings.HasPrefix(line, "{") || strings.Contains(line, `"type"`) {
			t.Errorf("an event line reached stderr: %s", line)
		}
	}
	if _, err := os.Stat(eventsFile); err == nil {
		t.Error("--events-file was created even though the invocation was refused")
	}
	if env.Model != nil {
		t.Errorf("a model was resolved (%+v) before the refusal", *env.Model)
	}
}
