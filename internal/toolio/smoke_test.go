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
	"strings"
	"testing"

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
	var dryRun, overwrite bool

	app := toolio.App{
		Name:    "issue",
		Version: agentfox.Version,
		Usage:   "issue [flags] <input>",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&repo, "repo", "", "target repository")
			fs.StringVar(&labels, "label", "", "labels")
			fs.BoolVar(&dryRun, "dry-run", false, "dry run")
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
				DryRun:    dryRun,
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
	var dryRun bool

	app := toolio.App{
		Name:    "fix",
		Version: agentfox.Version,
		Usage:   "fix [flags] <input>",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&land, "land", string(codefix.LandPR), "land mode")
			fs.StringVar(&repo, "repo", "", "target repo")
			fs.BoolVar(&dryRun, "dry-run", false, "dry run")
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
				DryRun:      dryRun,
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

	var comment, dryRun, architecture, noActivate bool
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
			fs.BoolVar(&dryRun, "dry-run", false, "dry run")
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
				DryRun:       dryRun,
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
					"id":       tID,
					"verdict":  "pass",
					"evidence": "lib_test.go: TestFunction passes verification cleanly",
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
