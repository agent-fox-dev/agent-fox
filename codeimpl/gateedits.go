package codeimpl

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// Edits that can make the gate pass without making the change correct. The
// harness cannot tell a legitimate one from a gamed one, so it reports them
// and leaves the judgement to whoever reviews the branch.

// gateConfigFile reports a build or lint configuration: the files that decide
// what the gate command runs and how strictly.
func gateConfigFile(p string) bool {
	base := path.Base(p)
	switch {
	case strings.EqualFold(base, "Makefile"), strings.EqualFold(base, "GNUmakefile"),
		strings.HasSuffix(base, ".mk"):
		return true
	case strings.HasPrefix(base, ".golangci."), strings.HasPrefix(base, ".eslintrc"),
		strings.HasPrefix(base, "eslint.config."), strings.HasPrefix(base, ".pylintrc"),
		strings.HasPrefix(base, ".flake8"), strings.HasPrefix(base, "ruff."), base == ".ruff.toml",
		base == "pytest.ini", base == "tox.ini", base == "setup.cfg", base == "Justfile",
		base == "clippy.toml", base == ".clippy.toml":
		return true
	}
	return strings.HasPrefix(p, ".github/workflows/")
}

// testFile reports a file that holds tests.
func testFile(p string) bool {
	base := path.Base(p)
	return strings.HasSuffix(base, "_test.go") || strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") ||
		strings.HasSuffix(base, "_test.py") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasSuffix(base, "_test.rs") || strings.HasSuffix(base, "Test.java")
}

// goldenFile reports an expected-output fixture.
func goldenFile(p string) bool {
	return strings.Contains(strings.ToLower(p), "golden")
}

// skipMarker matches a line that disables a test.
var skipMarker = regexp.MustCompile(`\bt\.Skip(Now|f)?\(|@pytest\.mark\.(skip|xfail)|\bpytest\.skip\(|` +
	`\b(it|test|describe)\.skip\(|\bxit\(|\bxdescribe\(|#\[ignore\]|@Disabled|@Ignore\b`)

// gateEdits describes what a change did to the gate, from git's name-status
// lines and unified patch. Each string is one finding.
func gateEdits(nameStatus, patch, specDir string) []string {
	var config, deleted, golden []string
	for _, line := range strings.Split(nameStatus, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		status, name := f[0][:1], f[len(f)-1]
		if specDir != "" && (name == specDir || strings.HasPrefix(name, specDir+"/")) {
			continue
		}
		switch {
		case gateConfigFile(name):
			config = append(config, name)
		case status == "D" && testFile(name):
			deleted = append(deleted, name)
		case (status == "M" || status == "D") && goldenFile(name):
			golden = append(golden, name)
		}
	}

	var out []string
	if len(config) > 0 {
		out = append(out, "the change edits the build or lint configuration the checks run on ("+
			strings.Join(config, ", ")+"); check it did not loosen the gate")
	}
	if len(deleted) > 0 {
		out = append(out, "the change deletes test files ("+strings.Join(deleted, ", ")+")")
	}
	if len(golden) > 0 {
		out = append(out, "the change rewrites expected-output files ("+strings.Join(golden, ", ")+
			"); check the new output is the intended one and not just what the code now prints")
	}
	if skipped := addedSkips(patch); len(skipped) > 0 {
		out = append(out, "the change adds test-skip markers in "+strings.Join(skipped, ", "))
	}
	return out
}

// addedSkips lists the test files that gained a skip marker.
func addedSkips(patch string) []string {
	var files []string
	seen := map[string]bool{}
	cur := ""
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			cur = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
		case strings.HasPrefix(line, "+") && cur != "" && testFile(cur) && skipMarker.MatchString(line):
			if !seen[cur] {
				seen[cur] = true
				files = append(files, cur)
			}
		}
	}
	return files
}

// flagGateEdits warns about edits that can make the gate pass without making
// the change correct, in what a phase changed since head. It never fails the
// task: a Makefile edit is sometimes the task.
func flagGateEdits(ctx context.Context, o Options, st *runState, head string) {
	bg, cancel := background(ctx)
	defer cancel()
	nameStatus, patch, err := st.git.DiffSince(bg, head)
	if err != nil {
		return
	}
	for _, msg := range gateEdits(nameStatus, patch, st.relSpecDir) {
		o.Run.Warn(toolio.WarnGateEdited, "high", "%s", msg)
	}
	if msg := project.MissingDocs(st.root, changedNames(nameStatus, st.relSpecDir)); msg != "" {
		o.Run.Warn(toolio.WarnDocsNotUpdated, "low", "%s", msg)
	}
}

// changedNames are the paths in git's name-status lines, the spec package
// excluded: it is the tool's own and says nothing about the code's docs.
func changedNames(nameStatus, specDir string) []string {
	var out []string
	for _, line := range strings.Split(nameStatus, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 2 {
			continue
		}
		name := f[len(f)-1]
		if specDir != "" && (name == specDir || strings.HasPrefix(name, specDir+"/")) {
			continue
		}
		out = append(out, name)
	}
	return out
}
