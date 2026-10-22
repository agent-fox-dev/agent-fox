package toolio

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"
)

// invokeWith runs the app under ctx with "af" as argv[0] followed by args.
func invokeWith(ctx context.Context, app *App, args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	code = app.Main(ctx, append([]string{"af"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// blocking is a command that waits for its context to end and reports why it was
// stopped.
type blocking struct {
	started chan struct{}
	cause   chan error
}

func newBlocking() *blocking {
	return &blocking{started: make(chan struct{}), cause: make(chan error, 1)}
}

func (b *blocking) command() Command {
	return Command{Path: "X", Timeout: true, Grace: 500 * time.Millisecond, Run: func(r *Run) (Outcome, error) {
		close(b.started)
		<-r.Ctx.Done()
		b.cause <- context.Cause(r.Ctx)
		return Outcome{Status: StatusDone, Summary: "stopped"}, nil
	}}
}

// TestInProcessInstallsNoSignalHandler_TS_13_6 verifies TS-13-6: an invocation with
// NoSignals is stopped by the caller's context, by --timeout and by the grace, and by no
// signal.
// Verifies: 13-REQ-1.4
func TestInProcessInstallsNoSignalHandler_TS_13_6(t *testing.T) {
	// The test takes the signals itself, so that the process is not ended by the ones it
	// sends: with the handler of the shell installed they would stop the command as well.
	caught := make(chan os.Signal, 4)
	signal.Notify(caught, syscall.SIGHUP, syscall.SIGTERM)
	t.Cleanup(func() { signal.Stop(caught) })

	t.Run("the caller's context is the only stop", func(t *testing.T) {
		b := newBlocking()
		app := newTestApp()
		app.NoSignals = true
		app.Register(b.command())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan int, 1)
		go func() { code, _, _ := invokeWith(ctx, app, "X"); done <- code }()
		<-b.started

		for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
			if err := syscall.Kill(os.Getpid(), sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-caught:
			case <-time.After(5 * time.Second):
				t.Fatalf("%v was not delivered to the test", sig)
			}
		}
		select {
		case <-done:
			t.Fatal("a signal stopped the invocation")
		case <-time.After(300 * time.Millisecond):
		}

		cancel()
		select {
		case code := <-done:
			if code != 0 {
				t.Errorf("exit code %d after the caller's context was cancelled, want 0 (the command returned normally)", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("cancelling the caller's context did not stop the invocation")
		}
	})

	t.Run("--timeout still applies", func(t *testing.T) {
		b := newBlocking()
		app := newTestApp()
		app.NoSignals = true
		app.Register(b.command())
		start := time.Now()
		invokeWith(context.Background(), app, "X", "--timeout", "200ms")
		if cause := <-b.cause; cause == nil || time.Since(start) > 5*time.Second {
			t.Errorf("cause %v after %v, want the deadline to have stopped the command", cause, time.Since(start))
		}
	})

	t.Run("the grace still applies", func(t *testing.T) {
		app := newTestApp()
		app.NoSignals = true
		app.Grace = 100 * time.Millisecond
		release := make(chan struct{})
		defer close(release)
		app.Register(Command{Path: "X", Timeout: true, Run: func(r *Run) (Outcome, error) {
			<-release // a command that does not stop when it is told to
			return Outcome{Status: StatusDone}, nil
		}})
		start := time.Now()
		code, stdout, _ := invokeWith(context.Background(), app, "X", "--timeout", "100ms")
		env := decode(t, stdout)
		if code == 0 || at(env, "error", "category") != CategoryTimeout || time.Since(start) > 5*time.Second {
			t.Errorf("exit code %d, error %v after %v; want the shell to give up on the command after the grace", code, env["error"], time.Since(start))
		}
	})
}

// TestInProcessSignalsStillStopTheBinary guards the other side of NoSignals: without it
// the shell takes the signals, as af does.
func TestInProcessSignalsStillStopTheBinary(t *testing.T) {
	caught := make(chan os.Signal, 1)
	signal.Notify(caught, syscall.SIGHUP)
	t.Cleanup(func() { signal.Stop(caught) })
	b := newBlocking()
	app := newTestApp()
	app.Register(b.command())
	done := make(chan struct{})
	go func() { invokeWith(context.Background(), app, "X"); close(done) }()
	<-b.started
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SIGHUP did not stop an invocation that has the shell's handler")
	}
}

// eventCommand is a command that emits events.
func eventCommand() Command {
	return Command{Path: "X", UsesRunDir: true, Run: func(r *Run) (Outcome, error) {
		r.Emit("step", map[string]string{"name": "one"})
		r.Emit("step", map[string]string{"name": "two"})
		r.Warn("some_warning", "something")
		r.Emit("step", map[string]string{"name": "three"})
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}}
}

// TestEventSinkReceivesTheLinesOfEventsJSONL_TS_13_7 verifies TS-13-7: the sink gets the
// lines of events.jsonl, in order, as they are written.
// Verifies: 13-REQ-1.5
func TestEventSinkReceivesTheLinesOfEventsJSONL_TS_13_7(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var lines []string
	var written []int // how long events.jsonl was when each line arrived
	app := newTestApp()
	var runDir string
	app.EventSink = func(line []byte) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, string(line))
		size := -1
		if runDir != "" {
			if info, err := os.Stat(filepath.Join(runDir, "events.jsonl")); err == nil {
				size = int(info.Size())
			}
		}
		written = append(written, size)
	}
	cmd := eventCommand()
	run := cmd.Run
	cmd.Run = func(r *Run) (Outcome, error) {
		runDir = r.RunDir.Path
		return run(r)
	}
	app.Register(cmd)

	code, stdout, _ := invoke(t, app, "X", "--dir", dir)
	env := decode(t, stdout)
	if code != 0 {
		t.Fatalf("exit code %d, envelope %v", code, env)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "run", *envRunID(t, env), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != len(want) || len(want) < 6 {
		t.Fatalf("the sink got %d lines, events.jsonl has %d (at least run_start, three steps, a warning and run_end are expected):\n%q\n%q", len(lines), len(want), lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d: the sink got %q, events.jsonl has %q", i, lines[i], want[i])
		}
	}
	for i, size := range written {
		if size >= 0 && size < len(strings.Join(want[:i+1], "\n")) {
			t.Errorf("line %d reached the sink before it was written to events.jsonl (the file held %d bytes)", i, size)
		}
	}
}

// TestEventSinkReceivesEventsOfACommandWithoutARunDirectory covers a sink that is the only
// place the events go.
func TestEventSinkReceivesEventsOfACommandWithoutARunDirectory(t *testing.T) {
	var lines []string
	app := newTestApp()
	app.EventSink = func(line []byte) { lines = append(lines, string(line)) }
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		r.Emit("step", map[string]string{"name": "one"})
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})
	invoke(t, app, "X")
	if len(lines) != 3 || !strings.Contains(lines[0], `"run_start"`) || !strings.Contains(lines[1], `"step"`) || !strings.Contains(lines[2], `"run_end"`) {
		t.Errorf("lines = %q, want run_start, the step and run_end", lines)
	}
}

