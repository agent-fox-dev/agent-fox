package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// submittedTree is the tree a report is submitted in: the tests a report may cite, and a
// directory where a test would be.
func submittedTree() fstest.MapFS {
	return fstest.MapFS{
		"tests/fix_test.sh": {Data: []byte("#!/bin/sh\n")},
		"src/a.go":          {Data: []byte("package a\n")},
		"tests/dir/x":       {Data: []byte("x")},
	}
}

// goodReport is a report that has no problem, as a brain sends it, changed by the edits.
func goodReport(edits ...func(m map[string]any)) map[string]any {
	m := map[string]any{
		"type":       "fix",
		"subject":    "Check the arguments before help runs",
		"root_cause": "Run calls Help before it looks at its arguments.",
		"change":     "Look at the arguments first.",
		"files": []any{
			map[string]any{"path": "src/a.go", "why": "the call order"},
			map[string]any{"path": "tests/fix_test.sh", "why": "the test"},
		},
		"tests":     []any{map[string]any{"path": "tests/fix_test.sh", "name": "no arguments", "proves": "the usage is printed"}},
		"criteria":  []any{map[string]any{"criterion": "no arguments prints the usage", "status": "met", "evidence": "tests/fix_test.sh"}},
		"decisions": []any{"kept Help as it was"},
	}
	for _, e := range edits {
		e(m)
	}
	return m
}

func validate(t *testing.T, v any) (json.RawMessage, []submission.Problem) {
	t.Helper()
	return FixReport.Validate(context.Background(), submittedTree(), testutil.JSON(t, v))
}

// pointers are the field pointers of the problems, in order.
func pointers(problems []submission.Problem) []string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Path)
	}
	return out
}

func question(text string, options ...any) map[string]any {
	q := map[string]any{"text": text}
	if options != nil {
		q["options"] = options
	}
	return map[string]any{"question": q}
}

func set(key string, v any) func(m map[string]any) { return func(m map[string]any) { m[key] = v } }

// setFile makes the files the one file {path, why} as edit leaves it.
func setFile(edit func(f map[string]any)) func(m map[string]any) {
	return func(m map[string]any) {
		f := map[string]any{"path": "src/a.go", "why": "w"}
		edit(f)
		m["files"] = []any{f}
	}
}

func filePath(p string) func(m map[string]any) {
	return setFile(func(f map[string]any) { f["path"] = p })
}

func setTest(p string) func(m map[string]any) {
	return set("tests", []any{map[string]any{"path": p, "proves": "p"}})
}

func items(n int, item func(i int) any) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = item(i)
	}
	return out
}

func x(n int) string { return strings.Repeat("x", n) }

func TestValidate_TakesAReportOrAQuestionAndNotBoth(t *testing.T) {
	for name, form := range map[string]any{
		"a report":                goodReport(),
		"a question":              question("Which session?", "old", "new"),
		"a question with no list": question("Which session?"),
		"a question and the analysis so far": map[string]any{"question": map[string]any{
			"text": "Which?", "options": []any{"a", "b"}, "root_cause": "the call order is wrong, I think",
		}},
	} {
		if out, problems := validate(t, form); len(problems) != 0 || len(out) == 0 {
			t.Errorf("%s: out %s, problems %v, want it accepted", name, out, problems)
		}
	}
	for name, form := range map[string]any{
		"a report with a question": goodReport(set("question", map[string]any{"text": "Which?"})),
		"an empty object":          map[string]any{},
		"an array":                 []any{1},
		"null":                     nil,
	} {
		if out, problems := validate(t, form); len(problems) == 0 || out != nil {
			t.Errorf("%s: out %s, problems %v, want it refused", name, out, problems)
		}
	}
}

