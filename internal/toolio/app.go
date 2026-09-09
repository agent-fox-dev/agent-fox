package toolio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/ghapi"
)

// UsageError is a wrong invocation: no input, a flag combination that cannot
// hold, a directory that is not one. It is separated from every other failure
// because it means nothing was fetched and nothing was written, and because
// it is the one class of error worth printing the flag list for.
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
	// GitHub is the REST client, always non-nil. Whether it can write is
	// GitHub.Authenticated().
	GitHub *ghapi.Client
	// Run accumulates warnings and per-phase cost for the envelope.
	Run *Run
	// Progress writes to stderr.
	Progress *Progress
	// Model is the resolved model and the spec that produced it.
	Model *ModelChoice
}

// App is one agent-fox tool.
//
// The three tools share this shell rather than each writing their own,
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

// Main parses, resolves and runs. It returns the process exit code, and it
// writes exactly one JSON object to stdout on every path.
func (a App) Main(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var common Common
	fs := flag.NewFlagSet(a.Name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, a.Usage)
		fs.PrintDefaults()
	}
	common.Register(fs)
	if a.Flags != nil {
		a.Flags(fs)
	}

	run := NewRun(a.Name, a.Version)

	input, err := SplitArgs(fs, argv)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(stderr, "%s: %v\n", a.Name, err)
		}
		return Emit(stdout, run.Envelope(ExitUsage, nil, &ErrorInfo{
			Stage: "usage", Category: "usage", Message: err.Error(),
		}))
	}
	if common.Version {
		fmt.Fprintf(stdout, "%s %s\n", a.Name, a.Version)
		return ExitOK
	}

	progress := NewProgress(stderr, a.Name, common.Verbose, common.Quiet)
	code, result, failure := a.execute(ctx, execArgs{
		common:   &common,
		fs:       fs,
		argument: input,
		run:      run,
		progress: progress,
		stdin:    stdin,
	})
	return Emit(stdout, run.Envelope(code, result, failure))
}

type execArgs struct {
	common   *Common
	fs       *flag.FlagSet
	argument string
	run      *Run
	progress *Progress
	stdin    io.Reader
}

func (a App) execute(ctx context.Context, e execArgs) (int, any, *ErrorInfo) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	if a.PreCheck != nil {
		if err := a.PreCheck(e.common); err != nil {
			return a.usage(e, err)
		}
	}

	ws, err := e.common.Workspace()
	if err != nil {
		return a.usage(e, err)
	}

	gh := ghapi.New(a.Name + "/" + a.Version)

	in, err := Resolve(ctx, e.argument, e.stdin, gh)
	if err != nil {
		if errors.Is(err, ErrNoInput) {
			return a.usage(e, Usagef(
				"no input: give a report, a file path, a GitHub URL, or - to read stdin"))
		}
		return ExitFailed, nil, &ErrorInfo{Stage: "input", Category: "input", Message: err.Error()}
	}
	e.run.SetInput(in)
	if in.Truncated {
		e.run.Warn("the input was truncated at %d bytes", MaxInputBytes)
	}
	if in.Thread != nil && in.Thread.CommentsErr != nil {
		e.run.Warn("the issue's comments could not be read: %v", in.Thread.CommentsErr)
	}
	e.progress.Detail("input: %s (%s, %d bytes)", in.Kind, in.Origin, len(in.Body))

	if a.CheckInput != nil {
		if err := a.CheckInput(in); err != nil {
			return a.usage(e, err)
		}
	}

	choice, err := e.common.ResolveModel()
	if err != nil {
		return ExitFailed, nil, &ErrorInfo{
			Stage: "preflight", Category: agentrun.CategoryOf(err), Message: err.Error(),
		}
	}
	e.run.SetModel(choice.Model, choice.Thinking, choice.Spec)
	e.progress.Detail("model: %s (%s)", choice.Model.ID, choice.Model.Provider)

	runner, err := agentrun.NewRunner(agentrun.Config{
		Model:         choice.Model,
		Thinking:      choice.Thinking,
		Providers:     agentrun.DefaultProviders(),
		Workspace:     ws,
		TrustProject:  e.common.TrustProject,
		Bounds:        e.common.Bounds(a.DefaultBounds),
		Observer:      e.progress,
		ShowText:      e.common.ShowText,
		SessionPrefix: a.Name,
	})
	if err != nil {
		return ExitFailed, nil, &ErrorInfo{
			Stage: "preflight", Category: agentrun.CategoryOf(err), Message: err.Error(),
		}
	}

	return a.Exec(ctx, Deps{
		Common:    e.common,
		Input:     in,
		Workspace: ws,
		Runner:    runner,
		GitHub:    gh,
		Run:       e.run,
		Progress:  e.progress,
		Model:     choice,
	})
}

func (a App) usage(e execArgs, err error) (int, any, *ErrorInfo) {
	e.fs.Usage()
	return ExitUsage, nil, &ErrorInfo{Stage: "usage", Category: "usage", Message: err.Error()}
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
	info := &ErrorInfo{Stage: defaultStage, Category: agentrun.CategoryInternal, Message: err.Error()}
	var se StageError
	if errors.As(err, &se) {
		info.Stage, info.Category = se.StageName(), se.CategoryName()
		return info
	}
	info.Category = agentrun.CategoryOf(err)
	return info
}

// ExitCodeFor maps an error category onto the shared exit table.
func ExitCodeFor(category string) int {
	switch category {
	case "usage":
		return ExitUsage
	case "ambiguous":
		return ExitNeedsHuman
	case "unverified":
		return ExitUnverified
	default:
		return ExitFailed
	}
}
