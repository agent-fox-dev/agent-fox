// Package toolio is the command shell of the af binary. A command registers
// on an App and gets, without writing it again, the contract every af
// command speaks: one JSON envelope on stdout with a fixed status and
// exit-code table, recovery from panics, and the rest of what docs/cli.md
// describes.
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
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"
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
	// Rand is the source of the random part of run ids; crypto/rand when nil.
	Rand io.Reader
	// NewForge builds the forge client of a command that uses one;
	// issuex.NewWithOptions when nil. Tests inject their own.
	NewForge func(issuex.Options) (issuex.Client, error)
	// Grace is how long a command that has been told to stop, by a signal or
	// by its deadline, gets to return before the shell gives up on it; 10
	// seconds when zero.
	Grace time.Duration

	// The fields below are for a caller that runs a command in its own process, as
	// agent-fox does, and not for the af binary. Each is read at the start of an
	// invocation and leaves af's behavior as it is while it is zero.

	// NoSignals installs no handler for SIGINT, SIGTERM and SIGHUP: the context given
	// to Main is the only way a command is stopped, besides --timeout and Grace, which
	// apply as always.
	NoSignals bool
	// EventSink, when set, is called with every event line as it is written to the
	// event files, in the same order and with the same bytes, less the newline. The
	// slice is the sink's only for the call. The sink is called with the stream's lock
	// held, so it must not call back into the run.
	EventSink func(line []byte)
	// RunID, when set, is the id of the run directory and the envelope's run_id, in
	// place of one drawn. An id that is not of the form YYYYMMDD_HHMMSS_<6 hex> is a
	// usage failure, before anything is created.
	RunID string
	// ReuseRunDir makes a run id that names a run directory that exists the run of this invocation:
	// the directory is opened, and its ledger and its events are carried on, where it would be a failure
	// to make it again. A run id that names none makes the directory, as it does without this.
	ReuseRunDir bool
	// LedgerSink, when it is set, is called with every record of the ledger of the run after it was
	// written, with its kind and its data, in the order they were written. It is called by the goroutine
	// that records, with no lock held.
	LedgerSink func(kind string, data json.RawMessage)
	// Mode, when it is set, is asked once the words have named a command, before the flags are
	// parsed, the input is resolved, a forge client is built, a run directory is made or anything
	// is sent. It is given the command the words matched and the words after its path, and
	// answers with the command to run and the words to parse for it, or with the failure to end the
	// invocation with: the envelope of a refusal is that of any invocation that is refused before its
	// command runs, and honours --output and --emit-events. The invocation's argv stays what
	// was received. It is for the af binary, which chooses between a loop on this machine and a
	// job on a service; a caller that runs the commands in its own process leaves it nil.
	Mode func(cmd *Command, words []string) (chosen *Command, parse []string, failure *Failure)
	// HelpExtra, when it is set, is the text that the help of a command ends with, for the
	// command it is given: a section on modes, say. It is empty for a command it has nothing to add to.
	HelpExtra func(cmd *Command) string
	// Target, when it is valid (a host included), is the repository every command that
	// uses a forge works on, whatever the input: the forge client is built for it, in
	// place of --repo and the origin of --dir, and an issue URL on another repository is
	// a usage failure.
	Target issuex.Repo

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
	// RawStdin gives the command stdin, unread, as Run.Stdin. It is for a
	// command that takes no input of its own; a command with an Input that
	// reads stdin ("-") shares it.
	RawStdin bool
	// UsesForge gives the command a forge client, Run.Forge, and --dir.
	UsesForge bool
	// UsesRunDir gives the command a run directory, Run.RunDir, and --dir.
	UsesRunDir bool
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
	// Grace is how long this command, once told to stop, gets to return before
	// the shell abandons it, when that must be longer than the App's: a
	// command that has something to put back (a tree it changed) needs it.
	// Zero is the App's.
	Grace time.Duration
	// ResultSchema is the JSON Schema of the command's result, which
	// af --schema prints under $defs. Nil if the command has none.
	ResultSchema json.RawMessage
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
	// Stdin is the process's stdin for a command with RawStdin; nil for any
	// other.
	Stdin io.Reader
	// Dir is --dir, made absolute; empty for a command without the flag.
	Dir string
	// Context holds the --context values, verbatim and in order.
	Context []string
	// DryRun is set when the command was given --dry-run. Run.Forge then
	// refuses every write; the command should not attempt other effects.
	DryRun bool
	// RunDir is the run directory; nil for a command that does not use one.
	RunDir *RunDir
	// Forge is the forge client; nil for a command that does not use one.
	// The shell closes it when the command returns.
	Forge issuex.Client
	// Target is the repository the forge client works on; invalid when
	// none could be determined.
	Target issuex.Repo
	// Stderr is where a command may write diagnostics; the shell writes
	// nothing else to it but a panic's stack.
	Stderr io.Writer

	events *eventStream
	// document is the Document of the outcome, which stdout gets in place of
	// the envelope.
	document []byte
	// app is the shell that runs the command, and closers what is closed when
	// the command has returned: the clients ForgeFor built.
	app     *App
	closers []func()

	mu          sync.Mutex
	warnings    []Warning
	artifacts   []Artifact
	sideEffects []SideEffect
	next        []Next
	usage       Usage
	inputInfo   *InputInfo // set by SetInput
	redacted    []string   // set by Redact
}

