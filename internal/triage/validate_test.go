package triage

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// oneFile sets the affected files to the one file f.
func oneFile(f map[string]any) func(m map[string]any) {
	return func(m map[string]any) { m["affected_files"] = []any{f} }
}

func TestValidate_AcceptsAValidDiagnosis(t *testing.T) {
	d, problems := Validate(payloadOf(t, valid()), testTree())

	want := Diagnosis{
		Title: "Login fails on submit", Severity: "high", Confidence: "probable", RootCause: "The handler dereferences a nil session.",
		AffectedFiles:      []AffectedFile{{"main.go", 3, "the handler"}, {"a/b.go", 0, "the session"}, {"README.md", 5, "the docs"}},
		SuggestedFix:       "Create the session first.",
		AcceptanceCriteria: []string{"The login form returns 200", "No panic is logged"},
	}
	if len(problems) != 0 || string(payloadOf(t, d)) != string(payloadOf(t, want)) {
		t.Errorf("Validate = %+v, %v, want %+v and no problem", d, problems, want)
	}
}

// Each broken rule of a field is one problem, at the JSON pointer of the field.
func TestValidate_EachBrokenRuleIsOneProblemAtItsPointer(t *testing.T) {
	cases := []struct {
		name    string
		change  func(m map[string]any)
		pointer string
	}{
		{"an unknown field", func(m map[string]any) { m["extra"] = 1 }, "/extra"},
		{"an unknown field with a slash", func(m map[string]any) { m["a/b~c"] = 1 }, "/a~1b~0c"},
		{"no title", func(m map[string]any) { delete(m, "title") }, "/title"},
		{"an empty title", func(m map[string]any) { m["title"] = " " }, "/title"},
		{"a title of two lines", func(m map[string]any) { m["title"] = "a\nb" }, "/title"},
		{"a title that is not a string", func(m map[string]any) { m["title"] = true }, "/title"},
		{"a severity outside the four", func(m map[string]any) { m["severity"] = "urgent" }, "/severity"},
		{"no severity", func(m map[string]any) { delete(m, "severity") }, "/severity"},
		{"a severity that is not a string", func(m map[string]any) { m["severity"] = 3 }, "/severity"},
		{"a confidence outside the four", func(m map[string]any) { m["confidence"] = "sure" }, "/confidence"},
		{"no root cause", func(m map[string]any) { m["root_cause"] = nil }, "/root_cause"},
		{"an empty root cause", func(m map[string]any) { m["root_cause"] = "" }, "/root_cause"},
		{"a root cause that is not a string", func(m map[string]any) { m["root_cause"] = []any{} }, "/root_cause"},
		{"no affected files", func(m map[string]any) { delete(m, "affected_files") }, "/affected_files"},
		{"an empty list of affected files", func(m map[string]any) { m["affected_files"] = []any{} }, "/affected_files"},
		{"affected files that are no list", func(m map[string]any) { m["affected_files"] = "main.go" }, "/affected_files"},
		{"an affected file that is no object", func(m map[string]any) { m["affected_files"] = []any{"main.go"} }, "/affected_files/0"},
		{"an affected file with no path", oneFile(map[string]any{"note": "n"}), "/affected_files/0/path"},
		{"a path that is not a string", oneFile(map[string]any{"path": 1, "note": "n"}), "/affected_files/0/path"},
		{"no note", oneFile(map[string]any{"path": "main.go"}), "/affected_files/0/note"},
		{"a note that is not a string", oneFile(map[string]any{"path": "main.go", "note": 1}), "/affected_files/0/note"},
		{"line 0", oneFile(map[string]any{"path": "main.go", "line": 0, "note": "n"}), "/affected_files/0/line"},
		{"a line that is not an integer", oneFile(map[string]any{"path": "main.go", "line": 1.5, "note": "n"}), "/affected_files/0/line"},
		{"a line too large for an integer", oneFile(map[string]any{"path": "main.go", "line": json.Number("1e30"), "note": "n"}), "/affected_files/0/line"},
		{"a line that is a string", oneFile(map[string]any{"path": "main.go", "line": "3", "note": "n"}), "/affected_files/0/line"},
		{"a line past the end of the file", func(m map[string]any) {
			m["affected_files"] = []any{map[string]any{"path": "main.go", "line": 10, "note": "n"}, map[string]any{"path": "main.go", "line": 11, "note": "n"}}
		}, "/affected_files/1/line"},
		{"an unknown field of an affected file", oneFile(map[string]any{"path": "main.go", "note": "n", "why": "x"}), "/affected_files/0/why"},
		{"no suggested fix", func(m map[string]any) { m["suggested_fix"] = "" }, "/suggested_fix"},
		{"no criteria", func(m map[string]any) { delete(m, "acceptance_criteria") }, "/acceptance_criteria"},
		{"an empty list of criteria", func(m map[string]any) { m["acceptance_criteria"] = []any{} }, "/acceptance_criteria"},
		{"criteria that are no list", func(m map[string]any) { m["acceptance_criteria"] = "works" }, "/acceptance_criteria"},
		{"a criterion that is not a string", func(m map[string]any) { m["acceptance_criteria"] = []any{"fine", 2} }, "/acceptance_criteria/1"},
		{"an empty criterion", func(m map[string]any) { m["acceptance_criteria"] = []any{"fine", ""} }, "/acceptance_criteria/1"},
		{"a question that is a string", func(m map[string]any) { m["confidence"], m["question"] = "suspected", "Which?" }, "/question"},
		{"an empty question text", suspecting(question("", "A", "B")), "/question/text"},
		{"options that are no list", suspecting(map[string]any{"text": "Which?", "options": "A or B"}), "/question/options"},
		{"an option that is not a string", suspecting(question("Which?", "A", false)), "/question/options/1"},
		{"one option", suspecting(question("Which?", "only")), "/question/options"},
		{"a question with confidence confirmed", func(m map[string]any) { m["confidence"], m["question"] = "confirmed", question("Which?", "A", "B") }, "/question"},
		{"a question with confidence probable", func(m map[string]any) { m["question"] = question("Which?", "A", "B") }, "/question"},
	}
	for _, tc := range cases {
		_, problems := Validate(payloadOf(t, with(tc.change)), testTree())

		if len(problems) != 1 || problems[0].Pointer != tc.pointer {
			t.Errorf("%s: problems = %v, want exactly one, at %s", tc.name, problems, tc.pointer)
		}
	}
}

