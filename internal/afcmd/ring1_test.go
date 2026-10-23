package afcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
)

// ring1Packages are the packages of the ring-1 commands.
var ring1Packages = []string{"project", "checks", "conform", "policy", "repomap"}

// ring1Commands are the six commands, with what each of them is given.
var ring1Commands = []struct {
	path    string
	timeout string
	flags   []string
}{
	{"lang detect", "1m0s", []string{"--write"}},
	{"gate run", "30m0s", []string{"--label", "--hermetic", "--no-cache"}},
	{"gate compare", "30s", []string{"--before", "--after"}},
	{"revert-check", "30m0s", []string{"--since"}},
	{"guard check", "10s", []string{"--policy", "--host"}},
	{"map", "2m0s", []string{"--tokens"}},
}

// minimalInvocation is a valid use of each command in the repository dir, and
// the stdin it reads.
func minimalInvocation(t *testing.T, path, dir, base string) ([]string, string) {
	t.Helper()
	switch path {
	case "gate compare":
		return []string{"gate", "compare", "--dir", dir, "--before", "first", "--after", "first"}, ""
	case "revert-check":
		return []string{"revert-check", "--dir", dir, "--since", base}, ""
	case "guard check":
		return []string{"guard", "check", "--dir", dir}, neutral(t, "Bash", map[string]any{"command": "git diff"})
	}
	return append(strings.Fields(path), "--dir", dir), ""
}

// TestSixCommandsAreRegistered_TS_06_69 verifies TS-06-69:
// all six commands are registered with their default timeouts, flags in --help
// and tool equal to the command path.
// Verifies: 06-REQ-10.1
func TestSixCommandsAreRegistered_TS_06_69(t *testing.T) {
	requireSh(t)
	dir := gitRepo(t, map[string]string{"go.mod": "module x\n", "a.go": "package x\n"})
	writeGate(t, dir, []string{"ok", "sh", "-c", "exit 0"})
	base := baseOf(t, dir)
	if code, _ := gateRun(t, dir, "--label", "first"); code != 0 {
		t.Fatal("the first gate run failed")
	}

	for _, c := range ring1Commands {
		t.Run(c.path, func(t *testing.T) {
			args := append(strings.Fields(c.path), "--help")
			code, stdout, stderr := runWithStdin(t, strings.NewReader(""), args...)
			if code != 0 || stderr != "" {
				t.Fatalf("--help: exit code %d, stderr %q", code, stderr)
			}
			for _, flag := range append([]string{"--dir", "--timeout", "--output", "--emit-events"}, c.flags...) {
				if !strings.Contains(stdout, flag) {
					t.Errorf("--help does not list %s:\n%s", flag, stdout)
				}
			}
			if want := "(default " + c.timeout + ")"; !strings.Contains(stdout, "--timeout") || !strings.Contains(stdout, want) {
				t.Errorf("--help does not give the default timeout %s:\n%s", want, stdout)
			}

			argv, stdin := minimalInvocation(t, c.path, dir, base)
			code, stdout, _ = runWithStdin(t, strings.NewReader(stdin), argv...)
			env := oneDocument(t, stdout)
			if env["tool"] != c.path || env["status"] != "done" || code != 0 {
				t.Errorf("a valid invocation: exit code %d, status %v, tool %v; want 0, done, %s", code, env["status"], env["tool"], c.path)
			}
		})
	}

	_, stdout, _ := runWithStdin(t, strings.NewReader(""), "help")
	for _, c := range ring1Commands {
		if !strings.Contains(stdout, c.path) {
			t.Errorf("af help does not list %s", c.path)
		}
	}
}

