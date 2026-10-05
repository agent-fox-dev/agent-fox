package project

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// MissingDocs reports a change that touches code with documented behaviour and
// ships no documentation: it returns a message, or "" when there is nothing to
// say. changed is the repository-relative paths the change touches.
//
// It is a path heuristic, which is all a tool that works on any repository can
// have. A file counts as user-visible surface when it is source code, is not a
// test, and sits under cmd/, cli/ or api/ or has a name that says flags,
// configuration, a schema, an envelope, a policy, a guard or a command. The
// repository must have documentation to be behind: a docs/ directory or a
// README. Any changed docs/ file, README or other prose file counts as the
// documentation change.
func MissingDocs(root string, changed []string) string {
	if !hasDocs(root) {
		return ""
	}
	var surface []string
	for _, f := range changed {
		f = filepath.ToSlash(f)
		if IsDocsFile(f) {
			return ""
		}
		if IsSourceFile(f) && !IsTestPath(f) && isSurface(f) {
			surface = append(surface, f)
		}
	}
	if len(surface) == 0 {
		return ""
	}
	return "the change touches code with documented behaviour (" + strings.Join(surface, ", ") +
		") and no documentation changed: check whether docs/ or the README needs updating"
}

func hasDocs(root string) bool {
	for _, d := range []string{"docs", "doc"} {
		if info, err := os.Stat(filepath.Join(root, d)); err == nil && info.IsDir() {
			return true
		}
	}
	matches, _ := filepath.Glob(filepath.Join(root, "README*"))
	return len(matches) > 0
}

// IsDocsFile reports whether p, a slash-separated repository path, is
// documentation: under docs/ or doc/, or a prose file by extension.
func IsDocsFile(p string) bool {
	if strings.HasPrefix(p, "docs/") || strings.HasPrefix(p, "doc/") {
		return true
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".rst", ".adoc":
		return true
	}
	return false
}

var sourceExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".rs": true,
	".java": true, ".kt": true, ".rb": true, ".cs": true, ".c": true, ".cc": true, ".cpp": true, ".h": true,
}

// IsSourceFile reports whether p is source code by its extension.
func IsSourceFile(p string) bool { return sourceExts[strings.ToLower(path.Ext(p))] }

// IsTestPath reports whether p is a test or test data, by the naming and
// directory conventions of the common languages.
func IsTestPath(p string) bool {
	base := path.Base(p)
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") ||
		strings.HasPrefix(base, "test_") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.HasSuffix(base, "_test.rs") || strings.HasSuffix(base, "Test.java") {
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		switch seg {
		case "testdata", "test", "tests", "__tests__":
			return true
		}
	}
	return false
}

var surfaceDirs = map[string]bool{"cmd": true, "cli": true, "api": true}

var surfaceNames = []string{"flag", "config", "schema", "envelope", "policy", "guard", "command", "option"}

func isSurface(p string) bool {
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if surfaceDirs[seg] {
			return true
		}
	}
	base := strings.ToLower(path.Base(p))
	for _, n := range surfaceNames {
		if strings.Contains(base, n) {
			return true
		}
	}
	return false
}
