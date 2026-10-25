package fix

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

const runID = "20261020_100000_aaaaaa"

// repo is a checkout on main, with a bare origin when it was asked for.
type repo struct {
	Dir, Origin string
	Git         *gitx.Git
}

func newRepo(t *testing.T, files map[string]string, withOrigin bool) *repo {
	t.Helper()
	testutil.IsolateGit(t)
	r := &repo{Dir: testutil.Repo(t, files)}
	r.Git = gitx.New(r.Dir, gitx.ExecRunner)
	r.Git.SetSleep(func(time.Duration) {})
	if withOrigin {
		r.Origin = filepath.Join(t.TempDir(), "origin.git")
		testutil.Git(t, r.Dir, "init", "-q", "--bare", "-b", "main", r.Origin)
		testutil.Git(t, r.Dir, "remote", "add", "origin", r.Origin)
		testutil.Git(t, r.Dir, "push", "-q", "-u", "origin", "main")
	}
	return r
}

// git runs git in the checkout and returns what it printed, trimmed.
func (r *repo) git(t *testing.T, args ...string) string {
	t.Helper()
	return strings.TrimSpace(testutil.Git(t, r.Dir, args...))
}

func (r *repo) branches(t *testing.T) []string {
	t.Helper()
	return strings.Fields(r.git(t, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
}

func (r *repo) remoteHas(t *testing.T, branch string) bool {
	t.Helper()
	return r.git(t, "ls-remote", "--heads", "origin", branch) != ""
}

// message is the message of the last commit of the ref.
func (r *repo) message(t *testing.T, ref string) string {
	t.Helper()
	return r.git(t, "log", "-1", "--format=%B", ref)
}

// files are the files of the checkout, its .git and .agent-fox left out.
func (r *repo) files(t *testing.T) map[string]string {
	t.Helper()
	return testutil.Files(t, r.Dir, ".git", ".agent-fox")
}

func (r *repo) read(t *testing.T, name string) string {
	t.Helper()
	data, _ := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(name)))
	return string(data)
}

func (r *repo) hook(t *testing.T, name, script string) {
	t.Helper()
	if err := os.Chmod(testutil.WriteFile(t, r.Dir, ".git/hooks/"+name, script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// answering is a git of the checkout whose commands answer may answer in place of git: it
// answers when ok is set.
func answering(dir string, answer func(argv []string) (out string, code int, ok bool)) *gitx.Git {
	g := gitx.New(dir, func(ctx context.Context, d string, argv []string, stdin ...string) (string, int, error) {
		if out, code, ok := answer(argv); ok {
			return out, code, nil
		}
		return gitx.ExecRunner(ctx, d, argv, stdin...)
	})
	g.SetSleep(func(time.Duration) {})
	return g
}

// has says whether the argv holds the word.
func has(argv []string, word string) bool {
	for _, a := range argv {
		if a == word {
			return true
		}
	}
	return false
}

// gateFiles is a checkout whose Makefile check target runs every test under tests/.
func gateFiles() map[string]string {
	return map[string]string{
		"Makefile":            "check:\n\tsh tests/run.sh\n",
		"tests/run.sh":        "set -e\nfor t in tests/*_test.sh; do sh \"$t\"; done\n",
		"tests/greet_test.sh": "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: greet printed $(sh src/greet.sh)\"; exit 1; }\n",
		"src/greet.sh":        "echo hello\n",
	}
}

func requireMake(t *testing.T) {
	t.Helper()
	testutil.Require(t, "make", "sh")
}

// failure is the *toolio.Failure of an error.
func failure(t *testing.T, err error) *toolio.Failure {
	t.Helper()
	var f *toolio.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *toolio.Failure", err, err)
	}
	return f
}

func sampleState() State {
	return State{
		RunID:  runID,
		Input:  StateInput{Kind: "issue", Origin: "https://github.com/o/r/issues/7", Key: "https://github.com/o/r/issues/7"},
		Issue:  &StateIssue{URL: "https://github.com/o/r/issues/7", Title: "Greeting is wrong", Number: 7},
		Target: StateTarget{Repo: "o/r", Host: "github.com"}, Base: "main", BaseHead: "0123456789abcdef0123456789abcdef01234567",
		Table:          project.Table{Source: "detected", Checks: []project.Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: "check", Source: "Makefile:check", Available: true}}},
		BaselineGateID: "abc123", BaselinePassed: true, BaselineMS: 1200,
		PrefixSHA256: "feed", PrefixBytes: 1234, Suite: [][]string{{"make", "check"}}, Programs: []string{"make", "sh"}, Context: []string{"A", "B"},
	}
}

// recorder is a Reporter that keeps what it is told.
type recorder struct {
	warnings  []string
	messages  []string
	artifacts []toolio.Artifact
	effects   []toolio.SideEffect
	next      []toolio.Next
}

