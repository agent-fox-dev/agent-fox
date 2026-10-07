// Package project detects what a repository is written in and how it is
// checked, so that a plan naming another ecosystem's tooling can be refused
// in code rather than found in review.
package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// Profile is what this repository is written in and how it is checked.
//
// It exists to make the skill's "post-generation language audit" a mechanism
// instead of a chore. The skill asks a human to read tasks.json afterwards
// and check that test_commands names the project's real runner, because a
// model asked to plan work in a repository it did not look at will reach for
// whichever ecosystem its training favours and write `pytest` into a Go
// project. Detecting the answer in Go and refusing a mismatched submission
// turns that from a review item into a repair the model performs before the
// file is ever written.
type Profile struct {
	// Language is the ecosystem, or "" when it could not be determined.
	Language string
	// AllTests and Linter are the project's real commands.
	AllTests string
	Linter   string
	// Manifest is the file the detection was made from, for the message.
	Manifest string
	// StubMarker is the language's spelling of "not implemented", which the
	// integration task is meant to search for.
	StubMarker string // Also are the other ecosystems whose manifests are present beside the
	// primary one (a package.json frontend in a Go repository).
	Also []string
}

// TargetedRun is how the language's test runner runs one test or one file:
// the form a writing phase is pointed at when the whole suite is refused.
// Empty when the language is unknown.
func (p Profile) TargetedRun() string {
	switch p.Language {
	case "go":
		return "go test ./pkg -run Name"
	case "rust":
		return "cargo test <name>"
	case "python":
		for _, run := range []string{"uv run ", "poetry run ", "pdm run "} {
			if strings.HasPrefix(p.AllTests, run) {
				return run + "pytest tests/test_x.py::test_name"
			}
		}
		return "pytest tests/test_x.py::test_name"
	case "node":
		switch {
		case strings.HasPrefix(p.AllTests, "pnpm "):
			return "pnpm test -- <file>"
		case strings.HasPrefix(p.AllTests, "yarn "):
			return "yarn test <file>"
		case strings.HasPrefix(p.AllTests, "bun "):
			return "bun test <file>"
		}
		return "npm test -- <file>"
	case "jvm":
		if g, ok := strings.CutSuffix(p.AllTests, " test"); ok && strings.Contains(g, "gradle") {
			return g + " test --tests <Class>"
		}
		return "mvn test -Dtest=<Class>"
	case "ruby":
		return "bundle exec rspec spec/x_spec.rb"
	case "dotnet":
		return "dotnet test --filter <Name>"
	case "elixir":
		return "mix test test/x_test.exs"
	case "php":
		return "vendor/bin/phpunit --filter <name>"
	case "dart":
		if strings.HasPrefix(p.AllTests, "flutter ") {
			return "flutter test test/x_test.dart"
		}
		return "dart test test/x_test.dart"
	case "swift":
		return "swift test --filter <Name>"
	case "cpp":
		return "ctest --test-dir build -R <name>"
	}
	return ""
}

// Known reports whether detection found anything.
func (p Profile) Known() bool { return p.Language != "" }

// runnerFamilies maps a program name to the ecosystem it belongs to. A tasks
// artifact whose all_tests names a program from a DIFFERENT known ecosystem
// than the one detected is the mismatch worth refusing; a program in neither
// table is left alone, because a project may legitimately drive its tests
// through a script nobody here has heard of.
var runnerFamilies = map[string]string{
	"go": "go", "gofmt": "go", "golangci-lint": "go", "gotestsum": "go", "go-vet": "go",
	"pytest": "python", "ruff": "python", "mypy": "python", "tox": "python",
	"black": "python", "flake8": "python", "uv": "python", "poetry": "python", "python": "python", "python3": "python",
	"npm": "node", "npx": "node", "yarn": "node", "pnpm": "node", "jest": "node",
	"vitest": "node", "eslint": "node", "tsc": "node", "node": "node",
	"pdm": "python", "pipenv": "python", "nox": "python", "hatch": "python",
	"mocha": "node", "ava": "node", "bun": "node", "bunx": "node", "deno": "node", "playwright": "node",
	"cargo": "rust", "rustfmt": "rust", "clippy-driver": "rust", "rustc": "rust", "cargo-nextest": "rust",
	"mvn": "jvm", "gradle": "jvm", "./gradlew": "jvm", "gradlew": "jvm", "./mvnw": "jvm", "mvnw": "jvm",
	"dotnet": "dotnet",
	"bundle": "ruby", "rake": "ruby", "rspec": "ruby", "rubocop": "ruby",
	"mix": "elixir", "elixir": "elixir",
	"composer": "php", "php": "php", "phpunit": "php", "vendor/bin/phpunit": "php",
	"dart": "dart", "flutter": "dart",
	"swift": "swift",
	"ctest": "cpp", "cmake": "cpp",
}

