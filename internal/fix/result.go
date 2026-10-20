package fix

import (
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// The outcomes of a run, as a result names them.
const (
	OutcomeNeedsHuman = "needs_human"
	OutcomeFailed     = "failed"
	OutcomeDryRun     = "dry_run"
)

// BoxInfo is the box a run used.
type BoxInfo struct {
	Provider string `json:"provider"`
	Image    string `json:"image"`
	// Network is host for a provider that puts no boundary around the tools, and the mode
	// of the network the box was given otherwise.
	Network string `json:"network"`
}

// NewBoxInfo describes the box of a provider that was opened with the spec.
func NewBoxInfo(entry box.Entry, spec sandbox.Spec) BoxInfo {
	network := spec.Network.Mode.String()
	if entry.Unconfined {
		network = "host"
	}
	return BoxInfo{Provider: entry.Name, Image: spec.Image, Network: network}
}

// Result is the result of `af fix` and of `af fix land`: what the run did, and the facts it
// measured to do it.
type Result struct {
	RunID        string    `json:"run_id"`
	Issue        *string   `json:"issue"`
	IssueCreated *IssueRef `json:"issue_created"`
	Base         string    `json:"base"`
	BaseHead     string    `json:"base_head"`
	Attempts     int       `json:"attempts"`
	Rounds       int       `json:"rounds"`
	AttemptsMax  int       `json:"attempts_max"`
	Outcome      string    `json:"outcome"`
	// Verdict is null for a run that measured nothing.
	Verdict       *string         `json:"verdict"`
	Landable      bool            `json:"landable"`
	Closes        bool            `json:"closes"`
	Baseline      *GateRef        `json:"baseline"`
	After         *GateRef        `json:"after"`
	RevertCheck   *RevertRef      `json:"revert_check"`
	ChangedFiles  []string        `json:"changed_files"`
	RevertedPaths []string        `json:"reverted_paths"`
	Scope         Scope           `json:"scope"`
	Land          string          `json:"land"`
	Branch        *string         `json:"branch"`
	Commit        *string         `json:"commit"`
	Pushed        bool            `json:"pushed"`
	PullRequest   *PullRequestRef `json:"pull_request"`
	Box           *BoxInfo        `json:"box"`
}

// ResultInput is what a result is made of.
type ResultInput struct {
	State            State
	Attempts, Rounds int
	AttemptsMax      int
	Outcome          string
	Measured         *Measurement
	Land             string
	Branch, Commit   string
	Pushed           bool
	PullRequest      *PullRequestRef
	IssueCreated     *IssueRef
	Box              *BoxInfo
}

// NewResult assembles the result of a run from the state of the run and what it did.
func NewResult(in ResultInput) Result {
	s := in.State
	r := Result{
		RunID: s.RunID, Base: s.Base, BaseHead: s.BaseHead, Attempts: in.Attempts, Rounds: in.Rounds, AttemptsMax: in.AttemptsMax,
		Outcome: in.Outcome, Land: in.Land, Pushed: in.Pushed, PullRequest: in.PullRequest, IssueCreated: in.IssueCreated, Box: in.Box,
		ChangedFiles: []string{}, RevertedPaths: []string{}, Scope: Scope{Undeclared: []string{}, Unchanged: []string{}},
	}
	if s.Issue != nil {
		url := s.Issue.URL
		r.Issue = &url
	}
	if s.BaselineGateID != "" {
		r.Baseline = &GateRef{GateID: s.BaselineGateID, Passed: s.BaselinePassed}
	}
	if m := in.Measured; m != nil {
		if m.Verdict != "" {
			verdict := m.Verdict
			r.Verdict = &verdict
		}
		r.Landable, r.Closes, r.After, r.RevertCheck = m.Landable, m.Closes, m.After, m.RevertCheck
		r.ChangedFiles = append(r.ChangedFiles, m.Changed...)
		r.RevertedPaths = append(r.RevertedPaths, m.Reverted...)
		r.Scope = Scope{Undeclared: append([]string{}, m.Scope.Undeclared...), Unchanged: append([]string{}, m.Scope.Unchanged...)}
	}
	if in.Branch != "" {
		branch := in.Branch
		r.Branch = &branch
	}
	if in.Commit != "" {
		commit := in.Commit
		r.Commit = &commit
	}
	return r
}
