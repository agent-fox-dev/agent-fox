package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const sandboxesPath = "/api/v1/workspaces/acme/sandboxes"

// leaseEnv is an environment whose workspace has the variables and a repository to clone.
func leaseEnv(t *testing.T, vars map[string]string) *env {
	t.Helper()
	return newEnv(t, fakehub.Workspace{Branch: "main", Files: repoFiles, Vars: vars})
}

// ended is an outcome with a sealed envelope of the tool of the attempt: done, or a failure.
func ended(a *attempt, failure *toolio.Failure) *outcome {
	env := toolio.NewEnvelope(string(a.req.Kind))
	env.Status, env.Result = toolio.StatusDone, map[string]any{"outcome": "landed"}
	if failure != nil {
		env.Fail(failure)
	}
	env.Seal(a.started, time.Now())
	data, err := env.Marshal()
	if err != nil {
		panic(err)
	}
	return &outcome{result: jobs.Result{Envelope: data, MaxAttempts: a.maxAttempts}}
}

// inBox is the step of a test that stands for the loop: it opens the box of the attempt through
// its registry, as the loop does, runs body in it and closes it, and ends the attempt as body
// says. A box that cannot be opened is a transient sandbox failure, as the loop's is.
func (e *env) inBox(body func(ctx context.Context, a *attempt) *outcome) func(ctx context.Context, a *attempt) *outcome {
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

// succeeds is the body of a loop that comes to a done envelope.
func succeeds(_ context.Context, a *attempt) *outcome { return ended(a, nil) }

// TestTheLeaseIsRequestedWithPurposeLabelsAndAProfileOnlyWhenSetAndTheSandboxIsReported_TS_13_41
// verifies TS-13-41: after the clone, one request {purpose, labels: {job, attempt}} with a profile
// only when AF_SANDBOX_PROFILE is set, and the recorder is told the sandbox of the lease.
// Verifies: 13-REQ-6.1
func TestTheLeaseIsRequestedWithPurposeLabelsAndAProfileOnlyWhenSetAndTheSandboxIsReported_TS_13_41(t *testing.T) {
	for name, tc := range map[string]struct {
		kind    jobs.Kind
		vars    map[string]string
		profile string
	}{
		"a fix with a profile":    {jobs.KindFix, map[string]string{"AF_SANDBOX_PROFILE": "py"}, "py"},
		"a fix with none":         {jobs.KindFix, nil, ""},
		"a triage with a profile": {jobs.KindTriage, map[string]string{"AF_SANDBOX_PROFILE": "go"}, "go"},
		"a triage with none":      {jobs.KindTriage, map[string]string{"AF_SANDBOX_PROFILE": ""}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := leaseEnv(t, tc.vars)
			req := e.request(tc.kind, jobs.Input{Text: "The greeting says helo."}, func(r *jobs.Request) { r.Attempt = 3 })

			a := e.through(t, "lease", req)

			requests := e.hub.RequestsTo("POST", sandboxesPath)
			if len(requests) != 1 {
				t.Fatalf("%d lease requests, want 1", len(requests))
			}
			var body map[string]any
			if err := json.Unmarshal(requests[0].Body, &body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"purpose": string(tc.kind), "labels": map[string]any{"job": req.JobID, "attempt": "3"}}
			if tc.profile != "" {
				want["profile"] = tc.profile
			}
			if got, _ := json.Marshal(body); !bytes.Equal(got, mustJSON(want)) {
				t.Errorf("the request body = %s, want %s", got, mustJSON(want))
			}
			leases := e.hub.Leases()
			if len(leases) != 1 || a.lease == nil || a.lease.ID != leases[0].ID {
				t.Fatalf("leases = %+v, the attempt holds %+v", leases, a.lease)
			}
			if got := e.sink.lastHub(); got != [3]string{a.sessionID, a.runID, leases[0].ID} || got[0] == "" || got[1] == "" {
				t.Errorf("the recorder was told %v, want the session, the run and the sandbox %s", got, leases[0].ID)
			}
		})
	}

	t.Run("after the session and the clone, and not after a refusal", func(t *testing.T) {
		e := leaseEnv(t, nil)
		e.through(t, "lease", e.fixRequest())
		var session, lease int
		for _, r := range e.hub.Requests() {
			switch {
			case r.Method == "POST" && r.Path == "/api/v1/sessions":
				session = r.Seq
			case r.Method == "POST" && r.Path == sandboxesPath:
				lease = r.Seq
			}
		}
		var cloned int
		for _, g := range e.hub.GitRequests() {
			cloned = max(cloned, g.Seq)
		}
		if session == 0 || !(session < cloned && cloned < lease) {
			t.Errorf("session %d, clone %d, lease %d: want the lease last", session, cloned, lease)
		}

		for name, tc := range map[string]struct {
			vars map[string]string
			mode string
		}{
			"a variable out of range":   {map[string]string{"AF_TIMEOUT": "never"}, ""},
			"a carry-patch workspace":   {nil, "carry_patch"},
			"a landing as a patch":      {map[string]string{"AF_FIX_LAND": "patch"}, ""},
			"a session that is refused": {nil, "session"},
		} {
			e := newEnv(t, fakehub.Workspace{Branch: "main", Files: repoFiles, Vars: tc.vars, Mode: strings.TrimPrefix(tc.mode, "session")})
			if tc.mode == "session" {
				e.hub.Inject("POST", "/api/v1/sessions", fakehub.Fault{Status: 503})
			}
			_, _ = e.run(e.fixRequest())
			if n := len(e.hub.RequestsTo("POST", sandboxesPath)); n != 0 {
				t.Errorf("%s: %d leases were requested, want none", name, n)
			}
		}
	})
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

// TestALeaseRefusedWith401Or403EndsAsAnAuthFailureEnvelope_TS_13_42 verifies TS-13-42.
// Verifies: 13-REQ-6.2
func TestALeaseRefusedWith401Or403EndsAsAnAuthFailureEnvelope_TS_13_42(t *testing.T) {
	for _, status := range []int{401, 403} {
		e := leaseEnv(t, nil)
		e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: status})

		res, err := e.run(e.fixRequest())

		if err != nil {
			t.Fatalf("%d: error = %v, want an envelope", status, err)
		}
		env := envelope(t, res)
		category, message, retryable, _ := errorOf(env)
		if env["status"] != "failed" || category != toolio.CategoryAuth || retryable || !strings.Contains(message, "PUT /api/v1/workspaces/acme") {
			t.Errorf("%d: status %v, error %q %q retryable %v, want failed, auth, not retryable, pointing to PUT /api/v1/workspaces/acme", status, env["status"], category, message, retryable)
		}
		if strings.Contains(message, pat) {
			t.Errorf("%d: the message holds the token: %q", status, message)
		}
		if len(e.hub.Leases()) != 0 {
			t.Errorf("%d: a lease was granted", status)
		}
	}
}

