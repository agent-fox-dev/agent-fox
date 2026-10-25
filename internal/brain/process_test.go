package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// command is the command line of a process of attempt 2 of a run.
func command(t *testing.T, cfg ProcessConfig, byName bool, spec ProcessSpec, credentials ...string) string {
	t.Helper()
	cmd, err := NewProcess(cfg).command(byName, Brief{RunID: "20261021_160000_abcdef", Attempt: 2}, spec, credentials)
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

// allowedNames are the settings of the host a process may have.
func allowedNames() []string {
	var out []string
	for _, base := range allowed {
		out = append(out, base, strings.ToLower(base))
	}
	return append(out, allowedExact...)
}

func TestProcess_CommandRunsAfUnderEnvIWithTheAllowedSettingsAndNamesTheCredentials(t *testing.T) {
	cfg := ProcessConfig{AFPath: "/x/bin/af", Getenv: envOf(map[string]string{
		"PATH": "/usr/bin", "LANG": "C.UTF-8", "tz": "UTC", "HTTPS_PROXY": "http://proxy:3128", "SSL_CERT_FILE": "/etc/ca.pem", "TMPDIR": "/host/tmp",
		"http_proxy": "a'b $(touch pwned) `id`", "GITHUB_TOKEN": "ghp-secret", "FOO": "bar", "ANTHROPIC_API_KEY": "sk-secret",
	})}
	spec := ProcessSpec{Brain: AgentkitName, Args: []string{"triage", "it's", "two words", "$(touch pwned)"}, Inline: map[string]string{"AF_MODEL": "m1"}}

	cmd := command(t, cfg, false, spec, "ANTHROPIC_API_KEY")

	if !strings.HasPrefix(cmd, "env -i PATH='/x/bin:/usr/bin' ") {
		t.Errorf("command = %s, want env -i with the directory of af first on PATH", cmd)
	}
	for _, want := range []string{
		`HOME="$HOME"`, `TMPDIR="$TMPDIR"`, `LANG='C.UTF-8'`, `tz='UTC'`, `HTTPS_PROXY='http://proxy:3128'`, `SSL_CERT_FILE='/etc/ca.pem'`,
		`http_proxy='a'\''b $(touch pwned) ` + "`id`" + `'`, `ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"`, `AF_MODEL='m1'`,
		` '/x/bin/af' 'triage' 'it'\''s' 'two words' '$(touch pwned)' > '.agent-fox/run/20261021_160000_abcdef/agentkit.2.json' 2> '.agent-fox/run/20261021_160000_abcdef/agentkit.2.stderr'`,
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command = %s, want it to hold %s", cmd, want)
		}
	}
	for _, banned := range []string{"GITHUB_TOKEN", "ghp-secret", "FOO", "bar", "sk-secret", "/host/tmp", "LC_ALL"} {
		if strings.Contains(cmd, banned) {
			t.Errorf("command = %s, want no %s", cmd, banned)
		}
	}

	byName := command(t, cfg, true, spec)
	if !strings.HasPrefix(byName, `env -i PATH="$PATH" HOME="$HOME"`) || !strings.Contains(byName, ` 'af' 'triage'`) || strings.Contains(byName, "/x/bin") {
		t.Errorf("a box whose programs are called by name: %s, want af by name on the box's PATH", byName)
	}
	if noPath := command(t, ProcessConfig{AFPath: "/x/bin/af", Getenv: noEnv}, false, spec); !strings.Contains(noPath, "PATH='/x/bin:/usr/local/bin:/usr/bin:/bin'") {
		t.Errorf("a host with no PATH: %s, want a PATH of the usual directories", noPath)
	}
	for _, bad := range []ProcessSpec{{Inline: map[string]string{"1X": "v"}}, {Inline: map[string]string{"A B": "v"}}} {
		if _, err := NewProcess(cfg).command(false, Brief{}, bad, nil); err == nil {
			t.Errorf("the setting %v was put in a command", bad.Inline)
		}
	}
	if _, err := NewProcess(cfg).command(false, Brief{}, ProcessSpec{}, []string{"KEY; rm -rf /"}); err == nil {
		t.Error("a credential that is not the name of a variable was put in a command")
	}
}