// Warn records a warning. The code matches ^[a-z][a-z0-9_]*$. A warning
// already recorded, with the same code and message, is recorded once.
func (r *Run) Warn(code, message string) {
	if !warningCodeFormat.MatchString(code) {
		// A warning never fails a run; a code that is not one is reported as
		// such, with the code it was given.
		code, message = WarnInvalidCode, fmt.Sprintf("%q: %s", code, message)
	}
	w := Warning{Code: code, Message: message}
	r.mu.Lock()
	recorded := slices.Contains(r.warnings, w)
	if !recorded {
		r.warnings = append(r.warnings, w)
	}
	r.mu.Unlock()
	if !recorded && r.events != nil {
		r.events.emit("warning", w)
	}
}

// Emit writes an event of the command's own, such as "step", "tool_call" or
// "heartbeat", to the event stream. The data is anything JSON can encode.
func (r *Run) Emit(typ string, data any) {
	if r.events != nil {
		r.events.emit(typ, data)
	}
}

// SetInput says what the input of the command was, for a command that reads its own and has
// none declared: the kind, where it came from (the URL, or the absolute path of a file, else "")
// and whether it was cut. The envelope reports it as its input.
func (r *Run) SetInput(kind InputKind, origin string, truncated bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inputInfo = &InputInfo{Kind: kind, Origin: origin, Truncated: truncated}
}

// minRedacted is the shortest value Redact takes out: a shorter one would be in ordinary words.
const minRedacted = 8

// Redact registers values that are to appear in no output of the invocation: the shell takes them
// out of the envelope on stdout and its copies (--output and the run directory) and of every
// event, wherever the command put them, and puts "***" in their place. It is for a secret the
// command holds, which a document that it carries from elsewhere may repeat. A value shorter than
// 8 bytes is not redacted.
func (r *Run) Redact(secrets ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range secrets {
		if len(s) < minRedacted {
			continue
		}
		r.redacted = append(r.redacted, s)
		// The same value as it stands inside a JSON string, which the envelope writes without
		// escaping <, > and & and an event writes with it.
		for _, escapeHTML := range []bool{false, true} {
			var buf bytes.Buffer
			enc := json.NewEncoder(&buf)
			enc.SetEscapeHTML(escapeHTML)
			if err := enc.Encode(s); err != nil {
				continue
			}
			if inner := strings.TrimSuffix(buf.String(), "\n"); len(inner) > 2 && inner[1:len(inner)-1] != s && !slices.Contains(r.redacted, inner[1:len(inner)-1]) {
				r.redacted = append(r.redacted, inner[1:len(inner)-1])
			}
		}
	}
}

// redactBytes is data without the values that were registered with Redact.
func (r *Run) redactBytes(data []byte) []byte {
	r.mu.Lock()
	secrets := slices.Clone(r.redacted)
	r.mu.Unlock()
	for _, s := range secrets {
		data = bytes.ReplaceAll(data, []byte(s), []byte("***"))
	}
	return data
}

// Artifact records something the run produced.
func (r *Run) Artifact(a Artifact) {
	if a.Path != "" && !filepath.IsAbs(a.Path) {
		if abs, err := filepath.Abs(a.Path); err == nil {
			a.Path = abs
		}
	}
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

// Warnings are the warnings the run has raised so far, in the order they were raised.
func (r *Run) Warnings() []Warning { return r.warningList() }

// warningList is a copy of the warnings recorded so far.
func (r *Run) warningList() []Warning {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.warnings)
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
	if r.inputInfo != nil {
		env.Input = *r.inputInfo
	}
}

