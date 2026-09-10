// Package codeimpl takes a version 2 specification package and implements it
// in a repository: one model phase per task, in the order the plan fixes,
// each verified by the project's own checks before it is committed, on one
// branch that is landed the way `fix` lands its change.
//
// It is the legacy orchestrator (`af code`) rebuilt on the rule ADR 03
// established for the other tools: the model does the part that needs
// judgment — reading the code and writing the change — and Go does
// everything else. Here that is the whole control loop. Go picks the task,
// renders the spec scoped to it, runs the checks before and after, decides
// from the comparison whether the task is done, writes the task's state,
// makes the commit, and moves on. The model cannot mark a task done, cannot
// commit, cannot touch the spec package, and cannot start the next task.
//
// The split is what makes the run's report trustworthy: every task's outcome
// in the result is derived from a measured run of the project's checks and
// from git's account of what changed, and the model's own report is carried
// beside those facts, labelled as its own.
package codeimpl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Options configure one implementation run.
type Options struct {
	// Input is the classified argument. It is read as a reference to a spec
	// package rather than as a problem: a directory, a spec id, a spec name,
	// a directory name, or a file inside the package.
	Input toolio.Input
	// Workspace roots the file tools at the repository. Required.
	Workspace *tools.Workspace
	// SpecsDir is where NN_name packages live. Empty means <workspace>/.specs,
	// or $AF_SPEC_DIR when that is set.
	SpecsDir string
	// Task restricts the run to one task id. Zero means every task that is
	// not done.
	Task int
	// Branch names the branch to work on. It is created when it does not
	// exist and checked out — and continued — when it does. Empty means
	// impl/<NN>-<slug>, with the same continue-or-create rule.
	Branch string
	// Repo is the target repository for the pull request. Zero means the
	// origin remote's.
	Repo ghapi.Repo
	// Land decides what happens once every task is done.
	Land LandMode
	// DryRun makes no REMOTE change: nothing is pushed and no pull request
	// is opened. The branch and the commits are still made locally.
	DryRun bool
	// VerifyCommand replaces the spec's own test commands with one command.
	// Empty means the spec's linter and all_tests.
	VerifyCommand string
	// NoVerify runs nothing. Every task is then reported as unverified — not
	// as a pass.
	NoVerify bool
	// VerifyTimeout bounds one check command.
	VerifyTimeout time.Duration
	// PushAttempts is how many times a push is retried.
	PushAttempts int
	// AllowPrograms widens the implementation phases' shell allowlist.
	AllowPrograms []string
	// Draft opens the pull request as a draft.
	Draft bool
	// Pull checks out and pulls the base branch from origin before anything
	// else happens.
	Pull bool
	// NoSurvey skips the read-only survey phase.
	NoSurvey bool
	// TaskAttempts is how many implementation attempts one task gets before
	// the run parks. Zero means DefaultTaskAttempts.
	TaskAttempts int
	// TotalBudgetUSD caps the spend of the whole run, across every phase.
	// Zero means no cap beyond the per-phase bounds.
	TotalBudgetUSD float64

	// Runner drives the model phases. Required unless brain is injected.
	Runner *agentrun.Runner
	// GitHub is the REST client.
	GitHub *ghapi.Client
	// Git is the repository wrapper. Nil means one rooted at the workspace.
	Git *gitx.Git
	// CheckRunner runs the check commands. Nil means the reduced-env
	// runner, which strips credentials.
	CheckRunner gitx.Runner
	// Run records warnings and per-phase cost.
	Run *toolio.Run
	// Progress reports steps to stderr.
	Progress *toolio.Progress

	// brain is the model half. It is unexported and injected by tests, which
	// is what lets the whole pipeline run against a scripted one.
	brain brain
}

// DefaultTaskAttempts is how many times one task is attempted. The second
// attempt starts from the last commit with the first attempt's failure in
// its prompt; there is no third, because a task that fails the same way
// twice is a task the spec or the code is wrong about.
const DefaultTaskAttempts = 2

// LandMode is what happens once every task is done and verified.
type LandMode string

const (
	// LandPR pushes the branch and opens a pull request. It is the default.
	LandPR LandMode = "pr"
	// LandBranch pushes the branch and opens nothing.
	LandBranch LandMode = "branch"
	// LandNone commits locally and pushes nothing.
	LandNone LandMode = "none"
)

