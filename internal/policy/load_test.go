package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// writeFile writes a file, and its directories, under root.
func writeFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadPolicy_TS_06_55 verifies TS-06-55:
// the policy comes from --policy, then guard.json, then detection defaults,
// with missing keys defaulted.
// Verifies: 06-REQ-8.1
func TestLoadPolicy_TS_06_55(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "check:\n\ttrue\n")
	files, err := checks.FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	detected, err := project.Detect(dir, files)
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	file := writeFile(t, other, "policy.json", `{"version":1,"suite":[["foo"]]}`)
	writeFile(t, dir, ".agent-fox/guard.json", `{"version":1,"protected_paths":["docs/"]}`)

	viaFlag, err := LoadPolicy(context.Background(), dir, file)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(viaFlag.Suite, [][]string{{"foo"}}) || !slices.Equal(viaFlag.ProtectedPaths, DefaultProtectedPaths()) || len(viaFlag.AllowPrograms) != 0 {
		t.Errorf("--policy gave %+v: want its suite, the default protected paths and no allowlist, although guard.json exists", viaFlag)
	}

	viaFile, err := LoadPolicy(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(viaFile.ProtectedPaths, []string{"docs/"}) || !reflect.DeepEqual(viaFile.Suite, detected.Suite) || len(viaFile.AllowPrograms) != 0 {
		t.Errorf("guard.json gave %+v: want docs/, the detected suite %v and no allowlist", viaFile, detected.Suite)
	}

	if err := os.Remove(filepath.Join(dir, ".agent-fox", "guard.json")); err != nil {
		t.Fatal(err)
	}
	defaults, err := LoadPolicy(context.Background(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(defaults.ProtectedPaths, []string{".git/", ".agent-fox/", ".specs/", ".github/workflows/", ".gitlab-ci.yml"}) ||
		!reflect.DeepEqual(defaults.Suite, detected.Suite) || len(defaults.AllowPrograms) != 0 {
		t.Errorf("the defaults are %+v", defaults)
	}
	if !slices.ContainsFunc(defaults.Suite, func(f []string) bool { return slices.Equal(f, []string{"make", "check"}) }) {
		t.Errorf("the suite %v lacks make check", defaults.Suite)
	}
}

// TestLoadPolicyKeys guards 06-REQ-8.1: every key of a file is taken as it is
// when it is there, an empty list included, and defaulted when it is not.
func TestLoadPolicyKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "Makefile", "check:\n\ttrue\n")
	writeFile(t, dir, ".agent-fox/guard.json", `{"version":1,"suite":[["a","b"],["c"]],"protected_paths":[],"allow_programs":["go","ls"]}`)

	p, err := LoadPolicy(context.Background(), dir, "")

	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Suite, [][]string{{"a", "b"}, {"c"}}) || len(p.ProtectedPaths) != 0 || !slices.Equal(p.AllowPrograms, []string{"go", "ls"}) {
		t.Errorf("policy = %+v, want the file's keys, and no protected paths because the file says none", p)
	}

	// With no key, nothing is detected that is not needed: the suite of the file
	// is not read from the repository.
	writeFile(t, dir, ".agent-fox/guard.json", `{"version":1}`)
	if p, err = LoadPolicy(context.Background(), dir, ""); err != nil || !slices.Equal(p.ProtectedPaths, DefaultProtectedPaths()) || len(p.Suite) == 0 {
		t.Errorf("a file with no keys gave %+v, %v", p, err)
	}
}

