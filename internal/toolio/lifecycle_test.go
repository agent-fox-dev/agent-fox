package toolio

import (
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSummaryIsOneLine_TS_05_20 verifies TS-05-20:
// The summary is always one line, and the shell fills it when it sets the
// status.
// Verifies: 05-REQ-3.5
func TestSummaryIsOneLine_TS_05_20(t *testing.T) {
	multi := "first\nsecond\r\nthird\rfourth"
	endings := []struct {
		name     string
		out      Outcome
		err      error
		shellSet bool
	}{
		{"done with a multi-line summary", Outcome{Status: StatusDone, Summary: multi}, nil, false},
		{"unverified", Outcome{Status: StatusUnverified, Summary: multi}, nil, false},
		{"needs_human", Outcome{Status: StatusNeedsHuman, Summary: multi, NeedsHuman: &Question{Text: "which?"}}, nil, false},
		{"a failure with a multi-line message", Outcome{}, &Failure{Category: CategoryForge, Message: multi}, false},
		{"usage without a message", Outcome{}, &Failure{Category: CategoryUsage}, true},
		{"usage with a multi-line message", Outcome{}, &Failure{Category: CategoryUsage, Message: multi}, true},
		{"internal without a message", Outcome{}, &Failure{Category: CategoryInternal}, true},
		{"internal from a plain error", Outcome{}, fmt.Errorf("%s", multi), true},
		{"cancelled without a message", Outcome{}, &Failure{Category: CategoryCancelled}, true},
		{"timeout without a message", Outcome{}, &Failure{Category: CategoryTimeout}, true},
		{"an inconsistent outcome", Outcome{Status: StatusNeedsHuman}, nil, true},
		{"a panic", Outcome{}, nil, true},
	}
	for _, tc := range endings {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp()
			if tc.name == "a panic" {
				app.Register(Command{Path: "X", Run: func(*Run) (Outcome, error) { panic("boom\nwith two lines") }})
			} else {
				app.Register(returning("X", tc.out, tc.err))
			}

			_, stdout, _ := invoke(t, app, "X")
			summary, _ := decode(t, stdout)["summary"].(string)

			if strings.ContainsAny(summary, "\r\n") {
				t.Errorf("summary %q has a newline", summary)
			}
			if tc.shellSet && strings.TrimSpace(summary) == "" {
				t.Errorf("the shell set the status and left the summary empty")
			}
		})
	}
}

// TestWarningsDedupedInOrder_TS_05_21 verifies TS-05-21:
// Warnings are deduplicated in first-raised order and never change the
// status.
// Verifies: 05-REQ-3.6
func TestWarningsDedupedInOrder_TS_05_21(t *testing.T) {
	codes := []string{"alpha", "beta_2", "gamma"}
	messages := []string{"one", "two"}
	rng := rand.New(rand.NewPCG(21, 3))

	for i := range 200 {
		raised := make([]Warning, rng.IntN(15))
		for j := range raised {
			raised[j] = Warning{Code: codes[rng.IntN(len(codes))], Message: messages[rng.IntN(len(messages))]}
		}
		var want []any
		seen := map[Warning]bool{}
		for _, w := range raised {
			if !seen[w] {
				seen[w] = true
				want = append(want, map[string]any{"code": w.Code, "message": w.Message})
			}
		}
		if want == nil {
			want = []any{}
		}

		app := newTestApp()
		app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
			for _, w := range raised {
				r.Warn(w.Code, w.Message)
			}
			return Outcome{Status: StatusDone, Summary: "ok"}, nil
		}})
		code, stdout, _ := invoke(t, app, "X")
		env := decode(t, stdout)

		if got := env["warnings"]; !reflect.DeepEqual(got, want) {
			t.Fatalf("#%d: raised %v, warnings = %v, want %v", i, raised, got, want)
		}
		if code != 0 || env["status"] != "done" {
			t.Fatalf("#%d: exit code %d, status %v; want 0, done", i, code, env["status"])
		}
	}
}

// signalProcess sends sig to this process, which the shell is listening for.
func signalProcess(t *testing.T, sig os.Signal) {
	t.Helper()
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal(sig); err != nil {
		t.Fatal(err)
	}
}

type mainResult struct {
	code   int
	stdout string
	stderr string
}

