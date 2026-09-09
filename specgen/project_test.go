package specgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func projectDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectProfilePerEcosystem(t *testing.T) {
	cases := []struct {
		files    map[string]string
		language string
		allTests string
		linter   string
	}{
		{map[string]string{"go.mod": "module x\n"}, "go", "go test ./... -count=1", "go vet ./..."},
		{map[string]string{"Cargo.toml": "[package]\n"}, "rust", "cargo test", "cargo clippy -- -D warnings"},
		{map[string]string{"pyproject.toml": "[project]\n"}, "python", "pytest -q", "ruff check ."},
		{map[string]string{"pyproject.toml": "[project]\n", "uv.lock": ""}, "python",
			"uv run pytest -q", "uv run ruff check ."},
		{map[string]string{"package.json": "{}"}, "node", "npm test", "npm run lint"},
	}
	for _, c := range cases {
		got := DetectProfile(projectDir(t, c.files))
		if got.Language != c.language || got.AllTests != c.allTests || got.Linter != c.linter {
			t.Errorf("%v: got %+v", c.files, got)
		}
		if !got.Known() {
			t.Errorf("%v: Known() = false", c.files)
		}
	}
}

// The Makefile answers "how do I check this"; the manifest answers "what is
// this written in". Conflating them would make a Go repository with a
// Makefile look like a project with no language.
func TestMakefileOverridesTheCommandsButNotTheLanguage(t *testing.T) {
	got := DetectProfile(projectDir(t, map[string]string{
		"go.mod":   "module x\n",
		"Makefile": "test:\n\tgo test ./...\nlint:\n\tgo vet ./...\n",
	}))
	if got.Language != "go" {
		t.Errorf("Language = %q", got.Language)
	}
	if got.AllTests != "make test" || got.Linter != "make lint" {
		t.Errorf("commands = %q / %q", got.AllTests, got.Linter)
	}
}

func TestDetectProfileReturnsNothingWhenItCannotTell(t *testing.T) {
	if got := DetectProfile(t.TempDir()); got.Known() {
		t.Errorf("got %+v for an empty directory", got)
	}
}

// The skill asks a human to read tasks.json afterwards and check that
// test_commands names the project's real runner. Here it is a check the model
// repairs before the file is ever written.
func TestAuditTasksRefusesAnotherEcosystemsRunner(t *testing.T) {
	profile := DetectProfile(projectDir(t, map[string]string{"go.mod": "module x\n"}))

	err := profile.AuditTasks(map[string]any{
		"test_commands": map[string]any{
			"all_tests": "pytest -q",
			"linter":    "ruff check .",
		},
	})
	if err == nil {
		t.Fatal("a Python command in a Go project was accepted")
	}
	for _, want := range []string{"go", "go.mod", "pytest", "ruff", "go test ./... -count=1", `panic("not implemented")`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message is missing %q:\n%v", want, err)
		}
	}
}

func TestAuditTasksAcceptsTheProjectsOwnCommands(t *testing.T) {
	profile := DetectProfile(projectDir(t, map[string]string{"go.mod": "module x\n"}))
	if err := profile.AuditTasks(map[string]any{
		"test_commands": map[string]any{
			"all_tests":  "go test ./... -count=1",
			"linter":     "go vet ./...",
			"spec_tests": "go test ./widget/... -count=1",
		},
	}); err != nil {
		t.Errorf("the project's own commands were refused: %v", err)
	}
}

// The check is narrow on purpose. `go test` in a Python project is
// unambiguously wrong; a command nobody here has heard of might be
// ./scripts/ci.sh, which is legitimate.
func TestAuditTasksLeavesUnknownProgramsAlone(t *testing.T) {
	profile := DetectProfile(projectDir(t, map[string]string{"go.mod": "module x\n"}))
	if err := profile.AuditTasks(map[string]any{
		"test_commands": map[string]any{
			"all_tests": "./scripts/ci.sh --all",
			"linter":    "make lint",
		},
	}); err != nil {
		t.Errorf("an unrecognized runner was refused: %v", err)
	}
}

// With no detected language there is nothing to check against, and refusing
// on a guess would be worse than accepting.
func TestAuditTasksIsInertWithoutAProfile(t *testing.T) {
	var none Profile
	if err := none.AuditTasks(map[string]any{
		"test_commands": map[string]any{"all_tests": "pytest -q"},
	}); err != nil {
		t.Errorf("an unknown project refused a command: %v", err)
	}
	if none.LanguageBlock() != "" {
		t.Error("an unknown project should contribute no prompt block")
	}
}

// The prompt block is the positive half of the audit: saying it up front is
// cheaper than refusing a submission afterwards.
func TestLanguageBlockNamesTheCommands(t *testing.T) {
	profile := DetectProfile(projectDir(t, map[string]string{"go.mod": "module x\n"}))
	block := profile.LanguageBlock()
	for _, want := range []string{"go", "go.mod", "go test ./... -count=1", "go vet ./...", "task.touches"} {
		if !strings.Contains(block, want) {
			t.Errorf("the block is missing %q:\n%s", want, block)
		}
	}
}
