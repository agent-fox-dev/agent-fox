package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/issuex"
)

// UsageError is a wrong invocation: no input on stdin, a flag combination
// that cannot hold, a directory that is not one. It is separated from every
// other failure because it means nothing was fetched and nothing was
// written.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

// Usagef builds a UsageError.
func Usagef(format string, args ...any) error {
	return &UsageError{msg: fmt.Sprintf(format, args...)}
}

// Deps are what an App's Exec receives: everything the shared main has
// already resolved and checked.
type Deps struct {
	// Common is the shared flag block, for a tool that needs a value from it.
	Common *Common
	// Input is the classified single argument.
	Input Input
	// Workspace roots the file tools at --dir.
	Workspace *tools.Workspace
	// Runner drives the model phases.
	Runner *agentrun.Runner
	// Forge is the forge client — GitHub or GitLab, chosen from the input
	// URL or the origin remote — and is always non-nil. Whether it can write
	// is Forge.Authenticated().
	Forge issuex.Client
	// Run accumulates warnings and per-phase cost for the envelope.
	Run *Run
	// Progress writes to stderr.
	Progress *Progress
	// Model is the resolved model and the spec that produced it.
	Model *ModelChoice

	// runner is the configuration Runner was built from, kept so that a
	// second runner on another model shares everything else.
	runner agentrun.Config
}

// RunnerFor builds a second runner on another model — a tier name or a
// catalog spec — with the same workspace, bounds, observer and trust as the
// run's own. It is for a tool that runs one phase on a different model than
// the rest; the model is resolved and its credential checked here, before
// any phase runs.
func (d Deps) RunnerFor(modelSpec string) (*agentrun.Runner, *ModelChoice, error) {
	if d.Common == nil || d.runner.Workspace == nil {
		return nil, nil, fmt.Errorf("no runner configuration to derive from")
	}
	choice, err := d.Common.ResolveModelNamed(modelSpec)
	if err != nil {
		return nil, nil, err
	}
	cfg := d.runner
	cfg.Model, cfg.Thinking = choice.Model, choice.Thinking
	r, err := agentrun.NewRunner(cfg)
	if err != nil {
		return nil, nil, err
	}
	return r, choice, nil
}

// App is one agent-fox tool.
//
// The tools share this shell rather than each writing their own,
// because the shape of the interface is the product decision: one positional
// input, one JSON object out, the same flags, the same exit codes. A shared
// shell is what keeps that true as the tools change.
type App struct {
	// Name is the program name, used in the envelope, the progress prefix
	// and the flag set.
	Name string
	// Version is the build identity.
	Version string
	// Usage is the help text printed above the flag list.
	Usage string
	// Flags registers the tool's own flags.
	Flags func(*flag.FlagSet)
	// DefaultBounds are the per-phase ceilings before the shared flags
	// override them.
	DefaultBounds agentrun.Bounds
	// SinglePhase marks a tool whose one model phase is the whole run (issue,
	// and spec's undivided input). --total-budget and --budget then bound the
	// same spend, so the lower of the two is the phase's effective ceiling.
	// A tool with interior boundaries checks --total-budget there instead.
	SinglePhase bool
	// PreCheck runs after flag parsing and before anything is fetched. It is
	// where a flag combination that cannot hold is refused, so a usage error
	// never costs a network call or a token.
	PreCheck func(*Common) error
	// CheckInput runs after the input is classified and before the model is
	// resolved, for the checks that need to know what the input was.
	CheckInput func(Input) error
	// Exec is the tool. It returns the exit code, the result to put in the
	// envelope, and the error object when there is one.
	Exec func(context.Context, Deps) (int, any, *ErrorInfo)
}

