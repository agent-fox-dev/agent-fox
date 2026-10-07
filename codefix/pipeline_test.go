package codefix

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// The tests below drive the REAL pipeline — the real pre-flight, the real
// git, the real verification comparison, the real rendering — with the model
// half replaced by a scripted brain. The split that makes the program
// trustworthy is the same split that makes it testable.

// scriptedBrain stands in for the two model phases.
type scriptedBrain struct {
	analysis Analysis
	impl     Implementation
	// edit runs in place of the implementation phase's file writes.
	edit func(root string) error
	// analyzeErr and implementErr fail a phase.
	analyzeErr, implementErr error

	// analyzeCost is the spend the analyse phase reports.
	analyzeCost float64

	// review answers the review of cited requirements; nil answers every id
	// in scope met.
	review func(in conform.ReviewInput) (conform.Review, error)

	analyzed, implemented int
	implementPrompt       string
	reviewIns             []conform.ReviewInput
}

func (b *scriptedBrain) Review(_ context.Context, in conform.ReviewInput) (conform.Review, agentrun.Result, error) {
	b.reviewIns = append(b.reviewIns, in)
	_ = conform.ReviewPrompt(in) // every prompt must render
	res := agentrun.Result{Name: conform.PhaseReview, Turns: 3}
	if b.review != nil {
		r, err := b.review(in)
		return r, res, err
	}
	r := conform.Review{Summary: "Met."}
	for _, id := range in.Scope.Requirements {
		r.Requirements = append(r.Requirements, conform.RequirementRow{ID: id, Status: conform.StatusImplemented,
			Evidence: "count.go:1 does it"})
	}
	for _, id := range in.Scope.Tests {
		r.Tests = append(r.Tests, conform.TestRow{ID: id, Assessment: conform.AssessAssertsContract,
			Evidence: "count_test.go:1 asserts it"})
	}
	return r, res, nil
}

func (b *scriptedBrain) Analyze(_ context.Context, in analysisInput) (Analysis, agentrun.Result, error) {
	b.analyzed++
	res := agentrun.Result{Name: "analyse", Turns: 2}
	res.Usage.CostUSD = b.analyzeCost
	if b.analyzeErr != nil {
		return Analysis{}, res, b.analyzeErr
	}
	return b.analysis, res, nil
}

func (b *scriptedBrain) Implement(_ context.Context, in implementInput) (Implementation, agentrun.Result, error) {
	b.implemented++
	b.implementPrompt = implementPrompt(in)
	res := agentrun.Result{Name: "implement", Turns: 5}
	if b.implementErr != nil {
		return Implementation{}, res, b.implementErr
	}
	if b.edit != nil {
		if err := b.edit(in.Root); err != nil {
			return Implementation{}, res, err
		}
	}
	return b.impl, res, nil
}

func defaultBrain() *scriptedBrain {
	return &scriptedBrain{
		analysis: Analysis{
			Classification: ClassBug,
			Title:          "stop double counting on retry",
			Summary:        "The counter increments twice when a retry occurs.",
			RootCause:      "count.go increments before and after the retry guard.",
			Approach:       "Move the increment inside the guard.",
			Files:          []FileChange{{Path: "count.go", Change: "move the increment"}},
		},
		impl: Implementation{
			Summary:       "Moved the increment inside the retry guard and added a regression test.",
			CommitSubject: "move the increment inside the retry guard",
			Changes:       []FileChange{{Path: "count.go", Change: "moved the increment"}},
			Tests:         []string{"count_test.go: a retry increments once"},
		},
		edit: func(root string) error {
			if err := os.WriteFile(filepath.Join(root, "count_test.go"), []byte("package x // regression\n"), 0o644); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(root, "count.go"), []byte("package x // fixed\n"), 0o644)
		},
	}
}

// newRepo builds a real repository with a Makefile whose `test` target the
// pipeline will detect and run.
func newRepo(t *testing.T, testExit int) (*tools.Workspace, *gitx.Git) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	ctx := context.Background()
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := gitx.ExecRunner(ctx, dir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	write(t, dir, "count.go", "package x\n")
	// The test target fails while count_test.go exists and count.go is not
	// fixed: the regression test the default brain writes depends on the fix,
	// which is what the revert check looks for.
	write(t, dir, "Makefile", "test:\n\t@[ ! -f count_test.go ] || grep -q fixed count.go\n\t@exit "+itoa(testExit)+"\n")

	g := gitx.New(dir, gitx.ExecRunner)
	g.SetSleep(func(time.Duration) {})
	if _, err := g.CommitAll(ctx, "chore: initial commit\n"); err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws, g
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return string(rune('0' + n)) }

func newOptions(ws *tools.Workspace, g *gitx.Git, b brain) Options {
	return Options{
		Input: toolio.Input{
			Kind: toolio.KindText, Origin: "argument",
			Body: "the widget counter double-counts on retry",
		},
		Workspace:     ws,
		Land:          LandNone,
		Git:           g,
		CheckRunner:   gitx.ExecRunner,
		VerifyTimeout: 30 * time.Second,
		Run:           toolio.NewRun("fix", "test"),
		Progress:      toolio.NewProgress(io.Discard, "fix", false, true),
		brain:         b,
	}
}

func TestPipelineLandsAVerifiedChange(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()

	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stage != "landed" {
		t.Errorf("Stage = %q", got.Stage)
	}
	if got.Verdict != string(checks.VerdictPass) {
		t.Errorf("Verdict = %q", got.Verdict)
	}
	if got.Branch != "fix/stop-double-counting-on-retry" {
		t.Errorf("Branch = %q", got.Branch)
	}
	if got.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q", got.BaseBranch)
	}
	if got.Commit == "" {
		t.Error("no commit was made")
	}
	if got.Pushed {
		t.Error("--land=none must not push")
	}
	if strings.Join(got.ChangedFiles, ",") != "count.go,count_test.go" {
		t.Errorf("ChangedFiles = %v — this list comes from git, not from the model", got.ChangedFiles)
	}

	// The commit is the pipeline's, and its subject carries the type prefix.
	ctx := context.Background()
	out, _, err := gitx.ExecRunner(ctx, ws.Root, []string{"git", "log", "-1", "--format=%s"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "fix: move the increment inside the retry guard") {
		t.Errorf("commit subject = %q", strings.TrimSpace(out))
	}
}

