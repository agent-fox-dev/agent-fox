package project

import (
	"slices"
	"testing"
)

// Issue #221: test support code — pytest's conftest.py, Jest and Vitest setup
// files, Go testutil packages, Ruby spec helpers and Java integration tests —
// is test code, not implementation, so the revert check keeps it.
func TestTestSupportFilesAreTests(t *testing.T) {
	for _, p := range []string{
		"conftest.py", "tests/unit/conftest.py", "src/setupTests.ts", "setupTests.js",
		"jest.setup.js", "vitest.setup.ts", "internal/testutil/fake.go", "pkg/testutils/x.go",
		"spec/models/user_spec.rb", "spec/spec_helper.rb", "test/test_helper.rb",
		"src/test/java/a/UserIT.java",
	} {
		if !IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = false", p)
		}
	}
	for _, p := range []string{"src/lib.rs", "app/models/user.rb", "internal/util/x.go", "IT.java"} {
		if IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = true", p)
		}
	}
}

// Issue #221: a command named by a path (./gradlew test) is looked up as
// spelled, so the families that key on a path are reachable.
func TestACommandNamedByAPathHasItsFamily(t *testing.T) {
	jvm := DetectProfile(projectDir(t, map[string]string{"pom.xml": "<project/>"}))
	if err := jvm.AuditTestCommandsFor("./gradlew test"); err != nil {
		t.Errorf("./gradlew test refused in a JVM project: %v", err)
	}
	py := DetectProfile(projectDir(t, map[string]string{"pyproject.toml": "x"}))
	if err := py.AuditTestCommandsFor("./gradlew test"); err == nil {
		t.Error("./gradlew test accepted in a Python project")
	}
}

// Issue #221: a repository with more than one ecosystem — a Go backend with a
// package.json frontend — accepts each one's commands; one from an ecosystem
// it does not have is still refused.
func TestAPolyglotRepositoryAcceptsEachOfItsEcosystems(t *testing.T) {
	p := DetectProfile(projectDir(t, map[string]string{"go.mod": "module x\n", "package.json": "{}"}))
	if p.Language != "go" || !slices.Contains(p.Also, "node") {
		t.Fatalf("profile = %+v, want go with node also present", p)
	}
	if err := p.AuditTasks(map[string]any{"test_commands": map[string]any{"all_tests": "npm test"}}); err != nil {
		t.Errorf("npm test refused in a Go + package.json repository: %v", err)
	}
	if err := p.AuditTasks(map[string]any{"test_commands": map[string]any{"all_tests": "pytest -q"}}); err == nil {
		t.Error("pytest accepted in a repository with no Python")
	}
}
