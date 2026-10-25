package toolio

import (
	"context"
	"math"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"
)

// signalSelf sends sig to this process.
func signalSelf(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatal(err)
	}
}

// runAsync runs the app in the background under ctx.
func runAsync(t *testing.T, ctx context.Context, app *App, args ...string) <-chan result {
	done := make(chan result, 1)
	go func() { done <- runWith(t, ctx, app, strings.NewReader(""), args...) }()
	return done
}

// within is the result of done, or a failure when it does not come in time,
// so that a command that is never stopped cannot hang the tests.
func within(t *testing.T, done <-chan result) result {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("Main did not return")
		return result{}
	}
}

func TestSignal_StopsTheCommand(t *testing.T) {
	for _, tc := range []struct {
		sig     syscall.Signal
		outcome bool // the command returns an outcome when it is stopped, not an error
		status  string
	}{
		{syscall.SIGINT, false, "failed"},
		{syscall.SIGTERM, false, "failed"},
		{syscall.SIGHUP, false, "failed"},
		{syscall.SIGTERM, true, "done"}, // it finished its job
	} {
		started := make(chan struct{})
		app := newApp()
		app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
			close(started)
			<-r.Ctx.Done()
			if tc.outcome {
				return Outcome{Status: StatusDone, Summary: "finished"}, nil
			}
			return Outcome{}, r.Ctx.Err()
		}})
		done := runAsync(t, context.Background(), app, "X")
		<-started

		signalSelf(t, tc.sig)

		r := within(t, done)
		env := r.env(t)
		if env["status"] != tc.status || (tc.status == "failed" && at(env, "error", "category") != CategoryCancelled) {
			t.Errorf("%v: envelope %s, want %s", tc.sig, r.stdout, tc.status)
		}
	}
}

func TestSignal_ACommandThatIgnoresItIsAbandonedAfterTheGrace(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	app := newApp()
	app.Grace = 50 * time.Millisecond
	app.Register(Command{Path: "X", Run: func(*Run) (Outcome, error) {
		close(started)
		<-release
		return Outcome{Status: StatusDone}, nil
	}})
	done := runAsync(t, context.Background(), app, "X")
	<-started

	signalSelf(t, syscall.SIGTERM)

	r := within(t, done)
	if env := r.env(t); r.code != 1 || at(env, "error", "category") != CategoryCancelled || !strings.Contains(at(env, "error", "message").(string), "abandoned") {
		t.Errorf("exit %d, envelope %s; want a cancellation that abandoned the command", r.code, r.stdout)
	}
}

