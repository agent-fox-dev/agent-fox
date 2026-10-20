package triage

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// treeFiles are the files of the test tree and their line counts.
var treeFiles = map[string]int{
	"a/b.go": 20, "c:5": 3, "main.go": 10, "pkg/inner.go": 4,
	"w/util.go": 1, "x/util.go": 1, "y/util.go": 1, "z/util.go": 1,
	"README.md": 5, "empty.txt": 0,
}

const treeRoot = "/work/repo"

func testTree() Tree {
	var files []string
	for name := range treeFiles {
		files = append(files, name)
	}
	slices.Sort(files)
	return Tree{
		Roots: []string{treeRoot},
		Files: files,
		Lines: func(path string) (int, error) {
			n, ok := treeFiles[path]
			if !ok {
				return 0, fmt.Errorf("%s is not a file", path)
			}
			return n, nil
		},
	}
}

// valid is a diagnosis that has no problem, as the map a brain would send.
func valid() map[string]any {
	return map[string]any{
		"title":      "Login fails on submit",
		"severity":   "high",
		"confidence": "probable",
		"root_cause": "The handler dereferences a nil session.",
		"affected_files": []any{
			map[string]any{"path": "main.go", "line": 3, "note": "the handler"},
			map[string]any{"path": "a/b.go", "note": "the session"},
			map[string]any{"path": "README.md", "line": 5, "note": "the docs"},
		},
		"suggested_fix":       "Create the session first.",
		"acceptance_criteria": []any{"The login form returns 200", "No panic is logged"},
	}
}

// with returns valid() changed by f.
func with(f func(m map[string]any)) map[string]any {
	m := valid()
	f(m)
	return m
}

func payloadOf(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func pointers(problems []Problem) []string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Pointer)
	}
	return out
}

func repeat(s string, n int) string { return strings.Repeat(s, n) }

func files(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"path": "main.go", "note": "n"}
	}
	return out
}

func criteria(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = fmt.Sprintf("criterion %d", i)
	}
	return out
}

// question is a question the validator accepts with confidence suspected.
func question(text string, options ...any) map[string]any {
	q := map[string]any{"text": text}
	if options != nil {
		q["options"] = options
	}
	return q
}

