package toolio

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestMain keeps the tests of this package on the loopback interface and
// independent of the environment they run in: the default HTTP transport
// refuses any other address, and the variables the forge clients read are
// unset, so a test that wants one sets it itself.
func TestMain(m *testing.M) {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL"} {
		_ = os.Unsetenv(name)
	}
	http.DefaultTransport = &http.Transport{DialContext: loopbackOnly}
	os.Exit(m.Run())
}

// loopbackOnly dials the loopback interface and nothing else.
func loopbackOnly(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("refusing to dial %s: the tests may reach only the loopback interface", addr)
	}
	return (&net.Dialer{}).DialContext(ctx, network, addr)
}

// repoRoot is the root of the repository, from the directory of this package.
const repoRoot = "../.."

// readRepoFile reads a file of the repository.
func readRepoFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestMakeBuildWritesBinary_TS_05_7 verifies TS-05-7:
// make build writes bin/af with the Makefile's version and cmd/af compiles.
// The binary is built into a directory of the test's own (BIN_DIR), and the
// default location is read from the commands make would run.
// Verifies: 05-REQ-1.7
func TestMakeBuildWritesBinary_TS_05_7(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	build := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "af"), "./cmd/af")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/af: %v\n%s", err, out)
	}

	// The default of make build is bin/af; it is not run for it, so that a
	// test does not replace the binary a developer built there.
	plan := exec.Command("make", "-n", "build", "VERSION=9.9.9-test")
	plan.Dir = repoRoot
	planned, err := plan.CombinedOutput()
	if err != nil || !strings.Contains(string(planned), "-o bin/af ./cmd/af") ||
		!strings.Contains(string(planned), "-X main.version=9.9.9-test") ||
		!strings.Contains(string(planned), "-X main.commit=") || !strings.Contains(string(planned), "-X main.buildTime=") {
		t.Fatalf("make -n build: %v\n%s", err, planned)
	}

	bin := t.TempDir()
	make := exec.Command("make", "build", "VERSION=9.9.9-test", "BIN_DIR="+bin)
	make.Dir = repoRoot
	if out, err := make.CombinedOutput(); err != nil {
		t.Fatalf("make build: %v\n%s", err, out)
	}
	out, err := exec.Command(filepath.Join(bin, "af"), "--version").Output()
	if err != nil {
		t.Fatalf("af --version: %v", err)
	}
	var version struct {
		Name      string `json:"name"`
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
	}
	if err := json.Unmarshal(out, &version); err != nil {
		t.Fatalf("af --version is not JSON: %v\n%s", err, out)
	}
	if version.Name != "af" || version.Version != "9.9.9-test" || version.Commit == "" || version.BuildTime == "" {
		t.Errorf("af --version = %+v, want af, 9.9.9-test, and the Makefile's commit and build time", version)
	}

	// cmd/af is wiring: main.go hands the process to App.Main, and it is the
	// only file that ends the process. Spec 06 adds the files that register
	// the commands (docs/errata/05_cmd_af_files.md).
	if !strings.Contains(readRepoFile(t, "cmd/af/main.go"), "os.Exit(newApp().Main(") {
		t.Error("cmd/af/main.go does not exit with App.Main's return value")
	}
	files, _ := filepath.Glob(filepath.Join(repoRoot, "cmd", "af", "*.go"))
	for _, f := range files {
		if name := filepath.Base(f); name != "main.go" && !strings.HasSuffix(name, "_test.go") &&
			strings.Contains(readRepoFile(t, "cmd/af/"+name), "os.Exit(") {
			t.Errorf("cmd/af/%s ends the process; only main.go may, with App.Main's return value", name)
		}
	}
}

// contains reports every term the text lacks.
func missing(text string, terms ...string) []string {
	var lacking []string
	for _, term := range terms {
		if !strings.Contains(text, term) {
			lacking = append(lacking, term)
		}
	}
	return lacking
}

