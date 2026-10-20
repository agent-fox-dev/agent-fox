package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fixture writes files (path to content) under a new directory and returns
// it with the list of its files, as Detect takes them.
func fixture(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir, listFiles(t, dir)
}

// listFiles lists the files under dir, slash-separated and sorted.
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "bin" || d.Name() == ".agent-fox") {
			return fs.SkipDir
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(files)
	return files
}

// detect is Detect on a fixture, failing the test on an error.
func detect(t *testing.T, files map[string]string) Detection {
	t.Helper()
	dir, list := fixture(t, files)
	d, err := Detect(dir, list)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	return d
}

// argvs lists the argv of the checks of d's gate.
func argvs(d Detection) [][]string {
	out := [][]string{}
	for _, c := range d.Gate.Checks {
		out = append(out, c.Argv)
	}
	return out
}

// TestDetectLanguages_TS_06_9 verifies TS-06-9:
// lang detect counts languages from the file view ordered by count then name
// and reports an empty directory as empty.
// Verifies: 06-REQ-2.1
func TestDetectLanguages_TS_06_9(t *testing.T) {
	d := detect(t, map[string]string{
		"go.mod": "module x\n", "a.go": "", "b.go": "", "sub/c.go": "",
		"a.py": "", "b.py": "", "x.rs": "", "y.rs": "", "README.md": "",
	})

	var got []string
	for _, l := range d.Languages {
		got = append(got, fmt.Sprintf("%s:%d", l.Name, l.Files))
	}
	if want := []string{"go:3", "python:2", "rust:2"}; !slices.Equal(got, want) {
		t.Errorf("languages = %v, want %v", got, want)
	}
	if d.Languages[0].Manifest != "go.mod" {
		t.Errorf("go's manifest = %q, want go.mod", d.Languages[0].Manifest)
	}

	empty := detect(t, nil)
	if len(empty.Languages) != 0 || len(empty.Gate.Checks) != 0 || empty.Gate.Source != SourceDetected {
		t.Errorf("an empty directory: languages %v, checks %v, source %q", empty.Languages, empty.Gate.Checks, empty.Gate.Source)
	}
	raw, _ := json.Marshal(empty)
	for _, field := range []string{`"languages":[]`, `"checks":[]`, `"suite":[]`, `"programs":[]`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("an empty detection marshals as %s, want %s in it", raw, field)
		}
	}
}

// TestLanguagesNamedLikeTheSpec guards TS-06-9: the languages of the hands'
// outline carry the spec's names.
func TestLanguagesNamedLikeTheSpec(t *testing.T) {
	d := detect(t, map[string]string{"a.sh": "", "b.cs": "", "c.cpp": "", "d.ts": "", "e.js": "", "f.kt": "", "g.php": ""})

	var names []string
	for _, l := range d.Languages {
		names = append(names, l.Name)
	}
	slices.Sort(names)
	if want := []string{"bash", "c#", "c++", "javascript", "kotlin", "php", "typescript"}; !slices.Equal(names, want) {
		t.Errorf("language names = %v, want %v", names, want)
	}
}

// TestGateFileIsTheEffectiveTable_TS_06_10 verifies TS-06-10:
// A parsing gate.json is the effective table with source file.
// Verifies: 06-REQ-2.2
func TestGateFileIsTheEffectiveTable_TS_06_10(t *testing.T) {
	d := detect(t, map[string]string{
		"Makefile":             "check:\n\ttrue\n",
		".agent-fox/gate.json": `{"version":1,"checks":[{"name":"ci","argv":["./ci.sh"],"kind":"check"}]}`,
	})

	if d.Gate.Source != SourceFile {
		t.Errorf("gate.source = %q, want file", d.Gate.Source)
	}
	if want := [][]string{{"./ci.sh"}}; !reflect.DeepEqual(argvs(d), want) {
		t.Errorf("checks = %v, want %v", argvs(d), want)
	}
	if c := d.Gate.Checks[0]; c.Name != "ci" || c.Kind != KindCheck || c.Source == "" {
		t.Errorf("check = %+v, want name ci, kind check and a source", c)
	}
}