// invokeAsync runs the app in the background.
func invokeAsync(t *testing.T, app *App, args ...string) <-chan mainResult {
	t.Helper()
	done := make(chan mainResult, 1)
	go func() {
		code, stdout, stderr := invoke(t, app, args...)
		done <- mainResult{code, stdout, stderr}
	}()
	return done
}

// invokeWithin runs the app and fails the test when Main does not return in
// time, so that a command that is never stopped cannot hang the suite.
func invokeWithin(t *testing.T, d time.Duration, app *App, args ...string) mainResult {
	t.Helper()
	select {
	case res := <-invokeAsync(t, app, args...):
		return res
	case <-time.After(d):
		t.Fatalf("Main did not return within %v", d)
		return mainResult{}
	}
}

// TestSignalCancelsCommand_TS_05_24 verifies TS-05-24:
// SIGINT and SIGTERM cancel the command context and yield failed/cancelled.
// Verifies: 05-REQ-4.1
func TestSignalCancelsCommand_TS_05_24(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals cannot be sent to the process")
	}
	for _, sig := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			started := make(chan struct{})
			app := newTestApp()
			app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
				close(started)
				<-r.Ctx.Done()
				return Outcome{}, r.Ctx.Err()
			}})
			done := invokeAsync(t, app, "X")

			<-started
			signalProcess(t, sig)

			select {
			case res := <-done:
				env := decode(t, res.stdout) // exactly one envelope
				if res.code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryCancelled {
					t.Errorf("got exit code %d, status %v, category %v; want 1, failed, cancelled", res.code, env["status"], at(env, "error", "category"))
				}
				if s, _ := env["summary"].(string); strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n") {
					t.Errorf("summary = %q, want one non-empty line", s)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the command was not cancelled")
			}
		})
	}
}

// TestIgnoredCancellationIsAbandoned_TS_05_25 verifies TS-05-25:
// A command ignoring cancellation is abandoned after 10 seconds.
// Verifies: 05-REQ-4.2
func TestIgnoredCancellationIsAbandoned_TS_05_25(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signals cannot be sent to the process")
	}
	if testing.Short() {
		t.Skip("waits for the real 10 second grace period")
	}
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	app := newTestApp() // the real grace period
	app.Register(Command{Path: "X", Run: func(*Run) (Outcome, error) {
		close(started)
		select {
		case <-release:
		case <-time.After(60 * time.Second):
		}
		return Outcome{Status: StatusDone}, nil
	}})
	done := invokeAsync(t, app, "X")

	<-started
	signalAt := time.Now()
	signalProcess(t, syscall.SIGTERM)

	select {
	case res := <-done:
		elapsed := time.Since(signalAt)
		env := decode(t, res.stdout)
		if res.code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryCancelled {
			t.Errorf("got exit code %d, status %v, category %v; want 1, failed, cancelled", res.code, env["status"], at(env, "error", "category"))
		}
		if elapsed < 9*time.Second || elapsed >= 15*time.Second {
			t.Errorf("Main returned %v after the signal, want about 10s", elapsed)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Main did not return")
	}
}

// TestTimeoutIsOptIn_TS_05_26 verifies TS-05-26:
// --timeout is opt-in, uses the command default, and 0 means no deadline.
// Verifies: 05-REQ-4.3
func TestTimeoutIsOptIn_TS_05_26(t *testing.T) {
	var deadline time.Time
	var has bool
	run := func(r *Run) (Outcome, error) {
		deadline, has = r.Ctx.Deadline()
		return Outcome{Status: StatusDone}, nil
	}
	app := newTestApp()
	app.Register(Command{Path: "X", Timeout: true, DefaultTimeout: 5 * time.Minute, Run: run})
	app.Register(Command{Path: "plain", Run: run})

	cases := []struct {
		name string
		args []string
		want time.Duration // 0: no deadline
	}{
		{"the default", []string{"X"}, 5 * time.Minute},
		{"a given value", []string{"X", "--timeout", "30s"}, 30 * time.Second},
		{"zero", []string{"X", "--timeout", "0"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deadline, has = time.Time{}, false
			if code, _, _ := invoke(t, app, tc.args...); code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if tc.want == 0 {
				if has {
					t.Errorf("deadline = %v, want none", deadline)
				}
				return
			}
			if !has || math.Abs(float64(time.Until(deadline)-tc.want)) > float64(2*time.Second) {
				t.Errorf("deadline in %v (set: %v), want about %v", time.Until(deadline), has, tc.want)
			}
		})
	}

	deadline, has = time.Time{}, false
	code, _, _ := invoke(t, app, "plain")
	if code != 0 || has {
		t.Errorf("a command that did not opt in: exit code %d, deadline set: %v; want 0, none", code, has)
	}
	code, stdout, _ := invoke(t, app, "plain", "--timeout", "30s")
	if code != 2 || decode(t, stdout)["status"] != "usage" {
		t.Errorf("--timeout for a command that did not opt in: exit code %d, status %v; want 2, usage", code, decode(t, stdout)["status"])
	}
}