// TestEachBrokenFieldRuleIsOneProblemWithAPointer_TS_08_32 verifies TS-08-32:
// each broken field rule yields one problem with a JSON pointer, a valid
// diagnosis none, and several broken rules are all reported together.
// Verifies: 08-REQ-5.2
func TestEachBrokenFieldRuleIsOneProblemWithAPointer_TS_08_32(t *testing.T) {
	tree := testTree()
	if got, problems := Validate(payloadOf(t, valid()), tree); len(problems) != 0 || got.Title != "Login fails on submit" {
		t.Fatalf("a valid diagnosis: %+v, %v", got, problems)
	}

	cases := []struct {
		name    string
		payload any
		pointer string
	}{
		{"an unknown field", with(func(m map[string]any) { m["extra"] = 1 }), "/extra"},
		{"no title", with(func(m map[string]any) { delete(m, "title") }), "/title"},
		{"an empty title", with(func(m map[string]any) { m["title"] = "" }), "/title"},
		{"a title of two lines", with(func(m map[string]any) { m["title"] = "a\nb" }), "/title"},
		{"a title of 121 characters", with(func(m map[string]any) { m["title"] = repeat("é", 121) }), "/title"},
		{"a title that is not a string", with(func(m map[string]any) { m["title"] = 7 }), "/title"},
		{"a severity outside the four", with(func(m map[string]any) { m["severity"] = "urgent" }), "/severity"},
		{"no severity", with(func(m map[string]any) { delete(m, "severity") }), "/severity"},
		{"a confidence outside the four", with(func(m map[string]any) { m["confidence"] = "sure" }), "/confidence"},
		{"an empty root cause", with(func(m map[string]any) { m["root_cause"] = "" }), "/root_cause"},
		{"a root cause of 6001 bytes", with(func(m map[string]any) { m["root_cause"] = repeat("x", 6001) }), "/root_cause"},
		{"31 affected files", with(func(m map[string]any) { m["affected_files"] = files(31) }), "/affected_files"},
		{"no affected files", with(func(m map[string]any) { m["affected_files"] = []any{} }), "/affected_files"},
		{"a note of 301 bytes", with(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "main.go", "note": repeat("n", 301)}}
		}), "/affected_files/0/note"},
		{"no note", with(func(m map[string]any) { m["affected_files"] = []any{map[string]any{"path": "main.go"}} }), "/affected_files/0/note"},
		{"line 0", with(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "main.go", "line": 0, "note": "n"}}
		}), "/affected_files/0/line"},
		{"a line past the end of the file", with(func(m map[string]any) {
			m["affected_files"] = []any{
				map[string]any{"path": "main.go", "line": 10, "note": "n"},
				map[string]any{"path": "a/b.go", "line": 20, "note": "n"},
				map[string]any{"path": "main.go", "line": 11, "note": "n"},
			}
		}), "/affected_files/2/line"},
		{"a line that is not an integer", with(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "main.go", "line": 1.5, "note": "n"}}
		}), "/affected_files/0/line"},
		{"an unknown field of an affected file", with(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "main.go", "note": "n", "why": "x"}}
		}), "/affected_files/0/why"},
		{"no suggested fix", with(func(m map[string]any) { m["suggested_fix"] = "" }), "/suggested_fix"},
		{"a suggested fix of 6001 bytes", with(func(m map[string]any) { m["suggested_fix"] = repeat("x", 6001) }), "/suggested_fix"},
		{"no criteria", with(func(m map[string]any) { m["acceptance_criteria"] = []any{} }), "/acceptance_criteria"},
		{"16 criteria", with(func(m map[string]any) { m["acceptance_criteria"] = criteria(16) }), "/acceptance_criteria"},
		{"an empty criterion", with(func(m map[string]any) { m["acceptance_criteria"] = []any{"fine", ""} }), "/acceptance_criteria/1"},
		{"a criterion of 301 bytes", with(func(m map[string]any) { m["acceptance_criteria"] = []any{repeat("c", 301)} }), "/acceptance_criteria/0"},
		{"an empty question text", with(func(m map[string]any) {
			m["confidence"], m["question"] = "suspected", question("", "A", "B")
		}), "/question/text"},
		{"a question text of 2001 bytes", with(func(m map[string]any) {
			m["confidence"], m["question"] = "suspected", question(repeat("q", 2001))
		}), "/question/text"},
		{"one option", with(func(m map[string]any) {
			m["confidence"], m["question"] = "suspected", question("Which?", "only")
		}), "/question/options"},
		{"seven options", with(func(m map[string]any) {
			m["confidence"], m["question"] = "suspected", question("Which?", "1", "2", "3", "4", "5", "6", "7")
		}), "/question/options"},
		{"an option of 201 bytes", with(func(m map[string]any) {
			m["confidence"], m["question"] = "suspected", question("Which?", "fine", repeat("o", 201))
		}), "/question/options/1"},
		{"a question with confidence confirmed", with(func(m map[string]any) {
			m["confidence"], m["question"] = "confirmed", question("Which?", "A", "B")
		}), "/question"},
		{"a question with confidence probable", with(func(m map[string]any) { m["question"] = question("Which?", "A", "B") }), "/question"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, problems := Validate(payloadOf(t, tc.payload), tree)

			if len(problems) != 1 || problems[0].Pointer != tc.pointer {
				t.Errorf("problems = %v, want exactly one, at %s", problems, tc.pointer)
			}
		})
	}

	t.Run("a payload of 65537 bytes", func(t *testing.T) {
		big := payloadOf(t, with(func(m map[string]any) { m["root_cause"] = repeat("x", 65537) }))
		_, problems := Validate(big, tree)
		if len(problems) != 1 || problems[0].Pointer != "" || !strings.Contains(problems[0].Message, "65536") {
			t.Errorf("problems = %v, want one for the payload as a whole, naming 65536", problems)
		}
	})
	for name, raw := range map[string]string{"an array": `[1]`, "a string": `"x"`, "null": `null`, "a number": `42`, "text": `oops`, "two objects": `{} {}`, "a cut object": `{"title":`} {
		t.Run(name+" is not a diagnosis", func(t *testing.T) {
			_, problems := Validate([]byte(raw), tree)
			if len(problems) != 1 || problems[0].Pointer != "" {
				t.Errorf("problems = %v, want one for the payload as a whole", problems)
			}
		})
	}

	t.Run("several broken rules are all reported", func(t *testing.T) {
		_, problems := Validate(payloadOf(t, with(func(m map[string]any) {
			m["title"], m["severity"], m["confidence"], m["suggested_fix"] = "", "urgent", "sure", ""
			m["extra"] = true
		})), tree)
		want := []string{"/confidence", "/extra", "/severity", "/suggested_fix", "/title"}
		if got := pointers(problems); !slices.Equal(got, want) {
			t.Errorf("pointers = %v, want %v, sorted", got, want)
		}
	})
}