// Main parses, resolves and runs. It returns the process exit code.
//
// It writes exactly one JSON object to stdout on every program-driven path.
// When stdout is not a terminal, a bare invocation with no positional argument
// (or only whitespace) also emits a usage envelope. When stdout is a terminal,
// a bare invocation prints the help text to stderr and writes nothing to stdout,
// the same way -h/--help exits 0 and --version prints a bare sentence.
func (a App) Main(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var common Common
	fs := flag.NewFlagSet(a.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), a.Usage)
		fs.PrintDefaults()
	}
	common.Register(fs)
	if a.Flags != nil {
		a.Flags(fs)
	}

	run := NewRun(a.Name, a.Version)

	// The flag package writes its own complaint (and the usage text) to the
	// flag set's output as it fails. That is buffered, so a flag another tool
	// defines can be reported by name instead of as Go's generic "flag
	// provided but not defined"; everything else is passed through as it
	// was.
	var parseOut bytes.Buffer
	fs.SetOutput(&parseOut)
	input, err := SplitArgs(fs, argv)
	fs.SetOutput(stderr)
	run.SetBounds(a.bounds(&common))
	if err != nil {
		if msg, ok := unsupportedFlagMessage(a.Name, err); ok {
			err = Usagef("%s", msg)
		} else {
			io.Copy(stderr, &parseOut)
		}
		if errors.Is(err, flag.ErrHelp) {
			// fs.Parse has already printed the help text to stderr: an
			// explicit -h/--help is a person asking what the tool does, not
			// a program handing over work, so there is nothing to Emit.
			return ExitOK
		}
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, err)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: err.Error(), err: err,
		})
	}
	if derr := common.ValidDetail(); derr != nil {
		// Checked before Workspace(), Resolve() or model resolution: an
		// unrecognized --detail value is a usage error like any other, and
		// nothing should be fetched or spent finding that out.
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, derr)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: derr.Error(), err: derr,
		})
	}
	if berr := common.ValidTotalBudget(); berr != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, berr)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: berr.Error(), err: berr,
		})
	}
	if kerr := common.ValidInputKind(); kerr != nil {
		// Same rule as --detail: refused before Workspace(), Resolve() or
		// model resolution.
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, kerr)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: kerr.Error(), err: kerr,
		})
	}
	if eerr := common.ValidEvents(); eerr != nil {
		// Refused before --version, the bare-invocation check or anything
		// fetched, and before any file or sink exists, so no event is emitted.
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, eerr)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: eerr.Error(), err: eerr,
		})
	}
	if common.Version {
		fmt.Fprintf(stdout, "%s %s\n", a.Name, a.Version)
		return ExitOK
	}
	if strings.TrimSpace(input) == "" {
		// A bare invocation with no positional argument (or only whitespace)
		// prints the help text to stderr. When stdout is a terminal, stdout
		// stays empty; when stdout is not a terminal, an envelope is emitted
		// so programs piping stdout receive valid JSON.
		fs.Usage()
		if isTerminal(stdout) {
			return ExitUsage
		}
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: NoInputMessage,
		})
	}

	// --output is validated here, once: after the two purely human-driven
	// returns (-h, bare on a terminal) that produce no envelope, and before
	// openEvents, PreCheck, Workspace(), Resolve or model resolution.
	if _, oerr := common.ResolveOutput(); oerr != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, oerr)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: oerr.Error(), err: oerr,
		})
	}

	sink, closeSink, serr := a.openEvents(&common, stderr)
	if serr != nil {
		fmt.Fprintf(stderr, "%s: %v\n", a.Name, serr)
		return a.emit(stdout, &common, run, ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: serr.Error(), err: serr,
		})
	}
	defer closeSink()
	// One heartbeat ticker for the run's lifetime, reading spend off the run's
	// own running total. run_end stops it; the deferred stop covers a path
	// that never reaches one.
	sink.StartHeartbeat(run.CostUSD)
	defer sink.StopHeartbeat()

	progress := NewProgress(stderr, a.Name, common.Verbose, common.Quiet)
	progress.SetEvents(sink)
	progress.SetEventsOnStderr(common.Events == EventsJSONL)
	// The run and its progress share one sink, attached once, before execute.
	run.AttachEvents(sink)
	progress.SetShowText(common.ShowText)
	code, result, failure := a.execute(ctx, execArgs{
		common:   &common,
		argument: input,
		run:      run,
		progress: progress,
		stdin:    stdin,
	})
	return a.emit(stdout, &common, run, code, result, failure)
}

// openEvents builds the JSONL sink for a run. The file, when --events-file
// names one, is opened once and truncated, and always carries the stream; the
// stderr writer is active only under --events jsonl and not --quiet. Each
// event is one Write call, so a killed process leaves whole lines only. The
// returned function closes the file. With neither destination the sink is
// inactive.
func (a App) openEvents(common *Common, stderr io.Writer) (*eventsSink, func(), error) {
	var writers []io.Writer
	closeFn := func() {}
	if common.EventsFile != "" {
		f, err := os.OpenFile(common.EventsFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return nil, closeFn, Usagef("--events-file %s: %v", common.EventsFile, err)
		}
		writers = append(writers, f)
		closeFn = func() { _ = f.Close() }
	}
	if common.Events == EventsJSONL && !common.Quiet {
		writers = append(writers, stderr)
	}
	return newEventsSink(a.Name, writers...), closeFn, nil
}