// DetectProfile reads the repository and reports what it is written in.
//
// The Makefile is consulted for the commands but never for the language: a
// project that ships `make check` has answered "how do I check this", and the
// manifest has answered "what is this written in". Conflating them would make
// a Go repository with a Makefile look like a project with no language.
func DetectProfile(root string) Profile {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(root, name))
		return string(b), err == nil
	}

	glob := func(pattern string) (string, bool) {
		m, _ := filepath.Glob(filepath.Join(root, pattern))
		if len(m) == 0 {
			return "", false
		}
		return filepath.Base(m[0]), true
	}

	var p Profile
	switch {
	case exists("go.mod"):
		p = Profile{Language: "go", Manifest: "go.mod",
			AllTests: "go test ./... -count=1", Linter: "go vet ./...",
			StubMarker: `panic("not implemented")`}
	case exists("Cargo.toml"):
		p = Profile{Language: "rust", Manifest: "Cargo.toml",
			AllTests: "cargo test", Linter: "cargo clippy -- -D warnings",
			StubMarker: "todo!()"}
	case exists("pyproject.toml") || exists("setup.py") || exists("setup.cfg") || exists("requirements.txt"):
		manifest := "pyproject.toml"
		for _, m := range []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"} {
			if exists(m) {
				manifest = m
				break
			}
		}
		p = Profile{Language: "python", Manifest: manifest,
			AllTests: "pytest -q", Linter: "ruff check .",
			StubMarker: "raise NotImplementedError"}
		// The lockfile names the tool that owns the environment.
		for _, l := range []struct{ lock, run string }{{"uv.lock", "uv run"}, {"poetry.lock", "poetry run"}, {"pdm.lock", "pdm run"}} {
			if exists(l.lock) {
				p.AllTests, p.Linter = l.run+" pytest -q", l.run+" ruff check ."
				break
			}
		}
	case exists("package.json"):
		p = Profile{Language: "node", Manifest: "package.json",
			StubMarker: "throw new Error('not implemented')"}
		pkg, _ := read("package.json")
		p.AllTests, p.Linter = nodeCommands(pkg, nodeRunner(exists))
	case exists("pom.xml"):
		p = Profile{Language: "jvm", Manifest: "pom.xml",
			AllTests: "mvn -q test", Linter: "mvn -q verify",
			StubMarker: "throw new UnsupportedOperationException()"}
	case exists("build.gradle") || exists("build.gradle.kts"):
		manifest := "build.gradle"
		if !exists(manifest) {
			manifest = "build.gradle.kts"
		}
		gradle := "gradle"
		if exists("gradlew") {
			gradle = "./gradlew"
		}
		p = Profile{Language: "jvm", Manifest: manifest,
			AllTests: gradle + " test", Linter: gradle + " check",
			StubMarker: "throw new UnsupportedOperationException()"}
	case hasGlob(glob, "*.sln") || hasGlob(glob, "*.csproj"):
		manifest, ok := glob("*.sln")
		if !ok {
			manifest, _ = glob("*.csproj")
		}
		p = Profile{Language: "dotnet", Manifest: manifest,
			AllTests: "dotnet test", Linter: "dotnet format --verify-no-changes",
			StubMarker: "throw new NotImplementedException();"}
	case exists("Gemfile"):
		p = Profile{Language: "ruby", Manifest: "Gemfile",
			AllTests: "bundle exec rspec", Linter: "bundle exec rubocop",
			StubMarker: "raise NotImplementedError"}
	case exists("mix.exs"):
		p = Profile{Language: "elixir", Manifest: "mix.exs",
			AllTests: "mix test", Linter: "mix format --check-formatted",
			StubMarker: `raise "not implemented"`}
	case exists("composer.json"):
		p = Profile{Language: "php", Manifest: "composer.json",
			AllTests:   "vendor/bin/phpunit",
			StubMarker: `throw new \RuntimeException('not implemented');`}
	case exists("pubspec.yaml"):
		p = Profile{Language: "dart", Manifest: "pubspec.yaml",
			AllTests: "dart test", Linter: "dart analyze",
			StubMarker: "throw UnimplementedError();"}
		if spec, _ := read("pubspec.yaml"); flutterRe.MatchString(spec) {
			p.AllTests, p.Linter = "flutter test", "flutter analyze"
		}
	case exists("Package.swift"):
		p = Profile{Language: "swift", Manifest: "Package.swift",
			AllTests: "swift test", StubMarker: `fatalError("not implemented")`}
	case exists("CMakeLists.txt"):
		p = Profile{Language: "cpp", Manifest: "CMakeLists.txt",
			AllTests: "ctest --test-dir build", StubMarker: `throw std::logic_error("not implemented");`}
	default:
		return Profile{}
	}

	// The first manifest decides the language; the others present are other
	// ecosystems the repository also has (a frontend beside a backend), whose
	// commands are not another project's.
	for _, m := range manifestFamilies {
		if m.family != p.Language && hasGlob(glob, m.file) && !slices.Contains(p.Also, m.family) {
			p.Also = append(p.Also, m.family)
		}
	}

	// A Makefile that defines the targets is the project's own answer and
	// beats the ecosystem default.
	if mk, ok := read("Makefile"); ok {
		if makeTarget(mk, "test") {
			p.AllTests = "make test"
		}
		if makeTarget(mk, "lint") {
			p.Linter = "make lint"
		} else if makeTarget(mk, "check") && !makeTarget(mk, "test") {
			p.AllTests = "make check"
		}
	}
	return p
}

