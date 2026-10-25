package worker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestLease_IsRequestedWithPurposeLabelsAndAProfileOnlyWhenSet(t *testing.T) {
	for _, tc := range []struct {
		kind    jobs.Kind
		vars    map[string]string
		profile string
	}{
		{jobs.KindFix, map[string]string{"AF_SANDBOX_PROFILE": "py"}, "py"},
		{jobs.KindFix, nil, ""},
		{jobs.KindTriage, map[string]string{"AF_SANDBOX_PROFILE": ""}, ""},
	} {
		e := varsEnv(t, tc.vars)
		req := e.request(tc.kind, jobs.Input{Text: "The greeting says helo."}, func(r *jobs.Request) { r.Attempt = 3 })

		a := e.through(t, "lease", req)

		requests := e.hub.RequestsTo("POST", sandboxesPath)
		want := map[string]any{"purpose": string(tc.kind), "labels": map[string]any{"job": jobID, "attempt": "3"}}
		if tc.profile != "" {
			want["profile"] = tc.profile
		}
		if len(requests) != 1 || string(requests[0].Body) != string(testutil.JSON(t, want)) {
			t.Fatalf("%s %v: the lease requests are %v, want one of %s", tc.kind, tc.vars, requests, testutil.JSON(t, want))
		}
		leases := e.hub.Leases()
		if a.lease == nil || a.lease.ID != leases[0].ID || e.sink.lastHub() != [3]string{a.sessionID, a.runID, leases[0].ID} {
			t.Errorf("the attempt holds %+v and the recorder was told %v, want the lease %s", a.lease, e.sink.lastHub(), leases[0].ID)
		}
	}
}

// A lease with less than two minutes left is one hub did not grant, and is released.
func TestLease_AnyOtherFailureIsATransientSandboxError(t *testing.T) {
	const reason = "no capacity: 4 of 4 sandboxes are in use"
	for name, tc := range map[string]struct {
		setup  func(e *env)
		leased bool
		says   string
	}{
		"a 503":               {func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 503}) }, false, "503"},
		"a hangup":            {func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Hangup: true}) }, false, "did not lease"},
		"a profile not found": {func(e *env) { e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 404}) }, false, "does not find the workspace or the profile"},
		"a capacity rejection": {func(e *env) {
			e.hub.Inject("POST", sandboxesPath, fakehub.Fault{Status: 409, Body: `{"error":{"code":409,"message":"` + reason + `","error_type":"sandbox_capacity"}}`})
		}, false, reason},
		"a lease of 90 seconds": {func(e *env) { e.hub.SetLeaseTTL(90 * time.Second) }, true, "less than 2 minutes"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			tc.setup(e)
			e.endsWith("done")

			res, err := e.run(e.fix())

			f := transient(err)
			if f == nil || f.Category != toolio.CategorySandbox || !f.Retryable || len(res.Envelope) != 0 || !strings.Contains(f.Message, tc.says) {
				t.Fatalf("result %s, error %v, want a transient sandbox error that says %q", res.Envelope, err, tc.says)
			}
			if strings.Contains(f.Message, pat) || strings.Contains(f.Message, "sbl_") {
				t.Errorf("the message holds a credential: %q", f.Message)
			}
			if leases := e.hub.Leases(); tc.leased != (len(leases) == 1) || (tc.leased && !leases[0].Released) {
				t.Errorf("leases = %+v, want one granted and released: %v", leases, tc.leased)
			}
		})
	}
}

func TestLease_CutsTheTimeoutToEndTwoMinutesBeforeTheLeaseExpires(t *testing.T) {
	for name, tc := range map[string]struct {
		ttl     time.Duration
		vars    map[string]string
		low, hi time.Duration
		cut     bool
	}{
		"a lease of 20 minutes":         {20 * time.Minute, map[string]string{"AF_TIMEOUT": "45m"}, 17*time.Minute + 50*time.Second, 18 * time.Minute, true},
		"a lease of 2 hours":            {2 * time.Hour, map[string]string{"AF_TIMEOUT": "45m"}, 45 * time.Minute, 45 * time.Minute, false},
		"a timeout that fits":           {20 * time.Minute, map[string]string{"AF_TIMEOUT": "10m"}, 10 * time.Minute, 10 * time.Minute, false},
		"the default timeout of a fix":  {30 * time.Minute, nil, 27*time.Minute + 50*time.Second, 28 * time.Minute, true},
		"a lease just over two minutes": {2*time.Minute + 30*time.Second, map[string]string{"AF_TIMEOUT": "45m"}, 20 * time.Second, 30 * time.Second, true},
	} {
		t.Run(name, func(t *testing.T) {
			e := varsEnv(t, tc.vars)
			e.hub.SetLeaseTTL(tc.ttl)
			var timeout time.Duration
			e.runner.loop = func(_ context.Context, a *attempt) *outcome {
				call, _ := e.runner.loopCall(a)
				timeout = call.Timeout
				return ended(a, toolio.StatusDone)
			}

			res, err := e.run(e.fix())

			if err != nil || timeout < tc.low || timeout > tc.hi || timeout != timeout.Truncate(time.Second) {
				t.Fatalf("error %v, the loop was given %v, want whole seconds from %v to %v", err, timeout, tc.low, tc.hi)
			}
			if warned := slices.Contains(codes(envelope(t, res)), WarnLeaseShortenedTimeout); warned != tc.cut {
				t.Errorf("warnings = %v", codes(envelope(t, res)))
			}
		})
	}
}

