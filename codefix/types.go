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
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
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
	Path   string `json:"path"`
	Change string `json:"change"`
}

// Analysis is the first phase's result: the diagnosis and the plan.
type Analysis struct {
	Classification Classification `json:"classification"`
	Summary        string         `json:"summary"`
	RootCause      string         `json:"root_cause"`
	Approach       string         `json:"approach"`
	Files          []FileChange   `json:"files"`
	Assumptions    []string       `json:"assumptions,omitempty"`
	// Title is what the branch, the commit subject and the pull request are
	// named after. It comes from the analysis rather than from the issue
	// title because an issue titled "login broken??" should not become the
	// branch name.
	Title string `json:"title"`

	// Ambiguity is set only when the input has two or more readings that lead
	// to fundamentally different fixes, and the codebase cannot settle which.
	// It is the one case where the run stops and asks.
	Ambiguity *Ambiguity `json:"ambiguity,omitempty"`
}

// Ambiguity is a question the run cannot answer for itself.
//
// The bar is deliberately high, and it is in the schema's field descriptions
// rather than only in the system prompt: minor ambiguities are recorded as
// assumptions and the work proceeds. Reserving the stop for contradictory
// readings is what keeps an unattended run unattended.
type Ambiguity struct {
	Question        string `json:"question"`
	InterpretationA string `json:"interpretation_a"`
	InterpretationB string `json:"interpretation_b"`
}

// Implementation is the second phase's result: what was actually done.
//
// It is the model's report of its own work, and it is therefore never the
// evidence that the work is correct. The evidence is the verification run and
// the diff, both produced by this package.
type Implementation struct {
	Summary       string       `json:"summary"`
	CommitSubject string       `json:"commit_subject"`
	Changes       []FileChange `json:"changes"`
	Tests         []string     `json:"tests,omitempty"`
	Notes         string       `json:"notes,omitempty"`

	// CriteriaVerdicts answers the acceptance criteria the report stated,
	// one verdict and one piece of evidence each. It is empty when the
	// report stated none, and it cannot be partial when it stated some: the
	// submit tool refuses a submission that skips a criterion.
	CriteriaVerdicts []CriterionVerdict `json:"criteria_verdicts,omitempty"`
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
	Stage string `json:"stage"`
	// Repo and Issue identify what was worked on, when the input was a
	// GitHub URL.
	Repo        string `json:"repo,omitempty"`
	IssueURL    string `json:"issue_url,omitempty"`
	IssueNumber int    `json:"issue_number,omitempty"`

	Classification string   `json:"classification"`
	Title          string   `json:"title"`
	Summary        string   `json:"summary"`
	RootCause      string   `json:"root_cause,omitempty"`
	Approach       string   `json:"approach,omitempty"`
	Assumptions    []string `json:"assumptions,omitempty"`

	// AcceptanceCriteria is what the report asked the change to satisfy,
	// extracted from its own text by this package. It is a fact about the
	// input, so it is here rather than under Implementation; the verdicts on
	// it are the model's and live there.
	AcceptanceCriteria []Criterion `json:"acceptance_criteria,omitempty"`
	// CriteriaOutcome is "pass" when every criterion was answered pass,
	// "fail" when any was not, and empty when the report stated none. It is
	// derived from the verdicts, never stored alongside them.
	CriteriaOutcome string `json:"criteria_outcome,omitempty"`

	// Branch, BaseBranch and Commit are what the run produced in git.
	Branch     string `json:"branch,omitempty"`
	BaseBranch string `json:"base_branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Pushed     bool   `json:"pushed"`
	// ChangedFiles is the diff, from git — not the model's list.
	ChangedFiles []string `json:"changed_files,omitempty"`
	DiffStat     string   `json:"diff_stat,omitempty"`

	// Baseline and Verification are the two runs of the project's own
	// checks, and Verdict is how they compare.
	Baseline     checks.Result `json:"baseline"`
	Verification checks.Result `json:"verification"`
	Verdict      string        `json:"verdict"`

	// PullRequestURL is set when --land=pr opened one.
	PullRequestURL    string `json:"pull_request_url,omitempty"`
	PullRequestNumber int    `json:"pull_request_number,omitempty"`
	// Comments are the URLs of what was posted on the issue.
	Comments []string `json:"comments,omitempty"`

	// Implementation is the model's report of the work, kept separate from
	// the facts above.
	Implementation *Implementation `json:"implementation,omitempty"`
	// Ambiguity is set when the run stopped to ask.
	Ambiguity *Ambiguity `json:"ambiguity,omitempty"`
	// DryRun records that no remote change was made.
	DryRun bool `json:"dry_run,omitempty"`
}

// issueRef is the reference a run may comment on, or nil when the input was
// text or a file.
type issueRef = *ghapi.IssueRef