func TestSignal_NoSignalsLeavesTheCallersContextTheOnlyStop(t *testing.T) {
	// The test takes the signals itself, so that the ones it sends do not end the process.
	caught := make(chan os.Signal, 4)
	signal.Notify(caught, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(caught)
	started := make(chan struct{})
	app := newApp()
	app.NoSignals = true
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		close(started)
		<-r.Ctx.Done()
		return Outcome{Status: StatusDone, Summary: "stopped"}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := runAsync(t, ctx, app, "X")
	<-started

	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		signalSelf(t, sig)
		<-caught
	}
	select {
	case r := <-done:
		t.Fatalf("a signal stopped the invocation: %s", r.stdout)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if r := within(t, done); r.code != 0 {
		t.Errorf("exit %d after the caller's context ended, want 0: %s", r.code, r.stdout)
	}
}

func TestTimeout_SetsTheDeadlineOfTheCommand(t *testing.T) {
	var deadline time.Time
	var has bool
	see := func(r *Run) (Outcome, error) {
		deadline, has = r.Ctx.Deadline()
		return Outcome{Status: StatusDone}, nil
	}
	app := newApp()
	app.Register(Command{Path: "X", Timeout: true, DefaultTimeout: 5 * time.Minute, Run: see})
	app.Register(Command{Path: "plain", Run: see})
	for _, tc := range []struct {
		args []string
		want time.Duration // 0: no deadline
	}{
		{[]string{"X"}, 5 * time.Minute},
		{[]string{"X", "--timeout", "30s"}, 30 * time.Second},
		{[]string{"X", "--timeout", "0"}, 0},
		{[]string{"plain"}, 0},
	} {
		deadline, has = time.Time{}, false
		if r := run(t, app, tc.args...); r.code != 0 || has != (tc.want > 0) ||
			(has && math.Abs(float64(time.Until(deadline)-tc.want)) > float64(time.Minute)) {
			t.Errorf("%q: exit %d, deadline in %v (set %v), want %v", tc.args, r.code, time.Until(deadline), has, tc.want)
		}
	}
}

func TestTimeout_ACommandStillRunningAtTheDeadlineHasTimedOut(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	for _, tc := range []struct {
		name      string
		timeout   string
		run       func(*Run) (Outcome, error)
		status    string
		abandoned bool
	}{
		{"it returns the error of its context", "50ms", func(r *Run) (Outcome, error) { <-r.Ctx.Done(); return Outcome{}, r.Ctx.Err() }, "failed", false},
		{"it returns an outcome after the deadline", "50ms", func(r *Run) (Outcome, error) {
			<-r.Ctx.Done()
			return Outcome{Status: StatusDone, Summary: "late"}, nil
		}, "failed", false},
		{"it ignores the deadline", "50ms", func(*Run) (Outcome, error) { <-release; return Outcome{Status: StatusDone}, nil }, "failed", true},
		{"it returns in time", "30s", func(*Run) (Outcome, error) { return Outcome{Status: StatusDone, Summary: "in time"}, nil }, "done", false},
	} {
		app := newApp()
		app.Grace = 20 * time.Millisecond
		app.NoSignals = true // the deadline and the grace hold without the signal handler too
		app.Register(Command{Path: "X", Timeout: true, Run: tc.run})

		r := within(t, runAsync(t, context.Background(), app, "X", "--timeout", tc.timeout))
		env := r.env(t)

		if env["status"] != tc.status {
			t.Errorf("%s: envelope %s, want %s", tc.name, r.stdout, tc.status)
			continue
		}
		if tc.status != "failed" {
			continue
		}
		msg, _ := at(env, "error", "message").(string)
		if at(env, "error", "category") != CategoryTimeout || at(env, "error", "retryable") != false ||
			at(env, "error", "fix_hint", "flag") != "--timeout" || at(env, "error", "fix_hint", "suggest") != "100ms" ||
			strings.Contains(msg, "abandoned") != tc.abandoned {
			t.Errorf("%s: error %v, want a timeout that suggests 100ms (abandoned: %v)", tc.name, env["error"], tc.abandoned)
		}
	}
}

func TestTimeout_TheSuggestedDoubleDoesNotOverflow(t *testing.T) {
	if got := doubled(time.Second); got != 2*time.Second {
		t.Errorf("doubled(1s) = %v", got)
	}
	if got := doubled(math.MaxInt64); got != math.MaxInt64 {
		t.Errorf("doubled(max) = %v, want max", got)
	}
}

func TestGrace_ACommandsOwnIsWaitedForInPlaceOfTheApps(t *testing.T) {
	putBack := func(r *Run) (Outcome, error) {
		<-r.Ctx.Done()
		<-time.After(30 * time.Millisecond) // the work of putting a tree back
		return Outcome{}, r.Ctx.Err()
	}
	for _, tc := range []struct {
		grace     time.Duration
		abandoned bool
	}{{10 * time.Second, false}, {0, true}} {
		app := newApp()
		app.Grace = time.Nanosecond
		app.Register(Command{Path: "X", Timeout: true, Grace: tc.grace, Run: putBack})

		r := within(t, runAsync(t, context.Background(), app, "X", "--timeout", "10ms"))

		if msg, _ := at(r.env(t), "error", "message").(string); at(r.env(t), "error", "category") != CategoryTimeout || strings.Contains(msg, "abandoned") != tc.abandoned {
			t.Errorf("grace %v: error %v, want a timeout (abandoned: %v)", tc.grace, at(r.env(t), "error"), tc.abandoned)
		}
	}
}