// A run that reports a fix and changed nothing is a failed run, not an empty
// commit. The evidence is git's diff, not the model's report.
func TestPipelineRefusesAnEmptyChange(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.edit = nil // the model claims a fix and writes nothing

	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err == nil {
		t.Fatal("want an error")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryEmpty {
		t.Fatalf("err = %v (%T)", err, err)
	}
	if got.Commit != "" {
		t.Error("nothing should have been committed")
	}
}

// The checks fail after the change: nothing is landed, the work is parked as
// a wip: commit on the branch, and the checkout is back on the base branch.
func TestPipelineRefusesToLandAFailingChange(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.edit = func(root string) error {
		// Break the checks as part of the "fix".
		return os.WriteFile(filepath.Join(root, "Makefile"), []byte("test:\n\t@exit 1\n"), 0o644)
	}

	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err == nil {
		t.Fatal("want an error")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryUnverified {
		t.Fatalf("err = %v", err)
	}
	if got.Stage != "unverified" || got.Verdict != string(checks.VerdictRegressed) {
		t.Errorf("stage/verdict = %q/%q", got.Stage, got.Verdict)
	}
	if got.Commit == "" {
		t.Error("the work should be parked as a commit, not left loose")
	}

	ctx := context.Background()
	branch, err := g.CurrentBranch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Errorf("the checkout is on %q, want the base branch", branch)
	}
	out, _, _ := gitx.ExecRunner(ctx, ws.Root, []string{"git", "log", "-1", "--format=%s", got.Branch})
	if !strings.HasPrefix(strings.TrimSpace(out), "wip:") {
		t.Errorf("the parked commit's subject = %q, want a wip: prefix", strings.TrimSpace(out))
	}
}

// A repository that was already failing must not be reported as a regression:
// the run compares before and after rather than requiring green.
func TestPipelineReportsAPreExistingFailureHonestly(t *testing.T) {
	ws, g := newRepo(t, 1) // red before anything changes
	b := defaultBrain()
	b.edit = func(root string) error {
		return os.WriteFile(filepath.Join(root, "Makefile"), []byte("test:\n\t@exit 0\n"), 0o644)
	}

	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Verdict != string(checks.VerdictRepaired) {
		t.Errorf("Verdict = %q, want the already-failing case named", got.Verdict)
	}
	if !got.Baseline.Ran() || got.Baseline.OK {
		t.Errorf("Baseline = %+v", got.Baseline)
	}
	// The implementation phase was told, so it does not chase failures that
	// were not its to fix.
	if !strings.Contains(b.implementPrompt, "ALREADY FAILING") {
		t.Error("the implementation prompt should state the baseline")
	}
}

// The ambiguity stop happens BEFORE the branch: a run that stops here leaves
// the repository exactly as it found it.
func TestPipelineStopsOnAmbiguityWithoutTouchingTheRepository(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.analysis.Ambiguity = &Ambiguity{
		Question:        "Should a retry reuse the previous id or allocate a new one?",
		InterpretationA: "reuse — the retry is the same logical request",
		InterpretationB: "allocate — the retry is a new attempt",
	}

	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err == nil {
		t.Fatal("want an error")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryAmbiguous {
		t.Fatalf("err = %v", err)
	}
	if got.Stage != "stopped" || got.Ambiguity == nil {
		t.Errorf("result = %+v", got)
	}
	if got.Branch != "" {
		t.Errorf("a branch was created: %q", got.Branch)
	}
	if b.implemented != 0 {
		t.Error("the implementation phase ran after an ambiguity stop")
	}
	if branch, _ := g.CurrentBranch(context.Background()); branch != "main" {
		t.Errorf("the checkout moved to %q", branch)
	}
}

// Pre-flight refuses before anything is fetched, posted or paid for.
func TestPipelineRefusesADirtyTree(t *testing.T) {
	ws, g := newRepo(t, 0)
	write(t, ws.Root, "uncommitted.txt", "x")
	b := defaultBrain()

	_, err := Run(context.Background(), newOptions(ws, g, b))
	if err == nil {
		t.Fatal("want an error")
	}
	var f *Failure
	if !errors.As(err, &f) || f.Stage != "preflight" || f.Category != "usage" {
		t.Fatalf("err = %v", err)
	}
	if b.analyzed != 0 {
		t.Error("the model was called despite a dirty tree")
	}
}

// With no verification command the result is reported as unverified — not as
// a pass — and the run still lands, because the operator asked for no checks.
func TestNoVerifyReportsUnverified(t *testing.T) {
	ws, g := newRepo(t, 0)
	o := newOptions(ws, g, defaultBrain())
	o.NoVerify = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Verdict != string(checks.VerdictUnverified) {
		t.Errorf("Verdict = %q", got.Verdict)
	}
	if got.Commit == "" {
		t.Error("the change should still be committed")
	}
	if strings.Contains(summaryComment(got), "✅ `") {
		t.Error("an unverified run must not render a green tick")
	}
}

// A failure in the analysis phase never reaches git.
func TestAnalysisFailureLeavesTheRepositoryAlone(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.analyzeErr = errors.New("budget exceeded")

	got, err := Run(context.Background(), newOptions(ws, g, b))
	if err == nil {
		t.Fatal("want an error")
	}
	if got.Branch != "" {
		t.Errorf("Branch = %q", got.Branch)
	}
	if branch, _ := g.CurrentBranch(context.Background()); branch != "main" {
		t.Errorf("the checkout moved to %q", branch)
	}
}

// Every comment and the pull request are written by this package, after the
// run, over the REST client — never by the model.
func TestIssueCommentsAndPullRequestAreWrittenByTheProgram(t *testing.T) {
	var comments []string
	var prPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/comments"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			comments = append(comments, body["body"].(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/a/b/issues/1#c"})
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			_ = json.NewDecoder(r.Body).Decode(&prPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number": 5, "html_url": "https://github.com/a/b/pull/5"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	ws, g := newRepo(t, 0)
	ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "a", Name: "b", Host: "github.com"}, Number: 1}
	o := newOptions(ws, g, defaultBrain())
	o.Input.Kind = toolio.KindIssue
	o.Input.Origin = ref.URL()
	o.Input.Issue = &ref
	o.Land = LandPR
	// --dry-run keeps the push out of it while still exercising the comment
	// and pull-request paths' inputs; the push itself is covered in gitx.
	client, err := issuex.NewWithOptions(issuex.Options{
		BaseURL:   srv.URL,
		Repo:      ref.Repo,
		Token:     "t",
		UserAgent: "test",
	})
	if err != nil {
		t.Fatalf("issuex.NewWithOptions: %v", err)
	}
	o.Forge = client
	o.DryRun = true

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("--dry-run posted %d comments", len(comments))
	}
	if got.PullRequestURL != "" {
		t.Error("--dry-run opened a pull request")
	}
	if got.IssueNumber != 1 {
		t.Errorf("IssueNumber = %d", got.IssueNumber)
	}

	// The rendered comment is a pure function of the result, so it can be
	// checked without posting it.
	summary := summaryComment(got)
	for _, want := range []string{
		"## Fix implemented", "count.go", "### Verification", "✅",
		"[`fix`](https://github.com/agent-fox-dev/agent-fox)",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary comment is missing %q:\n%s", want, summary)
		}
	}
}

