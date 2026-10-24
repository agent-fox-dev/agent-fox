package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// ProgramAF is the program a process brain runs: af <agent> run, inside the box.
const ProgramAF = "af"

const (
	// probeTimeout bounds each probe of a binary.
	probeTimeout = 30 * time.Second
	// maxOutputCopy and maxStderrCopy are the most of the output file, and the most of
	// the end of the error file, that are copied to the host.
	maxOutputCopy = 1 << 20
	maxStderrCopy = 64 << 10
	// collectTimeout bounds the read of the output files of a process that has ended,
	// on a context the end of the run did not reach.
	collectTimeout = 30 * time.Second
)

// allowed are the variables of the host a process is given, in either case, when they
// are set; allowedExact are given as they are spelt. The command refers to each as a
// shell parameter, so that the value is the one the shell of the box has, and never
// in the text of the command.
var (
	allowed      = []string{"LANG", "LC_ALL", "TZ", "TMPDIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"}
	allowedExact = []string{"SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS"}
)

// variableName is what the name of a variable of a command is held to.
var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ProcessConfig is what the process runner needs of the host.
type ProcessConfig struct {
	// AFPath is the af that a process runs in a box whose programs are not called by
	// name (local); empty is the running executable.
	AFPath string
	// Getenv reads the host's environment: its PATH and the variables a process may
	// have; nil is os.LookupEnv.
	Getenv Getenv
	// Version and Commit say which af this is, so that the af of an image that is
	// another can be told.
	Version, Commit string
}

// ProcessConfig is the config of the processes of the brains these options make.
func (o Options) ProcessConfig() ProcessConfig {
	return ProcessConfig{AFPath: o.AFPath, Getenv: o.lookup(), Version: o.AFVersion, Commit: o.AFCommit}
}

// ProcessSpec is one process of a brain.
type ProcessSpec struct {
	// Brain names the brain, which the files of the process carry in their names:
	// <brain>.<attempt>.json for its output and <brain>.<attempt>.stderr.
	Brain string
	// Args are what af is given; every one is quoted.
	Args []string
	// Inline are settings that are not secret, which go in the command: AF_MODEL.
	Inline map[string]string
	// Credentials name the variables the process needs from the box's own environment: they
	// are referred to as shell parameters, never given as values. They count only for a box
	// that supplies its own credentials; for the others the credentials are the names of the
	// environment the box was created with.
	Credentials []string
}

// ProcessResult is what a process left.
type ProcessResult struct {
	ExitCode int
	// TimedOut says the process was killed because its time ran out.
	TimedOut bool
	// Output is the file the process wrote its standard output to, whole, and nil if it
	// left none; Stderr is the end of the file of its standard error.
	Output   []byte
	Stderr   []byte
	Duration time.Duration
}

// Process runs af as the process of a brain: it finds af, probes it once for a session, and
// runs it in the box under env -i with the few variables it may have.
type Process struct {
	cfg ProcessConfig
}

// NewProcess makes the runner.
func NewProcess(cfg ProcessConfig) *Process { return &Process{cfg: cfg} }

func (p *Process) getenv() Getenv {
	if p.cfg.Getenv != nil {
		return p.cfg.Getenv
	}
	return os.LookupEnv
}

// note records an entry in the ledger of the run, if the brief has one.
func (b Brief) note(kind string, data any) {
	if b.Record != nil {
		b.Record(kind, data)
	}
}

// warn raises a warning of the run, if the brief has a way to.
func (b Brief) warn(code, message string) {
	if b.Warn != nil {
		b.Warn(code, message)
	}
}

// DefaultAFPath is the af that a process runs in a box whose programs are not called by name
// (local) when the config names none; empty is the running executable. A test binary is no
// af, and would run its tests again: its TestMain points this at a link to itself named af,
// and acts as af when it is run under that name.
var DefaultAFPath string

// program is the path of af as the box has it. In a box whose programs are called by name
// (podman, a gateway) it is found on the PATH of the image. Elsewhere it is the path it was
// given, DefaultAFPath or the running executable: a process there runs from the host's file
// system.
func (p *Process) program(byName bool) (string, error) {
	if byName {
		return ProgramAF, nil
	}
	if p.cfg.AFPath != "" {
		return p.cfg.AFPath, nil
	}
	if DefaultAFPath != "" {
		return DefaultAFPath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("cannot find the running af: %v", err)}
	}
	return exe, nil
}

