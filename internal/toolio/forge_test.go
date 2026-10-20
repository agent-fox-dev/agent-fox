package toolio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// stubForge is a forge client that counts how often it is closed.
type stubForge struct {
	issuex.Client
	closes int
}

func newStubForge() *stubForge { return &stubForge{Client: issuex.NewNoOp()} }

func (s *stubForge) Close() error {
	s.closes++
	return nil
}

// forgeApp is an app whose NewForge records the options it is given and
// returns stub.
func forgeApp(stub *stubForge, got *[]issuex.Options) *App {
	app := newTestApp()
	app.NewForge = func(o issuex.Options) (issuex.Client, error) {
		*got = append(*got, o)
		return stub, nil
	}
	return app
}

// forgeRecorder is a test command that uses a forge and records its Run.
type forgeRecorder struct {
	ran    int
	forge  issuex.Client
	target issuex.Repo
}

func (r *forgeRecorder) command(mutate ...func(*Command)) Command {
	c := Command{Path: "X", UsesForge: true, Repo: true, Input: InputOptional, Run: func(run *Run) (Outcome, error) {
		r.ran++
		r.forge = run.Forge
		r.target = run.Target
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}}
	for _, m := range mutate {
		m(&c)
	}
	return c
}

// TestForgeOptions_TS_05_51 verifies TS-05-51:
// The forge client is built with the documented options and only for
// commands that declare it.
// Verifies: 05-REQ-8.1
func TestForgeOptions_TS_05_51(t *testing.T) {
	dir := t.TempDir()
	issueURL := "https://github.com/o/r/issues/7"
	ref, ok := issuex.ParseIssueURL(issueURL)
	if !ok {
		t.Fatal("test URL is not an issue URL")
	}
	parsed, _ := issuex.ParseRepo("o/r")
	userAgent := "af/v0.0.0-test"

	t.Run("an issue URL", func(t *testing.T) {
		var got []issuex.Options
		stub := newStubForge()
		var r forgeRecorder
		app := forgeApp(stub, &got)
		app.Register(r.command())

		invoke(t, app, "X", issueURL, "--dir", dir)

		want := []issuex.Options{{Repo: ref.Repo, UserAgent: userAgent, Dir: dir}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("options = %+v, want %+v", got, want)
		}
		// The command cannot run on an issue URL before the thread can be
		// read, so the target is checked where it is chosen.
		cmd := r.command()
		_, target, err := app.forgeFor(&cmd, &flagValues{dir: dir}, []string{issueURL})
		if err != nil || target != ref.Repo {
			t.Errorf("target = %+v (error %v), want %+v", target, err, ref.Repo)
		}
	})

	t.Run("--repo and text", func(t *testing.T) {
		var got []issuex.Options
		stub := newStubForge()
		var r forgeRecorder
		app := forgeApp(stub, &got)
		app.Register(r.command())

		code, _, _ := invoke(t, app, "X", "fix the bug", "--repo", "o/r", "--dir", dir)

		want := []issuex.Options{{Repo: parsed, UserAgent: userAgent, Dir: dir}}
		if code != 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("exit code %d, options = %+v; want 0 and %+v", code, got, want)
		}
		if r.forge != issuex.Client(stub) || r.target != parsed {
			t.Errorf("Run.Forge = %v, Run.Target = %+v; want the stub and %+v", r.forge, r.target, parsed)
		}
	})

	t.Run("neither", func(t *testing.T) {
		var got []issuex.Options
		stub := newStubForge()
		var r forgeRecorder
		app := forgeApp(stub, &got)
		app.Register(r.command())

		code, _, _ := invoke(t, app, "X", "fix the bug", "--dir", dir)

		want := []issuex.Options{{UserAgent: userAgent, Dir: dir}}
		if code != 0 || !reflect.DeepEqual(got, want) {
			t.Errorf("exit code %d, options = %+v; want 0 and %+v", code, got, want)
		}
		if r.forge != issuex.Client(stub) || r.target.Valid() {
			t.Errorf("Run.Forge = %v, Run.Target = %+v; want the stub and no repository", r.forge, r.target)
		}
	})

	t.Run("a command without a forge", func(t *testing.T) {
		var got []issuex.Options
		var r forgeRecorder
		app := forgeApp(newStubForge(), &got)
		app.Register(r.command(func(c *Command) { c.UsesForge = false; c.Dir = true }))

		code, _, _ := invoke(t, app, "X", "fix the bug", "--dir", dir)

		if code != 0 || len(got) != 0 || r.forge != nil {
			t.Errorf("exit code %d, NewForge called %d times, Run.Forge = %v; want 0, 0, nil", code, len(got), r.forge)
		}
	})
}

// TestBadOrConflictingRepo_TS_05_52 verifies TS-05-52:
// A bad or conflicting --repo is a usage error.
// Verifies: 05-REQ-8.2
func TestBadOrConflictingRepo_TS_05_52(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"an invalid repository", []string{"X", "fix it", "--repo", "not a repo"}},
		{"a repository that is not owner/name", []string{"X", "fix it", "--repo", "justname"}},
		{"another repository than the issue's", []string{"X", "https://github.com/c/d/issues/1", "--repo", "a/b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []issuex.Options
			var r forgeRecorder
			app := forgeApp(newStubForge(), &got)
			app.Register(r.command())

			code, stdout, _ := invoke(t, app, tc.args...)

			if code != 2 || decode(t, stdout)["status"] != "usage" || r.ran != 0 {
				t.Errorf("exit code %d, status %v, ran %d; want 2, usage, 0", code, decode(t, stdout)["status"], r.ran)
			}
			if len(got) != 0 {
				t.Errorf("a forge client was built for a refused invocation")
			}
		})
	}

	// The same repository, spelled as the issue's, is no conflict.
	var got []issuex.Options
	var r forgeRecorder
	app := forgeApp(newStubForge(), &got)
	app.Register(r.command())
	invoke(t, app, "X", "https://github.com/c/d/issues/1", "--repo", "C/D")
	if len(got) != 1 {
		t.Errorf("--repo C/D with an issue of c/d built %d clients, want 1", len(got))
	}
}

