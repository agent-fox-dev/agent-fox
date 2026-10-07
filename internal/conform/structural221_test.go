package conform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// allAdded marks every line of each file added by the change.
func allAdded(t *testing.T, root string, files ...string) Added {
	t.Helper()
	a := Added{}
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		lines := map[int]string{}
		for i, l := range strings.Split(string(b), "\n") {
			lines[i+1] = l
		}
		a[f] = lines
	}
	return a
}

func checksOf(fs []Finding) map[string][]string {
	out := map[string][]string{}
	for _, f := range fs {
		out[f.Check] = append(out[f.Check], f.Ref())
	}
	return out
}

// Issue #221: the long-function and assertion-less-test checks read Python,
// JavaScript/TypeScript and Rust too, not only Go.
func TestStructuralChecksReadOtherLanguages(t *testing.T) {
	root := t.TempDir()
	long := func(open, line, close string) string {
		return open + "\n" + strings.Repeat(line+"\n", 12) + close + "\n"
	}
	writeFiles(t, root, map[string]string{
		"src/app.py":           long("def handle(req):", "    x = 1", "    return x"),
		"tests/test_app.py":    "def test_handles():\n    handle(None)\n\ndef test_checks():\n    assert handle(1) == 1\n",
		"src/app.ts":           long("export function handle(req: Req) {", "  const x = 1;", "}"),
		"src/app.test.ts":      "it('handles', () => {\n  handle(req);\n});\n\ntest('checks', () => {\n  expect(handle(1)).toBe(1);\n});\n",
		"src/lib.rs":           long("pub fn handle(x: i32) -> i32 {", "    let y = x;", "}"),
		"tests/integration.rs": "#[test]\nfn handles() {\n    handle(1);\n}\n\n#[test]\nfn checks() {\n    assert_eq!(handle(1), 1);\n}\n",
	})
	files := []string{"src/app.py", "tests/test_app.py", "src/app.ts", "src/app.test.ts", "src/lib.rs", "tests/integration.rs"}
	got := checksOf(Scan(context.Background(), ScanInput{Root: root, Changed: files, Added: allAdded(t, root, files...),
		Today: time.Now(), MaxFuncLines: 10}))
	for _, want := range []string{"src/app.py:1", "src/app.ts:1", "src/lib.rs:1"} {
		if !strings.Contains(strings.Join(got[CheckLongFunction], " "), want) {
			t.Errorf("long_function = %v, want %s", got[CheckLongFunction], want)
		}
	}
	noAssert := strings.Join(got[CheckNoAssertions], " ")
	for _, want := range []string{"tests/test_app.py:1", "src/app.test.ts:1", "tests/integration.rs:2"} {
		if !strings.Contains(noAssert, want) {
			t.Errorf("no_assertions = %v, want %s", got[CheckNoAssertions], want)
		}
	}
	for _, not := range []string{"tests/test_app.py:4", "src/app.test.ts:5", "tests/integration.rs:7"} {
		if strings.Contains(noAssert, not) {
			t.Errorf("no_assertions flags %s, which asserts", not)
		}
	}
}

// Issue #221: a Rust result discarded with `let _ =` under a comment that says
// it is logged is the discarded_error finding, as Go's `_ =` is.
func TestARustDiscardUnderALoggedCommentIsFound(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"src/io.rs": "fn save() {\n    // the error is logged by the caller\n    let _ = file.sync_all();\n}\n"})
	got := checksOf(Scan(context.Background(), ScanInput{Root: root, Changed: []string{"src/io.rs"},
		Added: allAdded(t, root, "src/io.rs"), Today: time.Now()}))
	if len(got[CheckDiscardedError]) != 1 {
		t.Errorf("discarded_error = %v", got[CheckDiscardedError])
	}
}

// Issue #221: the language's formatter is run over the touched files when it
// is installed — rustfmt, ruff (or black), prettier — as gofmt is for Go.
func TestTheLanguagesFormatterIsRunWhenInstalled(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{"src/lib.rs": "fn a(){}\n", "app/main.py": "x=1\n", "web/a.ts": "let x=1\n"})
	var ran []string
	r := func(_ context.Context, _ string, argv []string, _ ...string) (string, int, error) {
		cmd := strings.Join(argv, " ")
		ran = append(ran, cmd)
		switch {
		case strings.HasSuffix(cmd, "--version"):
			if argv[0] == "black" {
				return "", 127, nil // not installed: ruff is
			}
			return argv[0] + " 1.0", 0, nil
		case argv[0] == "rustfmt":
			return fmt.Sprintf("Diff in %s:1:\n-fn a(){}\n+fn a() {}\n", filepath.Join(root, "src/lib.rs")), 1, nil
		case argv[0] == "ruff":
			return "Would reformat: app/main.py\n1 file would be reformatted\n", 1, nil
		case argv[0] == "prettier":
			return "Checking formatting...\n[warn] web/a.ts\n[warn] Code style issues found in the above file.\n", 1, nil
		}
		return "", 0, nil
	}
	files := []string{"src/lib.rs", "app/main.py", "web/a.ts"}
	got := checksOf(Scan(context.Background(), ScanInput{Root: root, Changed: files, Added: allAdded(t, root, files...),
		Today: time.Now(), Runner: r}))
	if f := strings.Join(got[CheckFormat], " "); !strings.Contains(f, "src/lib.rs") || !strings.Contains(f, "app/main.py") ||
		!strings.Contains(f, "web/a.ts") {
		t.Errorf("format findings = %v (ran %v)", got[CheckFormat], ran)
	}

	// Not installed: no finding, nothing claimed.
	none := func(_ context.Context, _ string, argv []string, _ ...string) (string, int, error) {
		return "", 127, nil
	}
	if f := Scan(context.Background(), ScanInput{Root: root, Changed: files, Added: allAdded(t, root, files...),
		Today: time.Now(), Runner: none}); len(checksOf(f)[CheckFormat]) != 0 {
		t.Errorf("a formatter that is not installed reported %v", f)
	}
}

// Issue #221: the review's file:line examples are not a Go path, so a
// reviewer of a Python or TypeScript change is not shown Go to copy.
func TestTheReviewExamplesAreLanguageNeutral(t *testing.T) {
	text := ReviewSystemPrompt + SubmitReviewTool("", ReviewScope{}, nil).Description
	if b, err := json.Marshal(reviewSchema()); err == nil {
		text += string(b)
	}
	for _, goish := range []string{"internal/agentrun/phase.go", "root_test.go:NN", "phase.go:480"} {
		if strings.Contains(text, goish) {
			t.Errorf("the review still shows the Go example %q", goish)
		}
	}
}
