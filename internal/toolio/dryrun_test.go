package toolio

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

// recordingForge is an httptest forge that answers the read endpoints of both
// forges with empty documents and counts every request by method.
type recordingForge struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string // "METHOD path"
}

func newRecordingForge(t *testing.T) *recordingForge {
	t.Helper()
	f := &recordingForge{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureFor(r.URL.Path)))
	}))
	t.Cleanup(f.Close)
	return f
}

// fixtureFor is the empty-but-valid answer to a read of path.
func fixtureFor(path string) string {
	switch {
	case strings.HasSuffix(path, "/changes"):
		return `{"changes":[]}`
	case strings.HasSuffix(path, "/approvals"):
		return `{"approved_by":[]}`
	case strings.Contains(path, "/check-runs"):
		return `{"total_count":0,"check_runs":[]}`
	case strings.HasSuffix(path, "/comments"), strings.HasSuffix(path, "/notes"), strings.HasSuffix(path, "/files"),
		strings.HasSuffix(path, "/reviews"), strings.HasSuffix(path, "/pipelines"), strings.HasSuffix(path, "/issues"):
		return `[]`
	case strings.HasSuffix(path, "/pulls/7"):
		return `{"number":7,"state":"open","head":{"sha":"abc123","ref":"fix"},"base":{"ref":"main"}}`
	case strings.HasSuffix(path, "/merge_requests/7"):
		return `{"iid":7,"state":"opened","sha":"abc123"}`
	case strings.HasSuffix(path, "/issues/7"):
		return `{"number":7,"iid":7,"title":"T","state":"open"}`
	}
	return `{"full_name":"o/r","path_with_namespace":"o/r","default_branch":"main"}`
}

// writes counts the requests that are not reads.
func (f *recordingForge) writes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if !strings.HasPrefix(r, "GET ") {
			n++
		}
	}
	return n
}

func (f *recordingForge) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

// closeCounter counts the closes of the client it wraps.
type closeCounter struct {
	issuex.Client
	closes int
}

func (c *closeCounter) Close() error {
	c.closes++
	return c.Client.Close()
}

// MaxBodyLength forwards the limit of the forge behind the wrapper.
func (c *closeCounter) MaxBodyLength() int { return issuex.MaxBodyLength(c.Client) }

// TestDryRunFlagWiresDecorator_TS_05_56 verifies TS-05-56:
// --dry-run sets Run.DryRun and hands the command the decorator.
// Verifies: 05-REQ-8.6
func TestDryRunFlagWiresDecorator_TS_05_56(t *testing.T) {
	var gotRun *Run
	command := Command{Path: "X", UsesForge: true, DryRun: true, Input: InputOptional, Run: func(r *Run) (Outcome, error) {
		gotRun = r
		return Outcome{Status: StatusDone}, nil
	}}
	stub := newStubForge()
	var opts []issuex.Options
	app := forgeApp(stub, &opts)
	app.Register(command)
	plain := command
	plain.Path, plain.DryRun = "plain", false
	app.Register(plain)

	invoke(t, app, "X", "fix it", "--dry-run")
	if gotRun == nil || !gotRun.DryRun {
		t.Fatalf("Run.DryRun = false with --dry-run")
	}
	decorator, ok := gotRun.Forge.(*dryRunForge)
	if !ok || decorator.inner != issuex.Client(stub) {
		t.Errorf("Run.Forge is a %T, want the decorator around the inner client", gotRun.Forge)
	}

	invoke(t, app, "X", "fix it")
	if gotRun.DryRun || gotRun.Forge != issuex.Client(stub) {
		t.Errorf("without the flag: Run.DryRun = %v, Run.Forge = %T; want false and the inner client", gotRun.DryRun, gotRun.Forge)
	}

	code, stdout, _ := invoke(t, app, "plain", "fix it", "--dry-run")
	if code != 2 || decode(t, stdout)["status"] != "usage" {
		t.Errorf("--dry-run for a command that did not opt in: exit code %d, status %v; want 2, usage", code, decode(t, stdout)["status"])
	}
}

// forgeCase is one forge the decorator is tried against.
type forgeCase struct {
	name  string
	build func(url, token string) issuex.Client
	limit int
}

var forgeCases = []forgeCase{
	{"github", func(url, token string) issuex.Client {
		c, _ := issuex.NewGitHub(issuex.Options{BaseURL: url, Token: token})
		return c
	}, 65536},
	{"gitlab", func(url, token string) issuex.Client {
		c, _ := issuex.NewGitLab(issuex.Options{BaseURL: url, Token: token})
		return c
	}, 1048576},
}

