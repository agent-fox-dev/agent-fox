package toolio

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// SchemaVersion is the envelope's schema_version.
const SchemaVersion = "5.0.0"

// Status is the envelope's status, and with it the process's exit code.
type Status string

// The statuses, and the exit codes they map to (ExitCode).
const (
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
	StatusUsage      Status = "usage"
	StatusNeedsHuman Status = "needs_human"
	StatusUnverified Status = "unverified"
)

// ExitCode is the exit code a status maps to: 0 done, 1 failed, 2 usage,
// 3 needs_human, 4 unverified. An unknown status is a failure.
func ExitCode(s Status) int {
	switch s {
	case StatusDone:
		return 0
	case StatusUsage:
		return 2
	case StatusNeedsHuman:
		return 3
	case StatusUnverified:
		return 4
	}
	return 1
}

// InputKind is what the positional argument turned out to be.
type InputKind string

// The input kinds of the envelope's input.kind.
const (
	KindIssue InputKind = "issue"
	KindFile  InputKind = "file"
	KindText  InputKind = "text"
	KindStdin InputKind = "stdin"
	KindNone  InputKind = "none"
)

// The states of a side effect.
const (
	StateDone    = "done"
	StatePlanned = "planned"
	StateFailed  = "failed"
)

// The sources of usage.cost_usd.
const (
	CostMeasured = "measured"
	CostReported = "reported"
	CostNone     = "none"
)

// Envelope is the one JSON object every command invocation other than
// --version, --schema and help writes to stdout. Its fields are always all
// present and appear in this order; a consumer never has to tell "missing"
// from "empty". The JSON tags are what the schema's required list is checked
// against.
type Envelope struct {
	SchemaVersion string       `json:"schema_version"`
	Tool          string       `json:"tool"`
	RunID         *string      `json:"run_id"`
	Status        Status       `json:"status"`
	ExitCode      int          `json:"exit_code"`
	Summary       string       `json:"summary"`
	Input         InputInfo    `json:"input"`
	Result        any          `json:"result"`
	NeedsHuman    *NeedsHuman  `json:"needs_human"`
	Error         *ErrorInfo   `json:"error"`
	Warnings      []Warning    `json:"warnings"`
	Artifacts     []Artifact   `json:"artifacts"`
	SideEffects   []SideEffect `json:"side_effects"`
	Next          []Next       `json:"next"`
	Usage         Usage        `json:"usage"`
	StartedAt     string       `json:"started_at"`
	FinishedAt    string       `json:"finished_at"`
	DurationMS    int64        `json:"duration_ms"`
}

// InputInfo is the envelope's input: what the positional argument resolved to.
type InputInfo struct {
	Kind      InputKind `json:"kind"`
	Origin    string    `json:"origin"`
	Truncated bool      `json:"truncated"`
}

// NeedsHuman is the question a command stopped on, and how to answer it.
type NeedsHuman struct {
	Question string   `json:"question"`
	Options  []Option `json:"options"`
	Resume   Resume   `json:"resume"`
}

// Option is one answer a needs_human question offers. Ids run "A", "B", ….
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Resume says how to answer: run Argv with Flag and the answer appended.
type Resume struct {
	Argv []string `json:"argv"`
	Flag string   `json:"flag"`
}

// ErrorInfo is the envelope's error: why a failed or usage run failed.
type ErrorInfo struct {
	Category  string   `json:"category"`
	Message   string   `json:"message"`
	Retryable bool     `json:"retryable"`
	FixHint   *FixHint `json:"fix_hint"`
}

// FixHint names what would likely fix a failure: a flag to change, an
// environment variable or variable to set, and a suggested value.
type FixHint struct {
	Flag    string `json:"flag,omitempty"`
	Env     string `json:"env,omitempty"`
	Var     string `json:"var,omitempty"`
	Suggest string `json:"suggest,omitempty"`
}

// Warning is something that went differently than intended without stopping
// the run. A warning never changes the status.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Artifact is something a run produced. Paths are absolute.
type Artifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	URL    string `json:"url,omitempty"`
	Name   string `json:"name,omitempty"`
	Number int    `json:"number,omitempty"`
}

// SideEffect is a write a run made, or under --dry-run would have made, to
// something beyond the working tree.
type SideEffect struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	State  string `json:"state"`
	Detail string `json:"detail,omitempty"`
}

// Next is a suggested follow-up command.
type Next struct {
	Argv   []string `json:"argv"`
	Reason string   `json:"reason"`
}

// Usage is what the brain behind a run cost. With no brain it is the zero
// usage with cost_source "none".
type Usage struct {
	Brain        string  `json:"brain"`
	CostUSD      float64 `json:"cost_usd"`
	CostSource   string  `json:"cost_source"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	Turns        int     `json:"turns"`
}

// fail makes the envelope a failure: status usage for the usage category,
// failed for any other, with the error object from f.
func (e *Envelope) fail(f *Failure) {
	category := f.Category
	if category == "" {
		category = CategoryInternal
	}
	e.Status = StatusFailed
	if category == CategoryUsage {
		e.Status = StatusUsage
	}
	e.Summary = f.Message
	e.Result = nil
	e.NeedsHuman = nil
	e.Error = &ErrorInfo{Category: category, Message: f.Message, Retryable: f.Retryable, FixHint: f.FixHint}
}

// normalize replaces what would marshal as null by what the contract says:
// an empty array, and the no-brain cost source.
func (e *Envelope) normalize() {
	if e.Warnings == nil {
		e.Warnings = []Warning{}
	}
	if e.Artifacts == nil {
		e.Artifacts = []Artifact{}
	}
	if e.SideEffects == nil {
		e.SideEffects = []SideEffect{}
	}
	if e.Next == nil {
		e.Next = []Next{}
	}
	for i := range e.Next {
		if e.Next[i].Argv == nil {
			e.Next[i].Argv = []string{}
		}
	}
	if e.NeedsHuman != nil {
		if e.NeedsHuman.Options == nil {
			e.NeedsHuman.Options = []Option{}
		}
		if e.NeedsHuman.Resume.Argv == nil {
			e.NeedsHuman.Resume.Argv = []string{}
		}
	}
	if e.Usage.CostSource == "" {
		e.Usage.CostSource = CostNone
	}
}

// marshal renders the envelope as stdout gets it: two-space indented, one
// trailing newline, arrays as [] never null.
func (e *Envelope) marshal() ([]byte, error) {
	e.normalize()
	return encodeIndented(e)
}

// ensureEncodable turns an envelope that cannot be encoded as JSON, because
// a result or a cost is not encodable, into an internal failure that can.
func (e *Envelope) ensureEncodable() {
	_, err := e.marshal()
	if err == nil {
		return
	}
	e.Usage = Usage{CostSource: CostNone}
	e.fail(&Failure{Category: CategoryInternal, Message: fmt.Sprintf("the envelope cannot be encoded as JSON: %v", err)})
}

// encodeIndented renders v the way every JSON document af prints is
// rendered: two-space indented, one trailing newline, and without escaping
// <, > and & so that URLs and placeholders stay readable.
func encodeIndented(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
