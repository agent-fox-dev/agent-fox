package toolio

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestDryRun_TheFlagIsToldToTheCommandAndWrapsItsForge(t *testing.T) {
	stub := newStubForge()
	app, _ := forgeApp(stub)
	var withForge, without recorder
	app.Register(withForge.command(Command{Path: "X", UsesForge: true, DryRun: true}))
	app.Register(without.command(Command{Path: "local", DryRun: true}))

	run(t, app, "X", "--dry-run")
	if d, ok := withForge.last.Forge.(*dryRunForge); !withForge.last.DryRun || !ok || d.inner != issuex.Client(stub) {
		t.Errorf("--dry-run: Run.DryRun %v, Run.Forge %T; want the decorator around the client", withForge.last.DryRun, withForge.last.Forge)
	}
	run(t, app, "X")
	if withForge.last.DryRun || withForge.last.Forge != issuex.Client(stub) {
		t.Errorf("no --dry-run: Run.DryRun %v, Run.Forge %T; want the client itself", withForge.last.DryRun, withForge.last.Forge)
	}
	// A command without a forge is told so it can skip effects of its own, such as a commit.
	if r := run(t, app, "local", "--dry-run"); r.code != 0 || !without.last.DryRun || without.last.Forge != nil {
		t.Errorf("exit %d, Run.DryRun %v, Run.Forge %v", r.code, without.last.DryRun, without.last.Forge)
	}
}

// forges are the real clients the decorator is tried around, and their body
// limits.
var forges = []struct {
	name  string
	build func(url, token string) (issuex.Client, error)
	limit int
}{
	{"github", func(url, token string) (issuex.Client, error) {
		return issuex.NewGitHub(issuex.Options{BaseURL: url, Token: token})
	}, 65536},
	{"gitlab", func(url, token string) (issuex.Client, error) {
		return issuex.NewGitLab(issuex.Options{BaseURL: url, Token: token})
	}, 1048576},
}

func TestDryRunForge_PassesTheReadsThrough(t *testing.T) {
	ctx := context.Background()
	repo := issuex.Repo{Owner: "o", Name: "r"}
	ref := issuex.IssueRef{Repo: repo, Number: 7}
	pr := issuex.IssueRef{Repo: repo, Number: 7, IsPullRequest: true}
	reads := map[string]func(issuex.Client) (any, error){
		"GetRepository":    func(c issuex.Client) (any, error) { return c.GetRepository(ctx, repo) },
		"ReadIssue":        func(c issuex.Client) (any, error) { return c.ReadIssue(ctx, ref) },
		"ListIssues":       func(c issuex.Client) (any, error) { return c.ListIssues(ctx, repo, issuex.IssueFilter{}) },
		"ListComments":     func(c issuex.Client) (any, error) { return c.ListComments(ctx, ref) },
		"ReadPullRequest":  func(c issuex.Client) (any, error) { return c.ReadPullRequest(ctx, pr) },
		"ReadChangedFiles": func(c issuex.Client) (any, error) { return c.ReadChangedFiles(ctx, pr) },
		"GetPRState":       func(c issuex.Client) (any, error) { return c.GetPRState(ctx, pr) },
		"GetCIChecks":      func(c issuex.Client) (any, error) { return c.GetCIChecks(ctx, pr) },
		"GetPRReviews":     func(c issuex.Client) (any, error) { return c.GetPRReviews(ctx, pr) },
	}
	for _, f := range forges {
		forge := newRecordingForge(t)
		client, err := f.build(forge.URL, "t")
		if err != nil {
			t.Fatal(err)
		}
		inner := &stubForge{Client: client}
		dry := DryRunForge(inner)
		for name, read := range reads {
			before := len(forge.seen())
			got, gotErr := read(dry)
			reached := len(forge.seen()) > before
			want, wantErr := read(inner)
			if !reached || gotErr != nil || wantErr != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("%s %s: reached the forge %v, %+v (%v); the client itself %+v (%v)", f.name, name, reached, got, gotErr, want, wantErr)
			}
		}
		anonymous, _ := f.build(forge.URL, "")
		if !dry.Authenticated() || DryRunForge(anonymous).Authenticated() || issuex.MaxBodyLength(dry) != f.limit || forge.writes() != 0 {
			t.Errorf("%s: authenticated %v, body limit %d, %d writes", f.name, dry.Authenticated(), issuex.MaxBodyLength(dry), forge.writes())
		}
		if err := dry.Close(); err != nil || inner.closes != 1 {
			t.Errorf("%s: Close = %v, the client was closed %d times", f.name, err, inner.closes)
		}
	}
}

