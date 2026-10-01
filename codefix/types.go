// Package codefix takes a problem — a GitHub issue, a file, or a paragraph of
// text — diagnoses it against a repository, writes the fix on a branch,
// verifies it with the project's own checks, and lands it.
//
// It is the `af-fix` skill rebuilt as a program. In the skill all ten steps
// are prose the model is asked to follow. Here the model does the two steps
// that need judgment — diagnose and implement — and Go does the other eight:
// classifying the input, fetching the issue, checking the working tree,
// naming the branch, running the test suite, committing, pushing, opening the
// pull request, and writing every comment. The model cannot skip a step it
// finds boring, cannot report a test run it did not do, and cannot decide
// mid-run that the branch should be pushed to main.
//
// The split exists because an autonomous fixer has one hard problem: the
// thing that decides whether the work succeeded must not be the thing that
// did the work. A prompt-only agent runs the tests and also writes the
// sentence "all existing tests pass ✅", and nothing checks that those two are
// related. Here every line of every comment is rendered by a pure function
// from a checks.Result, so a verification section cannot claim a run that did
// not happen.
package codefix

import (
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// Classification is what the input turned out to be about. It decides the
// branch prefix and the commit type, and nothing else — the work is the same
// either way.
type Classification string

const (
	ClassBug         Classification = "bug"
	ClassFeature     Classification = "feature"
	ClassRefactor    Classification = "refactor"
	ClassPerformance Classification = "performance"
)

// Classifications is the enum the schema declares, in one place so the prompt
// and the schema cannot disagree.
var Classifications = []string{
	string(ClassBug), string(ClassFeature), string(ClassRefactor), string(ClassPerformance),
}

// Valid reports whether c is one of the four.
func (c Classification) Valid() bool {
	for _, k := range Classifications {
		if string(c) == k {
			return true
		}
	}
	return false
}

// BranchPrefix is the first segment of the branch name.
func (c Classification) BranchPrefix() string {
	if c == ClassBug {
		return "fix"
	}
	return "feature"
}

// CommitType is the conventional-commit type for the landed change.
func (c Classification) CommitType() string {
	switch c {
	case ClassFeature:
		return "feat"
	case ClassRefactor:
		return "refactor"
	case ClassPerformance:
		return "perf"
	default:
		return "fix"
	}
}

// FileChange is one file the model proposes to touch, or did.
type FileChange struct {
	Path   string `json:"path" trust:"model" description:"Path of the file, relative to the repository root."`
	Change string `json:"change" trust:"model" description:"What was done to the file or is proposed for it."`
}

// Analysis is the first phase's result: the diagnosis and the plan.
type Analysis struct {
	Classification Classification `json:"classification" description:"What the input is about: bug, feature, refactor or performance."`
	Summary        string         `json:"summary" description:"One-paragraph summary of the diagnosis."`
	RootCause      string         `json:"root_cause" description:"The root cause of the problem."`
	Approach       string         `json:"approach" description:"How the change will fix it."`
	Files          []FileChange   `json:"files" description:"The files the change is expected to touch."`
	Assumptions    []string       `json:"assumptions,omitempty" description:"Minor ambiguities resolved by assumption."`
	// Title is what the branch, the commit subject and the pull request are
	// named after. It comes from the analysis rather than from the issue
	// title because an issue titled "login broken??" should not become the
	// branch name.
	Title string `json:"title" description:"What the branch, the commit subject and the pull request are named after."`

	// Ambiguity is set only when the input has two or more readings that lead
	// to fundamentally different fixes, and the codebase cannot settle which.
	// It is the one case where the run stops and asks.
	Ambiguity *Ambiguity `json:"ambiguity,omitempty" description:"Set only when the input has contradictory readings the codebase cannot settle."`
}

// Ambiguity is a question the run cannot answer for itself.
//
// The bar is deliberately high, and it is in the schema's field descriptions
// rather than only in the system prompt: minor ambiguities are recorded as
// assumptions and the work proceeds. Reserving the stop for contradictory
// readings is what keeps an unattended run unattended.
type Ambiguity struct {
	Question        string `json:"question" trust:"model" description:"The question the run cannot answer for itself."`
	InterpretationA string `json:"interpretation_a" trust:"model" description:"The first reading of the input."`
	InterpretationB string `json:"interpretation_b" trust:"model" description:"The second reading, leading to a fundamentally different fix."`
}

// Implementation is the second phase's result: what was actually done.
//
// It is the model's report of its own work, and it is therefore never the
// evidence that the work is correct. The evidence is the verification run and
// the diff, both produced by this package.
type Implementation struct {
	Summary       string       `json:"summary" trust:"model" description:"The model account of what it did."`
	CommitSubject string       `json:"commit_subject" trust:"model" description:"The commit subject the model proposed."`
	Changes       []FileChange `json:"changes" description:"The files the model reports having changed."`
	Tests         []string     `json:"tests,omitempty" trust:"model" description:"The tests the model reports having added or run."`
	Notes         string       `json:"notes,omitempty" trust:"model" description:"Anything a reviewer should know."`

	// CriteriaVerdicts answers the acceptance criteria the report stated,
	// one verdict and one piece of evidence each. It is empty when the
	// report stated none, and it cannot be partial when it stated some: the
	// submit tool refuses a submission that skips a criterion.
	CriteriaVerdicts []CriterionVerdict `json:"criteria_verdicts,omitempty" description:"The model verdict and evidence for each acceptance criterion. Empty when the report stated none."`
}

// LandMode is what happens once a change is written and verified.
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

// Result is what the tool reports as JSON.
//
// Every field is a fact this package established, not a claim the model made.
// The two that came from the model — Analysis and Implementation — are named
// as such.
type Result struct {
	// Stage is how far the run got: analysed, implemented, verified,
	// committed, pushed, landed, or stopped.
	Stage string `json:"stage" trust:"fact" description:"How far the run got: analysed, implemented, verified, committed, pushed, landed or stopped."`
	// Repo and Issue identify what was worked on, when the input was a
	// GitHub URL.
	Repo        string `json:"repo,omitempty" trust:"fact" description:"The repository worked on, when the input was an issue URL."`
	IssueURL    string `json:"issue_url,omitempty" trust:"fact" description:"The issue the input came from, when it was one."`
	IssueNumber int    `json:"issue_number,omitempty" description:"The number of that issue."`

	Classification string   `json:"classification" trust:"model" description:"What the input turned out to be about: bug, feature, refactor or performance."`
	Title          string   `json:"title" trust:"model" description:"What the branch, the commit and the pull request are named after."`
	FixSummary     string   `json:"summary" trust:"model" description:"One-paragraph summary of the fix."`
	RootCause      string   `json:"root_cause,omitempty" trust:"model" description:"The root cause of the problem."`
	Approach       string   `json:"approach,omitempty" trust:"model" description:"How the change fixes it."`
	Assumptions    []string `json:"assumptions,omitempty" trust:"model" description:"Minor ambiguities resolved by assumption."`

	// AcceptanceCriteria is what the report asked the change to satisfy,
	// extracted from its own text by this package. It is a fact about the
	// input, so it is here rather than under Implementation; the verdicts on
	// it are the model's and live there.
	AcceptanceCriteria []Criterion `json:"acceptance_criteria,omitempty" description:"The acceptance criteria the report asked the change to satisfy, extracted from its own text."`
	// CriteriaOutcome is "pass" when every criterion was answered pass,
	// "fail" when any was not, and empty when the report stated none. It is
	// derived from the verdicts, never stored alongside them.
	CriteriaOutcome string `json:"criteria_outcome,omitempty" trust:"fact" description:"pass when every criterion was answered pass, fail when any was not, empty when the report stated none."`

	// Branch, BaseBranch and Commit are what the run produced in git.
	Branch     string `json:"branch,omitempty" trust:"fact" description:"The branch the change was written on."`
	BaseBranch string `json:"base_branch,omitempty" trust:"fact" description:"The branch it was cut from."`
	Commit     string `json:"commit,omitempty" trust:"fact" description:"The commit SHA of the change."`
	Pushed     bool   `json:"pushed" description:"True when the branch was pushed."`
	// ChangedFiles is the diff, from git — not the model's list.
	ChangedFiles []string `json:"changed_files,omitempty" trust:"fact" description:"The files in the diff, from git."`
	DiffStat     string   `json:"diff_stat,omitempty" trust:"fact" description:"The diff stat, from git."`

	// Baseline and Verification are the two runs of the project's own
	// checks, and Verdict is how they compare.
	Baseline     checks.Result `json:"baseline" description:"The project checks before the change."`
	Verification checks.Result `json:"verification" description:"The project checks after the change."`
	Verdict      string        `json:"verdict,omitempty" trust:"fact" description:"How the verification compares with the baseline. Empty when the run stopped before any comparison was made."`

	// PullRequestURL is set when --land=pr opened one.
	PullRequestURL    string `json:"pull_request_url,omitempty" trust:"fact" description:"The pull request that was opened, when --land=pr opened one."`
	PullRequestNumber int    `json:"pull_request_number,omitempty" description:"The number of that pull request."`
	// Comments are the URLs of what was posted on the issue.
	Comments []string `json:"comments,omitempty" trust:"fact" description:"URLs of the comments posted on the issue."`

	// Implementation is the model's report of the work, kept separate from
	// the facts above.
	Implementation *Implementation `json:"implementation,omitempty" description:"The model report of the work, kept separate from the facts above."`
	// Ambiguity is set when the run stopped to ask.
	Ambiguity *Ambiguity `json:"ambiguity,omitempty" description:"Set when the run stopped to ask a question."`
	// DryRun records that no remote change was made.
	DryRun bool `json:"dry_run,omitempty" description:"True when no remote change was made."`
	// Preflight and Estimate are set only by a --preflight run whose checks
	// all passed. A refusing check produces the ordinary failing envelope
	// instead, with neither field.
	Preflight []toolio.PreflightCheck `json:"preflight,omitempty" description:"Each check a --preflight run performed and its outcome. Absent on an ordinary run and on a refused one."`
	Estimate  *toolio.Estimate        `json:"estimate,omitempty" description:"What the real run would spend at most, from the plan already decided. Present only on a --preflight run that passed."`
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

// Summary returns one sentence describing the outcome in fix's vocabulary.
func (r Result) Summary() string {
	// Only a passing --preflight run populates Preflight, so an ordinary run
	// that refused at its own preflight stage (empty checklist) falls through.
	if r.Stage == "preflight" && len(r.Preflight) > 0 {
		return fmt.Sprintf("fix: preflight passed (%d checks)", len(r.Preflight))
	}
	if r.Ambiguity != nil {
		return "fix: stopped on ambiguity; asked a question"
	}
	var parts []string
	if r.Commit != "" || r.Branch != "" {
		commitStr := r.Commit
		if len(commitStr) > 7 {
			commitStr = commitStr[:7]
		}
		if commitStr != "" && r.Branch != "" {
			parts = append(parts, fmt.Sprintf("fix: committed %s on %s", commitStr, r.Branch))
		} else if r.Branch != "" {
			parts = append(parts, fmt.Sprintf("fix: branch %s", r.Branch))
		} else {
			parts = append(parts, fmt.Sprintf("fix: committed %s", commitStr))
		}
	} else {
		parts = append(parts, "fix")
	}

	if r.Verdict != "" {
		parts = append(parts, fmt.Sprintf("checks %s", r.Verdict))
	}

	if r.PullRequestURL != "" {
		if r.PullRequestNumber > 0 {
			parts = append(parts, fmt.Sprintf("landed PR #%d", r.PullRequestNumber))
		} else {
			parts = append(parts, "landed PR")
		}
	} else if r.Pushed {
		parts = append(parts, "pushed")
	} else if r.Commit != "" {
		parts = append(parts, "not landed")
	}

	return strings.Join(parts, "; ")
}

// NeedsHuman implements toolio.NeedsHumanSource.
func (r Result) NeedsHuman() (question string, options []toolio.Option, needed string, ok bool) {
	if r.Ambiguity == nil {
		return "", nil, "", false
	}
	return r.Ambiguity.Question, []toolio.Option{
		{ID: "A", Text: r.Ambiguity.InterpretationA},
		{ID: "B", Text: r.Ambiguity.InterpretationB},
	}, "", true
}

// Resumable implements toolio.Resumabler. For codefix, this is always false
// because branch names are non-deterministic, and a preflight stage creates
// no branch at all.
func (r Result) Resumable() bool {
	return false
}

// Categories this package adds to the shared vocabulary.
const (
	// CategoryForge and CategoryGit are the two external systems.
	CategoryForge  = "forge"
	CategoryGitHub = CategoryForge
)

// issueRef is the reference a run may comment on, or nil when the input was
// text or a file.
type issueRef = *issuex.IssueRef