// TestLoadPolicyRefusesWhatCannotBeUsed guards 06-REQ-8.6: a policy file that
// does not parse is a *ConfigError that names it, and nothing is decided with a
// policy that was half read.
func TestLoadPolicyRefusesWhatCannotBeUsed(t *testing.T) {
	cases := map[string]string{
		"not JSON":              `{not json`,
		"no version":            `{"suite":[["a"]]}`,
		"another version":       `{"version":2}`,
		"an unknown key":        `{"version":1,"protected_path":[".git/"]}`,
		"a suite of strings":    `{"version":1,"suite":["make check"]}`,
		"an empty form":         `{"version":1,"suite":[[]]}`,
		"a path above":          `{"version":1,"protected_paths":["../x"]}`,
		"an absolute path":      `{"version":1,"protected_paths":["/etc"]}`,
		"an empty path":         `{"version":1,"protected_paths":[""]}`,
		"programs of the wrong": `{"version":1,"allow_programs":"go"}`,
		"two documents":         `{"version":1} {"version":1}`,
		"an empty file":         ``,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, ".agent-fox/guard.json", content)
			path := filepath.Join(dir, ".agent-fox", "guard.json")

			_, err := LoadPolicy(context.Background(), dir, "")

			var config *ConfigError
			if !errors.As(err, &config) || config.Path != path || !strings.Contains(err.Error(), path) {
				t.Errorf("LoadPolicy = %v, want a *ConfigError naming %s", err, path)
			}
		})
	}

	dir := t.TempDir()
	broken := writeFile(t, dir, ".agent-fox/guard.json", `{broken`)
	good := writeFile(t, t.TempDir(), "p.json", `{"version":1,"suite":[["x"]]}`)
	if _, err := LoadPolicy(context.Background(), dir, good); err != nil {
		t.Errorf("--policy with a broken guard.json beside it: %v, want guard.json not to be read", err)
	}
	var config *ConfigError
	if _, err := LoadPolicy(context.Background(), dir, filepath.Join(dir, "missing.json")); !errors.As(err, &config) || !strings.Contains(err.Error(), "missing.json") {
		t.Errorf("--policy with a file that is not there: %v, want a *ConfigError naming it", err)
	}
	if _, err := LoadPolicy(context.Background(), dir, ""); !errors.As(err, &config) || config.Path != broken {
		t.Errorf("a broken guard.json: %v", err)
	}
	// A directory where there should be a file.
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(context.Background(), dir, ""); !errors.As(err, &config) {
		t.Errorf("guard.json that is a directory: %v", err)
	}
	// Detection fails when gate.json cannot be used, and so do the defaults.
	bad := t.TempDir()
	writeFile(t, bad, ".agent-fox/gate.json", `{broken`)
	if _, err := LoadPolicy(context.Background(), bad, ""); !errors.As(err, &config) || !strings.Contains(err.Error(), "gate.json") {
		t.Errorf("defaults with a broken gate.json: %v, want a config error naming it", err)
	}
	if _, err := LoadPolicy(context.Background(), bad, good); err != nil {
		t.Errorf("a policy file that does not need detection: %v", err)
	}
}

