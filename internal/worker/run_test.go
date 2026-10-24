package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"
	"github.com/agent-fox-dev/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const (
	sessionsPath = "/api/v1/sessions"
	pullsPath    = "/api/v1/workspaces/acme/pulls"
	issuePath    = "/api/v1/workspaces/acme/issues/42"
)

// resultMap is the result object of an envelope.
func resultMap(t *testing.T, res jobs.Result) map[string]any {
	t.Helper()
	m, _ := envelope(t, res)["result"].(map[string]any)
	if m == nil {
		t.Fatalf("the envelope has no result: %s", res.Envelope)
	}
	return m
}

// codes are the codes of the warnings of an envelope.
func codes(env map[string]any) []string {
	var out []string
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		out = append(out, w.(map[string]any)["code"].(string))
	}
	return out
}

// seqOf is the sequence number of the first request to hub with the method whose path has the
// prefix and the suffix, or 0 when there is none.
func seqOf(e *env, method, prefix, suffix string) int {
	for _, r := range e.hub.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, prefix) && strings.HasSuffix(r.Path, suffix) {
			return r.Seq
		}
	}
	return 0
}

// lastSeqAt is the sequence number of the last request to hub with the method and exactly the path,
// or 0 when there is none.
func lastSeqAt(e *env, method, path string) int {
	last := 0
	for _, r := range e.hub.RequestsTo(method, path) {
		last = r.Seq
	}
	return last
}

// TestTheFixLoopRunsWithOneAttemptAndAFMaxAttemptsIsReturned_TS_13_30 verifies TS-13-30.
// Verifies: 13-REQ-4.5
func TestTheFixLoopRunsWithOneAttemptAndAFMaxAttemptsIsReturned_TS_13_30(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_ATTEMPTS": "5"}, nil, fixSteps()...)

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	if env["status"] != "done" || res.MaxAttempts != 5 {
		t.Errorf("status %v, max attempts %d, error %v, want a done fix and 5", env["status"], res.MaxAttempts, env["error"])
	}
	if got := resultMap(t, res)["attempts_max"]; got != float64(1) {
		t.Errorf("the fix reports %v attempts in the run, want 1", got)
	}
}

// TestATriageOnACarryPatchWorkspaceRunsNormally_TS_13_32 verifies TS-13-32.
// Verifies: 13-REQ-4.7
func TestATriageOnACarryPatchWorkspaceRunsNormally_TS_13_32(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, func(w *fakehub.Workspace) { w.Mode = "carry_patch" }, submitStep(probableDiagnosis))

	res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	category, message, _, _ := errorOf(env)
	if env["status"] != "done" || category == "unsupported" {
		t.Errorf("status %v, error %q %q, want a done triage", env["status"], category, message)
	}
	if env["tool"] != "triage" {
		t.Errorf("tool = %v", env["tool"])
	}
}