// TestNotReproducibleOrAQuestionRelaxesOnlyTheLists_TS_08_33 verifies TS-08-33:
// not_reproducible or a question lets the three lists be empty, and nothing else.
// Verifies: 08-REQ-5.3
func TestNotReproducibleOrAQuestionRelaxesOnlyTheLists_TS_08_33(t *testing.T) {
	tree := testTree()
	empty := func(m map[string]any) {
		delete(m, "affected_files")
		m["suggested_fix"] = ""
		m["acceptance_criteria"] = []any{}
	}
	for name, tc := range map[string]struct {
		payload map[string]any
		want    []string
	}{
		"not_reproducible with empty lists": {with(func(m map[string]any) { empty(m); m["confidence"] = "not_reproducible" }), nil},
		"suspected with a question and empty lists": {with(func(m map[string]any) {
			empty(m)
			m["confidence"], m["question"] = "suspected", question("Which?", "A", "B")
		}), nil},
		"not_reproducible with a question": {with(func(m map[string]any) {
			empty(m)
			m["confidence"], m["question"] = "not_reproducible", question("Which?")
		}), nil},
		"suspected with no question and empty lists": {with(func(m map[string]any) { empty(m); m["confidence"] = "suspected" }),
			[]string{"/acceptance_criteria", "/affected_files", "/suggested_fix"}},
		"not_reproducible with a bad severity": {with(func(m map[string]any) {
			empty(m)
			m["confidence"], m["severity"] = "not_reproducible", "urgent"
		}), []string{"/severity"}},
		"not_reproducible with lists that break their rules": {with(func(m map[string]any) {
			m["confidence"] = "not_reproducible"
			m["acceptance_criteria"] = []any{repeat("c", 301)}
		}), []string{"/acceptance_criteria/0"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := Validate(payloadOf(t, tc.payload), tree)
			if got := pointers(problems); !slices.Equal(got, tc.want) {
				t.Errorf("pointers = %v, want %v", got, tc.want)
			}
		})
	}
}

// onePath validates a diagnosis that cites path (with line, when it is not 0)
// and returns the normalized file and the problems.
func onePath(t *testing.T, path string, line int) (AffectedFile, []Problem) {
	t.Helper()
	file := map[string]any{"path": path, "note": "n"}
	if line != 0 {
		file["line"] = line
	}
	got, problems := Validate(payloadOf(t, with(func(m map[string]any) { m["affected_files"] = []any{file} })), testTree())
	if len(problems) != 0 {
		return AffectedFile{}, problems
	}
	return got.AffectedFiles[0], nil
}