// sideEffectFixture wires a real repository to a bare origin and a forge
// served by httptest. pullStatus is what the pulls endpoint answers; the
// returned log records every forge call in arrival order.
func sideEffectFixture(t *testing.T, pullStatus int) (Options, *[]string) {
	t.Helper()
	var log []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/comments"):
			log = append(log, "comment")
			_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.com/acme/widgets/issues/1#c"})
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			log = append(log, "open_pr")
			if pullStatus != http.StatusOK {
				http.Error(w, "denied", pullStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 5, "html_url": "https://github.com/acme/widgets/pull/5"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	ws, g := newRepo(t, 0)
	ctx := context.Background()
	origin := filepath.Join(t.TempDir(), "origin.git")
	for _, argv := range [][]string{
		{"git", "init", "-q", "--bare", "-b", "main", origin},
		{"git", "remote", "add", "origin", origin},
	} {
		if out, code, err := gitx.ExecRunner(ctx, ws.Root, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	repo := issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}
	ref := issuex.IssueRef{Repo: repo, Number: 1}
	client, err := issuex.NewWithOptions(issuex.Options{BaseURL: srv.URL, Repo: repo, Token: "t", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	o := newOptions(ws, g, defaultBrain())
	o.Input.Kind = toolio.KindIssue
	o.Input.Origin = ref.URL()
	o.Input.Issue = &ref
	o.Repo = repo
	o.Forge = client
	o.Land = LandPR
	o.PushAttempts = 1
	return o, &log
}

// TS-06-27 (integration): a fix run that comments, pushes and opens a pull
// request records each write with the documented target, in the order the
// writes happened.
// TS-06-29 (integration): the recorded order is the order the forge saw its
// calls in, because each entry is recorded at the write itself.
func TestTS06_27_29_FixRecordsPushPullRequestAndCommentsInOrder(t *testing.T) {
	o, forgeLog := sideEffectFixture(t, http.StatusOK)
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	se := o.Run.SideEffects()
	wantActions := []string{"comment", "push", "open_pr", "comment"}
	if len(se) != len(wantActions) {
		t.Fatalf("SideEffects = %+v, want actions %v", se, wantActions)
	}
	for i, e := range se {
		if e.Action != wantActions[i] || !e.OK || e.Warning != "" {
			t.Errorf("entry %d = %+v, want ok %s", i, e, wantActions[i])
		}
	}
	if want := "origin " + got.Branch; se[1].Target != want {
		t.Errorf("push target = %q, want %q", se[1].Target, want)
	}
	if se[0].Target != "acme/widgets#1" || se[3].Target != "acme/widgets#1" {
		t.Errorf("comment targets = %q, %q", se[0].Target, se[3].Target)
	}
	if se[2].Target != "acme/widgets#5" {
		t.Errorf("open_pr target = %q, want acme/widgets#5", se[2].Target)
	}
	// The forge saw comment, open_pr, comment: the entries for the forge's
	// writes appear in the same relative order.
	var forgeSeen []string
	for _, e := range se {
		if e.Action != "push" {
			forgeSeen = append(forgeSeen, e.Action)
		}
	}
	if strings.Join(forgeSeen, ",") != strings.Join(*forgeLog, ",") {
		t.Errorf("recorded %v, forge saw %v", forgeSeen, *forgeLog)
	}
}

// TS-06-28 (integration): a pull request the forge refuses is recorded
// ok:false with the WarnCode the same call site recorded.
func TestTS06_28_FailedPullRequestSharesTheRecordedWarnCode(t *testing.T) {
	o, _ := sideEffectFixture(t, http.StatusForbidden)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var pr *toolio.SideEffect
	se := o.Run.SideEffects()
	for i := range se {
		if se[i].Action == "open_pr" {
			pr = &se[i]
		}
	}
	if pr == nil {
		t.Fatalf("no open_pr entry in %+v", se)
	}
	if pr.OK || pr.Warning != toolio.WarnPullRequestNotOpened || pr.Target != "acme/widgets" {
		t.Errorf("open_pr entry = %+v", *pr)
	}
	var warned bool
	for _, w := range o.Run.Warnings() {
		if w.Code == pr.Warning {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no Run.Warn carries the entry's code %q: %+v", pr.Warning, o.Run.Warnings())
	}
}

// TS-06-28 (integration): a failed push is recorded ok:false. The push site
// records no Run.Warn (the failure is the run's error), so the entry carries
// no warning code.
func TestTS06_28_FailedPushIsRecordedNotOK(t *testing.T) {
	o, _ := sideEffectFixture(t, http.StatusOK)
	o.Land = LandBranch
	ctx := context.Background()
	if out, code, err := gitx.ExecRunner(ctx, o.Workspace.Root,
		[]string{"git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git")}); err != nil || code != 0 {
		t.Fatalf("set-url: %v %s", err, out)
	}
	if _, err := Run(ctx, o); err == nil {
		t.Fatal("want the push to fail the run")
	}
	var push *toolio.SideEffect
	se := o.Run.SideEffects()
	for i := range se {
		if se[i].Action == "push" {
			push = &se[i]
		}
	}
	if push == nil || push.OK || push.Warning != "" || !strings.HasPrefix(push.Target, "origin ") {
		t.Fatalf("push entry = %+v in %+v", push, se)
	}
}

// TS-06-30 (integration): --dry-run --land=pr records nothing, because every
// recording call site is skipped under a dry run.
func TestTS06_30_DryRunRecordsNoSideEffects(t *testing.T) {
	o, forgeLog := sideEffectFixture(t, http.StatusOK)
	o.DryRun = true
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if se := o.Run.SideEffects(); len(se) != 0 {
		t.Errorf("dry run recorded %+v", se)
	}
	if len(*forgeLog) != 0 {
		t.Errorf("dry run reached the forge: %v", *forgeLog)
	}
	env := o.Run.Envelope(toolio.ExitOK, nil, nil)
	if env.SideEffects != nil {
		t.Errorf("envelope side_effects = %+v", env.SideEffects)
	}
}

// The attribution footer carries the current motto, not the retired
// disclaimer it replaced.
func TestFooterCarriesTheCurrentMotto(t *testing.T) {
	const want = "*Written by [`fix`](https://github.com/agent-fox-dev/agent-fox). Trust, but verify!*"
	const stale = "It is not a substitute for review."
	if footer != want {
		t.Errorf("footer = %q, want %q", footer, want)
	}
	if strings.Contains(footer, stale) {
		t.Errorf("footer still contains the retired disclaimer %q", stale)
	}
}

func TestParseLandMode(t *testing.T) {
	for _, s := range LandModes {
		if _, ok := ParseLandMode(s); !ok {
			t.Errorf("ParseLandMode(%q) rejected a documented mode", s)
		}
	}
	if _, ok := ParseLandMode("merge"); ok {
		t.Error("merge is not a mode: opening a pull request and squash-merging the branch " +
			"are alternatives, and the skill's version does both")
	}
	if m, _ := ParseLandMode("PR"); m != LandPR {
		t.Error("the mode should be case-insensitive")
	}
	if LandNone.Pushes() {
		t.Error("--land=none must not push")
	}
}

// "landed" means what --land asked for actually happened. A stage that always
// said "landed" would be a template rather than a report.
func TestStageNamesWhatTheModeActuallyDid(t *testing.T) {
	ws, g := newRepo(t, 0)
	o := newOptions(ws, g, defaultBrain())
	o.Land = LandNone

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Stage != "landed" || got.Pushed || got.PullRequestURL != "" {
		t.Errorf("--land=none: %+v", got)
	}

	// --land=pr under --dry-run pushes nothing and opens nothing, so the run
	// stops at the commit and must not claim to have landed.
	ws2, g2 := newRepo(t, 0)
	o2 := newOptions(ws2, g2, defaultBrain())
	o2.Land = LandPR
	o2.DryRun = true
	got2, err := Run(context.Background(), o2)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got2.Stage == "landed" {
		t.Errorf("a dry run reported %q", got2.Stage)
	}
	if got2.Stage != "committed" {
		t.Errorf("Stage = %q, want committed", got2.Stage)
	}
}

// The analysis phase is read-only, so it does not get the build toolchain the
// implementation phase needs — those programs compile and write.
func TestTheAnalysisPhaseDoesNotGetTheBuildPrograms(t *testing.T) {
	b := &agentBrain{extraPrograms: []string{"make", "custom-runner"}}

	guard := agentrun.Guard(agentrun.GuardOptions{
		Programs: agentrun.ReadOnlyPrograms, AllowOperators: false, ReadOnlyFiles: true,
	})
	ctx := context.Background()
	call := func(cmd string) core.BeforeToolCallContext {
		return core.BeforeToolCallContext{ToolName: "execute", Arguments: map[string]any{"command": cmd}}
	}
	if d := guard(ctx, call("make test")); !d.Block {
		t.Error("the analysis phase's allowlist admitted make")
	}
	if d := guard(ctx, call("git log --oneline -20")); d.Block {
		t.Errorf("the analysis phase cannot read history: %s", d.Reason)
	}

	// The implementation phase gets them.
	implPrograms := append(append(append([]string(nil), agentrun.ReadOnlyPrograms...), agentrun.BuildPrograms...), b.extraPrograms...)
	implGuard := agentrun.Guard(agentrun.GuardOptions{Programs: implPrograms, AllowOperators: true})
	for _, cmd := range []string{"make test", "custom-runner --all", "go build ./..."} {
		if d := implGuard(ctx, call(cmd)); d.Block {
			t.Errorf("the implementation phase cannot run %q: %s", cmd, d.Reason)
		}
	}
	if d := implGuard(ctx, call("git push")); !d.Block {
		t.Error("the implementation phase was allowed to push")
	}
}

func TestPipelinePull(t *testing.T) {
	ctx := context.Background()

	setupRemotes := func(t *testing.T) (originDir string, ws *tools.Workspace, g *gitx.Git) {
		t.Helper()
		originDir = t.TempDir()
		if out, code, err := gitx.ExecRunner(ctx, originDir, []string{"git", "init", "--bare", "-b", "main"}); err != nil || code != 0 {
			t.Fatalf("git init bare: %v (%d) %s", err, code, out)
		}

		seedDir := t.TempDir()
		for _, argv := range [][]string{
			{"git", "clone", originDir, seedDir},
			{"git", "config", "user.email", "seed@example.com"},
			{"git", "config", "user.name", "Seed"},
			{"git", "config", "commit.gpgsign", "false"},
		} {
			if out, code, err := gitx.ExecRunner(ctx, seedDir, argv); err != nil || code != 0 {
				t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
			}
		}
		write(t, seedDir, "count.go", "package x\n")
		write(t, seedDir, "Makefile", "test:\n\t@exit 0\n")
		seedGit := gitx.New(seedDir, gitx.ExecRunner)
		if _, err := seedGit.CommitAll(ctx, "feat: initial seed\n"); err != nil {
			t.Fatal(err)
		}
		if err := seedGit.Push(ctx, "main", 1, nil); err != nil {
			t.Fatal(err)
		}

		// Also create a dev branch on origin
		if err := seedGit.CreateBranch(ctx, "dev"); err != nil {
			t.Fatal(err)
		}
		write(t, seedDir, "dev.txt", "dev branch file\n")
		if _, err := seedGit.CommitAll(ctx, "feat: dev file\n"); err != nil {
			t.Fatal(err)
		}
		if err := seedGit.Push(ctx, "dev", 1, nil); err != nil {
			t.Fatal(err)
		}

		// Now clone local working dir from origin
		localDir := t.TempDir()
		for _, argv := range [][]string{
			{"git", "clone", originDir, localDir},
			{"git", "config", "user.email", "test@example.com"},
			{"git", "config", "user.name", "Test"},
			{"git", "config", "commit.gpgsign", "false"},
		} {
			if out, code, err := gitx.ExecRunner(ctx, localDir, argv); err != nil || code != 0 {
				t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
			}
		}

		g = gitx.New(localDir, gitx.ExecRunner)
		g.SetSleep(func(time.Duration) {})
		ws, err := tools.NewWorkspace(localDir)
		if err != nil {
			t.Fatal(err)
		}
		return originDir, ws, g
	}

	t.Run("pulls default branch when on another branch", func(t *testing.T) {
		originDir, ws, g := setupRemotes(t)

		// Create a local feature branch behind main
		if err := g.CreateBranch(ctx, "feature/old"); err != nil {
			t.Fatal(err)
		}

		// Push a new commit to main on origin via another clone
		otherDir := t.TempDir()
		for _, argv := range [][]string{
			{"git", "clone", originDir, otherDir},
			{"git", "config", "user.email", "other@example.com"},
			{"git", "config", "user.name", "Other"},
			{"git", "config", "commit.gpgsign", "false"},
		} {
			if out, code, err := gitx.ExecRunner(ctx, otherDir, argv); err != nil || code != 0 {
				t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
			}
		}
		write(t, otherDir, "upstream.txt", "upstream content\n")
		otherGit := gitx.New(otherDir, gitx.ExecRunner)
		if _, err := otherGit.CommitAll(ctx, "feat: upstream change\n"); err != nil {
			t.Fatal(err)
		}
		if err := otherGit.Push(ctx, "main", 1, nil); err != nil {
			t.Fatal(err)
		}

		opts := newOptions(ws, g, defaultBrain())
		opts.Pull = true

		res, err := Run(ctx, opts)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.BaseBranch != "main" {
			t.Errorf("BaseBranch = %q, want main", res.BaseBranch)
		}

		// Verify upstream.txt is present in the workspace
		if _, err := os.Stat(filepath.Join(ws.Root, "upstream.txt")); err != nil {
			t.Errorf("expected upstream.txt to be present after pull: %v", err)
		}
	})

	t.Run("pulls specified branch", func(t *testing.T) {
		_, ws, g := setupRemotes(t)

		opts := newOptions(ws, g, defaultBrain())
		opts.Pull = true
		opts.PullBranch = "dev"

		res, err := Run(ctx, opts)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if res.BaseBranch != "dev" {
			t.Errorf("BaseBranch = %q, want dev", res.BaseBranch)
		}
		if _, err := os.Stat(filepath.Join(ws.Root, "dev.txt")); err != nil {
			t.Errorf("expected dev.txt to be present after checking out dev: %v", err)
		}
	})

	t.Run("fails on merge conflict before baseline or model run", func(t *testing.T) {
		originDir, ws, g := setupRemotes(t)

		// Create a local commit on main that conflicts with origin/main
		write(t, ws.Root, "count.go", "package x // local change\n")
		if _, err := g.CommitAll(ctx, "feat: local commit\n"); err != nil {
			t.Fatal(err)
		}

		// In another clone, commit conflicting change to main and push
		otherDir := t.TempDir()
		for _, argv := range [][]string{
			{"git", "clone", originDir, otherDir},
			{"git", "config", "user.email", "other@example.com"},
			{"git", "config", "user.name", "Other"},
			{"git", "config", "commit.gpgsign", "false"},
		} {
			if out, code, err := gitx.ExecRunner(ctx, otherDir, argv); err != nil || code != 0 {
				t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
			}
		}
		write(t, otherDir, "count.go", "package x // remote conflicting change\n")
		otherGit := gitx.New(otherDir, gitx.ExecRunner)
		if _, err := otherGit.CommitAll(ctx, "feat: remote commit\n"); err != nil {
			t.Fatal(err)
		}
		if err := otherGit.Push(ctx, "main", 1, nil); err != nil {
			t.Fatal(err)
		}

		brain := defaultBrain()
		opts := newOptions(ws, g, brain)
		opts.Pull = true

		res, err := Run(ctx, opts)
		if err == nil {
			t.Fatal("expected Run to fail on merge conflict")
		}
		var f *Failure
		if !errors.As(err, &f) || f.Stage != "preflight" || f.Category != CategoryGit {
			t.Fatalf("expected preflight git failure, got %v (res=%+v)", err, res)
		}
		if brain.analyzed != 0 || brain.implemented != 0 {
			t.Errorf("brain was called: analyzed=%d, implemented=%d", brain.analyzed, brain.implemented)
		}
	})

	t.Run("without pull flag does not pull", func(t *testing.T) {
		originDir, ws, g := setupRemotes(t)

		// Create local branch feature/local
		if err := g.CreateBranch(ctx, "feature/local"); err != nil {
			t.Fatal(err)
		}

		// Push a change to origin/main
		otherDir := t.TempDir()
		for _, argv := range [][]string{
			{"git", "clone", originDir, otherDir},
			{"git", "config", "user.email", "other@example.com"},
			{"git", "config", "user.name", "Other"},
			{"git", "config", "commit.gpgsign", "false"},
		} {
			if out, code, err := gitx.ExecRunner(ctx, otherDir, argv); err != nil || code != 0 {
				t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
			}
		}
		write(t, otherDir, "notpulled.txt", "should not be pulled\n")
		otherGit := gitx.New(otherDir, gitx.ExecRunner)
		if _, err := otherGit.CommitAll(ctx, "feat: notpulled\n"); err != nil {
			t.Fatal(err)
		}
		if err := otherGit.Push(ctx, "main", 1, nil); err != nil {
			t.Fatal(err)
		}

		opts := newOptions(ws, g, defaultBrain())
		opts.Pull = false

		res, err := Run(ctx, opts)
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := os.Stat(filepath.Join(ws.Root, "notpulled.txt")); !os.IsNotExist(err) {
			t.Errorf("notpulled.txt should not exist in workspace, err=%v", err)
		}
		// The run branched from feature/local, so that is what the pull
		// request targets, although origin advertises main (issue #217).
		if res.BaseBranch != "feature/local" {
			t.Errorf("BaseBranch = %q, want the checked-out feature/local", res.BaseBranch)
		}
	})
}

// A report that defines acceptance criteria is answered criterion by
// criterion: the ids come from the report, the verdicts from the model, and
// the section that pairs them is rendered by this package — so "Fix
// implemented" can never be a claim about the criteria in general.
func TestCriteriaAreAnsweredOneByOneInTheSummary(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.impl.CriteriaVerdicts = []CriterionVerdict{
		{ID: "AC-1", Verdict: CriterionPass, Evidence: "count.go increments inside the retry " +
			"guard; count_test.go TestRetryIncrementsOnce passes"},
		{ID: "AC-2", Verdict: CriterionPass, Evidence: "count.go emits the metric after the " +
			"guard; count_test.go TestRetryEmitsOneMetric passes"},
	}
	o := newOptions(ws, g, b)
	o.Input.Body = issueWithCriteria

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got.AcceptanceCriteria) != 2 {
		t.Fatalf("AcceptanceCriteria = %+v", got.AcceptanceCriteria)
	}
	if got.CriteriaOutcome != CriterionPass {
		t.Errorf("CriteriaOutcome = %q", got.CriteriaOutcome)
	}
	// Both phases were told what the change is measured against.
	if !strings.Contains(b.implementPrompt, "AC-2") {
		t.Error("the implementation prompt does not carry the criteria")
	}

	summary := summaryComment(got)
	for _, want := range []string{
		"### Per-criterion verdicts",
		"- ✅ **AC-1**: PASS",
		"  - Evidence: count.go increments inside the retry guard;",
		"- ✅ **AC-2**: PASS",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary comment is missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "Not every acceptance criterion is met") {
		t.Error("every criterion passed and the comment says otherwise")
	}
	// The pull request carries the same answers, one heading level up.
	if !strings.Contains(pullRequestBody(got), "## Per-criterion verdicts") {
		t.Error("the pull-request body does not carry the verdicts")
	}
	// The analysis comment, posted before any code, names what the work is
	// measured against.
	analysis := analysisComment(b.analysis, got.AcceptanceCriteria, got.Branch, "make test",
		got.Baseline)
	if !strings.Contains(analysis, "### Acceptance criteria") ||
		!strings.Contains(analysis, "**AC-1:**") {
		t.Errorf("the analysis comment does not list the criteria:\n%s", analysis)
	}
}

// A criterion the change does not meet is reported as unmet — in the result,
// in the run's warnings and in the comment — even when the project's checks
// are green. The two are different questions and the report answers both.
func TestAnUnmetCriterionIsReportedEvenWhenTheChecksPass(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.impl.CriteriaVerdicts = []CriterionVerdict{
		{ID: "AC-1", Verdict: CriterionPass, Evidence: "count.go increments inside the retry " +
			"guard; count_test.go TestRetryIncrementsOnce passes"},
		{ID: "AC-2", Verdict: CriterionFail, Evidence: "the metric is still emitted twice; the " +
			"emitter is in another package and was left alone"},
	}
	o := newOptions(ws, g, b)
	o.Input.Body = issueWithCriteria

	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Verdict != string(checks.VerdictPass) {
		t.Fatalf("the checks passed; Verdict = %q", got.Verdict)
	}
	if got.CriteriaOutcome != CriterionFail {
		t.Errorf("CriteriaOutcome = %q, want the unmet criterion named", got.CriteriaOutcome)
	}
	summary := summaryComment(got)
	if !strings.Contains(summary, "- ❌ **AC-2**: FAIL") {
		t.Errorf("the summary comment does not report the failure:\n%s", summary)
	}
	if !strings.Contains(summary, "Not every acceptance criterion is met") {
		t.Error("a green check run must not stand in for an unmet criterion")
	}
	var warned bool
	for _, w := range o.Run.Warnings() {
		if strings.Contains(w.Message, "AC-2") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the run does not warn about the unmet criterion: %v", o.Run.Warnings())
	}
}

// A criterion that reached the renderer with no verdict is rendered as
// unanswered rather than dropped. The submit tool refuses that submission, so
// this is the belt to its braces: a comment cannot quietly lose a criterion.
func TestAMissingVerdictIsRenderedAsMissing(t *testing.T) {
	criteria := ParseCriteria(issueWithCriteria)
	section := criteriaVerdictSection("###", criteria, &Implementation{
		CriteriaVerdicts: []CriterionVerdict{{ID: "AC-1", Verdict: CriterionPass,
			Evidence: "count.go increments inside the retry guard; the test passes"}},
	})
	if !strings.Contains(section, "- ⚠️ **AC-2**: NOT REPORTED") {
		t.Errorf("section:\n%s", section)
	}
	if criteriaVerdictSection("###", nil, nil) != "" {
		t.Error("a report with no criteria gets no section")
	}
}

// TS-04-13 (unit): codefix declares forge-neutral Options, CategoryForge constant, and issueRef type alias
// Verifies: 04-REQ-4.1
func TestTS0413_CodefixTypesAndConstants(t *testing.T) {
	var o Options
	var _ issuex.Repo = o.Repo
	var _ issuex.Client = o.Forge
	if CategoryForge != "forge" {
		t.Errorf("CategoryForge = %q, want %q", CategoryForge, "forge")
	}
	if CategoryGitHub != CategoryForge {
		t.Errorf("CategoryGitHub = %q, want %q", CategoryGitHub, CategoryForge)
	}
	var _ issueRef = (*issuex.IssueRef)(nil)
}

// TS-04-14 (integration): codefix preflight detects repository via issuex.DetectRepo
// Verifies: 04-REQ-4.2
func TestTS0414_PreflightDetectsRepo(t *testing.T) {
	ws, g := newRepo(t, 0)
	ctx := context.Background()

	// Add origin remote to the repo
	if out, code, err := gitx.ExecRunner(ctx, ws.Root, []string{
		"git", "remote", "add", "origin", "https://github.com/acme/repo.git",
	}); err != nil || code != 0 {
		t.Fatalf("git remote add: %v (%d) %s", err, code, out)
	}

	result := &Result{Stage: "preflight"}
	mockForge := issuex.NewNoOp()
	opts := Options{
		Workspace: ws,
		Forge:     mockForge,
		Git:       g,
		Land:      LandNone,
	}

	target, _, pfErr := Preflight(ctx, opts, g, result)
	if pfErr != nil {
		t.Fatalf("Preflight failed: %v", pfErr)
	}
	if target.Owner != "acme" || target.Name != "repo" {
		t.Errorf("target = %+v, want Owner: acme, Name: repo", target)
	}

	// Test fallback to o.Input.Issue.Repo when origin remote is absent
	ws2, g2 := newRepo(t, 0)
	result2 := &Result{Stage: "preflight"}
	issueRef := &issuex.IssueRef{
		Repo:   issuex.Repo{Owner: "fallback-owner", Name: "fallback-repo", Host: "github.com"},
		Number: 42,
	}
	mockAuthForge := &mockAuthClient{authenticated: true}
	opts2 := Options{
		Workspace: ws2,
		Forge:     mockAuthForge,
		Git:       g2,
		Land:      LandNone,
		Input: toolio.Input{
			Kind:  toolio.KindIssue,
			Issue: issueRef,
		},
	}
	target2, _, pfErr2 := Preflight(ctx, opts2, g2, result2)
	if pfErr2 != nil {
		t.Fatalf("Preflight fallback failed: %v", pfErr2)
	}
	if target2.Owner != "fallback-owner" || target2.Name != "fallback-repo" {
		t.Errorf("target2 = %+v, want Owner: fallback-owner, Name: fallback-repo", target2)
	}
}

// 04-REQ-4.2: the pull request's target is --repo, else the origin remote, else
// the input issue's repository. With both an origin and an issue, the origin
// wins: the branch is pushed there, so that is where the pull request goes.
func TestPreflightTargetPrefersTheOriginRemoteOverTheIssuesRepo(t *testing.T) {
	ws, g := newRepo(t, 0)
	ctx := context.Background()
	if out, code, err := gitx.ExecRunner(ctx, ws.Root, []string{
		"git", "remote", "add", "origin", "https://github.com/acme/repo.git",
	}); err != nil || code != 0 {
		t.Fatalf("git remote add: %v (%d) %s", err, code, out)
	}
	opts := Options{
		Workspace: ws,
		Forge:     &mockAuthClient{authenticated: true},
		Git:       g,
		Land:      LandNone,
		Input: toolio.Input{
			Kind:  toolio.KindIssue,
			Issue: &issuex.IssueRef{Repo: issuex.Repo{Owner: "elsewhere", Name: "tracker", Host: "github.com"}, Number: 7},
		},
	}
	target, _, pfErr := Preflight(ctx, opts, g, &Result{Stage: "preflight"})
	if pfErr != nil {
		t.Fatalf("Preflight: %v", pfErr)
	}
	if target.Owner != "acme" || target.Name != "repo" {
		t.Errorf("target = %+v, want the origin remote acme/repo", target)
	}

	// An explicit --repo beats both.
	opts.Repo = issuex.Repo{Owner: "explicit", Name: "target", Host: "github.com"}
	target, _, pfErr = Preflight(ctx, opts, g, &Result{Stage: "preflight"})
	if pfErr != nil {
		t.Fatalf("Preflight: %v", pfErr)
	}
	if target.Owner != "explicit" || target.Name != "target" {
		t.Errorf("target = %+v, want the explicit --repo", target)
	}
}

// TS-04-15 (unit): codefix preflight returns auth failure when unauthenticated for PR landing or commenting
// Verifies: 04-REQ-4.3
type mockAuthClient struct {
	issuex.NoOpClient
	authenticated bool
}

func (m *mockAuthClient) Authenticated() bool {
	return m.authenticated
}

func TestTS0415_PreflightAuthFailure(t *testing.T) {
	ws, g := newRepo(t, 0)
	ctx := context.Background()

	// Case 1: LandPR enabled, unauthenticated Forge
	opts := Options{
		Workspace: ws,
		Land:      LandPR,
		DryRun:    false,
		Forge:     &mockAuthClient{authenticated: false},
		Git:       g,
	}
	result := &Result{Stage: "preflight"}
	_, _, err := Preflight(ctx, opts, g, result)
	if err == nil {
		t.Fatal("expected preflight auth failure, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want %q", err.StageName(), "preflight")
	}
	if err.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want %q", err.CategoryName(), "auth")
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN") {
		t.Errorf("error %q should mention GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN", err.Error())
	}

	// Case 2: Input.Issue present, unauthenticated Forge
	issueRef := &issuex.IssueRef{
		Repo:   issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Number: 12,
	}
	opts2 := Options{
		Workspace: ws,
		Land:      LandNone,
		DryRun:    false,
		Forge:     &mockAuthClient{authenticated: false},
		Git:       g,
		Input: toolio.Input{
			Kind:  toolio.KindIssue,
			Issue: issueRef,
		},
	}
	result2 := &Result{Stage: "preflight"}
	_, _, err2 := Preflight(ctx, opts2, g, result2)
	if err2 == nil {
		t.Fatal("expected preflight auth failure for issue input, got nil")
	}
	if err2.StageName() != "preflight" {
		t.Errorf("StageName = %q, want %q", err2.StageName(), "preflight")
	}
	if err2.CategoryName() != "auth" {
		t.Errorf("CategoryName = %q, want %q", err2.CategoryName(), "auth")
	}
	if !strings.Contains(err2.Error(), "GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN") {
		t.Errorf("error %q should mention GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN", err2.Error())
	}
}

// TS-04-16 (unit): codefix preflight returns usage failure when target repository cannot be resolved for LandPR
// Verifies: 04-REQ-4.4
func TestTS0416_PreflightTargetUsageFailure(t *testing.T) {
	ws, g := newRepo(t, 0)
	ctx := context.Background()

	opts := Options{
		Workspace: ws, // has no origin remote
		Land:      LandPR,
		DryRun:    false,
		Forge:     &mockAuthClient{authenticated: true},
		Git:       g,
	}
	result := &Result{Stage: "preflight"}
	_, _, err := Preflight(ctx, opts, g, result)
	if err == nil {
		t.Fatal("expected usage failure for unresolved target repo under LandPR, got nil")
	}
	if err.StageName() != "preflight" {
		t.Errorf("StageName = %q, want %q", err.StageName(), "preflight")
	}
	if err.CategoryName() != "usage" {
		t.Errorf("CategoryName = %q, want %q", err.CategoryName(), "usage")
	}
	if !strings.Contains(err.Error(), "--land=pr needs a target repository") {
		t.Errorf("error %q should contain '--land=pr needs a target repository'", err.Error())
	}
	if !strings.Contains(err.Error(), "has no origin remote on a recognized forge") {
		t.Errorf("error %q should mention 'has no origin remote on a recognized forge'", err.Error())
	}
}

// TS-04-17 (integration): codefix opens pull request and posts comment via issuex.Client
// Verifies: 04-REQ-4.5, 04-REQ-4.6, 04-REQ-10.3
func TestTS0417_OpenPullRequestAndPostComment(t *testing.T) {
	var prCreated bool
	var prPayload map[string]any
	var commentCreated bool
	var commentPayload map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/pulls") {
			prCreated = true
			_ = json.NewDecoder(r.Body).Decode(&prPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "https://forge/pr/1",
				"number":   1,
				"title":    prPayload["title"],
				"body":     prPayload["body"],
			})
			return
		}
		if r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/comments") {
			commentCreated = true
			_ = json.NewDecoder(r.Body).Decode(&commentPayload)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"html_url": "https://forge/comment/123",
				"id":       123,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx := context.Background()
	client, err := issuex.NewWithOptions(issuex.Options{
		BaseURL:   server.URL,
		Repo:      issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"},
		Token:     "tok",
		UserAgent: "test",
	})
	if err != nil {
		t.Fatalf("issuex.NewWithOptions: %v", err)
	}

	target := issuex.Repo{Owner: "acme", Name: "widgets", Host: "github.com"}
	issueRef := issuex.IssueRef{Repo: target, Number: 42}
	opts := Options{
		Forge: client,
		Draft: true,
		Input: toolio.Input{
			Kind:  toolio.KindIssue,
			Issue: &issueRef,
		},
		Run:      toolio.NewRun("fix", "test"),
		Progress: toolio.NewProgress(io.Discard, "fix", false, true),
	}

	result := &Result{Stage: "committed"}
	analysis := Analysis{
		Classification: ClassBug,
		Title:          "fix null pointer in widget",
		Summary:        "fixed nil deref",
	}
	impl := Implementation{
		Summary: "handled nil pointer",
	}

	// Execute OpenPullRequest
	OpenPullRequest(ctx, opts, target, result, analysis, impl, "main", "fix/42-fix-null-pointer")
	if !prCreated {
		t.Error("expected CreatePullRequest to be called on server")
	}
	if result.PullRequestURL != "https://forge/pr/1" {
		t.Errorf("PullRequestURL = %q, want %q", result.PullRequestURL, "https://forge/pr/1")
	}
	if result.PullRequestNumber != 1 {
		t.Errorf("PullRequestNumber = %d, want 1", result.PullRequestNumber)
	}
	if prPayload["draft"] != true {
		t.Errorf("draft payload = %v, want true", prPayload["draft"])
	}
	if prPayload["head"] != "fix/42-fix-null-pointer" {
		t.Errorf("head payload = %v, want 'fix/42-fix-null-pointer'", prPayload["head"])
	}
	if prPayload["base"] != "main" {
		t.Errorf("base payload = %v, want 'main'", prPayload["base"])
	}

	// Execute PostComment
	PostComment(ctx, opts, result, "summary body text", "summary")
	if !commentCreated {
		t.Error("expected AddComment to be called on server")
	}
	if len(result.Comments) != 1 || result.Comments[0] != "https://forge/comment/123" {
		t.Errorf("Comments = %v, want ['https://forge/comment/123']", result.Comments)
	}
	if commentPayload["body"] != "summary body text" {
		t.Errorf("comment body payload = %v, want 'summary body text'", commentPayload["body"])
	}
}

// TS-05-15 (integration): codefix's Ambiguity maps onto needs_human's question and options A/B
func TestTS05_15_CodefixAmbiguityMapsOntoNeedsHuman(t *testing.T) {
	result := &Result{
		Ambiguity: &Ambiguity{
			Question:        "Which retry loop?",
			InterpretationA: "the HTTP client's retry loop",
			InterpretationB: "the job queue's redelivery",
		},
	}
	run := toolio.NewRun("fix", "test")
	env := run.Envelope(toolio.ExitNeedsHuman, result, &toolio.ErrorInfo{Stage: "analyse", Category: "ambiguous"})

	if env.NeedsHuman == nil {
		t.Fatal("expected env.NeedsHuman to be non-nil")
	}
	if env.NeedsHuman.Question != "Which retry loop?" {
		t.Errorf("expected Question %q, got %q", "Which retry loop?", env.NeedsHuman.Question)
	}
	if len(env.NeedsHuman.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(env.NeedsHuman.Options))
	}
	wantOptA := toolio.Option{ID: "A", Text: "the HTTP client's retry loop"}
	wantOptB := toolio.Option{ID: "B", Text: "the job queue's redelivery"}
	if env.NeedsHuman.Options[0] != wantOptA {
		t.Errorf("option A: got %+v, want %+v", env.NeedsHuman.Options[0], wantOptA)
	}
	if env.NeedsHuman.Options[1] != wantOptB {
		t.Errorf("option B: got %+v, want %+v", env.NeedsHuman.Options[1], wantOptB)
	}
	if env.NeedsHuman.Needed != "" {
		t.Errorf("expected Needed to be empty, got %q", env.NeedsHuman.Needed)
	}
}

