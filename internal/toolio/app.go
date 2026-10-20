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
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/agent-fox-dev/agentfox/issuex"
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
	// BuildInfo reads the binary's build information;
	// runtime/debug.ReadBuildInfo when nil.
	BuildInfo func() (*debug.BuildInfo, bool)
	// NewForge builds the forge client of a command that uses one;
	// issuex.NewWithOptions when nil. Tests inject their own.
	NewForge func(issuex.Options) (issuex.Client, error)
	// Grace is how long a command that has been told to stop, by a signal or
	// by its deadline, gets to return before the shell gives up on it; 10
	// seconds when zero.
	Grace time.Duration

	commands []*Command
}

// NoArgs is the Command.MaxArgs of a command that takes no positional
// argument.
const NoArgs = -1

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
	// MaxArgs is how many positional arguments the command takes. Zero means
	// one; NoArgs means none. Another is a usage error.
	MaxArgs int
	// Input says whether the first positional argument is an input to
	// resolve.
	Input InputMode
	// UsesForge gives the command a forge client, Run.Forge, and --dir.
	UsesForge bool
	// Dir, Context, DryRun, Repo and Timeout opt the command into the flags
	// --dir, --context, --dry-run, --repo and --timeout. Every command
	// accepts --output and --emit-events.
	Dir     bool
	Context bool
	DryRun  bool
	Repo    bool
	Timeout bool
	// DefaultTimeout is the deadline when --timeout is absent; zero is none.
	DefaultTimeout time.Duration
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
	// Args are the positional arguments, in order.
	Args []string
	// Input is the resolved input; Kind none for a command without one.
	Input Input
	// Dir is --dir, made absolute; empty for a command without the flag.
	Dir string
	// Context holds the --context values, verbatim and in order.
	Context []string
	// DryRun is set when the command was given --dry-run. Run.Forge then
	// refuses every write; the command should not attempt other effects.
	DryRun bool
	// Forge is the forge client; nil for a command that does not use one.
	// The shell closes it when the command returns.
	Forge issuex.Client
	// Target is the repository the forge client works on; invalid when
	// none could be determined.
	Target issuex.Repo
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

// Warn records a warning. The code matches ^[a-z][a-z0-9_]*$. A warning
// already recorded, with the same code and message, is recorded once.
func (r *Run) Warn(code, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	w := Warning{Code: code, Message: message}
	if !slices.Contains(r.warnings, w) {
		r.warnings = append(r.warnings, w)
	}
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

	if code, answered := a.answer(words, started, stdout); answered {
		return code
	}

	cmd, rest := a.match(words)
	if cmd == nil {
		env := a.newEnvelope(a.name())
		env.fail(&Failure{Category: CategoryUsage, Message: a.unknownCommand(words)})
		return a.write(stdout, env, started)
	}

	env := a.newEnvelope(cmd.Path)
	values, positional, failure := parse(cmd, rest)
	if failure != nil {
		env.fail(failure)
		return a.write(stdout, env, started)
	}

	if values.timeout < 0 {
		env.fail(&Failure{Category: CategoryUsage, Message: fmt.Sprintf("--timeout %s is negative; use 0 for no limit", values.timeout)})
		return a.write(stdout, env, started)
	}

	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	run := &Run{
		Tool:    cmd.Path,
		Argv:    append([]string{a.name()}, words...),
		Args:    positional,
		Input:   Input{Kind: KindNone},
		Dir:     values.dir,
		Context: slices.Clone(values.context),
		Stderr:  stderr,
	}

	// The forge client is closed before the envelope is written, whichever
	// way the invocation ends.
	closeForge := func() {}
	finish := func() int {
		closeForge()
		return a.write(stdout, env, started)
	}
	if cmd.UsesForge {
		client, target, err := a.forgeFor(cmd, values, positional)
		if err != nil {
			env.fail(failureOf(err, stderr))
			return a.write(stdout, env, started)
		}
		run.Forge, run.Target = client, target
		if values.dryRun {
			run.DryRun = true
			run.Forge = DryRunForge(client, run.SideEffect)
		}
		// A client that fails to close its idle connections has nothing
		// more to tell.
		closeForge = func() { _ = client.Close() }
	}

	if cmd.Input != InputNone {
		in, err := resolveInput(sigCtx, cmd.Input, positional, stdin, run.Forge)
		if err != nil {
			env.fail(failureOf(err, stderr))
			return finish()
		}
		run.Input = in
		env.Input = InputInfo{Kind: in.Kind, Origin: in.Origin, Truncated: in.Truncated}
		if in.Truncated {
			run.Warn(WarnInputTruncated, fmt.Sprintf("the input was cut at %d bytes", MaxInputBytes))
		}
	}
	out, err := a.execute(sigCtx, cmd, run, values.timeout)
	a.settle(env, run, out, err, stderr)
	run.collect(env)
	return finish()
}

