package project

import "testing"

func TestIsTestPath_FollowsTheConventionsOfEachLanguage(t *testing.T) {
	for _, p := range []string{
		"a_test.go", "pkg/testdata/x.json", "test_a.py", "a_test.py", "conftest.py", "a.test.ts", "b.spec.js",
		"__tests__/x.js", "src/test/java/A.java", "FooTest.java", "FooTests.java", "FooIT.java", "x_spec.rb",
		"x_test.rb", "tests/a.rs", "spec/x.rb", "a/test/b.c", "pkg/sub/deep_test.go", "pkg/conftest.py",
	} {
		if !IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"main.go", "latest.go", "contest.py", "src/main/java/A.java", "testing.md", "attest/x.go", "README.md", "src/spectrum/x.go", "test"} {
		if IsTestPath(p) {
			t.Errorf("IsTestPath(%q) = true, want false", p)
		}
	}
}
