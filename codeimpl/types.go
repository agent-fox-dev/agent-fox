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
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
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
	Repo issuex.Repo
	// Land decides what happens once every task is done.
	Land LandMode
	// DryRun makes no REMOTE change: nothing is pushed and no pull request
	// is opened. The branch and the commits are still made locally.
	DryRun bool
	// VerifyCommand replaces the spec's own test commands with one command.
	// Empty means the spec's linter and all_tests.
	VerifyCommand string
	// NoTestFirst stops the submit tool from requiring red-first evidence
	// (a failing run recorded before the implementation) for the tests a
	// task owns. The prompt still asks for test-first work.
	NoTestFirst bool
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
	// Repair makes a red gate a phase of its own at the two points where
	// the whole suite is what matters: before the first task, when the
	// baseline is red, and after the integration task, when its checks
	// fail. The model fixes what fails, the gate runs again, and the run
	// goes on from green or stops. Without it a red baseline is recorded and
	// the tasks are judged by comparison, and a red integration task is
	// retried from scratch like any other.
	Repair bool
	// RepairAttempts is how many attempts one repair gets before the run
	// gives up. Zero means DefaultRepairAttempts.
	RepairAttempts int
	// RepairRunner drives the repair phase, when it should run on another
	// model than the tasks. Nil means Runner.
	RepairRunner *agentrun.Runner

	// Runner drives the model phases. Required unless brain is injected.
	Runner *agentrun.Runner
	// Forge is the forge client, GitHub or GitLab. Required when Land is
	// LandPR and DryRun is off.
	Forge issuex.Client
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

// DefaultRepairAttempts is how many times a red baseline is repaired before
// the run gives up. It is one more than a task gets because every attempt
// starts from the same failing tree with the previous attempt's failure in
// its prompt, and a suite that fails for two reasons is often fixed one
// reason at a time.
const DefaultRepairAttempts = 3

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
	Stage       string
	Category    string
	Err         error
	TotalBudget float64
}

func (f *Failure) Error() string           { return f.Err.Error() }
func (f *Failure) Unwrap() error           { return f.Err }
func (f *Failure) StageName() string       { return f.Stage }
func (f *Failure) CategoryName() string    { return f.Category }
func (f *Failure) TotalBudgetUSD() float64 { return f.TotalBudget }

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
	// CategoryGit and CategoryForge are the two external systems.
	CategoryGit    = "git"
	CategoryForge  = "forge"
	CategoryGitHub = CategoryForge
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
	ID       string `json:"id" trust:"fact" description:"The id of the test or done_when entry this verdict answers."`
	Verdict  string `json:"verdict" trust:"model" description:"pass or fail."`
	Evidence string `json:"evidence" trust:"model" description:"The evidence for the verdict."`
	// RedEvidence is, for a test verdict, the command run and the failure
	// seen before the implementation existed. It is the model's own claim.
	RedEvidence string `json:"red_evidence,omitempty" trust:"model" description:"For a test verdict, the command run and the failure seen before the implementation existed. The model own claim."`
}

// Passed reports whether v is a pass.
func (v Verdict) Passed() bool { return strings.EqualFold(strings.TrimSpace(v.Verdict), VerdictPass) }

// FileChange is one file the model reports having changed.
type FileChange struct {
	Path   string `json:"path" trust:"model" description:"Path of the file, relative to the repository root."`
	Change string `json:"change" trust:"model" description:"What was done to the file."`
}

// Blocker is a reason the run cannot proceed without a person.
//
// The bar is deliberately high: small doubts are resolved and recorded as
// notes, and the work proceeds. It is reserved for a spec that cannot be
// implemented as written — it names a module the PRD does not say to
// create, or contradicts an invariant the code enforces.
type Blocker struct {
	Reason string `json:"reason" trust:"model" description:"Why the work cannot proceed without a person."`
	Needed string `json:"needed" trust:"model" description:"What a person has to decide or change."`
}