// TestTheWorkerCreatesOnlyCloneAndInputWithMode0700AndLeavesTheRestAlone_TS_13_33 verifies
// TS-13-33: for an attempt on a file, clone/ and input/ are made with mode 0700 and the file is
// written under input/ with its base name, and once the attempt has returned the pre-seeded
// envelope.json and report.md are as they were, with the attempt directory, and nothing else.
// Verifies: 13-REQ-5.1
func TestTheWorkerCreatesOnlyCloneAndInputWithMode0700AndLeavesTheRestAlone_TS_13_33(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	seeded := map[string]string{"envelope.json": "{\"seeded\": true}\n", "report.md": "# seeded\n"}
	for name, content := range seeded {
		if err := os.WriteFile(filepath.Join(e.dir, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	var during struct {
		entries           []string
		cloneMode, inMode fs.FileMode
		fileMode          fs.FileMode
		notes             string
		staleErr          error
		seen              bool
	}
	e.sink.hook = func(typ string, _ json.RawMessage) {
		if typ != "run_start" || during.seen {
			return
		}
		during.seen = true
		list, _ := os.ReadDir(e.dir)
		for _, entry := range list {
			during.entries = append(during.entries, entry.Name())
		}
		if info, err := os.Stat(filepath.Join(e.dir, "clone")); err == nil {
			during.cloneMode = info.Mode().Perm()
		}
		if info, err := os.Stat(filepath.Join(e.dir, "input")); err == nil {
			during.inMode = info.Mode().Perm()
		}
		data, _ := os.ReadFile(filepath.Join(e.dir, "input", "notes.md"))
		during.notes = string(data)
		if info, err := os.Stat(filepath.Join(e.dir, "input", "notes.md")); err == nil {
			during.fileMode = info.Mode().Perm()
		}
		_, during.staleErr = os.Stat(filepath.Join(e.dir, "input", "stale.md"))
	}
	if err := os.MkdirAll(filepath.Join(e.dir, "input"), 0o755); err != nil { // what a delivery that crashed left
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "input", "stale.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := e.request(jobs.KindFix, jobs.Input{File: &jobs.File{Name: "notes.md", Content: "The greeting says helo.\n"}})

	res, err := e.run(req)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if !during.seen {
		t.Fatal("the loop never started")
	}
	if during.cloneMode != 0o700 || during.inMode != 0o700 {
		t.Errorf("clone/ has mode %v and input/ %v, want 0700 for both", during.cloneMode, during.inMode)
	}
	if during.fileMode != 0o600 || !os.IsNotExist(during.staleErr) {
		t.Errorf("input/notes.md has mode %v and the stale file of the earlier delivery is %v, want 0600 and gone", during.fileMode, during.staleErr)
	}
	if !slices.Equal(during.entries, []string{"clone", "envelope.json", "input", "report.md"}) {
		t.Errorf("the attempt directory held %v during the run, want clone/, input/ and the two files that were there", during.entries)
	}
	if during.notes != "The greeting says helo.\n" {
		t.Errorf("input/notes.md = %q, want the file", during.notes)
	}
	list, err := os.ReadDir(e.dir)
	if err != nil {
		t.Fatalf("the attempt directory is gone: %v", err)
	}
	var left []string
	for _, entry := range list {
		left = append(left, entry.Name())
		if want, ok := seeded[entry.Name()]; ok {
			if got, _ := os.ReadFile(filepath.Join(e.dir, entry.Name())); string(got) != want {
				t.Errorf("%s = %q, want what was there", entry.Name(), got)
			}
		}
	}
	if !slices.Equal(left, []string{"envelope.json", "report.md"}) {
		t.Errorf("the attempt directory holds %v after the attempt, want the two files, and no other entry", left)
	}
}

// TestCloneAndInputAreRemovedOnEveryReturn_TS_13_39 verifies
// TS-13-39.
// Verifies: 13-REQ-5.7
func TestCloneAndInputAreRemovedOnEveryReturn_TS_13_39(t *testing.T) {
	for name, tc := range map[string]struct {
		body func(cancel context.CancelCauseFunc) func(ctx context.Context, a *attempt) *outcome
	}{
		"a normal return": {func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome { return succeeds }},
		"a failure": {func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
			return func(_ context.Context, a *attempt) *outcome {
				return ended(a, &toolio.Failure{Category: toolio.CategoryBrain, Message: "the brain failed"})
			}
		}},
		"a shutdown": {func(cancel context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
			return func(ctx context.Context, _ *attempt) *outcome {
				cancel(jobs.ErrShutdown)
				return &outcome{err: context.Cause(ctx)}
			}
		}},
		"a cancellation": {func(cancel context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
			return func(ctx context.Context, _ *attempt) *outcome {
				cancel(jobs.ErrCancelled)
				return &outcome{err: context.Cause(ctx)}
			}
		}},
		"a transient error": {func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
			return func(context.Context, *attempt) *outcome {
				return transientFailure(toolio.CategorySandbox, "the box was lost")
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			e := leaseEnv(t, nil)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			body := tc.body(cancel)
			e.runner.loop = func(ctx context.Context, a *attempt) *outcome {
				if err := os.MkdirAll(filepath.Join(a.req.Dir, "input"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(a.req.Dir, "input", "notes.md"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				return body(ctx, a)
			}

			_, _ = e.runner.Run(ctx, e.fixRequest())

			_, cloneErr := os.Stat(filepath.Join(e.dir, "clone"))
			_, inputErr := os.Stat(filepath.Join(e.dir, "input"))
			if cloneErr == nil {
				t.Error("clone/ is still there")
			}
			if inputErr == nil {
				t.Error("input/ is still there")
			}
			if _, err := os.Stat(e.dir); err != nil {
				t.Errorf("the attempt directory: %v", err)
			}
		})
	}
}

// TestTheRequestsOfAFixAttemptHappenInThePipelineOrder_TS_13_57 verifies TS-13-57: the reads of
// the workspace and the issue, the session, the clone, the lease, the requests of the loop, the
// push of the branch, the pull request and the release come in that order.
// Verifies: 13-REQ-8.1
func TestTheRequestsOfAFixAttemptHappenInThePipelineOrder_TS_13_57(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	var cloned int
	for _, g := range e.hub.GitRequests() {
		if cloned == 0 || g.Seq < cloned {
			cloned = g.Seq
		}
	}
	var gitPush int
	for _, g := range e.hub.GitRequests() {
		if g.Method == "POST" && strings.HasSuffix(g.Path, "/git-receive-pack") {
			gitPush = g.Seq
		}
	}
	leases := e.hub.Leases()
	if len(leases) != 1 {
		t.Fatalf("%d leases", len(leases))
	}
	steps := []struct {
		name string
		seq  int
	}{
		{"read the record", seqOf(e, "GET", "/api/v1/workspaces/acme", "")},
		{"read the variables", seqOf(e, "GET", "/api/v1/workspaces/acme/vars", "")},
		{"read the issue", seqOf(e, "GET", issuePath, "")},
		{"open the session", seqOf(e, "POST", sessionsPath, "")},
		{"mark the issue", seqOf(e, "POST", issuePath+"/labels", "")},
		{"clone", cloned},
		{"lease", seqOf(e, "POST", sandboxesPath, "")},
		{"read the issue in the loop", lastSeqAt(e, "GET", issuePath)},
		{"push to the git server", gitPush},
		{"publish the branch", seqOf(e, "POST", "/api/v1/workspaces/acme/branches/", "/push")},
		{"open the pull request", seqOf(e, "POST", pullsPath, "")},
		{"post the summary", seqOf(e, "POST", issuePath+"/comments", "")},
		{"release the lease", leases[0].ReleasedSeq},
	}
	for i, s := range steps {
		if s.seq == 0 {
			t.Errorf("%s: no such request", s.name)
		}
		if i > 0 && steps[i-1].seq >= s.seq {
			t.Errorf("%s (%d) did not come after %s (%d)", s.name, s.seq, steps[i-1].name, steps[i-1].seq)
		}
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || !strings.HasPrefix(pulls[0].Head, "fix/42-") {
		t.Errorf("pull requests = %+v, want one for the branch of issue 42", pulls)
	}
}

// TestInfrastructureFailuresReturnTransientErrorsThatTheJobsLayerHandsOverAgain_TS_13_58 verifies
// TS-13-58: hub not answering at the record, the variables, the issue and the session, and a
// loop that ends failed and retryable with category sandbox, forge or hub, return a transient error
// that carries the failure and no envelope; a loop that ends in another failure is an envelope; and
// the same attempt, handed over again, runs.
// Verifies: 13-REQ-8.2
func TestInfrastructureFailuresReturnTransientErrorsThatTheJobsLayerHandsOverAgain_TS_13_58(t *testing.T) {
	assertTransient := func(t *testing.T, res jobs.Result, err error, category string) *toolio.Failure {
		t.Helper()
		f := transient(err)
		if f == nil || f.Category != category || !f.Retryable || f.Message == "" {
			t.Fatalf("error = %v, want a transient error carrying a retryable failure of category %s", err, category)
		}
		if len(res.Envelope) != 0 {
			t.Errorf("the result holds an envelope: %s", res.Envelope)
		}
		return f
	}

	t.Run("hub does not answer before the loop", func(t *testing.T) {
		for name, tc := range map[string]struct{ method, path string }{
			"the record":    {"GET", "/api/v1/workspaces/acme"},
			"the variables": {"GET", "/api/v1/workspaces/acme/vars/resolved"},
			"the issue":     {"GET", issuePath},
			"the session":   {"POST", sessionsPath},
		} {
			t.Run(name, func(t *testing.T) {
				e := newEnv(t, fakehub.Workspace{Branch: "main", Files: fixFiles})
				e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "x"})
				e.hub.Inject(tc.method, tc.path, fakehub.Fault{Status: 503})

				res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

				_ = assertTransient(t, res, err, "hub")
			})
		}
	})

	t.Run("the loop ends with a retryable sandbox failure", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Gateway().RefuseBoxes("no room for another box")

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		f := assertTransient(t, res, err, "sandbox")
		if !strings.Contains(f.Message, "no room for another box") {
			t.Errorf("message = %q, want the reason of the gateway", f.Message)
		}
		if leases := e.hub.Leases(); len(leases) != 1 || !leases[0].Released {
			t.Errorf("leases = %+v, want the one that was granted released", leases)
		}
	})

	t.Run("the loop ends with a retryable forge failure", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Inject("GET", issuePath, fakehub.Fault{Status: 429, Skip: 1}) // the read of resolve passes, the loop's does not

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		_ = assertTransient(t, res, err, "forge")
	})

	t.Run("an envelope is classified by its category and its retryable flag", func(t *testing.T) {
		for name, tc := range map[string]struct {
			category  string
			retryable bool
			transient bool
		}{
			"sandbox retryable":     {"sandbox", true, true},
			"forge retryable":       {"forge", true, true},
			"hub retryable":         {"hub", true, true},
			"sandbox not retryable": {"sandbox", false, false},
			"forge not retryable":   {"forge", false, false},
			"hub not retryable":     {"hub", false, false},
			"brain retryable":       {"brain", true, false},
			"no_result retryable":   {"no_result", true, false},
			"git retryable":         {"git", true, false},
			"timeout retryable":     {"timeout", true, false},
		} {
			t.Run(name, func(t *testing.T) {
				e := leaseEnv(t, nil)
				a := &attempt{req: e.fixRequest(), report: e.sink, maxAttempts: 2}
				stdout := fakerunner.Env(toolio.StatusFailed, fakerunner.WithError(tc.category, "it failed: "+tc.category, tc.retryable))

				out := e.runner.loopOutcome(context.Background(), a, stdout)

				if got := jobs.IsTransient(out.err); got != tc.transient {
					t.Fatalf("transient = %v (%v), want %v", got, out.err, tc.transient)
				}
				if tc.transient {
					f := transient(out.err)
					if f == nil || f.Category != tc.category || !f.Retryable || f.Message != "it failed: "+tc.category || len(out.result.Envelope) != 0 {
						t.Errorf("failure %+v, result %q: want the category, the message and no envelope", f, out.result.Envelope)
					}
				} else if out.err != nil || !bytes.Equal(out.result.Envelope, stdout) || out.result.MaxAttempts != 2 {
					t.Errorf("result %q, error %v, want the envelope as it was", out.result.Envelope, out.err)
				}
			})
		}
	})

	t.Run("the same attempt, handed over again, runs", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Gateway().RefuseBoxes("no room for another box")
		req := e.request(jobs.KindFix, issueInput("42"))

		_, err := e.run(req)
		if !jobs.IsTransient(err) {
			t.Fatalf("the first delivery: %v, want a transient error", err)
		}
		e.hub.Gateway().RefuseBoxes("")
		req.Delivery = 2
		// A clone that a delivery which died left is removed by the next one.
		if err := os.MkdirAll(filepath.Join(e.dir, "clone"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(e.dir, "clone", "left-by-the-first-delivery"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		res, err := e.run(req)

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("the second delivery: %s, %v, want a done envelope", res.Envelope, err)
		}
		if len(e.hub.Leases()) != 2 {
			t.Errorf("%d leases, want one for each delivery", len(e.hub.Leases()))
		}
	})
}

// envelopeSchema is the schema of the envelope that af prints.
func envelopeSchema(t *testing.T) *schema.Schema {
	t.Helper()
	var out, errb bytes.Buffer
	afcmd.New(&afcmd.Config{}).Main(context.Background(), []string{"af", "--schema"}, strings.NewReader(""), &out, &errb)
	s, err := schema.Parse(out.Bytes())
	if err != nil {
		t.Fatalf("the schema of the envelope: %v", err)
	}
	return s
}

// TestPermanentRefusalsReturnBuiltEnvelopesThatValidateAgainstTheSchema_TS_13_59 verifies
// TS-13-59.
// Verifies: 13-REQ-8.3
func TestPermanentRefusalsReturnBuiltEnvelopesThatValidateAgainstTheSchema_TS_13_59(t *testing.T) {
	s := envelopeSchema(t)
	for name, tc := range map[string]struct {
		kind     jobs.Kind
		setup    func(e *env)
		input    jobs.Input
		status   string
		category string
		points   bool
	}{
		"a variable out of range": {jobs.KindFix, func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/vars/resolved", fakehub.Fault{Body: `[{"key":"AF_TIMEOUT","value":"never","origin":"workspace"}]`})
		}, issueInput("42"), "failed", "config", false},
		"a fix that is not served":   {jobs.KindFix, func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Body: carryPatchRecord(e)}) }, issueInput("42"), "failed", "unsupported", false},
		"an issue that is elsewhere": {jobs.KindTriage, func(*env) {}, issueInput(`"other/repo#3"`), "usage", "usage", false},
		"a refused PAT":              {jobs.KindFix, func(e *env) { e.subs.tokens[slug] = "not-the-pat" }, issueInput("42"), "failed", "auth", true},
		"a forbidden operation":      {jobs.KindTriage, func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme/vars/resolved", fakehub.Fault{Status: 403}) }, issueInput("42"), "failed", "auth", true},
		"an unreadable token":        {jobs.KindFix, func(e *env) { e.subs.tokenErr[slug] = store.ErrUnreadable }, issueInput("42"), "failed", "auth", true},
		"a missing subscription":     {jobs.KindTriage, func(e *env) { delete(e.subs.tokens, slug) }, issueInput("42"), "failed", "auth", true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{Branch: "main", Files: fixFiles})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "x"})
			tc.setup(e)

			res, err := e.run(e.request(tc.kind, tc.input))

			if err != nil {
				t.Fatalf("error = %v, want an envelope", err)
			}
			env := envelope(t, res)
			category, message, retryable, _ := errorOf(env)
			if env["tool"] != string(tc.kind) || env["status"] != tc.status || category != tc.category || retryable {
				t.Fatalf("tool %v, status %v, error %q %q retryable %v, want %s, %s, %s, not retryable", env["tool"], env["status"], category, message, retryable, tc.kind, tc.status, tc.category)
			}
			for _, field := range []string{"warnings", "artifacts", "side_effects", "next"} {
				if list, ok := env[field].([]any); !ok || len(list) != 0 {
					t.Errorf("%s = %v, want []", field, env[field])
				}
			}
			if usage, _ := env["usage"].(map[string]any); usage["cost_source"] != "none" {
				t.Errorf("usage = %v, want the source none", usage)
			}
			if tc.points && !strings.Contains(message, "PUT /api/v1/workspaces/acme") {
				t.Errorf("message = %q, want it to point to PUT /api/v1/workspaces/acme", message)
			}
			var doc any
			if err := json.Unmarshal(res.Envelope, &doc); err != nil {
				t.Fatal(err)
			}
			if err := schema.Validate(s, doc); err != nil {
				t.Errorf("the envelope does not validate against the schema: %v\n%s", err, res.Envelope)
			}
		})
	}

	t.Run("a push that fails after the commit", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.runner.sleep = func(context.Context, time.Duration) error { return nil }
		e.hub.Inject("POST", "/api/v1/workspaces/acme/branches/*", fakehub.Fault{Status: 503})

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil {
			t.Fatalf("error = %v, want an envelope", err)
		}
		env := envelope(t, res)
		category, message, retryable, _ := errorOf(env)
		if env["status"] != "failed" || category != "git" || retryable {
			t.Errorf("status %v, error %q %q retryable %v, want failed, git, not retryable", env["status"], category, message, retryable)
		}
		if n := len(e.hub.RequestsTo("POST", pullsPath)); n != 0 {
			t.Errorf("%d pull requests were asked for after the publication failed", n)
		}
		var doc any
		_ = json.Unmarshal(res.Envelope, &doc)
		if err := schema.Validate(s, doc); err != nil {
			t.Errorf("the envelope does not validate against the schema: %v", err)
		}
	})
}