// TestDocumentationNamesWhatTheShellDefines_TS_05_72 verifies TS-05-72:
// The documentation files exist and name every status, exit code, category
// and warning code.
// Verifies: 05-REQ-10.5
func TestDocumentationNamesWhatTheShellDefines_TS_05_72(t *testing.T) {
	cli := readRepoFile(t, "docs/cli.md")

	for _, status := range []Status{StatusDone, StatusFailed, StatusUsage, StatusNeedsHuman, StatusUnverified} {
		row := regexp.MustCompile("\\|\\s*`" + string(status) + "`\\s*\\|\\s*" + fmt.Sprint(ExitCode(status)) + "\\s*\\|")
		if !row.MatchString(cli) {
			t.Errorf("docs/cli.md has no table row giving %s exit code %d", status, ExitCode(status))
		}
	}
	var terms []string
	for _, category := range []string{CategoryUsage, CategoryAuth, CategoryForge, CategoryIO, CategoryCancelled, CategoryTimeout, CategoryInternal, CategoryConfig, CategoryRestore, CategoryGit, CategorySandbox, CategorySubmission, CategoryBrain, CategoryNoResult, CategoryBudget} {
		terms = append(terms, "`"+category+"`")
	}
	for _, code := range WarningCodes {
		terms = append(terms, "`"+code+"`")
	}
	for _, field := range jsonTags(Envelope{}) {
		terms = append(terms, "`"+field+"`")
	}
	// The version the envelope carries, and the rule for changing it.
	terms = append(terms, "`"+SchemaVersion+"`", "## Interface versions", "bumps the major version")
	terms = append(terms, "--context", "--dry-run", "--schema", "--version", "--timeout", "--output", "--emit-events",
		"envelope.json", "events.jsonl", "ledger.jsonl", "report.md", "panic.txt", ".agent-fox/.gitignore", "go test ./internal/toolio ./cmd/af -update")
	if lacking := missing(cli, terms...); len(lacking) > 0 {
		t.Errorf("docs/cli.md does not mention %v", lacking)
	}

	configuration := readRepoFile(t, "docs/configuration.md")
	if lacking := missing(configuration, "GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL", ".agent-fox"); len(lacking) > 0 {
		t.Errorf("docs/configuration.md does not mention %v", lacking)
	}

	if usage := readRepoFile(t, "docs/model-usage.md"); !strings.Contains(usage, "runs no model") {
		t.Error("docs/model-usage.md does not say that af runs no model")
	}

	readme := readRepoFile(t, "README.md")
	if lacking := missing(readme, "cmd/af", "internal/toolio", "bin/af"); len(lacking) > 0 {
		t.Errorf("README.md does not mention %v", lacking)
	}

	architecture := readRepoFile(t, "docs/architecture.md")
	for _, claim := range []string{"already documents", "already defines"} {
		if strings.Contains(architecture, claim) {
			t.Errorf("docs/architecture.md still says %q", claim)
		}
	}
	if !strings.Contains(architecture, "docs/cli.md") || !strings.Contains(architecture, "ledger.jsonl") {
		t.Error("docs/architecture.md does not describe the run directory layout and the ledger")
	}
	for _, item := range []string{"The run directory layout and the ledger file formats"} {
		if strings.Contains(architecture, item) {
			t.Errorf("docs/architecture.md still lists %q among the sections to be written", item)
		}
	}

	if generated := readRepoFile(t, "docs/generated/cmd/af.txt"); !strings.Contains(generated, "Command af") {
		t.Error("docs/generated/cmd/af.txt does not hold the package documentation of cmd/af")
	}
}

// hostsAllowedInTests are the hosts that URLs in the test sources may name
// besides the loopback interface. They are text the tests parse, or schema
// identifiers; TestMain makes the default HTTP transport refuse to dial them.
var hostsAllowedInTests = map[string]bool{
	"127.0.0.1": true, "127.0.0.2": true, "localhost": true, "[::1]": true,
	"github.com": true, "gitlab.com": true, "example.com": true, "example.invalid": true, "json-schema.org": true,
}

// TestTestsStayOffTheNetwork_TS_05_73 verifies TS-05-73:
// Lint passes and the toolio and cmd tests contact only loopback servers.
// Verifies: 05-REQ-10.6
func TestTestsStayOffTheNetwork_TS_05_73(t *testing.T) {
	if _, err := exec.LookPath("make"); err == nil {
		lint := exec.Command("make", "lint")
		lint.Dir = repoRoot
		if out, err := lint.CombinedOutput(); err != nil {
			t.Errorf("make lint: %v\n%s", err, out)
		}
	}

	files, _ := filepath.Glob("*_test.go")
	more, _ := filepath.Glob(filepath.Join(repoRoot, "cmd", "af", "*_test.go"))
	commands, _ := filepath.Glob(filepath.Join(repoRoot, "internal", "afcmd", "*_test.go"))
	more = append(more, commands...)
	urlPattern := regexp.MustCompile(`https?://[^\s"'` + "`" + `<>)]+`)
	forbidden := regexp.MustCompile(`\bhttp\.(Get|Post|Head|PostForm)\(|http\.DefaultClient|\bnet\.Dial(Timeout)?\(`)
	for _, file := range append(files, more...) {
		if file == "docs_test.go" { // this file states the rules
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, raw := range urlPattern.FindAllString(line, -1) {
				if u, err := url.Parse(raw); err != nil || !hostsAllowedInTests[u.Hostname()] && !hostsAllowedInTests["["+u.Hostname()+"]"] {
					t.Errorf("%s:%d names %s, which is not on the loopback interface or an allowed inert host", file, i+1, raw)
				}
			}
			if forbidden.MatchString(line) {
				t.Errorf("%s:%d calls the network directly: %s", file, i+1, strings.TrimSpace(line))
			}
			if strings.Contains(line, "net.Listen(") && !strings.Contains(line, "127.0.0.1") && !strings.Contains(line, "localhost") {
				t.Errorf("%s:%d listens on more than the loopback interface", file, i+1)
			}
		}
	}

	// The scan reads sources; this is what enforces the rule at run time.
	if _, err := http.Get("http://example.com/"); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Errorf("a request to a host outside the loopback interface was not refused: %v", err)
	}
}
