package toolio

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
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

// StatusFor maps an exit code to its status string.
func StatusFor(code int) string {
	switch code {
	case ExitOK:
		return "done"
	case ExitFailed:
		return "failed"
	case ExitUsage:
		return "usage"
	case ExitNeedsHuman:
		return "needs_human"
	case ExitUnverified:
		return "unverified"
	default:
		return "failed"
	}
}

// Summarizer is implemented by Result types that supply a summary of the run.
type Summarizer interface {
	Summary() string
}

// Resumabler is implemented by Result types that report whether re-running
// the same input continues rather than restarts the work.
type Resumabler interface {
	Resumable() bool
}

// Summarizable is implemented by Result types that supply a trimmed
// "summary" view of themselves — the shape emitted on stdout by default,
// under --detail summary (06-REQ-3). Only the fields a caller acts on next
// are kept; every other field of the full Result is still computed exactly
// as under --detail full, and is still written in full to the report file
// (06-REQ-3.5). A Result that has nothing worth trimming need not implement
// this: ApplyDetail falls back to the full value.
type Summarizable interface {
	SummaryView() any
}

// ArtifactKind is the closed set of shapes an Artifact can take (06-REQ-4.1).
// A caller chaining tools switches on this rather than guessing which
// fields are meaningful.
type ArtifactKind string

// The closed set of artifact kinds. There is no other.
const (
	ArtifactBranch      ArtifactKind = "branch"
	ArtifactCommit      ArtifactKind = "commit"
	ArtifactPullRequest ArtifactKind = "pull_request"
	ArtifactComment     ArtifactKind = "comment"
	ArtifactIssue       ArtifactKind = "issue"
	ArtifactSpecPackage ArtifactKind = "spec_package"
	ArtifactReportFile  ArtifactKind = "report_file"
)

// Artifact is one thing a run produced. Every field below is meaningful for
// some kinds and not others; MarshalJSON emits only the fields fixed for
// this entry's own Kind (plus dry_run when it is set), so a caller reading
// one never has to know which fields to ignore (06-REQ-4.1).
type Artifact struct {
	Kind ArtifactKind

	// branch
	Name string
	Base string

	// commit
	SHA    string
	Branch string

	// pull_request, issue: URL and Number. comment: URL only.
	URL    string
	Number int

	// spec_package
	Path  string
	ID    string
	Valid bool

	// report_file: Path (above).

	// DryRun marks an entry as hypothetical: something that would have
	// happened on a forge or a remote under --dry-run, but did not
	// (06-REQ-4.3). Never marshalled when false, since an entry for
	// something that actually happened locally — a branch, a commit —
	// carries no marker at all (06-REQ-4.4).
	DryRun bool
}

// MarshalJSON emits exactly the fields fixed for a's own Kind, so a branch
// entry is {kind,name,base} and nothing else, never the zero values of the
// fields other kinds use (06-REQ-4.1).
func (a Artifact) MarshalJSON() ([]byte, error) {
	m := map[string]any{"kind": string(a.Kind)}
	switch a.Kind {
	case ArtifactBranch:
		m["name"] = a.Name
		m["base"] = a.Base
	case ArtifactCommit:
		m["sha"] = a.SHA
		m["branch"] = a.Branch
	case ArtifactPullRequest:
		m["url"] = a.URL
		m["number"] = a.Number
	case ArtifactComment:
		m["url"] = a.URL
	case ArtifactIssue:
		m["url"] = a.URL
		m["number"] = a.Number
	case ArtifactSpecPackage:
		m["path"] = a.Path
		m["id"] = a.ID
		m["valid"] = a.Valid
	case ArtifactReportFile:
		m["path"] = a.Path
	}
	if a.DryRun {
		m["dry_run"] = true
	}
	return json.Marshal(m)
}

// ArtifactsProvider is implemented by Result types that supply what the run
// produced, in the uniform shape Artifact fixes (06-REQ-4). Every entry
// must be read off a fact the Result already holds — Branch, Commit,
// PullRequestURL, Comments, SpecDir, Validation.Valid and the rest — never
// from a field that is the model's own account of its work.
type ArtifactsProvider interface {
	Artifacts() []Artifact
}