// TestDryRunPassesReadsThrough_TS_05_57 verifies TS-05-57:
// DryRunForge passes reads through and keeps the inner body limit.
// Verifies: 05-REQ-8.7
func TestDryRunPassesReadsThrough_TS_05_57(t *testing.T) {
	ctx := context.Background()
	repo := issuex.Repo{Owner: "o", Name: "r"}
	ref := issuex.IssueRef{Repo: repo, Number: 7}
	prRef := issuex.IssueRef{Repo: repo, Number: 7, IsPullRequest: true}

	for _, fc := range forgeCases {
		t.Run(fc.name, func(t *testing.T) {
			forge := newRecordingForge(t)
			inner := &closeCounter{Client: fc.build(forge.URL, "t")}
			dry := DryRunForge(inner)

			reads := []struct {
				name string
				call func(c issuex.Client) (any, error)
			}{
				{"GetRepository", func(c issuex.Client) (any, error) { return c.GetRepository(ctx, repo) }},
				{"ReadIssue", func(c issuex.Client) (any, error) { return c.ReadIssue(ctx, ref) }},
				{"ListIssues", func(c issuex.Client) (any, error) { return c.ListIssues(ctx, repo, issuex.IssueFilter{}) }},
				{"ListComments", func(c issuex.Client) (any, error) { return c.ListComments(ctx, ref) }},
				{"ReadPullRequest", func(c issuex.Client) (any, error) { return c.ReadPullRequest(ctx, prRef) }},
				{"ReadChangedFiles", func(c issuex.Client) (any, error) { return c.ReadChangedFiles(ctx, prRef) }},
				{"GetPRState", func(c issuex.Client) (any, error) { return c.GetPRState(ctx, prRef) }},
				{"GetCIChecks", func(c issuex.Client) (any, error) { return c.GetCIChecks(ctx, prRef) }},
				{"GetPRReviews", func(c issuex.Client) (any, error) { return c.GetPRReviews(ctx, prRef) }},
			}
			for _, rd := range reads {
				before := forge.count()
				got, gotErr := rd.call(dry)
				if forge.count() == before {
					t.Errorf("%s did not reach the server", rd.name)
				}
				want, wantErr := rd.call(inner)
				if gotErr != nil || wantErr != nil {
					t.Errorf("%s: errors %v (decorated) and %v (inner), want none", rd.name, gotErr, wantErr)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: decorated result %+v, inner result %+v", rd.name, got, want)
				}
			}

			if dry.Authenticated() != inner.Authenticated() || !dry.Authenticated() {
				t.Errorf("Authenticated() = %v, want the inner client's (true)", dry.Authenticated())
			}
			if anonymous := DryRunForge(fc.build(forge.URL, "")); anonymous.Authenticated() {
				t.Error("the decorator of a client with no token reports it is authenticated")
			}
			if got := issuex.MaxBodyLength(dry); got != fc.limit {
				t.Errorf("MaxBodyLength = %d, want %d", got, fc.limit)
			}
			if err := dry.Close(); err != nil || inner.closes != 1 {
				t.Errorf("Close() = %v, inner closed %d times; want nil and 1", err, inner.closes)
			}
			if forge.writes() != 0 {
				t.Errorf("the reads sent %d write requests", forge.writes())
			}
		})
	}
}

// write is one of the forge writes a dry run refuses.
type write struct {
	kind   string
	byRepo bool // the target is a repository, not an issue
	call   func(c issuex.Client, repo issuex.Repo, ref issuex.IssueRef) error
}

var dryRunWrites = []write{
	{"create_issue", true, func(c issuex.Client, repo issuex.Repo, _ issuex.IssueRef) error {
		_, err := c.CreateIssue(context.Background(), repo, issuex.CreateIssueRequest{Title: "t"})
		return err
	}},
	{"update_issue", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		_, err := c.UpdateIssue(context.Background(), ref, issuex.UpdateIssueRequest{Title: "t"})
		return err
	}},
	{"close_issue", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		return c.CloseIssue(context.Background(), ref, "done")
	}},
	{"add_comment", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		_, err := c.AddComment(context.Background(), ref, "hello")
		return err
	}},
	{"add_labels", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		return c.AddLabels(context.Background(), ref, []string{"bug"})
	}},
	{"remove_label", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		return c.RemoveLabel(context.Background(), ref, "bug")
	}},
	{"create_label", true, func(c issuex.Client, repo issuex.Repo, _ issuex.IssueRef) error {
		return c.CreateLabel(context.Background(), repo, issuex.Label{Name: "bug"})
	}},
	{"create_pull_request", true, func(c issuex.Client, repo issuex.Repo, _ issuex.IssueRef) error {
		_, err := c.CreatePullRequest(context.Background(), repo, issuex.CreatePullRequestRequest{Title: "t", Head: "h", Base: "b"})
		return err
	}},
	{"post_review_comment", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		return c.PostReviewComment(context.Background(), ref, "looks fine")
	}},
	{"merge_pull_request", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		_, err := c.MergePullRequest(context.Background(), ref, issuex.MergeOptions{})
		return err
	}},
	{"close_pull_request", false, func(c issuex.Client, _ issuex.Repo, ref issuex.IssueRef) error {
		return c.ClosePullRequest(context.Background(), ref)
	}},
}