// TestOtherLeaseOrBoxProblemsAreTransientSandboxErrors_TS_13_43 verifies TS-13-43: a lease that
// hub answers with 503, refuses for capacity with its reason, does not answer in time, or grants
// with less than two minutes left, and a gateway that is gone or refuses to make the box, end the
// attempt with a transient error of category sandbox that can be tried again, with hub's reason
// when it gave one, and no envelope; a lease that was granted is released.
// Verifies: 13-REQ-6.3
func TestOtherLeaseOrBoxProblemsAreTransientSandboxErrors_TS_13_43(t *testing.T) {
	const reason = "no capacity: 4 of 4 sandboxes are in use"
	for name, tc := range map[string]struct {
		setup    func(e *env)
		leased   bool
		contains string
	}{
		"a 503": {setup: func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 503}) }, contains: "503"},
		"a capacity rejection with a reason": {setup: func(e *env) {
			e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 409, Body: `{"error":{"code":"capacity","message":"` + reason + `"}}`})
		}, contains: reason},
		"a lease timeout": {setup: func(e *env) {
			e.runner.cfg.Hub.LongTimeout = 300 * time.Millisecond
			e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Delay: 10 * time.Second})
		}},
		"a lease that hub does not answer": {setup: func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Hangup: true}) }},
		"a lease expiring in 90 seconds":   {setup: func(e *env) { e.hub.SetLeaseTTL(90 * time.Second) }, leased: true, contains: "2 minutes"},
		"an unreachable gateway":           {setup: func(e *env) { e.hub.Gateway().Stop() }, leased: true},
		"a gateway refusing to make a box": {setup: func(e *env) { e.hub.Gateway().RefuseBoxes("no room for another box") }, leased: true, contains: "no room for another box"},
	} {
		t.Run(name, func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...) // the gateway cases are the real loop's
			tc.setup(e)

			start := time.Now()
			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			f := transient(err)
			if f == nil || f.Category != toolio.CategorySandbox || !f.Retryable {
				t.Fatalf("error = %v, want a transient error carrying a failure of category sandbox that is retryable", err)
			}
			if len(res.Envelope) != 0 {
				t.Errorf("the result holds an envelope: %s", res.Envelope)
			}
			if tc.contains != "" && !strings.Contains(f.Message, tc.contains) {
				t.Errorf("message = %q, want it to hold %q", f.Message, tc.contains)
			}
			for _, secret := range []string{pat, "sbl_"} {
				if strings.Contains(f.Message, secret) {
					t.Errorf("the message holds a credential (%s): %q", secret, f.Message)
				}
			}
			leases := e.hub.Leases()
			if tc.leased != (len(leases) == 1) {
				t.Fatalf("%d leases were granted, want %v", len(leases), tc.leased)
			}
			if tc.leased && !leases[0].Released {
				t.Error("a lease that was granted was not released")
			}
			if time.Since(start) > 20*time.Second {
				t.Errorf("the attempt took %v to give up", time.Since(start))
			}
		})
	}
}