// quote is s as one word of a shell, in single quotes.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// environment is the part of a command that sets the environment of the process: env
// -i, PATH, HOME, the settings of the host that may be had and are set (locale, time zone,
// proxies and certificates, with their values; the box's own temporary directory), the
// credentials of the box's spec as shell parameters, and the settings in the text.
func (p *Process) environment(byName bool, credentials []string, inline map[string]string) (string, error) {
	var parts []string
	if byName {
		parts = append(parts, `PATH="$PATH"`)
	} else {
		af, err := p.program(byName)
		if err != nil {
			return "", err
		}
		host, _ := p.getenv()("PATH")
		if host == "" {
			host = "/usr/local/bin:/usr/bin:/bin"
		}
		parts = append(parts, "PATH="+quote(filepath.Dir(af)+string(os.PathListSeparator)+host))
	}
	parts = append(parts, `HOME="$HOME"`)
	get := p.getenv()
	// The settings of the host that are set go with their values: the shell of a box has not
	// the environment of the host, so a parameter would be empty there. The temporary
	// directory is the exception, since a path of the host means nothing in a box: the box's
	// own is referred to.
	setting := func(name string) {
		value, ok := get(name)
		switch {
		case !ok:
		case strings.EqualFold(name, "TMPDIR"):
			parts = append(parts, name+`="$`+name+`"`)
		default:
			parts = append(parts, name+"="+quote(value))
		}
	}
	for _, base := range allowed {
		setting(base)
		setting(strings.ToLower(base))
	}
	for _, name := range allowedExact {
		setting(name)
	}
	for _, name := range slices.Sorted(slices.Values(credentials)) {
		if !variableName.MatchString(name) {
			return "", fmt.Errorf("brain: %q is not the name of a variable", name)
		}
		parts = append(parts, name+`="$`+name+`"`)
	}
	for _, name := range slices.Sorted(mapKeysOf(inline)) {
		if !variableName.MatchString(name) {
			return "", fmt.Errorf("brain: %q is not the name of a variable", name)
		}
		parts = append(parts, name+"="+quote(inline[name]))
	}
	return "env -i " + strings.Join(parts, " "), nil
}

func mapKeysOf(m map[string]string) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// files are where a process of an attempt writes its output and its errors, in the
// tree and as a file name.
func files(b Brief, spec ProcessSpec) (outRel, errRel string) {
	base := path.Join(agentDir, "run", b.RunID, fmt.Sprintf("%s.%d", spec.Brain, b.Attempt))
	return base + ".json", base + ".stderr"
}

// command is the command line of a process, as the shell of the box runs it at the
// root of the tree: the environment, af with every argument quoted, and its output and
// its errors in files of the run directory. The credentials are named and never given:
// the shell of the box has them from the spec of the box.
func (p *Process) command(byName bool, b Brief, spec ProcessSpec, credentials []string) (string, error) {
	env, err := p.environment(byName, credentials, spec.Inline)
	if err != nil {
		return "", err
	}
	prog, err := p.program(byName)
	if err != nil {
		return "", err
	}
	words := []string{env, quote(prog)}
	for _, arg := range spec.Args {
		words = append(words, quote(arg))
	}
	outRel, errRel := files(b, spec)
	words = append(words, ">", quote(outRel), "2>", quote(errRel))
	return strings.Join(words, " "), nil
}

// Run runs the process in the box of the brief. Before the first run of a session af is
// probed. The process gets Brief.Timeout, or the time left on the context,
// and is killed with the processes it started when it is over. Whatever happens a
// brain_process entry is noted. What the process wrote is read through the box and
// copied to the host run directory (the output up to 1 MiB, the end of the errors up to
// 64 KiB), each as a brain_output artifact; neither file decides whether the run has a
// submission. A process that exits with a code other than 0 is a result, not an error.
func (p *Process) Run(ctx context.Context, b Brief, spec ProcessSpec) (ProcessResult, error) {
	if b.Box == nil {
		return ProcessResult{}, errors.New("brain: a process needs a box")
	}
	if err := p.probe(ctx, b); err != nil {
		return ProcessResult{}, err
	}
	credentials := slices.Collect(mapKeysOf(b.Box.Spec().Env))
	if b.Box.SuppliesCredentials() {
		for _, name := range spec.Credentials {
			if !slices.Contains(credentials, name) {
				credentials = append(credentials, name)
			}
		}
	}
	cmd, err := p.command(b.Box.ByName(), b, spec, credentials)
	if err != nil {
		return ProcessResult{}, err
	}

	timeout := b.Timeout
	if deadline, ok := ctx.Deadline(); ok {
		// Never more than a little under what is left, so that the process is ended and says
		// so before the context cancels the call, and the result is a timeout, with its output
		// to be copied, and not a lost call: brain.Execute gives the context the deadline of
		// the brief's timeout, which is already running when the process starts.
		left := time.Until(deadline)
		if limit := max(left-min(left/10, 5*time.Second), time.Second); timeout <= 0 || timeout > limit {
			timeout = limit
		}
	}
	start := time.Now()
	code, _, err := b.Box.Exec(ctx, cmd, timeout)
	took := time.Since(start)
	if err != nil {
		code = -1
	}
	b.note("brain_process", map[string]any{"brain": spec.Brain, "exit_code": code, "duration_ms": took.Milliseconds()})
	if err != nil {
		return ProcessResult{}, err
	}

	res := ProcessResult{ExitCode: code, TimedOut: code == box.ExitTimeout, Duration: took}
	outRel, errRel := files(b, spec)
	res.Output, res.Stderr = p.collect(ctx, b, outRel, errRel)
	return res, nil
}