func (r *recorder) Warn(code, message string) {
	r.warnings = append(r.warnings, code)
	r.messages = append(r.messages, message)
}
func (r *recorder) Artifact(a toolio.Artifact)     { r.artifacts = append(r.artifacts, a) }
func (r *recorder) SideEffect(s toolio.SideEffect) { r.effects = append(r.effects, s) }
func (r *recorder) Next(n toolio.Next)             { r.next = append(r.next, n) }

// warning is the message of the first warning of the code, and whether there is one.
func (r *recorder) warning(code string) (string, bool) {
	for i, c := range r.warnings {
		if c == code {
			return r.messages[i], true
		}
	}
	return "", false
}

// effect is the last side effect of the kind, or the zero one.
func (r *recorder) effect(kind string) toolio.SideEffect {
	var out toolio.SideEffect
	for _, e := range r.effects {
		if e.Kind == kind {
			out = e
		}
	}
	return out
}

func (r *recorder) artifact(kind string) toolio.Artifact {
	for _, a := range r.artifacts {
		if a.Kind == kind {
			return a
		}
	}
	return toolio.Artifact{}
}

// nextIs says whether one of the next steps is the argv.
func (r *recorder) nextIs(argv ...string) bool {
	for _, n := range r.next {
		if strings.Join(n.Argv, " ") == strings.Join(argv, " ") {
			return true
		}
	}
	return false
}

// landWorld is a checkout with a bare origin and a change in its working tree, a fake forge,
// and the state of a run on it.
type landWorld struct {
	repo   *repo
	forge  *fakeforge.Forge
	client issuex.Client
	target issuex.Repo
	state  State
	rep    *recorder
}

func newLandWorld(t *testing.T, kind string) *landWorld {
	t.Helper()
	r := newRepo(t, gateFiles(), true)
	f := fakeforge.New(t, kind, "o/r")
	target := issuex.Repo{Owner: "o", Name: "r"}
	client, err := f.NewForge("token")(issuex.Options{Repo: target})
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, r.Dir, "src/greet.sh", "echo hello, world\n")
	return &landWorld{repo: r, forge: f, client: client, target: target, rep: &recorder{},
		state: State{RunID: runID, Base: "main", BaseHead: r.git(t, "rev-parse", "HEAD")}}
}

// withIssue makes the run one of issue 7, and is its input.
func (w *landWorld) withIssue() Input {
	w.state.Issue = &StateIssue{URL: "https://github.com/o/r/issues/7", Title: "Greeting is wrong", Number: 7}
	w.state.Input = StateInput{Kind: "issue", Origin: w.state.Issue.URL, Key: w.state.Issue.URL}
	return Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}
}

// onBase checks that the checkout is back on the base, at its commit, with a clean tree.
func (w *landWorld) onBase(t *testing.T) {
	t.Helper()
	if branch, _ := w.repo.Git.CurrentBranch(context.Background()); branch != "main" {
		t.Errorf("checked out %q, want the base", branch)
	}
	if dirty, _ := w.repo.Git.DirtyFiles(context.Background()); len(dirty) != 0 {
		t.Errorf("the tree is dirty: %v", dirty)
	}
	if head := w.repo.git(t, "rev-parse", "HEAD"); head != w.state.BaseHead {
		t.Errorf("HEAD = %s, want the base %s", head, w.state.BaseHead)
	}
}

// postPush is a post-push hook that notes, at each call, the branch, whether origin had it,
// and how many pull requests had been asked for.
type postPush struct {
	w        *landWorld
	t        *testing.T
	branches []string
	onRemote []bool
	pulls    []int
	err      error
}

func (h *postPush) hook(_ context.Context, branch string) error {
	h.branches = append(h.branches, branch)
	h.onRemote = append(h.onRemote, h.w.repo.remoteHas(h.t, branch))
	h.pulls = append(h.pulls, h.w.forge.Count(fakeforge.OpPullRequest))
	return h.err
}

// measureWorld is a checkout over the gate fixture whose preflight has run, so that its
// baseline is measured, with the counts of the gate and of the revert check.
type measureWorld struct {
	repo         *repo
	state        State
	gateRuns     int
	conformRuns  int
	conformSince string
	warnings     []string
}

// measuredFiles is the gate fixture with a script that prints the wrong word and a test that
// takes any, and files for a protected path and for a change.
func measuredFiles() map[string]string {
	files := gateFiles()
	files["src/greet.sh"] = "echo helo\n"
	files["tests/greet_test.sh"] = "[ -n \"$(sh src/greet.sh)\" ] || { echo \"FAIL: greet printed nothing\"; exit 1; }\n"
	files[".specs/a.md"] = "# spec\n"
	files["docs/readme.md"] = "readme\n"
	return files
}

