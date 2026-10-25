package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestLoop_IsMadeOfTheAttempt(t *testing.T) {
	e := varsEnv(t, map[string]string{"AF_SANDBOX_PROFILE": "py", "AF_MODEL": "model-1"})
	chosen := "B"
	a := e.through(t, "lease", e.fix(func(r *jobs.Request) {
		r.Context = []string{"c1", "c2"}
		r.Answers = []jobs.Answer{{Question: "Which?", Options: []jobs.Option{{ID: "A", Label: "x"}, {ID: "B", Label: "y"}}, Option: &chosen, Text: "y"}}
	}))

	cfg := e.runner.loopConfig(a)

	if cfg.Boxes != a.boxes || !slices.Equal(cfg.Boxes.Names(), []string{"gateway"}) || cfg.SandboxProfile != "py" || cfg.BoxLabels["af.job"] != jobID || cfg.BoxLabels["af.attempt"] != "1" {
		t.Errorf("boxes %v, profile %q, labels %v, want the gateway of the attempt, py and the job's labels", cfg.Boxes.Names(), cfg.SandboxProfile, cfg.BoxLabels)
	}
	if cfg.Git == nil || cfg.PostPush == nil || cfg.Version != "v-test" || cfg.Commit != "c-test" {
		t.Errorf("git %v, post-push %v, identity %q %q", cfg.Git != nil, cfg.PostPush != nil, cfg.Version, cfg.Commit)
	}
	if client, err := cfg.Forge(issuex.Options{}); err != nil || client == nil {
		t.Errorf("the forge: %v, %v", client, err)
	}
	if _, err := cfg.Forge(issuex.Options{Repo: issuex.Repo{Owner: "someone", Name: "else"}}); err == nil {
		t.Error("the forge takes a repository that is not the workspace's")
	}
	if v, ok := cfg.Brain.Getenv("ANTHROPIC_API_KEY"); cfg.Brain.Model != "model-1" || ok || v != "" {
		t.Errorf("the brain has the model %q and reads %q from the environment", cfg.Brain.Model, v)
	}
	if q := cfg.Earlier.Questions; len(q) != 3 || q[0] != nil || q[1] != nil || q[2] == nil || q[2].Text != "Which?" || !slices.Equal(q[2].Options, []string{"x", "y"}) {
		t.Errorf("earlier questions = %+v, want none for the two strings and Which? with its options", q)
	}

	call, refused := e.runner.loopCall(a)
	o, _ := call.Options.(afcmd.LoopOptions)
	if refused != nil || call.Path != "fix" || !slices.Equal(call.Args, []string{issueURL}) || call.Dir != a.cloneDir || !slices.Equal(call.Context, []string{"c1", "c2", "y"}) ||
		o.Sandbox != "gateway" || o.MaxTurns != 150 || o.Land != "pr" || call.Timeout != a.settings.Timeout {
		t.Errorf("the call is %+v (%v), want fix of the issue in the clone with the settings and the context in order", call, refused)
	}

	app := e.runner.loopApp(a)
	if !app.NoSignals || app.RunID != a.runID || app.Target != a.repo || app.EventSink == nil {
		t.Errorf("no signals %v, run id %q, target %v, sink %v, want the shell of the attempt", app.NoSignals, app.RunID, app.Target, app.EventSink != nil)
	}
}

