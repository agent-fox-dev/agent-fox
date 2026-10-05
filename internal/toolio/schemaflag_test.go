package toolio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// schemaApp is an App configured like fix, for the --schema path: every field
// the self-description document is built from is set, and Exec fails the test
// if it is ever reached.
func schemaApp(t *testing.T) *App {
	t.Helper()
	return &App{
		Name:    "tool",
		Version: "9.9.9",
		Usage:   "usage\n",
		Flags: func(fs *flag.FlagSet) {
			fs.String("land", "pr", "how the change lands")
			DeclareEnum(fs, "land", []string{"pr", "branch", "none"})
		},
		Description:      "Does a thing.",
		InputDescription: "Exactly one of text, a file, stdin or an issue.",
		ExitCodes:        map[int]string{ExitOK: "done", ExitFailed: "failed", ExitUsage: "usage"},
		ResultSample: struct {
			Stage string `json:"stage" description:"where it stopped"`
		}{},
		PreCheck: func(*Common) error {
			t.Error("PreCheck ran for --schema")
			return nil
		},
		CheckInput: func(Input) error {
			t.Error("CheckInput ran for --schema")
			return nil
		},
		Exec: func(context.Context, Deps) (int, any, *ErrorInfo) {
			t.Error("Exec ran for --schema")
			return ExitOK, nil, nil
		},
	}
}

// TS-09-1 (unit): Common.Register adds a boolean --schema flag defaulting to
// false.
func TestTS09_1_RegisterAddsSchemaFlag(t *testing.T) {
	var c Common
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.Register(fs)
	f := fs.Lookup("schema")
	if f == nil {
		t.Fatal("--schema is not registered")
	}
	if f.DefValue != "false" {
		t.Errorf("DefValue = %q, want false", f.DefValue)
	}
	g, ok := f.Value.(flag.Getter)
	if !ok {
		t.Fatalf("Value %T is not a flag.Getter", f.Value)
	}
	if _, ok := g.Get().(bool); !ok {
		t.Errorf("Get() = %T, want bool", g.Get())
	}
}

// TS-09-2 (integration): --schema prints the self-description document as one
// indented JSON object plus a newline, and exits 0.
func TestTS09_2_SchemaPrintsTheDocument(t *testing.T) {
	app := schemaApp(t)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--schema"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	out := stdout.Bytes()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("stdout is not one JSON object (%v):\n%s", err, out)
	}
	if !bytes.HasSuffix(out, []byte("}\n")) || bytes.HasSuffix(out, []byte("\n\n")) {
		t.Errorf("stdout must end in exactly one newline after the object:\n%q", out)
	}
	var tool, sv, desc string
	_ = json.Unmarshal(m["tool"], &tool)
	_ = json.Unmarshal(m["schema_version"], &sv)
	_ = json.Unmarshal(m["description"], &desc)
	if tool != "tool" || sv != SchemaVersion || desc != "Does a thing." {
		t.Errorf("tool=%q schema_version=%q description=%q", tool, sv, desc)
	}
	for _, k := range []string{"input", "flags", "result", "exit_codes"} {
		if _, ok := m[k]; !ok {
			t.Errorf("document has no %q key", k)
		}
	}
	// Indented, as Emit renders the envelope.
	if !bytes.Contains(out, []byte("\n  \"tool\"")) {
		t.Errorf("stdout is not indented:\n%s", out)
	}
	var in struct {
		Description string   `json:"description"`
		Kinds       []string `json:"kinds"`
	}
	if err := json.Unmarshal(m["input"], &in); err != nil {
		t.Fatal(err)
	}
	if want := "text,file,stdin,issue"; strings.Join(in.Kinds, ",") != want || in.Description == "" {
		t.Errorf("input = %+v, want kinds %s", in, want)
	}
	var codes map[string]string
	if err := json.Unmarshal(m["exit_codes"], &codes); err != nil {
		t.Fatal(err)
	}
	if len(codes) != 3 || codes["0"] != "done" {
		t.Errorf("exit_codes = %v", codes)
	}
	if bytes.Contains(m["flags"], []byte(`"schema"`)) || bytes.Contains(m["flags"], []byte(`"version"`)) {
		t.Errorf("flags must exclude --schema and --version: %s", m["flags"])
	}
	if !bytes.Contains(m["flags"], []byte(`"land"`)) {
		t.Errorf("flags lacks the tool's own flag: %s", m["flags"])
	}
}