// Register adds a command to the app.
func (a *App) Register(c Command) {
	a.commands = append(a.commands, &c)
}

// Command returns the command registered under path, which a caller may adapt before it runs an
// invocation (the schema of its result, say), or nil when there is none.
func (a *App) Command(path string) *Command {
	for _, c := range a.commands {
		if c.Path == path {
			return c
		}
	}
	return nil
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
		inv := a.newInvocation(stdout, stderr, a.name(), started)
		inv.env.fail(&Failure{Category: CategoryUsage, Message: a.unknownCommand(words)})
		return inv.finish()
	}
	if a.Mode != nil {
		chosen, parseWords, failure := a.Mode(cmd, rest)
		if failure != nil {
			// The flags the matched command knows are read so that --output and --emit-events are honoured.
			values, _, _ := parse(cmd, rest)
			inv := a.newInvocation(stdout, stderr, cmd.Path, started)
			inv.setup(cmd, words, values, nil)
			inv.env.fail(failure)
			return inv.finish()
		}
		cmd, rest = chosen, parseWords
	}

	inv := a.newInvocation(stdout, stderr, cmd.Path, started)
	values, positional, failure := parse(cmd, rest)
	inv.setup(cmd, words, values, positional)
	if failure == nil {
		failure = checkValues(cmd, values)
	}
	if failure != nil {
		inv.env.fail(failure)
		return inv.finish()
	}

	sigCtx := ctx
	if !a.NoSignals {
		var stop context.CancelFunc
		sigCtx, stop = signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
	}
	if err := inv.prepare(sigCtx, stdin); err != nil {
		inv.env.fail(failureOf(err, stderr))
		return inv.finish()
	}

	out, err := a.execute(sigCtx, cmd, inv.run, values.timeout)
	a.settle(inv.env, inv.run, out, err, stderr)
	return inv.finish()
}

// invocation is one call of Main: the envelope being made, and what has to
// be done with it before the process ends.
type invocation struct {
	app     *App
	stdout  io.Writer
	stderr  io.Writer
	started time.Time
	env     *Envelope

	cmd        *Command    // nil if the words named no command
	values     *flagValues // nil if the words named no command
	run        *Run        // nil if the words named no command
	events     *eventStream
	closeForge func()
}

func (a *App) newInvocation(stdout, stderr io.Writer, tool string, started time.Time) *invocation {
	return &invocation{app: a, stdout: stdout, stderr: stderr, started: started, env: a.newEnvelope(tool)}
}

// setup makes the Run of the command from what was parsed, and the event
// stream if there is a file for it, so that an invocation that is refused
// before its command can run is reported to them too.
func (inv *invocation) setup(cmd *Command, words []string, values *flagValues, positional []string) {
	inv.cmd, inv.values = cmd, values
	inv.run = &Run{
		Tool:    cmd.Path,
		Argv:    append([]string{inv.app.name()}, words...),
		Args:    positional,
		Input:   Input{Kind: KindNone},
		DryRun:  values.dryRun,
		Dir:     values.dir,
		Context: slices.Clone(values.context),
		Stderr:  inv.stderr,
		app:     inv.app,
	}
	if values.emitEvents != "" || cmd.UsesRunDir || inv.app.EventSink != nil {
		inv.events = newEventStream(inv.app.now, inv.run.Warn)
		inv.events.redact = inv.run.redactBytes
		inv.run.events = inv.events
		if values.emitEvents != "" {
			inv.events.addFile(values.emitEvents)
		}
		if inv.app.EventSink != nil {
			inv.events.addFunc(inv.app.EventSink)
		}
	}
}