// collect reads what the process wrote out of the box, copies it to the host, and
// returns the output whole and the end of the errors.
func (p *Process) collect(ctx context.Context, b Brief, outRel, errRel string) (output, stderr []byte) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), collectTimeout)
	defer cancel()
	fsys := b.Box.FS(rctx)
	read := func(rel string) []byte {
		data, err := fs.ReadFile(fsys, rel)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				b.warn("brain_output_unreadable", fmt.Sprintf("%s could not be read out of the box: %v", rel, err))
			}
			return nil
		}
		return data
	}
	output, stderr = read(outRel), read(errRel)
	if len(stderr) > maxStderrCopy {
		stderr = stderr[len(stderr)-maxStderrCopy:]
	}
	copyOut := func(rel string, data []byte, found bool) {
		if !found || b.RunDir == "" {
			return
		}
		dst := filepath.Join(b.RunDir, path.Base(rel))
		if err := agentdir.WriteFileAtomic(dst, data); err != nil {
			b.warn("brain_output_unreadable", fmt.Sprintf("%s could not be copied to the run directory: %v", rel, err))
			return
		}
		if b.Artifact != nil {
			b.Artifact(toolio.Artifact{Kind: "brain_output", Path: dst})
		}
	}
	copyOut(outRel, output[:min(len(output), maxOutputCopy)], output != nil)
	copyOut(errRel, stderr, stderr != nil)
	return output, stderr
}

// afVersion is what af --version prints.
type afVersion struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// probe runs, once for the session, what a process needs to find in the box: af says its
// version, and HOME is set and can be written. A probe that fails
// stops the run before any money is spent, and each is a box_probe entry. The af of an
// image that is not this one is a warning and not a failure: the host checks every
// submission again.
func (p *Process) probe(ctx context.Context, b Brief) error {
	sess, provider, byName := b.Box, b.Box.Provider(), b.Box.ByName()

	// fail is the failure of a probe: it is the box's own when the box could not be asked, so
	// that an outage is as retryable as the box says it is.
	fail := func(what, why string, cause error) error {
		image := sess.Image()
		if image == "" {
			image = "none"
		}
		failure := &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("%s cannot run in the %s box (image: %s): %s", what, provider, image, why)}
		var asked *toolio.Failure
		if errors.As(cause, &asked) {
			failure.Retryable = asked.Retryable
		}
		return failure
	}
	if !sess.Probed(ProgramAF) {
		prog, err := p.program(byName)
		if err != nil {
			b.note("box_probe", map[string]any{"binary": ProgramAF, "ok": false, "version": ""})
			return err
		}
		env, err := p.environment(byName, nil, nil)
		if err != nil {
			return err
		}
		code, out, err := sess.Exec(ctx, env+" "+quote(prog)+" '--version'", probeTimeout)
		version, line := parseVersion(out)
		ok := err == nil && code == 0
		b.note("box_probe", map[string]any{"binary": ProgramAF, "ok": ok, "version": version.Version})
		if !ok {
			why := fmt.Sprintf("it exited with code %d: %s", code, strings.TrimSpace(line))
			if err != nil {
				why = err.Error()
			}
			return fail(ProgramAF, why, err)
		}
		sess.MarkProbed(ProgramAF)
		p.compare(b, version)
	}
	if !sess.Probed("HOME") {
		code, _, err := sess.Exec(ctx, `[ -n "$HOME" ] && [ -w "$HOME" ]`, probeTimeout)
		ok := err == nil && code == 0
		b.note("box_probe", map[string]any{"binary": "HOME", "ok": ok, "version": ""})
		if !ok {
			return fail("HOME", "it is not set, or cannot be written, in the box", err)
		}
		sess.MarkProbed("HOME")
	}
	return nil
}

// parseVersion reads what af printed for --version: its JSON document, or else the first
// line, which is taken for the version.
func parseVersion(out string) (afVersion, string) {
	out = strings.TrimSpace(out)
	line, _, _ := strings.Cut(out, "\n")
	var v afVersion
	if json.Unmarshal([]byte(out), &v) == nil && v.Version != "" {
		return v, line
	}
	return afVersion{Version: strings.TrimSpace(line)}, line
}

// compare warns when the af of the box is not the running one: another version or
// another commit.
func (p *Process) compare(b Brief, found afVersion) {
	if found.Commit == "" && found.Version == "" {
		return
	}
	differs := p.cfg.Version != "" && found.Version != p.cfg.Version || p.cfg.Commit != "" && found.Commit != p.cfg.Commit
	if differs {
		b.warn("box_af_version_mismatch", fmt.Sprintf("the af of the %s box is version %s (commit %s), and this af is version %s (commit %s): the host checks every submission again, so the run goes on",
			b.Box.Provider(), found.Version, found.Commit, p.cfg.Version, p.cfg.Commit))
	}
}