// carryPatchRecord is the record of the workspace as hub gives it for a carry-patch workspace.
func carryPatchRecord(e *env) string {
	return `{"slug":"acme","branch":"main","workspace_mode":"carry_patch","hub_url":"` + e.hub.CloneURL(slug) + `","git_url":"` + forgeRepo + `"}`
}

// TestEachStopPointHasItsDocumentedWritesLeaseAndRelease_TS_13_60 verifies the stop points of
// TS-13-60 that the pipeline has so far: a refusal at resolve, hub not answering at resolve or at
// the session, and a session refused with 401 or 403 write nothing to the forge, take no lease
// and report nothing to hub; a clone, a lease or a box that fails as the infrastructure, and a
// credential that is refused at the clone or at the lease, release a lease that was granted.
// Verifies: 13-REQ-8.4
func TestEachStopPointHasItsDocumentedWritesLeaseAndRelease_TS_13_60(t *testing.T) {
	quiet := func(t *testing.T, e *env) {
		t.Helper()
		for _, r := range e.hub.Requests() {
			switch {
			case strings.Contains(r.Path, "/issues/") && r.Method != "GET", strings.HasSuffix(r.Path, "/pulls"), strings.HasSuffix(r.Path, "/sandboxes"),
				strings.Contains(r.Path, "/usage"), strings.Contains(r.Path, "/events"), strings.Contains(r.Path, "/complete"):
				t.Errorf("%s %s: a request that an attempt which stopped this early does not make", r.Method, r.Path)
			}
		}
	}
	early := func(setup func(e *env)) func(t *testing.T) *env {
		return func(t *testing.T) *env {
			e := newEnv(t, fakehub.Workspace{Branch: "main", Files: fixFiles})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "x"})
			setup(e)
			_, _ = e.run(e.request(jobs.KindFix, issueInput("42")))
			return e
		}
	}
	for name, run := range map[string]func(t *testing.T) *env{
		"a refusal at resolve":           early(func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Body: carryPatchRecord(e)}) }),
		"hub unavailable at resolve":     early(func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme/vars/resolved", fakehub.Fault{Status: 503}) }),
		"hub unavailable at the session": early(func(e *env) { e.hub.Inject("POST", sessionsPath, fakehub.Fault{Status: 503}) }),
		"a session refused with 401":     early(func(e *env) { e.hub.Inject("POST", sessionsPath, fakehub.Fault{Status: 401}) }),
		"a session refused with 403":     early(func(e *env) { e.hub.Inject("POST", sessionsPath, fakehub.Fault{Status: 403}) }),
	} {
		t.Run(name, func(t *testing.T) { quiet(t, run(t)) })
	}

	late := func(t *testing.T, setup func(e *env), wantLease bool) (*env, jobs.Result, error) {
		t.Helper()
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		setup(e)
		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))
		if issue, _ := e.hub.IssueOf(slug, 42); slices.Contains(issue.Labels, "af:in-progress") {
			t.Errorf("the issue still carries in-progress: %v", issue.Labels)
		}
		if leases := e.hub.Leases(); wantLease != (len(leases) == 1) || (wantLease && !leases[0].Released) {
			t.Errorf("leases = %+v, want one that was granted and released: %v", leases, wantLease)
		}
		if n := len(e.hub.RequestsTo("POST", pullsPath)); n != 0 {
			t.Errorf("%d pull requests were asked for", n)
		}
		return e, res, err
	}
	t.Run("a clone that fails as the infrastructure", func(t *testing.T) {
		e, _, err := late(t, func(e *env) { e.hub.Inject("", "/git/acme.git", fakehub.Fault{Status: 503}) }, false)
		if f := transient(err); f == nil || f.Category != "git" {
			t.Errorf("error = %v, want a transient git error", err)
		}
		if issue, _ := e.hub.IssueOf(slug, 42); len(issue.Labels) != 0 || len(issue.Comments) != 0 {
			t.Errorf("issue 42 has the labels %v and %d comments, want in-progress removed and nothing else", issue.Labels, len(issue.Comments))
		}
	})
	t.Run("a lease that fails as the infrastructure", func(t *testing.T) {
		_, _, err := late(t, func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 503}) }, false)
		if f := transient(err); f == nil || f.Category != "sandbox" {
			t.Errorf("error = %v, want a transient sandbox error", err)
		}
	})
	t.Run("a box that fails as the infrastructure", func(t *testing.T) {
		_, _, err := late(t, func(e *env) { e.hub.Gateway().RefuseBoxes("no room") }, true)
		if f := transient(err); f == nil || f.Category != "sandbox" {
			t.Errorf("error = %v, want a transient sandbox error", err)
		}
	})
	t.Run("a clone whose credential is refused", func(t *testing.T) {
		e, res, err := late(t, func(e *env) { e.hub.Inject("", "/git/acme.git", fakehub.Fault{Status: 401}) }, false)
		if category, _, _, _ := errorOf(envelope(t, res)); err != nil || category != "auth" {
			t.Errorf("result %s, error %v, want an auth envelope", res.Envelope, err)
		}
		if issue, _ := e.hub.IssueOf(slug, 42); !slices.Equal(issue.Labels, []string{"af:failed"}) || len(issue.Comments) != 1 {
			t.Errorf("issue 42 has the labels %v and %d comments, want failed and one comment", issue.Labels, len(issue.Comments))
		}
	})
	t.Run("a lease whose credential is refused", func(t *testing.T) {
		e, res, err := late(t, func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 403}) }, false)
		if category, _, _, _ := errorOf(envelope(t, res)); err != nil || category != "auth" {
			t.Errorf("result %s, error %v, want an auth envelope", res.Envelope, err)
		}
		if issue, _ := e.hub.IssueOf(slug, 42); !slices.Equal(issue.Labels, []string{"af:failed"}) || len(issue.Comments) != 1 {
			t.Errorf("issue 42 has the labels %v and %d comments, want failed and one comment", issue.Labels, len(issue.Comments))
		}
	})
}