// envRunID is the run_id of an envelope.
func envRunID(t *testing.T, env map[string]any) *string {
	t.Helper()
	id, ok := env["run_id"].(string)
	if !ok {
		t.Fatalf("the envelope has no run_id: %v", env["run_id"])
	}
	return &id
}

// TestCallerChosenRunID_TS_13_8 verifies TS-13-8: a run id of the caller names the run
// directory and the envelope, and one that is malformed is a usage error with nothing
// created.
// Verifies: 13-REQ-1.6
func TestCallerChosenRunID_TS_13_8(t *testing.T) {
	t.Run("a valid id", func(t *testing.T) {
		for _, id := range []string{"20250102_030405_abc123", "20261231_235959_0f9e8d"} {
			dir := t.TempDir()
			var c runDirCommand
			app := newTestApp()
			app.RunID = id
			app.Register(c.command(nil))

			code, stdout, _ := invoke(t, app, "X", "--dir", dir)

			env := decode(t, stdout)
			if code != 0 || env["run_id"] != id {
				t.Errorf("exit code %d, run_id %v; want 0 and %s", code, env["run_id"], id)
			}
			if c.runDir == nil || c.runDir.ID != id || filepath.Base(c.runDir.Path) != id {
				t.Errorf("run directory = %+v, want one named %s", c.runDir, id)
			}
		}
	})

	t.Run("a malformed id", func(t *testing.T) {
		for _, id := range []string{"run-1", "20250102_030405_ABC123", "20250102_030405_abc12", "../20250102_030405_abc123", "20250102_030405_abc123/x"} {
			dir := t.TempDir()
			var c runDirCommand
			app := newTestApp()
			app.RunID = id
			app.Register(c.command(nil))

			code, stdout, _ := invoke(t, app, "X", "--dir", dir)

			env := decode(t, stdout)
			if code != ExitCode(StatusUsage) || env["status"] != "usage" || at(env, "error", "category") != CategoryUsage {
				t.Errorf("id %q: exit code %d, envelope %v; want a usage error", id, code, env)
			}
			if c.ran != 0 {
				t.Errorf("id %q: the command ran", id)
			}
			if _, err := os.Stat(agentFoxDir(dir)); !os.IsNotExist(err) {
				t.Errorf("id %q: %s exists (%v), want nothing created", id, agentFoxDir(dir), err)
			}
		}
	})

	t.Run("an id that is taken", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(agentFoxDir(dir), "run", "20250102_030405_abc123"), 0o755); err != nil {
			t.Fatal(err)
		}
		var c runDirCommand
		app := newTestApp()
		app.RunID = "20250102_030405_abc123"
		app.Register(c.command(nil))

		code, stdout, _ := invoke(t, app, "X", "--dir", dir)

		env := decode(t, stdout)
		if code == 0 || c.ran != 0 || at(env, "error", "category") == nil {
			t.Errorf("exit code %d, envelope %v, ran %d; want a failure and no run", code, env, c.ran)
		}
	})
}