// TestPathFormsResolveToTheCleanedPathAndLine_TS_08_34 verifies TS-08-34:
// ./x, an absolute path under the tree and path:N resolve to the cleaned path
// and the line, and the colon form is tried only when it is needed.
// Verifies: 08-REQ-5.4
func TestPathFormsResolveToTheCleanedPathAndLine_TS_08_34(t *testing.T) {
	for name, tc := range map[string]struct {
		path     string
		line     int
		wantPath string
		wantLine int
	}{
		"a path as it is":                  {"a/b.go", 0, "a/b.go", 0},
		"a leading ./":                     {"./a/b.go", 0, "a/b.go", 0},
		"an absolute path under the tree":  {treeRoot + "/a/b.go", 0, "a/b.go", 0},
		"backslashes":                      {`a\b.go`, 0, "a/b.go", 0},
		"doubled slashes":                  {"./a//b.go", 0, "a/b.go", 0},
		"path:N":                           {"a/b.go:12", 0, "a/b.go", 12},
		"./path:N":                         {"./a/b.go:20", 0, "a/b.go", 20},
		"a file whose name is path:N":      {"c:5", 0, "c:5", 0},
		"a file named path:N, with a line": {"c:5", 2, "c:5", 2},
	} {
		t.Run(name, func(t *testing.T) {
			got, problems := onePath(t, tc.path, tc.line)
			if len(problems) != 0 {
				t.Fatalf("problems = %v", problems)
			}
			if got.Path != tc.wantPath || got.Line != tc.wantLine {
				t.Errorf("normalized = %s:%d, want %s:%d", got.Path, got.Line, tc.wantPath, tc.wantLine)
			}
		})
	}

	t.Run("with a line already given the colon form is not split", func(t *testing.T) {
		_, problems := onePath(t, "a/b.go:12", 3)
		if len(problems) != 1 || problems[0].Pointer != "/affected_files/0/path" || !problems[0].Unresolved {
			t.Errorf("problems = %v, want the path unresolved", problems)
		}
	})
	t.Run("a line taken from the colon form is held to the file", func(t *testing.T) {
		for _, path := range []string{"a/b.go:21", "a/b.go:0"} {
			_, problems := onePath(t, path, 0)
			if len(problems) != 1 || problems[0].Pointer != "/affected_files/0/line" {
				t.Errorf("%s: problems = %v, want one at the line", path, problems)
			}
		}
	})
}

// TestUnresolvablePathsAreReportedWithSuggestions_TS_08_35 verifies TS-08-35:
// each path that is not a file of the tree is a problem at its pointer, naming
// the path and up to three files with the same base name.
// Verifies: 08-REQ-5.5
func TestUnresolvablePathsAreReportedWithSuggestions_TS_08_35(t *testing.T) {
	tree := testTree()
	bad := []string{"/etc/passwd", "../x/util.go", "a/../main.go", ".git/config", ".agent-fox/run/x", "pkg", "missing/util.go", "nowhere.go", "pkg/", "/work/other/main.go"}
	items := make([]any, 0, len(bad)+1)
	for _, p := range bad {
		items = append(items, map[string]any{"path": p, "note": "n"})
	}
	items = append(items, map[string]any{"path": "main.go", "note": "fine"})

	_, problems := Validate(payloadOf(t, with(func(m map[string]any) { m["affected_files"] = items })), tree)

	byPointer := map[string]Problem{}
	for _, p := range problems {
		byPointer[p.Pointer] = p
	}
	if len(problems) != len(bad) {
		t.Fatalf("%d problems for %d bad paths and one good: %v", len(problems), len(bad), problems)
	}
	for i, p := range bad {
		pr, ok := byPointer[fmt.Sprintf("/affected_files/%d/path", i)]
		if !ok {
			t.Errorf("no problem at /affected_files/%d/path for %q", i, p)
			continue
		}
		if !pr.Unresolved || pr.Path != p || !strings.Contains(pr.Message, fmt.Sprintf("%q", p)) {
			t.Errorf("problem for %q = %+v, want it unresolved and naming the path", p, pr)
		}
	}
	missing := byPointer["/affected_files/6/path"]
	suggested := 0
	for _, s := range []string{"w/util.go", "x/util.go", "y/util.go", "z/util.go"} {
		if strings.Contains(missing.Message, s) {
			suggested++
		}
	}
	if suggested != 3 {
		t.Errorf("missing/util.go names %d of the 4 files with that base name, want 3: %s", suggested, missing.Message)
	}
	if !strings.Contains(byPointer["/affected_files/5/path"].Message, "directory") {
		t.Errorf("pkg: %s, want it called a directory", byPointer["/affected_files/5/path"].Message)
	}
}