// emit builds the envelope, writes the complete (full-view) envelope to the
// report file, and prints the --detail view to stdout. It is the one path
// every JSON-emitting branch of Main goes through, so a report file is
// written for a success, a failure and a post-classification usage error
// alike (06-REQ-2.1, 06-REQ-2.6) — and, deliberately, not for the two
// purely human-driven paths that return before ever calling it.
//
// The report file always carries the full view of result, independent of
// what --detail asked for on stdout (06-REQ-2.8, 06-REQ-3.5): FullView marks
// it, and that value is what gets written to the file.
//
// Every envelope field derived from the result — Summary, NeedsHuman,
// Error.Resumable — is derived from the full value on both paths, never
// from the trimmed one: those are the 05_envelope_decidable fields that
// decide the outcome, and a summary view that drops Ambiguity or Blocker
// must not also drop the ability to tell that the run needs a human. Only
// the envelope's own "result" key is swapped for the trimmed view, once
// everything else has already been derived from the full one.
func (a App) emit(stdout io.Writer, common *Common, run *Run, code int, result any, failure *ErrorInfo) int {
	full := FullView(result)

	var artifacts []Artifact
	if ap, ok := full.(ArtifactsProvider); ok {
		artifacts = ap.Artifacts()
	}

	var next []Next
	if np, ok := full.(NextProvider); ok {
		next = np.Next()
	}

	// --output is resolved here, at the one funnel every envelope passes
	// through, so each branch of Main is covered. A malformed value (refused
	// as a usage error, or not yet reached by an earlier usage failure)
	// names nothing to write to, so it yields "".
	outPath, _ := common.ResolveOutput()

	path, perr := reportPath(common, a.Name, run)
	// A collision is decided from the two configured destinations alone,
	// before either write is attempted, so its verdict never depends on
	// whether a write succeeds (08-REQ-4.2). It is recorded before the
	// report envelope is built so every envelope carries it. When they
	// collide only the complete report is written there: --output's own
	// write is skipped entirely (08-REQ-4.3).
	if perr == nil && configuredDestinationsCollide(outPath, path) {
		run.Warn(WarnOutputMatchesReportFile, "low", "--output and the report file both name %s: the file there holds the complete report, not the --detail view --output alone would have written", path)
		outPath = ""
	}

	fileEnv := run.Envelope(code, full, failure)
	fileEnv.Artifacts = artifacts
	fileEnv.Next = next

	reportFile := ""
	if perr != nil {
		run.Warn(WarnReportFileNotWritten, "low", "the report file's path could not be determined: %v", perr)
	} else {
		fileEnv.ReportFile = path
		// A report_file artifact entry is added once the path is known, so
		// "what did this run leave behind" is answered by this array too
		// (06-REQ-2.4-derived, 06-REQ-4). It names the same file it sits
		// inside — that is not circular, the same way ReportFile naming
		// itself is not.
		fileEnv.Artifacts = withReportFileArtifact(artifacts, path)
		if werr := WriteReport(path, fileEnv); werr != nil {
			run.Warn(WarnReportFileNotWritten, "low", "the report file could not be written to %s: %v", path, werr)
		} else {
			reportFile = path
		}
	}

	buildEnv := func() Envelope {
		env := run.Envelope(code, full, failure)
		env.ReportFile = reportFile
		env.Artifacts = artifacts
		env.Next = next
		if reportFile != "" {
			env.Artifacts = withReportFileArtifact(artifacts, reportFile)
		}
		if env.Result != nil && wantsSummary(common.Detail) {
			if s, ok := full.(Summarizable); ok {
				env.Result = s.SummaryView()
			}
		}
		return env
	}
	env := buildEnv()

	if outPath != "" {
		if b, merr := json.MarshalIndent(env, "", "  "); merr == nil {
			// The write is attempted before the stdout envelope is final, so a
			// failure can be recorded as a warning that the same envelope
			// carries (08-REQ-3.3). Only the warning changes: ok, status and
			// exit_code are derived from code, never from this write.
			if werr := writeAtomic(outPath, append(b, '\n')); werr != nil {
				run.Warn(WarnOutputNotWritten, "low", "the --output file could not be written to %s: %v", outPath, werr)
				env = buildEnv()
			}
			// Written (or failed and warned) here; EmitWithOutput must not
			// write it a second time.
			outPath = ""
		}
		// A marshal failure falls through: EmitWithOutput writes Emit's
		// fallback envelope to outPath and, having no further envelope to
		// carry a warning, ignores a failure of its own (08-REQ-3.4).
	}
	// run_end goes out immediately before the envelope, from the same code
	// that decided env.OK and env.Status, so the two cannot disagree. A nil or
	// inactive sink (the paths that return before one is built) is a no-op.
	run.eventSink().Emit(newRunEndEvent(env.Status, env.ExitCode))
	return EmitWithOutput(stdout, outPath, env)
}