func TestProcess_FindsAfWhereTheBoxRunsIt(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := ProcessSpec{Brain: AgentkitName, Args: []string{"x"}}
	if cmd := command(t, ProcessConfig{Getenv: noEnv}, false, spec); !strings.Contains(cmd, quote(self)+" 'x'") {
		t.Errorf("with no path for af: %s, want the running executable", cmd)
	}
	old := DefaultAFPath
	DefaultAFPath = "/default/af"
	t.Cleanup(func() { DefaultAFPath = old })
	if cmd := command(t, ProcessConfig{Getenv: noEnv}, false, spec); !strings.Contains(cmd, "'/default/af' 'x'") {
		t.Errorf("with DefaultAFPath: %s, want it", cmd)
	}
	if cmd := command(t, ProcessConfig{AFPath: "/given/af", Getenv: noEnv}, false, spec); !strings.Contains(cmd, "'/given/af' 'x'") {
		t.Errorf("with a path for af: %s, want it over DefaultAFPath", cmd)
	}
}

func TestProcess_ProbesAfAndHomeOnceForABox(t *testing.T) {
	w := newProcWorld(t, box.Request{Provider: "local"}, nil)
	w.script(fakeaf.Script{})

	for attempt := 1; attempt <= 2; attempt++ {
		runner := NewProcess(ProcessConfig{AFPath: w.af, Version: "v1.0.0", Commit: "abc123"}) // a runner of its own each time
		if res, err := runner.Run(context.Background(), w.processBrief(attempt), ProcessSpec{Brain: AgentkitName, Args: []string{"fix", "run"}}); err != nil || res.ExitCode != 0 {
			t.Fatalf("attempt %d: %+v, %v", attempt, res, err)
		}
	}

	if probes, runs := len(w.records(true)), len(w.runs()); probes != 1 || runs != 2 {
		t.Errorf("af --version ran %d times and af %d times, want once and twice", probes, runs)
	}
	var probed []string
	for _, e := range w.ledger() {
		if data, _ := e["data"].(map[string]any); e["kind"] == "box_probe" && data["ok"] == true {
			probed = append(probed, data["binary"].(string)+" "+data["version"].(string))
		}
	}
	if !slices.Equal(probed, []string{"af v1.0.0", "HOME "}) || len(w.report.warnings) != 0 {
		t.Errorf("box_probe entries that are ok: %v, warnings %+v; want af with its version and HOME, once, and no warning", probed, w.report.warnings)
	}
}

func TestProcess_AnAfThatFailsItsProbeStopsTheRunBeforeItStarts(t *testing.T) {
	w := newProcWorld(t, box.Request{Provider: "local"}, nil)
	w.script(fakeaf.Script{VersionExit: map[string]int{"af": 127}})

	_, err := w.p.Run(context.Background(), w.processBrief(1), ProcessSpec{Brain: AgentkitName})

	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || f.Retryable || !strings.Contains(f.Message, "af cannot run in the local box (image: none)") || !strings.Contains(f.Message, "code 127") {
		t.Errorf("failure = %+v, want sandbox, not retryable, naming af, the box and the code", f)
	}
	if probe := w.entry("box_probe"); probe["binary"] != "af" || probe["ok"] != false || len(w.runs()) != 0 {
		t.Errorf("box_probe = %v, %d runs; want af not ok and no run", probe, len(w.runs()))
	}
}

