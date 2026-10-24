package jobs

import (
	"encoding/json"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Kind is what a job asks for.
type Kind string

// The kinds of job.
const (
	KindTriage Kind = "triage"
	KindFix    Kind = "fix"
)

// State is the state of a job.
type State string

// The states of a job. A job is open while it is queued, running or blocked, and
// finished once it is done, failed or cancelled.
const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateBlocked   State = "blocked"
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Open reports whether the job is queued, running or blocked.
func (s State) Open() bool {
	return s == StateQueued || s == StateRunning || s == StateBlocked
}

// Finished reports whether the job is done, failed or cancelled, which is when
// it has a finished_at.
func (s State) Finished() bool {
	return s == StateDone || s == StateFailed || s == StateCancelled
}

// AttemptState is the state of an attempt.
type AttemptState string

// The states of an attempt.
const (
	AttemptQueued     AttemptState = "queued"
	AttemptRunning    AttemptState = "running"
	AttemptDone       AttemptState = "done"
	AttemptNeedsHuman AttemptState = "needs_human"
	AttemptFailed     AttemptState = "failed"
	AttemptCancelled  AttemptState = "cancelled"
)

// TriggerKind is what made an attempt.
type TriggerKind string

// The triggers of an attempt: the request itself, a person answering a question,
// a person asking again after a failure, and the service trying again after a
// failure of the brain.
const (
	TriggerCreate TriggerKind = "create"
	TriggerAnswer TriggerKind = "answer"
	TriggerRetry  TriggerKind = "retry"
	TriggerAuto   TriggerKind = "auto"
)

// Trigger is what made an attempt, and who.
type Trigger struct {
	Kind TriggerKind `json:"kind"`
	By   string      `json:"by"`
}

// Source is where a job came from: the API, or one of the triggers of later
// specs (label, event, rebuild, triage), and who or what made it.
type Source struct {
	Kind string `json:"kind"`
	By   string `json:"by"`
}

// File is a file posted as the input of a triage.
type File struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// Input is what a job was asked about, as it was posted and without the context:
// an issue (the number, or the URL or owner/repo#N it was given as), or text with a
// title, or a file. Exactly one of the three is set.
type Input struct {
	Issue json.RawMessage `json:"issue,omitempty"`
	Text  string          `json:"text,omitempty"`
	Title string          `json:"title,omitempty"`
	File  *File           `json:"file,omitempty"`
}

// Option is one answer a question offers. The ids run A, B, … in order.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Answer is a question that a person answered, with the options it offered and
// the answer: the text they typed, or the label of the option they chose.
type Answer struct {
	Question string   `json:"question"`
	Options  []Option `json:"options"`
	// Option is the id of the option chosen; nil for an answer in free text.
	Option *string `json:"option"`
	Text   string  `json:"text"`
}

// Blocked is why a job waits for a person.
type Blocked struct {
	Reason     string   `json:"reason"`
	Question   string   `json:"question"`
	Options    []Option `json:"options"`
	AnswerWith string   `json:"answer_with"`
}

// ResultDoc is what the latest finished attempt came to.
type ResultDoc struct {
	Status   toolio.Status     `json:"status"`
	ExitCode int               `json:"exit_code"`
	Summary  string            `json:"summary"`
	Error    *toolio.ErrorInfo `json:"error"`
}

// LabelsDoc is what the job did to labels, as the runner (or the cancel cleanup) last
// reported it, the request label that a cancel says belongs back on the issue while it is
// not back, and the state of the cancel cleanup: pending, done, label_not_set, or null for
// a job that had none.
type LabelsDoc struct {
	Set     []string `json:"set"`
	Removed []string `json:"removed"`
	Restore *string  `json:"restore"`
	Cleanup *string  `json:"cleanup"`
}

// UsageDoc is what a job cost: the sum over its finished attempts.
type UsageDoc struct {
	CostUSD      float64 `json:"cost_usd"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Turns        int     `json:"turns"`
}

// HubDoc is what the runner of the current attempt reported of its session on hub.
type HubDoc struct {
	SessionID *string `json:"session_id"`
	RunID     *string `json:"run_id"`
	SandboxID *string `json:"sandbox_id"`
}

// AnswerDoc is the answer an attempt was started with, as it is shown.
type AnswerDoc struct {
	Question string  `json:"question"`
	Option   *string `json:"option"`
	Text     string  `json:"text"`
}

// AttemptDoc is an attempt, as the single-job view shows it. The fields of what
// the attempt came to are null until it has an envelope.
type AttemptDoc struct {
	N                 int               `json:"n"`
	State             AttemptState      `json:"state"`
	Trigger           Trigger           `json:"trigger"`
	Answer            *AnswerDoc        `json:"answer"`
	Deliveries        int               `json:"deliveries"`
	TransientFailures int               `json:"transient_failures"`
	Status            *toolio.Status    `json:"status"`
	ExitCode          *int              `json:"exit_code"`
	Summary           *string           `json:"summary"`
	Error             *toolio.ErrorInfo `json:"error"`
	Usage             *toolio.Usage     `json:"usage"`
	StartedAt         *string           `json:"started_at"`
	FinishedAt        *string           `json:"finished_at"`
}

// Doc is a job as every route serves it: one shape for a single job and for an
// entry of a list. The single view adds the attempts.
type Doc struct {
	ID             string              `json:"id"`
	Workspace      string              `json:"workspace"`
	Kind           Kind                `json:"kind"`
	Input          Input               `json:"input"`
	Issue          *int64              `json:"issue"`
	Source         Source              `json:"source"`
	RequestLabel   *string             `json:"request_label"`
	State          State               `json:"state"`
	Blocked        *Blocked            `json:"blocked"`
	AttemptCount   int                 `json:"attempt_count"`
	CurrentAttempt int                 `json:"current_attempt"`
	Result         *ResultDoc          `json:"result"`
	Artifacts      []toolio.Artifact   `json:"artifacts"`
	SideEffects    []toolio.SideEffect `json:"side_effects"`
	Labels         *LabelsDoc          `json:"labels"`
	CancelCause    *string             `json:"cancel_cause"`
	Usage          UsageDoc            `json:"usage"`
	Hub            *HubDoc             `json:"hub"`
	CreatedAt      string              `json:"created_at"`
	StartedAt      *string             `json:"started_at"`
	FinishedAt     *string             `json:"finished_at"`
	Attempts       []AttemptDoc        `json:"attempts,omitempty"`
}