// A diagnosis exactly at a limit is accepted, and one a unit over is refused
// with the one problem; lengths count in characters where the rule says
// characters and in bytes where it says bytes.
func TestValidate_EveryLimitHoldsAtTheLimitAndNoFurther(t *testing.T) {
	for _, tc := range []struct {
		name     string
		at, over func(m map[string]any)
		pointer  string
	}{
		{"the title, in characters", func(m map[string]any) { m["title"] = repeat("é", 120) }, func(m map[string]any) { m["title"] = repeat("é", 121) }, "/title"},
		{"the root cause, in bytes", func(m map[string]any) { m["root_cause"] = repeat("x", 6000) }, func(m map[string]any) { m["root_cause"] = repeat("x", 6001) }, "/root_cause"},
		{"the suggested fix, in bytes", func(m map[string]any) { m["suggested_fix"] = repeat("x", 6000) }, func(m map[string]any) { m["suggested_fix"] = repeat("x", 6001) }, "/suggested_fix"},
		{"the affected files", func(m map[string]any) { m["affected_files"] = items(30, mainGo) }, func(m map[string]any) { m["affected_files"] = items(31, mainGo) }, "/affected_files"},
		{"a note", oneFile(map[string]any{"path": "main.go", "note": repeat("n", 300)}), oneFile(map[string]any{"path": "main.go", "note": repeat("n", 301)}), "/affected_files/0/note"},
		{"the criteria", func(m map[string]any) { m["acceptance_criteria"] = items(15, criterion) }, func(m map[string]any) { m["acceptance_criteria"] = items(16, criterion) }, "/acceptance_criteria"},
		{"a criterion", func(m map[string]any) { m["acceptance_criteria"] = []any{repeat("c", 300)} }, func(m map[string]any) { m["acceptance_criteria"] = []any{repeat("c", 301)} }, "/acceptance_criteria/0"},
		{"the question text", suspecting(question(repeat("q", 2000))), suspecting(question(repeat("q", 2001))), "/question/text"},
		{"the options", suspecting(question("Which?", "1", "2", "3", "4", "5", "6")), suspecting(question("Which?", "1", "2", "3", "4", "5", "6", "7")), "/question/options"},
		{"an option", suspecting(question("Which?", "a", repeat("o", 200))), suspecting(question("Which?", "a", repeat("o", 201))), "/question/options/1"},
		{"the fields of a question", suspecting(question("Which?", "a", "b")), suspecting(map[string]any{"text": "Which?", "options": []any{"a", "b"}, "hint": "x"}), "/question/hint"},
	} {
		if _, problems := Validate(payloadOf(t, with(tc.at)), testTree()); len(problems) != 0 {
			t.Errorf("%s at the limit: problems = %v, want none", tc.name, problems)
		}
		if _, problems := Validate(payloadOf(t, with(tc.over)), testTree()); len(problems) != 1 || problems[0].Pointer != tc.pointer {
			t.Errorf("%s over the limit: problems = %v, want exactly one, at %s", tc.name, problems, tc.pointer)
		}
	}

	at := payloadOf(t, valid())
	at = append(at[:len(at)-1], []byte(repeat(" ", MaxPayloadBytes-len(at))+"}")...) // white space between tokens
	if _, problems := Validate(at, testTree()); len(problems) != 0 {
		t.Errorf("a payload of %d bytes: problems = %v, want none", len(at), problems)
	}
	if _, problems := Validate(append(at[:len(at)-1], []byte(" }")...), testTree()); len(problems) != 1 || problems[0].Pointer != "" || !strings.Contains(problems[0].Message, "65536") {
		t.Errorf("a payload of 65537 bytes: problems = %v, want one for the whole, naming 65536", problems)
	}
}

