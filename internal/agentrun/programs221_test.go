package agentrun

import (
	"context"
	"slices"
	"testing"
)

// Issue #221: an implementing phase can run the common toolchains' own
// tools — a TypeScript model tsc and jest, a Python model ruff and mypy, a
// JVM model its build wrapper — without --allow.
func TestBuildProgramsCoverTheCommonToolchains(t *testing.T) {
	for _, p := range []string{"tsc", "eslint", "prettier", "jest", "vitest", "mocha", "bun", "deno",
		"ruff", "mypy", "black", "flake8", "poetry", "pipenv", "pdm", "tox",
		"mvn", "gradle", "./gradlew", "./mvnw", "dotnet", "bundle", "rake", "rspec", "ruby",
		"mix", "php", "composer", "dart", "flutter", "swift", "cmake", "ctest"} {
		if !slices.Contains(BuildPrograms, p) {
			t.Errorf("BuildPrograms lacks %s", p)
		}
	}
	g := Guard(GuardOptions{Programs: append(append([]string(nil), ReadOnlyPrograms...), BuildPrograms...),
		AllowOperators: true})
	for _, cmd := range []string{"./gradlew test --tests UserTest", "npx tsc --noEmit", "ruff check src", "bundle exec rspec spec/x_spec.rb"} {
		if d := g(context.Background(), execCall(cmd)); d.Block {
			t.Errorf("refused %q: %s", cmd, d.Reason)
		}
	}
}
