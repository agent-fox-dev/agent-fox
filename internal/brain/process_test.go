package brain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// standIn is the local provider under the name of one that isolates: it takes the image,
// the network and the Landlock requirement of the request, which it cannot honour, and
// makes the sandbox without them.
type standIn struct {
	sandbox.Provider
	name string
}

func (p standIn) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{
		Provider: p.name, Isolation: sandbox.IsolationContainer, Image: true, Landlock: true,
		Network: []sandbox.NetworkMode{sandbox.NetworkNone, sandbox.NetworkUnrestricted},
	}
}

func (p standIn) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	spec.Image, spec.RequireLandlock = "", false
	spec.Network = sandbox.Network{Mode: sandbox.NetworkUnrestricted}
	return p.Provider.Create(ctx, spec)
}

// procWorld is a world with the fake af and claude, and a runner that finds them.
type procWorld struct {
	*world
	bin, af, claude string
	p               *Process
}

// newProcWorld opens a box of the provider (podman is the local provider under that
// name, with an image) with the environment, over a new repository, and makes the runner
// for the host environment.
func newProcWorld(t *testing.T, provider string, boxEnv map[string]string, getenv Getenv) *procWorld {
	t.Helper()
	bin, af, claude := fakeBin(t)
	dir := newRepo(t)
	rd := newRunDir(t, dir)
	req := box.Request{Provider: provider, HostDir: dir, RunID: rd.ID, Tool: "fix", Record: rd.Record, Warn: func(string, string) {}, Env: boxEnv}
	if provider == "podman" || provider == "landlock" {
		reg := box.NewRegistry()
		reg.Register(box.Entry{Name: provider, New: func() sandbox.Provider { return standIn{Provider: local.Unisolated(), name: provider} }})
		req.Registry, req.Network = reg, box.NetworkOpen
		if provider == "podman" {
			req.Image = "example.test/img:1"
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	s, err := box.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if getenv == nil {
		getenv = os.LookupEnv
	}
	pw := &procWorld{world: &world{t: t, dir: dir, run: rd, session: s, report: &recorder{}}, bin: bin, af: af, claude: claude}
	pw.p = NewProcess(ProcessConfig{
		AFPath: af, LookPath: func(string) (string, error) { return claude, nil }, Getenv: getenv, Version: "v1.0.0", Commit: "abc123",
	})
	return pw
}

// processBrief is the brief of an attempt as Execute leaves it for a brain.
func (w *procWorld) processBrief(attempt int, mutate ...func(*Brief)) Brief {
	b := w.brief(attempt)
	for _, m := range mutate {
		m(&b)
	}
	// Execute places the brief in the run directory of the box before a brain runs.
	if err := w.session.WriteFile(context.Background(), path.Join(".agent-fox", "run", w.run.ID, "brief.md"), []byte(b.Document())); err != nil {
		w.t.Fatal(err)
	}
	b.Box, b.RunDir = w.session, w.run.Path
	b.Record = func(kind string, data any) { _ = w.run.Record(kind, data) }
	b.Artifact = w.report.Artifact
	b.Warn = w.report.Warn
	return b
}

// script places the script of the fake process in the tree of the box.
func (w *procWorld) script(s fakeScript) {
	w.t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.session.WriteFile(context.Background(), fakeScriptPath, data); err != nil {
		w.t.Fatal(err)
	}
}

// records are the lines of the record of the fake processes.
func (w *procWorld) records() []fakeRecord {
	w.t.Helper()
	data, err := w.boxFile(fakeRecordPath)
	if err != nil {
		return nil
	}
	var out []fakeRecord
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		var r fakeRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			w.t.Fatalf("a line of the record of the fake processes: %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// runsOf are the records of the processes that ran, not the ones that said their version.
func (w *procWorld) runsOf(program string) []fakeRecord {
	var out []fakeRecord
	for _, r := range w.records() {
		if r.Program == program && !slices.Equal(r.Argv, []string{"--version"}) {
			out = append(out, r)
		}
	}
	return out
}

// probes are the box_probe entries of the ledger.
func (w *procWorld) probes() []map[string]any {
	w.t.Helper()
	kinds, data := w.ledger()
	var out []map[string]any
	for i, k := range kinds {
		if k == "box_probe" {
			var m map[string]any
			if err := json.Unmarshal(data[i], &m); err != nil {
				w.t.Fatal(err)
			}
			out = append(out, m)
		}
	}
	return out
}

func claudeSpec() ProcessSpec {
	return ProcessSpec{Brain: "claude", Program: ProgramClaude, Args: []string{"-p", "--output-format", "json"}, Probes: []string{ProgramAF, ProgramClaude}}
}

// TestTheProbeRunsOnceForASessionAndRecordsEachBinary_TS_10_39 verifies TS-10-39: two
// runs in one session run af --version and claude --version once, each probe is a
// box_probe entry with the binary, ok and the version, and a run of a brain that is
// not claude probes af and not claude.
// Verifies: 10-REQ-7.2
func TestTheProbeRunsOnceForASessionAndRecordsEachBinary_TS_10_39(t *testing.T) {
	w := newProcWorld(t, "local", nil, nil)
	w.script(fakeScript{AFVersion: "v1.2.3", ClaudeVersion: "2.0.5"})
	ctx := context.Background()

	for attempt := 1; attempt <= 2; attempt++ {
		res, err := w.p.Run(ctx, w.processBrief(attempt), claudeSpec())
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("attempt %d: %+v, %v", attempt, res, err)
		}
	}

	versions := map[string]int{}
	for _, r := range w.records() {
		if slices.Equal(r.Argv, []string{"--version"}) {
			versions[r.Program]++
		}
	}
	if versions[ProgramAF] != 1 || versions[ProgramClaude] != 1 {
		t.Errorf("--version ran %v, want once for af and once for claude in a session of two runs", versions)
	}
	if n := len(w.runsOf(ProgramClaude)); n != 2 {
		t.Errorf("claude ran %d times, want twice", n)
	}
	got := map[string]map[string]any{}
	for _, p := range w.probes() {
		got[p["binary"].(string)] = p
	}
	if len(w.probes()) != 3 || got["af"]["ok"] != true || got["af"]["version"] != "v1.2.3" ||
		got["claude"]["ok"] != true || got["claude"]["version"] != "2.0.5 (Claude Code)" || got["HOME"]["ok"] != true {
		t.Errorf("box_probe entries = %v, want af (v1.2.3), claude (2.0.5 (Claude Code)) and HOME, each ok, once", w.probes())
	}

	// A run of the driver inside a box needs af and no claude.
	other := newProcWorld(t, "local", nil, nil)
	if _, err := other.p.Run(ctx, other.processBrief(1), ProcessSpec{Brain: "agentkit", Program: ProgramAF, Args: []string{"triage", "run"}, Probes: []string{ProgramAF}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range other.probes() {
		if p["binary"] == "claude" {
			t.Errorf("a run of af probed claude: %v", other.probes())
		}
	}
	if len(other.probes()) != 2 {
		t.Errorf("box_probe entries = %v, want af and HOME", other.probes())
	}
}

// TestAFailedProbeStopsBeforeTheBrainWithASandboxFailure_TS_10_40 verifies TS-10-40:
// a claude that exits 127 on --version, a HOME that is unset and a HOME that cannot be
// written are failed/sandbox, not retryable, name the binary (or HOME), the provider
// and the image, record the probe as not ok, and the brain's command is never started.
// Verifies: 10-REQ-7.3
func TestAFailedProbeStopsBeforeTheBrainWithASandboxFailure_TS_10_40(t *testing.T) {
	type tc struct {
		name, provider, binary string
		setup                  func(t *testing.T, w *procWorld)
	}
	for _, c := range []tc{
		{"claude exits 127", "podman", "claude", func(t *testing.T, w *procWorld) {
			w.script(fakeScript{VersionExit: map[string]int{"claude": 127}})
		}},
		{"af exits 1", "local", "af", func(t *testing.T, w *procWorld) {
			w.script(fakeScript{VersionExit: map[string]int{"af": 1}})
		}},
		{"HOME is unset", "none", "HOME", func(t *testing.T, w *procWorld) { t.Setenv("HOME", "") }},
		{"HOME cannot be written", "none", "HOME", func(t *testing.T, w *procWorld) {
			if os.Geteuid() == 0 {
				t.Skip("root can write anywhere")
			}
			home := filepath.Join(t.TempDir(), "home")
			if err := os.Mkdir(home, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newProcWorld(t, c.provider, nil, nil)
			c.setup(t, w)

			_, err := w.p.Run(context.Background(), w.processBrief(1), claudeSpec())

			f := asFailure(t, err)
			if f.Category != toolio.CategorySandbox || f.Retryable {
				t.Errorf("failure = %+v, want sandbox and not retryable", f)
			}
			if !strings.Contains(f.Message, c.binary) || !strings.Contains(f.Message, c.provider) {
				t.Errorf("message = %q, want it to name %s and the %s provider", f.Message, c.binary, c.provider)
			}
			if c.provider == "podman" && !strings.Contains(f.Message, "example.test/img:1") {
				t.Errorf("message = %q, want it to name the image", f.Message)
			}
			if n := len(w.runsOf(ProgramClaude)); n != 0 {
				t.Errorf("claude was started %d times after a probe that failed", n)
			}
			var failed bool
			for _, p := range w.probes() {
				if p["binary"] == c.binary && p["ok"] == false {
					failed = true
				}
			}
			if !failed {
				t.Errorf("box_probe entries = %v, want %s recorded as not ok", w.probes(), c.binary)
			}
		})
	}
}

// TestADifferentAFVersionInTheImageIsAWarningAndTheRunGoesOn_TS_10_21 verifies TS-10-21:
// an af in the image whose version or commit is not the running binary's raises
// box_af_version_mismatch once, the probe is ok and the run goes on; the same
// version raises none.
// Verifies: 10-REQ-4.3
func TestADifferentAFVersionInTheImageIsAWarningAndTheRunGoesOn_TS_10_21(t *testing.T) {
	for _, tc := range []struct {
		name, version, commit string
		warns                 bool
	}{
		{"another commit", "v1.0.0", "zzz999", true},
		{"another version", "v2.0.0", "abc123", true},
		{"the same", "v1.0.0", "abc123", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProcWorld(t, "podman", nil, nil)
			w.script(fakeScript{AFVersion: tc.version, AFCommit: tc.commit})

			for attempt := 1; attempt <= 2; attempt++ {
				res, err := w.p.Run(context.Background(), w.processBrief(attempt), claudeSpec())
				if err != nil || res.ExitCode != 0 {
					t.Fatalf("attempt %d = %+v, %v: a mismatch does not stop the run", attempt, res, err)
				}
			}

			var warned []toolio.Warning
			for _, w := range w.report.warnings {
				if w.Code == "box_af_version_mismatch" {
					warned = append(warned, w)
				}
			}
			if tc.warns && (len(warned) != 1 || !strings.Contains(warned[0].Message, tc.commit) || !strings.Contains(warned[0].Message, "v1.0.0")) {
				t.Errorf("warnings = %v, want one box_af_version_mismatch that names both versions", w.report.warnings)
			}
			if !tc.warns && len(warned) != 0 {
				t.Errorf("warnings = %v, want none", warned)
			}
			for _, p := range w.probes() {
				if p["binary"] == "af" && p["ok"] != true {
					t.Errorf("the probe of af = %v, want ok", p)
				}
			}
		})
	}
}

// commandFor is the command of a process under a provider, for a runner with the
// paths and the host environment given.
func commandFor(t *testing.T, provider string, cfg ProcessConfig, spec ProcessSpec, credentials []string) string {
	t.Helper()
	b := Brief{RunID: "20261021_160000_abcdef", Attempt: 2}
	cmd, err := NewProcess(cfg).command(provider, b, spec, credentials)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func hostEnvOf(m map[string]string) Getenv {
	return func(name string) (string, bool) { v, ok := m[name]; return v, ok }
}

// TestProgramsAreResolvedPerProvider_TS_10_41 verifies TS-10-41: under local, landlock
// and none, af is the path it is given (or the running executable) with its directory
// first on PATH and claude is the one found on the host; under podman both are called by
// name with the PATH of the box.
// Verifies: 10-REQ-7.4
func TestProgramsAreResolvedPerProvider_TS_10_41(t *testing.T) {
	cfg := ProcessConfig{
		AFPath: "/x/bin/af", LookPath: func(string) (string, error) { return "/y/claude", nil },
		Getenv: hostEnvOf(map[string]string{"PATH": "/usr/local/bin:/usr/bin"}),
	}
	for _, provider := range []string{"local", "landlock", "none"} {
		af := commandFor(t, provider, cfg, ProcessSpec{Brain: "agentkit", Program: ProgramAF, Args: []string{"triage", "run"}}, nil)
		claude := commandFor(t, provider, cfg, claudeSpec(), nil)
		if !strings.Contains(af, `PATH='/x/bin:/usr/local/bin:/usr/bin'`) || !strings.Contains(af, ` '/x/bin/af' 'triage' 'run'`) {
			t.Errorf("%s: af command = %s, want the path it is given, its directory first on PATH", provider, af)
		}
		if !strings.Contains(claude, `PATH='/x/bin:/usr/local/bin:/usr/bin'`) || !strings.Contains(claude, ` '/y/claude' '-p'`) {
			t.Errorf("%s: claude command = %s, want the one the host finds", provider, claude)
		}
	}
	af := commandFor(t, "podman", cfg, ProcessSpec{Brain: "agentkit", Program: ProgramAF, Args: []string{"triage", "run"}}, nil)
	claude := commandFor(t, "podman", cfg, claudeSpec(), nil)
	if !strings.Contains(af, `PATH="$PATH"`) || !strings.Contains(af, ` 'af' 'triage' 'run'`) || strings.Contains(af, "/x/bin") {
		t.Errorf("podman: af command = %s, want af by name with the PATH of the box", af)
	}
	if !strings.Contains(claude, `PATH="$PATH"`) || !strings.Contains(claude, ` 'claude' '-p'`) || strings.Contains(claude, "/y/") {
		t.Errorf("podman: claude command = %s, want claude by name with the PATH of the box", claude)
	}

	// Without a path to af, the running executable is it.
	self, _ := os.Executable()
	cfg.AFPath = ""
	if cmd := commandFor(t, "local", cfg, ProcessSpec{Brain: "agentkit", Program: ProgramAF, Args: []string{"x"}}, nil); !strings.Contains(cmd, "'"+self+"' 'x'") {
		t.Errorf("without AFPath: %s, want the running executable %s", cmd, self)
	}
}

// TestTheCommandRunsUnderEnvIWithAnAllowlist_TS_10_42 verifies TS-10-42: the command
// starts with env -i, holds PATH, HOME, the allowlisted variables that are set as
// shell parameters, the credentials as shell parameters and the settings inline, quotes
// every argument, and has no variable that is not in the list and no secret value.
// Verifies: 10-REQ-7.5
func TestTheCommandRunsUnderEnvIWithAnAllowlist_TS_10_42(t *testing.T) {
	cfg := ProcessConfig{
		AFPath: "/x/bin/af", LookPath: func(string) (string, error) { return "/y/claude", nil },
		Getenv: hostEnvOf(map[string]string{
			"PATH": "/usr/bin", "LANG": "C.UTF-8", "tz": "UTC", "HTTPS_PROXY": "http://user:proxy-secret@proxy:3128", "SSL_CERT_FILE": "/etc/ca.pem",
			"GITHUB_TOKEN": "ghp-secret", "FOO": "bar", "ANTHROPIC_API_KEY": "sk-secret",
		}),
	}
	spec := claudeSpec()
	spec.Args = []string{"-p", "it's", "two words", "$(touch pwned)"}
	spec.Inline = map[string]string{"AF_MODEL": "m1"}
	spec.StdinBrief = true

	cmd := commandFor(t, "local", cfg, spec, []string{"ANTHROPIC_API_KEY"})

	if !strings.HasPrefix(cmd, "env -i ") {
		t.Errorf("command = %s, want it to start with env -i", cmd)
	}
	for _, want := range []string{
		`HOME="$HOME"`, `LANG="$LANG"`, `tz="$tz"`, `HTTPS_PROXY="$HTTPS_PROXY"`, `SSL_CERT_FILE="$SSL_CERT_FILE"`,
		`ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"`, `AF_MODEL='m1'`,
		` '/y/claude' '-p' 'it'\''s' 'two words' '$(touch pwned)'`,
		` < '.agent-fox/run/20261021_160000_abcdef/brief.md'`,
		` > '.agent-fox/run/20261021_160000_abcdef/claude.2.json'`,
		` 2> '.agent-fox/run/20261021_160000_abcdef/claude.2.stderr'`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command = %s, want it to contain %s", cmd, want)
		}
	}
	for _, banned := range []string{"GITHUB_TOKEN", "ghp-secret", "FOO", "bar", "sk-secret", "proxy-secret", "LC_ALL", "NO_PROXY", "TMPDIR", "SSL_CERT_DIR"} {
		if strings.Contains(cmd, banned) {
			t.Errorf("command = %s, want no %s", cmd, banned)
		}
	}
}

// TestAProcessSeesTheVendorKeyAndNoOtherCredentialOfTheHost_TS_10_42 verifies TS-10-42
// and 10-REQ-7.6 end to end: the process sees PATH, HOME, the key of the box's spec and
// the setting, and nothing else of the host's environment, and no secret is in the
// ledger.
// Verifies: 10-REQ-7.5, 10-REQ-7.6
func TestAProcessSeesTheVendorKeyAndNoOtherCredentialOfTheHost_TS_10_42(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp-host")
	t.Setenv("GH_TOKEN", "gh-host")
	t.Setenv("GITLAB_TOKEN", "gl-host")
	t.Setenv("FOO", "bar")
	t.Setenv("ANTHROPIC_API_KEY", "sk-host")
	for _, provider := range []string{"local", "none"} {
		t.Run(provider, func(t *testing.T) {
			w := newProcWorld(t, provider, map[string]string{"ANTHROPIC_API_KEY": "sk-secret"}, nil)
			spec := claudeSpec()
			spec.Inline = map[string]string{"AF_MODEL": "m1"}

			if _, err := w.p.Run(context.Background(), w.processBrief(1), spec); err != nil {
				t.Fatal(err)
			}

			runs := w.runsOf(ProgramClaude)
			if len(runs) != 1 {
				t.Fatalf("claude ran %d times", len(runs))
			}
			seen := map[string]string{}
			for _, kv := range runs[0].Env {
				name, value, _ := strings.Cut(kv, "=")
				seen[name] = value
			}
			if seen["ANTHROPIC_API_KEY"] != "sk-secret" || seen["AF_MODEL"] != "m1" || seen["HOME"] == "" || !strings.HasPrefix(seen["PATH"], filepath.Dir(w.af)) {
				t.Errorf("the process saw %v, want PATH with the directory of af first, HOME, the key of the box and AF_MODEL", seen)
			}
			for name := range seen {
				if !slices.Contains([]string{"PATH", "HOME", "ANTHROPIC_API_KEY", "AF_MODEL", "LANG", "TMPDIR", "TZ", "LC_ALL"}, name) {
					t.Errorf("the process saw %s, which is not on the list", name)
				}
			}
			ledger, _ := os.ReadFile(filepath.Join(w.run.Path, "ledger.jsonl"))
			for _, secret := range []string{"sk-secret", "sk-host", "ghp-host", "gh-host", "gl-host"} {
				if strings.Contains(string(ledger), secret) {
					t.Errorf("the ledger holds %s", secret)
				}
			}
		})
	}
}

// pidIsGone says whether the process no longer exists.
func pidIsGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

// TestAProcessThatOutlivesItsTimeoutIsKilledWithItsGroup_TS_10_45 verifies TS-10-45:
// a process that starts a child and sleeps is killed with the child when the timeout of
// the brief is over, and also when only the context has a deadline, and brain_process
// is recorded for that run and for one that ends by itself.
// Verifies: 10-REQ-7.8
func TestAProcessThatOutlivesItsTimeoutIsKilledWithItsGroup_TS_10_45(t *testing.T) {
	for _, provider := range []string{"local", "none"} {
		for _, byContext := range []bool{false, true} {
			name := provider + "/brief timeout"
			if byContext {
				name = provider + "/context deadline"
			}
			t.Run(name, func(t *testing.T) {
				w := newProcWorld(t, provider, nil, nil)
				w.script(fakeScript{SleepMS: 30000, SpawnChild: true})
				b := w.processBrief(1)
				ctx := context.Background()
				if byContext {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
					defer cancel()
				} else {
					b.Timeout = time.Second
				}
				start := time.Now()

				res, err := w.p.Run(ctx, b, claudeSpec())

				if d := time.Since(start); d > 20*time.Second {
					t.Errorf("Run took %s for a timeout of a second", d)
				}
				if err != nil || !res.TimedOut || res.ExitCode != box.ExitTimeout {
					t.Errorf("Run = %+v, %v, want a timeout, the result of the process and not a lost call", res, err)
				}
				data, rerr := w.boxFile(fakeChildPath)
				if rerr != nil {
					t.Fatalf("the fake process never started its child: %v", rerr)
				}
				pid, perr := strconv.Atoi(strings.TrimSpace(data))
				if perr != nil {
					t.Fatal(perr)
				}
				deadline := time.Now().Add(10 * time.Second)
				for !pidIsGone(pid) && time.Now().Before(deadline) {
					time.Sleep(50 * time.Millisecond)
				}
				if !pidIsGone(pid) {
					_ = syscall.Kill(pid, syscall.SIGKILL)
					t.Errorf("the child %d of the process is alive after the timeout", pid)
				}
				entry := w.entry("brain_process")
				if entry["brain"] != "claude" || entry["duration_ms"] == nil || entry["exit_code"] == nil {
					t.Errorf("brain_process = %v, want brain, exit_code and duration_ms", entry)
				}
			})
		}
	}

	w := newProcWorld(t, "local", nil, nil)
	w.script(fakeScript{Exit: 3})
	res, err := w.p.Run(context.Background(), w.processBrief(1), claudeSpec())
	if err != nil || res.ExitCode != 3 || res.TimedOut {
		t.Fatalf("a normal run = %+v, %v", res, err)
	}
	if entry := w.entry("brain_process"); entry["exit_code"] != float64(3) || entry["brain"] != "claude" {
		t.Errorf("brain_process = %v, want exit_code 3", entry)
	}
}

// newRunDir makes the run directory of a run in the checkout.
func newRunDir(t *testing.T, dir string) *toolio.RunDir {
	t.Helper()
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}