// SideEffect is one write a run made to a forge or a remote (06-REQ-5).
//
// Action is one of create_issue, update_issue, comment, push, open_pr.
// Target names what was written to: "origin <branch>" for a push,
// "<owner>/<repo>#<number>" for a comment or an issue update,
// "<owner>/<repo>" for an issue that has no number yet, and
// "<owner>/<repo>#<pr-number>" for a pull request. Warning is set only when
// OK is false and the call site recorded a Run.Warn for the same failure: the
// two come from one call site, so they cannot disagree.
type SideEffect struct {
	Action  string   `json:"action"`
	Target  string   `json:"target"`
	OK      bool     `json:"ok"`
	Warning WarnCode `json:"warning,omitempty"`
}

// SameInputPlaceholder stands for the original input in a suggested
// invocation whose input may be arbitrarily large (raw text, stdin, a
// report too long to repeat). needs_human.resume and a next[] entry that
// must agree with it both use it.
const SameInputPlaceholder = "<same input>"

// AnswerPlaceholder stands for the caller's answer to a needs_human question
// in a resume command.
const AnswerPlaceholder = "\"<answer>\""

// Next is one suggested follow-up invocation (06-REQ-6): the tool to run,
// the single input to give it, the flags to add, and why. It is derived in
// Go from a tool's own Result, never from the model.
type Next struct {
	Tool  string   `json:"tool"`
	Input string   `json:"input"`
	Flags []string `json:"flags"`
	Why   string   `json:"why"`
}

// Command renders n as one command line: tool, input, flags, space-joined.
func (n Next) Command() string {
	parts := append([]string{n.Tool, n.Input}, n.Flags...)
	return strings.Join(parts, " ")
}

// ResumeNext is the suggestion to run tool again on the same input with an
// answer to its question: the one shape needs_human.resume and a tool's own
// next[] ambiguity entry share, so the two cannot disagree. Its input is the
// placeholder, not the literal argument, because a report can be up to
// 256 KB and is not runnable as given in a JSON array.
func ResumeNext(tool, why string) Next {
	return Next{Tool: tool, Input: SameInputPlaceholder, Flags: []string{"--context", AnswerPlaceholder}, Why: why}
}

// ResumePlaceholder is how a next[] entry names in to re-run it: the
// literal origin (a file path or an issue URL) when the input had a small,
// stable one, and SameInputPlaceholder when it was raw text or stdin.
func ResumePlaceholder(in Input) string {
	switch in.Kind {
	case KindFile, KindIssue:
		if in.Origin != "" {
			return in.Origin
		}
	}
	return SameInputPlaceholder
}

// NextProvider is implemented by Result types that suggest their own
// follow-up invocations (06-REQ-6). Every entry must be derived from fact
// fields the Result already holds, never from the model's own report.
type NextProvider interface {
	Next() []Next
}

// Option is one possible answer to a question in needs_human.
type Option struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// NeedsHuman carries the question a person must answer when status is "needs_human".
type NeedsHuman struct {
	Question string   `json:"question"`
	Options  []Option `json:"options,omitempty"`
	Needed   string   `json:"needed,omitempty"`
	Stage    string   `json:"stage"`
	Resume   string   `json:"resume"`
}

// NeedsHumanSource is implemented by Result types that supply human decision details.
type NeedsHumanSource interface {
	NeedsHuman() (question string, options []Option, needed string, ok bool)
}

// Warning is one thing that went differently than intended but did not stop
// the run: a comment that could not be posted, a dependency that could not
// be checked, a truncated input. Code is a stable identifier a caller can
// switch on without parsing Message; Stage is looked up centrally from
// warnStages, never supplied at the call site, so a code cannot be recorded
// against two different stages by accident.
type Warning struct {
	Code     WarnCode `json:"code"`
	Severity string   `json:"severity"`
	Stage    string   `json:"stage"`
	Message  string   `json:"message"`
}

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
	// Status is one of done, failed, usage, needs_human, unverified.
	Status string `json:"status"`
	// ExitCode is the process's exit code, repeated here so a caller that
	// captured only stdout still has it.
	ExitCode int `json:"exit_code"`
	// Summary is one sentence describing the outcome.
	Summary string `json:"summary"`

	// Error is present exactly when OK is false.
	Error *ErrorInfo `json:"error,omitempty"`
	// NeedsHuman is set when the tool stopped for a human decision.
	NeedsHuman *NeedsHuman `json:"needs_human,omitempty"`

	// Warnings are things that went differently than intended but did not
	// stop the run: a comment that could not be posted, a dependency that
	// could not be checked, a truncated input.
	Warnings []Warning `json:"warnings,omitempty"`
	Result   any       `json:"result,omitempty"`

	// Artifacts is what the run produced, in one uniform shape across every
	// tool, over the closed ArtifactKind set. Present whenever the run
	// produced anything (06-REQ-4).
	Artifacts []Artifact `json:"artifacts,omitempty"`

	// SideEffects is every write the run made to a forge or a remote, in
	// the order it happened. Omitted when there were none — which is
	// always the case under --dry-run (06-REQ-5).
	SideEffects []SideEffect `json:"side_effects,omitempty"`

	// Next is the invocations a caller would plausibly make next, derived
	// from the result in Go. Omitted when there is nothing to suggest
	// (06-REQ-6).
	Next []Next `json:"next,omitempty"`

	Input *InputInfo `json:"input,omitempty"`
	Usage *UsageInfo `json:"usage,omitempty"`
	Model *ModelInfo `json:"model,omitempty"`

	DurationMS int64  `json:"duration_ms"`
	StartedAt  string `json:"started_at"`

	// ReportFile is the path the complete envelope was written to, present
	// whenever that write succeeded. Absent when the write failed (a
	// low-severity report_file_not_written warning explains why) or when
	// this envelope is never written to a report file at all (the two
	// purely human-driven paths: a bare invocation on a terminal, and
	// -h/--help/--version).
	ReportFile string `json:"report_file,omitempty"`
}