// fakeIndex is a tools.Index test double. Tools returns a code_search tool, as
// the real codesearch index does.
type fakeIndex struct{ closed int }

func (f *fakeIndex) Symbols(context.Context, tools.SymbolQuery) (tools.SymbolAnswer, bool, error) {
	return tools.SymbolAnswer{}, false, nil
}
func (f *fakeIndex) Tools() []core.Tool {
	return []core.Tool{{
		Name: "code_search", Description: "ranked search",
		Execute: func(context.Context, json.RawMessage) core.ToolResult { return core.OKResult(map[string]any{}) },
	}}
}
func (f *fakeIndex) Invalidate(string) {}
func (f *fakeIndex) Close() error      { f.closed++; return nil }

// indexedRunner is a Runner whose Config carries idx — the Config the shell
// builds from the same index it hands the tool's Options.
func indexedRunner(t *testing.T, ws *tools.Workspace, p *faux.Provider, idx tools.Index) *agentrun.Runner {
	t.Helper()
	r, err := agentrun.NewRunner(agentrun.Config{
		Model:         faux.Model(),
		Providers:     core.ProviderRegistry{faux.API: p.APIProvider()},
		Workspace:     ws,
		Bounds:        agentrun.Bounds{MaxTurns: 8, MaxBudgetUSD: 1, MaxAttempts: 1},
		SessionPrefix: "fix",
		Index:         idx,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// wireTools is the names of the tools the first request offered the model.
func wireTools(t *testing.T, p *faux.Provider) map[string]bool {
	t.Helper()
	reqs := p.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the model")
	}
	got := map[string]bool{}
	for _, w := range reqs[0].Tools {
		got[w.Name] = true
	}
	return got
}

// TS-16-3 (unit): codefix.Options.Index is forwarded to agentrun.Config.Index.
// Run builds the real agentBrain, so both of fix's phases are granted
// code_search, and the model is offered it because the index reached the
// Runner's Config.
//
// Verifies: 16-REQ-1.4, 16-REQ-2.1
func TestTS16_3_IndexReachesTheAnalysePhase(t *testing.T) {
	ws, g := newRepo(t, 0)
	skipIfNoFindReferences(t, ws)
	idx := &fakeIndex{}
	p := faux.New()
	o := newOptions(ws, g, nil)
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, idx)
	o.Index = idx

	// The unscripted model ends the analyse phase with no result: the run
	// fails there, which is all this test needs of it.
	_, _ = Run(context.Background(), o)

	got := wireTools(t, p)
	if !got["code_search"] {
		t.Errorf("code_search was not offered to the analyse phase: %v", got)
	}
	for _, n := range agentrun.ReadOnlyFileTools {
		if !got[n] {
			t.Errorf("%s is missing from the analyse phase", n)
		}
	}
}

// 16-REQ-2.1: the implement phase is granted code_search too.
func TestTS16_3_IndexReachesTheImplementPhase(t *testing.T) {
	ws, _ := newRepo(t, 0)
	idx := &fakeIndex{}
	p := faux.New()
	b := &agentBrain{runner: indexedRunner(t, ws, p, idx), codeSearch: true}

	_, _, _ = b.Implement(context.Background(), implementInput{Root: ws.Root})

	got := wireTools(t, p)
	if !got["code_search"] || !got["write_file"] {
		t.Errorf("implement phase tools = %v, want code_search beside the write tools", got)
	}
}

// 16-REQ-2.2: with no index no phase names code_search.
func TestTS16_3_NilIndexLeavesBothGrantsUnchanged(t *testing.T) {
	ws, g := newRepo(t, 0)
	p := faux.New()
	o := newOptions(ws, g, nil)
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, nil)
	_, _ = Run(context.Background(), o)
	if got := wireTools(t, p); got["code_search"] {
		t.Errorf("analyse offered code_search without an index: %v", got)
	}

	p2 := faux.New()
	b := &agentBrain{runner: indexedRunner(t, ws, p2, nil)}
	_, _, _ = b.Implement(context.Background(), implementInput{Root: ws.Root})
	if got := wireTools(t, p2); got["code_search"] {
		t.Errorf("implement offered code_search without an index: %v", got)
	}
}

