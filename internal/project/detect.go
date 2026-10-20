// Package project says what a repository is written in and how it is
// checked: the languages, the gate table of checks, the command forms that
// count as the full test suite, the programs a gate may run, and which paths
// are tests. It reads files; it runs nothing.
package project

import (
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Language is a language found in a repository.
type Language struct {
	Name     string `json:"name"`
	Files    int    `json:"files"`
	Manifest string `json:"manifest,omitempty"`
}

// Detection is what af lang detect reports.
type Detection struct {
	Languages []Language `json:"languages"`
	Gate      Table      `json:"gate"`
	Suite     [][]string `json:"suite"`
	Programs  []string   `json:"programs"`
}

// maxRead caps what is read of any one file: a manifest is small, and a file
// that is not must not be read whole.
const maxRead = 1 << 20

// language is a language the outline knows, the extensions that make a file
// one of its files, the manifests that mark a root as its project, and the
// programs of its toolchain.
type language struct {
	name       string
	extensions []string
	manifests  []string
	programs   []string
}

var languages = []language{
	{"go", []string{".go"}, []string{"go.mod"}, []string{"go", "gofmt"}},
	{"python", []string{".py"}, pythonManifests, []string{"python", "python3", "pytest", "ruff"}},
	{"javascript", []string{".js", ".jsx", ".mjs", ".cjs"}, []string{"package.json"}, []string{"node", "npm"}},
	{"typescript", []string{".ts", ".tsx", ".mts", ".cts"}, []string{"package.json"}, []string{"node", "npm"}},
	{"rust", []string{".rs"}, []string{"Cargo.toml"}, []string{"cargo", "rustc"}},
	{"java", []string{".java"}, javaManifests, []string{"java", "mvn", "gradle"}},
	{"kotlin", []string{".kt"}, []string{"build.gradle.kts", "build.gradle", "pom.xml"}, []string{"java", "gradle"}},
	{"ruby", []string{".rb"}, []string{"Gemfile"}, []string{"ruby", "bundle"}},
	{"c", []string{".c", ".h"}, []string{"CMakeLists.txt"}, nil},
	{"c++", []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"}, []string{"CMakeLists.txt"}, nil},
	{"c#", []string{".cs"}, nil, nil},
	{"php", []string{".php"}, []string{"composer.json"}, []string{"php", "composer"}},
	{"scala", []string{".scala"}, []string{"build.sbt"}, []string{"sbt"}},
	{"lua", []string{".lua"}, nil, nil},
	{"bash", []string{".sh", ".bash"}, nil, nil},
}

var (
	pythonManifests = []string{"pyproject.toml", "setup.py", "setup.cfg", "pytest.ini", "tox.ini"}
	javaManifests   = []string{"pom.xml", "build.gradle", "build.gradle.kts"}
)

// repo is the root of a repository as detection sees it: which files are
// there, and what is in them.
type repo struct {
	dir  string
	root map[string]bool // the files at the root, by name
}

func newRepo(dir string, files []string) repo {
	r := repo{dir: dir, root: map[string]bool{}}
	for _, f := range files {
		if !strings.Contains(f, "/") {
			r.root[f] = true
		}
	}
	return r
}

// first is the first of names that is a root file, or "".
func (r repo) first(names ...string) string {
	for _, name := range names {
		if r.root[name] {
			return name
		}
	}
	return ""
}

// read is the content of a root file, or "" when it cannot be read.
func (r repo) read(name string) string {
	f, err := os.Open(filepath.Join(r.dir, name))
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxRead))
	if err != nil {
		return ""
	}
	return string(data)
}

// Detect reports on the repository at dir, whose files are files: slash
// separated paths relative to dir. A gate.json that cannot be used is an
// error wrapping ErrGateConfig, and detection does not fall back to anything
// else.
func Detect(dir string, files []string) (Detection, error) {
	checks, found, err := readGateFile(dir)
	if err != nil {
		return Detection{}, err
	}
	return assemble(dir, files, checks, found), nil
}

// DetectFresh is Detect without the gate.json: the table that the repository's
// own files give, which is what af lang detect --write stores.
func DetectFresh(dir string, files []string) Detection {
	return assemble(dir, files, nil, false)
}

// assemble is the detection, with the checks of the gate.json if there is one.
func assemble(dir string, files []string, fileChecks []Check, fromFile bool) Detection {
	r := newRepo(dir, files)
	mk := readMakefile(r)
	derived := languageChecks(r)

	gate := Table{Source: SourceDetected}
	switch {
	case fromFile:
		gate = Table{Source: SourceFile, Checks: fileChecks}
	case len(mk.gateChecks()) > 0:
		gate.Checks = mk.gateChecks()
	default:
		gate.Checks = slices.Clone(derived)
	}
	if gate.Checks == nil {
		gate.Checks = []Check{}
	}
	for i := range gate.Checks {
		gate.Checks[i].Available = available(dir, gate.Checks[i].Argv[0])
	}

	langs := countLanguages(r, files)
	return Detection{
		Languages: langs,
		Gate:      gate,
		Suite:     suiteForms(gate.Checks, mk, derived),
		Programs:  toolchainPrograms(gate.Checks, langs, derived),
	}
}

// countLanguages counts the files of each language by extension, most files
// first and the name breaking a tie, each with the manifest at the root that
// marks its project.
func countLanguages(r repo, files []string) []Language {
	byExtension := map[string]int{}
	for i, l := range languages {
		for _, ext := range l.extensions {
			byExtension[ext] = i
		}
	}
	counts := make([]int, len(languages))
	for _, f := range files {
		if i, ok := byExtension[strings.ToLower(path.Ext(f))]; ok {
			counts[i]++
		}
	}

	found := []Language{}
	for i, l := range languages {
		if counts[i] > 0 {
			found = append(found, Language{Name: l.name, Files: counts[i], Manifest: r.first(l.manifests...)})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Files != found[j].Files {
			return found[i].Files > found[j].Files
		}
		return found[i].Name < found[j].Name
	})
	return found
}

