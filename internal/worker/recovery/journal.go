package recovery

import (
	"time"
)

// The names of the directory and of its files, in the attempt directory.
const (
	DirName     = "recovery"
	JournalFile = "journal.json"
	OutcomeFile = "outcome.json"
)

// FormatVersion is the version of the format of the journal. A journal of another version is not
// read.
const FormatVersion = 1

// The endings of a delivery. A delivery that has none was stopped by the death of the process.
const (
	EndingOutcome   = "outcome"
	EndingTransient = "transient"
	EndingShutdown  = "shutdown"
)

// Journal is the journal of an attempt. It holds no PAT, no token of a lease, no Authorization
// value and no URL with user information.
type Journal struct {
	Version int    `json:"version"`
	Job     string `json:"job"`
	Attempt int    `json:"attempt"`
	Kind    string `json:"kind"`
	// CloneDir is the clone, relative to the attempt directory; HubURL is the hub_url of the workspace,
	// its clone URL, without user information (the key is clone_url: the names of the fields of hub are
	// known to internal/hub alone), and Forge its forge repository.
	CloneDir string `json:"clone_dir"`
	HubURL   string `json:"clone_url"`
	Forge    string `json:"forge_repository"`
	// Settings are what the run was started with.
	Settings Settings `json:"settings"`
	// Run is the run of the loop: its id, and the base the fix is made on.
	Run Run `json:"run"`
	// Session is the hub session of the attempt.
	Session Session `json:"session"`
	// Leases are the sandboxes the attempt leased, in order.
	Leases []Lease `json:"leases"`
	// Deliveries are the times the attempt was handed to the worker, in order.
	Deliveries []Delivery `json:"deliveries"`
	// Spend is what the brain runs of the attempt cost so far, and Carried the part of it that was spent
	// in run directories that a discarded resume removed.
	Spend   Spend `json:"spend"`
	Carried Spend `json:"carried_spend"`
	// Landing is the plan of the landing and the steps of it that are done; Mirror and Report are the
	// steps of the bookkeeping that are done.
	Landing Landing `json:"landing"`
	Mirror  Mirror  `json:"mirror"`
	Report  Report  `json:"report"`
}

// Settings are the settings a run is pinned to: a value that changes on hub afterwards takes effect
// with the next attempt.
type Settings struct {
	Brain    string  `json:"brain"`
	Model    string  `json:"model"`
	Profile  string  `json:"sandbox_profile"`
	Land     string  `json:"landing_mode"`
	Budget   float64 `json:"budget_usd"`
	MaxTurns int     `json:"max_turns"`
}

// Run is the run of the loop of the attempt.
type Run struct {
	ID       string `json:"id"`
	Base     string `json:"base_branch"`
	BaseHead string `json:"base_head"`
}

// Session is the session of the attempt on hub: its id, the run it was opened with, and whether it was
// completed.
type Session struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	Completed bool   `json:"completed"`
}

// Lease is a sandbox the attempt leased, whether it gave it back, and whether the sandbox was lost while
// it was held: gone, or out of reach, in the middle of a delivery, which the delivery replaced.
type Lease struct {
	SandboxID string    `json:"sandbox_id"`
	ExpiresAt time.Time `json:"expires_at"`
	Released  bool      `json:"released"`
	Lost      bool      `json:"lost,omitempty"`
}

// Delivery is one handover of the attempt to the worker.
type Delivery struct {
	Count   int       `json:"count"`
	Started time.Time `json:"started"`
	// Ending is how the delivery ended: outcome, transient or shutdown; none when the process died.
	Ending string `json:"ending,omitempty"`
}

// Spend is what the brain runs of the attempt cost.
type Spend struct {
	CostUSD      float64 `json:"cost_usd"`
	CostSource   string  `json:"cost_source"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Turns        int     `json:"turns"`
}

// Ref is a pull request or an issue that hub made, by number and URL.
type Ref struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

// Landing is the plan of the landing of a fix and the steps of it that are done.
type Landing struct {
	Plan *Plan `json:"plan,omitempty"`
	// Pushed is the push to hub's git server, and HubPush the call of hub's branch push route.
	Pushed       bool `json:"pushed"`
	HubPush      bool `json:"hub_push"`
	PullRequest  *Ref `json:"pull_request,omitempty"`
	CreatedIssue *Ref `json:"created_issue,omitempty"`
}

// Mirror is the steps of the mirror to the issue that are done.
type Mirror struct {
	StatusLabels bool `json:"status_labels"`
	Comment      bool `json:"comment"`
}

// Report is the steps of the report to hub that are done.
type Report struct {
	UsagePosted      bool `json:"usage_posted"`
	EventsPosted     bool `json:"events_posted"`
	SessionCompleted bool `json:"session_completed"`
}