func TestValidate_APayloadThatIsNoObjectIsOneProblemForTheWhole(t *testing.T) {
	for _, raw := range []string{`[1]`, `"x"`, `null`, `42`, `true`, `oops`, `{} {}`, `{"title":`} {
		if _, problems := Validate([]byte(raw), testTree()); len(problems) != 1 || problems[0].Pointer != "" {
			t.Errorf("%s: problems = %v, want one for the payload as a whole", raw, problems)
		}
	}
}

// Every problem is reported at once, sorted by pointer and then by message,
// and a payload of nothing but mistakes is answered with at most fifty and a
// line for the rest.
func TestValidate_ReportsEveryProblemSortedAndAtMostFifty(t *testing.T) {
	_, problems := Validate(payloadOf(t, with(func(m map[string]any) {
		m["title"], m["severity"], m["confidence"], m["suggested_fix"], m["extra"] = "", "urgent", "sure", "", true
		m["affected_files"] = []any{map[string]any{"path": "main.go", "note": repeat("n", 300) + markerText}}
	})), testTree())

	want := []string{"/affected_files/0/note", "/affected_files/0/note", "/confidence", "/extra", "/severity", "/suggested_fix", "/title"}
	if got := pointers(problems); !slices.Equal(got, want) || problems[0].Message > problems[1].Message {
		t.Errorf("problems = %v, want %v, sorted", problems, want)
	}

	_, problems = Validate(payloadOf(t, with(func(m map[string]any) {
		empty := items(200, func(int) any { return map[string]any{} })
		m["affected_files"], m["acceptance_criteria"], m["extra"] = empty[:30], empty[:15], empty
	})), testTree())

	if last := problems[len(problems)-1]; len(problems) != 51 || last.Pointer != "" || !strings.HasPrefix(last.Message, "and ") {
		t.Errorf("%d problems ending in %+v, want 50 and a line that says how many more there are", len(problems), last)
	}
}

