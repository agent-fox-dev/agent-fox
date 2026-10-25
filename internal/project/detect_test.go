package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestDetect_CountsTheLanguagesMostFilesFirst: by extension, in any case,
// with the manifest at the root, a tie broken by the name.
func TestDetect_CountsTheLanguagesMostFilesFirst(t *testing.T) {
	d := detect(t, map[string]string{
		"go.mod": "module x\n", "a.go": "", "b.GO": "", "sub/c.go": "", "a.py": "", "b.py": "", "x.rs": "", "y.rs": "", "README.md": "",
	})
	var got []string
	for _, l := range d.Languages {
		got = append(got, fmt.Sprintf("%s:%d:%s", l.Name, l.Files, l.Manifest))
	}
	if want := []string{"go:3:go.mod", "python:2:", "rust:2:"}; !slices.Equal(got, want) {
		t.Errorf("languages = %v, want %v", got, want)
	}

	// The table of languages has javascript before java; the tie is by name.
	tie := detect(t, map[string]string{"a.js": "", "b.js": "", "a.java": "", "b.java": "", "c.rs": ""})
	var order []string
	for _, l := range tie.Languages {
		order = append(order, l.Name)
	}
	if want := []string{"java", "javascript", "rust"}; !slices.Equal(order, want) {
		t.Errorf("a tie is ordered %v, want %v", order, want)
	}

	named := detect(t, map[string]string{"a.sh": "", "b.cs": "", "c.cpp": "", "d.ts": "", "e.js": "", "f.kt": "", "g.php": "", "h.rb": "", "i.scala": "", "j.lua": "", "k.c": ""})
	var names []string
	for _, l := range named.Languages {
		names = append(names, l.Name)
	}
	if want := []string{"bash", "c", "c#", "c++", "javascript", "kotlin", "lua", "php", "ruby", "scala", "typescript"}; !slices.Equal(names, want) {
		t.Errorf("language names = %v, want %v", names, want)
	}
}

// TestDetect_AnEmptyDirectoryIsEmptyListsNotNothing: the report of an empty
// directory has its lists, empty, and not null.
func TestDetect_AnEmptyDirectoryIsEmptyListsNotNothing(t *testing.T) {
	d := detect(t, nil)
	if len(d.Languages) != 0 || len(d.Gate.Checks) != 0 || d.Gate.Source != SourceDetected {
		t.Errorf("an empty directory: %+v", d)
	}
	raw, _ := json.Marshal(d)
	for _, field := range []string{`"languages":[]`, `"checks":[]`, `"suite":[]`, `"programs":[]`} {
		if !strings.Contains(string(raw), field) {
			t.Errorf("an empty detection marshals as %s, want %s in it", raw, field)
		}
	}
}