// TestTheTimeoutIsCutToEndTwoMinutesBeforeTheLeaseExpires_TS_13_46 verifies TS-13-46: AF_TIMEOUT
// 45m and a lease of 20 minutes give the loop a timeout of about 18 minutes and the envelope the
// warning lease_shortened_timeout; a lease of 2 hours leaves 45m and no warning.
// Verifies: 13-REQ-6.6
func TestTheTimeoutIsCutToEndTwoMinutesBeforeTheLeaseExpires_TS_13_46(t *testing.T) {
	for name, tc := range map[string]struct {
		ttl     time.Duration
		vars    map[string]string
		cut     bool
		low, hi time.Duration
	}{
		"a lease of 20 minutes":             {20 * time.Minute, map[string]string{"AF_TIMEOUT": "45m"}, true, 17*time.Minute + 50*time.Second, 18 * time.Minute},
		"a lease of 2 hours":                {2 * time.Hour, map[string]string{"AF_TIMEOUT": "45m"}, false, 45 * time.Minute, 45 * time.Minute},
		"a timeout that fits":               {20 * time.Minute, map[string]string{"AF_TIMEOUT": "10m"}, false, 10 * time.Minute, 10 * time.Minute},
		"the default timeout of a fix":      {30 * time.Minute, nil, true, 27*time.Minute + 50*time.Second, 28 * time.Minute},
		"a lease just over the two minutes": {2*time.Minute + 30*time.Second, map[string]string{"AF_TIMEOUT": "45m"}, true, 20 * time.Second, 30 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			e := leaseEnv(t, tc.vars)
			e.hub.SetLeaseTTL(tc.ttl)
			e.runner.loop = e.inBox(func(_ context.Context, a *attempt) *outcome {
				if got := a.settings.Timeout; got < tc.low || got > tc.hi || got != got.Truncate(time.Second) {
					t.Errorf("the timeout of the loop is %v, want a whole number of seconds from %v to %v", got, tc.low, tc.hi)
				}
				flags := strings.Join(a.settings.flags(jobs.KindFix), " ")
				if !strings.Contains(flags, "--timeout "+a.settings.Timeout.String()) {
					t.Errorf("the flags of the loop are %q, want the timeout of the settings", flags)
				}
				return ended(a, nil)
			})

			res, err := e.run(e.fixRequest())

			if err != nil {
				t.Fatal(err)
			}
			warned := false
			warnings, _ := envelope(t, res)["warnings"].([]any)
			for _, w := range warnings {
				if w.(map[string]any)["code"] == "lease_shortened_timeout" {
					warned = true
				}
			}
			if warned != tc.cut {
				t.Errorf("warnings = %v, want lease_shortened_timeout: %v", warnings, tc.cut)
			}
		})
	}
}