// TestWriteTools_TS_06_56 verifies TS-06-56:
// write tools are denied under protected paths and outside --dir except under
// the temp directory.
// Verifies: 06-REQ-8.2
func TestWriteTools_TS_06_56(t *testing.T) {
	tmp := filepath.Join(os.TempDir(), "af-policy-test", "x.txt")
	tools := map[string]string{
		"Write": "file_path", "Edit": "file_path", "MultiEdit": "file_path", "NotebookEdit": "notebook_path",
		"write_file": "path", "edit_file": "path",
	}
	for tool, field := range tools {
		for _, tc := range []struct {
			path, decision, rule string
		}{
			{".specs/x.md", Deny, RuleProtectedPath},
			{dir + "/.git/config", Deny, RuleProtectedPath},
			{"./.github/workflows/ci.yml", Deny, RuleProtectedPath},
			{".gitlab-ci.yml", Deny, RuleProtectedPath},
			{".agent-fox/gate/labels.json", Deny, RuleProtectedPath},
			{"src/../.specs/x", Deny, RuleProtectedPath},
			{"/etc/x", Deny, RuleOutsideDir},
			{"../outside.txt", Deny, RuleOutsideDir},
			{"/work/repo2/a.go", Deny, RuleOutsideDir},
			{"/work/repo/../repo2/a.go", Deny, RuleOutsideDir},
			{"src/a.go", Allow, ""},
			{"a.go", Allow, ""},
			{dir + "/src/a.go", Allow, ""},
			{"docs/guide.md", Allow, ""},
			{".specsx/a", Allow, ""},
			{tmp, Allow, ""},
			{".github/CODEOWNERS", Allow, ""},
		} {
			d := Decide(defaultPolicy(), dir, tool, input(field, tc.path))

			if d.Decision != tc.decision || d.Rule != tc.rule {
				t.Errorf("%s %s: %+v, want %s by %q", tool, tc.path, d, tc.decision, tc.rule)
			}
			if d.Decision == Deny && (d.Segment == "" || d.Reason == "" || !strings.Contains(d.Reason, tc.path)) {
				t.Errorf("%s %s: a denial that does not say what it is about: %+v", tool, tc.path, d)
			}
			if d.Decision == Allow && (d.Rule != "" || d.Segment != "" || d.Reason != "") {
				t.Errorf("%s %s: an allowance that carries %+v", tool, tc.path, d)
			}
		}
	}

	// A tool reads the field that is its own.
	if d := Decide(defaultPolicy(), dir, "NotebookEdit", map[string]any{"file_path": ".specs/x"}); d.Decision != Deny {
		t.Errorf("a NotebookEdit with a file_path: %+v, want a denial: a write call that names no path in its own field cannot be judged", d)
	}
	for _, in := range []map[string]any{nil, {}, {"file_path": 7}, {"file_path": ""}} {
		d := Decide(defaultPolicy(), dir, "Write", in)
		if d.Decision != Deny || d.Rule != RuleUnparseable || d.Segment == "" || d.Reason == "" {
			t.Errorf("a Write with %v: %+v, want a denial by unparseable: there is no path to judge", in, d)
		}
	}
	// A policy with no protected paths protects none, and still keeps the
	// checkout.
	open := Policy{}
	if d := Decide(open, dir, "Write", input("file_path", ".specs/x")); d.Decision != Allow {
		t.Errorf("no protected paths: %+v", d)
	}
	if d := Decide(open, dir, "Write", input("file_path", "/etc/x")); d.Rule != RuleOutsideDir {
		t.Errorf("no protected paths, outside the checkout: %+v", d)
	}
}

// input is the input of a write call.
func input(field, path string) map[string]any {
	return map[string]any{field: path, "content": "x"}
}

// TestWriteToolsInTheDirectoryOfASession guards 06-REQ-8.2: a relative path is
// judged where the session is, which may be below the checkout, and against the
// checkout as well.
func TestWriteToolsInTheDirectoryOfASession(t *testing.T) {
	cwd := dir + "/sub"
	for _, tc := range []struct {
		path, decision, rule string
	}{
		{"../.specs/x", Deny, RuleProtectedPath},
		{"a.go", Allow, ""},
		{"../src/a.go", Allow, ""},
		{".specs/x", Deny, RuleProtectedPath},
		{"../../outside", Deny, RuleOutsideDir},
	} {
		d := DecideAt(defaultPolicy(), dir, cwd, "Write", input("file_path", tc.path))
		if d.Decision != tc.decision || d.Rule != tc.rule {
			t.Errorf("Write %s from %s: %+v, want %s by %q", tc.path, cwd, d, tc.decision, tc.rule)
		}
	}
	// And for a shell, which sees the .specs of the checkout from below it.
	d := DecideAt(defaultPolicy(), dir, cwd, "Bash", map[string]any{"command": "echo x > ../.specs/a"})
	if d.Rule != RuleProtectedPath {
		t.Errorf("a redirection from a subdirectory: %+v", d)
	}
	// A directory outside the checkout is not a place to judge from.
	d = DecideAt(defaultPolicy(), dir, "/elsewhere", "Write", input("file_path", "a.go"))
	if d.Decision != Allow {
		t.Errorf("a cwd outside the checkout is ignored: %+v", d)
	}
}