// match returns the command with the longest registered path that the
// leading non-flag words begin with, and the words after that path.
func (a *App) match(words []string) (*Command, []string) {
	leading := leadingWords(words)
	var best *Command
	bestLen := 0
	for _, c := range a.commands {
		path := strings.Fields(c.Path)
		if len(path) > bestLen && len(path) <= len(leading) && slices.Equal(path, leading[:len(path)]) {
			best, bestLen = c, len(path)
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, words[bestLen:]
}

// leadingWords are the words before the first flag.
func leadingWords(words []string) []string {
	for i, w := range words {
		if strings.HasPrefix(w, "-") {
			return words[:i]
		}
	}
	return words
}

// unknownCommand is the usage message for words that name no command: it
// lists every registered command.
func (a *App) unknownCommand(words []string) string {
	commands := strings.Join(a.paths(), ", ")
	if commands == "" {
		commands = "none"
	}
	leading := leadingWords(words)
	if len(leading) == 0 {
		return fmt.Sprintf("no command given; the commands are: %s", commands)
	}
	return fmt.Sprintf("unknown command %q; the commands are: %s", strings.Join(leading, " "), commands)
}

// answer prints what a request for help or the version asks for, wherever on
// the command line it stands, and reports that it did. Neither reads input,
// builds a forge client or creates anything.
func (a *App) answer(words []string, started time.Time, stdout io.Writer) (code int, answered bool) {
	var wantHelp, wantVersion bool
	for _, w := range words {
		if w == "--" {
			break
		}
		switch w {
		case "-h", "-help", "--help":
			wantHelp = true
		case "-version", "--version":
			wantVersion = true
		}
	}

	switch {
	case len(words) > 0 && words[0] == "help":
		// "help" asks for a command by name; one that does not exist is the
		// one reply to a help request that is not plain text.
		cmd, _ := a.match(words[1:])
		if cmd == nil && len(leadingWords(words[1:])) > 0 {
			env := a.newEnvelope(a.name())
			env.fail(&Failure{Category: CategoryUsage, Message: a.unknownCommand(words[1:])})
			return a.write(stdout, env, started), true
		}
		return a.printHelp(cmd, stdout), true
	case wantHelp:
		cmd, _ := a.match(words)
		return a.printHelp(cmd, stdout), true
	case wantVersion:
		data, _ := encodeIndented(a.version())
		_, _ = stdout.Write(data)
		return 0, true
	}
	return 0, false
}

// printHelp prints the help of cmd, or of the program when cmd is nil.
func (a *App) printHelp(cmd *Command, stdout io.Writer) int {
	text := a.listHelp()
	if cmd != nil {
		text = a.commandHelp(cmd)
	}
	_, _ = io.WriteString(stdout, text)
	return 0
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
	env.Summary = oneLine(env.Summary)
	if env.Summary == "" && env.Error != nil {
		env.Summary = defaultSummary(env.Error.Category)
	}

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