func TestValidate_EachLimitHoldsAtItsBoundary(t *testing.T) {
	setSubject := func(s string) func(m map[string]any) { return set("subject", s) }
	file := func(i int) any { return map[string]any{"path": fmt.Sprintf("src/f%03d.go", i), "why": "w"} }
	test := func(int) any { return map[string]any{"path": "tests/fix_test.sh", "proves": "p"} }
	criterion := func(i int) any { return map[string]any{"criterion": fmt.Sprintf("c%d", i), "status": "met"} }
	decision := func(int) any { return "d" }
	for _, c := range []struct {
		name    string
		ok, bad func(m map[string]any)
		pointer string
	}{
		{"subject length", setSubject(x(72)), setSubject(x(73)), "/subject"},
		{"subject in characters", setSubject(strings.Repeat("é", 72)), setSubject(strings.Repeat("é", 73)), "/subject"},
		{"subject period", setSubject("No period"), setSubject("A period."), "/subject"},
		{"subject type prefix", setSubject("fixes the thing"), setSubject("fix: the thing"), "/subject"},
		{"subject scoped prefix", setSubject("the (fix) thing"), setSubject("feat(cli)!: the thing"), "/subject"},
		{"subject line", setSubject("one line"), setSubject("two\nlines"), "/subject"},
		{"subject empty", setSubject("a"), setSubject("  "), "/subject"},
		{"subject missing", setSubject("a"), func(m map[string]any) { delete(m, "subject") }, "/subject"},
		{"type", set("type", "refactor"), set("type", "bugfix"), "/type"},
		{"root_cause", set("root_cause", x(4000)), set("root_cause", x(4001)), "/root_cause"},
		{"root_cause with a NUL", set("root_cause", "x"), set("root_cause", "a\x00b"), "/root_cause"},
		{"change empty", set("change", "x"), set("change", " \n "), "/change"},
		{"files count", set("files", items(500, file)), set("files", items(501, file)), "/files"},
		{"files none", set("files", items(1, file)), set("files", []any{}), "/files"},
		{"file under .git", filePath("src/.gitignore"), filePath(".git/config"), "/files/0/path"},
		{"file under .agent-fox", filePath("src/.agent-fox"), filePath(".Agent-Fox/gate.json"), "/files/0/path"},
		{"file absolute", filePath("src/a.go"), filePath("/etc/passwd"), "/files/0/path"},
		{"file on a drive", filePath("src/a.go"), filePath("C:/a.go"), "/files/0/path"},
		{"file up", filePath("src/a..go"), filePath("src/../../x"), "/files/0/path"},
		{"file not clean", filePath("src/a.go"), filePath("src//a.go"), "/files/0/path"},
		{"file is the root", filePath("a"), filePath("."), "/files/0/path"},
		{"file with a backslash", filePath("src/a.go"), filePath(`src\a.go`), "/files/0/path"},
		{"file why", setFile(func(f map[string]any) { f["why"] = x(300) }), setFile(func(f map[string]any) { f["why"] = x(301) }), "/files/0/why"},
		{"duplicate files", set("files", []any{file(1), file(2)}), set("files", []any{file(1), file(1)}), "/files/1/path"},
		{"test file in the tree", setTest("tests/fix_test.sh"), setTest("tests/missing.sh"), "/tests/0/path"},
		{"test that is a directory", setTest("tests/fix_test.sh"), setTest("tests/dir"), "/tests/0/path"},
		{"tests count", set("tests", items(100, test)), set("tests", items(101, test)), "/tests"},
		{"criteria count", set("criteria", items(50, criterion)), set("criteria", items(51, criterion)), "/criteria"},
		{"criterion status", set("criteria", []any{map[string]any{"criterion": "c", "status": "not_applicable"}}),
			set("criteria", []any{map[string]any{"criterion": "c", "status": "done"}}), "/criteria/0/status"},
		{"decisions count", set("decisions", items(30, decision)), set("decisions", items(31, decision)), "/decisions"},
		{"decision length", set("decisions", []any{x(500)}), set("decisions", []any{x(501)}), "/decisions/0"},
		{"unknown field", set("type", "fix"), set("extra", 1), "/extra"},
		{"unknown field of a file", setFile(func(map[string]any) {}), setFile(func(f map[string]any) { f["a/b"] = "x" }), "/files/0/a~1b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, problems := validate(t, goodReport(c.ok)); len(problems) != 0 {
				t.Errorf("inside the limit: problems = %v, want none", problems)
			}
			if _, problems := validate(t, goodReport(c.bad)); !slices.Contains(pointers(problems), c.pointer) {
				t.Errorf("outside the limit: problems = %v, want one at %s", problems, c.pointer)
			}
		})
	}

	for _, c := range []struct {
		name    string
		ok, bad map[string]any
		pointer string
	}{
		{"text", question(x(2000)), question(x(2001)), "/question/text"},
		{"no text", question("q"), map[string]any{"question": map[string]any{"options": []any{"a"}}}, "/question/text"},
		{"options", question("q", "1", "2", "3", "4", "5", "6"), question("q", "1", "2", "3", "4", "5", "6", "7"), "/question/options"},
		{"an option", question("q", x(200)), question("q", x(201)), "/question/options/0"},
		{"an empty option", question("q", "a"), question("q", ""), "/question/options/0"},
		{"root_cause", map[string]any{"question": map[string]any{"text": "q", "root_cause": x(8000)}}, map[string]any{"question": map[string]any{"text": "q", "root_cause": x(8001)}}, "/question/root_cause"},
		{"unknown field", question("q"), map[string]any{"question": map[string]any{"text": "q", "hint": "h"}}, "/question/hint"},
		{"another field beside it", question("q"), map[string]any{"question": map[string]any{"text": "q"}, "subject": "s"}, "/subject"},
	} {
		t.Run("question "+c.name, func(t *testing.T) {
			if _, problems := validate(t, c.ok); len(problems) != 0 {
				t.Errorf("inside the limit: problems = %v, want none", problems)
			}
			if _, problems := validate(t, c.bad); !slices.Contains(pointers(problems), c.pointer) {
				t.Errorf("outside the limit: problems = %v, want one at %s", problems, c.pointer)
			}
		})
	}
}