// TestTextTitlesFilesAndContextReachTheLoopAsSpecified_TS_13_62 verifies TS-13-62: a text with a
// title goes to the loop on stdin with the title as a Markdown heading, a file is passed by the
// path under input/, and the context strings and the answers are the --context values in order,
// with no question for the strings and the question with its options for the answer, so that the
// brief holds each question with its answer and the envelope does not warn of a question that
// could not be found.
// Verifies: 13-REQ-8.5
func TestTextTitlesFilesAndContextReachTheLoopAsSpecified_TS_13_62(t *testing.T) {
	t.Run("a text with a title", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)

		res, err := e.run(e.request(jobs.KindFix, jobs.Input{Title: "Greeting is wrong", Text: "The greeting says helo."}))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if got := envelope(t, res)["input"].(map[string]any)["kind"]; got != "stdin" {
			t.Errorf("the input of the loop is %v, want stdin", got)
		}
		var created string
		for _, r := range e.hub.RequestsTo("POST", "/api/v1/workspaces/acme/issues") {
			created += string(r.Body)
		}
		if !strings.Contains(created, `# Greeting is wrong\n\nThe greeting says helo.`) {
			t.Errorf("the issue that was made does not hold the heading and the text: %s", created)
		}
	})

	t.Run("context and answers", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		var brief string
		e.sink.hook = func(typ string, data json.RawMessage) {
			if typ != "run_end" {
				return
			}
			runDir := filepath.Join(e.dir, "clone", ".agent-fox", "run", e.sink.lastHub()[1])
			text, _ := os.ReadFile(filepath.Join(runDir, "brief.md"))
			brief = string(text)
		}
		chosen := "B"
		req := e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) {
			r.Context = []string{"c1: the report is old", "c2: see the log"}
			r.Answers = []jobs.Answer{{Question: "Which greeting?", Options: []jobs.Option{{ID: "A", Label: "hello"}, {ID: "B", Label: "hi"}}, Option: &chosen, Text: "hi"}}
		})

		res, err := e.run(req)

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		for _, want := range []string{"Which greeting?", "A. hello", "B. hi", "c1: the report is old", "c2: see the log", "A person's answer", "Material from a person"} {
			if !strings.Contains(brief, want) {
				t.Errorf("the brief lacks %q:\n%s", want, brief)
			}
		}
		if strings.Contains(brief, "could not be found") || slices.Contains(codes(envelope(t, res)), "question_not_found") {
			t.Errorf("the loop looked for a question it was given: warnings %v", codes(envelope(t, res)))
		}
		if i, j, k := strings.Index(brief, "c1: the report is old"), strings.Index(brief, "c2: see the log"), strings.LastIndex(brief, "hi"); !(i < j && j < k) {
			t.Errorf("the context values are not in order: %d %d %d", i, j, k)
		}
	})
}

