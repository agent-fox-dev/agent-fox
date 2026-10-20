package project

import (
	"path"
	"slices"
	"strings"
)

// testDirs are the directory names that make everything below them tests.
var testDirs = []string{"test", "tests", "spec", "testdata", "__tests__"}

// testNames are the file name patterns of tests, by convention of the
// language: Go, Python, JavaScript and TypeScript, Java and Ruby.
var testNames = []string{
	"*_test.go",
	"test_*.py", "*_test.py", "conftest.py",
	"*.test.*", "*.spec.*",
	"*Test.java", "*Tests.java", "*IT.java",
	"*_spec.rb", "*_test.rb",
}

// IsTestPath reports whether the slash-separated path names a test: a file
// named as the conventions of its language name tests, or any file below a
// directory called test, tests, spec, testdata or __tests__.
func IsTestPath(p string) bool {
	segments := strings.Split(p, "/")
	dirs, name := segments[:len(segments)-1], segments[len(segments)-1]
	if slices.ContainsFunc(dirs, func(d string) bool { return slices.Contains(testDirs, d) }) {
		return true
	}
	return slices.ContainsFunc(testNames, func(pattern string) bool {
		ok, _ := path.Match(pattern, name)
		return ok
	})
}