// prepare gives the command what it declared it uses, in this order: a forge
// client (the decorator that refuses writes under --dry-run), the resolved
// input, and a run directory; then it starts the events.
func (inv *invocation) prepare(ctx context.Context, stdin io.Reader) error {
	cmd, run, values := inv.cmd, inv.run, inv.values
	if id := inv.app.RunID; id != "" && !runIDFormat.MatchString(id) {
		return &Failure{Category: CategoryUsage, Message: fmt.Sprintf("the run id %q is not of the form YYYYMMDD_HHMMSS_<6 hex>", id)}
	}
	if cmd.UsesForge {
		client, target, err := inv.app.forgeFor(cmd, values, run.Args)
		if err != nil {
			return err
		}
		run.Forge, run.Target = client, target
		if values.dryRun {
			run.Forge = DryRunForge(client, run.SideEffect)
		}
		// A client that fails to close its idle connections has nothing
		// more to tell.
		inv.closeForge = func() { _ = client.Close() }
	}

	if cmd.RawStdin {
		run.Stdin = stdin
	}
	if cmd.Input != InputNone {
		in, err := resolveInput(ctx, cmd.Input, run.Args, stdin, run.Forge)
		if err != nil {
			return err
		}
		run.Input = in
		inv.env.Input = InputInfo{Kind: in.Kind, Origin: in.Origin, Truncated: in.Truncated}
		for _, w := range in.warnings() {
			run.Warn(w.Code, w.Message)
		}
	}

	if cmd.UsesRunDir {
		rd, err := newRunDirFor(inv.app, values.dir)
		if err != nil {
			return err
		}
		run.RunDir = rd
		inv.env.RunID = &rd.ID
		run.Artifact(Artifact{Kind: "run_dir", Path: rd.Path})
		inv.events.setRunID(rd.ID)
		inv.events.addFile(rd.file("events.jsonl"))
	}
	inv.beginEvents()
	return nil
}

// beginEvents starts the event stream, if there is one to write to, with
// run_start and the warnings raised so far.
func (inv *invocation) beginEvents() {
	if inv.events == nil || !inv.events.active() {
		return
	}
	inv.events.begin(inv.run.Tool, inv.run.Argv, inv.run.Input.Kind, inv.run.warningList())
}

// finish ends the invocation: it closes the forge client, makes the envelope
// what it is to be, and publishes it. It returns the exit code.
func (inv *invocation) finish() int {
	if inv.closeForge != nil {
		inv.closeForge()
	}
	if inv.run != nil {
		inv.run.closeClients()
	}
	env := inv.env
	if inv.run != nil && inv.run.RunDir != nil {
		if path := inv.run.RunDir.reportPath(); path != "" {
			inv.run.Artifact(Artifact{Kind: "report", Path: path})
		}
	}
	inv.collect()
	env.ensureEncodable()

	env.Seal(inv.started, inv.app.now())
	return inv.publish()
}

// collect copies what the run recorded into the envelope.
func (inv *invocation) collect() {
	if inv.run != nil {
		inv.run.collect(inv.env)
	}
}

// encode renders the envelope as it is now.
func (inv *invocation) encode() []byte {
	data, _ := inv.env.marshal() // ensureEncodable made sure it can be
	if inv.run != nil {
		data = inv.run.redactBytes(data)
	}
	return data
}