// TestEventsHubReferencesAndArtifactsReachTheRecorderAndTheEnvelope_TS_13_64 verifies TS-13-64
// and 13-REQ-8.8: every line of the events of the loop reaches the recorder as it was emitted,
// the worker adds its step events, the session, the run and the sandbox are told to the
// recorder, and the envelope that comes back is the one the loop wrote with the artifacts
// hub_session and sandbox added, and the report of the run is the report text of the result.
// Verifies: 13-REQ-8.7, 13-REQ-8.8
func TestEventsHubReferencesAndArtifactsReachTheRecorderAndTheEnvelope_TS_13_64(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	var snap struct{ events, envelope, report []byte }
	e.sink.hook = func(typ string, data json.RawMessage) {
		var step struct{ Name string }
		if typ != "step" || json.Unmarshal(data, &step) != nil || step.Name != "release" {
			return
		}
		runDir := filepath.Join(e.dir, "clone", ".agent-fox", "run", e.sink.lastHub()[1])
		snap.events, _ = os.ReadFile(filepath.Join(runDir, "events.jsonl"))
		snap.envelope, _ = os.ReadFile(filepath.Join(runDir, "envelope.json"))
		snap.report, _ = os.ReadFile(filepath.Join(runDir, "report.md"))
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if len(snap.events) == 0 || len(snap.envelope) == 0 {
		t.Fatal("the run directory of the loop was not there when the lease was released")
	}

	// The events of the loop, as they were written, are the events the recorder got.
	type line struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	var written []line
	for raw := range bytes.SplitSeq(bytes.TrimSpace(snap.events), []byte("\n")) {
		var l line
		if err := json.Unmarshal(raw, &l); err != nil {
			t.Fatalf("an event line: %v: %s", err, raw)
		}
		written = append(written, l)
	}
	var got []line
	var steps []string
	for _, ev := range e.sink.emitted() {
		var step struct{ Name, Source string }
		if ev.Type == "step" && json.Unmarshal(ev.Data, &step) == nil && step.Source == "worker" {
			steps = append(steps, step.Name)
			continue
		}
		got = append(got, line(ev))
	}
	if len(written) < 2 || len(got) != len(written) {
		t.Fatalf("the recorder got %d events of the loop, the loop wrote %d", len(got), len(written))
	}
	for i := range written {
		if got[i].Type != written[i].Type || !jsonEqual(got[i].Data, written[i].Data) {
			t.Errorf("event %d: the recorder got %s %s, the loop wrote %s %s", i, got[i].Type, got[i].Data, written[i].Type, written[i].Data)
		}
	}
	if written[0].Type != "run_start" {
		t.Errorf("the first event of the loop is %s", written[0].Type)
	}
	if want := []string{"resolve", "session", "clone", "lease", "run", "mirror", "release", "report"}; !slices.Equal(steps, want) {
		t.Errorf("the steps of the worker are %v, want %v", steps, want)
	}

	// The references.
	leases := e.hub.Leases()
	sessions := e.hub.Sessions()
	if len(leases) != 1 || len(sessions) != 1 {
		t.Fatalf("%d leases and %d sessions", len(leases), len(sessions))
	}
	wantHubs := [][3]string{{sessions[0].ID, sessions[0].RunID, ""}, {sessions[0].ID, sessions[0].RunID, leases[0].ID}}
	if e.sink.hubs[0] != wantHubs[0] || e.sink.hubs[len(e.sink.hubs)-1] != wantHubs[1] {
		t.Errorf("the recorder was told %v, want the session and the run once the session opened, then the sandbox", e.sink.hubs)
	}
	env := envelope(t, res)
	if env["run_id"] != sessions[0].RunID {
		t.Errorf("run_id = %v, want the one of the session %s", env["run_id"], sessions[0].RunID)
	}

	// The envelope.
	artifacts, _ := env["artifacts"].([]any)
	var have []string
	for _, a := range artifacts {
		m := a.(map[string]any)
		have = append(have, m["kind"].(string)+"="+asString(m["name"]))
	}
	for _, want := range []string{"hub_session=" + sessions[0].ID, "sandbox=" + leases[0].ID} {
		if !slices.Contains(have, want) {
			t.Errorf("artifacts = %v, want %s", have, want)
		}
	}
	strip := func(doc []byte) string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(doc, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "warnings")
		delete(m, "artifacts")
		delete(m, "side_effects")
		out, _ := json.Marshal(m)
		return string(out)
	}
	if strip(res.Envelope) != strip(snap.envelope) {
		t.Errorf("the envelope was changed beyond what was added:\n%s\nwas\n%s", res.Envelope, snap.envelope)
	}
	if res.Report == "" || res.Report != string(snap.report) {
		t.Errorf("report = %q, want the report of the run %q", res.Report, snap.report)
	}
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// TestTheLoopIsMadeOfTheSeamsOfTheAttempt pins what the loop is given: hub's forge for the
// workspace's repository, the gateway box registry of the lease with its labels, the hermetic git
// runner and the hook that publishes the branch, measuring in the box with the profile, the earlier
// questions of the answers, the model of the settings to the brain, and an environment that is
// empty.
// Verifies: 13-REQ-8.1
func TestTheLoopIsMadeOfTheSeamsOfTheAttempt(t *testing.T) {
	e := leaseEnv(t, map[string]string{"AF_SANDBOX_PROFILE": "py", "AF_MODEL": "model-1"})
	e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "x"})
	chosen := "B"
	a := e.through(t, "lease", e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) {
		r.Context = []string{"c1", "c2"}
		r.Answers = []jobs.Answer{{Question: "Which?", Options: []jobs.Option{{ID: "A", Label: "x"}, {ID: "B", Label: "y"}}, Option: &chosen, Text: "y"}}
	}))

	cfg := e.runner.loopConfig(a)

	if cfg.Boxes != a.boxes || cfg.Boxes == nil || !slices.Equal(cfg.Boxes.Names(), []string{"gateway"}) {
		t.Errorf("the boxes are %v, want the registry of the attempt with the gateway alone", cfg.Boxes)
	}
	if cfg.InBox == nil || cfg.InBox.Profile != "py" {
		t.Errorf("InBox = %+v, want measuring in the box of the profile py", cfg.InBox)
	}
	if cfg.BoxLabels["af.job"] != a.req.JobID || cfg.BoxLabels["af.attempt"] != "1" {
		t.Errorf("box labels = %v", cfg.BoxLabels)
	}
	if cfg.Git == nil || cfg.PostPush == nil || cfg.Forge == nil {
		t.Errorf("git %v, post-push %v, forge %v: want all three", cfg.Git != nil, cfg.PostPush != nil, cfg.Forge != nil)
	}
	if client, err := cfg.Forge(issuex.Options{}); err != nil || client == nil {
		t.Errorf("the forge of the loop: %v, %v", client, err)
	}
	if _, err := cfg.Forge(issuex.Options{Repo: issuex.Repo{Owner: "someone", Name: "else"}}); err == nil {
		t.Error("the forge of the loop takes a repository that is not the workspace's")
	}
	if cfg.Brain.Model != "model-1" {
		t.Errorf("the model of the brain is %q, want the one of the settings", cfg.Brain.Model)
	}
	if v, ok := cfg.Brain.Getenv("ANTHROPIC_API_KEY"); ok || v != "" {
		t.Errorf("the brain can read the environment: %q", v)
	}
	if cfg.Version != "v-test" || cfg.Commit != "c-test" {
		t.Errorf("identity %q %q", cfg.Version, cfg.Commit)
	}
	if cfg.Earlier == nil || len(cfg.Earlier.Questions) != 3 || cfg.Earlier.Questions[0] != nil || cfg.Earlier.Questions[1] != nil ||
		cfg.Earlier.Questions[2] == nil || cfg.Earlier.Questions[2].Text != "Which?" || !slices.Equal(cfg.Earlier.Questions[2].Options, []string{"x", "y"}) {
		t.Errorf("earlier questions = %+v, want none for the two strings and Which? with its options for the answer", cfg.Earlier)
	}

	args, _, refused := e.runner.loopArgs(a)
	if refused != nil {
		t.Fatal(refused.message)
	}
	want := []string{"af", "fix", "https://github.example.com/Org/Repo/issues/42", "--dir", a.cloneDir, "--sandbox", "gateway"}
	if !slices.Equal(args[:len(want)], want) {
		t.Errorf("the invocation begins %v, want %v", args[:len(want)], want)
	}
	if tail := args[len(args)-3:]; !slices.Equal(tail, []string{"--context=c1", "--context=c2", "--context=y"}) {
		t.Errorf("the invocation ends %v, want the context values in order, the answer last", tail)
	}
	if flags := strings.Join(args, " "); !strings.Contains(flags, "--model") && !strings.Contains(flags, "--attempts 1 --land pr") {
		t.Errorf("the invocation %q lacks the settings as flags", flags)
	}
}