// not_reproducible or a question lets the three lists be empty, and nothing
// else.
func TestValidate_NotReproducibleOrAQuestionRelaxesOnlyTheLists(t *testing.T) {
	empty := func(confidence string, q map[string]any) func(m map[string]any) {
		return func(m map[string]any) {
			delete(m, "affected_files")
			m["suggested_fix"], m["acceptance_criteria"], m["confidence"] = "", []any{}, confidence
			if q != nil {
				m["question"] = q
			}
		}
	}
	for name, tc := range map[string]struct {
		change func(m map[string]any)
		want   []string
	}{
		"not_reproducible with empty lists":          {empty("not_reproducible", nil), nil},
		"suspected with a question and empty lists":  {empty("suspected", question("Which?", "A", "B")), nil},
		"not_reproducible with a question":           {empty("not_reproducible", question("Which?")), nil},
		"suspected with no question and empty lists": {empty("suspected", nil), []string{"/acceptance_criteria", "/affected_files", "/suggested_fix"}},
		"not_reproducible with a bad severity": {func(m map[string]any) {
			empty("not_reproducible", nil)(m)
			m["severity"] = "urgent"
		}, []string{"/severity"}},
		"not_reproducible with a fix that is no string": {func(m map[string]any) {
			empty("not_reproducible", nil)(m)
			m["suggested_fix"] = 1
		}, []string{"/suggested_fix"}},
		"not_reproducible with lists that break their rules": {func(m map[string]any) {
			m["confidence"], m["acceptance_criteria"] = "not_reproducible", []any{repeat("c", 301)}
		}, []string{"/acceptance_criteria/0"}},
	} {
		if _, problems := Validate(payloadOf(t, with(tc.change)), testTree()); !slices.Equal(pointers(problems), tc.want) {
			t.Errorf("%s: problems = %v, want %v", name, problems, tc.want)
		}
	}
}

// onePath validates a diagnosis that cites path (with line, when it is not 0).
func onePath(t *testing.T, tree Tree, path string, line int) (AffectedFile, []Problem) {
	t.Helper()
	file := map[string]any{"path": path, "note": "n"}
	if line != 0 {
		file["line"] = line
	}
	d, problems := Validate(payloadOf(t, with(oneFile(file))), tree)
	if len(problems) != 0 {
		return AffectedFile{}, problems
	}
	return d.AffectedFiles[0], nil
}

// ./x, an absolute path under the tree, backslashes and path:N resolve to the
// cleaned path and the line, and the colon form is tried only when the path as
// given is no file and no line is given.
func TestValidate_ACitedPathIsCleanedAndItsLineSplitOff(t *testing.T) {
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
		got, problems := onePath(t, testTree(), tc.path, tc.line)
		if len(problems) != 0 || got.Path != tc.wantPath || got.Line != tc.wantLine {
			t.Errorf("%s: %s:%d (%v), want %s:%d", name, got.Path, got.Line, problems, tc.wantPath, tc.wantLine)
		}
	}

	for name, tc := range map[string]struct {
		path    string
		line    int
		pointer string
	}{
		"path:N with a line given":          {"a/b.go:12", 3, "/affected_files/0/path"},
		"path:N past the end of the file":   {"a/b.go:21", 0, "/affected_files/0/line"},
		"path:0":                            {"a/b.go:0", 0, "/affected_files/0/line"},
		"path:N with digits past an int64":  {"a/b.go:99999999999999999999", 0, "/affected_files/0/path"},
		"a file whose lines cannot be read": {"pkg/inner.go:1", 0, "/affected_files/0/line"},
	} {
		tree := testTree()
		lines := tree.Lines
		tree.Lines = func(p string) (int, error) {
			if p == "pkg/inner.go" {
				return 0, errors.New("unreadable")
			}
			return lines(p)
		}
		_, problems := onePath(t, tree, tc.path, tc.line)
		if len(problems) != 1 || problems[0].Pointer != tc.pointer {
			t.Errorf("%s: problems = %v, want one at %s", name, problems, tc.pointer)
		}
	}
}

