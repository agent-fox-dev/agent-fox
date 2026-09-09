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
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
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

	analyzed, implemented int
	implementPrompt       string
}

func (b *scriptedBrain) Analyze(_ context.Context, in analysisInput) (Analysis, agentrun.Result, error) {
	b.analyzed++
	res := agentrun.Result{Name: "analyse", Turns: 2}
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
	write(t, dir, "Makefile", "test:\n\t@exit "+itoa(testExit)+"\n")

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
	if len(got.ChangedFiles) != 1 || got.ChangedFiles[0] != "count.go" {
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
			_ = json.NewEncoder(w).Encode(ghapi.Comment{HTMLURL: "https://github.com/a/b/issues/1#c"})
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			_ = json.NewDecoder(r.Body).Decode(&prPayload)
			_ = json.NewEncoder(w).Encode(ghapi.PullRequest{
				Number: 5, HTMLURL: "https://github.com/a/b/pull/5"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	ws, g := newRepo(t, 0)
	ref := ghapi.IssueRef{Repo: ghapi.Repo{Owner: "a", Name: "b"}, Number: 1}
	o := newOptions(ws, g, defaultBrain())
	o.Input.Kind = toolio.KindIssue
	o.Input.Origin = ref.URL()
	o.Input.Issue = &ref
	o.Land = LandPR
	// --dry-run keeps the push out of it while still exercising the comment
	// and pull-request paths' inputs; the push itself is covered in gitx.
	o.GitHub = ghapi.NewWithOptions(ghapi.Options{BaseURL: srv.URL, Token: "t", UserAgent: "test"})
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
		Programs: readOnlyPrograms, AllowOperators: false, ReadOnlyFiles: true,
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
	implPrograms := append(append(append([]string(nil), readOnlyPrograms...), buildPrograms...), b.extraPrograms...)
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