// TestAmbiguousForgeWithIssueURL_TS_05_53 verifies TS-05-53:
// Ambiguous forge with an issue URL is a usage error with a GITHUB_API_URL
// hint.
// Verifies: 05-REQ-8.3
func TestAmbiguousForgeWithIssueURL_TS_05_53(t *testing.T) {
	for _, sentinel := range []error{issuex.ErrAmbiguousForge, issuex.ErrUnsupportedForge} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			failure := fmt.Errorf("%w: cannot tell which forge serves \"git.example.com\"", sentinel)
			var r forgeRecorder
			app := newTestApp()
			app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, failure }
			app.Register(r.command())

			code, stdout, _ := invoke(t, app, "X", "https://github.com/o/r/issues/7")
			env := decode(t, stdout)

			if code != 2 || env["status"] != "usage" || r.ran != 0 {
				t.Errorf("exit code %d, status %v, ran %d; want 2, usage, 0", code, env["status"], r.ran)
			}
			if got := at(env, "error", "fix_hint", "env"); got != "GITHUB_API_URL" {
				t.Errorf("fix_hint.env = %v, want GITHUB_API_URL", got)
			}
			if msg, _ := at(env, "error", "message").(string); !strings.Contains(msg, failure.Error()) {
				t.Errorf("error.message = %q, want issuex's message %q", msg, failure)
			}
		})
	}
}

// TestAmbiguousForgeGivesNoOpClient_TS_05_54 verifies TS-05-54:
// Ambiguous forge without an issue URL gives the command a no-op client.
// Verifies: 05-REQ-8.4
func TestAmbiguousForgeGivesNoOpClient_TS_05_54(t *testing.T) {
	for _, sentinel := range []error{issuex.ErrAmbiguousForge, issuex.ErrUnsupportedForge} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			var r forgeRecorder
			app := newTestApp()
			app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, fmt.Errorf("%w: unable to detect", sentinel) }
			app.Register(r.command())

			code, _, _ := invoke(t, app, "X", "fix the bug")

			if code != 0 || r.ran != 1 {
				t.Fatalf("exit code %d, ran %d; want 0, 1", code, r.ran)
			}
			if r.forge == nil || r.forge.Authenticated() || r.target.Valid() {
				t.Errorf("Run.Forge = %v, Run.Target = %+v; want a client that is not authenticated and no repository", r.forge, r.target)
			}
			if _, ok := r.forge.(*issuex.NoOpClient); !ok {
				t.Errorf("Run.Forge is a %T, want *issuex.NoOpClient", r.forge)
			}
		})
	}
}

// TestOtherForgeErrorIsAFailure guards the cases behind TS-05-53 and -54: an
// error that is neither ambiguity nor an unsupported forge is a forge
// failure, not a silent no-op.
func TestOtherForgeErrorIsAFailure(t *testing.T) {
	var r forgeRecorder
	app := newTestApp()
	app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, errors.New("adapter broke") }
	app.Register(r.command())

	code, stdout, _ := invoke(t, app, "X", "fix the bug")
	env := decode(t, stdout)

	if code != 1 || at(env, "error", "category") != CategoryForge || r.ran != 0 {
		t.Errorf("exit code %d, category %v, ran %d; want 1, forge, 0", code, at(env, "error", "category"), r.ran)
	}
}

// closeProbe is a stdout that records how often the forge client had been
// closed when the envelope was first written.
type closeProbe struct {
	stub          *stubForge
	closesAtWrite int
	wrote         bool
	buf           bytes.Buffer
}

func (p *closeProbe) Write(b []byte) (int, error) {
	if !p.wrote {
		p.wrote = true
		p.closesAtWrite = p.stub.closes
	}
	return p.buf.Write(b)
}

// TestForgeClosedOnce_TS_05_55 verifies TS-05-55:
// The forge client is closed exactly once on return, error and panic, before
// the envelope is written.
// Verifies: 05-REQ-8.5
func TestForgeClosedOnce_TS_05_55(t *testing.T) {
	cases := []struct {
		name string
		run  func(*Run) (Outcome, error)
		args []string
	}{
		{"a return", func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }, []string{"X", "text"}},
		{"an error", func(*Run) (Outcome, error) { return Outcome{}, errors.New("failed") }, []string{"X", "text"}},
		{"a panic", func(*Run) (Outcome, error) { panic("boom") }, []string{"X", "text"}},
		{"an input that cannot be resolved", func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }, []string{"X", "   "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []issuex.Options
			stub := newStubForge()
			app := forgeApp(stub, &got)
			app.Register(Command{Path: "X", UsesForge: true, Input: InputOptional, Run: tc.run})
			probe := &closeProbe{stub: stub}

			app.Main(context.Background(), append([]string{"af"}, tc.args...), strings.NewReader(""), probe, &bytes.Buffer{})

			if stub.closes != 1 {
				t.Errorf("Close was called %d times, want 1", stub.closes)
			}
			if !probe.wrote || probe.closesAtWrite != 1 {
				t.Errorf("the forge had been closed %d times when the envelope was written, want 1", probe.closesAtWrite)
			}
		})
	}
}
