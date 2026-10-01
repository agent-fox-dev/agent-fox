package toolio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox"
	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// ---------------------------------------------------------------------------
// 08_envelope_output_file: the four execution paths, end to end. Every test
// drives App.Main with a real git repository, real pipelines and a scripted
// model, and isolates XDG_STATE_HOME so no run writes into the developer's
// own state directory.
// ---------------------------------------------------------------------------

// smokeLostStdout is a supervisor's stdout that is already gone: it records
// what the tool tried to write, snapshots the --output file at the instant of
// the first write (which is what "written before stdout" means), and then
// fails every write with a broken pipe.
type smokeLostStdout struct {
	path string

	mu       sync.Mutex
	wrote    bytes.Buffer
	snapshot []byte
	snapErr  error
	taken    bool
}

func (w *smokeLostStdout) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.taken {
		w.taken = true
		w.snapshot, w.snapErr = os.ReadFile(w.path)
	}
	w.wrote.Write(p)
	return 0, syscall.EPIPE
}

// smokePollOutput reads path in a tight loop until stop is closed, and
// returns what it saw: how many complete reads, and every read that was not
// a whole JSON document. A reader polling an atomically written file sees
// nothing yet, or the complete document.
func smokePollOutput(path string, stop <-chan struct{}) (reads *int, partial *[]string, done <-chan struct{}) {
	n := 0
	var bad []string
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		for {
			select {
			case <-stop:
				return
			default:
			}
			b, err := os.ReadFile(path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				bad = append(bad, err.Error())
				continue
			}
			if !json.Valid(b) || !bytes.HasSuffix(b, []byte("}\n")) {
				bad = append(bad, string(b))
				continue
			}
			n++
		}
	}()
	return &n, &bad, fin
}