// Survey is the read-only phase's brief: where the things the spec names
// actually live, the conventions a coder must follow, and every place the
// spec's assumptions and the code disagree.
type Survey struct {
	Summary     string     `json:"summary" trust:"model" description:"Summary of what the read-only survey found."`
	Conventions []string   `json:"conventions,omitempty" trust:"model" description:"Conventions a coder must follow."`
	Locations   []Location `json:"locations,omitempty" description:"Where the names the spec uses live in the code."`
	Drift       []Drift    `json:"drift,omitempty" description:"Every place the spec assumptions and the code disagree."`
	Blocker     *Blocker   `json:"blocker,omitempty" description:"Set when the spec cannot be implemented as written."`
}

// Location maps a name the spec uses onto the code.
type Location struct {
	Name string `json:"name" trust:"model" description:"A name the spec uses."`
	Path string `json:"path" trust:"model" description:"Where it lives in the code."`
	Note string `json:"note,omitempty" trust:"model" description:"Anything worth knowing about it."`
}

// Drift is one disagreement between the spec and the code, with the
// resolution the tasks should follow.
type Drift struct {
	SpecRef    string `json:"spec_ref" trust:"model" description:"The part of the spec that disagrees with the code."`
	Finding    string `json:"finding" trust:"model" description:"What the code does instead."`
	Resolution string `json:"resolution" trust:"model" description:"What the tasks should follow."`
}

// Submission is the model's report of one task's work.
//
// It is the model's account and never the evidence that the task is done.
// The evidence is the gate and the diff, both produced by this package.
type Submission struct {
	Summary       string       `json:"summary" trust:"model" description:"The model account of the task work."`
	CommitSubject string       `json:"commit_subject" trust:"model" description:"The commit subject the model proposed."`
	Changes       []FileChange `json:"changes" description:"The files the model reports having changed."`
	// TestVerdicts answers for every test the task owns, by id. The submit
	// tool refuses a submission that skips one.
	TestVerdicts []Verdict `json:"test_verdicts,omitempty" description:"The verdict on every test the task owns, by id."`
	// DoneWhenVerdicts answers for every done_when entry, as DW-n.
	DoneWhenVerdicts []Verdict `json:"done_when_verdicts,omitempty" description:"The verdict on every done_when entry, as DW-n."`
	// TestFirstDeviation is the reason the tests could not be written and
	// run red before the implementation. It stands in for per-test red
	// evidence and is shown to the reviewer.
	TestFirstDeviation string `json:"test_first_deviation,omitempty" trust:"model" description:"Why the tests could not be written and run red first, when that was the case."`
	// Notes is what a reviewer or the next task should know.
	Notes string `json:"notes,omitempty" trust:"model" description:"What a reviewer or the next task should know."`
	// Gotchas are the surprises — the things the next task's prompt carries.
	Gotchas []string `json:"gotchas,omitempty" trust:"model" description:"Surprises the next task prompt carries."`
	// Blocker is set when the task cannot be implemented as specified.
	Blocker *Blocker `json:"blocker,omitempty" description:"Set when the task cannot be implemented as specified."`
}

// RepairSubmission is the model's report of a baseline repair.
//
// Like a task's submission it is the model's account and never the evidence:
// the evidence is the gate running green afterwards.
type RepairSubmission struct {
	// Cause is what was wrong before the change, in the model's reading.
	Cause         string       `json:"cause" trust:"model" description:"What was wrong before the change, in the model reading."`
	Summary       string       `json:"summary" trust:"model" description:"The model account of the repair."`
	CommitSubject string       `json:"commit_subject" trust:"model" description:"The commit subject the model proposed."`
	Changes       []FileChange `json:"changes" description:"The files the model reports having changed."`
	Notes         string       `json:"notes,omitempty" trust:"model" description:"What a reviewer should know."`
	// Blocker is set when the failure cannot be repaired in code: a
	// credential, a service, a tool the machine lacks.
	Blocker *Blocker `json:"blocker,omitempty" description:"Set when the failure cannot be repaired in code."`
}