// The grant is a copy: appending code_search must not grow the shared
// ReadOnlyFileTools slice.
func TestTS16_3_TheSharedReadOnlyListIsNotMutated(t *testing.T) {
	before := len(agentrun.ReadOnlyFileTools)
	got := agentrun.WithCodeSearch(agentrun.ReadOnlyFileTools, true)
	if len(agentrun.ReadOnlyFileTools) != before {
		t.Fatal("ReadOnlyFileTools was modified")
	}
	if got[len(got)-1] != "code_search" || len(got) != before+1 {
		t.Errorf("grant = %v", got)
	}
	if same := agentrun.WithCodeSearch(agentrun.ReadOnlyFileTools, false); len(same) != before {
		t.Errorf("grant without an index = %v", same)
	}
}

// TS-16-15 (unit): codefix invalidates the whole index after git.CreateBranch
// and before the implement phase starts (16-REQ-4.1).
//
// The git runner is wrapped to stamp the moment `checkout -b` runs and the
// scripted brain stamps the moment the implement phase starts, so the order
// is observed rather than assumed.
//
// Verifies: 16-REQ-4.1
func TestTS16_15_CodefixInvalidatesTheIndexAfterCreateBranch(t *testing.T) {
	ws, _ := newRepo(t, 0)
	var branchSeq, implementSeq int64
	runner := func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		out, code, err := gitx.ExecRunner(ctx, dir, argv, stdin...)
		if len(argv) >= 3 && argv[1] == "checkout" && argv[2] == "-b" && code == 0 && err == nil {
			branchSeq = indextest.Next()
		}
		return out, code, err
	}
	g := gitx.New(ws.Root, runner)
	g.SetSleep(func(time.Duration) {})

	idx := &indextest.Index{}
	b := defaultBrain()
	edit := b.edit
	b.edit = func(root string) error {
		implementSeq = indextest.Next()
		return edit(root)
	}
	o := newOptions(ws, g, b)
	o.Index = idx

	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if branchSeq == 0 || implementSeq == 0 {
		t.Fatalf("the branch (%d) or the implement phase (%d) was not observed", branchSeq, implementSeq)
	}
	var seen []indextest.Event
	for _, e := range idx.Events() {
		if e.Kind == "invalidate" {
			seen = append(seen, e)
		}
	}
	if len(seen) == 0 {
		t.Fatal("Invalidate was never called")
	}
	// Earlier invalidations (after the baseline run) are fine; what matters is
	// that one falls between the branch and the start of the implement phase.
	found := false
	for _, e := range seen {
		if e.Rel != "" {
			t.Errorf("Invalidate(%q): want the whole index, \"\"", e.Rel)
		}
		if e.Seq > branchSeq && e.Seq < implementSeq {
			found = true
		}
	}
	if !found {
		t.Errorf("no Invalidate between git.CreateBranch (%d) and the implement phase (%d): %v", branchSeq, implementSeq, seen)
	}
}