// TestAFileWithNoNameToWriteItUnderIsRefusedAndNothingIsWritten pins the names of a file that
// cannot be written under input/.
// Verifies: 13-REQ-8.5
func TestAFileWithNoNameToWriteItUnderIsRefusedAndNothingIsWritten(t *testing.T) {
	for _, name := range []string{"", ".", "..", "dir/..", "a\x00b"} {
		e := leaseEnv(t, nil)
		a := &attempt{req: e.request(jobs.KindFix, jobs.Input{File: &jobs.File{Name: name, Content: "x"}}), input: loopInput{kind: inputFile, file: &jobs.File{Name: name, Content: "x"}}}

		_, _, refused := e.runner.loopArgs(a)

		if refused == nil || refused.category != toolio.CategoryUsage {
			t.Errorf("name %q: refusal %+v, want a usage refusal", name, refused)
		}
		if entries, _ := os.ReadDir(e.dir); len(entries) != 0 {
			t.Errorf("name %q: the attempt directory holds %d entries", name, len(entries))
		}
	}

	t.Run("a name with a path is written under its base name", func(t *testing.T) {
		e := leaseEnv(t, nil)
		file := &jobs.File{Name: "../../etc/notes.md", Content: "x"}
		a := &attempt{req: e.request(jobs.KindFix, jobs.Input{File: file}), input: loopInput{kind: inputFile, file: file}}

		args, _, refused := e.runner.loopArgs(a)

		if refused != nil || args[2] != filepath.Join(e.dir, "input", "notes.md") {
			t.Fatalf("args %v, refusal %v, want the path under input/", args, refused)
		}
	})
}