// InputInfo records what the single argument turned out to be. A caller
// debugging a surprising result reads this first: a tool that triaged the
// wrong thing usually classified its input differently than the caller
// assumed.
type InputInfo struct {
	Kind         string `json:"kind"`
	Origin       string `json:"origin"`
	Bytes        int    `json:"bytes"`
	Truncated    bool   `json:"truncated,omitempty"`
	ContextBytes int    `json:"context_bytes,omitempty"`
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

// FixHint is a machine-usable remedy for a failure.
type FixHint struct {
	Flag    string   `json:"flag,omitempty"`
	Env     []string `json:"env,omitempty"`
	Valid   []string `json:"valid,omitempty"`
	Current float64  `json:"current,omitempty"`
	Suggest float64  `json:"suggest,omitempty"`
}

// ErrorInfo says what failed and where.
type ErrorInfo struct {
	// Stage is the pipeline step that failed, in the tool's own vocabulary
	// ("preflight", "analyse", "verify", "push").
	Stage string `json:"stage"`
	// Category classifies the failure so a caller can decide whether
	// re-running could help: usage, auth, input, model, budget, max_turns,
	// git, forge, verify, internal.
	Category    string   `json:"category"`
	Message     string   `json:"message"`
	Retryable   bool     `json:"retryable"`
	Resumable   bool     `json:"resumable"`
	FixHint     *FixHint `json:"fix_hint,omitempty"`
	TotalBudget float64  `json:"-"`
	err         error    `json:"-"`
}

func (e *ErrorInfo) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ErrorInfo) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *ErrorInfo) TotalBudgetUSD() float64 {
	if e == nil {
		return 0
	}
	if e.TotalBudget > 0 {
		return e.TotalBudget
	}
	var tb interface{ TotalBudgetUSD() float64 }
	if errors.As(e.err, &tb) {
		return tb.TotalBudgetUSD()
	}
	return 0
}

// RetryableFor reports whether an error of the given category can succeed
// if re-run unchanged. Only "api" and "aborted" are retryable.
func RetryableFor(category string) bool {
	return category == "api" || category == "aborted"
}

// BuildErrorInfo builds an ErrorInfo with Retryable set according to the category.
func BuildErrorInfo(category string, extra ...string) *ErrorInfo {
	stage := "error"
	message := ""
	if len(extra) > 0 {
		stage = extra[0]
	}
	if len(extra) > 1 {
		message = extra[1]
	}
	return &ErrorInfo{
		Stage:     stage,
		Category:  category,
		Message:   message,
		Retryable: RetryableFor(category),
	}
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
	warnings []Warning
	effects  []SideEffect
	phases   []PhaseInfo
	model    *ModelInfo
	input    *InputInfo
	bounds   agentrun.Bounds
}

// NewRun starts a run's bookkeeping.
func NewRun(tool, version string) *Run {
	return &Run{tool: tool, version: version, started: time.Now()}
}

// SetBounds records the resolved per-phase ceilings.
func (r *Run) SetBounds(b agentrun.Bounds) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bounds = b
}