// LandModes is the set a flag accepts.
var LandModes = []string{string(LandPR), string(LandBranch), string(LandNone)}

// ParseLandMode validates a --land value.
func ParseLandMode(s string) (LandMode, bool) {
	switch LandMode(strings.TrimSpace(strings.ToLower(s))) {
	case LandPR:
		return LandPR, true
	case LandBranch:
		return LandBranch, true
	case LandNone:
		return LandNone, true
	}
	return "", false
}

// Pushes reports whether the mode publishes the branch.
func (m LandMode) Pushes() bool { return m == LandPR || m == LandBranch }

// Failure carries the stage and category of a failed run.
type Failure struct {
	Stage    string
	Category string
	Err      error
}

func (f *Failure) Error() string        { return f.Err.Error() }
func (f *Failure) Unwrap() error        { return f.Err }
func (f *Failure) StageName() string    { return f.Stage }
func (f *Failure) CategoryName() string { return f.Category }

func fail(stage, category string, err error) *Failure {
	return &Failure{Stage: stage, Category: category, Err: err}
}

func failf(stage, category, format string, args ...any) *Failure {
	return &Failure{Stage: stage, Category: category, Err: fmt.Errorf(format, args...)}
}

// Categories this package adds to the shared vocabulary.
const (
	// CategoryInvalidSpec means the package does not validate. The rules
	// that broke are named in the message.
	CategoryInvalidSpec = "invalid_spec"
	// CategoryBlocked means the run stopped on purpose: the spec cannot be
	// implemented as written, or an upstream spec is not done, and a person
	// has to decide. The caller maps it to the "needs a human" exit code.
	CategoryBlocked = "blocked"
	// CategoryUnverified means a task was implemented and the checks do not
	// pass; the work is parked on the branch.
	CategoryUnverified = "unverified"
	// CategoryGit and CategoryGitHub are the two external systems.
	CategoryGit    = "git"
	CategoryGitHub = "github"
	// CategoryEmpty means a task reported work and changed nothing.
	CategoryEmpty = "empty_change"
)

// Verdicts a submitted test or done_when entry can carry. There is no
// third: "partial" is where work that does not meet a criterion goes to be
// described as though it did.
const (
	VerdictPass = "pass"
	VerdictFail = "fail"
)

// Verdicts is the enum the schema declares.
var Verdicts = []string{VerdictPass, VerdictFail}

