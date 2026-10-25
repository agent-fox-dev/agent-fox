package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The test binary is also the af a box runs: linked as af it is the real af, with the identity of
// the af of the worker under test, which the worker otherwise refuses.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(afcmd.New(&afcmd.Config{Version: "v-test", Commit: "c-test"}).Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

const (
	pat           = "pat-of-the-acme-subscription"
	slug          = "acme"
	forgeRepo     = "https://github.example.com/Org/Repo"
	issueURL      = forgeRepo + "/issues/42"
	jobID         = "job_0123456789abcdef0123"
	recordPath    = "/api/v1/workspaces/acme"
	varsPath      = "/api/v1/workspaces/acme/vars/resolved"
	issuePath     = "/api/v1/workspaces/acme/issues/42"
	sandboxesPath = "/api/v1/workspaces/acme/sandboxes"
	sessionsPath  = "/api/v1/sessions"
	pullsPath     = "/api/v1/workspaces/acme/pulls"
	gitPath       = "/git/acme.git"
)

// repoFiles are the files of the repository of a workspace whose loop is a stand-in.
var repoFiles = map[string]string{"README.md": "hello\n", "src/greet.sh": "echo helo\n"}

// subs is the store of the subscriptions: a token, or an error, by slug.
type subs struct {
	mu       sync.Mutex
	tokens   map[string]string
	tokenErr map[string]error
	reads    int
}

func (s *subs) Token(_ context.Context, slug string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if err := s.tokenErr[slug]; err != nil {
		return "", err
	}
	if tok, ok := s.tokens[slug]; ok {
		return tok, nil
	}
	return "", store.ErrNotSubscribed
}

// sink is a reporter that keeps what it is told; hook, when set, sees every event after it was kept.
type sink struct {
	mu     sync.Mutex
	events []sunk
	hubs   [][3]string
	hook   func(typ string, data json.RawMessage)
}

type sunk struct {
	Type string
	Data json.RawMessage
}

func (s *sink) Emit(typ string, data any) {
	raw, _ := json.Marshal(data)
	s.mu.Lock()
	s.events = append(s.events, sunk{typ, raw})
	hook := s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(typ, raw)
	}
}

func (s *sink) Hub(sessionID, runID, sandboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hubs = append(s.hubs, [3]string{sessionID, runID, sandboxID})
}

func (s *sink) emitted() []sunk { s.mu.Lock(); defer s.mu.Unlock(); return slices.Clone(s.events) }

func (s *sink) lastHub() (last [3]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.hubs) > 0 {
		last = s.hubs[len(s.hubs)-1]
	}
	return last
}

// env is a fake hub with the workspace acme, its issue 42 and long leases, and a worker over it.
type env struct {
	t      *testing.T
	hub    *fakehub.Hub
	subs   *subs
	sink   *sink
	log    *testutil.Log
	runner *Runner
	dir    string
}

// newEnv makes the environment of the workspace w, on Org/Repo with repoFiles unless w says otherwise.
func newEnv(t *testing.T, w fakehub.Workspace) *env {
	t.Helper()
	testutil.Require(t, "git")
	e := &env{t: t, hub: fakehub.New(t), sink: &sink{}, log: &testutil.Log{}, dir: t.TempDir()}
	w.Slug, w.PAT = slug, pat
	if w.ForgeURL == "" {
		w.ForgeURL = forgeRepo
	}
	if w.Files == nil {
		w.Files = repoFiles
	}
	e.hub.AddWorkspace(t, w)
	e.hub.SetLeaseTTL(3 * time.Hour)
	e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "The greeting should say hello."})
	e.subs = &subs{tokens: map[string]string{slug: pat}, tokenErr: map[string]error{}}
	e.runner = New(Config{Hub: hub.New(e.hub.URL, "1"), Subscriptions: e.subs, Logger: e.log.Logger(), Version: "v-test", Commit: "c-test"})
	e.runner.reporter = e.sink
	return e
}

func varsEnv(t *testing.T, vars map[string]string) *env {
	return newEnv(t, fakehub.Workspace{Vars: vars})
}

// request is the request of the first attempt of a job of the kind on the input.
func (e *env) request(kind jobs.Kind, input jobs.Input, mutate ...func(*jobs.Request)) jobs.Request {
	req := jobs.Request{
		JobID: jobID, Attempt: 1, Delivery: 1, Workspace: slug, Kind: kind, Input: input,
		Context: []string{}, Answers: []jobs.Answer{}, Source: jobs.Source{Kind: "api", By: "u1"}, Dir: e.dir,
	}
	for _, m := range mutate {
		m(&req)
	}
	return req
}

// fix is the request of a fix of issue 42.
func (e *env) fix(m ...func(*jobs.Request)) jobs.Request {
	return e.request(jobs.KindFix, issueInput("42"), m...)
}

func issueInput(raw string) jobs.Input { return jobs.Input{Issue: json.RawMessage(raw)} }

func (e *env) run(req jobs.Request) (jobs.Result, error) {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return e.runner.Run(ctx, req)
}

