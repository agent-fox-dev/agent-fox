// Package toolio is the command shell of the af binary. A command registers
// on an App and gets, without writing it again, the contract every af
// command speaks: one JSON envelope on stdout with a fixed status and
// exit-code table, recovery from panics, and the rest of what docs/cli.md
// describes.
package toolio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
)

// contextFlag is the flag a needs_human answer is appended with.
const contextFlag = "--context"

// maxOptions is how many options a needs_human question may offer: the
// letters A to Z.
const maxOptions = 26

// App is the command shell: it dispatches an invocation to a registered
// command and turns what the command returns into the envelope.
type App struct {
	// Name is the program's name, "af" when empty.
	Name string
	// Version, Commit and BuildTime identify the build.
	Version   string
	Commit    string
	BuildTime string
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	commands []*Command
}

// Command is one af command. The shell owns everything around Run.
type Command struct {
	// Path is what the command is registered under, one or more words
	// ("triage", "gate run"). It is the envelope's tool.
	Path string
	// Summary is the one line the command list shows.
	Summary string
	// Usage is the command's synopsis.
	Usage string
	// Flags registers the command's own flags on fs.
	Flags func(fs *flag.FlagSet)
	// Run does the work. It returns an outcome or an error.
	Run func(r *Run) (Outcome, error)
}

// Run is what a command sees of its invocation, and where it reports what it
// noticed along the way.
type Run struct {
	// Ctx is cancelled when the process is told to stop.
	Ctx context.Context
	// Tool is the command path.
	Tool string
	// Argv is the invocation as received, with argv[0] replaced by the
	// program's name.
	Argv []string
	// Stderr is where a command may write diagnostics; the shell writes
	// nothing else to it but a panic's stack.
	Stderr io.Writer

	mu          sync.Mutex
	warnings    []Warning
	artifacts   []Artifact
	sideEffects []SideEffect
	next        []Next
	usage       Usage
}

// Warn records a warning. The code matches ^[a-z][a-z0-9_]*$.
func (r *Run) Warn(code, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.warnings = append(r.warnings, Warning{Code: code, Message: message})
}

// Artifact records something the run produced.
func (r *Run) Artifact(a Artifact) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.artifacts = append(r.artifacts, a)
}

// SideEffect records a write the run made, or would have made.
func (r *Run) SideEffect(s SideEffect) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sideEffects = append(r.sideEffects, s)
}

// Next records a suggested follow-up command.
func (r *Run) Next(n Next) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next = append(r.next, n)
}

// SetUsage records what the brain behind the run cost.
func (r *Run) SetUsage(u Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = u
}

// collect copies what the run recorded into the envelope.
func (r *Run) collect(env *Envelope) {
	r.mu.Lock()
	defer r.mu.Unlock()
	env.Warnings = slices.Clone(r.warnings)
	env.Artifacts = slices.Clone(r.artifacts)
	env.SideEffects = slices.Clone(r.sideEffects)
	env.Next = slices.Clone(r.next)
	env.Usage = r.usage
}

// Register adds a command to the app.
func (a *App) Register(c Command) {
	a.commands = append(a.commands, &c)
}

func (a *App) name() string {
	if a.Name != "" {
		return a.Name
	}
	return "af"
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Main runs one invocation. args is the whole command line, argv[0]
// included. It writes the envelope to stdout and returns the process's exit
// code.
func (a *App) Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	started := a.now()
	var words []string
	if len(args) > 1 {
		words = args[1:]
	}

	cmd := a.match(words)
	if cmd == nil {
		env := a.newEnvelope(a.name())
		env.fail(&Failure{Category: CategoryUsage, Message: "unknown command"})
		return a.write(stdout, env, started)
	}

	env := a.newEnvelope(cmd.Path)
	fs := flag.NewFlagSet(cmd.Path, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	if err := fs.Parse(words[1:]); err != nil {
		env.fail(&Failure{Category: CategoryUsage, Message: err.Error()})
		return a.write(stdout, env, started)
	}

	run := &Run{
		Ctx:    ctx,
		Tool:   cmd.Path,
		Argv:   append([]string{a.name()}, words...),
		Stderr: stderr,
	}
	out, err := call(cmd, run)
	a.settle(env, run, out, err, stderr)
	run.collect(env)
	return a.write(stdout, env, started)
}

// match returns the command registered under the first word.
func (a *App) match(words []string) *Command {
	if len(words) == 0 {
		return nil
	}
	for _, c := range a.commands {
		if c.Path == words[0] {
			return c
		}
	}
	return nil
}

// newEnvelope returns an envelope with the fields that hold unless something
// changes them.
func (a *App) newEnvelope(tool string) *Envelope {
	return &Envelope{
		SchemaVersion: SchemaVersion,
		Tool:          tool,
		Input:         InputInfo{Kind: KindNone},
		Usage:         Usage{CostSource: CostNone},
	}
}

// panicError is a panic of a command, recovered.
type panicError struct {
	value any
	stack []byte
}

func (p *panicError) Error() string { return fmt.Sprintf("command panicked: %v", p.value) }

// call runs the command and turns a panic into an error.
func call(cmd *Command, r *Run) (out Outcome, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &panicError{value: v, stack: debug.Stack()}
		}
	}()
	if cmd.Run == nil {
		return Outcome{}, errors.New("command has no Run function")
	}
	return cmd.Run(r)
}