// TestGateFileThatCannotBeUsed guards TS-06-10 and 06-REQ-2.6 at the package
// level: a gate.json that does not parse, names another version or holds a
// check that cannot run is an error that names the file, and detection does
// not fall back.
func TestGateFileThatCannotBeUsed(t *testing.T) {
	cases := map[string]string{
		"not JSON":          `{"version":`,
		"another version":   `{"version":2,"checks":[]}`,
		"no version":        `{"checks":[]}`,
		"an empty argv":     `{"version":1,"checks":[{"name":"x","argv":[]}]}`,
		"an unknown kind":   `{"version":1,"checks":[{"name":"x","argv":["a"],"kind":"smoke"}]}`,
		"a repeated name":   `{"version":1,"checks":[{"name":"x","argv":["a"]},{"name":"x","argv":["b"]}]}`,
		"an unknown field":  `{"version":1,"checks":[],"extra":true}`,
		"trailing document": `{"version":1,"checks":[]} {}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir, list := fixture(t, map[string]string{"Makefile": "check:\n", ".agent-fox/gate.json": content})

			_, err := Detect(dir, list)

			if !errors.Is(err, ErrGateConfig) {
				t.Fatalf("Detect = %v, want an error wrapping ErrGateConfig", err)
			}
			if want := filepath.Join(dir, ".agent-fox", "gate.json"); !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		})
	}
}

// TestGateFileDefaultsAreFilledIn guards TS-06-10: a check without a name is
// named by its command, one without a kind is a check, and availability is
// worked out and never read.
func TestGateFileDefaultsAreFilledIn(t *testing.T) {
	d := detect(t, map[string]string{
		".agent-fox/gate.json": `{"version":1,"checks":[{"argv":["sh","-c","true"]},{"name":"gone","argv":["no-such-program-anywhere"],"available":true}]}`,
	})

	if len(d.Gate.Checks) != 2 {
		t.Fatalf("checks = %+v", d.Gate.Checks)
	}
	if c := d.Gate.Checks[0]; c.Name != "sh -c true" || c.Kind != KindCheck || !c.Available {
		t.Errorf("first check = %+v, want name \"sh -c true\", kind check, available", c)
	}
	if c := d.Gate.Checks[1]; c.Available {
		t.Errorf("a program that does not exist is available: %+v", c)
	}
}

// TestDetectGateFromRootFiles_TS_06_11 verifies TS-06-11:
// Without gate.json the table is detected from root files with Makefile
// precedence, and this repository gives exactly make check.
// Verifies: 06-REQ-2.3
func TestDetectGateFromRootFiles_TS_06_11(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []Check
	}{
		{"a check target", map[string]string{"Makefile": "check: lint test\n\t@true\n"},
			[]Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: KindCheck, Source: "Makefile:check"}}},
		{"test and lint in a GNUmakefile", map[string]string{"GNUmakefile": "test:\n\ttrue\nlint:\n\ttrue\n"},
			[]Check{
				{Name: "make lint", Argv: []string{"make", "lint"}, Kind: KindLint, Source: "GNUmakefile:lint"},
				{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "GNUmakefile:test"},
			}},
		{"test only in a makefile", map[string]string{"makefile": "test:\n\ttrue\n"},
			[]Check{{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "makefile:test"}}},
		{"assignments and .PHONY are not targets", map[string]string{
			"go.mod": "module x\n", "Makefile": "VAR := x\nOTHER ::= y\nexport Z := 1\n.PHONY: check test\n"},
			[]Check{
				{Name: "go vet ./...", Argv: []string{"go", "vet", "./..."}, Kind: KindLint, Source: "go.mod"},
				{Name: "go test ./...", Argv: []string{"go", "test", "./..."}, Kind: KindTest, Source: "go.mod"},
			}},
		{"several targets before the colon", map[string]string{"Makefile": "all lint test: deps\n\ttrue\n"},
			[]Check{
				{Name: "make lint", Argv: []string{"make", "lint"}, Kind: KindLint, Source: "Makefile:lint"},
				{Name: "make test", Argv: []string{"make", "test"}, Kind: KindTest, Source: "Makefile:test"},
			}},
		{"a recipe line is not a target", map[string]string{"Makefile": "build:\n\techo check: done\n"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := detect(t, tc.files)

			if d.Gate.Source != SourceDetected {
				t.Errorf("gate.source = %q, want detected", d.Gate.Source)
			}
			var got []Check
			for _, c := range d.Gate.Checks {
				c.Available = false // PATH is not what is tested here
				got = append(got, c)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %+v, want %+v", got, tc.want)
			}
		})
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Detect(root, listFiles(t, root))
	if err != nil {
		t.Fatal(err)
	}
	if d.Languages[0].Name != "go" {
		t.Errorf("this repository's first language is %q, want go", d.Languages[0].Name)
	}
	if want := [][]string{{"make", "check"}}; !reflect.DeepEqual(argvs(d), want) {
		t.Errorf("this repository's checks = %v, want %v", argvs(d), want)
	}
}

// TestDetectLanguageChecks_TS_06_12 verifies TS-06-12:
// Go, Rust, Java and Node manifests yield exactly their listed checks.
// Verifies: 06-REQ-2.4
func TestDetectLanguageChecks_TS_06_12(t *testing.T) {
	pkg := `{"scripts":{"test":"jest","lint":"eslint ."}}`
	cases := []struct {
		name  string
		files map[string]string
		want  [][]string
	}{
		{"go", map[string]string{"go.mod": "module x\n"}, [][]string{{"go", "vet", "./..."}, {"go", "test", "./..."}}},
		{"rust", map[string]string{"Cargo.toml": "[package]\n"}, [][]string{{"cargo", "test"}}},
		{"maven", map[string]string{"pom.xml": "<project/>"}, [][]string{{"mvn", "-q", "test"}}},
		{"maven with a wrapper", map[string]string{"pom.xml": "<project/>", "mvnw": "#!/bin/sh\n"}, [][]string{{"./mvnw", "test"}}},
		{"gradle", map[string]string{"build.gradle": ""}, [][]string{{"gradle", "test"}}},
		{"gradle with a wrapper", map[string]string{"build.gradle.kts": "", "gradlew": "#!/bin/sh\n"}, [][]string{{"./gradlew", "test"}}},
		{"maven and gradle", map[string]string{"pom.xml": "", "build.gradle": ""}, [][]string{{"mvn", "-q", "test"}, {"gradle", "test"}}},
		{"pnpm", map[string]string{"package.json": pkg, "pnpm-lock.yaml": ""}, [][]string{{"pnpm", "test"}, {"pnpm", "run", "lint"}}},
		{"yarn", map[string]string{"package.json": pkg, "yarn.lock": ""}, [][]string{{"yarn", "test"}, {"yarn", "run", "lint"}}},
		{"bun", map[string]string{"package.json": pkg, "bun.lockb": ""}, [][]string{{"bun", "test"}, {"bun", "run", "lint"}}},
		{"npm without a lockfile", map[string]string{"package.json": pkg}, [][]string{{"npm", "test"}, {"npm", "run", "lint"}}},
		{"npm's placeholder test script", map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`}, [][]string{}},
		{"no lint script", map[string]string{"package.json": `{"scripts":{"test":"jest"}}`, "pnpm-lock.yaml": ""}, [][]string{{"pnpm", "test"}}},
		{"a package.json that is not JSON", map[string]string{"package.json": "{"}, [][]string{}},
		{"other languages add none", map[string]string{"a.rb": "", "b.php": ""}, [][]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvs(detect(t, tc.files)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}

	// The order across languages: Go, Python, Node, Rust, Java.
	d := detect(t, map[string]string{
		"pom.xml": "", "Cargo.toml": "", "package.json": pkg, "setup.py": "", "go.mod": "module x\n",
	})
	var order []string
	for _, c := range d.Gate.Checks {
		order = append(order, c.Source)
	}
	if want := []string{"go.mod", "go.mod", "setup.py", "package.json", "package.json", "Cargo.toml", "pom.xml"}; !slices.Equal(order, want) {
		t.Errorf("the checks come from %v, want %v", order, want)
	}
}