// withReportFileArtifact appends a report_file entry naming path, without
// mutating the slice artifacts was built from.
func withReportFileArtifact(artifacts []Artifact, path string) []Artifact {
	out := append([]Artifact(nil), artifacts...)
	return append(out, Artifact{Kind: ArtifactReportFile, Path: path})
}

// wantsSummary reports whether detail selects the trimmed view: "summary",
// or the empty string, which is the zero Common's default before Register
// runs.
func wantsSummary(detail string) bool {
	return detail == "" || detail == "summary"
}

// reportPath is the path a report file is written to: --report-file when
// given, else the computed default.
func reportPath(common *Common, tool string, run *Run) (string, error) {
	if common != nil && common.ReportFile != "" {
		return common.ReportFile, nil
	}
	return DefaultReportPath(tool, run.started, os.Getpid())
}

// bounds are the per-phase ceilings this App runs with: the shared flags over
// the tool's defaults, with --total-budget folded in for a one-phase tool.
func (a App) bounds(c *Common) agentrun.Bounds {
	if a.SinglePhase {
		return c.SinglePhaseBounds(a.DefaultBounds)
	}
	return c.Bounds(a.DefaultBounds)
}

type execArgs struct {
	common   *Common
	argument string
	run      *Run
	progress *Progress
	stdin    io.Reader
}