// TestCallerChosenTargetRepository_TS_13_9 verifies TS-13-9: the target repository of the
// caller is the repository of every input kind, in place of --repo and the origin, and an
// issue URL on another repository is a usage error with no forge request.
// Verifies: 13-REQ-1.7
func TestCallerChosenTargetRepository_TS_13_9(t *testing.T) {
	target := issuex.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}
	// The origin of --dir is another host.
	origin := t.TempDir()
	git(t, origin, "init", "-q", "-b", "main")
	git(t, origin, "remote", "add", "origin", "https://github.com/other/origin.git")

	t.Run("text and --repo", func(t *testing.T) {
		var got []issuex.Options
		stub := newStubForge()
		var r forgeRecorder
		app := forgeApp(stub, &got)
		app.Target = target
		app.Register(r.command())

		code, _, _ := invoke(t, app, "X", "fix the bug", "--repo", "o/r", "--dir", origin)

		if code != 0 || len(got) != 1 || got[0].Repo != target {
			t.Fatalf("exit code %d, options = %+v; want one client, built for %+v", code, got, target)
		}
		if r.target != target || r.forge != issuex.Client(stub) {
			t.Errorf("Run.Target = %+v, want %+v", r.target, target)
		}
	})

	t.Run("an issue on the target", func(t *testing.T) {
		var got []issuex.Options
		var r forgeRecorder
		app := forgeApp(newStubForge(), &got)
		app.Target = target
		app.Register(r.command())

		code, _, _ := invoke(t, app, "X", "https://github.com/Acme/Widgets/issues/7", "--dir", origin)

		if code != 0 || len(got) != 1 || got[0].Repo != target {
			t.Errorf("exit code %d, options = %+v; want the client of the target", code, got)
		}
	})

	t.Run("an issue on another repository", func(t *testing.T) {
		for _, url := range []string{"https://github.com/acme/other/issues/7", "https://gitlab.com/acme/widgets/issues/7"} {
			var got []issuex.Options
			var r forgeRecorder
			app := forgeApp(newStubForge(), &got)
			app.Target = target
			app.Register(r.command())

			code, stdout, _ := invoke(t, app, "X", url, "--dir", origin)

			env := decode(t, stdout)
			if code != ExitCode(StatusUsage) || at(env, "error", "category") != CategoryUsage {
				t.Errorf("%s: exit code %d, envelope %v; want a usage error", url, code, env["error"])
			}
			if len(got) != 0 || r.ran != 0 {
				t.Errorf("%s: %d forge clients built, command ran %d times; want neither", url, len(got), r.ran)
			}
		}
	})
}