func newMeasureWorld(t *testing.T) *measureWorld {
	t.Helper()
	requireMake(t)
	r := newRepo(t, measuredFiles(), false)
	pre, err := preflightAll(context.Background(), preflightParams(r))
	if err != nil {
		t.Fatal(err)
	}
	return &measureWorld{repo: r, state: State{
		Base: pre.Base, BaseHead: pre.BaseHead, Table: pre.Detection.Gate, BaselineGateID: pre.Baseline.ID, BaselinePassed: pre.Baseline.Passed,
		BaselineMS: GateMS(pre.Baseline),
	}}
}

func (w *measureWorld) edit(t *testing.T, name, content string) {
	t.Helper()
	testutil.WriteFile(t, w.repo.Dir, name, content)
}

func (w *measureWorld) gate(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
	w.gateRuns++
	return checks.Run(ctx, dir, o)
}

func (w *measureWorld) conform(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error) {
	w.conformRuns++
	w.conformSince = since
	return conform.Check(ctx, dir, since, o)
}

// measure measures the change on the checkout, with the counting seams.
func (w *measureWorld) measure() (Measurement, error) {
	w.warnings = nil
	return Measure(context.Background(), MeasureParams{
		Dir: w.repo.Dir, Git: w.repo.Git, State: w.state,
		Warn: func(code, _ string) { w.warnings = append(w.warnings, code) },
		Gate: w.gate, Conform: w.conform,
	})
}

// noForge is the client of a run that has no forge: it is never asked.
type noForge struct{ issuex.Client }

func (noForge) Authenticated() bool { return false }

func preflightParams(r *repo) PreflightParams {
	return PreflightParams{Dir: r.Dir, Land: LandNone, Forge: noForge{}, Warn: func(string, string) {}}
}

// preflightAll is the whole of the checks before a run: those of the host, then the gate and
// the baseline.
func preflightAll(ctx context.Context, p PreflightParams) (Preflighted, error) {
	pre, err := Preflight(ctx, p)
	if err != nil {
		return pre, err
	}
	return pre, pre.Measure(ctx, p)
}

// spy is a brain that runs its rounds, one for each Run, and keeps the briefs it was given;
// past its rounds it ends with no submission.
type spy struct {
	rounds []func(ctx context.Context, b brain.Brief) (brain.Outcome, error)
	briefs []brain.Brief
}

func (s *spy) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	s.briefs = append(s.briefs, b)
	if len(s.briefs) > len(s.rounds) {
		return brain.Outcome{StopReason: brain.StopEnded}, nil
	}
	return s.rounds[len(s.briefs)-1](ctx, b)
}

// submit submits the payload the way a brain's tool does, through the submit core.
func submit(ctx context.Context, b brain.Brief, payload string) (brain.Outcome, error) {
	target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
	if _, err := submission.Submit(ctx, target, b.Kind, []byte(payload), time.Now()); err != nil {
		return brain.Outcome{StopReason: brain.StopError}, err
	}
	return brain.Outcome{Submitted: true, StopReason: brain.StopSubmitted}, nil
}

// edits writes files into the box of the brief.
func edits(t *testing.T, b brain.Brief, files map[string]string) {
	t.Helper()
	for name, content := range files {
		testutil.WriteFile(t, b.Box.Spec().Workdir, name, content)
	}
}

// round is a round of a spy that writes the files, submits the payload, and has used the cost
// and the turns.
func round(t *testing.T, files map[string]string, payload string, cost float64, turns int) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		edits(t, b, files)
		out, err := submit(ctx, b, payload)
		out.CostUSD, out.Turns, out.CostSource = cost, turns, brain.CostMeasured
		return out, err
	}
}

const reportPayload = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}]}`

// openLocalBox opens a local box over the checkout, closed when the test ends.
func openLocalBox(t *testing.T, dir string) *box.Session {
	t.Helper()
	sess, err := box.Open(context.Background(), box.Request{Provider: "local", HostDir: dir, RunID: runID, Tool: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func boxFile(ctx context.Context, sess *box.Session, rel string) (string, bool) {
	data, err := fs.ReadFile(sess.FS(ctx), rel)
	return string(data), err == nil
}

// ledgerKinds are the kinds of the records of the ledger of the run.
func ledgerKinds(t *testing.T, run *toolio.RunDir) []string {
	t.Helper()
	entries, err := run.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

// warnings is a brain.Report that keeps the codes of its warnings.
type warnings struct{ codes []string }

func (*warnings) Emit(string, any)         {}
func (w *warnings) Warn(code, _ string)    { w.codes = append(w.codes, code) }
func (*warnings) Artifact(toolio.Artifact) {}
func (*warnings) SetUsage(toolio.Usage)    {}

func countOf(list []string, s string) int {
	n := 0
	for _, x := range list {
		if x == s {
			n++
		}
	}
	return n
}
