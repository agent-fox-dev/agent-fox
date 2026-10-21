package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The programs a process brain runs.
const (
	ProgramAF     = "af"
	ProgramClaude = "claude"
)

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
	// AFPath is the af that a process runs under local, landlock and none; empty is
	// the running executable.
	AFPath string
	// LookPath finds claude on the host; nil is exec.LookPath.
	LookPath func(string) (string, error)
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
	// Program is the program to run, ProgramAF or ProgramClaude, and Args what it is
	// given; every one is quoted.
	Program string
	Args    []string
	// Inline are settings that are not secret, which go in the command: AF_MODEL.
	Inline map[string]string
	// Probes are the programs to probe, once for a session, before the first run.
	Probes []string
	// StdinBrief says the process reads brief.md on its stdin.
	StdinBrief bool
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

// Process runs the processes of the brains that run as one: it finds the programs,
// probes them once for a session, and runs them in the box under env -i with the few
// variables they may have.
type Process struct {
	cfg ProcessConfig

	mu     sync.Mutex
	probed map[*box.Session]map[string]bool
}

// NewProcess makes the runner.
func NewProcess(cfg ProcessConfig) *Process {
	return &Process{cfg: cfg, probed: map[*box.Session]map[string]bool{}}
}

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

// program is the path of a program as the box has it. Under podman both are called by
// name and found on the PATH of the image. Elsewhere af is the path it was given or
// the running executable, and claude the one the host finds: a process there runs from
// the host's file system.
func (p *Process) program(provider, name string) (string, error) {
	if provider == "podman" {
		return name, nil
	}
	switch name {
	case ProgramAF:
		if p.cfg.AFPath != "" {
			return p.cfg.AFPath, nil
		}
		exe, err := os.Executable()
		if err != nil {
			return "", &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("cannot find the running af: %v", err)}
		}
		return exe, nil
	case ProgramClaude:
		look := p.cfg.LookPath
		if look == nil {
			look = exec.LookPath
		}
		found, err := look(ProgramClaude)
		if err != nil {
			return "", &toolio.Failure{Category: toolio.CategoryBrain, Message: fmt.Sprintf("claude is not on the PATH of this machine: %v", err)}
		}
		return found, nil
	}
	return "", fmt.Errorf("brain: %q is not a program of a process brain", name)
}

