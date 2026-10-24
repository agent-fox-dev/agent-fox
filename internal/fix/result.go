package fix

import "github.com/agent-fox-dev/agentfox/internal/box"

// The outcomes of a run, as a result names them.
const (
	OutcomeNeedsHuman = "needs_human"
	OutcomeFailed     = "failed"
)

// BoxInfo is the box a run used.
type BoxInfo struct {
	Provider string `json:"provider"`
	Image    string `json:"image"`
	// Network is host for a provider that puts no boundary around the tools, none for a box
	// with no network and open for one with the network of the machine.
	Network string `json:"network"`
}

// NewBoxInfo describes the box of a session: its provider, the image it was made from and
// its network, as the ledger says them.
func NewBoxInfo(sess *box.Session) BoxInfo {
	return BoxInfo{Provider: sess.Provider(), Image: sess.Image(), Network: sess.Network()}
}

// Result is the result of `af fix` and of `af fix land`: what the run did, and the facts it
// measured to do it.
type Result struct {
	RunID    string  `json:"run_id"`
	Issue    *string `json:"issue"`
	Base     string  `json:"base"`
	BaseHead string  `json:"base_head"`
	Rounds   int     `json:"rounds"`
	Outcome  string  `json:"outcome"`
	// Verdict is null for a run that measured nothing.
	Verdict       *string         `json:"verdict"`
	Landable      bool            `json:"landable"`
	Closes        bool            `json:"closes"`
	Baseline      *GateRef        `json:"baseline"`
	After         *GateRef        `json:"after"`
	RevertCheck   *RevertRef      `json:"revert_check"`
	ChangedFiles  []string        `json:"changed_files"`
	RevertedPaths []string        `json:"reverted_paths"`
	Land          string          `json:"land"`
	Branch        *string         `json:"branch"`
	Commit        *string         `json:"commit"`
	Pushed        bool            `json:"pushed"`
	PullRequest   *PullRequestRef `json:"pull_request"`
	Box           *BoxInfo        `json:"box"`
}

// ResultInput is what a result is made of.
type ResultInput struct {
	State          State
	Rounds         int
	Outcome        string
	Measured       *Measurement
	Land           string
	Branch, Commit string
	Pushed         bool
	PullRequest    *PullRequestRef
	Box            *BoxInfo
}

// NewResult assembles the result of a run from the state of the run and what it did.
func NewResult(in ResultInput) Result {
	s := in.State
	r := Result{
		RunID: s.RunID, Base: s.Base, BaseHead: s.BaseHead, Rounds: in.Rounds,
		Outcome: in.Outcome, Land: in.Land, Pushed: in.Pushed, PullRequest: in.PullRequest, Box: in.Box,
		ChangedFiles: []string{}, RevertedPaths: []string{},
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