func TestValidate_NamesWhatAValueOfTheWrongTypeIs(t *testing.T) {
	for _, c := range []struct {
		form    map[string]any
		pointer string
		message string
	}{
		{goodReport(set("subject", 7)), "/subject", "must be a string, not a number"},
		{goodReport(set("files", "src/a.go")), "/files", "must be an array, not a string"},
		{goodReport(set("files", []any{[]any{}})), "/files/0", "must be an object {path, why}, not an array"},
		{goodReport(set("tests", []any{true})), "/tests/0", "not a boolean"},
		{goodReport(set("criteria", []any{nil})), "/criteria/0", "not null"},
		{goodReport(set("decisions", []any{map[string]any{}})), "/decisions/0", "not an object"},
		{map[string]any{"question": "Which?"}, "/question", "must be an object {text, options, root_cause}, not a string"},
		{map[string]any{"question": map[string]any{"text": "q", "options": map[string]any{}}}, "/question/options", "must be an array of strings, not an object"},
		{map[string]any{"question": map[string]any{"text": "q", "options": []any{1}}}, "/question/options/0", "must be a string, not a number"},
	} {
		_, problems := validate(t, c.form)
		if i := slices.Index(pointers(problems), c.pointer); i < 0 || !strings.Contains(problems[i].Message, c.message) {
			t.Errorf("problems = %v, want %q at %s", problems, c.message, c.pointer)
		}
	}
}

// A brain that is told to correct every problem it is refused for corrects them in one round
// only if it is told of all of them, a field breaking several rules included.
func TestValidate_RefusesWithEveryProblemAtOnceInTheOrderOfThePointers(t *testing.T) {
	bad := goodReport(set("extra", true), set("subject", "fix: add thing."), set("criteria", []any{map[string]any{"criterion": "c", "status": "maybe"}}),
		set("files", []any{map[string]any{"path": "src/a.go", "why": "w"}, map[string]any{"path": `/a\..\.git/config`, "why": "w"}}))

	out, problems := validate(t, bad)

	want := []string{"/criteria/0/status", "/extra", "/files/1/path", "/files/1/path", "/subject", "/subject"}
	if out != nil || !slices.Equal(pointers(problems), want) {
		t.Errorf("out %s, pointers %v, want no payload and %v", out, pointers(problems), want)
	}
	var messages []string
	for _, p := range problems {
		messages = append(messages, p.Message)
	}
	for _, word := range []string{"backslash", "absolute", "period", "type"} {
		if !strings.Contains(strings.Join(messages, "|"), word) {
			t.Errorf("no problem says %q: %v", word, messages)
		}
	}
}

