// Package brain gives af one way to run a brief through a brain: any brain
// implements Brain, the loop holds a box around the brain's tree, and a brain
// hands back exactly one thing, a submission that af validated before it
// existed. This package holds the contract, the choice of a brain, the run of a
// brief (Execute), the gate that every tool call of an in-process brain passes,
// and the brains that run in the process: the scripted replay and the AgentKit
// driver on the host over the box's tools.
package brain

import (
	"context"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Brain runs one brief to a submission. It must not depend on anything inside
// the brain's transcript: the submission arrives through the submit core, in
// the box, and af decides whether one exists from the file, never from what the
// brain says or prints.
type Brain interface {
	Run(ctx context.Context, b Brief) (Outcome, error)
}

// Brief is what a brain is given.
type Brief struct {
	RunID   string
	Attempt int // 1-based
	// Kind is what the brain submits and how it is validated.
	Kind submission.Kind
	// Prefix is the run-constant part of the brief, byte-identical across
	// attempts and resumes; Task is the block that follows it.
	Prefix string
	Task   string
	// Tools is the allowlist of box tool names.
	Tools []string
	Hook  HookConfig
	// Box is the held box. It is never nil in this release.
	Box *box.Session
	// Env is the environment of a brain process started inside the box: empty
	// for the brains that run in the process.
	Env        map[string]string
	MaxCostUSD float64
	MaxTurns   int
	// Timeout bounds the run; 0 is bounded by the context alone.
	Timeout time.Duration
	// TranscriptPath is a host path that the brain may write a transcript to.
	TranscriptPath string
	// Observe, when it is not nil, is called once for each tool call that
	// reaches the gate. A call that the driver answers itself, because the tool
	// does not exist or the arguments do not fit its schema, does not.
	Observe func(ToolCall)
	// Warn, when it is not nil, raises a warning of the run: what the brain
	// could not do that does not stop it, such as a transcript it cannot write.
	Warn func(code, message string)
	// RunDir is the run's directory on the host, where a process brain copies what
	// its process wrote; Record notes an entry in the run's ledger, and Artifact lists
	// a file the run left. Execute sets all three.
	RunDir   string
	Record   func(kind string, data any)
	Artifact func(toolio.Artifact)
}

// Document is the brief as the brain reads it: the prefix, then the task.
func (b Brief) Document() string { return b.Prefix + b.Task }

// The reasons a brain stopped.
const (
	StopSubmitted = "submitted"
	StopEnded     = "ended"
	StopBudget    = "budget"
	StopTurns     = "turns"
	StopError     = "error"
)

// The sources of a cost.
const (
	CostMeasured = "measured" // the driver counted what it was charged
	CostReported = "reported" // the brain said what it cost
	CostNone     = "none"     // no model was used
)

// Outcome is what a brain says about its run. It is used for the envelope's
// usage, the warning submission_claim_mismatch, and the failure of a run that has
// no submission (the stop reason says whether a bound was reached). Whether a
// submission exists is decided only from the file.
type Outcome struct {
	// Submitted is the brain's claim; af decides from the file.
	Submitted    bool
	StopReason   string
	CostUSD      float64
	CostSource   string
	Turns        int
	InputTokens  int
	OutputTokens int
	// Transcript is a path, which af never reads.
	Transcript string
}

// HookConfig is what a hook, or the gate that stands for one, is configured
// with. It is also hook.json in the box.
type HookConfig struct {
	RunID string `json:"run_id"`
	// ProtectedPaths always contains ".agent-fox/": the Brief that Execute gives
	// a brain has it, and so has hook.json.
	ProtectedPaths []string `json:"protected_paths"`
	// AllowPrograms are the programs a shell command may run, beside af and
	// git; none means no restriction.
	AllowPrograms []string `json:"allow_programs"`
}

// The decisions of the gate.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

// ToolCall is one tool call a brain made, as the gate decided it.
type ToolCall struct {
	Brain, Tool, Decision, Reason string // Decision: allow | deny
	OK                            bool
	DurationMS                    int64
}
