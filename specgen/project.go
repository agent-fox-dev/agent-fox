package specgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	StubMarker string
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
	"cargo": "rust", "rustfmt": "rust", "clippy-driver": "rust",
	"mvn": "jvm", "gradle": "jvm", "./gradlew": "jvm",
	"dotnet": "dotnet",
	"bundle": "ruby", "rake": "ruby", "rspec": "ruby", "rubocop": "ruby",
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
	case exists("pyproject.toml"):
		p = Profile{Language: "python", Manifest: "pyproject.toml",
			AllTests: "pytest -q", Linter: "ruff check .",
			StubMarker: "raise NotImplementedError"}
		if exists("uv.lock") {
			p.AllTests, p.Linter = "uv run pytest -q", "uv run ruff check ."
		}
	case exists("package.json"):
		p = Profile{Language: "node", Manifest: "package.json",
			AllTests: "npm test", Linter: "npm run lint",
			StubMarker: "throw new Error('not implemented')"}
	case exists("pom.xml"):
		p = Profile{Language: "jvm", Manifest: "pom.xml",
			AllTests: "mvn -q test", Linter: "mvn -q verify",
			StubMarker: "throw new UnsupportedOperationException()"}
	case exists("Gemfile"):
		p = Profile{Language: "ruby", Manifest: "Gemfile",
			AllTests: "bundle exec rspec", Linter: "bundle exec rubocop",
			StubMarker: "raise NotImplementedError"}
	default:
		return Profile{}
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
		family, known := runnerFamilies[firstWord(cmd)]
		if !known || family == p.Language {
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

- `+"`test_commands.all_tests`"+` is `+"`%s`"+` and `+"`test_commands.linter`"+` is `+"`%s`"+`, unless you find better ones in the repository.
- `+"`task.steps`"+` must use %s constructs, not another language's.
- `+"`task.touches`"+` must name paths that exist in this project's layout.
- A stub marker in this language is `+"`%s`"+`.

A command from another ecosystem is refused before the artifact is written.
`, p.Language, p.Manifest, p.AllTests, p.Linter, p.Language, p.StubMarker)
}

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return filepath.Base(f[0])
}