// TS-08-26 (smoke): A supervisor that has already lost its own stdout still
// recovers the full result from --output
// Verifies: 08-PATH-1, 08-REQ-2.2, 08-REQ-2.4
// Real components: toolio.App, toolio.Emit, codefix.Run, internal/gitx.Git, issuex.GitHubClient
func TestTS08_26_LostStdoutStillRecoversEnvelopeFromOutput_Smoke(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "gh-smoke-token")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	smokeGitDates(t)

	originDir := t.TempDir()
	if out, err := exec.Command("git", "init", "--bare", "-q", "-b", "main", originDir).CombinedOutput(); err != nil {
		t.Fatalf("git init bare: %v\n%s", err, out)
	}
	wsDir := smokeWidgetRepo(t, "https://github.com/acme/widgets.git")
	if out, err := exec.Command("git", "-C", wsDir, "remote", "set-url", "--push", "origin", originDir).CombinedOutput(); err != nil {
		t.Fatalf("git remote set-url --push: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wsDir, "push", "-q", "origin", "main").CombinedOutput(); err != nil {
		t.Fatalf("git push main: %v\n%s", err, out)
	}

	var mu sync.Mutex
	prCreated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/7":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 7, "number": 7, "title": "Fix widget counter double-count",
				"body": "Widget count returns wrong count on retry", "state": "open",
				"html_url": "https://github.com/acme/widgets/issues/7",
				"user":     map[string]any{"login": "carol"},
				"labels":   []any{map[string]any{"name": "bug"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widgets/issues/7/comments":
			_ = json.NewEncoder(w).Encode([]any{})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/pulls":
			prCreated = true
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 21, "number": 21, "title": body["title"], "body": body["body"],
				"html_url": "https://github.com/acme/widgets/pull/21",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/widgets/issues/7/comments":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 88, "html_url": "https://github.com/acme/widgets/issues/7#issuecomment-88",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	http.DefaultTransport = testRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "github.com" || req.URL.Host == "api.github.com" {
			req.URL.Scheme = "http"
			req.URL.Host = server.Listener.Addr().String()
		}
		return old.RoundTrip(req)
	})

	// smokeFixApp lands nothing, so this run swaps in an Exec that lands a
	// pull request, wired the way cmd/fix wires it.
	app := smokeFixApp(nil, "")
	app.Exec = func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		p := faux.New(smokeFixTurns()...)
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
			Input:       d.Input,
			Workspace:   d.Workspace,
			Land:        codefix.LandPR,
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
			return toolio.ExitCodeFor(info.Category), result, info
		}
		return toolio.ExitOK, result, nil
	}

	outPath := filepath.Join(t.TempDir(), "durable", "fix-result.json")
	stdout := &smokeLostStdout{path: outPath}
	var stderr bytes.Buffer

	stop := make(chan struct{})
	reads, partial, fin := smokePollOutput(outPath, stop)
	code := app.Main(context.Background(),
		[]string{"--dir", wsDir, "--output", outPath, "https://github.com/acme/widgets/issues/7"},
		strings.NewReader(""), stdout, &stderr)
	close(stop)
	<-fin

	if code != toolio.ExitOK {
		t.Fatalf("code = %d, want 0; stdout:\n%s\nstderr:\n%s", code, stdout.wrote.String(), stderr.String())
	}
	if !prCreated {
		t.Error("the pull request was never opened on the forge")
	}
	if !stdout.taken {
		t.Fatal("nothing was written to stdout")
	}
	// Written before stdout: the file was already whole at the first stdout write.
	if stdout.snapErr != nil {
		t.Fatalf("--output did not exist when stdout was first written: %v", stdout.snapErr)
	}
	if !bytes.Equal(stdout.snapshot, stdout.wrote.Bytes()) {
		t.Errorf("the file at the first stdout write differs from the stdout envelope:\nfile:\n%s\nstdout:\n%s",
			stdout.snapshot, stdout.wrote.Bytes())
	}

	// Read after the process exits: one complete envelope, the same one.
	file, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("--output was not written: %v", err)
	}
	if !bytes.Equal(file, stdout.wrote.Bytes()) {
		t.Errorf("the file differs from what an intact stdout would have received:\nfile:\n%s\nstdout:\n%s", file, stdout.wrote.Bytes())
	}
	var env struct {
		toolio.Envelope
		Result struct {
			PullRequestURL string `json:"pull_request_url"`
			Stage          string `json:"stage"`
		} `json:"result"`
	}
	if err := json.Unmarshal(file, &env); err != nil {
		t.Fatalf("--output is not one JSON envelope: %v\n%s", err, file)
	}
	if !env.OK || env.Tool != "fix" || env.ExitCode != toolio.ExitOK {
		t.Errorf("envelope = ok:%v tool:%q exit:%d, want a successful fix", env.OK, env.Tool, env.ExitCode)
	}
	if env.Result.PullRequestURL != "https://github.com/acme/widgets/pull/21" {
		t.Errorf("result.pull_request_url = %q, want the opened pull request", env.Result.PullRequestURL)
	}
	for _, w := range env.Warnings {
		if w.Code == toolio.WarnOutputNotWritten || w.Code == toolio.WarnOutputMatchesReportFile {
			t.Errorf("unexpected warning %+v", w)
		}
	}

	// No partially written document was ever observed, and no temp file remains.
	if len(*partial) > 0 {
		t.Errorf("a reader polling --output saw %d partial documents; first:\n%s", len(*partial), (*partial)[0])
	}
	t.Logf("poller saw %d complete reads", *reads)
	entries, err := os.ReadDir(filepath.Dir(outPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the output directory holds %d entries, want only the envelope", len(entries))
	}
}