func TestLease_IsReleasedOnceAfterTheBoxClosesHoweverTheAttemptEnds(t *testing.T) {
	ok := func(res jobs.Result, err error) bool { return err == nil && len(res.Envelope) > 0 }
	for name, tc := range map[string]struct {
		body  loopOf
		check func(res jobs.Result, err error) bool
	}{
		"done":              {always(succeeds), ok},
		"a transient error": {always(boxLost), func(_ jobs.Result, err error) bool { return jobs.IsTransient(err) }},
		"a cancel":          {stopping(jobs.ErrCancelled), ok},
		"a shutdown":        {stopping(jobs.ErrShutdown), func(_ jobs.Result, err error) bool { return errors.Is(err, jobs.ErrShutdown) }},
		"a panic":           {always(func(context.Context, *attempt) *outcome { panic("the loop panicked") }), nil},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			e.runner.loop = inBox(tc.body(cancel))

			var res jobs.Result
			var err error
			recovered := func() (v any) {
				defer func() { v = recover() }()
				res, err = e.runner.Run(ctx, e.fix())
				return nil
			}()

			if (tc.check == nil) != (recovered != nil) || (tc.check != nil && !tc.check(res, err)) {
				t.Fatalf("result %s, error %v, panic %v", res.Envelope, err, recovered)
			}
			leases, closed := e.hub.Leases(), e.hub.Gateway().Closed()
			if len(leases) != 1 || len(closed) != 1 || len(e.hub.RequestsTo("DELETE", sandboxesPath+"/"+leases[0].ID)) != 1 {
				t.Fatalf("leases %+v, %d boxes closed, want one lease released once and one box closed", leases, len(closed))
			}
			if leases[0].ReleasedSeq <= closed[0].Seq {
				t.Errorf("the lease was released at %d, before the box was closed at %d", leases[0].ReleasedSeq, closed[0].Seq)
			}
		})
	}
}

func TestReleaseBoundWithin_IsWhatTheEndOfTheAttemptHasLeft(t *testing.T) {
	for left, want := range map[time.Duration][2]time.Duration{0: {defaultReleaseTimeout, defaultReleaseTimeout}, 10 * time.Second: {9 * time.Second, 10 * time.Second},
		5 * time.Minute: {defaultReleaseTimeout, defaultReleaseTimeout}, time.Millisecond: {minRelease, minRelease}} {
		ctx, cancel := context.WithTimeout(context.Background(), left)
		if left == 0 {
			ctx = context.Background() // no deadline
		}
		if got := New(Config{}).releaseBoundWithin(ctx); got < want[0] || got > want[1] {
			t.Errorf("with %v left the bound is %v, want %v", left, got, want)
		}
		cancel()
	}
}

// With no envelope to carry the warning, the failure is logged, once.
func TestLease_AFailedReleaseIsAWarningAndChangesNothingElse(t *testing.T) {
	t.Run("an envelope", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{})
		e.hub.Inject("DELETE", sandboxesPath+"/*", fakehub.Fault{Status: 500})
		var sealed []byte
		e.runner.loop = func(_ context.Context, a *attempt) *outcome {
			out := ended(a, toolio.StatusDone)
			sealed = out.result.Envelope
			return out
		}

		res, err := e.run(e.fix())

		env, was := envelope(t, res), testutil.Decode[map[string]any](t, sealed)
		warnings, _ := env["warnings"].([]any)
		if err != nil || len(warnings) != 1 || warnings[0].(map[string]any)["code"] != WarnLeaseReleaseFailed {
			t.Fatalf("error %v, warnings %v, want lease_release_failed", err, warnings)
		}
		if msg := warnings[0].(map[string]any)["message"].(string); !strings.Contains(msg, e.hub.Leases()[0].ID) || strings.Contains(msg, pat) {
			t.Errorf("message = %q, want the lease and not the token", msg)
		}
		for _, field := range []string{"warnings", "artifacts", "side_effects"} { // what the worker adds to
			delete(env, field)
			delete(was, field)
		}
		if string(testutil.JSON(t, env)) != string(testutil.JSON(t, was)) {
			t.Errorf("the envelope changed beyond what the worker adds:\n%v\nwas\n%v", env, was)
		}
		if n := len(e.log.Find("WARN", "could not be released")); n != 1 {
			t.Errorf("the failure was logged %d times, want once", n)
		}
	})

	for name, tc := range map[string]struct {
		loop  loop
		fault fakehub.Fault
	}{
		"a transient error":       {boxLost, fakehub.Fault{Status: 500}},
		"a panic":                 {func(context.Context, *attempt) *outcome { panic("the loop panicked") }, fakehub.Fault{Status: 500}},
		"a release that hangs on": {boxLost, fakehub.Fault{Delay: time.Minute}}, // the release is bounded
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			e.hub.Inject("DELETE", sandboxesPath+"/*", tc.fault)
			e.runner.loop, e.runner.releaseTimeout = tc.loop, 100*time.Millisecond

			func() {
				defer func() { _ = recover() }()
				_, _ = e.run(e.fix())
			}()

			logged := e.log.Find("WARN", "could not be released")
			if len(logged) != 1 || logged[0]["lease"] != e.hub.Leases()[0].ID || strings.Contains(e.log.String(), pat) {
				t.Errorf("logged %v, want the failure once, with the lease and not the token", logged)
			}
		})
	}
}
