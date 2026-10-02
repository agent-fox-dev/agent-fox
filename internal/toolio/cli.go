package toolio

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// ModelEnv is the variable that selects the model. AGENTKIT_MODEL is honoured
// as a fallback so a shell already configured for the SDK's own examples
// works here too.
const (
	ModelEnv         = "AF_MODEL"
	ModelEnvFallback = "AGENTKIT_MODEL"
	// VendorEnv selects which tier table SIMPLE/STANDARD/ADVANCED resolve
	// against. It has no effect on a model named by id.
	VendorEnv = "AF_MODEL_VENDOR"
)

// DefaultModel is the tier every tool runs on unless told otherwise. It is a
// tier rather than a model id so that the tools keep working when a vendor
// retires an id, and so that nobody has to know which id is current this
// month.
const DefaultModel = "STANDARD"

// Common is the flag set every agent-fox tool shares.
//
// The tools take exactly one positional argument — text, a file path, an
// issue URL on GitHub or GitLab, or "-" for stdin — and everything else is a
// flag. Keeping the flags identical across the tools is deliberate: a caller
// that can drive one can drive all of them.
type Common struct {
	Dir          string
	Model        string
	Vendor       string
	Variant      string
	MaxTurns     int
	Budget       float64
	Timeout      time.Duration
	TrustProject bool
	Verbose      bool
	Quiet        bool
	ShowText     bool
	Version      bool
	// Schema prints the tool's self-description document and exits.
	Schema  bool
	Context []string
	// Detail selects the result view emitted on stdout: "summary" (the
	// default, a per-tool trimmed subset) or "full" (everything the tool
	// computed). The complete value is always available in the report file
	// regardless of what this selects.
	Detail string
	// ReportFile overrides where the complete envelope is written. Empty
	// means the computed default under $XDG_STATE_HOME/agent-fox/runs (or
	// its ~/.local/state fallback).
	ReportFile string
	// InputKind forces how the single argument is classified: "file",
	// "text", "issue" or "stdin". Empty means auto-classify (Resolve).
	InputKind string
	// DryRun makes no remote change: no push, no write to a forge. It is
	// defined here, once, so it means the same thing on every tool; each
	// tool's help says what it still does locally.
	DryRun bool
	// TotalBudgetUSD is a ceiling, in dollars, on the run's total spend
	// across every phase. Zero means no ceiling beyond the per-phase
	// --budget.
	TotalBudgetUSD float64
	// Events selects what stderr carries: "text" (the default, the human
	// progress lines) or "jsonl" (one JSON event object per line).
	Events string
	// EventsFile names a file that receives the JSONL event stream whatever
	// Events says about stderr. Empty means no file.
	EventsFile string
	// Output is where a second, atomic copy of the stdout envelope is
	// written. Empty means no copy. It is resolved against the process's
	// working directory, never --dir; see ResolveOutput.
	Output string
	// Preflight runs every check that would refuse the run, then stops
	// before any model phase: see App.PreflightExec.
	Preflight bool
}

// DryRunUsage is the one definition of --dry-run, shared by every tool.
const DryRunUsage = "make no remote change: no push, no write to a forge"

// TotalBudgetUsage is the one definition of --total-budget, shared by every
// tool.
const TotalBudgetUsage = "spend ceiling for the whole run across every phase, in dollars; 0 means only the per-phase --budget"

type contextFlag []string

func (f *contextFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ", ")
}

func (f *contextFlag) Set(val string) error {
	*f = append(*f, val)
	return nil
}