// TS-08-27 (smoke): A failed --output write is downgraded to a warning and
// never changes impl's exit code
// Verifies: 08-PATH-2, 08-REQ-3.1
// Real components: toolio.App, codeimpl.Run, internal/gitx.Git, issuex.Client
func TestTS08_27_FailedOutputWriteIsAWarningNotAFailure_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	specDir := filepath.Join(wsDir, ".specs", "09_output_spec")
	writeSpecPackage(t, specDir, "09", "output_spec")
	for _, argv := range [][]string{
		{"git", "-C", wsDir, "add", ".specs"},
		{"git", "-C", wsDir, "commit", "-q", "-m", "chore: initial"},
	} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
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
				verdicts[i] = map[string]any{
					"id": id, "verdict": "pass",
					"evidence":     "lib_test.go: TestFunction passes verification cleanly",
					"red_evidence": "go test ./... failed before the change: TestFunction got the zero value",
				}
			}
			p := faux.New(
				toolCallTurn("t0", "write_file", map[string]any{
					"path": "task1.go", "content": "package repo\nfunc Loaded() bool { return true }\n",
				}),
				toolCallTurn("t1", "submit_task", map[string]any{
					"summary":        "implemented task 1",
					"commit_subject": "feat: implement task 1",
					"test_verdicts":  verdicts,
					"changes":        []map[string]any{{"path": "task1.go", "change": "added Loaded()"}},
				}),
			)
			runner, err := agentrun.NewRunner(agentrun.Config{
				Model:         faux.Model(),
				Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
				Workspace:     d.Workspace,
				Bounds:        d.Common.Bounds(agentrun.Bounds{MaxTurns: 50, MaxBudgetUSD: 5}),
				SessionPrefix: "impl",
			})
			if err != nil {
				return toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "runner", Message: err.Error()}
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
				Forge:        d.Forge,
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

	// The destination sits under a regular file, so its parent directory
	// cannot be created (this holds as root too).
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(blocker, "sub", "out.json")

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, "--output", outPath, specDir},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d, want 0 despite the unwritable --output; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.ExitCode != toolio.ExitOK || env.Status != "done" {
		t.Errorf("ok=%v exit_code=%d status=%q, want true/0/done as without --output", env.OK, env.ExitCode, env.Status)
	}
	var found *toolio.Warning
	for i, w := range env.Warnings {
		if w.Code == toolio.WarnOutputNotWritten {
			found = &env.Warnings[i]
		}
	}
	if found == nil {
		t.Fatalf("no output_not_written warning; warnings = %+v", env.Warnings)
	}
	if found.Severity != "low" || found.Stage != "emit" {
		t.Errorf("warning = %+v, want low severity at stage emit", *found)
	}
	if !strings.Contains(found.Message, outPath) {
		t.Errorf("the warning does not name the path %s: %q", outPath, found.Message)
	}
	if !strings.Contains(found.Message, "not a directory") {
		t.Errorf("the warning does not name the underlying cause: %q", found.Message)
	}
	if _, err := os.Stat(outPath); err == nil {
		t.Errorf("a file was left at the unwritable --output path %s", outPath)
	}
	if _, err := os.Stat(filepath.Join(blocker, "sub")); err == nil {
		t.Error("the unwritable destination's parent was created")
	}
}