// TestExpiredTimeout_TS_05_27 verifies TS-05-27:
// An expired --timeout gives failed/timeout with a doubled-timeout hint.
// Verifies: 05-REQ-4.4
func TestExpiredTimeout_TS_05_27(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "X", Timeout: true, Run: func(r *Run) (Outcome, error) {
		<-r.Ctx.Done()
		return Outcome{}, r.Ctx.Err()
	}})

	res := invokeWithin(t, 5*time.Second, app, "X", "--timeout", "50ms")
	code, env := res.code, decode(t, res.stdout)

	if code != 1 || env["status"] != "failed" {
		t.Errorf("got exit code %d and status %v, want 1 and failed", code, env["status"])
	}
	if at(env, "error", "category") != CategoryTimeout || at(env, "error", "retryable") != false {
		t.Errorf("category %v, retryable %v; want timeout, false", at(env, "error", "category"), at(env, "error", "retryable"))
	}
	want := map[string]any{"flag": "--timeout", "suggest": "100ms"}
	if got := at(env, "error", "fix_hint"); !reflect.DeepEqual(got, want) {
		t.Errorf("fix_hint = %v, want %v", got, want)
	}
	if s, _ := env["summary"].(string); strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\r\n") {
		t.Errorf("summary = %q, want one non-empty line", s)
	}
}

// TestTimeoutIgnoredByCommandIsAbandoned guards the bound behind TS-05-27: a
// command that ignores an expired deadline is given the grace period and then
// abandoned, so the process still ends with an envelope.
func TestTimeoutIgnoredByCommandIsAbandoned(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	app := newTestApp()
	app.Grace = 50 * time.Millisecond
	app.Register(Command{Path: "X", Timeout: true, Run: func(*Run) (Outcome, error) {
		<-release
		return Outcome{Status: StatusDone}, nil
	}})

	res := invokeWithin(t, 5*time.Second, app, "X", "--timeout", "20ms")
	code, env := res.code, decode(t, res.stdout)

	if code != 1 || at(env, "error", "category") != CategoryTimeout {
		t.Errorf("got exit code %d and category %v, want 1 and timeout", code, at(env, "error", "category"))
	}
}

// TestCommandGraceIsLongerThanTheShells: a command that declares its own grace
// period, because it has something to put back when it is told to stop, gets
// all of it: it is not abandoned at the shell's, and what it returns is
// reported.
func TestCommandGraceIsLongerThanTheShells(t *testing.T) {
	finished := false
	app := newTestApp()
	app.Grace = 30 * time.Millisecond
	app.Register(Command{Path: "X", Timeout: true, Grace: 5 * time.Second, Run: func(r *Run) (Outcome, error) {
		<-r.Ctx.Done()
		time.Sleep(300 * time.Millisecond) // the work of putting things back
		finished = true
		return Outcome{}, r.Ctx.Err()
	}})
	app.Register(Command{Path: "Y", Timeout: true, Run: func(r *Run) (Outcome, error) {
		<-r.Ctx.Done()
		time.Sleep(300 * time.Millisecond)
		return Outcome{}, r.Ctx.Err()
	}})

	res := invokeWithin(t, 10*time.Second, app, "X", "--timeout", "50ms")
	env := decode(t, res.stdout)
	other := decode(t, invokeWithin(t, 10*time.Second, app, "Y", "--timeout", "50ms").stdout)

	if !finished {
		t.Error("the command was told to stop and was abandoned before it had finished")
	}
	if res.code != 1 || at(env, "error", "category") != CategoryTimeout || strings.Contains(at(env, "error", "message").(string), "abandoned") {
		t.Errorf("exit code %d, error %v; want a timeout that is not an abandonment", res.code, env["error"])
	}
	if msg, _ := at(other, "error", "message").(string); !strings.Contains(msg, "abandoned") {
		t.Errorf("a command with no grace of its own: %q, want the shell's: abandoned", msg)
	}
}