func makeTarget(makefile, name string) bool {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*:`).MatchString(makefile)
}

// AuditTasks checks a submitted tasks artifact against the detected project.
//
// It refuses exactly one thing: a test command whose program belongs to a
// different, known ecosystem. That is narrow on purpose. `go test ./...` in a
// Python project is unambiguously wrong and mechanically detectable; a
// command this table has never heard of might be `./scripts/ci.sh`, which is
// legitimate and which a stricter check would reject.
//
// The returned error is read by the model, so it names both what was
// submitted and what the project actually uses.
func (p Profile) AuditTasks(content map[string]any) error {
	if !p.Known() {
		return nil
	}
	commands, ok := content["test_commands"].(map[string]any)
	if !ok {
		return nil
	}
	var problems []string
	for _, field := range []string{"all_tests", "linter", "spec_tests"} {
		cmd, _ := commands[field].(string)
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		family, known := familyOf(cmd)
		if !known || p.hasEcosystem(family) {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"test_commands.%s is %q, which is a %s command", field, cmd, family))
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf(
		"this project is %s (detected from %s), but %s.\n"+
			"Use the project's real commands: all_tests %q, linter %q. "+
			"Check task.steps, task.touches and task.done_when for the same mistake — "+
			"steps must use %s constructs, touches must name paths that fit this project's "+
			"layout, and a stub marker here is %s.",
		p.Language, p.Manifest, strings.Join(problems, "; "),
		p.AllTests, p.Linter, p.Language, p.StubMarker)
}

// AuditTestCommands is the typed form of AuditTasks, for a package that has
// already been loaded rather than one being submitted by a model.
//
// The message is written for an operator rather than for a repairing model:
// the plan is on disk, and the person who can fix it is the one reading the
// envelope.
func (p Profile) AuditTestCommands(tc afspec.TestCommands) error {
	if !p.Known() {
		return nil
	}
	fields := []struct{ name, cmd string }{
		{"all_tests", tc.AllTests}, {"linter", tc.Linter},
	}
	if tc.SpecTests != nil {
		fields = append(fields, struct{ name, cmd string }{"spec_tests", *tc.SpecTests})
	}
	var problems []string
	for _, f := range fields {
		if strings.TrimSpace(f.cmd) == "" {
			continue
		}
		family, known := familyOf(f.cmd)
		if !known || p.hasEcosystem(family) {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"test_commands.%s is %q, which is a %s command", f.name, f.cmd, family))
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("this project is %s (detected from %s), but %s; the project's real "+
		"commands are all_tests %q and linter %q — fix tasks.json before implementing it",
		p.Language, p.Manifest, strings.Join(problems, "; "), p.AllTests, p.Linter)
}

// LanguageBlock is the section of a generation prompt that tells the model
// what the project is. It is the positive half of the audit above: saying it
// up front is cheaper than refusing a submission afterwards.
func (p Profile) LanguageBlock() string {
	if !p.Known() {
		return ""
	}
	return fmt.Sprintf(`
## Project language and tooling

This project is **%s**, detected from `+"`%s`"+`. The plan must fit it:

- %s
- `+"`task.steps`"+` must use %s constructs, not another language's.
- `+"`task.touches`"+` must name paths that exist in this project's layout.
- A stub marker in this language is `+"`%s`"+`.

A command from another ecosystem is refused before the artifact is written.
`, p.Language, p.Manifest, p.commandsLine(), p.Language, p.StubMarker)
}

// commandsLine says what the project's test and lint commands are, or that
// it defines none, for the language block.
func (p Profile) commandsLine() string {
	cmd := func(field, c string) string {
		if c == "" {
			return "`test_commands." + field + "`: the project defines none; choose one that fits it"
		}
		return "`test_commands." + field + "` is `" + c + "`"
	}
	return cmd("all_tests", p.AllTests) + "; " + cmd("linter", p.Linter) + " — unless you find better ones in the repository."
}

// AuditTestCommandsFor audits one command as all_tests.
func (p Profile) AuditTestCommandsFor(cmd string) error {
	return p.AuditTestCommands(afspec.TestCommands{AllTests: cmd})
}

// familyOf is the ecosystem a command's program belongs to: looked up as
// spelled first, so a program named by a path (`./gradlew`) finds its own
// entry, then by its base name.
func familyOf(cmd string) (string, bool) {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return "", false
	}
	if family, ok := runnerFamilies[f[0]]; ok {
		return family, true
	}
	family, ok := runnerFamilies[filepath.Base(f[0])]
	return family, ok
}

// hasEcosystem reports whether family is the project's language or one of
// the other ecosystems present beside it.
func (p Profile) hasEcosystem(family string) bool {
	return family == p.Language || slices.Contains(p.Also, family)
}

// manifestFamilies are the manifests DetectProfile recognizes, in its order
// of precedence, with the ecosystem each one means.
var manifestFamilies = []struct{ file, family string }{
	{"go.mod", "go"}, {"Cargo.toml", "rust"}, {"pyproject.toml", "python"}, {"setup.py", "python"},
	{"setup.cfg", "python"}, {"requirements.txt", "python"}, {"package.json", "node"}, {"pom.xml", "jvm"},
	{"build.gradle", "jvm"}, {"build.gradle.kts", "jvm"}, {"*.sln", "dotnet"}, {"*.csproj", "dotnet"},
	{"Gemfile", "ruby"}, {"mix.exs", "elixir"}, {"composer.json", "php"}, {"pubspec.yaml", "dart"},
	{"Package.swift", "swift"}, {"CMakeLists.txt", "cpp"},
}

// flutterRe finds a Flutter dependency in a pubspec.
var flutterRe = regexp.MustCompile(`(?m)^\s*flutter\s*:`)

func hasGlob(glob func(string) (string, bool), pattern string) bool {
	_, ok := glob(pattern)
	return ok
}

// nodeRunner is the package manager a node project uses, by its lockfile.
func nodeRunner(exists func(string) bool) string {
	switch {
	case exists("pnpm-lock.yaml"):
		return "pnpm"
	case exists("yarn.lock"):
		return "yarn"
	case exists("bun.lockb") || exists("bun.lock"):
		return "bun"
	}
	return "npm"
}

// nodeCommands are a node project's own test and lint scripts, as runner
// runs them. A script the package does not define is no command: the npm
// init stub test only fails, and a lint script that is not there fails too.
func nodeCommands(pkg, runner string) (allTests, linter string) {
	var doc struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal([]byte(pkg), &doc) != nil {
		return "", ""
	}
	if t := strings.TrimSpace(doc.Scripts["test"]); t != "" && !strings.Contains(t, "no test specified") {
		switch runner {
		case "bun":
			allTests = "bun run test"
		default:
			allTests = runner + " test"
		}
	}
	if strings.TrimSpace(doc.Scripts["lint"]) != "" {
		switch runner {
		case "yarn":
			linter = "yarn lint"
		default:
			linter = runner + " run lint"
		}
	}
	return allTests, linter
}
