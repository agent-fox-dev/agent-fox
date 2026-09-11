package toolio

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agentfox/agentkit-go/core"
)

// Exit codes. Every tool uses the same table, because something other
// than a human reads them.
const (
	// ExitOK means the tool did what it was asked.
	ExitOK = 0
	// ExitFailed means a step errored. The failing stage is named in the
	// envelope's error object.
	ExitFailed = 1
	// ExitUsage means the invocation was wrong: no input, an undefined flag,
	// a flag combination that cannot hold. Nothing was fetched and nothing
	// was written.
	ExitUsage = 2
	// ExitNeedsHuman means the tool stopped on purpose because the work
	// cannot proceed without a decision only a person can make. It is not a
	// failure and re-running unchanged will not help.
	ExitNeedsHuman = 3
	// ExitUnverified means work was produced but the checks that would prove
	// it correct did not pass. What was produced is named in the envelope.
	ExitUnverified = 4
)

// Envelope is the single JSON object every agent-fox tool writes to stdout.
//
// It is written exactly once, on every path including the failing ones,
// because the caller is a program: a tool that prints JSON on success and a
// bare sentence on failure forces its caller to parse two formats and guess
// which one it got.
type Envelope struct {
	// Tool is the program that produced this object: "spec", "issue", "fix".
	Tool string `json:"tool"`
	// Version is the build identity, so a surprising result can be traced to
	// a build.
	Version string `json:"version"`
	// OK is the one field a caller has to read. It is true only when the
	// tool did the whole job it was asked to do.
	OK bool `json:"ok"`
	// ExitCode is the process's exit code, repeated here so a caller that
	// captured only stdout still has it.
	ExitCode int `json:"exit_code"`

	Input  *InputInfo `json:"input,omitempty"`
	Model  *ModelInfo `json:"model,omitempty"`
	Usage  *UsageInfo `json:"usage,omitempty"`
	Result any        `json:"result,omitempty"`

	// Warnings are things that went differently than intended but did not
	// stop the run: a comment that could not be posted, a dependency that
	// could not be checked, a truncated input.
	Warnings []string `json:"warnings,omitempty"`
	// Error is present exactly when OK is false.
	Error *ErrorInfo `json:"error,omitempty"`

	DurationMS int64  `json:"duration_ms"`
	StartedAt  string `json:"started_at"`
}

// InputInfo records what the single argument turned out to be. A caller
// debugging a surprising result reads this first: a tool that triaged the
// wrong thing usually classified its input differently than the caller
// assumed.
type InputInfo struct {
	Kind      string `json:"kind"`
	Origin    string `json:"origin"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated,omitempty"`
}

// ModelInfo records which model served the run.
type ModelInfo struct {
	Spec     string `json:"spec"`
	ID       string `json:"id"`
	Vendor   string `json:"vendor"`
	API      string `json:"api"`
	Thinking string `json:"thinking,omitempty"`
}

// UsageInfo is the run's cost, summed over every phase.
type UsageInfo struct {
	InputTokens         int64       `json:"input_tokens"`
	OutputTokens        int64       `json:"output_tokens"`
	CacheReadTokens     int64       `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int64       `json:"cache_creation_tokens,omitempty"`
	CostUSD             float64     `json:"cost_usd"`
	Turns               int         `json:"turns"`
	Phases              []PhaseInfo `json:"phases,omitempty"`
}

// PhaseInfo is one model-facing step of a pipeline.
type PhaseInfo struct {
	Name         string  `json:"name"`
	Turns        int     `json:"turns"`
	StopReason   string  `json:"stop_reason"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	DurationMS   int64   `json:"duration_ms"`
}

// ErrorInfo says what failed and where.
type ErrorInfo struct {
	// Stage is the pipeline step that failed, in the tool's own vocabulary
	// ("preflight", "analyse", "verify", "push").
	Stage string `json:"stage"`
	// Category classifies the failure so a caller can decide whether
	// re-running could help: usage, auth, input, model, budget, max_turns,
	// git, forge, verify, internal.
	Category string `json:"category"`
	Message  string `json:"message"`
}