// TestUnknownToolsAreAllowed_TS_06_57 verifies TS-06-57:
// unknown tools are allowed with an empty rule, and every denial carries a
// rule from the closed set, a segment and a reason.
// Verifies: 06-REQ-8.3
func TestUnknownToolsAreAllowed_TS_06_57(t *testing.T) {
	for _, tool := range []string{"Read", "Grep", "WebFetch", "mcp__x", "run_command", "Glob", "Task", ""} {
		d := Decide(defaultPolicy(), dir, tool, map[string]any{"command": "git push", "file_path": ".specs/x", "path": "/etc/x", "argv": []any{"git", "push"}})
		if d != (Decision{Decision: Allow}) {
			t.Errorf("%q: %+v, want an allowance with nothing else", tool, d)
		}
	}

	rules := []string{RuleUnparseable, RuleHandsGuard, RuleGitMutation, RuleSuiteCommand, RuleAfCommand, RuleProgramNotAllowed, RuleProtectedPath, RuleOutsideDir}
	p := defaultPolicy()
	p.AllowPrograms = []string{"ls"}
	seen := map[string]bool{}
	for _, call := range []struct {
		tool  string
		input map[string]any
	}{
		{"Bash", map[string]any{"command": `echo "x`}},
		{"Bash", map[string]any{"command": "PATH=/x ls"}},
		{"Bash", map[string]any{"command": "git push"}},
		{"Bash", map[string]any{"command": "make check"}},
		{"Bash", map[string]any{"command": "af fix land"}},
		{"Bash", map[string]any{"command": "curl x"}},
		{"Bash", map[string]any{"command": "ls > .specs/a"}},
		{"Write", map[string]any{"file_path": "/etc/x"}},
		{"Write", map[string]any{"file_path": ".specs/x"}},
		{"Edit", nil},
	} {
		d := Decide(p, dir, call.tool, call.input)
		if d.Decision != Deny || !slices.Contains(rules, d.Rule) || d.Segment == "" || d.Reason == "" {
			t.Errorf("%s %v: %+v", call.tool, call.input, d)
		}
		seen[d.Rule] = true
	}
	for _, rule := range rules {
		if !seen[rule] {
			t.Errorf("no case of the test is denied by %s", rule)
		}
	}
}

// TestMarshalGuardFileIsAPolicyLoadPolicyTakes guards the guard.json that a loop places
// in a box: read from a file by LoadPolicy it is the policy it was made of, an empty list
// included, which a file takes as it is.
func TestMarshalGuardFileIsAPolicyLoadPolicyTakes(t *testing.T) {
	want := Policy{
		Suite:          [][]string{{"make", "check"}, {"go", "test", "./..."}},
		ProtectedPaths: DefaultProtectedPaths(),
		AllowPrograms:  []string{"git", "make"},
	}
	for name, p := range map[string]Policy{
		"a full policy": want,
		"empty lists":   {Suite: [][]string{{"make", "test"}}, ProtectedPaths: []string{}, AllowPrograms: []string{}},
		"no lists":      {},
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := MarshalGuardFile(p)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			writeFile(t, dir, ".agent-fox/guard.json", string(doc))

			got, err := LoadPolicy(context.Background(), dir, "")

			if err != nil {
				t.Fatalf("LoadPolicy of %s: %v", doc, err)
			}
			if len(got.Suite) != len(p.Suite) || len(got.Suite) > 0 && !reflect.DeepEqual(got.Suite, p.Suite) || !slices.Equal(got.ProtectedPaths, p.ProtectedPaths) || !slices.Equal(got.AllowPrograms, p.AllowPrograms) {
				t.Errorf("read back %+v from %s, want %+v", got, doc, p)
			}
			if !strings.Contains(string(doc), `"version": 1`) || strings.Contains(string(doc), "null") {
				t.Errorf("guard.json = %s, want the version and no null", doc)
			}
		})
	}
}