// TestNoForgeNoNetworkNoRunDirectory_TS_06_70 verifies TS-06-70:
// none of the six commands builds a forge client, contacts the network or
// creates a run directory.
// Verifies: 06-REQ-10.2
func TestNoForgeNoNetworkNoRunDirectory_TS_06_70(t *testing.T) {
	requireSh(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on the loopback interface: %v", err)
	}
	defer listener.Close()
	var attempts atomic.Int32
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			conn.Close()
		}
	}()
	proxy := "http://" + listener.Addr().String()
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(name, proxy)
	}
	t.Setenv("NO_PROXY", "")
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL"} {
		t.Setenv(name, "") // restored after the test
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}

	dir := gitRepo(t, map[string]string{"go.mod": "module x\n", "a.go": "package x\n"})
	writeGate(t, dir, []string{"ok", "sh", "-c", "exit 0"})
	base := baseOf(t, dir)
	app := newApp()
	app.NewForge = func(issuex.Options) (issuex.Client, error) {
		t.Error("a command built a forge client")
		return nil, fmt.Errorf("no forge")
	}
	if code, _ := gateRun(t, dir, "--label", "first"); code != 0 {
		t.Fatal("the first gate run failed")
	}

	for _, c := range ring1Commands {
		argv, stdin := minimalInvocation(t, c.path, dir, base)
		var out, errb bytes.Buffer
		code := app.Main(context.Background(), append([]string{"af"}, argv...), strings.NewReader(stdin), &out, &errb)
		if env := oneDocument(t, out.String()); code != 0 || env["status"] != "done" {
			t.Errorf("%s: exit code %d, status %v, error %v; want it to succeed with no token and no remote", c.path, code, env["status"], env["error"])
		}
	}

	listener.Close()
	wg.Wait()
	if attempts.Load() != 0 {
		t.Errorf("%d connections were made", attempts.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "run")); err == nil {
		t.Error("a command created .agent-fox/run")
	}
}

// calls lists the calls in a file as "pkg.Func" for a selector and "name" for
// a plain identifier.
func calls(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if id, ok := fn.X.(*ast.Ident); ok {
				out = append(out, id.Name+"."+fn.Sel.Name)
			}
		case *ast.Ident:
			out = append(out, fn.Name)
		}
		return true
	})
	return out
}

// TestPackagesDoNotKnowTheShell_TS_06_71 verifies TS-06-71:
// the five internal packages do not import internal/toolio and do not end the
// process or panic on bad input.
// Verifies: 06-REQ-10.3
func TestPackagesDoNotKnowTheShell_TS_06_71(t *testing.T) {
	for _, pkg := range ring1Packages {
		dir := filepath.Join("..", "..", "internal", pkg)
		matches, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, path := range matches {
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "internal/toolio") {
					t.Errorf("%s imports %s", path, imp.Path.Value)
				}
			}
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			for _, call := range calls(f) {
				switch call {
				case "os.Exit", "log.Fatal", "log.Fatalf", "log.Fatalln", "log.Panic", "log.Panicf", "log.Panicln", "panic":
					t.Errorf("%s calls %s", path, call)
				}
			}
		}
	}
}

// jsonFields are the names a struct is encoded with and whether each is
// always there (no omitempty).
func jsonFields(typ reflect.Type) (all, required []string) {
	for i := range typ.NumField() {
		f := typ.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = f.Name
		}
		all = append(all, name)
		if !strings.Contains(opts, "omitempty") && !strings.Contains(opts, "omitzero") {
			required = append(required, name)
		}
	}
	slices.Sort(all)
	slices.Sort(required)
	return all, required
}

// schemaDiff lists where a schema and the Go type it describes differ: the
// properties of an object against the fields of a struct, the ones it requires
// against the ones that are never omitted, and the same below them.
func schemaDiff(schema map[string]any, typ reflect.Type, where string) []string {
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	var have []string
	for name := range props {
		have = append(have, name)
	}
	slices.Sort(have)
	all, required := jsonFields(typ)
	var diffs []string
	if !slices.Equal(have, all) {
		diffs = append(diffs, fmt.Sprintf("%s: the schema has %v, the Go type %v", where, have, all))
	}
	var req []string
	for _, r := range schema["required"].([]any) {
		req = append(req, r.(string))
	}
	slices.Sort(req)
	if !slices.Equal(req, required) {
		diffs = append(diffs, fmt.Sprintf("%s: the schema requires %v, the Go type always has %v", where, req, required))
	}
	if schema["additionalProperties"] != false {
		diffs = append(diffs, where+": the schema allows other properties")
	}

	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		sub, ok := props[name].(map[string]any)
		if !ok || name == "" || name == "-" {
			continue
		}
		if items, ok := sub["items"].(map[string]any); ok {
			sub = items
		}
		if _, nested := sub["properties"]; nested {
			diffs = append(diffs, schemaDiff(sub, f.Type, where+"."+name)...)
		}
	}
	return diffs
}

