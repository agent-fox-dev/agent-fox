package project

import (
	"slices"
	"testing"
)

// Issue #221: the manifests of the common ecosystems are detected, each with
// its conventional commands, and a lockfile picks the tool that runs them.
func TestDetectProfileKnowsTheCommonEcosystems(t *testing.T) {
	for _, c := range []struct {
		name     string
		files    map[string]string
		language string
		allTests string
		linter   string
	}{
		{"gradle wrapper", map[string]string{"build.gradle.kts": "", "gradlew": ""}, "jvm", "./gradlew test", "./gradlew check"},
		{"gradle", map[string]string{"build.gradle": ""}, "jvm", "gradle test", "gradle check"},
		{"dotnet project", map[string]string{"App.csproj": "<Project/>"}, "dotnet", "dotnet test", "dotnet format --verify-no-changes"},
		{"dotnet solution", map[string]string{"App.sln": ""}, "dotnet", "dotnet test", "dotnet format --verify-no-changes"},
		{"setup.py", map[string]string{"setup.py": ""}, "python", "pytest -q", "ruff check ."},
		{"requirements.txt", map[string]string{"requirements.txt": "requests\n"}, "python", "pytest -q", "ruff check ."},
		{"poetry", map[string]string{"pyproject.toml": "", "poetry.lock": ""}, "python", "poetry run pytest -q", "poetry run ruff check ."},
		{"pdm", map[string]string{"pyproject.toml": "", "pdm.lock": ""}, "python", "pdm run pytest -q", "pdm run ruff check ."},
		{"elixir", map[string]string{"mix.exs": ""}, "elixir", "mix test", "mix format --check-formatted"},
		{"php", map[string]string{"composer.json": "{}"}, "php", "vendor/bin/phpunit", ""},
		{"dart", map[string]string{"pubspec.yaml": "name: x\n"}, "dart", "dart test", "dart analyze"},
		{"flutter", map[string]string{"pubspec.yaml": "name: x\nflutter:\n  sdk: flutter\n"}, "dart", "flutter test", "flutter analyze"},
		{"swift", map[string]string{"Package.swift": ""}, "swift", "swift test", ""},
		{"cmake", map[string]string{"CMakeLists.txt": ""}, "cpp", "ctest --test-dir build", ""},
	} {
		got := DetectProfile(projectDir(t, c.files))
		if got.Language != c.language || got.AllTests != c.allTests || got.Linter != c.linter {
			t.Errorf("%s: got %s / %q / %q, want %s / %q / %q", c.name, got.Language, got.AllTests, got.Linter,
				c.language, c.allTests, c.linter)
		}
		if got.StubMarker == "" || got.TargetedRun() == "" {
			t.Errorf("%s: stub %q, targeted run %q", c.name, got.StubMarker, got.TargetedRun())
		}
	}
}

// Issue #221: a node project's commands are its own scripts, run by the tool
// its lockfile names; a script it does not have is not a command.
func TestANodeProjectsCommandsAreItsScripts(t *testing.T) {
	scripts := `{"scripts": {"test": "vitest run", "lint": "eslint ."}}`
	for _, c := range []struct {
		lock, allTests, linter string
	}{
		{"", "npm test", "npm run lint"},
		{"pnpm-lock.yaml", "pnpm test", "pnpm run lint"},
		{"yarn.lock", "yarn test", "yarn lint"},
		{"bun.lockb", "bun run test", "bun run lint"},
	} {
		files := map[string]string{"package.json": scripts}
		if c.lock != "" {
			files[c.lock] = ""
		}
		got := DetectProfile(projectDir(t, files))
		if got.AllTests != c.allTests || got.Linter != c.linter {
			t.Errorf("lock %q: %q / %q, want %q / %q", c.lock, got.AllTests, got.Linter, c.allTests, c.linter)
		}
	}
	none := DetectProfile(projectDir(t, map[string]string{
		"package.json": `{"scripts": {"test": "echo \"Error: no test specified\" && exit 1"}}`}))
	if none.Language != "node" || none.AllTests != "" || none.Linter != "" {
		t.Errorf("no real scripts: %+v, want node with no commands", none)
	}
}

// Issue #221: the runner families know the common tools, so another
// ecosystem's runner is refused — mocha in a Python project as jest is.
func TestRunnerFamiliesKnowTheCommonTools(t *testing.T) {
	py := DetectProfile(projectDir(t, map[string]string{"pyproject.toml": ""}))
	for _, cmd := range []string{"mocha", "ava", "bun test", "deno test", "npx playwright test", "mix test",
		"dotnet test", "./mvnw test", "swift test", "flutter test", "vendor/bin/phpunit"} {
		if err := py.AuditTestCommandsFor(cmd); err == nil {
			t.Errorf("%q accepted in a Python project", cmd)
		}
	}
	for _, cmd := range []string{"pdm run pytest", "pipenv run pytest", "nox", "hatch test"} {
		if err := py.AuditTestCommandsFor(cmd); err != nil {
			t.Errorf("%q refused in a Python project: %v", cmd, err)
		}
	}
	// The other ecosystems' manifests count as present ones.
	p := DetectProfile(projectDir(t, map[string]string{"go.mod": "", "build.gradle": "", "App.csproj": ""}))
	if !slices.Contains(p.Also, "jvm") || !slices.Contains(p.Also, "dotnet") {
		t.Errorf("Also = %v", p.Also)
	}
}