// TestOnlyTheReportOfTheRunInTheCloneIsTheReportOfTheAttempt pins that a path of the envelope
// outside the clone is not read.
// Verifies: 13-REQ-8.8
func TestOnlyTheReportOfTheRunInTheCloneIsTheReportOfTheAttempt(t *testing.T) {
	dir := t.TempDir()
	clone := filepath.Join(dir, "clone")
	if err := os.MkdirAll(filepath.Join(clone, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside, outside := filepath.Join(clone, "run", "report.md"), filepath.Join(dir, "elsewhere.md")
	for path, text := range map[string]string{inside: "# the report\n", outside: "# not the report\n"} {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := &attempt{cloneDir: clone}

	for name, tc := range map[string]struct {
		artifacts []toolio.Artifact
		want      string
	}{
		"the report in the clone":    {[]toolio.Artifact{{Kind: "branch", Name: "b"}, {Kind: "report", Path: inside}}, "# the report\n"},
		"a report outside the clone": {[]toolio.Artifact{{Kind: "report", Path: outside}}, ""},
		"a report that climbs out":   {[]toolio.Artifact{{Kind: "report", Path: filepath.Join(clone, "..", "elsewhere.md")}}, ""},
		"no report":                  {[]toolio.Artifact{{Kind: "branch", Name: "b"}}, ""},
		"a report that is not there": {[]toolio.Artifact{{Kind: "report", Path: filepath.Join(clone, "gone.md")}}, ""},
		"a report with no path":      {[]toolio.Artifact{{Kind: "report"}}, ""},
	} {
		if got := a.reportOf(toolio.Envelope{Artifacts: tc.artifacts}); got != tc.want {
			t.Errorf("%s: report = %q, want %q", name, got, tc.want)
		}
	}
}

// TestTheLoopIsRunByAShellOfTheAttempt pins the options of the shell the loop runs under.
// Verifies: 13-REQ-8.1
func TestTheLoopIsRunByAShellOfTheAttempt(t *testing.T) {
	e := leaseEnv(t, nil)
	e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "x"})
	a := e.through(t, "lease", e.request(jobs.KindFix, issueInput("42")))

	app := e.runner.loopApp(a)

	if !app.NoSignals || app.RunID != a.runID || app.RunID == "" || app.Target != a.repo || !app.Target.Valid() || app.EventSink == nil || app.NewForge == nil {
		t.Errorf("no signals %v, run id %q (the session's is %q), target %v, sink %v, forge %v", app.NoSignals, app.RunID, a.runID, app.Target, app.EventSink != nil, app.NewForge != nil)
	}
}

// TestALoopThatReturnsAfterTheAttemptWasStoppedIsItsContextsEnd pins that an attempt that was
// stopped is the end of its context, and not the envelope the loop wrote when it was.
// Verifies: 13-REQ-8.2
func TestALoopThatReturnsAfterTheAttemptWasStoppedIsItsContextsEnd(t *testing.T) {
	e := leaseEnv(t, nil)
	a := &attempt{req: e.fixRequest(), maxAttempts: 2}
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(jobs.ErrCancelled)

	out := e.runner.loopOutcome(ctx, a, fakerunner.Env(toolio.StatusFailed, fakerunner.WithError("cancelled", "stopped", false)))

	if !errors.Is(out.err, jobs.ErrCancelled) || len(out.result.Envelope) == 0 {
		t.Errorf("error %v, envelope %q, want the cancellation, with the envelope the loop wrote", out.err, out.result.Envelope)
	}
	if gone := e.runner.loopOutcome(ctx, a, []byte("not an envelope")); !errors.Is(gone.err, jobs.ErrCancelled) || len(gone.result.Envelope) != 0 {
		t.Errorf("a stopped loop that wrote no envelope: %v, %q, want the cancellation alone", gone.err, gone.result.Envelope)
	}
	if bad := e.runner.loopOutcome(context.Background(), a, []byte("not an envelope")); bad.err == nil || jobs.IsTransient(bad.err) {
		t.Errorf("a loop that wrote no envelope: %v, want an error that is not transient", bad.err)
	}
}

// TestTheReportOfTheRunIsCutAtAMegabyte pins the bound of the report text.
// Verifies: 13-REQ-8.8
func TestTheReportOfTheRunIsCutAtAMegabyte(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 2*maxReport), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &attempt{cloneDir: dir}

	if got := a.reportOf(toolio.Envelope{Artifacts: []toolio.Artifact{{Kind: "report", Path: path}}}); len(got) != maxReport {
		t.Errorf("the report is %d bytes, want %d", len(got), maxReport)
	}
}