func TestProcess_AnotherAfInTheBoxIsAWarningOnce(t *testing.T) {
	w := newProcWorld(t, box.Request{Provider: "local"}, nil)
	w.script(fakeaf.Script{AFVersion: "v2.0.0"})

	for attempt := 1; attempt <= 2; attempt++ {
		if res, err := w.p.Run(context.Background(), w.processBrief(attempt), ProcessSpec{Brain: AgentkitName}); err != nil || res.ExitCode != 0 {
			t.Fatalf("attempt %d: %+v, %v; another af does not stop the run", attempt, res, err)
		}
	}

	if got := w.report.warned("box_af_version_mismatch"); len(got) != 1 || !strings.Contains(got[0], "v2.0.0") || !strings.Contains(got[0], "v1.0.0") {
		t.Errorf("warnings = %+v, want one box_af_version_mismatch that names both versions", w.report.warnings)
	}

	// What --version prints is compared with the af that runs.
	b := Brief{Box: w.session}
	for _, tc := range []struct {
		version, commit, printed string
		warns                    bool
	}{
		{"v1.0.0", "abc123", `{"name":"af","version":"v1.0.0","commit":"abc123"}`, false},
		{"v1.0.0", "abc123", `{"name":"af","version":"v1.0.0","commit":"zzz999"}`, true},
		{"v1.0.0", "", "v2.0.0\nmore text", true},
		{"v1.0.0", "abc123", "", false},
		{"", "", `{"version":"v9.9.9","commit":"x"}`, false},
	} {
		rec := &recorder{}
		b.Warn = rec.Warn
		found, _ := parseVersion(tc.printed)
		NewProcess(ProcessConfig{Version: tc.version, Commit: tc.commit}).compare(b, found)
		if warned := len(rec.warned("box_af_version_mismatch")) == 1; warned != tc.warns {
			t.Errorf("af %s (%s) printing %q: warned %v, want %v", tc.version, tc.commit, tc.printed, warned, tc.warns)
		}
	}
}

func TestProcess_SeesTheAllowedSettingsOfTheHostAndNoOtherVariable(t *testing.T) {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "FOO"} {
		t.Setenv(name, "secret-of-"+name)
	}
	settings := map[string]string{"HTTPS_PROXY": "http://proxy.example:3128", "NO_PROXY": "localhost", "TZ": "UTC", "LANG": "C.UTF-8", "SSL_CERT_FILE": "/etc/ssl/ca.pem"}
	for _, name := range allowedNames() {
		t.Setenv(name, settings[name])
		if _, ok := settings[name]; !ok {
			_ = os.Unsetenv(name) // t.Setenv puts it back when the test ends
		}
	}
	hostTmp := t.TempDir()
	t.Setenv("TMPDIR", hostTmp)
	w := newProcWorld(t, box.Request{Provider: "local", Env: map[string]string{VendorKeyVar: "sk-secret"}}, nil)

	if _, err := w.p.Run(context.Background(), w.processBrief(1), ProcessSpec{Brain: AgentkitName, Inline: map[string]string{"AF_MODEL": "m1"}}); err != nil {
		t.Fatal(err)
	}

	seen := envOfRecord(w.runs()[0])
	if seen[VendorKeyVar] != "sk-secret" || seen["AF_MODEL"] != "m1" || seen["HOME"] == "" || !strings.HasPrefix(seen["PATH"], filepath.Dir(w.af)+":") {
		t.Errorf("the process saw %v, want PATH with af first, HOME, the key of the box and AF_MODEL", seen)
	}
	for name := range seen {
		// PWD and GOCOVERDIR are the fake's script's own.
		if !slices.Contains(append(allowedNames(), "PATH", "HOME", VendorKeyVar, "AF_MODEL", "PWD", "GOCOVERDIR"), name) {
			t.Errorf("the process saw %s, which is not allowed", name)
		}
	}
	for name, want := range settings {
		if seen[name] != want {
			t.Errorf("the process saw %s=%q, want the host's %q", name, seen[name], want)
		}
	}
	if seen["TMPDIR"] == hostTmp {
		t.Errorf("the process was given the temporary directory of the host, and not its box's")
	}
	if ledger := testutil.ReadFile(t, filepath.Join(w.run.Path, "ledger.jsonl")); strings.Contains(ledger, "secret") {
		t.Errorf("the ledger holds a secret: %s", ledger)
	}
}

func TestProcess_IsToldTheCredentialsOfItsBoxByName(t *testing.T) {
	spec := ProcessSpec{Brain: AgentkitName, Credentials: []string{VendorKeyVar}}
	for _, tc := range []struct {
		name  string
		req   box.Request
		value string // what the process sees of the key, and "-" for nothing
	}{
		{"a box created with the key", box.Request{Provider: "local", Env: map[string]string{VendorKeyVar: "sk-box"}}, "sk-box"},
		{"a box that supplies its own", box.Request{Provider: "supplies", Registry: suppliesCredentials()}, ""},
		{"a box with none", box.Request{Provider: "local"}, "-"},
	} {
		w := newProcWorld(t, tc.req, noEnv)

		if _, err := w.p.Run(context.Background(), w.processBrief(1), spec); err != nil {
			t.Fatal(err)
		}

		value, ok := envOfRecord(w.runs()[0])[VendorKeyVar]
		if !ok {
			value = "-"
		}
		if value != tc.value {
			t.Errorf("%s: the process saw %s=%q, want %q", tc.name, VendorKeyVar, value, tc.value)
		}
	}
}