// ContextBlock renders the --context values into one labelled block,
// or returns an empty string when none were given.
func (c *Common) ContextBlock() string {
	if c == nil || len(c.Context) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Additional context from the caller\n\n")
	for i, p := range c.Context {
		b.WriteString(p)
		if i < len(c.Context)-1 {
			b.WriteString("\n\n")
		} else {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Register adds the shared flags to fs.
func (c *Common) Register(fs *flag.FlagSet) {
	fs.StringVar(&c.Dir, "dir", ".", "the repository to work in; the file tools cannot reach outside it")
	fs.StringVar(&c.Model, "model", "", "model tier (SIMPLE, STANDARD, ADVANCED) or catalog spec; default $"+ModelEnv+", else "+DefaultModel)
	fs.StringVar(&c.Vendor, "vendor", "", "vendor whose tier table the tier names resolve against; default $"+VendorEnv)
	fs.StringVar(&c.Variant, "variant", "", "tier variant, e.g. extended")
	fs.IntVar(&c.MaxTurns, "max-turns", 0, "per-phase turn ceiling")
	fs.Float64Var(&c.Budget, "budget", 0, "per-phase spend ceiling, in dollars")
	fs.DurationVar(&c.Timeout, "phase-timeout", 0, "wall-clock ceiling on one phase")
	fs.BoolVar(&c.TrustProject, "trust-project", false, "admit AGENTS.md, CLAUDE.md and .specs/steering.md into the system prompt")
	fs.BoolVar(&c.Verbose, "verbose", false, "trace tool calls and timings on stderr")
	fs.BoolVar(&c.Quiet, "quiet", false, "print nothing on stderr")
	fs.BoolVar(&c.ShowText, "show-text", false, "stream the model's prose to stderr")
	fs.BoolVar(&c.Version, "version", false, "print the build identity and exit")
	fs.BoolVar(&c.Schema, "schema", false, "print this tool's flags and result JSON Schema and exit, doing no work")
	fs.Var((*contextFlag)(&c.Context), "context", "additional context from the caller, repeatable")
	fs.StringVar(&c.Detail, "detail", "summary", "result view: summary (default, a trimmed subset) or full (everything computed)")
	DeclareEnum(fs, "detail", []string{"summary", "full"})
	fs.StringVar(&c.InputKind, "input-kind", "", "force how the argument is classified: file, text, issue or stdin; a mismatch is a usage error (default: guess)")
	fs.BoolVar(&c.DryRun, "dry-run", false, DryRunUsage)
	fs.Float64Var(&c.TotalBudgetUSD, "total-budget", 0, TotalBudgetUsage)
	fs.StringVar(&c.Events, "events", EventsText, "what stderr carries: text (default, human-readable progress) or jsonl (one JSON event object per line)")
	DeclareEnum(fs, "events", []string{EventsText, EventsJSONL})
	fs.StringVar(&c.EventsFile, "events-file", "", "also write the JSONL event stream to this file (truncated), whatever --events says about stderr")
	fs.StringVar(&c.Output, "output", "", "also write a copy of the stdout envelope to this file, atomically and before stdout; relative to the working directory")
	fs.BoolVar(&c.Preflight, "preflight", false, "run every check that would refuse the run, then stop; makes no change beyond a verification baseline")
	fs.StringVar(&c.ReportFile, "report-file", "", "where to write the complete envelope; default $XDG_STATE_HOME/agent-fox/runs/<tool>-<started>-<pid>.json")
}

// ResolveOutput validates --output and returns the absolute path it names,
// or "" when none was given. "-" (which already means stdin for the
// positional input) and a path that is an existing directory are usage
// errors. The path is resolved against the process's working directory, not
// --dir: it names a destination on the machine running the tool, not a file
// inside the sandboxed workspace. A missing parent is not an error; it is
// created when the envelope is written.
func (c *Common) ResolveOutput() (string, error) {
	if c.Output == "" {
		return "", nil
	}
	if c.Output == "-" {
		return "", Usagef("--output cannot be %q: that means stdin for the input, not a file", "-")
	}
	abs, err := filepath.Abs(c.Output)
	if err != nil {
		return "", Usagef("resolving --output %q: %v", c.Output, err)
	}
	if info, serr := os.Stat(abs); serr == nil && info.IsDir() {
		return "", Usagef("--output %s is a directory, not a file", abs)
	}
	return abs, nil
}

// ValidInputKind refuses any --input-kind value other than the four source
// kinds. Empty is legal and means auto-classify.
func (c *Common) ValidInputKind() error {
	switch c.InputKind {
	case "", string(KindFile), string(KindText), string(KindIssue), string(KindStdin):
		return nil
	default:
		return fmt.Errorf("--input-kind must be one of %q, %q, %q or %q, got %q",
			KindFile, KindText, KindIssue, KindStdin, c.InputKind)
	}
}

// The two values --events accepts.
const (
	EventsText  = "text"
	EventsJSONL = "jsonl"
)

// ValidEvents refuses any --events value other than "text" or "jsonl". An
// empty value (the zero Common, before Register runs) is treated as the
// default.
func (c *Common) ValidEvents() error {
	switch c.Events {
	case "", EventsText, EventsJSONL:
		return nil
	default:
		return fmt.Errorf("--events must be %q or %q, got %q", EventsText, EventsJSONL, c.Events)
	}
}

// ValidTotalBudget refuses a negative --total-budget.
func (c *Common) ValidTotalBudget() error {
	if c.TotalBudgetUSD < 0 {
		return fmt.Errorf("--total-budget cannot be negative")
	}
	return nil
}

// ValidDetail refuses any --detail value other than "summary" or "full". An
// empty value (the zero Common, before Register runs) is treated as the
// default so a caller building a Common by hand is not forced through
// Register first.
func (c *Common) ValidDetail() error {
	switch c.Detail {
	case "", "summary", "full":
		return nil
	default:
		return fmt.Errorf("--detail must be %q or %q, got %q", "summary", "full", c.Detail)
	}
}

// DetailedResult is implemented by every tool's Result type: it records
// which view ("summary" or "full") was emitted, so a caller reading a
// report file or a --detail full run can tell which document it has without
// re-running the tool.
type DetailedResult interface {
	SetDetail(string)
}

// FullView marks result as the full view, via DetailedResult, and returns it
// unchanged. A result that does not implement DetailedResult is returned as
// given.
func FullView(result any) any {
	if dr, ok := result.(DetailedResult); ok {
		dr.SetDetail("full")
	}
	return result
}

// ApplyDetail returns the value to put in the envelope's "result" key for
// the given --detail selection: result's own SummaryView() when detail is
// "summary" (or empty, the zero Common's default) and result implements
// Summarizable, and the full value — via FullView — otherwise. A Result
// that does not implement Summarizable has nothing to trim, so it is always
// emitted in full regardless of what --detail asked for.
//
// It answers only "what goes under result" — App.emit derives every other
// envelope field (Summary, NeedsHuman, Error.Resumable) from the full value
// on every run, since those decide the outcome and must not depend on what
// --detail trimmed away.
func ApplyDetail(detail string, result any) any {
	if wantsSummary(detail) {
		if s, ok := result.(Summarizable); ok {
			return s.SummaryView()
		}
	}
	return FullView(result)
}

// ModelSpec is the model the run will use, after the environment is consulted.
func (c *Common) ModelSpec() string {
	if c.Model != "" {
		return c.Model
	}
	if v := os.Getenv(ModelEnv); v != "" {
		return v
	}
	if v := os.Getenv(ModelEnvFallback); v != "" {
		return v
	}
	return DefaultModel
}

// VendorName is the tier table to resolve against.
func (c *Common) VendorName() string {
	if c.Vendor != "" {
		return c.Vendor
	}
	return os.Getenv(VendorEnv)
}

// Bounds are the per-phase ceilings, with the tool's own defaults filled in
// where the operator set nothing.
func (c *Common) Bounds(defaults agentrun.Bounds) agentrun.Bounds {
	b := defaults
	if c.MaxTurns > 0 {
		b.MaxTurns = c.MaxTurns
	}
	if c.Budget > 0 {
		b.MaxBudgetUSD = c.Budget
	}
	if c.Timeout > 0 {
		b.Timeout = c.Timeout
	}
	return b
}

// SinglePhaseBounds is Bounds for a tool whose one phase is the whole run
// (issue, and spec's undivided input): the run-wide --total-budget and the
// per-phase ceiling bound the same spend, so the lower of the two is the
// phase's effective ceiling.
func (c *Common) SinglePhaseBounds(defaults agentrun.Bounds) agentrun.Bounds {
	b := c.Bounds(defaults)
	if c.TotalBudgetUSD > 0 && (b.MaxBudgetUSD <= 0 || c.TotalBudgetUSD < b.MaxBudgetUSD) {
		b.MaxBudgetUSD = c.TotalBudgetUSD
	}
	return b
}

// Workspace resolves --dir and roots the file tools at it. Every path a tool
// is handed is resolved against this root, symlinks included, so a path that
// escapes is refused by the tool rather than by a paragraph in a prompt.
func (c *Common) Workspace() (*tools.Workspace, error) {
	abs, err := filepath.Abs(c.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolving --dir %q: %w", c.Dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("--dir %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--dir %s is not a directory", abs)
	}
	ws, err := tools.NewWorkspace(abs)
	if err != nil {
		return nil, fmt.Errorf("rooting the file tools at %s: %w", abs, err)
	}
	return ws, nil
}

// ResolveModel resolves the model spec and checks that this shell can
// authenticate to its vendor.
//
// Both happen before a prompt is built. An operator learns about an unsupported
// platform variable, an unknown model or a missing key in the first second,
// rather than in the tenth minute of a run they are paying for.
func (c *Common) ResolveModel() (*ModelChoice, error) {
	return c.ResolveModelNamed(c.ModelSpec())
}

// ResolveModelNamed resolves one model by tier name or catalog spec, against
// the run's vendor and variant, and checks its credential. It is what a tool
// that runs one phase on another model than the rest resolves that model
// with, so the second choice obeys the same rules as the first.
func (c *Common) ResolveModelNamed(spec string) (*ModelChoice, error) {
	if err := agentrun.CheckUnsupportedPlatformVars(); err != nil {
		return nil, err
	}
	m, thinking, err := agentrun.ResolveModel(spec, c.Variant, c.VendorName())
	if err != nil {
		return nil, err
	}
	if err := agentrun.CheckCredentials(m); err != nil {
		return nil, err
	}
	return &ModelChoice{Model: m, Thinking: thinking, Spec: spec}, nil
}

// ModelChoice is the resolved model and the spec string that produced it.
// The envelope reports both, so a surprising answer can be traced back to
// what was asked for.
type ModelChoice struct {
	Model    *core.Model
	Thinking core.ThinkingLevel
	Spec     string
}

// SplitArgs separates the one positional argument from the flags, allowing
// flags on either side of it.
//
// A report is frequently a multi-word string, and requiring it last is a
// papercut a caller hits every time. Anything after the first non-flag token
// that itself starts with "-" is still parsed as a flag, except the bare "-",
// which means stdin.
func SplitArgs(fs *flag.FlagSet, argv []string) (positional string, err error) {
	var rest []string
	var positionals []string

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			positionals = append(positionals, argv[i+1:]...)
			i = len(argv)
		case a == "-":
			positionals = append(positionals, a)
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
			// A flag written as "--name value" consumes the next token; one
			// written as "--name=value" does not. Booleans consume nothing.
			if !strings.Contains(a, "=") && i+1 < len(argv) && needsValue(fs, a) {
				i++
				rest = append(rest, argv[i])
			}
		default:
			positionals = append(positionals, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		return "", err
	}
	switch len(positionals) {
	case 0:
		return "", nil
	case 1:
		return positionals[0], nil
	default:
		return "", fmt.Errorf("expected one input, got %d: %s — quote a multi-word report",
			len(positionals), strings.Join(positionals, " "))
	}
}

// needsValue reports whether a flag takes a separate value token. A boolean
// flag does not, which is what makes `--dry-run ./crash.log` work.
func needsValue(fs *flag.FlagSet, arg string) bool {
	name := strings.TrimLeft(arg, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false // unknown: let Parse report it
	}
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !bf.IsBoolFlag()
}