// TestDryRunNeverWrites_TS_05_58 verifies TS-05-58:
// No write method ever sends a request and each is recorded as planned.
// Verifies: 05-REQ-8.8
func TestDryRunNeverWrites_TS_05_58(t *testing.T) {
	rng := rand.New(rand.NewPCG(58, 1))
	name := func() string { return fmt.Sprintf("n%d", rng.IntN(1000)) }

	for _, fc := range forgeCases {
		t.Run(fc.name, func(t *testing.T) {
			forge := newRecordingForge(t)
			var effects []SideEffect
			dry := DryRunForge(fc.build(forge.URL, "t"), func(s SideEffect) { effects = append(effects, s) })

			var want []SideEffect
			for i := range 150 {
				w := dryRunWrites[rng.IntN(len(dryRunWrites))]
				repo := issuex.Repo{Owner: name(), Name: name()}
				ref := issuex.IssueRef{Repo: repo, Number: 1 + rng.IntN(500), IsPullRequest: rng.IntN(2) == 0}

				err := w.call(dry, repo, ref)

				if !errors.Is(err, ErrDryRun) {
					t.Fatalf("#%d %s: error %v does not wrap ErrDryRun", i, w.kind, err)
				}
				target := ref.String()
				if w.byRepo {
					target = repo.String()
				}
				want = append(want, SideEffect{Kind: w.kind, Target: target, State: StatePlanned})
				if !reflect.DeepEqual(effects, want) {
					t.Fatalf("#%d %s: side effects %+v, want %+v", i, w.kind, effects[len(effects)-1], want[len(want)-1])
				}
			}
			if forge.count() != 0 {
				t.Errorf("the forge saw %d requests, want none (%d writes)", forge.count(), forge.writes())
			}
		})
	}

	// Without a recorder the writes are still refused.
	forge := newRecordingForge(t)
	dry := DryRunForge(forgeCases[0].build(forge.URL, "t"))
	if err := dryRunWrites[3].call(dry, issuex.Repo{Owner: "o", Name: "r"}, issuex.IssueRef{Number: 1}); !errors.Is(err, ErrDryRun) || forge.count() != 0 {
		t.Errorf("without a recorder: error %v, %d requests; want ErrDryRun and none", err, forge.count())
	}
}

// TestRefusedWritesAreSideEffects_TS_05_59 verifies TS-05-59:
// A command's refused writes appear as planned side effects in the envelope.
// Verifies: 05-REQ-8.8, 05-REQ-8.6
func TestRefusedWritesAreSideEffects_TS_05_59(t *testing.T) {
	forge := newRecordingForge(t)
	var errs []error
	app := newTestApp()
	app.NewForge = func(o issuex.Options) (issuex.Client, error) {
		return issuex.NewGitHub(issuex.Options{BaseURL: forge.URL, Token: "t", Repo: o.Repo})
	}
	app.Register(Command{Path: "X", UsesForge: true, DryRun: true, Repo: true, Run: func(r *Run) (Outcome, error) {
		ref := issuex.IssueRef{Repo: r.Target, Number: 3}
		_, err1 := r.Forge.AddComment(r.Ctx, ref, "hello")
		err2 := r.Forge.AddLabels(r.Ctx, ref, []string{"bug"})
		_, err3 := r.Forge.CreateIssue(r.Ctx, r.Target, issuex.CreateIssueRequest{Title: "t"})
		errs = []error{err1, err2, err3}
		return Outcome{Status: StatusDone, Summary: "planned"}, nil
	}})

	code, stdout, _ := invoke(t, app, "X", "--repo", "o/r", "--dry-run")
	env := decode(t, stdout)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v; want 0, done\n%s", code, env["status"], stdout)
	}
	want := []any{
		map[string]any{"kind": "add_comment", "target": "o/r#3", "state": "planned"},
		map[string]any{"kind": "add_labels", "target": "o/r#3", "state": "planned"},
		map[string]any{"kind": "create_issue", "target": "o/r", "state": "planned"},
	}
	if got := env["side_effects"]; !reflect.DeepEqual(got, want) {
		t.Errorf("side_effects = %v, want %v", got, want)
	}
	if forge.writes() != 0 {
		t.Errorf("the forge saw %d write requests, want none", forge.writes())
	}
	for i, err := range errs {
		if !errors.Is(err, ErrDryRun) {
			t.Errorf("write %d returned %v, want an error wrapping ErrDryRun", i, err)
		}
	}
}

// TestDryRunWithoutAForge guards the rule behind TS-05-56: a command that
// opts into --dry-run is told so even when it uses no forge, so that it can
// skip the effects of its own, such as a commit.
func TestDryRunWithoutAForge(t *testing.T) {
	var dry bool
	var forge issuex.Client
	app := newTestApp()
	app.Register(Command{Path: "X", DryRun: true, Run: func(r *Run) (Outcome, error) {
		dry, forge = r.DryRun, r.Forge
		return Outcome{Status: StatusDone}, nil
	}})

	if code, _, _ := invoke(t, app, "X", "--dry-run"); code != 0 || !dry || forge != nil {
		t.Errorf("--dry-run: exit code %d, Run.DryRun %v, Run.Forge %v; want 0, true, nil", code, dry, forge)
	}
	if invoke(t, app, "X"); dry {
		t.Error("Run.DryRun is true without the flag")
	}
}