// TestDetect_TheManifestsGiveTheChecksOfTheirLanguages: each manifest at the
// root gives its checks, by the lockfile and wrapper beside it, in the order
// Go, Python, Node, Rust, Java.
func TestDetect_TheManifestsGiveTheChecksOfTheirLanguages(t *testing.T) {
	pkg := `{"scripts":{"test":"jest","lint":"eslint ."}}`
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  [][]string
	}{
		{"go", map[string]string{"go.mod": "module x\n"}, [][]string{{"go", "vet", "./..."}, {"go", "test", "./..."}}},
		{"a go.mod below the root", map[string]string{"sub/go.mod": "module x\n"}, [][]string{}},
		{"rust", map[string]string{"Cargo.toml": "[package]\n"}, [][]string{{"cargo", "test"}}},
		{"maven", map[string]string{"pom.xml": "<project/>"}, [][]string{{"mvn", "-q", "test"}}},
		{"maven with a wrapper", map[string]string{"pom.xml": "<project/>", "mvnw": "#!/bin/sh\n"}, [][]string{{"./mvnw", "test"}}},
		{"gradle", map[string]string{"build.gradle": ""}, [][]string{{"gradle", "test"}}},
		{"gradle with a wrapper", map[string]string{"build.gradle.kts": "", "gradlew": "#!/bin/sh\n"}, [][]string{{"./gradlew", "test"}}},
		{"maven and gradle", map[string]string{"pom.xml": "", "build.gradle": ""}, [][]string{{"mvn", "-q", "test"}, {"gradle", "test"}}},
		{"pnpm", map[string]string{"package.json": pkg, "pnpm-lock.yaml": ""}, [][]string{{"pnpm", "test"}, {"pnpm", "run", "lint"}}},
		{"yarn", map[string]string{"package.json": pkg, "yarn.lock": ""}, [][]string{{"yarn", "test"}, {"yarn", "run", "lint"}}},
		{"bun", map[string]string{"package.json": pkg, "bun.lockb": ""}, [][]string{{"bun", "test"}, {"bun", "run", "lint"}}},
		{"bun's text lockfile", map[string]string{"package.json": pkg, "bun.lock": ""}, [][]string{{"bun", "test"}, {"bun", "run", "lint"}}},
		{"npm without a lockfile", map[string]string{"package.json": pkg}, [][]string{{"npm", "test"}, {"npm", "run", "lint"}}},
		{"npm's placeholder test script", map[string]string{"package.json": `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`}, [][]string{}},
		{"no lint script", map[string]string{"package.json": `{"scripts":{"test":"jest"}}`, "pnpm-lock.yaml": ""}, [][]string{{"pnpm", "test"}}},
		{"a package.json that is not JSON", map[string]string{"package.json": "{"}, [][]string{}},
		{"other languages add none", map[string]string{"a.rb": "", "b.php": ""}, [][]string{}},
		// Python: pytest or unittest, the uv or poetry prefix, ruff when configured.
		{"uv, pytest and ruff", map[string]string{"pyproject.toml": "[tool.pytest.ini_options]\n[tool.ruff]\n", "uv.lock": ""},
			[][]string{{"uv", "run", "pytest"}, {"uv", "run", "ruff", "check", "."}}},
		{"poetry and pytest in requirements", map[string]string{"pyproject.toml": "[project]\n", "poetry.lock": "", "requirements-dev.txt": "pytest>=8\n"},
			[][]string{{"poetry", "run", "pytest"}}},
		{"setup.py only", map[string]string{"setup.py": ""}, [][]string{{"python", "-m", "unittest", "discover"}}},
		{"tox.ini and a root conftest.py", map[string]string{"tox.ini": "", "conftest.py": ""}, [][]string{{"pytest"}}},
		{"pytest.ini only", map[string]string{"pytest.ini": ""}, [][]string{{"pytest"}}},
		{"pytest named in setup.cfg", map[string]string{"setup.cfg": "[options]\ntests_require = pytest\n"}, [][]string{{"pytest"}}},
		{"pytest named in pyproject", map[string]string{"pyproject.toml": "dev = [\"pytest\"]\n"}, [][]string{{"pytest"}}},
		{"ruff.toml", map[string]string{"setup.py": "", "ruff.toml": ""}, [][]string{{"python", "-m", "unittest", "discover"}, {"ruff", "check", "."}}},
		{".ruff.toml", map[string]string{"setup.py": "", ".ruff.toml": ""}, [][]string{{"python", "-m", "unittest", "discover"}, {"ruff", "check", "."}}},
		{"unittest under uv", map[string]string{"setup.py": "", "uv.lock": ""}, [][]string{{"uv", "run", "python", "-m", "unittest", "discover"}}},
		{"every language, in order", map[string]string{"pom.xml": "", "Cargo.toml": "", "package.json": pkg, "setup.py": "", "go.mod": "module x\n"},
			[][]string{{"go", "vet", "./..."}, {"go", "test", "./..."}, {"python", "-m", "unittest", "discover"}, {"npm", "test"}, {"npm", "run", "lint"}, {"cargo", "test"}, {"mvn", "-q", "test"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := argvs(detect(t, tc.files)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("checks = %v, want %v", got, tc.want)
			}
		})
	}

	d := detect(t, map[string]string{"pyproject.toml": "[tool.ruff]\n[tool.pytest]\n", "go.mod": "module x\n"})
	var got []string
	for _, c := range d.Gate.Checks {
		got = append(got, c.Name+"/"+c.Kind+"/"+c.Source)
	}
	if want := []string{"go vet ./.../lint/go.mod", "go test ./.../test/go.mod", "pytest/test/pyproject.toml", "ruff check ./lint/pyproject.toml"}; !slices.Equal(got, want) {
		t.Errorf("checks = %v, want %v", got, want)
	}
}

