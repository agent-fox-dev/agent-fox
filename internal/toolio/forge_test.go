package toolio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// forgeCommand is a command that takes an optional input and --repo and uses a
// forge.
func forgeCommand(rec *recorder) Command {
	return rec.command(Command{Path: "X", UsesForge: true, Repo: true, Input: InputOptional})
}

// checkoutWithOrigin is a repository whose origin is acme/widgets on GitHub.
func checkoutWithOrigin(t *testing.T) string {
	t.Helper()
	dir := testutil.Repo(t, nil)
	testutil.Git(t, dir, "remote", "add", "origin", "https://github.com/acme/widgets.git")
	return dir
}

func TestForge_TheClientIsForTheIssueElseTheRepoElseTheCheckout(t *testing.T) {
	plain, checkout := t.TempDir(), checkoutWithOrigin(t)
	or := issuex.Repo{Owner: "o", Name: "r"}
	issue, _ := issuex.ParseIssueURL("https://github.com/o/r/issues/7")
	widgets, _ := issuex.DetectRepo(checkout)
	for _, tc := range []struct {
		name   string
		args   []string
		dir    string
		repo   issuex.Repo // of the options
		target issuex.Repo
	}{
		{"an issue", []string{"https://github.com/o/r/issues/7"}, plain, issue.Repo, issue.Repo},
		{"--repo", []string{"fix the bug", "--repo", "o/r"}, checkout, or, or},
		{"the origin of the checkout", []string{"fix the bug"}, checkout, issuex.Repo{}, widgets},
		{"none", []string{"fix the bug"}, plain, issuex.Repo{}, issuex.Repo{}},
	} {
		stub := newStubForge()
		app, got := forgeApp(stub)
		var rec recorder
		app.Register(forgeCommand(&rec))

		r := run(t, app, append(append([]string{"X"}, tc.args...), "--dir", tc.dir)...)

		want := []issuex.Options{{Repo: tc.repo, UserAgent: "af/v0.0.0-test", Dir: tc.dir}}
		if r.code != 0 || !reflect.DeepEqual(*got, want) || rec.last.Forge != issuex.Client(stub) || rec.last.Target != tc.target {
			t.Errorf("%s: exit %d, options %+v, target %+v; want %+v and %+v\n%s", tc.name, r.code, *got, rec.last.Target, want, tc.target, r.stdout)
		}
	}

	app, got := forgeApp(newStubForge())
	var rec recorder
	app.Register(rec.command(Command{Path: "X", Dir: true}))
	if r := run(t, app, "X", "--dir", plain); r.code != 0 || len(*got) != 0 || rec.last.Forge != nil {
		t.Errorf("a command without a forge: exit %d, %d clients built, Run.Forge %v", r.code, len(*got), rec.last.Forge)
	}
}

func TestForge_ABadOrConflictingRepoIsUsage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"fix it", "--repo", "not a repo"}, 2},
		{[]string{"fix it", "--repo", "justname"}, 2},
		{[]string{"https://github.com/c/d/issues/1", "--repo", "a/b"}, 2},
		{[]string{"https://github.com/c/d/issues/1", "--repo", "C/D"}, 0}, // forges do not tell the case apart
	} {
		app, got := forgeApp(newStubForge())
		var rec recorder
		app.Register(forgeCommand(&rec))

		r := run(t, app, append([]string{"X"}, tc.args...)...)

		if r.code != tc.code || rec.ran != 1-tc.code/2 || len(*got) != 1-tc.code/2 {
			t.Errorf("%q: exit %d, ran %d, %d clients built; want exit %d", tc.args, r.code, rec.ran, len(*got), tc.code)
		}
	}
}