// through runs the steps of an attempt up to the one named, and returns the attempt.
func (e *env) through(t *testing.T, last string, req jobs.Request) *attempt {
	t.Helper()
	a := &attempt{req: req, report: e.sink, started: time.Now()}
	t.Cleanup(a.close)
	for _, s := range e.runner.stages() {
		if out := s.run(context.Background(), a); out != nil {
			t.Fatalf("the step %s ended the attempt: %s, %v", s.name, out.result.Envelope, out.err)
		}
		if s.name == last {
			return a
		}
	}
	t.Fatalf("there is no step %s", last)
	return nil
}

// endsWith makes the loop a stand-in that ends with the envelope, and ended is the outcome of an
// envelope of the tool of the attempt.
func (e *env) endsWith(status toolio.Status, mods ...func(*toolio.Envelope)) {
	e.runner.loop = func(_ context.Context, a *attempt) *outcome { return ended(a, status, mods...) }
}

func ended(a *attempt, status toolio.Status, mods ...func(*toolio.Envelope)) *outcome {
	return &outcome{result: jobs.Result{Envelope: fakerunner.Env(status, append(mods, fakerunner.WithTool(string(a.req.Kind)))...), MaxAttempts: a.maxAttempts}}
}

func succeeds(_ context.Context, a *attempt) *outcome { return ended(a, toolio.StatusDone) }

func boxLost(context.Context, *attempt) *outcome {
	return transientFailure(toolio.CategorySandbox, "the box was lost")
}

// loop is a stand-in loop; loopOf makes one that may stop the attempt with the cancel of its context.
type (
	loop   = func(context.Context, *attempt) *outcome
	loopOf = func(cancel context.CancelCauseFunc) loop
)

func always(l loop) loopOf { return func(context.CancelCauseFunc) loop { return l } }

// stopping is a stand-in loop that stops the attempt with the cause and returns its end.
func stopping(cause error) loopOf {
	return func(cancel context.CancelCauseFunc) loop {
		return func(ctx context.Context, _ *attempt) *outcome {
			cancel(cause)
			return &outcome{err: context.Cause(ctx)}
		}
	}
}

// inBox is a stand-in loop that runs body in the box of the attempt, as the loop does.
func inBox(body loop) loop {
	return func(ctx context.Context, a *attempt) *outcome {
		var out *outcome
		err := box.With(ctx, box.Request{
			Provider: box.GatewayName, HostDir: a.cloneDir, RunID: a.runID, Tool: string(a.req.Kind), Labels: a.boxLabels, Registry: a.boxes,
		}, func(*box.Session) error {
			out = body(ctx, a)
			return nil
		})
		if err != nil {
			return transientFailure(toolio.CategorySandbox, err.Error())
		}
		return out
	}
}

func withResult(result any) func(*toolio.Envelope) {
	return func(e *toolio.Envelope) { e.Result = result }
}

// fixResult is the result of a fix that came to the outcome, landed as land.
func fixResult(outcome, land string, closes bool) map[string]any {
	res := map[string]any{
		"outcome": outcome, "branch": "fix/42-greeting-is-wrong", "verdict": "pass", "closes": closes, "land": land,
		"revert_check": map[string]any{"outcome": "proven", "reason": "tests_fail_without_the_change"},
	}
	if land == "pr" {
		res["pull_request"] = map[string]any{"number": 9, "url": forgeRepo + "/pull/9", "draft": false}
	}
	if !closes {
		res["revert_check"] = map[string]any{"outcome": "not_proven", "reason": "tests_pass_without_the_change"}
	}
	return res
}

// triageResult is the result of a triage of issue 42 with the confidence.
func triageResult(confidence, action string) map[string]any {
	return map[string]any{
		"action": action, "diagnosis": map[string]any{"confidence": confidence},
		"issue": map[string]any{"repo": "Org/Repo", "host": "github.example.com", "number": 42, "url": issueURL},
	}
}

// issueNow is issue 42 as the hub holds it, with its labels sorted.
func (e *env) issueNow() fakehub.Issue { return e.issue(slug, 42) }

func (e *env) issue(ws string, n int) fakehub.Issue {
	issue, ok := e.hub.IssueOf(ws, n)
	if !ok {
		e.t.Fatalf("the hub has no issue %d", n)
	}
	slices.Sort(issue.Labels)
	return issue
}

var markerKind = regexp.MustCompile(`kind=([a-z_]+)`)

// commentKinds are the kinds of the comments of issue 42, by their markers.
func (e *env) commentKinds() []string {
	var kinds []string
	for _, c := range e.issueNow().Comments {
		m := markerKind.FindStringSubmatch(c.Body)
		if m == nil {
			m = []string{"", "no-marker"}
		}
		kinds = append(kinds, m[1])
	}
	return kinds
}

// writes are the requests to hub that were not reads.
func (e *env) writes() []string {
	var out []string
	for _, r := range e.hub.Requests() {
		if r.Method != "GET" {
			out = append(out, r.Method+" "+r.Path)
		}
	}
	return out
}

// session is the one session of the hub.
func (e *env) session() fakehub.Session {
	e.t.Helper()
	sessions := e.hub.Sessions()
	if len(sessions) != 1 {
		e.t.Fatalf("%d sessions, want 1", len(sessions))
	}
	return sessions[0]
}