// TestDetect_SuiteAndProgramsAreInTheOrderFirstSeen: the gate's checks, make
// check and make test, then the forms and toolchains of the languages, each
// once.
func TestDetect_SuiteAndProgramsAreInTheOrderFirstSeen(t *testing.T) {
	d := detect(t, map[string]string{"Makefile": "check:\n\ttrue\ntest:\n\ttrue\n", "go.mod": "module x\n", "a.go": "", "Cargo.toml": "", "b.rs": ""})

	if want := [][]string{{"make", "check"}, {"make", "test"}, {"go", "vet", "./..."}, {"go", "test", "./..."}, {"cargo", "test"}}; !reflect.DeepEqual(d.Suite, want) {
		t.Errorf("suite = %v, want %v", d.Suite, want)
	}
	if want := []string{"make", "go", "gofmt", "cargo", "rustc"}; !slices.Equal(d.Programs, want) {
		t.Errorf("programs = %v, want %v", d.Programs, want)
	}
}

// TestDetect_TheChecksDoNotDependOnPath: which checks, in which order, with
// which argv, is the same whatever is on PATH; only availability follows it,
// and a path is looked for in the directory detected, executable.
func TestDetect_TheChecksDoNotDependOnPath(t *testing.T) {
	dir, list := fixture(t, map[string]string{"go.mod": "module x\n", "Cargo.toml": "", "package.json": `{"scripts":{"test":"jest"}}`, "pnpm-lock.yaml": ""})
	base, err := Detect(dir, list)
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, p := range []string{"go", "pnpm"} {
		if err := os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"", bin} {
		t.Setenv("PATH", path)
		d, err := Detect(dir, list)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(unavailable(d.Gate), unavailable(base.Gate)) || !reflect.DeepEqual(d.Suite, base.Suite) || !slices.Equal(d.Programs, base.Programs) {
			t.Fatalf("PATH=%q changed the detection: %+v, want %+v", path, d, base)
		}
		var on []string
		for _, c := range d.Gate.Checks {
			if c.Available {
				on = append(on, c.Name)
			}
		}
		if want := map[string][]string{"": nil, bin: {"go vet ./...", "go test ./...", "pnpm test"}}[path]; !slices.Equal(on, want) {
			t.Errorf("PATH=%q: available %v, want %v", path, on, want)
		}
	}

	dir, list = fixture(t, map[string]string{"pom.xml": "", "mvnw": "#!/bin/sh\n"})
	for mode, want := range map[os.FileMode]bool{0o755: true, 0o644: false} {
		if err := os.Chmod(filepath.Join(dir, "mvnw"), mode); err != nil {
			t.Fatal(err)
		}
		if d, err := Detect(dir, list); err != nil || d.Gate.Checks[0].Argv[0] != "./mvnw" || d.Gate.Checks[0].Available != want {
			t.Errorf("./mvnw of mode %o: %+v, %v; want available %v", mode, d.Gate.Checks, err, want)
		}
	}
	if err := os.Remove(filepath.Join(dir, "mvnw")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "mvnw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if d, _ := Detect(dir, list); d.Gate.Checks[0].Available {
		t.Error("a directory is an available program")
	}
}