// SetInput records the classified input.
func (r *Run) SetInput(in Input) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.input = &InputInfo{
		Kind:         in.Kind.String(),
		Origin:       in.Origin,
		Bytes:        len(in.Body),
		Truncated:    in.Truncated,
		ContextBytes: len(in.Context),
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
// posts are two facts, not one. code must be a declared WarnCode — its
// stage is looked up from the shared table, never passed in, so a code
// cannot be recorded against two different stages by accident.
func (r *Run) Warn(code WarnCode, severity string, format string, args ...any) {
	if r == nil {
		return
	}
	stage, _ := WarnStage(code)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, Warning{
		Code:     code,
		Severity: severity,
		Stage:    stage,
		Message:  strings.TrimSpace(fmt.Sprintf(format, args...)),
	})
}

// RecordSideEffect records one write to a forge or a remote, in the order it
// happened. It is called at the one place each write actually happens,
// beside the Run.Warn the same site makes on failure, and passes that
// warning's code as warn when ok is false. An ok entry never carries a
// warning, whatever was passed, so the two cannot disagree (06-REQ-5.4).
func (r *Run) RecordSideEffect(action, target string, ok bool, warn WarnCode) {
	if r == nil {
		return
	}
	if ok {
		warn = ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.effects = append(r.effects, SideEffect{Action: action, Target: target, OK: ok, Warning: warn})
}

// SideEffects returns a copy of the writes recorded so far, in order.
func (r *Run) SideEffects() []SideEffect {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.effects) == 0 {
		return nil
	}
	return append([]SideEffect(nil), r.effects...)
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
func (r *Run) Warnings() []Warning {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Warning(nil), r.warnings...)
}

// WarningMessages returns just the message text of each warning, in order.
func WarningMessages(ws []Warning) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.Message
	}
	return out
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
		Status:     StatusFor(code),
		ExitCode:   code,
		Error:      failure,
		Input:      r.input,
		Model:      r.model,
		Result:     presentOrNil(result),
		Warnings:   append([]Warning(nil), r.warnings...),
		DurationMS: time.Since(r.started).Milliseconds(),
		StartedAt:  r.started.UTC().Format(time.RFC3339),
	}
	if len(r.effects) > 0 {
		env.SideEffects = append([]SideEffect(nil), r.effects...)
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
		env.Status = StatusFor(ExitFailed)
	}

	if code == ExitNeedsHuman {
		if nhs, ok := env.Result.(NeedsHumanSource); ok {
			if q, opts, needed, ok := nhs.NeedsHuman(); ok {
				stage := ""
				if env.Error != nil {
					stage = env.Error.Stage
				}
				env.NeedsHuman = &NeedsHuman{
					Question: q,
					Options:  opts,
					Needed:   needed,
					Stage:    stage,
					Resume:   ResumeNext(r.tool, "").Command(),
				}
			}
		}
	}

	var summary string
	if s, ok := env.Result.(Summarizer); ok {
		summary = strings.TrimSpace(s.Summary())
	}
	if summary != "" {
		if len(summary) > 200 {
			summary = summary[:200]
		}
	} else {
		if env.OK {
			summary = fmt.Sprintf("%s: done", r.tool)
		} else if env.Error != nil && env.Error.Message != "" {
			summary = env.Error.Message
		} else {
			summary = fmt.Sprintf("%s: %s", r.tool, env.Status)
		}
	}
	highCount := 0
	for _, w := range r.warnings {
		if w.Severity == "high" {
			highCount++
		}
	}
	if env.OK && highCount > 0 {
		if highCount == 1 {
			summary += "; 1 high-severity warning (highest severity: high)"
		} else {
			summary += fmt.Sprintf("; %d high-severity warnings (highest severity: high)", highCount)
		}
	}
	env.Summary = summary

	if env.Error != nil {
		errCopy := *env.Error
		errCopy.Retryable = RetryableFor(errCopy.Category)
		if res, ok := env.Result.(Resumabler); ok {
			errCopy.Resumable = res.Resumable()
		} else {
			errCopy.Resumable = false
		}
		if errCopy.FixHint == nil {
			var underlying error = &errCopy
			if errCopy.err != nil {
				underlying = errCopy.err
			}
			errCopy.FixHint = FixHintFor(errCopy.Category, errCopy.Stage, r.bounds, underlying)
		}
		env.Error = &errCopy
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
			Tool: env.Tool, Version: env.Version, OK: false, Status: StatusFor(ExitFailed), ExitCode: ExitFailed,
			Summary: "the result could not be encoded as JSON: " + err.Error(),
			Error: &ErrorInfo{
				Stage: "emit", Category: "internal",
				Message:   "the result could not be encoded as JSON: " + err.Error(),
				Retryable: RetryableFor("internal"),
				Resumable: false,
			},
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