// TestSchemasMatchTheirTypes_TS_06_72 verifies TS-06-72:
// each command's embedded schema matches its Go result type's JSON fields,
// appears in af --schema, and the golden is current.
// Verifies: 06-REQ-10.4
func TestSchemasMatchTheirTypes_TS_06_72(t *testing.T) {
	results := map[string]reflect.Type{
		"lang_detect":  reflect.TypeOf(project.Detection{}),
		"gate_run":     reflect.TypeOf(checks.Result{}),
		"gate_compare": reflect.TypeOf(checks.Comparison{}),
		"revert_check": reflect.TypeOf(conform.Result{}),
		"guard_check":  reflect.TypeOf(guardResult{}),
		"map":          reflect.TypeOf(repomap.Result{}),
	}
	for name, typ := range results {
		var schema map[string]any
		if err := json.Unmarshal(resultSchema(name), &schema); err != nil {
			t.Fatalf("schemas/%s.json: %v", name, err)
		}
		for _, diff := range schemaDiff(schema, typ, name) {
			t.Error(diff)
		}
	}

	// The comparison notices a field that was renamed.
	type renamed struct {
		Decision string `json:"verdict"`
		Rule     string `json:"rule"`
		Reason   string `json:"reason"`
		Tool     string `json:"tool"`
		Segment  string `json:"segment"`
	}
	var schema map[string]any
	if err := json.Unmarshal(resultSchema("guard_check"), &schema); err != nil {
		t.Fatal(err)
	}
	if diffs := schemaDiff(schema, reflect.TypeOf(renamed{}), "guard_check"); len(diffs) == 0 {
		t.Error("a renamed field is not noticed")
	}

	// Every schema is in af --schema, under the path of its command, and the
	// envelope's version is the one of spec 05.
	_, stdout, _ := run(t, "--schema")
	var doc struct {
		Defs       map[string]json.RawMessage `json:"$defs"`
		Properties struct {
			Version struct {
				Const string `json:"const"`
			} `json:"schema_version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	for _, c := range ring1Commands {
		def := strings.ReplaceAll(c.path, " ", "_")
		if _, ok := doc.Defs[def]; !ok {
			t.Errorf("af --schema has no $defs.%s", def)
		}
	}
	if doc.Properties.Version.Const != "5.0.0" {
		t.Errorf("the envelope schema_version is %q, want the 5.0.0 of spec 05", doc.Properties.Version.Const)
	}
	golden, err := os.ReadFile(filepath.Join("..", "..", "cmd", "af", "testdata", "schema.golden.json"))
	if err != nil || string(golden) != stdout {
		t.Errorf("the golden file is not current (%v); rewrite it with go test ./internal/toolio ./cmd/af -update", err)
	}
}

// offlineScope are the packages whose tests this spec added or changed.
var offlineScope = []string{
	filepath.Join("..", "..", "internal", "gitx"), filepath.Join("..", "..", "internal", "project"),
	filepath.Join("..", "..", "internal", "checks"), filepath.Join("..", "..", "internal", "conform"),
	filepath.Join("..", "..", "internal", "policy"), filepath.Join("..", "..", "internal", "repomap"),
	filepath.Join("..", "..", "internal", "agentdir"), ".", filepath.Join("..", "..", "cmd", "af"),
}

// credentialName matches the name of a variable that holds a secret.
var credentialName = regexp.MustCompile(`(?i)token|secret|api_?key|password|credential|passwd`)

// quiet are the hosts a test may name besides the loopback interface: text it
// parses, not a place it connects to.
var quiet = map[string]bool{"127.0.0.1": true, "localhost": true, "[::1]": true, "::1": true, "example.com": true, "example.invalid": true, "github.com": true, "json-schema.org": true}

// TestTestsAreOffline_TS_06_75 verifies TS-06-75:
// the tests of the new packages are offline and skip when git is missing.
// Verifies: 06-REQ-10.7
func TestTestsAreOffline_TS_06_75(t *testing.T) {
	for _, dir := range offlineScope {
		paths, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
		for _, path := range paths {
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					id, _ := sel.X.(*ast.Ident)
					if id == nil {
						return true
					}
					call := id.Name + "." + sel.Sel.Name
					switch call {
					case "os.Getenv", "os.LookupEnv":
						if name := firstString(n); credentialName.MatchString(name) && name != "" {
							t.Errorf("%s reads %s", path, name)
						}
					case "net.Dial", "net.DialTimeout", "tls.Dial":
						if host := firstString(n); host != "" && !loopback(host) {
							t.Errorf("%s dials %s", path, host)
						}
					}
				case *ast.BasicLit:
					if n.Kind != token.STRING {
						return true
					}
					s, err := strconv.Unquote(n.Value)
					if err != nil || !(strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")) {
						return true
					}
					if u, err := url.Parse(s); err == nil && u.Host != "" && !quiet[u.Hostname()] && !quiet[u.Host] && !strings.Contains(u.Host, "%") {
						t.Errorf("%s names the host %s", path, u.Host)
					}
				}
				return true
			})
		}
	}
}

// firstString is the first argument of a call when it is a string literal.
func firstString(call *ast.CallExpr) string {
	for _, a := range call.Args {
		if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			return s
		}
	}
	return ""
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}

// TestSuitesPassWithoutGit_TS_06_75 verifies the second half of TS-06-75: with
// no git, no sh and nothing else on PATH, the tests of the packages skip what
// needs them and pass.
// Verifies: 06-REQ-10.7
func TestSuitesPassWithoutGit_TS_06_75(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles and runs the suites of eight packages")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("there is no go to compile the tests with; this is the run without PATH")
	}
	empty := t.TempDir()
	bins := t.TempDir()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	packages := []string{"./internal/gitx", "./internal/project", "./internal/checks", "./internal/conform", "./internal/policy", "./internal/repomap", "./internal/agentdir", "./internal/afcmd", "./cmd/af"}
	type outcome struct {
		pkg string
		out []byte
		err error
	}
	results := make(chan outcome, len(packages))
	for _, pkg := range packages {
		bin := filepath.Join(bins, strings.NewReplacer("/", "_", ".", "").Replace(pkg)+".test")
		compile := exec.Command(goTool, "test", "-c", "-o", bin, pkg)
		compile.Dir = root
		if out, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("go test -c %s: %v\n%s", pkg, err, out)
		}
		go func() {
			cmd := exec.Command(bin, "-test.count=1")
			cmd.Dir = filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, "./")))
			cmd.Env = []string{"PATH=" + empty, "HOME=" + t.TempDir(), "TMPDIR=" + os.TempDir()}
			out, err := cmd.CombinedOutput()
			results <- outcome{pkg, out, err}
		}()
	}
	for range packages {
		r := <-results
		if r.err != nil {
			t.Errorf("%s with no programs on PATH: %v\n%s", r.pkg, r.err, r.out)
		}
	}
}

// docsOf reads a documentation file of the repository.
func docsOf(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(data)
}

// TestDocumentation_TS_06_76 verifies TS-06-76:
// the documentation describes this scope, mentions the layout and contains no
// TODO or TBD.
// Verifies: 06-REQ-10.8
func TestDocumentation_TS_06_76(t *testing.T) {
	packages := []string{"internal/project", "internal/checks", "internal/conform", "internal/policy", "internal/repomap"}
	layout := []string{".agent-fox/gate/", ".agent-fox/revert/"}
	expect := map[string][]string{
		"docs/cli.md": {
			"### lang detect", "### gate run", "### gate compare", "### revert-check", "### guard check", "### map",
			"--write", "--label", "--hermetic", "--no-cache", "--before", "--after", "--since", "--policy", "--host", "--tokens",
			"`check_unavailable`", "`no_checks`", "`tree_changed_during_gate`", "`fix_not_proven`", "`map_outline_skipped`",
		},
		"docs/configuration.md": {"gate.json", "guard.json", "Precedence", "--policy"},
		"docs/model-usage.md":   {"runs no model", "call no model", "lang detect", "revert-check", "guard check"},
		"docs/architecture.md":  append(slices.Clone(packages), layout...),
		"docs/development.md":   append(append(slices.Clone(packages), layout...), "make check", "make doc", "-update"),
		"README.md":             append(slices.Clone(packages), "docs/development.md"),
	}
	for name, terms := range expect {
		text := docsOf(t, name)
		for _, term := range terms {
			if !strings.Contains(text, term) {
				t.Errorf("%s does not mention %s", name, term)
			}
		}
		for _, word := range []string{"TODO", "TBD"} {
			if regexp.MustCompile(`\b` + word + `\b`).MatchString(text) {
				t.Errorf("%s contains %s", name, word)
			}
		}
	}
	if development := docsOf(t, "docs/development.md"); !strings.Contains(development, "no network") {
		t.Error("docs/development.md does not say that make check needs no network")
	}
	if readme := docsOf(t, "README.md"); strings.Contains(readme, "No work command yet") {
		t.Error("README.md still says that there is no work command")
	}
}