// settle turns what the command returned into the envelope's status, summary,
// result, needs_human and error.
func (a *App) settle(env *Envelope, r *Run, out Outcome, err error, stderr io.Writer) {
	if err != nil {
		env.fail(failureOf(err, stderr))
		return
	}
	if problem := checkOutcome(out); problem != "" {
		env.fail(&Failure{Category: CategoryInternal, Message: "the command returned an inconsistent outcome: " + problem})
		return
	}
	env.Status = out.Status
	env.Summary = out.Summary
	env.Result = out.Result
	if out.Status == StatusNeedsHuman {
		env.NeedsHuman = newNeedsHuman(out.NeedsHuman, r.Argv)
	}
}

// failureOf is the failure an error stands for: a *Failure as it is, a panic
// as an internal failure whose stack goes to stderr, and any other error as
// an internal failure.
func failureOf(err error, stderr io.Writer) *Failure {
	var p *panicError
	if errors.As(err, &p) {
		fmt.Fprintf(stderr, "panic: %v\n\n%s", p.value, p.stack)
		return &Failure{Category: CategoryInternal, Message: p.Error()}
	}
	var f *Failure
	if errors.As(err, &f) {
		failure := *f
		failure.Message = err.Error()
		return &failure
	}
	return &Failure{Category: CategoryInternal, Message: err.Error()}
}

// checkOutcome returns what is wrong with an outcome, or "". An outcome that
// is inconsistent is a bug in the command.
func checkOutcome(o Outcome) string {
	switch o.Status {
	case StatusDone, StatusUnverified:
		if o.NeedsHuman != nil {
			return fmt.Sprintf("a question with status %s", o.Status)
		}
	case StatusNeedsHuman:
		if o.NeedsHuman == nil || strings.TrimSpace(o.NeedsHuman.Text) == "" {
			return "status needs_human with an empty question"
		}
		if n := len(o.NeedsHuman.Options); n > maxOptions {
			return fmt.Sprintf("%d options, at most %d", n, maxOptions)
		}
	default:
		return fmt.Sprintf("status %q is not done, unverified or needs_human", o.Status)
	}
	return ""
}

// newNeedsHuman is the envelope's needs_human for q: the options lettered in
// order, and the invocation to re-run with an answer.
func newNeedsHuman(q *Question, argv []string) *NeedsHuman {
	options := make([]Option, len(q.Options))
	for i, label := range q.Options {
		options[i] = Option{ID: string(rune('A' + i)), Label: label}
	}
	return &NeedsHuman{
		Question: q.Text,
		Options:  options,
		Resume:   Resume{Argv: slices.Clone(argv), Flag: contextFlag},
	}
}

// write stamps the envelope with its times and exit code, prints it, and
// returns the exit code.
func (a *App) write(stdout io.Writer, env *Envelope, started time.Time) int {
	finished := a.now()
	env.StartedAt = stamp(started)
	env.FinishedAt = stamp(finished)
	env.DurationMS = finished.Sub(started).Milliseconds()
	env.ExitCode = ExitCode(env.Status)

	data, err := env.marshal()
	if err != nil {
		// The result cannot be encoded. One envelope is still owed.
		env.fail(&Failure{Category: CategoryInternal, Message: "the result cannot be encoded as JSON: " + err.Error()})
		env.ExitCode = ExitCode(env.Status)
		data, _ = env.marshal()
	}
	// A broken stdout changes nothing: the exit code is the status's.
	_, _ = stdout.Write(data)
	return env.ExitCode
}

// stamp formats t as RFC 3339 in UTC with milliseconds.
func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