// RepairReport is what happened to one repair: of the baseline, or of the
// checks after the integration task.
type RepairReport struct {
	// Outcome is done, unverified (the checks still fail after the last
	// attempt), blocked, failed or aborted.
	Outcome  string `json:"outcome" trust:"fact" description:"done, unverified, blocked, failed or aborted."`
	Attempts int    `json:"attempts,omitempty" description:"How many repair attempts were made."`
	// Failing is the gate that was red before the repair.
	Failing *GateResult `json:"failing,omitempty" description:"The gate that was red before the repair."`
	// Model is the model the repair phase ran on, when it differs from the
	// run's.
	Model string `json:"model,omitempty" trust:"fact" description:"The model the repair phase ran on, when it differs from the run."`
	// Commit is the repair's commit on the branch: the fix when it landed,
	// the parked attempt when it did not.
	Commit string `json:"commit,omitempty" trust:"fact" description:"The commit of the repair, or of the parked attempt."`
	// ChangedFiles and DiffStat come from git, not from the model.
	ChangedFiles []string `json:"changed_files,omitempty" trust:"fact" description:"The files in the diff, from git."`
	DiffStat     string   `json:"diff_stat,omitempty" trust:"fact" description:"The diff stat, from git."`
	// Verification is the gate after the last attempt.
	Verification *GateResult `json:"verification,omitempty" description:"The gate after the last attempt."`
	// Submission is the model's report, kept separate from the facts above.
	Submission *RepairSubmission `json:"submission,omitempty" description:"The model report, kept separate from the facts above."`
	// Error says why the repair did not land.
	Error string `json:"error,omitempty" trust:"fact" description:"Why the repair did not land."`
}

// GateResult is one run of the spec's check commands, in order.
type GateResult struct {
	Checks []checks.Result `json:"checks" description:"One run of each check command, in order."`
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
	ID    int    `json:"id" description:"The task number within the spec."`
	Kind  string `json:"kind" trust:"fact" description:"The kind of task."`
	Title string `json:"title" trust:"fact" description:"The task title."`
	// Outcome is pending (the run never reached it), done, skipped (already
	// done or dropped before the run), unverified, blocked, failed or
	// aborted.
	Outcome  string `json:"outcome" trust:"fact" description:"pending, done, skipped, unverified, blocked, failed or aborted."`
	Attempts int    `json:"attempts,omitempty" description:"How many attempts the task took."`
	// Commit is the task's commit on the branch.
	Commit string `json:"commit,omitempty" trust:"fact" description:"The commit of the task on the branch."`
	// ChangedFiles and DiffStat come from git, not from the model.
	ChangedFiles []string `json:"changed_files,omitempty" trust:"fact" description:"The files in the diff, from git."`
	DiffStat     string   `json:"diff_stat,omitempty" trust:"fact" description:"The diff stat, from git."`
	// Verification is the gate after the task, and Verdict its comparison
	// with the gate before it.
	Verification *GateResult `json:"verification,omitempty" description:"The gate after the task."`
	Verdict      string      `json:"verdict,omitempty" trust:"fact" description:"How that gate compares with the gate before the task."`
	// TestsOutcome is "pass" when every owned test was answered pass and
	// "fail" otherwise. It is derived from the verdicts, never stored beside
	// them.
	TestsOutcome string `json:"tests_outcome,omitempty" trust:"fact" description:"pass when every owned test was answered pass, fail otherwise."`
	// Submission is the model's report, kept separate from the facts above.
	Submission *Submission `json:"submission,omitempty" description:"The model report, kept separate from the facts above."`
	// Repair is the repair of the checks after this task, when the run was
	// asked for one and this is the integration task whose checks failed.
	Repair *RepairReport `json:"repair,omitempty" description:"The repair of the checks after this task, when one was run."`
	// Error says why a task did not land.
	Error string `json:"error,omitempty" trust:"fact" description:"Why the task did not land."`
}