// TestAGrantedLeaseIsReleasedOnceAfterTheBoxClosesOnEveryPath_TS_13_47 verifies TS-13-47: the
// attempts that end in success, failure, a transient error, a cancellation, a shutdown and a
// panic each make exactly one release of their lease, after the box was closed, and a
// cancellation does not reach the release.
// Verifies: 13-REQ-6.7
func TestAGrantedLeaseIsReleasedOnceAfterTheBoxClosesOnEveryPath_TS_13_47(t *testing.T) {
	for name, tc := range map[string]struct {
		body   func(cancel context.CancelCauseFunc) func(ctx context.Context, a *attempt) *outcome
		check  func(t *testing.T, res jobs.Result, err error)
		panics bool
	}{
		"success": {
			body: func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome { return succeeds },
			check: func(t *testing.T, res jobs.Result, err error) {
				if err != nil || envelope(t, res)["status"] != "done" {
					t.Errorf("result %s, error %v", res.Envelope, err)
				}
			},
		},
		"failure": {
			body: func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
				return func(_ context.Context, a *attempt) *outcome {
					return ended(a, &toolio.Failure{Category: toolio.CategoryBrain, Message: "the brain failed"})
				}
			},
			check: func(t *testing.T, res jobs.Result, err error) {
				if err != nil || envelope(t, res)["status"] != "failed" {
					t.Errorf("result %s, error %v", res.Envelope, err)
				}
			},
		},
		"transient error": {
			body: func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
				return func(context.Context, *attempt) *outcome {
					return transientFailure(toolio.CategorySandbox, "the box was lost")
				}
			},
			check: func(t *testing.T, _ jobs.Result, err error) {
				if !jobs.IsTransient(err) {
					t.Errorf("error = %v, want a transient error", err)
				}
			},
		},
		"cancel": {
			body: func(cancel context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
				return func(ctx context.Context, _ *attempt) *outcome {
					cancel(jobs.ErrCancelled)
					return &outcome{err: context.Cause(ctx)}
				}
			},
			check: func(t *testing.T, _ jobs.Result, err error) {
				if !errors.Is(err, jobs.ErrCancelled) {
					t.Errorf("error = %v, want the cancellation", err)
				}
			},
		},
		"shutdown": {
			body: func(cancel context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
				return func(ctx context.Context, _ *attempt) *outcome {
					cancel(jobs.ErrShutdown)
					return &outcome{err: context.Cause(ctx)}
				}
			},
			check: func(t *testing.T, _ jobs.Result, err error) {
				if !errors.Is(err, jobs.ErrShutdown) {
					t.Errorf("error = %v, want the shutdown", err)
				}
			},
		},
		"panic": {
			body: func(context.CancelCauseFunc) func(context.Context, *attempt) *outcome {
				return func(context.Context, *attempt) *outcome { panic("the loop panicked") }
			},
			panics: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := leaseEnv(t, nil)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			e.runner.loop = e.inBox(tc.body(cancel))

			var res jobs.Result
			var err error
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				res, err = e.runner.Run(ctx, e.fixRequest())
			}()

			if tc.panics != (recovered != nil) {
				t.Fatalf("recovered %v, want a panic: %v", recovered, tc.panics)
			}
			if tc.check != nil {
				tc.check(t, res, err)
			}
			leases := e.hub.Leases()
			if len(leases) != 1 || !leases[0].Released {
				t.Fatalf("leases = %+v, want one that was released", leases)
			}
			if n := len(e.hub.RequestsTo("DELETE", sandboxesPath+"/"+leases[0].ID)); n != 1 {
				t.Errorf("%d releases were made, want exactly one", n)
			}
			closed := e.hub.Gateway().Closed()
			if len(closed) != 1 {
				t.Fatalf("%d boxes were closed, want the one the loop opened", len(closed))
			}
			if leases[0].ReleasedSeq <= closed[0].Seq {
				t.Errorf("the lease was released at %d, before the box was closed at %d", leases[0].ReleasedSeq, closed[0].Seq)
			}
		})
	}

	t.Run("a lease that cannot be used is released too, and the release is bounded to 30 seconds", func(t *testing.T) {
		e := leaseEnv(t, nil)
		if got := e.runner.releaseBound(); got != 30*time.Second {
			t.Errorf("the release is bounded to %v, want 30s", got)
		}
		e.hub.SetLeaseTTL(90 * time.Second)
		e.runner.releaseTimeout = 300 * time.Millisecond
		e.hub.Inject("DELETE", sandboxesPath+"/*", fakehub.Fault{Delay: 10 * time.Second})

		start := time.Now()
		_, err := e.run(e.fixRequest())

		if f := transient(err); f == nil || f.Category != toolio.CategorySandbox {
			t.Errorf("error = %v, want a transient sandbox error", err)
		}
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("the attempt took %v: the release was not bounded", took)
		}
		leases := e.hub.Leases()
		if len(leases) != 1 {
			t.Fatalf("%d leases were granted, want one", len(leases))
		}
		if n := len(e.hub.RequestsTo("DELETE", sandboxesPath+"/"+leases[0].ID)); n != 1 {
			t.Errorf("%d releases were made, want one", n)
		}
	})
}