// A run without an index must not trip over the invalidation call.
func TestTS16_15_NilIndexRunsWithoutInvalidating(t *testing.T) {
	ws, g := newRepo(t, 0)
	if _, err := Run(context.Background(), newOptions(ws, g, defaultBrain())); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// The project's checks are its own programs and can write into the tree, and
// the revert check takes the fix out and puts it back: the index is
// invalidated after each, before whatever runs next (16-REQ-4, design
// decision 9).
func TestCodefixInvalidatesTheIndexAfterEveryCheckRun(t *testing.T) {
	ws, _ := newRepo(t, 0)
	var stamps []int64
	runner := func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		out, code, err := gitx.ExecRunner(ctx, dir, argv, stdin...)
		for _, a := range argv {
			if a == "make" || strings.HasSuffix(a, "make test") {
				stamps = append(stamps, indextest.Next())
				break
			}
		}
		return out, code, err
	}
	g := gitx.New(ws.Root, gitx.ExecRunner)
	g.SetSleep(func(time.Duration) {})
	idx := &indextest.Index{}
	o := newOptions(ws, g, defaultBrain())
	o.CheckRunner = runner
	o.Index = idx
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// baseline, verification and at least one revert check
	if len(stamps) < 3 {
		t.Fatalf("the checks ran %d times, want at least 3 (baseline, verification, revert check)", len(stamps))
	}
	var inv []int64
	for _, e := range idx.Events() {
		if e.Kind == "invalidate" {
			inv = append(inv, e.Seq)
		}
	}
	for i, s := range stamps {
		next := int64(1) << 62
		if i+1 < len(stamps) {
			next = stamps[i+1]
		}
		found := false
		for _, q := range inv {
			if q > s && q < next {
				found = true
			}
		}
		if !found {
			t.Errorf("no Invalidate after check run %d (seq %d) before the next one (seq %d); invalidations: %v", i, s, next, inv)
		}
	}
}