// seqOf is the sequence number of the first such request to hub, or 0.
func (e *env) seqOf(method, prefix, suffix string) int {
	for _, r := range e.hub.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, prefix) && strings.HasSuffix(r.Path, suffix) {
			return r.Seq
		}
	}
	return 0
}

func envelope(t *testing.T, res jobs.Result) map[string]any {
	t.Helper()
	return testutil.Decode[map[string]any](t, res.Envelope)
}

// errorOf is the error object of an envelope.
func errorOf(env map[string]any) (category, message string, retryable bool, hint map[string]any) {
	obj, _ := env["error"].(map[string]any)
	category, _ = obj["category"].(string)
	message, _ = obj["message"].(string)
	retryable, _ = obj["retryable"].(bool)
	hint, _ = obj["fix_hint"].(map[string]any)
	return
}

// codes are the codes of the warnings of an envelope, and failedWrites the kinds of its side
// effects that failed.
func codes(env map[string]any) []string { return pick(env, "warnings", "code", "") }

func failedWrites(env map[string]any) []string { return pick(env, "side_effects", "kind", "failed") }

func pick(env map[string]any, list, field, state string) []string {
	var out []string
	items, _ := env[list].([]any)
	for _, item := range items {
		if m := item.(map[string]any); state == "" || m["state"] == state {
			out = append(out, m[field].(string))
		}
	}
	return out
}

// transient is the failure a transient error carries, or nil when err is none.
func transient(err error) *toolio.Failure {
	var failure *toolio.Failure
	if !jobs.IsTransient(err) || !errors.As(err, &failure) {
		return nil
	}
	return failure
}

// The fixtures of the real loop: a repository whose gate fails, which the brain fixes and commits
// in its box with the target box-commit, and a repository to triage.
var fixFiles = map[string]string{
	"Makefile":            "check:\n\tsh tests/run.sh\nbox-commit:\n\tgit add src tests\n\tgit -c user.name=box -c user.email=box@example.com commit -q -m 'a commit made in the box'\n",
	"tests/run.sh":        "set -e\nfor t in tests/*_test.sh; do sh \"$t\"; done\n",
	"tests/greet_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: greet printed $(sh src/greet.sh)\"; exit 1; }\n",
	"src/greet.sh":        "echo helo\n",
}

var triageFiles = map[string]string{
	"go.mod":       "module example.com/x\n",
	"main.go":      "package main\n\nimport \"example.com/x/util\"\n\n// Run starts the program.\nfunc Run() {\n\tutil.Help()\n}\n\nfunc main() { Run() }\n",
	"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
}

const (
	fixReport         = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"},{"path":"tests/hello_test.sh","why":"the proof"}]}`
	probableDiagnosis = `{"title":"Run starts the wrong thing","severity":"high","confidence":"probable","root_cause":"Run calls Help before it checks its arguments.","affected_files":[{"path":"main.go","line":6,"note":"Run"},{"path":"util/util.go","note":"Help"}],"suggested_fix":"Check the arguments first.","acceptance_criteria":["Run with no arguments prints the usage"]}`
)

func writeStep(path, content string) map[string]any {
	return map[string]any{"tool": "write_file", "args": map[string]any{"path": path, "content": content}, "expect": "ok"}
}

func execStep(command string) map[string]any {
	return map[string]any{"tool": "execute", "args": map[string]any{"command": command}, "expect": "ok"}
}

func submitStep(payload string) map[string]any {
	var p any
	_ = json.Unmarshal([]byte(payload), &p)
	return map[string]any{"submit": p, "expect": "accepted"}
}

// fixSteps fix the greeting, prove it with a test of its own, commit and hand over the report.
func fixSteps() []map[string]any {
	return []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"),
		writeStep("tests/hello_test.sh", "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"),
		execStep("make box-commit"),
		submitStep(fixReport),
	}
}

// scriptFile writes a script of the scripted brain, one round of the steps.
func scriptFile(t *testing.T, steps ...map[string]any) string {
	t.Helper()
	return testutil.WriteFile(t, t.TempDir(), "script.json", string(testutil.JSON(t, map[string]any{"steps": steps})))
}

// loopEnv is an environment that runs the real loop on the files, with the test binary as the af
// of the boxes and the scripted brain, which no variable may choose, doing the steps.
func loopEnv(t *testing.T, files map[string]string, w fakehub.Workspace, steps ...map[string]any) *env {
	t.Helper()
	testutil.Require(t, "sh", "make", "git")
	bin, _ := fakeaf.Install(t)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	w.Files = files
	e := newEnv(t, w)
	script := scriptFile(t, steps...)
	e.runner.loopOptions = func(_ *attempt, o *afcmd.LoopOptions) { o.Brain, o.Script = "scripted", script }
	return e
}

// runDir is the run directory of the loop of the attempt, in its clone.
func (e *env) runDir() string {
	return filepath.Join(e.dir, "clone", ".agent-fox", "run", e.sink.lastHub()[1])
}