// The normalised payload has no CR, no untrimmed string, the default type and its files in
// order, and it is its own normal form.
func TestValidate_NormalisesThePayloadOnce(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	white := []string{"", " ", "\n", "\r\n", "\t ", "\v\f", "\u00a0", "\u2003\n", " \t\r\n "}
	pad := func(s string) string { return white[rng.Intn(len(white))] + s + white[rng.Intn(len(white))] }
	text := func(n int) string {
		var b strings.Builder
		for i := range n {
			if i > 0 {
				b.WriteString([]string{" ", "\r\n", "\n", "\r"}[rng.Intn(4)])
			}
			b.WriteString([]string{"alpha", "beta", "gamma"}[rng.Intn(3)])
		}
		return b.String()
	}
	for i := range 50 {
		m := map[string]any{
			"subject": pad("Subject " + text(1)), "root_cause": pad(text(rng.Intn(6) + 1)), "change": pad(text(3)),
			"files": []any{
				map[string]any{"path": pad("src/z.go"), "why": pad(text(2))},
				map[string]any{"path": pad("src/a.go"), "why": pad(text(1))},
			},
			"criteria":  []any{map[string]any{"criterion": pad(text(2)), "status": "met", "evidence": pad(text(1))}},
			"decisions": []any{pad(text(3))},
		}
		if i%2 == 0 {
			m["type"] = "docs"
		}

		first, problems := validate(t, m)
		if len(problems) != 0 {
			t.Fatalf("case %d: problems = %v", i, problems)
		}
		r := testutil.Decode[Report](t, first)
		if strings.Contains(string(first), `\r`) || (i%2 == 1 && r.Type != DefaultType) || r.Files[0].Path != "src/a.go" || r.Files[1].Path != "src/z.go" {
			t.Fatalf("case %d: a CR, no default type or files out of order: %s", i, first)
		}
		for _, s := range []string{r.Subject, r.RootCause, r.Change, r.Files[0].Why, r.Criteria[0].Criterion, r.Criteria[0].Evidence, r.Decisions[0]} {
			if strings.TrimSpace(s) != s {
				t.Fatalf("case %d: %q is not trimmed", i, s)
			}
		}
		if second, problems := FixReport.Validate(context.Background(), submittedTree(), first); len(problems) != 0 || string(second) != string(first) {
			t.Fatalf("case %d: validating the normal form gave %s (%v), want it unchanged", i, second, problems)
		}
	}
	out, _ := validate(t, goodReport(set("root_cause", " first\r\nsecond\rthird\nfourth\r\n ")))
	if r := testutil.Decode[Report](t, out); r.RootCause != "first\nsecond\nthird\nfourth" {
		t.Errorf("root_cause = %q, want each line ending one LF", r.RootCause)
	}
}

func TestDecode_ReadsAnAcceptedPayloadAsAReportOrAQuestion(t *testing.T) {
	out, _ := validate(t, goodReport())
	if sub, err := Decode(out); err != nil || sub.Report == nil || sub.Question != nil || sub.Report.Subject != "Check the arguments before help runs" || len(sub.Report.Files) != 2 {
		t.Errorf("Decode = %+v, %v, want the report", sub, err)
	}
	out, _ = validate(t, question("Which?", "a", "b"))
	if sub, err := Decode(out); err != nil || sub.Report != nil || sub.Question == nil || sub.Question.Text != "Which?" || !slices.Equal(sub.Question.Options, []string{"a", "b"}) {
		t.Errorf("Decode = %+v, %v, want the question", sub, err)
	}
	for _, payload := range []string{`[1]`, `{"subject":7}`} {
		if _, err := Decode([]byte(payload)); err == nil {
			t.Errorf("Decode(%s) succeeded", payload)
		}
	}
}
