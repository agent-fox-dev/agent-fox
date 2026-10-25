package toolio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

// recorded is a repository a command learns at run time, on a host the
// environment does not name.
var recorded = issuex.Repo{Owner: "o", Name: "r", Host: "git.example.com"}

func TestForgeFor_BuildsTheClientAsTheShellDoesAndClosesItOnce(t *testing.T) {
	for name, end := range map[string]func() (Outcome, error){
		"a return": func() (Outcome, error) { return Outcome{Status: StatusDone, Summary: "ok"}, nil },
		"an error": func() (Outcome, error) { return Outcome{}, errors.New("failed") },
		"a panic":  func() (Outcome, error) { panic("boom") },
	} {
		stub := newStubForge()
		app, got := forgeApp(stub)
		dir := t.TempDir()
		app.Register(Command{Path: "X", Dir: true, Run: func(r *Run) (Outcome, error) {
			if _, err := r.ForgeFor(recorded); err != nil {
				return Outcome{}, err
			}
			return end()
		}})
		probe := &closeProbe{stub: stub}

		app.Main(context.Background(), []string{"af", "X", "--dir", dir}, strings.NewReader(""), probe, io.Discard)

		if want := (issuex.Options{Repo: recorded, UserAgent: "af/v0.0.0-test", Dir: dir}); len(*got) != 1 || (*got)[0] != want {
			t.Errorf("%s: options %+v, want once %+v", name, *got, want)
		}
		if stub.closes != 1 || probe.closesAtWrite != 1 {
			t.Errorf("%s: closed %d times, %d of them before the envelope; want once, before", name, stub.closes, probe.closesAtWrite)
		}
	}
}

func TestForgeFor_UnderDryRunRefusesTheWrites(t *testing.T) {
	for _, dry := range []bool{true, false} {
		stub := newStubForge()
		app, _ := forgeApp(stub)
		var writeErr error
		var client issuex.Client
		app.Register(Command{Path: "X", Dir: true, DryRun: true, Run: func(r *Run) (Outcome, error) {
			var err error
			if client, err = r.ForgeFor(recorded); err != nil {
				return Outcome{}, err
			}
			_, writeErr = client.UpdateIssue(r.Ctx, issuex.IssueRef{Repo: recorded, Number: 1}, issuex.UpdateIssueRequest{Body: "x"})
			return Outcome{Status: StatusDone, Summary: "ok"}, nil
		}})
		args := []string{"X"}
		if dry {
			args = append(args, "--dry-run")
		}

		effects := run(t, app, args...).env(t)["side_effects"].([]any)

		if errors.Is(writeErr, ErrDryRun) != dry || (client == issuex.Client(stub)) == dry {
			t.Errorf("dry run %v: the client is a %T and the write returned %v", dry, client, writeErr)
		}
		if dry && (len(effects) != 1 || at(effects[0], "kind") != "update_issue" || at(effects[0], "state") != StatePlanned) || !dry && len(effects) != 0 {
			t.Errorf("dry run %v: side effects %v", dry, effects)
		}
	}
}

func TestForgeFor_AHostOfNeitherForgeIsUsageAndAnyOtherErrorAForgeFailure(t *testing.T) {
	for _, tc := range []struct {
		err      error
		category string
		hint     any
	}{
		{fmt.Errorf("%w: git.example.com", issuex.ErrAmbiguousForge), CategoryUsage, "GITHUB_API_URL"},
		{fmt.Errorf("%w: x", issuex.ErrUnsupportedForge), CategoryUsage, "GITHUB_API_URL"},
		{errors.New("no adapter"), CategoryForge, nil},
	} {
		app := newApp()
		app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, tc.err }
		app.Register(Command{Path: "X", Dir: true, Run: func(r *Run) (Outcome, error) {
			_, err := r.ForgeFor(recorded)
			return Outcome{}, err
		}})

		env := run(t, app, "X").env(t)

		if at(env, "error", "category") != tc.category || at(env, "error", "fix_hint", "env") != tc.hint {
			t.Errorf("%v: error %v, want %s with the hint %v", tc.err, env["error"], tc.category, tc.hint)
		}
	}
}