// TS-09-3 (integration): --version keeps priority over --schema.
func TestTS09_3_VersionBeatsSchema(t *testing.T) {
	app := schemaApp(t)
	for _, argv := range [][]string{{"--schema", "--version"}, {"--version", "--schema"}} {
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
		if code != ExitOK || stdout.String() != "tool 9.9.9\n" {
			t.Errorf("%v: code=%d stdout=%q", argv, code, stdout.String())
		}
	}
}

// 09-REQ-1.4, 09-REQ-1.7: --schema takes no action on any other flag, so a
// value another check would refuse (--detail, --input-kind, --total-budget)
// does not pre-empt it; and --version, which wins over everything, is not
// pre-empted either. Only a flag-parse error comes first. Nothing is written
// to the state directory.
func TestTS09_SchemaAndVersionAreNotPreemptedByValueChecks(t *testing.T) {
	bad := [][]string{
		{"--detail", "bogus"},
		{"--input-kind", "bogus"},
		{"--total-budget", "-1"},
	}
	for _, flags := range bad {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			state := t.TempDir()
			t.Setenv("XDG_STATE_HOME", state)
			app := schemaApp(t)

			var stdout, stderr bytes.Buffer
			code := app.Main(context.Background(), append([]string{"--schema"}, flags...), strings.NewReader(""), &stdout, &stderr)
			if code != ExitOK {
				t.Fatalf("--schema %v: code = %d, want 0; stdout:\n%s", flags, code, stdout.String())
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil || doc["flags"] == nil {
				t.Errorf("--schema %v did not print the self-description document:\n%s", flags, stdout.String())
			}
			if entries, _ := os.ReadDir(state); len(entries) != 0 {
				t.Errorf("--schema %v wrote to the state directory: %v", flags, entries)
			}

			stdout.Reset()
			code = app.Main(context.Background(), append([]string{"--version"}, flags...), strings.NewReader(""), &stdout, &stderr)
			if code != ExitOK || stdout.String() != "tool 9.9.9\n" {
				t.Errorf("--version %v: code=%d stdout=%q", flags, code, stdout.String())
			}
		})
	}
}

// 09-REQ-1.5, in process: a positional argument beside --schema is ignored.
func TestTS09_5_SchemaIgnoresPositionalInProcess(t *testing.T) {
	app := schemaApp(t)
	run := func(argv ...string) (int, string) {
		var stdout, stderr bytes.Buffer
		code := app.Main(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
		return code, stdout.String()
	}
	c1, a := run("--schema")
	c2, b := run("--schema", "some text")
	if c1 != ExitOK || c2 != ExitOK || a != b || a == "" {
		t.Errorf("codes %d/%d, outputs differ or empty:\n%s\n---\n%s", c1, c2, a, b)
	}
}

// TS-09-7 (integration, in process): --schema makes no network call, needs no
// credential, no repository and resolves no model.
func TestTS09_7_SchemaMakesNoNetworkCallAndNeedsNoCredential(t *testing.T) {
	for _, v := range []string{"AF_MODEL", "AGENTKIT_MODEL", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GITHUB_TOKEN", "GITLAB_TOKEN"} {
		t.Setenv(v, "")
		_ = os.Unsetenv(v)
	}
	// An unsupported platform variable would fail model resolution; --schema
	// must not even look.
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var dialed bool
	old := http.DefaultTransport
	http.DefaultTransport = &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("no network in this test")
	}}
	defer func() { http.DefaultTransport = old }()

	app := schemaApp(t)
	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--dir", t.TempDir(), "--schema"}, strings.NewReader(""), &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("code = %d, stderr:\n%s", code, stderr.String())
	}
	if dialed {
		t.Error("a network connection was attempted")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
	if !json.Valid(stdout.Bytes()) {
		t.Errorf("stdout is not the document:\n%s", stdout.String())
	}
}

var (
	schemaBinOnce sync.Once
	schemaBinDir  string
	schemaBinErr  error
)