// Each path that is not a file of the tree is a problem at its pointer that
// names the path, and up to three files with the same base name.
func TestValidate_APathThatIsNoFileIsUnresolvedWithSuggestions(t *testing.T) {
	bad := []string{"/etc/passwd", "../x/util.go", "a/../main.go", ".git/config", ".agent-fox/run/x", "pkg", "missing/util.go", "nowhere.go", "pkg/", "/work/other/main.go", treeRoot, "C:/work/main.go", "./"}
	files := items(len(bad), func(i int) any { return map[string]any{"path": bad[i], "note": "n"} })

	_, problems := Validate(payloadOf(t, with(func(m map[string]any) { m["affected_files"] = append(files, mainGo(0)) })), testTree())

	if len(problems) != len(bad) {
		t.Fatalf("%d problems for %d bad paths and one good: %v", len(problems), len(bad), problems)
	}
	byPointer := map[string]Problem{}
	for _, p := range problems {
		byPointer[p.Pointer] = p
	}
	for i, p := range bad {
		if pr := byPointer[fmt.Sprintf("/affected_files/%d/path", i)]; !pr.Unresolved || pr.Path != p || !strings.Contains(pr.Message, fmt.Sprintf("unresolved path %q", p)) {
			t.Errorf("problem for %q = %+v, want it unresolved and naming the path", p, pr)
		}
	}
	suggested := 0
	for _, s := range []string{"w/util.go", "x/util.go", "y/util.go", "z/util.go"} {
		if strings.Contains(byPointer["/affected_files/6/path"].Message, s) {
			suggested++
		}
	}
	if suggested != 3 || !strings.Contains(byPointer["/affected_files/5/path"].Message, "directory") {
		t.Errorf("missing/util.go names %d of the 4 files with its base name, want 3; pkg is %q, want a directory", suggested, byPointer["/affected_files/5/path"].Message)
	}
}

// The file view lists a link whatever it leads to, and the directory of another
// repository as a file; neither is a file a diagnosis may cite.
func TestValidate_ALinkOrANestedRepositoryIsNoFileToCite(t *testing.T) {
	tree := testTree()
	tree.Files = append(tree.Files, "linkout", "nested")
	tree.Regular = func(p string) bool { return p != "linkout" && p != "nested" }
	for _, cited := range []string{"linkout", "./nested", "linkout:3"} {
		_, problems := onePath(t, tree, cited, 0)
		if len(problems) != 1 || problems[0].Pointer != "/affected_files/0/path" || !problems[0].Unresolved || !strings.Contains(problems[0].Message, "cite a file") {
			t.Errorf("%s: problems = %+v, want one unresolved path", cited, problems)
		}
	}
	if _, problems := Validate(payloadOf(t, valid()), tree); len(problems) != 0 {
		t.Errorf("the regular files can no longer be cited: %v", problems)
	}
}

// No string may hold a NUL byte or the text that starts the markers of a body.
func TestValidate_NoTextMayHoldANULOrTheMarkerText(t *testing.T) {
	marker := "<!-- agent-fox:original-report:end -->"
	for name, tc := range map[string]struct {
		change  func(m map[string]any)
		pointer string
	}{
		"a NUL in the title":           {func(m map[string]any) { m["title"] = "a\x00" }, "/title"},
		"a NUL in a note":              {oneFile(map[string]any{"path": "main.go", "note": "a\x00b"}), "/affected_files/0/note"},
		"the marker in a path":         {oneFile(map[string]any{"path": "main.go" + marker, "note": "n"}), "/affected_files/0/path"},
		"the marker in the root cause": {func(m map[string]any) { m["root_cause"] = "see " + marker }, "/root_cause"},
		"the marker in the fix":        {func(m map[string]any) { m["suggested_fix"] = marker }, "/suggested_fix"},
		"the marker in a criterion":    {func(m map[string]any) { m["acceptance_criteria"] = []any{"x <!-- agent-fox: y"} }, "/acceptance_criteria/0"},
		"the marker in the question":   {suspecting(question(marker)), "/question/text"},
		"the marker in an option":      {suspecting(question("Which?", "A", marker)), "/question/options/1"},
	} {
		_, problems := Validate(payloadOf(t, with(tc.change)), testTree())

		atPointer := len(problems) > 0 && !slices.ContainsFunc(problems, func(p Problem) bool { return p.Pointer != tc.pointer })
		if !atPointer || !slices.ContainsFunc(problems, func(p Problem) bool {
			return strings.Contains(p.Message, "a NUL byte") || strings.Contains(p.Message, markerText)
		}) {
			t.Errorf("%s: problems = %v, want the text refused at %s", name, problems, tc.pointer)
		}
	}
}