// TestAFailedReleaseAddsAWarningWithoutChangingTheStatus_TS_13_48 verifies TS-13-48: with hub
// answering the release with 500, a successful envelope keeps its status and gains the warning
// lease_release_failed, and a transient error is returned as it is with the failure logged.
// Verifies: 13-REQ-6.8
func TestAFailedReleaseAddsAWarningWithoutChangingTheStatus_TS_13_48(t *testing.T) {
	t.Run("a successful attempt", func(t *testing.T) {
		e := leaseEnv(t, nil)
		e.hub.SetLeaseTTL(3 * time.Hour) // the default timeout of a fix fits, so that the release is the only warning
		e.hub.Inject("DELETE", sandboxesPath+"/*", fakehub.Fault{Status: 500})
		var sealed []byte
		e.runner.loop = e.inBox(func(_ context.Context, a *attempt) *outcome {
			out := ended(a, nil)
			sealed = out.result.Envelope
			return out
		})

		res, err := e.run(e.fixRequest())

		if err != nil {
			t.Fatal(err)
		}
		env := envelope(t, res)
		var was map[string]any
		if err := json.Unmarshal(sealed, &was); err != nil {
			t.Fatal(err)
		}
		if env["status"] != "done" || env["exit_code"] != float64(0) || env["error"] != nil {
			t.Errorf("status %v, exit code %v, error %v: the status was changed", env["status"], env["exit_code"], env["error"])
		}
		warnings, _ := env["warnings"].([]any)
		if len(warnings) != 1 || warnings[0].(map[string]any)["code"] != "lease_release_failed" {
			t.Fatalf("warnings = %v, want lease_release_failed", warnings)
		}
		if msg := warnings[0].(map[string]any)["message"].(string); !strings.Contains(msg, e.hub.Leases()[0].ID) || strings.Contains(msg, pat) {
			t.Errorf("message = %q, want the lease and not the token", msg)
		}
		for _, field := range []string{"warnings", "artifacts"} { // the lists the worker adds to
			delete(env, field)
			delete(was, field)
		}
		if !bytes.Equal(mustJSON(env), mustJSON(was)) {
			t.Errorf("the envelope was changed beyond its warnings:\n%s\nwas\n%s", mustJSON(env), mustJSON(was))
		}
	})

	t.Run("a transient error", func(t *testing.T) {
		e := leaseEnv(t, nil)
		e.hub.Inject("DELETE", sandboxesPath+"/*", fakehub.Fault{Status: 500})
		var logged bytes.Buffer
		e.runner.cfg.Logger = slog.New(slog.NewTextHandler(&logged, nil))
		e.runner.loop = e.inBox(func(context.Context, *attempt) *outcome {
			return transientFailure(toolio.CategorySandbox, "the box was lost")
		})

		res, err := e.run(e.fixRequest())

		if f := transient(err); f == nil || f.Message != "the box was lost" || len(res.Envelope) != 0 {
			t.Errorf("result %q, error %v, want the transient error as it was", res.Envelope, err)
		}
		if leases := e.hub.Leases(); len(leases) != 1 || !strings.Contains(logged.String(), "lease") || !strings.Contains(logged.String(), leases[0].ID) {
			t.Errorf("log = %q, want the failure to release the lease", logged.String())
		}
		if strings.Contains(logged.String(), pat) {
			t.Errorf("the log holds the token: %q", logged.String())
		}
	})

	t.Run("an attempt that ended before the lease has nothing to release", func(t *testing.T) {
		e := leaseEnv(t, map[string]string{"AF_TIMEOUT": "never"})
		e.hub.Inject("DELETE", sandboxesPath+"/*", fakehub.Fault{Status: 500})

		res, err := e.run(e.fixRequest())

		warnings, _ := envelope(t, res)["warnings"].([]any)
		if err != nil || len(warnings) != 0 {
			t.Errorf("error %v, warnings %v, want an envelope with none", err, warnings)
		}
	})
}