// suiteForms are the commands that count as the full test suite: the gate's
// own, make check and make test where the Makefile has them, and the forms
// of each language, without repeats and in the order first seen.
func suiteForms(gate []Check, mk makefile, derived []Check) [][]string {
	suite := [][]string{}
	add := func(argv []string) {
		if !slices.ContainsFunc(suite, func(s []string) bool { return slices.Equal(s, argv) }) {
			suite = append(suite, argv)
		}
	}
	for _, c := range gate {
		add(c.Argv)
	}
	for _, target := range []string{"check", "test"} {
		if mk.has(target) {
			add([]string{"make", target})
		}
	}
	for _, c := range derived {
		add(c.Argv)
	}
	return suite
}

// toolchainPrograms are the programs a gate may run: the first word of each
// of its checks, then the toolchain of each language, without repeats and in
// the order first seen.
func toolchainPrograms(gate []Check, langs []Language, derived []Check) []string {
	programs := []string{}
	add := func(program string) {
		if !slices.Contains(programs, program) {
			programs = append(programs, program)
		}
	}
	for _, c := range gate {
		add(c.Argv[0])
	}
	for _, found := range langs {
		for _, l := range languages {
			if l.name == found.Name {
				for _, program := range l.programs {
					add(program)
				}
			}
		}
	}
	for _, c := range derived {
		add(c.Argv[0])
	}
	return programs
}

// languageChecks are the checks the manifests at the root give, in the order
// Go, Python, Node, Rust, Java. They depend on the files and on nothing else.
func languageChecks(r repo) []Check {
	var checks []Check
	for _, derive := range []func(repo) []Check{goChecks, pythonChecks, nodeChecks, rustChecks, javaChecks} {
		checks = append(checks, derive(r)...)
	}
	return checks
}

// newCheck is a check named by its command.
func newCheck(kind, source string, argv ...string) Check {
	return Check{Name: strings.Join(argv, " "), Argv: argv, Kind: kind, Source: source}
}

func goChecks(r repo) []Check {
	if !r.root["go.mod"] {
		return nil
	}
	return []Check{
		newCheck(KindLint, "go.mod", "go", "vet", "./..."),
		newCheck(KindTest, "go.mod", "go", "test", "./..."),
	}
}

func pythonChecks(r repo) []Check {
	manifest := r.first(pythonManifests...)
	if manifest == "" {
		return nil
	}
	var prefix []string
	switch {
	case r.root["uv.lock"]:
		prefix = []string{"uv", "run"}
	case r.root["poetry.lock"]:
		prefix = []string{"poetry", "run"}
	}
	with := func(argv ...string) []string { return append(slices.Clone(prefix), argv...) }

	test := with("python", "-m", "unittest", "discover")
	if usesPytest(r) {
		test = with("pytest")
	}
	checks := []Check{newCheck(KindTest, manifest, test...)}
	if r.first("ruff.toml", ".ruff.toml") != "" || strings.Contains(r.read("pyproject.toml"), "[tool.ruff") {
		checks = append(checks, newCheck(KindLint, manifest, with("ruff", "check", ".")...))
	}
	return checks
}

// usesPytest reports whether the root shows that pytest runs the tests: a
// pytest.ini, a conftest.py, or the name in the project files.
func usesPytest(r repo) bool {
	if r.root["pytest.ini"] || r.root["conftest.py"] {
		return true
	}
	named := []string{"pyproject.toml", "setup.cfg"}
	for name := range r.root {
		if ok, _ := path.Match("requirements*.txt", name); ok {
			named = append(named, name)
		}
	}
	return slices.ContainsFunc(named, func(name string) bool {
		return strings.Contains(r.read(name), "pytest")
	})
}

func nodeChecks(r repo) []Check {
	if !r.root["package.json"] {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal([]byte(r.read("package.json")), &pkg) != nil {
		return nil
	}
	pm := "npm"
	switch {
	case r.root["pnpm-lock.yaml"]:
		pm = "pnpm"
	case r.root["yarn.lock"]:
		pm = "yarn"
	case r.root["bun.lockb"], r.root["bun.lock"]:
		pm = "bun"
	}

	var checks []Check
	// npm's generated package.json carries a test script that only fails.
	if test, ok := pkg.Scripts["test"]; ok && !strings.Contains(test, "no test specified") {
		checks = append(checks, newCheck(KindTest, "package.json", pm, "test"))
	}
	if _, ok := pkg.Scripts["lint"]; ok {
		checks = append(checks, newCheck(KindLint, "package.json", pm, "run", "lint"))
	}
	return checks
}

func rustChecks(r repo) []Check {
	if !r.root["Cargo.toml"] {
		return nil
	}
	return []Check{newCheck(KindTest, "Cargo.toml", "cargo", "test")}
}

func javaChecks(r repo) []Check {
	var checks []Check
	if r.root["pom.xml"] {
		argv := []string{"mvn", "-q", "test"}
		if r.root["mvnw"] {
			argv = []string{"./mvnw", "test"}
		}
		checks = append(checks, newCheck(KindTest, "pom.xml", argv...))
	}
	if gradle := r.first("build.gradle", "build.gradle.kts"); gradle != "" {
		argv := []string{"gradle", "test"}
		if r.root["gradlew"] {
			argv = []string{"./gradlew", "test"}
		}
		checks = append(checks, newCheck(KindTest, gradle, argv...))
	}
	return checks
}