// The diagnosis as the body renders it may be 16384 bytes and no more, within
// the caps of its fields.
func TestValidate_TheRenderedDiagnosisMayBeExactly16384Bytes(t *testing.T) {
	notes := items(20, func(int) any { return map[string]any{"path": "main.go", "note": repeat("n", 300)} })
	payload := func(fix int) []byte {
		return payloadOf(t, with(func(m map[string]any) {
			m["root_cause"], m["affected_files"], m["suggested_fix"] = repeat("r", 6000), notes, repeat("f", fix)
		}))
	}
	d, _ := Validate(payload(1), testTree())
	exact := 1 + MaxRenderedBytes - renderedSize(d, placeholderRun) // the fix adds a byte for a byte

	if _, problems := Validate(payload(exact), testTree()); len(problems) != 0 {
		t.Errorf("a rendering of %d bytes: problems = %v, want none", MaxRenderedBytes, problems)
	}
	_, problems := Validate(payload(exact+1), testTree())
	if len(problems) != 1 || problems[0].Pointer != "" || !strings.Contains(problems[0].Message, fmt.Sprintf("is %d bytes, and the limit is 16384", MaxRenderedBytes+1)) {
		t.Errorf("a rendering of %d bytes: problems = %v, want one for the whole with both numbers", MaxRenderedBytes+1, problems)
	}
}

// The stored form has the paths cleaned and the lists never null, and
// validating it again gives the same diagnosis.
func TestValidate_TheNormalizedFormIsStable(t *testing.T) {
	first, problems := Validate(payloadOf(t, with(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "./a//b.go:12", "note": "n"}, map[string]any{"path": treeRoot + "/main.go", "line": 2, "note": "m"}}
		suspecting(question("Which?"))(m)
	})), testTree())
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if first.AffectedFiles[0] != (AffectedFile{"a/b.go", 12, "n"}) || first.AffectedFiles[1] != (AffectedFile{"main.go", 2, "m"}) || first.Question.Options == nil {
		t.Errorf("normalized = %+v, %+v", first.AffectedFiles, first.Question)
	}
	second, problems := Validate(payloadOf(t, first), testTree())
	if len(problems) != 0 || string(payloadOf(t, first)) != string(payloadOf(t, second)) {
		t.Errorf("validating the normalized form again: %v, %+v", problems, second)
	}
	bare, _ := Validate(payloadOf(t, with(func(m map[string]any) {
		delete(m, "affected_files")
		delete(m, "acceptance_criteria")
		m["confidence"] = "not_reproducible"
	})), testTree())
	if bare.AffectedFiles == nil || bare.AcceptanceCriteria == nil {
		t.Errorf("the lists of a bare diagnosis are null: %+v", bare)
	}
}

// A submission accepted in a box is checked against the host's tree once more,
// which may have changed while the brain ran.
func TestStaleReason_SaysWhyASubmissionNoLongerHoldsOnTheHost(t *testing.T) {
	payload := payloadOf(t, with(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "a.go", "line": 3, "note": "n"}, map[string]any{"path": "b.go", "note": "n"}}
	}))
	five := func(string) (int, error) { return 5, nil }

	if got := StaleReason(payload, Tree{Files: []string{"a.go", "b.go"}, Lines: five}); got != "" {
		t.Errorf("a tree that has the files: %q, want none", got)
	}
	for name, tree := range map[string]Tree{
		"files the host no longer has":  {Files: []string{"c.go"}},
		"a file that became shorter":    {Files: []string{"a.go"}, Lines: func(string) (int, error) { return 2, nil }},
		"a file that cannot be counted": {Files: []string{"a.go"}, Lines: func(string) (int, error) { return 0, errors.New("gone") }},
	} {
		if got := StaleReason(payload, tree); !strings.HasPrefix(got, "/affected_files/0/") {
			t.Errorf("%s: %q, want the problem with its pointer", name, got)
		}
	}
	if got := StaleReason(payload, Tree{Files: []string{"c.go"}}); !strings.Contains(got, "; /affected_files/1/path: ") {
		t.Errorf("two missing files: %q, want both, one after the other", got)
	}
}
