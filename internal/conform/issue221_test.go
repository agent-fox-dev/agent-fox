package conform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// Issue #221: a Rust file carries its unit tests inline, in a #[cfg(test)]
// module. Taking the implementation out keeps that module as the change left
// it, so the tests run against the code as it was — and can prove the fix.
func TestTheRevertKeepsRustInlineTests(t *testing.T) {
	base := "pub fn add(a: i32, b: i32) -> i32 {\n    a - b\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n}\n"
	fixed := "pub fn add(a: i32, b: i32) -> i32 {\n    a + b\n}\n\n#[cfg(test)]\nmod tests {\n    use super::*;\n\n" +
		"    #[test]\n    fn adds() {\n        assert_eq!(add(2, 2), 4); // { brace in a comment\n        let s = \"}\";\n    }\n}\n"
	g, dir, head := newRepo(t, map[string]string{"src/lib.rs": base})
	writeFiles(t, dir, map[string]string{"src/lib.rs": fixed})
	var during string
	res, err := Revert(context.Background(), g, dir, head, []string{"src/lib.rs"}, func(context.Context) checks.Result {
		b, _ := os.ReadFile(filepath.Join(dir, "src", "lib.rs"))
		during = string(b)
		// The "suite": fails when the new test runs against the old code.
		ok := !(strings.Contains(during, "a - b") && strings.Contains(during, "fn adds()"))
		code := 0
		if !ok {
			code = 101
		}
		return checks.Result{Command: "cargo test", OK: ok, ExitCode: code}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(during, "a - b") || !strings.Contains(during, "fn adds()") {
		t.Errorf("during the check src/lib.rs was:\n%s\nwant the old code with the new test module", during)
	}
	if !res.Proves {
		t.Errorf("result = %+v; the inline test fails without the fix, so it proves it", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "src", "lib.rs")); string(b) != fixed {
		t.Errorf("src/lib.rs was not restored:\n%s", b)
	}
}

// Issue #221: an erratum citing a JavaScript test by its it()/describe()
// name cites a test.
func TestAJavaScriptTestNameIsATestReference(t *testing.T) {
	for _, s := range []string{
		"proved by `it('rejects an expired token')` in session.test.ts",
		`see describe("refresh") in session.spec.js`,
		"test(`counts once`) covers it",
	} {
		if !testRefRe.MatchString(s) {
			t.Errorf("not a test reference: %s", s)
		}
	}
	if testRefRe.MatchString("it is fine, described in the PRD") {
		t.Error("prose matched as a test reference")
	}
}