func TestProcess_APastItsTimeIsKilledWithItsChildrenAndKeepsItsOutput(t *testing.T) {
	for _, byContext := range []bool{false, true} {
		w := newProcWorld(t, box.Request{Provider: "local"}, nil)
		w.script(fakeaf.Script{SleepMS: 30000, SpawnChild: true, OutputJSON: "{}"})
		b := w.processBrief(1)
		ctx := context.Background()
		if byContext {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
		} else {
			b.Timeout = time.Second
		}

		res, err := w.p.Run(ctx, b, ProcessSpec{Brain: AgentkitName})

		if err != nil || !res.TimedOut || res.ExitCode != box.ExitTimeout {
			t.Errorf("by the context %v: %+v, %v; want a timeout, as a result", byContext, res, err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(must(w.boxFile(fakeaf.ChildPath))))
		if err != nil {
			t.Fatal(err)
		}
		testutil.Eventually(t, "the child of the process to be killed", func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) })
		var outputs []string
		for _, a := range w.report.artifacts {
			outputs = append(outputs, a.Kind+":"+filepath.Base(a.Path))
		}
		if slices.Sort(outputs); !slices.Equal(outputs, []string{"brain_output:agentkit.1.json", "brain_output:agentkit.1.stderr"}) {
			t.Errorf("by the context %v: artifacts %v, want what the process wrote, copied", byContext, outputs)
		}
		if entry := w.entry("brain_process"); entry["exit_code"] != float64(box.ExitTimeout) || entry["brain"] != AgentkitName || entry["duration_ms"] == nil {
			t.Errorf("by the context %v: brain_process = %v", byContext, entry)
		}
	}
}

func TestProcess_CopiesWhatTheProcessWroteToTheRunDirectory(t *testing.T) {
	w := newProcWorld(t, box.Request{Provider: "local"}, nil)
	w.script(fakeaf.Script{Exit: 3, OutputJSON: `{"x":1}`, StderrBytes: 70 << 10, Stderr: "END"})

	res, err := w.p.Run(context.Background(), w.processBrief(1), ProcessSpec{Brain: "ring1"})

	if err != nil || res.ExitCode != 3 || res.TimedOut || string(res.Output) != `{"x":1}` || len(res.Stderr) != maxStderrCopy {
		t.Fatalf("Run = %+v (%d bytes of errors), %v; want exit 3, the output whole and the last 64 KiB of the errors", res.ExitCode, len(res.Stderr), err)
	}
	if got := testutil.ReadFile(t, filepath.Join(w.run.Path, "ring1.1.json")); got != `{"x":1}` {
		t.Errorf("the host's copy of the output = %q", got)
	}
	if got := testutil.ReadFile(t, filepath.Join(w.run.Path, "ring1.1.stderr")); got != string(res.Stderr) {
		t.Errorf("the host's copy of the errors is %d bytes, want the %d returned", len(got), len(res.Stderr))
	}
	if entry := w.entry("brain_process"); entry["exit_code"] != 3.0 || entry["brain"] != "ring1" {
		t.Errorf("brain_process = %v", entry)
	}

	b := w.processBrief(2)
	b.RunDir = filepath.Join(w.run.Path, "missing", "dir")
	if _, err := w.p.Run(context.Background(), b, ProcessSpec{Brain: "ring1"}); err != nil {
		t.Fatal(err)
	}
	if got := w.report.warned("brain_output_unreadable"); len(got) != 2 || !strings.Contains(got[0], "could not be copied") {
		t.Errorf("warnings = %+v, want brain_output_unreadable for each file that could not be copied", w.report.warnings)
	}
	if _, err := w.p.Run(context.Background(), Brief{}, ProcessSpec{}); err == nil || !strings.Contains(err.Error(), "needs a box") {
		t.Errorf("a brief with no box: %v", err)
	}
}