// TestNULBytesMarkerTextAndAnOversizedRenderingAreReported_TS_08_36 verifies TS-08-36:
// a string with a NUL byte or the text <!-- agent-fox: is a problem at its
// pointer, and a rendering over 16384 bytes is a problem with both numbers.
// Verifies: 08-REQ-5.6
func TestNULBytesMarkerTextAndAnOversizedRenderingAreReported_TS_08_36(t *testing.T) {
	tree := testTree()
	marker := "<!-- agent-fox:original-report:end -->"
	for name, tc := range map[string]struct {
		payload map[string]any
		pointer string
	}{
		"a NUL in a note": {with(func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "main.go", "note": "a\x00b"}}
		}), "/affected_files/0/note"},
		"the marker in the root cause": {with(func(m map[string]any) { m["root_cause"] = "see " + marker }), "/root_cause"},
		"the marker in an option": {with(func(m map[string]any) {
			m["confidence"], m["question"] = "suspected", question("Which?", "A", marker)
		}), "/question/options/1"},
		"a NUL in the title":         {with(func(m map[string]any) { m["title"] = "a\x00" }), "/title"},
		"the marker in a criterion":  {with(func(m map[string]any) { m["acceptance_criteria"] = []any{"x <!-- agent-fox: y"} }), "/acceptance_criteria/0"},
		"the marker in the fix":      {with(func(m map[string]any) { m["suggested_fix"] = marker }), "/suggested_fix"},
		"the marker in the question": {with(func(m map[string]any) { m["confidence"], m["question"] = "suspected", question(marker) }), "/question/text"},
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := Validate(payloadOf(t, tc.payload), tree)
			if len(problems) != 1 || problems[0].Pointer != tc.pointer {
				t.Errorf("problems = %v, want one at %s", problems, tc.pointer)
			}
		})
	}

	t.Run("a rendering over 16384 bytes within the field caps", func(t *testing.T) {
		big := with(func(m map[string]any) {
			m["root_cause"] = repeat("r", 6000)
			m["suggested_fix"] = repeat("f", 6000)
			var items []any
			for range 30 {
				items = append(items, map[string]any{"path": "main.go", "note": repeat("n", 300)})
			}
			m["affected_files"] = items
		})
		_, problems := Validate(payloadOf(t, big), tree)
		if len(problems) != 1 || problems[0].Pointer != "" || !strings.Contains(problems[0].Message, "16384") {
			t.Fatalf("problems = %v, want one that names 16384", problems)
		}
		var got int
		if _, err := fmt.Sscanf(problems[0].Message, "the rendered diagnosis is %d bytes", &got); err != nil || got <= MaxRenderedBytes {
			t.Errorf("message %q: want the rendered byte count (over 16384) first", problems[0].Message)
		}
	})
}

// TestTheNormalizedFormIsStable guards 08-REQ-5.8: the form that is stored has
// the paths cleaned and the options never null, and validating it again gives
// the same diagnosis.
func TestTheNormalizedFormIsStable(t *testing.T) {
	tree := testTree()
	in := with(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "./a//b.go:12", "note": "n"}, map[string]any{"path": treeRoot + "/main.go", "line": 2, "note": "m"}}
		m["confidence"], m["question"] = "suspected", question("Which?")
	})
	first, problems := Validate(payloadOf(t, in), tree)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if first.AffectedFiles[0] != (AffectedFile{Path: "a/b.go", Line: 12, Note: "n"}) || first.AffectedFiles[1] != (AffectedFile{Path: "main.go", Line: 2, Note: "m"}) {
		t.Errorf("affected files = %+v", first.AffectedFiles)
	}
	if first.Question == nil || first.Question.Options == nil || len(first.Question.Options) != 0 {
		t.Errorf("question = %+v, want options as an empty list", first.Question)
	}
	second, problems := Validate(payloadOf(t, first), tree)
	if len(problems) != 0 || !equalDiagnosis(first, second) {
		t.Errorf("validating the normalized form again: %v, %+v", problems, second)
	}
}

func equalDiagnosis(a, b Diagnosis) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