// TestDetectPythonChecks_TS_06_13 verifies TS-06-13:
// Python manifests choose pytest or unittest, the uv or poetry prefix, and
// ruff when configured.
// Verifies: 06-REQ-2.4
func TestDetectPythonChecks_TS_06_13(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  [][]string
	}{
		{"uv, pytest and ruff", map[string]string{
			"pyproject.toml": "[tool.pytest.ini_options]\n[tool.ruff]\n", "uv.lock": ""},
			[][]string{{"uv", "run", "pytest"}, {"uv", "run", "ruff", "check", "."}}},
		{"poetry and pytest in requirements", map[string]string{
			"pyproject.toml": "[project]\n", "poetry.lock": "", "requirements-dev.txt": "pytest>=8\n"},
			[][]string{{"poetry", "run", "pytest"}}},
		{"setup.py only", map[string]string{"setup.py": ""}, [][]string{{"python", "-m", "unittest", "discover"}}},
		{"tox.ini and a root conftest.py", map[string]string{"tox.ini": "", "conftest.py": ""}, [][]string{{"pytest"}}},
		{"pytest.ini only", map[string]string{"pytest.ini": ""}, [][]string{{"pytest"}}},
		{"pytest named in setup.cfg", map[string]string{"setup.cfg": "[options]\ntests_require = pytest\n"}, [][]string{{"pytest"}}},
		{"pytest named in pyproject", map[string]string{"pyproject.toml": "dev = [\"pytest\"]\n"}, [][]string{{"pytest"}}},
		{"ruff.toml", map[string]string{"setup.py": "", "ruff.toml": ""}, [][]string{{"python", "-m", "unittest", "discover"}, {"ruff", "check", "."}}},
		{"unittest under uv", map[string]string{"setup.py": "", "uv.lock": ""}, [][]string{{"uv", "run", "python", "-m", "unittest", "discover"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvs(detect(t, tc.files)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}

	kinds := detect(t, map[string]string{"pyproject.toml": "[tool.ruff]\n[tool.pytest]\n"})
	if kinds.Gate.Checks[0].Kind != KindTest || kinds.Gate.Checks[1].Kind != KindLint {
		t.Errorf("kinds = %s, %s; want test, lint", kinds.Gate.Checks[0].Kind, kinds.Gate.Checks[1].Kind)
	}
}

// TestSuiteAndPrograms_TS_06_14 verifies TS-06-14:
// suite and programs are de-duplicated in first-seen order and on this
// repository contain the make and go forms.
// Verifies: 06-REQ-2.5
func TestSuiteAndPrograms_TS_06_14(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Detect(root, listFiles(t, root))
	if err != nil {
		t.Fatal(err)
	}
	for _, form := range [][]string{{"make", "check"}, {"make", "test"}, {"go", "vet", "./..."}, {"go", "test", "./..."}} {
		if !containsArgv(d.Suite, form) {
			t.Errorf("this repository's suite %v lacks %v", d.Suite, form)
		}
	}
	for _, program := range []string{"make", "go"} {
		if !slices.Contains(d.Programs, program) {
			t.Errorf("this repository's programs %v lack %s", d.Programs, program)
		}
	}

	f := detect(t, map[string]string{"Makefile": "check:\n\ttrue\ntest:\n\ttrue\n", "go.mod": "module x\n", "Cargo.toml": ""})
	wantSuite := [][]string{{"make", "check"}, {"make", "test"}, {"go", "vet", "./..."}, {"go", "test", "./..."}, {"cargo", "test"}}
	if !reflect.DeepEqual(f.Suite, wantSuite) {
		t.Errorf("suite = %v, want %v", f.Suite, wantSuite)
	}
	if f.Programs[0] != "make" || !slices.Contains(f.Programs, "cargo") || !slices.Contains(f.Programs, "go") {
		t.Errorf("programs = %v, want make first, then go and cargo among the toolchain programs", f.Programs)
	}
	if len(slices.Compact(slices.Sorted(slices.Values(f.Programs)))) != len(f.Programs) {
		t.Errorf("programs %v repeat a name", f.Programs)
	}
}

func containsArgv(list [][]string, argv []string) bool {
	return slices.ContainsFunc(list, func(a []string) bool { return slices.Equal(a, argv) })
}

// TestDetectionDoesNotDependOnPath_TS_06_16 verifies TS-06-16:
// The selected checks, their order and argv are identical whatever is on PATH.
// Verifies: 06-REQ-2.7
func TestDetectionDoesNotDependOnPath_TS_06_16(t *testing.T) {
	dir, list := fixture(t, map[string]string{
		"go.mod": "module x\n", "Cargo.toml": "", "package.json": `{"scripts":{"test":"jest"}}`, "pnpm-lock.yaml": "",
	})
	strip := func(d Detection) Detection {
		for i := range d.Gate.Checks {
			d.Gate.Checks[i].Available = false
		}
		return d
	}
	base, err := Detect(dir, list)
	if err != nil {
		t.Fatal(err)
	}
	want := strip(base)

	rng := rand.New(rand.NewPCG(6, 16))
	programs := []string{"go", "cargo", "pnpm", "make", "sh"}
	paths := []string{"", os.Getenv("PATH")}
	for range 8 {
		bin := t.TempDir()
		for _, p := range programs {
			if rng.IntN(2) == 0 {
				if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
		paths = append(paths, bin)
	}

	availabilities := map[string]bool{}
	for _, p := range paths {
		t.Setenv("PATH", p)
		d, err := Detect(dir, list)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range d.Gate.Checks {
			availabilities[c.Name+fmt.Sprint(c.Available)] = true
		}
		if !reflect.DeepEqual(strip(d), want) {
			t.Fatalf("PATH=%q changed the checks: %+v, want %+v", p, strip(d), want)
		}
	}
	if len(availabilities) <= len(base.Gate.Checks) {
		t.Errorf("availability never varied with PATH: %v", availabilities)
	}
}

// TestRelativeProgramIsAvailableInTheDirectory guards 06-REQ-2.7: a program
// such as ./mvnw is looked for in the directory being detected, not in the
// directory the process happens to be in.
func TestRelativeProgramIsAvailableInTheDirectory(t *testing.T) {
	dir, list := fixture(t, map[string]string{"pom.xml": "", "mvnw": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(dir, "mvnw"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, err := Detect(dir, list)
	if err != nil {
		t.Fatal(err)
	}
	if c := d.Gate.Checks[0]; c.Argv[0] != "./mvnw" || !c.Available {
		t.Errorf("check = %+v, want ./mvnw available", c)
	}

	if err := os.Chmod(filepath.Join(dir, "mvnw"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, _ = Detect(dir, list)
	if d.Gate.Checks[0].Available {
		t.Error("a wrapper that is not executable is available")
	}
}

// TestWriteGateFileRoundTrips guards 06-REQ-2.8 at the package level: what is
// written is read back as the same table with source file, atomically, and
// without anything that depends on the machine.
func TestWriteGateFileRoundTrips(t *testing.T) {
	dir, list := fixture(t, map[string]string{"go.mod": "module x\n"})
	detected, err := Detect(dir, list)
	if err != nil {
		t.Fatal(err)
	}

	if err := WriteGateFile(dir, detected.Gate.Checks); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "available") {
		t.Errorf("gate.json holds the machine's availability: %s", data)
	}
	var file struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &file); err != nil || file.Version != 1 {
		t.Errorf("gate.json = %s", data)
	}
	again, err := Detect(dir, list)
	if err != nil {
		t.Fatal(err)
	}
	if again.Gate.Source != SourceFile || !reflect.DeepEqual(again.Gate.Checks, detected.Gate.Checks) {
		t.Errorf("read back %+v (source %s), want %+v", again.Gate.Checks, again.Gate.Source, detected.Gate.Checks)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox"))
	for _, e := range entries {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
	if status, err := os.ReadFile(filepath.Join(dir, ".agent-fox", ".gitignore")); err != nil || string(status) != "*\n" {
		t.Errorf(".agent-fox/.gitignore = %q (%v)", status, err)
	}
}

// TestIsTestPath_TS_06_39 verifies TS-06-39:
// Test-path classification follows the fixed convention table.
// Verifies: 06-REQ-6.2
func TestIsTestPath_TS_06_39(t *testing.T) {
	positive := []string{
		"a_test.go", "pkg/testdata/x.json", "test_a.py", "a_test.py", "conftest.py", "a.test.ts", "b.spec.js",
		"__tests__/x.js", "src/test/java/A.java", "FooTest.java", "FooTests.java", "FooIT.java", "x_spec.rb",
		"x_test.rb", "tests/a.rs", "spec/x.rb", "a/test/b.c", "pkg/sub/deep_test.go", "pkg/conftest.py",
	}
	negative := []string{"main.go", "latest.go", "contest.py", "src/main/java/A.java", "testing.md", "attest/x.go", "README.md", "src/spectrum/x.go"}
	for _, p := range positive {
		if !IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = false, want true", p)
		}
	}
	for _, p := range negative {
		if IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = true, want false", p)
		}
	}
}

// TestDetectFreshIgnoresTheGateFile guards 06-REQ-2.8: the table that
// af lang detect --write stores comes from the repository's files whatever
// gate.json says, even when it cannot be read.
func TestDetectFreshIgnoresTheGateFile(t *testing.T) {
	files := map[string]string{"Makefile": "check:\n\ttrue\n"}
	for name, gate := range map[string]string{
		"none":      "",
		"another":   `{"version":1,"checks":[{"name":"ci","argv":["./ci.sh"]}]}`,
		"not JSON":  `{not json`,
		"a version": `{"version":9,"checks":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			content := maps.Clone(files)
			if gate != "" {
				content[".agent-fox/gate.json"] = gate
			}
			dir, list := fixture(t, content)

			d := DetectFresh(dir, list)

			if d.Gate.Source != SourceDetected || !reflect.DeepEqual(argvs(d), [][]string{{"make", "check"}}) {
				t.Errorf("gate = %+v, want the detected make check", d.Gate)
			}
		})
	}
}