// Run is the accumulating state of one tool invocation. It is what a
// pipeline reports into, and what Emit turns into the envelope.
//
// It is safe for concurrent use so that a phase streaming events on one
// goroutine can record a warning while the pipeline runs on another.
type Run struct {
	tool    string
	version string
	started time.Time

	mu       sync.Mutex
	warnings []string
	phases   []PhaseInfo
	model    *ModelInfo
	input    *InputInfo
}

// NewRun starts a run's bookkeeping.
func NewRun(tool, version string) *Run {
	return &Run{tool: tool, version: version, started: time.Now()}
}

// SetInput records the classified input.
func (r *Run) SetInput(in Input) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.input = &InputInfo{
		Kind:      in.Kind.String(),
		Origin:    in.Origin,
		Bytes:     len(in.Body),
		Truncated: in.Truncated,
	}
}

// SetModel records the resolved model.
func (r *Run) SetModel(m *core.Model, thinking core.ThinkingLevel, spec string) {
	if r == nil || m == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info := &ModelInfo{Spec: spec, ID: m.ID, Vendor: m.Provider, API: string(m.API)}
	if thinking != core.ThinkingUnset {
		info.Thinking = string(thinking)
	}
	r.model = info
}

// Warn records a non-fatal problem. Duplicates are kept: two failed comment
// posts are two facts, not one.
func (r *Run) Warn(format string, args ...any) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// AddPhase records one model-facing step's cost.
func (r *Run) AddPhase(p PhaseInfo) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.phases = append(r.phases, p)
}

// Warnings returns a copy of the warnings recorded so far.
func (r *Run) Warnings() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.warnings...)
}

// Envelope assembles the object to print. code decides OK: only ExitOK is a
// success, so a tool cannot report ok:true alongside a non-zero exit.
func (r *Run) Envelope(code int, result any, failure *ErrorInfo) Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()

	env := Envelope{
		Tool:       r.tool,
		Version:    r.version,
		OK:         code == ExitOK,
		ExitCode:   code,
		Input:      r.input,
		Model:      r.model,
		Result:     presentOrNil(result),
		Warnings:   append([]string(nil), r.warnings...),
		Error:      failure,
		DurationMS: time.Since(r.started).Milliseconds(),
		StartedAt:  r.started.UTC().Format(time.RFC3339),
	}
	if len(r.phases) > 0 {
		u := &UsageInfo{Phases: append([]PhaseInfo(nil), r.phases...)}
		for _, p := range r.phases {
			u.InputTokens += p.InputTokens
			u.OutputTokens += p.OutputTokens
			u.CostUSD += p.CostUSD
			u.Turns += p.Turns
		}
		env.Usage = u
	}
	if env.OK && env.Error != nil {
		// Defensive: an ok envelope carrying an error is a contradiction the
		// caller should never have to resolve.
		env.OK = false
		env.ExitCode = ExitFailed
	}
	return env
}

// Emit writes the envelope as one indented JSON document followed by a
// newline, and returns the exit code so a caller can `os.Exit(toolio.Emit(…))`.
//
// Indented rather than compact because the primary consumer is a model
// reading a tool result, and a 4KB single-line object is measurably harder
// for one to quote accurately than the same object over 120 lines. Machines
// that prefer compact JSON can pipe it through anything.
func Emit(w io.Writer, env Envelope) int {
	b, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		// Marshalling cannot be allowed to lose the exit code, so fall back
		// to an envelope that is guaranteed to encode.
		fallback, _ := json.MarshalIndent(Envelope{
			Tool: env.Tool, Version: env.Version, OK: false, ExitCode: ExitFailed,
			Error: &ErrorInfo{Stage: "emit", Category: "internal",
				Message: "the result could not be encoded as JSON: " + err.Error()},
		}, "", "  ")
		_, _ = w.Write(append(fallback, '\n'))
		return ExitFailed
	}
	_, _ = w.Write(append(b, '\n'))
	return env.ExitCode
}

// presentOrNil drops a typed nil pointer.
//
// A pipeline that fails before it has a result returns (*Result)(nil), and an
// interface holding a typed nil is not nil — `omitempty` keeps it and the
// envelope grows a `"result": null` that a caller then has to handle as a
// third case beside present and absent.
func presentOrNil(v any) any {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
	}
	return v
}

// SortedUnique is a small helper for result fields that are sets rendered as
// arrays: a stable order makes two runs that found the same thing produce the
// same document.
func SortedUnique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