func (a App) execute(ctx context.Context, e execArgs) (int, any, *ErrorInfo) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	bounds := a.bounds(e.common)
	e.run.SetBounds(bounds)

	if a.PreCheck != nil {
		if err := a.PreCheck(e.common); err != nil {
			return a.usage(err)
		}
	}

	ws, err := e.common.Workspace()
	if err != nil {
		return a.usage(err)
	}

	contextBlock := e.common.ContextBlock()
	contextLen := len(contextBlock)

	// Check if source length + context length exceeds MaxInputBytes before fetching anything.
	// For text argument, we can check e.argument directly.
	if len(e.argument)+contextLen > MaxInputBytes {
		return a.usage(fmt.Errorf("input and context together exceed %d bytes (%d bytes)",
			MaxInputBytes, len(e.argument)+contextLen))
	}

	var forge issuex.Client
	if ref, ok := issuex.ParseIssueURL(e.argument); ok && ref.Repo.Host != "" {
		forge, _ = issuex.NewWithOptions(issuex.Options{
			// Pass the repo, not a BaseURL: the web host (github.com) is
			// not the API host (api.github.com); detectForge derives it.
			Repo:      ref.Repo,
			UserAgent: a.Name + "/" + a.Version,
		})
	}
	if forge == nil {
		opts := issuex.Options{UserAgent: a.Name + "/" + a.Version}
		if repo, ok := issuex.DetectRepo(ws.Root); ok {
			opts.Repo = repo
		}
		var forgeErr error
		forge, forgeErr = issuex.NewWithOptions(opts)
		if forgeErr != nil || forge == nil {
			forge = issuex.NewNoOp()
		}
	}

	var in Input
	if e.common.InputKind != "" {
		in, err = ResolveForced(ctx, e.common.InputKind, e.argument, e.stdin, forge, e.run)
	} else {
		in, err = Resolve(ctx, e.argument, e.stdin, forge, e.run)
	}
	if err != nil {
		if errors.Is(err, ErrNoInput) {
			return a.usage(Usagef("%s", NoInputMessage))
		}
		var ue *UsageError
		if errors.As(err, &ue) {
			return a.usage(err)
		}
		return ExitFailed, nil, &ErrorInfo{Stage: "input", Category: "input", Message: err.Error(), err: err}
	}
	if len(in.Body)+contextLen > MaxInputBytes {
		return a.usage(fmt.Errorf("input and context together exceed %d bytes (%d bytes)",
			MaxInputBytes, len(in.Body)+contextLen))
	}
	in.Context = contextBlock
	e.run.SetInput(in)
	if in.Truncated {
		e.run.Warn(WarnInputTruncated, "high", "the input was truncated at %d bytes", MaxInputBytes)
	}
	if in.Thread != nil && in.Thread.CommentsErr != nil {
		e.run.Warn(WarnCommentsUnreadable, "low", "the issue's comments could not be read: %v", in.Thread.CommentsErr)
	}
	e.progress.Detail("input: %s (%s, %d bytes)", in.Kind, in.Origin, len(in.Body))

	if a.CheckInput != nil {
		if err := a.CheckInput(in); err != nil {
			return a.usage(err)
		}
	}

	choice, err := e.common.ResolveModel()
	if err != nil {
		return ExitFailed, nil, &ErrorInfo{
			Stage: "preflight", Category: agentrun.CategoryOf(err), Message: err.Error(), err: err,
		}
	}
	e.run.SetModel(choice.Model, choice.Thinking, choice.Spec)
	e.progress.Detail("model: %s (%s)", choice.Model.ID, choice.Model.Provider)
	// run_start is the stream's first event: the input is known and the model
	// resolved, and no Runner exists yet to emit anything of its own.
	e.run.eventSink().Emit(newRunStartEvent(in.Kind.String(), EventModelInfo{
		Spec: choice.Spec, ID: choice.Model.ID, Vendor: choice.Model.Provider,
	}))

	cfg := agentrun.Config{
		Model:         choice.Model,
		Thinking:      choice.Thinking,
		Providers:     agentrun.DefaultProviders(),
		Workspace:     ws,
		TrustProject:  e.common.TrustProject,
		Bounds:        bounds,
		Observer:      e.progress,
		ShowText:      e.common.ShowText,
		SessionPrefix: a.Name,
	}
	runner, err := agentrun.NewRunner(cfg)
	if err != nil {
		return ExitFailed, nil, &ErrorInfo{
			Stage: "preflight", Category: agentrun.CategoryOf(err), Message: err.Error(), err: err,
		}
	}

	return a.Exec(ctx, Deps{
		Common:    e.common,
		Input:     in,
		Workspace: ws,
		Runner:    runner,
		Forge:     forge,
		Run:       e.run,
		Progress:  e.progress,
		Model:     choice,
		runner:    cfg,
	})
}

// usage reports a UsageError as the envelope's error object, without
// printing the flag list a second time: fs.Usage() is reserved for the two
// paths (a bare invocation, -h/--help) that a person, not a program, hits,
// and this one keeps stdout parseable for the caller that hit it.
func (a App) usage(err error) (int, any, *ErrorInfo) {
	return ExitUsage, nil, &ErrorInfo{Stage: "usage", Category: "usage", Message: err.Error(), err: err}
}

// StageError maps a pipeline failure carrying a stage and a category onto the
// envelope's error object. A failure that carries neither is reported as an
// internal error at the named stage, which is the honest default.
type StageError interface {
	error
	StageName() string
	CategoryName() string
}

// ErrorFrom builds the envelope's error object from a pipeline failure.
func ErrorFrom(defaultStage string, err error) *ErrorInfo {
	info := &ErrorInfo{Stage: defaultStage, Category: agentrun.CategoryInternal, Message: err.Error(), err: err}
	var se StageError
	if errors.As(err, &se) {
		info.Stage, info.Category = se.StageName(), se.CategoryName()
	} else {
		info.Category = agentrun.CategoryOf(err)
	}
	var tb interface{ TotalBudgetUSD() float64 }
	if errors.As(err, &tb) {
		info.TotalBudget = tb.TotalBudgetUSD()
	}
	return info
}

// ExitCodeFor maps an error category onto the shared exit table.
func ExitCodeFor(category string) int {
	switch category {
	case "usage":
		return ExitUsage
	case "ambiguous", "blocked":
		return ExitNeedsHuman
	case "unverified":
		return ExitUnverified
	default:
		return ExitFailed
	}
}