// quote is s as one word of a shell, in single quotes.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// environment is the part of a command that sets the environment of the process: env
// -i, PATH, HOME, the settings of the host that may be had and are set (locale, time zone,
// proxies and certificates, with their values; the box's own temporary directory), the
// credentials of the box's spec as shell parameters, and the settings in the text.
func (p *Process) environment(provider string, credentials []string, inline map[string]string) (string, error) {
	var parts []string
	if provider == "podman" {
		parts = append(parts, `PATH="$PATH"`)
	} else {
		af, err := p.program(provider, ProgramAF)
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
// root of the tree: the environment, the program with every argument quoted, the brief
// on its standard input when it reads it, and its output and its errors in files of the
// run directory. The credentials are named and never given: the shell of the box has
// them from the spec of the box.
func (p *Process) command(provider string, b Brief, spec ProcessSpec, credentials []string) (string, error) {
	env, err := p.environment(provider, credentials, spec.Inline)
	if err != nil {
		return "", err
	}
	prog, err := p.program(provider, spec.Program)
	if err != nil {
		return "", err
	}
	words := []string{env, quote(prog)}
	for _, arg := range spec.Args {
		words = append(words, quote(arg))
	}
	outRel, errRel := files(b, spec)
	if spec.StdinBrief {
		words = append(words, "<", quote(path.Join(agentDir, "run", b.RunID, "brief.md")))
	}
	words = append(words, ">", quote(outRel), "2>", quote(errRel))
	return strings.Join(words, " "), nil
}

// Run runs the process in the box of the brief. Before the first run of a session the
// programs are probed. The process gets Brief.Timeout, or the time left on the context,
// and is killed with the processes it started when it is over. Whatever happens a
// brain_process entry is noted. What the process wrote is read through the box and
// copied to the host run directory (the output up to 1 MiB, the end of the errors up to
// 64 KiB), each as a brain_output artifact; neither file decides whether the run has a
// submission. A process that exits with a code other than 0 is a result, not an error.
func (p *Process) Run(ctx context.Context, b Brief, spec ProcessSpec) (ProcessResult, error) {
	if b.Box == nil {
		return ProcessResult{}, errors.New("brain: a process needs a box")
	}
	provider := b.Box.Provider()
	if err := p.probe(ctx, b, spec.Probes); err != nil {
		return ProcessResult{}, err
	}
	credentials := slices.Collect(mapKeysOf(b.Box.Spec().Env))
	cmd, err := p.command(provider, b, spec, credentials)
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

// probe runs, once for the session, what a process needs to find in the box: each of
// the programs says its version, and HOME is set and can be written. A probe that fails
// stops the run before any money is spent, and each is a box_probe entry. The af of an
// image that is not this one is a warning and not a failure: the host checks every
// submission again.
func (p *Process) probe(ctx context.Context, b Brief, programs []string) error {
	sess, provider := b.Box, b.Box.Provider()
	p.mu.Lock()
	done := p.probed[sess]
	if done == nil {
		done = map[string]bool{}
		p.probed[sess] = done
	}
	p.mu.Unlock()

	fail := func(what, why string) error {
		image := sess.Image()
		if image == "" {
			image = "none"
		}
		return &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("%s cannot run in the %s box (image: %s): %s", what, provider, image, why)}
	}
	for _, name := range programs {
		if done[name] {
			continue
		}
		prog, err := p.program(provider, name)
		if err != nil {
			b.note("box_probe", map[string]any{"binary": name, "ok": false, "version": ""})
			return err
		}
		env, err := p.environment(provider, nil, nil)
		if err != nil {
			return err
		}
		code, out, err := sess.Exec(ctx, env+" "+quote(prog)+" '--version'", probeTimeout)
		version, line := parseVersion(name, out)
		ok := err == nil && code == 0
		b.note("box_probe", map[string]any{"binary": name, "ok": ok, "version": version.Version})
		if !ok {
			why := fmt.Sprintf("it exited with code %d: %s", code, strings.TrimSpace(line))
			if err != nil {
				why = err.Error()
			}
			return fail(name, why)
		}
		p.mu.Lock()
		done[name] = true
		p.mu.Unlock()
		p.compare(b, name, version)
	}
	if !done["HOME"] {
		code, _, err := sess.Exec(ctx, `[ -n "$HOME" ] && [ -w "$HOME" ]`, probeTimeout)
		ok := err == nil && code == 0
		b.note("box_probe", map[string]any{"binary": "HOME", "ok": ok, "version": ""})
		if !ok {
			return fail("HOME", "it is not set, or cannot be written, in the box")
		}
		p.mu.Lock()
		done["HOME"] = true
		p.mu.Unlock()
	}
	return nil
}

// parseVersion reads what a program printed for --version: the JSON document of af, or
// the first line of anything else, which is the version of that.
func parseVersion(program, out string) (afVersion, string) {
	out = strings.TrimSpace(out)
	line, _, _ := strings.Cut(out, "\n")
	if program == ProgramAF {
		var v afVersion
		if json.Unmarshal([]byte(out), &v) == nil && v.Version != "" {
			return v, line
		}
	}
	return afVersion{Version: strings.TrimSpace(line)}, line
}

// compare warns when the af of the box is not the running one: another version or
// another commit.
func (p *Process) compare(b Brief, name string, found afVersion) {
	if name != ProgramAF || found.Commit == "" && found.Version == "" {
		return
	}
	differs := p.cfg.Version != "" && found.Version != p.cfg.Version || p.cfg.Commit != "" && found.Commit != p.cfg.Commit
	if differs {
		b.warn("box_af_version_mismatch", fmt.Sprintf("the af of the %s box is version %s (commit %s), and this af is version %s (commit %s): the host checks every submission again, so the run goes on",
			b.Box.Provider(), found.Version, found.Commit, p.cfg.Version, p.cfg.Commit))
	}
}