// TS-08-28 (smoke): spec's --output and --report-file, pointed at the same
// path, land the complete report rather than the --detail view
// Verifies: 08-PATH-3, 08-REQ-4.3, 08-REQ-4.4
// Real components: toolio.App, specgen.Run, issuex.GitLabClient
func TestTS08_28_OutputAndReportFileCollideCompleteReportLands_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	wsDir := t.TempDir()
	initGitRepo(t, wsDir, "", "")
	if err := os.WriteFile(filepath.Join(wsDir, "go.mod"), []byte("module example.com/widgets\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(wsDir, "widgets.md")
	if err := os.WriteFile(draft, []byte("widgets: a model and a store\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var p *faux.Provider
	app := smokeSpecApp(t, smokeSpecTurns(t, "01", "widget_core", 0, nil), &p)

	// Both flags name ./run.json, spelled two ways, from the working directory.
	// (The fixtures are loaded above, relative to the repository's cwd.)
	cwd := t.TempDir()
	t.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(),
		[]string{"--dir", wsDir, "--detail", "summary", "--output", "./run.json", "--report-file", "run.json", draft},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d; stdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	var env struct {
		toolio.Envelope
		Result struct {
			Detail string `json:"detail"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON envelope: %v\n%s", err, stdout.String())
	}
	if env.Result.Detail != "summary" {
		t.Errorf("stdout result.detail = %q, want the summary view", env.Result.Detail)
	}
	var collided, notWritten bool
	for _, w := range env.Warnings {
		switch w.Code {
		case toolio.WarnOutputMatchesReportFile:
			collided = true
			if w.Severity != "low" || w.Stage != "emit" {
				t.Errorf("warning = %+v, want low severity at stage emit", w)
			}
		case toolio.WarnReportFileNotWritten, toolio.WarnOutputNotWritten:
			notWritten = true
		}
	}
	if !collided {
		t.Errorf("stdout carries no output_matches_report_file warning: %+v", env.Warnings)
	}
	if notWritten {
		t.Errorf("a write failed: %+v", env.Warnings)
	}

	file, err := os.ReadFile(filepath.Join(cwd, "run.json"))
	if err != nil {
		t.Fatalf("nothing landed at the shared path: %v", err)
	}
	var rep struct {
		toolio.Envelope
		Result struct {
			Detail string `json:"detail"`
		} `json:"result"`
	}
	if err := json.Unmarshal(file, &rep); err != nil {
		t.Fatalf("the shared file is not JSON: %v\n%s", err, file)
	}
	if rep.Result.Detail != "full" {
		t.Errorf("the shared file's result.detail = %q, want the complete report (full), not the --detail view", rep.Result.Detail)
	}
	if len(file) <= len(stdout.Bytes()) {
		t.Errorf("the shared file (%d bytes) is not larger than the summary on stdout (%d bytes)", len(file), stdout.Len())
	}
	if rep.ReportFile != "run.json" {
		t.Errorf("the report names %q, want the --report-file value as given", rep.ReportFile)
	}
	// --output made no write of its own: nothing but the report is in the directory.
	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the working directory holds %v, want only run.json", names)
	}
}

// TS-08-29 (smoke): Emit's internal marshal-failure fallback still leaves a
// durable, minimal envelope at --output
// Verifies: 08-PATH-4, 08-REQ-2.3
// Real components: toolio.App, toolio.Emit, codefix.Run
func TestTS08_29_MarshalFailureFallbackLeavesMinimalEnvelopeAtOutput_Smoke(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	smokeGitDates(t)

	wsDir := smokeWidgetRepo(t, "")
	app := smokeFixApp(smokeFixTurns(), "")
	inner := app.Exec
	// fix runs for real; the value it hands back to toolio carries a field
	// that cannot be encoded as JSON.
	app.Exec = func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
		code, result, failure := inner(ctx, d)
		return code, map[string]any{"fix": result, "unencodable": make(chan int)}, failure
	}

	outPath := filepath.Join(t.TempDir(), "fresh", "out.json")
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", wsDir, "--output", outPath, "fix the widget count"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitFailed {
		t.Fatalf("code = %d, want %d; stdout:\n%s\nstderr:\n%s", code, toolio.ExitFailed, stdout.String(), stderr.String())
	}
	var fb toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &fb); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if fb.OK || fb.ExitCode != toolio.ExitFailed || fb.Error == nil || fb.Error.Stage != "emit" ||
		!strings.Contains(fb.Error.Message, "could not be encoded") {
		t.Errorf("stdout is not the minimal fallback envelope: %+v", fb)
	}
	if fb.Result != nil {
		t.Errorf("the fallback carries a result: %v", fb.Result)
	}

	file, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("the fallback left nothing at --output: %v", err)
	}
	if !bytes.Equal(file, stdout.Bytes()) {
		t.Errorf("--output is not byte-for-byte the fallback on stdout:\nfile:\n%s\nstdout:\n%s", file, stdout.String())
	}
	for _, s := range []string{`"commit"`, `"branch"`, `"unencodable"`} {
		if bytes.Contains(file, []byte(s)) {
			t.Errorf("the full result leaked into the fallback: found %s", s)
		}
	}
}