// Verdict is the model's judgement on one of the task's tests or done_when
// entries, with the evidence for it.
type Verdict struct {
	ID       string `json:"id"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
}

// Passed reports whether v is a pass.
func (v Verdict) Passed() bool { return strings.EqualFold(strings.TrimSpace(v.Verdict), VerdictPass) }

// FileChange is one file the model reports having changed.
type FileChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

// Blocker is a reason the run cannot proceed without a person.
//
// The bar is deliberately high: small doubts are resolved and recorded as
// notes, and the work proceeds. It is reserved for a spec that cannot be
// implemented as written — it names a module the PRD does not say to
// create, or contradicts an invariant the code enforces.
type Blocker struct {
	Reason string `json:"reason"`
	Needed string `json:"needed"`
}

// Survey is the read-only phase's brief: where the things the spec names
// actually live, the conventions a coder must follow, and every place the
// spec's assumptions and the code disagree.
type Survey struct {
	Summary     string     `json:"summary"`
	Conventions []string   `json:"conventions,omitempty"`
	Locations   []Location `json:"locations,omitempty"`
	Drift       []Drift    `json:"drift,omitempty"`
	Blocker     *Blocker   `json:"blocker,omitempty"`
}

// Location maps a name the spec uses onto the code.
type Location struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Note string `json:"note,omitempty"`
}

// Drift is one disagreement between the spec and the code, with the
// resolution the tasks should follow.
type Drift struct {
	SpecRef    string `json:"spec_ref"`
	Finding    string `json:"finding"`
	Resolution string `json:"resolution"`
}

// Submission is the model's report of one task's work.
//
// It is the model's account and never the evidence that the task is done.
// The evidence is the gate and the diff, both produced by this package.
type Submission struct {
	Summary       string       `json:"summary"`
	CommitSubject string       `json:"commit_subject"`
	Changes       []FileChange `json:"changes"`
	// TestVerdicts answers for every test the task owns, by id. The submit
	// tool refuses a submission that skips one.
	TestVerdicts []Verdict `json:"test_verdicts,omitempty"`
	// DoneWhenVerdicts answers for every done_when entry, as DW-n.
	DoneWhenVerdicts []Verdict `json:"done_when_verdicts,omitempty"`
	// Notes is what a reviewer or the next task should know.
	Notes string `json:"notes,omitempty"`
	// Gotchas are the surprises — the things the next task's prompt carries.
	Gotchas []string `json:"gotchas,omitempty"`
	// Blocker is set when the task cannot be implemented as specified.
	Blocker *Blocker `json:"blocker,omitempty"`
}

// GateResult is one run of the spec's check commands, in order.
type GateResult struct {
	Checks []checks.Result `json:"checks"`
}

// Outcomes a task can end the run with. A task the run never reached stays
// pending.
const (
	OutcomePending    = "pending"
	OutcomeDone       = "done"
	OutcomeSkipped    = "skipped"
	OutcomeUnverified = "unverified"
	OutcomeBlocked    = "blocked"
	OutcomeFailed     = "failed"
	OutcomeAborted    = "aborted"
)

// TaskReport is what happened to one task.
type TaskReport struct {
	ID    int    `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Outcome is pending (the run never reached it), done, skipped (already
	// done or dropped before the run), unverified, blocked, failed or
	// aborted.
	Outcome  string `json:"outcome"`
	Attempts int    `json:"attempts,omitempty"`
	// Commit is the task's commit on the branch.
	Commit string `json:"commit,omitempty"`
	// ChangedFiles and DiffStat come from git, not from the model.
	ChangedFiles []string `json:"changed_files,omitempty"`
	DiffStat     string   `json:"diff_stat,omitempty"`
	// Verification is the gate after the task, and Verdict its comparison
	// with the gate before it.
	Verification *GateResult `json:"verification,omitempty"`
	Verdict      string      `json:"verdict,omitempty"`
	// TestsOutcome is "pass" when every owned test was answered pass and
	// "fail" otherwise. It is derived from the verdicts, never stored beside
	// them.
	TestsOutcome string `json:"tests_outcome,omitempty"`
	// Submission is the model's report, kept separate from the facts above.
	Submission *Submission `json:"submission,omitempty"`
	// Error says why a task did not land.
	Error string `json:"error,omitempty"`
}

// Result is what the tool reports as JSON.
type Result struct {
	// Stage is how far the run got: preflight, surveyed, implementing,
	// complete, parked, stopped, committed, pushed or landed.
	Stage string `json:"stage"`

	SpecDir  string `json:"spec_dir"`
	SpecID   string `json:"spec_id"`
	SpecName string `json:"spec_name"`
	Title    string `json:"title"`
	Status   string `json:"status"`

	Repo       string `json:"repo,omitempty"`
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	// Resumed records that the branch existed and the run continued it.
	Resumed bool `json:"resumed,omitempty"`

	TasksTotal     int          `json:"tasks_total"`
	TasksDone      int          `json:"tasks_done"`
	TasksSkipped   int          `json:"tasks_skipped"`
	TasksRemaining int          `json:"tasks_remaining"`
	Tasks          []TaskReport `json:"tasks"`

	// Gate is what runs before and after every task.
	Gate []string `json:"gate,omitempty"`
	// Baseline is the gate before any change; Verification is the last gate
	// that ran, and Verdict its comparison with the gate before it.
	Baseline     GateResult `json:"baseline"`
	Verification GateResult `json:"verification"`
	Verdict      string     `json:"verdict"`

	Pushed            bool   `json:"pushed"`
	PullRequestURL    string `json:"pull_request_url,omitempty"`
	PullRequestNumber int    `json:"pull_request_number,omitempty"`

	// Survey and Blocker are the model's.
	Survey  *Survey  `json:"survey,omitempty"`
	Blocker *Blocker `json:"blocker,omitempty"`

	// CostUSD is the run's spend across every phase.
	CostUSD float64 `json:"cost_usd"`
	// DryRun records that no remote change was made.
	DryRun bool `json:"dry_run,omitempty"`
}

// brain is the model-driven half: the survey and the per-task
// implementation, and nothing else.
type brain interface {
	Survey(context.Context, surveyInput) (Survey, agentrun.Result, error)
	Implement(context.Context, taskInput) (Submission, agentrun.Result, error)
}
