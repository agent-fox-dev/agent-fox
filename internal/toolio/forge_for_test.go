package toolio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

// recorded is a repository learned at run time, on a host the environment does
// not name.
var recorded = issuex.Repo{Owner: "o", Name: "r", Host: "git.example.com"}

// TestForgeForBuildsTheClientTheWayTheShellDoes verifies 08-REQ-1.6 at the
// shell: the client is built by App.NewForge with the repository and its host,
// the user agent of the shell and the --dir of the command, and it is closed
// once when the command returns, whatever the command does.
func TestForgeForBuildsTheClientTheWayTheShellDoes(t *testing.T) {
	for name, run := range map[string]func(*Run) (Outcome, error){
		"a return": func(r *Run) (Outcome, error) {
			_, err := r.ForgeFor(recorded)
			return Outcome{Status: StatusDone, Summary: "ok"}, err
		},
		"an error": func(r *Run) (Outcome, error) {
			_, _ = r.ForgeFor(recorded)
			return Outcome{}, errors.New("failed")
		},
		"a panic": func(r *Run) (Outcome, error) { _, _ = r.ForgeFor(recorded); panic("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			var got []issuex.Options
			stub := newStubForge()
			app := forgeApp(stub, &got)
			app.Register(Command{Path: "X", Dir: true, DryRun: true, Run: run})
			probe := &closeProbe{stub: stub}

			app.Main(context.Background(), []string{"af", "X", "--dir", t.TempDir()}, strings.NewReader(""), probe, &bytes.Buffer{})

			if len(got) != 1 {
				t.Fatalf("NewForge was called %d times, want once", len(got))
			}
			o := got[0]
			if o.Repo != recorded || o.UserAgent != "af/v0.0.0-test" || o.Dir == "" || o.BaseURL != "" || o.Token != "" {
				t.Errorf("options = %+v, want the repository with its host, the user agent af/v0.0.0-test and --dir, and nothing else", o)
			}
			if stub.closes != 1 {
				t.Errorf("the client was closed %d times, want once", stub.closes)
			}
			if !probe.wrote || probe.closesAtWrite != 1 {
				t.Errorf("the client had been closed %d times when the envelope was written, want once", probe.closesAtWrite)
			}
		})
	}
}

// TestForgeForUnderDryRunRefusesEveryWrite guards 08-REQ-8.8 for a client built
// at run time: under --dry-run its writes are refused as planned side effects,
// its reads pass through; without it the client is the one NewForge made.
func TestForgeForUnderDryRunRefusesEveryWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		args    []string
		refused bool
	}{"a dry run": {[]string{"af", "X", "--dry-run"}, true}, "a real run": {[]string{"af", "X"}, false}} {
		t.Run(name, func(t *testing.T) {
			var got []issuex.Options
			stub := newStubForge()
			app := forgeApp(stub, &got)
			var writeErr error
			app.Register(Command{Path: "X", Dir: true, DryRun: true, Run: func(r *Run) (Outcome, error) {
				client, err := r.ForgeFor(recorded)
				if err != nil {
					return Outcome{}, err
				}
				if client.Authenticated() != stub.Authenticated() { // a read passes through
					return Outcome{}, errors.New("a read did not pass through")
				}
				_, writeErr = client.UpdateIssue(r.Ctx, issuex.IssueRef{Repo: recorded, Number: 1}, issuex.UpdateIssueRequest{Body: "x"})
				return Outcome{Status: StatusDone, Summary: "ok"}, nil
			}})

			_, stdout, _ := invoke(t, app, tc.args[1:]...)

			env := decode(t, stdout)
			if got := errors.Is(writeErr, ErrDryRun); got != tc.refused {
				t.Errorf("the write was refused as a dry run: %v, want %v (%v)", got, tc.refused, writeErr)
			}
			effects, _ := env["side_effects"].([]any)
			if tc.refused && (len(effects) != 1 || at(effects[0].(map[string]any), "state") != "planned" || at(effects[0].(map[string]any), "kind") != "update_issue") {
				t.Errorf("side effects = %v, want one planned update_issue", effects)
			}
			if !tc.refused && len(effects) != 0 {
				t.Errorf("side effects = %v, want none", effects)
			}
		})
	}
}

// TestForgeForFailures guards the errors of building the client: a host that
// names neither forge is a usage failure with the variable to set, any other
// error a forge failure, and a client that was built is closed all the same.
func TestForgeForFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err      error
		category string
		hint     string
	}{
		"an ambiguous forge":   {fmt.Errorf("%w: git.example.com", issuex.ErrAmbiguousForge), "usage", "GITHUB_API_URL"},
		"an unsupported forge": {fmt.Errorf("%w: x", issuex.ErrUnsupportedForge), "usage", "GITHUB_API_URL"},
		"another error":        {errors.New("no adapter"), "forge", ""},
	} {
		t.Run(name, func(t *testing.T) {
			app := newTestApp()
			app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, tc.err }
			app.Register(Command{Path: "X", Dir: true, Run: func(r *Run) (Outcome, error) {
				_, err := r.ForgeFor(recorded)
				return Outcome{}, err
			}})

			_, stdout, _ := invoke(t, app, "X")

			env := decode(t, stdout)
			if at(env, "error", "category") != tc.category || (tc.hint != "" && at(env, "error", "fix_hint", "env") != tc.hint) {
				t.Errorf("error = %v, want category %s and the hint %q", env["error"], tc.category, tc.hint)
			}
		})
	}
}