func TestForge_ThatCannotBeToldIsUsageForAnIssueAndANoOpForTheRest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		arg      string
		category string // "" when the command runs
	}{
		{"an ambiguous forge and an issue", fmt.Errorf("%w: git.example.com", issuex.ErrAmbiguousForge), "https://github.com/o/r/issues/7", CategoryUsage},
		{"an unsupported forge and an issue", fmt.Errorf("%w: x", issuex.ErrUnsupportedForge), "https://github.com/o/r/issues/7", CategoryUsage},
		{"an ambiguous forge and text", fmt.Errorf("%w: unable to detect", issuex.ErrAmbiguousForge), "fix the bug", ""},
		{"an unsupported forge and text", fmt.Errorf("%w: x", issuex.ErrUnsupportedForge), "fix the bug", ""},
		{"another error", errors.New("adapter broke"), "fix the bug", CategoryForge},
	} {
		var rec recorder
		app := newApp()
		app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, tc.err }
		app.Register(forgeCommand(&rec))

		r := run(t, app, "X", tc.arg)
		env := r.env(t)

		if tc.category == "" {
			if _, noop := rec.last.Forge.(*issuex.NoOpClient); r.code != 0 || !noop || rec.last.Target.Valid() {
				t.Errorf("%s: exit %d, Run.Forge %T, Run.Target %+v; want a no-op client and no repository", tc.name, r.code, rec.last.Forge, rec.last.Target)
			}
			continue
		}
		if rec.ran != 0 || at(env, "error", "category") != tc.category || !strings.Contains(fmt.Sprint(at(env, "error", "message")), tc.err.Error()) {
			t.Errorf("%s: ran %d, error %v; want %s with the error of issuex", tc.name, rec.ran, env["error"], tc.category)
		}
		if tc.category == CategoryUsage && at(env, "error", "fix_hint", "env") != "GITHUB_API_URL" {
			t.Errorf("%s: fix_hint %v, want GITHUB_API_URL", tc.name, at(env, "error", "fix_hint"))
		}
	}
}

func TestForge_TheClientIsClosedOnceBeforeTheEnvelopeIsWritten(t *testing.T) {
	for name, tc := range map[string]struct {
		run func(*Run) (Outcome, error)
		arg string
	}{
		"a return":                         {func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }, "text"},
		"an error":                         {func(*Run) (Outcome, error) { return Outcome{}, errors.New("failed") }, "text"},
		"a panic":                          {func(*Run) (Outcome, error) { panic("boom") }, "text"},
		"an input that cannot be resolved": {func(*Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }, "   "},
	} {
		stub := newStubForge()
		app, _ := forgeApp(stub)
		app.Register(Command{Path: "X", UsesForge: true, Input: InputOptional, Run: tc.run})
		probe := &closeProbe{stub: stub}

		app.Main(context.Background(), []string{"af", "X", tc.arg}, strings.NewReader(""), probe, io.Discard)

		if stub.closes != 1 || !probe.wrote || probe.closesAtWrite != 1 {
			t.Errorf("%s: closed %d times, %d of them before the envelope was written; want once, before", name, stub.closes, probe.closesAtWrite)
		}
	}
}

func TestForge_TheCallersTargetIsTheRepositoryOfEveryInput(t *testing.T) {
	target := issuex.Repo{Host: "github.com", Owner: "acme", Name: "widgets"}
	checkout := checkoutWithOrigin(t)
	testutil.Git(t, checkout, "remote", "set-url", "origin", "https://github.com/other/origin.git")
	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"fix the bug", "--repo", "o/r"}, 0},
		{[]string{"https://github.com/Acme/Widgets/issues/7"}, 0},
		{[]string{"https://github.com/acme/other/issues/7"}, 2},
		{[]string{"https://gitlab.com/acme/widgets/issues/7"}, 2},
	} {
		stub := newStubForge()
		app, got := forgeApp(stub)
		app.Target = target
		var rec recorder
		app.Register(forgeCommand(&rec))

		r := run(t, app, append(append([]string{"X"}, tc.args...), "--dir", checkout)...)

		if r.code != tc.code {
			t.Errorf("%q: exit %d, want %d: %s", tc.args, r.code, tc.code, r.stdout)
		} else if tc.code == 0 && (len(*got) != 1 || (*got)[0].Repo != target || rec.last.Target != target) {
			t.Errorf("%q: options %+v, Run.Target %+v; want the client of %+v", tc.args, *got, rec.last.Target, target)
		} else if tc.code != 0 && (len(*got) != 0 || rec.ran != 0) {
			t.Errorf("%q: %d clients built, ran %d; want neither", tc.args, len(*got), rec.ran)
		}
	}

	app := newApp()
	app.Target = target
	app.NewForge = func(issuex.Options) (issuex.Client, error) { return nil, errors.New("no adapter") }
	app.Register(forgeCommand(&recorder{}))
	if r := run(t, app, "X", "text"); at(r.env(t), "error", "category") != CategoryForge {
		t.Errorf("a client that cannot be built for the target: %s", r.stdout)
	}
}