func TestDryRunForge_RefusesEveryWriteAndRecordsItAsPlanned(t *testing.T) {
	ctx := context.Background()
	repo := issuex.Repo{Owner: "o", Name: "r"}
	ref := issuex.IssueRef{Repo: repo, Number: 3, IsPullRequest: true}
	writes := []struct {
		kind, target string
		call         func(issuex.Client) error
	}{
		{"create_issue", "o/r", func(c issuex.Client) error {
			_, err := c.CreateIssue(ctx, repo, issuex.CreateIssueRequest{Title: "t"})
			return err
		}},
		{"update_issue", ref.String(), func(c issuex.Client) error {
			_, err := c.UpdateIssue(ctx, ref, issuex.UpdateIssueRequest{Title: "t"})
			return err
		}},
		{"close_issue", ref.String(), func(c issuex.Client) error { return c.CloseIssue(ctx, ref, "done") }},
		{"add_comment", ref.String(), func(c issuex.Client) error { _, err := c.AddComment(ctx, ref, "hello"); return err }},
		{"add_labels", ref.String(), func(c issuex.Client) error { return c.AddLabels(ctx, ref, []string{"bug"}) }},
		{"remove_label", ref.String(), func(c issuex.Client) error { return c.RemoveLabel(ctx, ref, "bug") }},
		{"create_label", "o/r", func(c issuex.Client) error { return c.CreateLabel(ctx, repo, issuex.Label{Name: "bug"}) }},
		{"create_pull_request", "o/r", func(c issuex.Client) error {
			_, err := c.CreatePullRequest(ctx, repo, issuex.CreatePullRequestRequest{Title: "t", Head: "h", Base: "b"})
			return err
		}},
		{"post_review_comment", ref.String(), func(c issuex.Client) error { return c.PostReviewComment(ctx, ref, "fine") }},
		{"merge_pull_request", ref.String(), func(c issuex.Client) error {
			_, err := c.MergePullRequest(ctx, ref, issuex.MergeOptions{})
			return err
		}},
		{"close_pull_request", ref.String(), func(c issuex.Client) error { return c.ClosePullRequest(ctx, ref) }},
	}
	for _, f := range forges {
		forge := newRecordingForge(t)
		client, _ := f.build(forge.URL, "t")
		var effects []SideEffect
		dry := DryRunForge(client, func(s SideEffect) { effects = append(effects, s) })
		silent := DryRunForge(client)
		for _, w := range writes {
			if err := w.call(dry); !errors.Is(err, ErrDryRun) || !strings.Contains(err.Error(), w.kind+" "+w.target) {
				t.Errorf("%s %s: %v, want a refusal wrapping ErrDryRun", f.name, w.kind, err)
			}
			if want := (SideEffect{Kind: w.kind, Target: w.target, State: StatePlanned}); len(effects) == 0 || effects[len(effects)-1] != want {
				t.Errorf("%s %s: side effects %+v, want %+v last", f.name, w.kind, effects, want)
			}
			if err := w.call(silent); !errors.Is(err, ErrDryRun) {
				t.Errorf("%s %s without a recorder: %v", f.name, w.kind, err)
			}
		}
		if seen := forge.seen(); len(seen) != 0 {
			t.Errorf("%s: the forge saw %v, want nothing", f.name, seen)
		}
	}
}

func TestDryRun_ReadsAnIssueAndWritesNothing(t *testing.T) {
	forge := newRecordingForge(t)
	githubEnv(t, forge.URL)
	checkout := testutil.Repo(t, nil)
	var writeErrs []error
	app := newApp() // the forge client is the real one
	app.Register(Command{Path: "X", Input: InputRequired, UsesForge: true, UsesRunDir: true, DryRun: true, Run: func(r *Run) (Outcome, error) {
		ref := issuex.IssueRef{Repo: r.Target, Number: 7}
		_, commentErr := r.Forge.AddComment(r.Ctx, ref, "a diagnosis")
		_, prErr := r.Forge.CreatePullRequest(r.Ctx, r.Target, issuex.CreatePullRequestRequest{Title: "t", Head: "h", Base: "main"})
		writeErrs = []error{commentErr, prErr}
		return Outcome{Status: StatusDone, Summary: "planned two writes"}, nil
	}})

	r := run(t, app, "X", forge.URL+"/o/r/issues/7", "--dry-run", "--dir", checkout)
	env := r.env(t)

	if r.code != 0 || at(env, "input", "kind") != "issue" || r.stderr != "" {
		t.Fatalf("exit %d, stderr %q, envelope %s", r.code, r.stderr, r.stdout)
	}
	want := []any{
		map[string]any{"kind": "add_comment", "target": "o/r#7", "state": "planned"},
		map[string]any{"kind": "create_pull_request", "target": "o/r", "state": "planned"},
	}
	if !reflect.DeepEqual(env["side_effects"], want) || !errors.Is(writeErrs[0], ErrDryRun) || !errors.Is(writeErrs[1], ErrDryRun) {
		t.Errorf("side effects %v, write errors %v; want %v refused", env["side_effects"], writeErrs, want)
	}
	seen := forge.seen()
	if forge.writes() != 0 || !slices.Contains(seen, "GET /repos/o/r/issues/7") || !slices.Contains(seen, "GET /repos/o/r/issues/7/comments") {
		t.Errorf("the forge saw %v; want the reads of the issue and its comments and no write", seen)
	}
	runDir := filepath.Join(agentFoxDir(checkout), "run", env["run_id"].(string))
	if testutil.ReadFile(t, filepath.Join(runDir, "envelope.json")) != r.stdout || len(testutil.JSONLines(t, filepath.Join(runDir, "events.jsonl"))) < 2 {
		t.Errorf("the run directory %s lacks the envelope or the events", runDir)
	}
	if status := testutil.Git(t, checkout, "status", "--porcelain"); status != "" {
		t.Errorf("git status --porcelain:\n%s", status)
	}
}