func TestLoopCall_GivesTextOnStdinAndAFileUnderInput(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	testutil.WriteFile(t, e.dir, "input/stale.md", "what a delivery that died left")

	text, _ := e.runner.loopCall(&attempt{req: e.fix(), input: loopInput{kind: inputText, text: "# T\n\nbody"}})
	file, refused := e.runner.loopCall(&attempt{req: e.fix(), input: loopInput{kind: inputFile, file: &jobs.File{Name: "../../etc/notes.md", Content: "x"}}})

	if stdin, _ := io.ReadAll(text.Stdin); !slices.Equal(text.Args, []string{"-"}) || string(stdin) != "# T\n\nbody" {
		t.Errorf("text: args %v, stdin %q", text.Args, stdin)
	}
	path := filepath.Join(e.dir, "input", "notes.md")
	if refused != nil || !slices.Equal(file.Args, []string{path}) || testutil.ReadFile(t, path) != "x" {
		t.Fatalf("file: args %v, refusal %v, want the file under its base name in input/", file.Args, refused)
	}
	dir, _ := os.Stat(filepath.Join(e.dir, "input"))
	written, _ := os.Stat(path)
	if dir.Mode().Perm() != 0o700 || written.Mode().Perm() != 0o600 {
		t.Errorf("input/ has the mode %v and the file %v, want 0700 and 0600", dir.Mode().Perm(), written.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(e.dir, "input", "stale.md")); !os.IsNotExist(err) {
		t.Errorf("the file of an earlier delivery is still there: %v", err)
	}

	for _, name := range []string{"", ".", "..", "dir/..", "a\x00b"} {
		dir := t.TempDir()
		_, refused := e.runner.loopCall(&attempt{req: jobs.Request{Kind: jobs.KindTriage, Dir: dir}, input: loopInput{kind: inputFile, file: &jobs.File{Name: name}}})
		if entries, _ := os.ReadDir(dir); refused == nil || refused.category != toolio.CategoryUsage || len(entries) != 0 {
			t.Errorf("the name %q: refusal %+v and %d entries, want a usage refusal and nothing written", name, refused, len(entries))
		}
	}
}

// A loop that returns after the attempt was stopped is the cause of the stop, with its envelope.
func TestLoopOutcome_MakesTransientWhatTheInfrastructureFailed(t *testing.T) {
	a := &attempt{req: jobs.Request{Kind: jobs.KindFix}, maxAttempts: 2}
	r := New(Config{})
	for _, tc := range []struct {
		category  string
		retryable bool
		transient bool
	}{
		{"sandbox", true, true}, {"forge", true, true}, {"hub", true, true}, {"io", true, true},
		{"sandbox", false, false}, {"hub", false, false}, {"brain", true, false}, {"no_result", true, false}, {"git", true, false}, {"timeout", true, false},
	} {
		data := fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(tc.category, "it failed", tc.retryable))

		out := r.loopOutcome(context.Background(), a, testutil.Decode[*toolio.Envelope](t, data), data)

		if f := transient(out.err); tc.transient && (f == nil || f.Category != tc.category || f.Message != "it failed" || len(out.result.Envelope) != 0) {
			t.Errorf("%s retryable: %+v, %v, want a transient error with no envelope", tc.category, out.result, out.err)
		}
		if !tc.transient && (out.err != nil || string(out.result.Envelope) != string(data) || out.result.MaxAttempts != 2) {
			t.Errorf("%s retryable %v: %+v, %v, want the envelope as it was", tc.category, tc.retryable, out.result, out.err)
		}
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(jobs.ErrCancelled)
	data := fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("cancelled", "stopped", false))
	if out := r.loopOutcome(ctx, a, testutil.Decode[*toolio.Envelope](t, data), data); !errors.Is(out.err, jobs.ErrCancelled) || len(out.result.Envelope) == 0 {
		t.Errorf("a stopped loop: %v, %s, want the cancel with the envelope", out.err, out.result.Envelope)
	}
}

func TestReportOf_ReadsOnlyTheReportOfTheRunInTheClone(t *testing.T) {
	dir := t.TempDir()
	clone := filepath.Join(dir, "clone")
	inside := testutil.WriteFile(t, clone, "run/report.md", "# the report\n")
	outside := testutil.WriteFile(t, dir, "elsewhere.md", "# not the report\n")
	big := testutil.WriteFile(t, clone, "big.md", strings.Repeat("x", 2*maxReport))
	a := &attempt{cloneDir: clone}
	for name, tc := range map[string]struct {
		artifacts []toolio.Artifact
		want      string
	}{
		"the report in the clone":    {[]toolio.Artifact{{Kind: "branch", Name: "b"}, {Kind: "report", Path: inside}}, "# the report\n"},
		"a report outside the clone": {[]toolio.Artifact{{Kind: "report", Path: outside}}, ""},
		"a report that climbs out":   {[]toolio.Artifact{{Kind: "report", Path: filepath.Join(clone, "..", "elsewhere.md")}}, ""},
		"a report that is not there": {[]toolio.Artifact{{Kind: "report", Path: filepath.Join(clone, "gone.md")}}, ""},
		"a report with no path":      {[]toolio.Artifact{{Kind: "report"}}, ""},
		"a report that is a folder":  {[]toolio.Artifact{{Kind: "report", Path: filepath.Join(clone, "run")}}, ""},
		"a report of two megabytes":  {[]toolio.Artifact{{Kind: "report", Path: big}}, strings.Repeat("x", maxReport)},
	} {
		if got := a.reportOf(&toolio.Envelope{Artifacts: tc.artifacts}); got != tc.want {
			t.Errorf("%s: %d bytes, want %d", name, len(got), len(tc.want))
		}
	}
}