// Result is what the tool reports as JSON.
type Result struct {
	// Stage is how far the run got: preflight, surveyed, repairing,
	// repaired, implementing, complete, parked, stopped, committed, pushed
	// or landed.
	Stage string `json:"stage" trust:"fact" description:"How far the run got: preflight, surveyed, repairing, repaired, implementing, complete, parked, stopped, committed, pushed or landed."`

	SpecDir  string `json:"spec_dir" trust:"fact" description:"The spec package directory."`
	SpecID   string `json:"spec_id" trust:"fact" description:"The spec identifier."`
	SpecName string `json:"spec_name" trust:"fact" description:"The spec name."`
	Title    string `json:"title" trust:"fact" description:"The spec title."`
	Status   string `json:"status" trust:"fact" description:"The spec status."`

	Repo       string `json:"repo,omitempty" trust:"fact" description:"The repository worked on."`
	Branch     string `json:"branch,omitempty" trust:"fact" description:"The branch the work was written on."`
	BaseBranch string `json:"base_branch,omitempty" trust:"fact" description:"The branch it was cut from."`
	// Resumed records that the branch existed and the run continued it.
	Resumed bool `json:"resumed,omitempty" description:"True when the branch existed and the run continued it."`

	TasksTotal     int          `json:"tasks_total" description:"How many tasks the spec has."`
	TasksDone      int          `json:"tasks_done" description:"How many tasks are done."`
	TasksSkipped   int          `json:"tasks_skipped" description:"How many tasks were skipped."`
	TasksRemaining int          `json:"tasks_remaining" description:"How many tasks are still to do."`
	Tasks          []TaskReport `json:"tasks" description:"What happened to each task."`

	// Gate is what runs before and after every task.
	Gate []string `json:"gate,omitempty" trust:"fact" description:"The commands that run before and after every task."`
	// Baseline is the gate before any change; Verification is the last gate
	// that ran, and Verdict its comparison with the gate before it.
	Baseline     GateResult `json:"baseline" description:"The gate before any change."`
	Verification GateResult `json:"verification" description:"The last gate that ran."`
	Verdict      string     `json:"verdict" trust:"fact" description:"How the last gate compares with the gate before it."`

	Pushed            bool   `json:"pushed" description:"True when the branch was pushed."`
	PullRequestURL    string `json:"pull_request_url,omitempty" trust:"fact" description:"The pull request that was opened."`
	PullRequestNumber int    `json:"pull_request_number,omitempty" description:"The number of that pull request."`

	// Repair is the baseline repair, when the run was asked for one and
	// the baseline was red. The repair after the integration task is on
	// that task's entry.
	Repair *RepairReport `json:"repair,omitempty" description:"The baseline repair, when one was run."`

	// Survey and Blocker are the model's.
	Survey  *Survey  `json:"survey,omitempty" description:"The read-only survey, the model own account."`
	Blocker *Blocker `json:"blocker,omitempty" description:"Set when the spec cannot be implemented as written."`

	// CostUSD is the run's spend across every phase.
	CostUSD float64 `json:"cost_usd" description:"The run spend across every phase in US dollars."`
	// DryRun records that no remote change was made.
	DryRun bool `json:"dry_run,omitempty" description:"True when no remote change was made."`
	// Preflight and Estimate are set only by a --preflight run whose checks
	// all passed (RunPreflight): the checks that were performed, and what the
	// real run would spend at most. An ordinary run leaves both empty.
	Preflight []toolio.PreflightCheck `json:"preflight,omitempty" description:"Every check a --preflight run performed and its outcome. Present only under --preflight, when every refusing check passed."`
	Estimate  *toolio.Estimate        `json:"estimate,omitempty" description:"What the real run would spend at most: phases, per-phase ceilings and total. Present only under --preflight, when every refusing check passed."`
	// Land records the land mode this run was asked for (pr, branch or
	// none), so a --dry-run run that reached --land=pr can report the pull
	// request it would have opened as hypothetical (06-REQ-4.3) even though
	// PullRequestURL, its only other trace, stays empty either way.
	Land string `json:"land,omitempty" trust:"fact" description:"The land mode the run was asked for: pr, branch or none."`

	// Detail records which view of this result was emitted: "summary" or
	// "full". It is present on both.
	Detail string `json:"detail" trust:"fact" description:"Which view of this result was emitted: summary or full."`
}