// TestBadTimeoutRefused_TS_05_28 verifies TS-05-28:
// An unparseable or negative --timeout is refused before the command runs.
// Verifies: 05-REQ-4.5
func TestBadTimeoutRefused_TS_05_28(t *testing.T) {
	for _, value := range []string{"abc", "-5s", "5"} {
		t.Run(value, func(t *testing.T) {
			ran := false
			app := newTestApp()
			app.Register(Command{Path: "X", Timeout: true, Run: func(*Run) (Outcome, error) {
				ran = true
				return Outcome{Status: StatusDone}, nil
			}})

			code, stdout, _ := invoke(t, app, "X", "--timeout", value)

			if code != 2 || decode(t, stdout)["status"] != "usage" || ran {
				t.Errorf("exit code %d, status %v, ran %v; want 2, usage, false", code, decode(t, stdout)["status"], ran)
			}
		})
	}
}

// TestNeedsHumanOptionsAreLettered_TS_05_29 verifies TS-05-29:
// A needs_human outcome becomes {question, options, resume} with lettered
// ids.
// Verifies: 05-REQ-5.1
func TestNeedsHumanOptionsAreLettered_TS_05_29(t *testing.T) {
	app := newTestApp()
	app.Register(returning("lettered", Outcome{
		Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "Which?", Options: []string{"x", "y"}},
	}, nil))
	app.Register(returning("free", Outcome{Status: StatusNeedsHuman, NeedsHuman: &Question{Text: "Why?"}}, nil))

	code, stdout, _ := invoke(t, app, "lettered")
	env := decode(t, stdout)
	if code != 3 || env["status"] != "needs_human" {
		t.Errorf("got exit code %d and status %v, want 3 and needs_human", code, env["status"])
	}
	if got, want := at(env, "needs_human", "question"), "Which?"; got != want {
		t.Errorf("question = %v, want %v", got, want)
	}
	want := []any{map[string]any{"id": "A", "label": "x"}, map[string]any{"id": "B", "label": "y"}}
	if got := at(env, "needs_human", "options"); !reflect.DeepEqual(got, want) {
		t.Errorf("options = %v, want %v", got, want)
	}

	_, stdout, _ = invoke(t, app, "free")
	if got, ok := at(decode(t, stdout), "needs_human", "options").([]any); !ok || len(got) != 0 {
		t.Errorf("options without choices = %v, want []", at(decode(t, stdout), "needs_human", "options"))
	}
}

// TestOutcomeAfterTheDeadlineIsATimeout guards TS-05-27: a command that
// ignores its deadline and returns an outcome after it has run out is
// reported as timed out, not as done. A command that is cancelled by a
// signal and still returns an outcome has finished its job, and keeps it.
func TestOutcomeAfterTheDeadlineIsATimeout(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "X", Timeout: true, Run: func(r *Run) (Outcome, error) {
		time.Sleep(150 * time.Millisecond)
		return Outcome{Status: StatusDone, Summary: "late"}, nil
	}})
	app.Register(Command{Path: "quick", Timeout: true, Run: func(r *Run) (Outcome, error) {
		return Outcome{Status: StatusDone, Summary: "in time"}, nil
	}})

	res := invokeWithin(t, 5*time.Second, app, "X", "--timeout", "30ms")
	env := decode(t, res.stdout)
	if res.code != 1 || at(env, "error", "category") != CategoryTimeout {
		t.Errorf("a late outcome: exit code %d, category %v; want 1, timeout", res.code, at(env, "error", "category"))
	}
	if got := at(env, "error", "fix_hint", "suggest"); got != "60ms" {
		t.Errorf("fix_hint.suggest = %v, want 60ms", got)
	}

	res = invokeWithin(t, 5*time.Second, app, "quick", "--timeout", "30s")
	if env := decode(t, res.stdout); res.code != 0 || env["status"] != "done" {
		t.Errorf("an outcome in time: exit code %d, status %v; want 0, done", res.code, env["status"])
	}
}