// TestTheBriefOfATriageFramesContextAsMaterialAndAnswersAsAnswers pins, with the real loop, that
// the strings of context a job carries are material from a person to a triage and only the
// answers are answers.
// Verifies: 13-REQ-8.5
func TestTheBriefOfATriageFramesContextAsMaterialAndAnswersAsAnswers(t *testing.T) {
	e := loopEnv(t, triageFiles, nil, nil, submitStep(probableDiagnosis))
	var brief string
	e.sink.hook = func(typ string, _ json.RawMessage) {
		if typ == "run_end" {
			text, _ := os.ReadFile(filepath.Join(e.dir, "clone", ".agent-fox", "run", e.sink.lastHub()[1], "brief.md"))
			brief = string(text)
		}
	}
	chosen := "B"
	req := e.request(jobs.KindTriage, issueInput("42"), func(r *jobs.Request) {
		r.Context = []string{"c1: the report is old"}
		r.Answers = []jobs.Answer{{Question: "Which greeting?", Options: []jobs.Option{{ID: "A", Label: "hello"}, {ID: "B", Label: "hi"}}, Option: &chosen, Text: "hi"}}
	})

	res, err := e.run(req)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	for _, want := range []string{"Material from a person (1 of 2)", "c1: the report is old", "A person's answer (2 of 2)", "Which greeting?", "B. hi"} {
		if !strings.Contains(brief, want) {
			t.Errorf("the brief lacks %q:\n%s", want, brief)
		}
	}
}