// publish writes the envelope to <run_dir>/envelope.json, to the file named
// by --output, and to stdout, and ends the events, in this order: the
// copies, so that one that cannot be written is a warning and a warning event
// like any other; run_end; stdout. A copy that was written before the
// envelope last changed (the events can raise a warning of their own) is
// written again, so that every copy is the envelope stdout gets.
func (inv *invocation) publish() int {
	env := inv.env
	inv.beginEvents()

	type copyTo struct{ path, code string }
	var copies []copyTo
	if inv.run != nil && inv.run.RunDir != nil {
		copies = append(copies, copyTo{inv.run.RunDir.file("envelope.json"), WarnEnvelopeUnwritable})
	}
	if inv.values != nil && inv.values.output != "" {
		copies = append(copies, copyTo{inv.values.output, WarnOutputUnwritable})
	}
	data := inv.encode()
	var written []string
	for _, c := range copies {
		if err := os.WriteFile(c.path, data, 0o644); err != nil {
			inv.run.Warn(c.code, fmt.Sprintf("cannot write the envelope to %s: %v", c.path, err))
			continue
		}
		written = append(written, c.path)
	}
	inv.collect()

	if inv.events != nil {
		inv.events.end(env)
		inv.collect() // the events may have raised a warning
	}
	if final := inv.encode(); !bytes.Equal(final, data) {
		data = final
		for _, path := range written {
			// A copy that cannot be written again stays as it was written.
			_ = os.WriteFile(path, data, 0o644)
		}
	}

	// A command's own document replaces the envelope on stdout, but only
	// while the run is done: whatever failed it since gets the envelope.
	if inv.run != nil && len(inv.run.document) > 0 && env.Status == StatusDone {
		data = inv.run.document
	}
	// A broken stdout changes nothing: the exit code is the status's.
	_, _ = inv.stdout.Write(data)
	return env.ExitCode
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

// answer prints what a request for help, the version or the schema asks for,
// wherever on the command line it stands, and reports that it did. None of
// them reads input, builds a forge client or creates anything.
func (a *App) answer(words []string, started time.Time, stdout io.Writer) (code int, answered bool) {
	wantHelp, wantVersion, wantSchema := a.requests(words)

	switch {
	case len(words) > 0 && words[0] == "help":
		// "help" asks for a command by name; one that does not exist is the
		// one reply to a help request that is not plain text.
		cmd, _ := a.match(words[1:])
		if cmd == nil && len(leadingWords(words[1:])) > 0 {
			inv := a.newInvocation(stdout, io.Discard, a.name(), started)
			inv.env.fail(&Failure{Category: CategoryUsage, Message: a.unknownCommand(words[1:])})
			return inv.finish(), true
		}
		return a.printHelp(cmd, stdout), true
	case wantHelp:
		cmd, _ := a.match(words)
		return a.printHelp(cmd, stdout), true
	case wantVersion:
		data, _ := encodeIndented(a.version())
		_, _ = stdout.Write(data)
		return 0, true
	case wantSchema:
		cmd, _ := a.match(words)
		if cmd == nil && len(leadingWords(words)) > 0 {
			return 0, false // not a command: the usage error says so
		}
		data, err := a.schema(cmd)
		if err != nil {
			inv := a.newInvocation(stdout, io.Discard, a.name(), started)
			inv.env.fail(&Failure{Category: CategoryInternal, Message: err.Error()})
			return inv.finish(), true
		}
		_, _ = stdout.Write(data)
		return 0, true
	}
	return 0, false
}

// requests reports which of help, the version and the schema the words ask
// for. They are asked for wherever they stand before a "--", except where
// they are the value of a flag that takes one: in "--context --help" the
// answer is "--help".
func (a *App) requests(words []string) (help, version, schema bool) {
	var fs *flag.FlagSet
	scan := words
	if cmd, rest := a.match(words); cmd != nil {
		fs, scan = flagSet(cmd, &flagValues{}), rest
	}
	for i := 0; i < len(scan); i++ {
		switch w := scan[i]; w {
		case "--":
			return
		case "-h", "-help", "--help":
			help = true
		case "-version", "--version":
			version = true
		case "-schema", "--schema":
			schema = true
		default:
			if fs != nil && takesValue(fs, w) {
				i++
			}
		}
	}
	return
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
		if p := panicOf(err); p != nil && r.RunDir != nil {
			r.RunDir.writePanic(p)
		}
		return
	}
	if problem := checkOutcome(out); problem != "" {
		env.fail(&Failure{Category: CategoryInternal, Message: "the command returned an inconsistent outcome: " + problem})
		return
	}
	env.Status = out.Status
	env.Summary = out.Summary
	env.Result = out.Result
	r.document = out.Document
	if out.Status == StatusNeedsHuman {
		env.NeedsHuman = newNeedsHuman(out.NeedsHuman, r.Argv)
	}
}

// failureOf is the failure an error stands for: a *Failure as it is, a panic
// as an internal failure whose stack goes to stderr, and any other error as
// an internal failure.
func failureOf(err error, stderr io.Writer) *Failure {
	if p := panicOf(err); p != nil {
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

// panicOf is the recovered panic err stands for, or nil.
func panicOf(err error) *panicError {
	var p *panicError
	if errors.As(err, &p) {
		return p
	}
	return nil
}

// checkOutcome returns what is wrong with an outcome, or "". An outcome that
// is inconsistent is a bug in the command.
func checkOutcome(o Outcome) string {
	if len(o.Document) > 0 && o.Status != StatusDone {
		return fmt.Sprintf("a document with status %s", o.Status)
	}
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
// order, and the invocation to re-run with an answer, which is the question's own
// ResumeArgv when it has one.
func newNeedsHuman(q *Question, argv []string) *NeedsHuman {
	options := make([]Option, len(q.Options))
	for i, label := range q.Options {
		options[i] = Option{ID: string(rune('A' + i)), Label: label}
	}
	if len(q.ResumeArgv) > 0 {
		argv = q.ResumeArgv
	}
	return &NeedsHuman{
		Question: q.Text,
		Options:  options,
		Resume:   Resume{Argv: slices.Clone(argv), Flag: contextFlag},
	}
}

// stamp formats t as RFC 3339 in UTC with milliseconds.
func stamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}