// What the jobs layer keeps in the attempt directory is left as it was.
func TestRun_RemovesTheCloneAndTheInputHoweverTheAttemptEnds(t *testing.T) {
	for name, body := range map[string]loopOf{
		"done": always(succeeds), "a transient error": always(boxLost), "a cancel": stopping(jobs.ErrCancelled),
		"a shutdown": stopping(jobs.ErrShutdown), "another stop": stopping(context.Canceled),
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			seeded := map[string]string{"envelope.json": "{\"seeded\": true}\n", "report.md": "# seeded\n"}
			for name, content := range seeded {
				testutil.WriteFile(t, e.dir, name, content)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			loop := body(cancel)
			e.runner.loop = func(ctx context.Context, a *attempt) *outcome {
				testutil.WriteFile(t, a.req.Dir, "input/notes.md", "x")
				return loop(ctx, a)
			}

			_, _ = e.runner.Run(ctx, e.request(jobs.KindTriage, jobs.Input{Text: "t"}))

			if got := testutil.Files(t, e.dir); len(got) != 2 || got["envelope.json"] != seeded["envelope.json"] || got["report.md"] != seeded["report.md"] {
				t.Errorf("the attempt directory holds %v, want the two files that were there", got)
			}
		})
	}
}

func TestRun_ALoopThatComesToNoOutcomeIsAnError(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	e.runner.reporter = nil // and the attempt reports to nothing, which a request without a recorder has
	e.runner.loop = func(context.Context, *attempt) *outcome { return nil }

	res, err := e.run(e.fix())

	if err == nil || jobs.IsTransient(err) || !strings.Contains(err.Error(), "without an envelope") || len(res.Envelope) != 0 {
		t.Errorf("result %s, error %v, want the error of the worker", res.Envelope, err)
	}
	if issue, s := e.issueNow(), e.session(); len(issue.Labels) != 0 || s.Status != "failed" || s.Message != err.Error() {
		t.Errorf("issue %v, session %q %q, want in-progress taken off and the session failed with the error", issue.Labels, s.Status, s.Message)
	}
}

// The issue is told the failure.
func TestRun_ACredentialRefusedAtTheCloneOrTheLeaseIsAnAuthFailure(t *testing.T) {
	for _, at := range [][2]string{{"", gitPath}, {"POST", sandboxesPath}} {
		for _, status := range []int{401, 403} {
			e := newEnv(t, fakehub.Workspace{})
			e.hub.Inject(at[0], at[1], fakehub.Fault{Status: status})

			res, err := e.run(e.fix())

			category, message, retryable, _ := errorOf(envelope(t, res))
			if err != nil || category != toolio.CategoryAuth || retryable || !strings.Contains(message, "PUT /api/v1/workspaces/acme") || strings.Contains(message, pat) {
				t.Errorf("%s %d: error %v, envelope %s, want an auth failure that says how to replace the token", at[1], status, err, res.Envelope)
			}
			if issue := e.issueNow(); !slices.Equal(issue.Labels, []string{"af:failed"}) || len(issue.Comments) != 1 || len(e.hub.Leases()) != 0 {
				t.Errorf("%s %d: issue %v with %d comments, %d leases, want failed and one comment, and no lease", at[1], status, issue.Labels, len(issue.Comments), len(e.hub.Leases()))
			}
		}
	}
}

// A cancel is an envelope that says so, a shutdown its cause.
func TestRun_AStopDuringAStepIsTheStop(t *testing.T) {
	at := map[string]func(e *env, stop func()){
		"the clone": func(e *env, stop func()) {
			stubGit(e, func(ctx context.Context, argv []string) (string, int, error) {
				stop()
				<-ctx.Done()
				return "", -1, ctx.Err()
			})
		},
		"the identity of the clone": func(e *env, stop func()) {
			stubGit(e, func(ctx context.Context, argv []string) (string, int, error) {
				if argv[1] == "config" {
					stop()
					return "", -1, ctx.Err()
				}
				return "", 0, nil
			})
		},
		"the lease": func(e *env, stop func()) {
			e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Delay: time.Minute})
			e.hub.OnRequest(func(r fakehub.Request) {
				if r.Method == "POST" && r.Path == sandboxesPath {
					stop()
				}
			})
		},
	}
	for step, setup := range at {
		for _, cause := range []error{jobs.ErrCancelled, jobs.ErrUnsubscribed, jobs.ErrShutdown} {
			e := newEnv(t, fakehub.Workspace{})
			ctx, cancel := context.WithCancelCause(context.Background())
			setup(e, func() { cancel(cause) })

			res, err := e.runner.Run(ctx, e.fix())

			if cause == jobs.ErrShutdown {
				if !errors.Is(err, jobs.ErrShutdown) || jobs.IsTransient(err) || len(res.Envelope) != 0 {
					t.Errorf("a shutdown at %s: result %s, error %v, want the shutdown", step, res.Envelope, err)
				}
			} else if category, _, _, _ := errorOf(envelope(t, res)); err != nil || category != "cancelled" {
				t.Errorf("%v at %s: result %s, error %v, want a cancelled envelope", cause, step, res.Envelope, err)
			}
		}
	}
}