// The pipeline grants and invalidates the index its Runner's phases read
// (16-REQ-1.4): a Runner built on another index is refused before any phase
// runs, and the Runner's index is the run's when the Options carry none.
func TestTS16_3_TheRunnersIndexIsTheRunsIndex(t *testing.T) {
	ws, g := newRepo(t, 0)
	p := faux.New()
	o := newOptions(ws, g, nil)
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, &fakeIndex{})
	o.Index = &fakeIndex{}
	if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), "not the one its runner was built with") {
		t.Fatalf("Run with two indexes: err = %v, want a refusal", err)
	}
	if n := len(p.Requests()); n != 0 {
		t.Errorf("%d requests reached the model after the refusal", n)
	}

	p = faux.New()
	o = newOptions(ws, g, nil)
	o.brain = nil
	o.Runner = indexedRunner(t, ws, p, &fakeIndex{})
	_, _ = Run(context.Background(), o)
	if got := wireTools(t, p); !got["code_search"] {
		t.Errorf("the Runner's index was not taken as the run's: %v", got)
	}
}

// 16-REQ-2.1: the independent review phase, which conform.RunReview builds, is
// granted code_search by this tool's brain like every other phase, and is not
// without an index (16-REQ-2.2).
func TestTheReviewPhaseIsGrantedCodeSearch(t *testing.T) {
	ws, _ := newRepo(t, 0)
	for _, on := range []bool{true, false} {
		var idx tools.Index
		if on {
			idx = &fakeIndex{}
		}
		p := faux.New()
		b := &agentBrain{runner: indexedRunner(t, ws, p, idx), codeSearch: on}
		_, _, _ = b.Review(context.Background(), conform.ReviewInput{
			Root: ws.Root, Base: "HEAD", Spec: "spec", Scope: conform.ReviewScope{Requirements: []string{"01-REQ-1"}},
		})
		if got := wireTools(t, p); got["code_search"] != on {
			t.Errorf("index %v: the review phase offered %v", on, got)
		}
	}
}