// schemaBin builds the four tools once and returns the path of one.
func schemaBin(t *testing.T, tool string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds four binaries")
	}
	schemaBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "toolio-schema-bin")
		if err != nil {
			schemaBinErr = err
			return
		}
		schemaBinDir = dir
		for _, name := range []string{"spec", "triage", "fix", "impl"} {
			build := exec.Command("go", "build", "-o", filepath.Join(dir, name), "github.com/agent-fox-dev/agentfox/cmd/"+name)
			if out, err := build.CombinedOutput(); err != nil {
				schemaBinErr = errors.New("go build " + name + ": " + err.Error() + "\n" + string(out))
				return
			}
		}
	})
	if schemaBinErr != nil {
		t.Fatal(schemaBinErr)
	}
	return filepath.Join(schemaBinDir, tool)
}

type binRun struct {
	stdout, stderr string
	code           int
}

// runBin runs a built tool with a clean environment: no model or forge
// credential, and a private state directory for the report file.
func runBin(t *testing.T, bin, state string, args ...string) binRun {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + state, "XDG_STATE_HOME=" + state}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", bin, err)
	}
	return binRun{stdout.String(), stderr.String(), code}
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// TS-09-4 (integration): --schema beside --report-file and --emit-events
// creates no files and has no run.
func TestTS09_4_SchemaWithReportAndEmitEventsFlagsCreatesNothing(t *testing.T) {
	bin := schemaBin(t, "fix")
	tmp, state := t.TempDir(), t.TempDir()
	r := runBin(t, bin, state, "--schema",
		"--report-file", filepath.Join(tmp, "out.json"),
		"--emit-events")
	if r.code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
	}
	if exists(filepath.Join(tmp, "out.json")) {
		t.Error("out.json exists after --schema")
	}
	if entries, _ := os.ReadDir(filepath.Join(state, "agent-fox")); len(entries) != 0 {
		t.Errorf("the default report directory was populated: %v", entries)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil || m["tool"] != "fix" {
		t.Errorf("stdout is not the fix document (%v):\n%s", err, r.stdout)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want empty (no events were emitted)", r.stderr)
	}
}

// TS-09-5 (integration): a positional argument beside --schema is accepted
// and ignored.
func TestTS09_5_PositionalArgumentBesideSchemaIsIgnored(t *testing.T) {
	bin := schemaBin(t, "fix")
	state := t.TempDir()
	a := runBin(t, bin, state, "--schema")
	b := runBin(t, bin, state, "--schema", "some text")
	if a.code != 0 || b.code != 0 {
		t.Fatalf("exit codes %d and %d", a.code, b.code)
	}
	if a.stdout != b.stdout || a.stdout == "" {
		t.Errorf("stdout differs or is empty:\n%s\n---\n%s", a.stdout, b.stdout)
	}
}

// TS-09-6 (integration): an unparseable flag beside --schema reports
// flag.Parse's own error, not the document.
func TestTS09_6_UnparseableFlagBesideSchemaIsReported(t *testing.T) {
	bin := schemaBin(t, "fix")
	r := runBin(t, bin, t.TempDir(), "--schema", "--not-a-real-flag")
	if r.code != ExitUsage {
		t.Errorf("exit = %d, want %d", r.code, ExitUsage)
	}
	if !strings.Contains(r.stderr, "flag provided but not defined") {
		t.Errorf("stderr lacks flag.Parse's error:\n%s", r.stderr)
	}
	if strings.Contains(r.stdout, `"flags"`) {
		t.Errorf("stdout carries the document:\n%s", r.stdout)
	}
}

// TS-09-7 (integration, binaries): each of the four tools prints its document
// and exits 0 with no credential, no model variable and --dir an empty
// directory.
func TestTS09_7_AllFourToolsDescribeThemselvesWithNoCredential(t *testing.T) {
	for _, tool := range []string{"spec", "triage", "fix", "impl"} {
		t.Run(tool, func(t *testing.T) {
			bin := schemaBin(t, tool)
			r := runBin(t, bin, t.TempDir(), "--dir", t.TempDir(), "--schema")
			if r.code != 0 {
				t.Fatalf("exit %d, stderr:\n%s", r.code, r.stderr)
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
				t.Fatalf("stdout is not one JSON object (%v):\n%s", err, r.stdout)
			}
			if m["tool"] != tool {
				t.Errorf("tool = %v, want %s", m["tool"], tool)
			}
			if r.stderr != "" || strings.Contains(strings.ToLower(r.stderr), "credential") {
				t.Errorf("stderr = %q, want empty", r.stderr)
			}
		})
	}
}