// TestAnAttemptThatIsStoppedWhileItWaitsForTheLeaseIsStoppedAndNotTransient pins that a context
// that ended is its own end, and not a lease that hub did not grant.
// Verifies: 13-REQ-6.3
func TestAnAttemptThatIsStoppedWhileItWaitsForTheLeaseIsStoppedAndNotTransient(t *testing.T) {
	e := leaseEnv(t, nil)
	e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Delay: 30 * time.Second})
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(400*time.Millisecond, func() { cancel(jobs.ErrCancelled) })

	res, err := e.runner.Run(ctx, e.fixRequest())

	if !errors.Is(err, jobs.ErrCancelled) || jobs.IsTransient(err) || len(res.Envelope) != 0 {
		t.Errorf("result %q, error %v, want the cancellation and no envelope", res.Envelope, err)
	}
}

// TestTheBoxOfTheLoopIsMadeWithTheLabelsOfTheJobAndTheAttempt checks the wiring of the labels
// that the registry of the attempt gives the box: the gateway records af.run_id, af.tool, af.job
// and af.attempt, and no environment.
// Verifies: 13-REQ-6.4
func TestTheBoxOfTheLoopIsMadeWithTheLabelsOfTheJobAndTheAttempt(t *testing.T) {
	e := leaseEnv(t, nil)
	e.runner.loop = e.inBox(succeeds)
	req := e.fixRequest()
	req.Attempt = 2

	if _, err := e.run(req); err != nil {
		t.Fatal(err)
	}

	created := e.hub.Gateway().Created()
	if len(created) != 1 {
		t.Fatalf("%d boxes were created, want 1", len(created))
	}
	labels := created[0].Spec.Labels
	if labels["af.job"] != req.JobID || labels["af.attempt"] != "2" || labels["af.tool"] != "fix" || !strings.Contains(labels["af.run_id"], "_") || len(created[0].Spec.Env) != 0 {
		t.Errorf("labels %v, env %v, want af.job, af.attempt 2, af.tool fix, af.run_id and no environment", labels, created[0].Spec.Env)
	}
}