// SetDetail implements toolio.DetailedResult.
func (r *Result) SetDetail(d string) { r.Detail = d }

// Summary returns one sentence describing the outcome in impl's vocabulary.
func (r Result) Summary() string {
	if r.preflightPassed() {
		return fmt.Sprintf("impl: preflight passed; %d of %d tasks remain", r.TasksRemaining, r.TasksTotal)
	}
	var parts []string
	taskPart := fmt.Sprintf("impl: %d/%d tasks done", r.TasksDone, r.TasksTotal)
	if r.Branch != "" {
		taskPart += fmt.Sprintf(" on %s", r.Branch)
	}
	parts = append(parts, taskPart)

	var stoppedReason string
	if r.Repair != nil && r.Repair.Outcome != "" && r.Repair.Outcome != "done" {
		stoppedReason = fmt.Sprintf("repair stopped (%s)", r.Repair.Outcome)
	} else {
		for _, t := range r.Tasks {
			if t.Outcome != OutcomeDone && t.Outcome != OutcomeSkipped && t.Outcome != OutcomePending {
				if t.Verdict != "" {
					stoppedReason = fmt.Sprintf("task %d stopped with verdict %s", t.ID, t.Verdict)
				} else if t.Outcome != "" {
					stoppedReason = fmt.Sprintf("task %d stopped (%s)", t.ID, t.Outcome)
				}
				break
			}
		}
	}
	if stoppedReason != "" {
		parts = append(parts, stoppedReason)
	} else if r.Verdict != "" {
		parts = append(parts, fmt.Sprintf("verdict: %s", r.Verdict))
	}

	return strings.Join(parts, "; ")
}

// NeedsHuman implements toolio.NeedsHumanSource.
func (r Result) NeedsHuman() (question string, options []toolio.Option, needed string, ok bool) {
	if r.Blocker == nil {
		return "", nil, "", false
	}
	return r.Blocker.Reason, nil, r.Blocker.Needed, true
}

// preflightPassed reports whether r is the result of a --preflight run whose
// checks all passed. Stage is "preflight" for an ordinary run that refused in
// its own preflight too, so the stage alone cannot say: a --preflight run
// that passed carries its checklist, and a refusal never does. A result with
// tasks remaining and no baseline run is also a pass — the only refusal after
// the tasks are selected is a baseline that could not run, which leaves its
// checks in Baseline, and every refusal before it leaves TasksRemaining at
// zero.
func (r Result) preflightPassed() bool {
	if r.Stage != "preflight" {
		return false
	}
	return len(r.Preflight) > 0 || (r.TasksRemaining > 0 && len(r.Baseline.Checks) == 0)
}

// Resumable implements toolio.Resumabler. For codeimpl, this is true whenever
// Branch is non-empty — except at the preflight stage, where Branch is the name
// the branch would have, not something to resume. That also covers an ordinary
// run that refused at its preflight stage: nothing was written.
func (r Result) Resumable() bool {
	if r.Stage == "preflight" {
		return false
	}
	return r.Branch != ""
}

// brain is the model-driven half: the survey, the baseline repair and the
// per-task implementation, and nothing else.
type brain interface {
	Survey(context.Context, surveyInput) (Survey, agentrun.Result, error)
	Repair(context.Context, repairInput) (RepairSubmission, agentrun.Result, error)
	Implement(context.Context, taskInput) (Submission, agentrun.Result, error)
}
