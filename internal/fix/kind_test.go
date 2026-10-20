package fix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/submission"
)

// tree is the tree a report is submitted in: it holds the tests a report may
// cite, and a directory where a test would be.
func tree() fstest.MapFS {
	return fstest.MapFS{
		"tests/fix_test.sh": {Data: []byte("#!/bin/sh\n")},
		"tests/other.sh":    {Data: []byte("#!/bin/sh\n")},
		"src/a.go":          {Data: []byte("package a\n")},
		"tests/dir/x":       {Data: []byte("x")},
	}
}

// report is a report that has no problem, as the map a brain would send.
func report() map[string]any {
	return map[string]any{
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
}

// with returns report() changed by f.
func with(f func(m map[string]any)) map[string]any {
	m := report()
	f(m)
	return m
}

func payload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validate(t *testing.T, v any) (json.RawMessage, []submission.Problem) {
	t.Helper()
	return FixReport.Validate(context.Background(), tree(), payload(t, v))
}

// paths are the field pointers of the problems.
func paths(problems []submission.Problem) []string {
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

// TestTheKindIsFixReportAndTakesOneOfTwoForms_TS_09_26 verifies TS-09-26:
// the kind has its name and the name of its tool, and takes a report or a
// question, and not both.
// Verifies: 09-REQ-4.1
func TestTheKindIsFixReportAndTakesOneOfTwoForms_TS_09_26(t *testing.T) {
	if FixReport.Name != "fix_report" || FixReport.ToolName != "submit_fix" || FixReport.Description == "" {
		t.Fatalf("kind = %q / %q (%q)", FixReport.Name, FixReport.ToolName, FixReport.Description)
	}
	var schema map[string]any
	if err := json.Unmarshal(FixReport.Schema, &schema); err != nil || schema["type"] != "object" {
		t.Errorf("the schema of the tool = %s (%v), want a JSON Schema object", FixReport.Schema, err)
	}

	for name, form := range map[string]any{
		"a report":                report(),
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
	mixed := with(func(m map[string]any) { m["question"] = map[string]any{"text": "Which?"} })
	if out, problems := validate(t, mixed); len(problems) == 0 || out != nil {
		t.Errorf("a report with a question: out %s, problems %v, want it refused", out, problems)
	}
	if _, problems := validate(t, map[string]any{}); len(problems) == 0 {
		t.Error("an empty object was accepted: it is neither form")
	}
}

type limit struct {
	name    string
	ok, bad func(m map[string]any)
	pointer string
}

func str(n int) string { return strings.Repeat("x", n) }

func setFile(f func(file map[string]any)) func(m map[string]any) {
	return func(m map[string]any) {
		file := map[string]any{"path": "src/a.go", "why": "w"}
		f(file)
		m["files"] = []any{file}
	}
}

func fileList(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"path": fmt.Sprintf("src/f%03d.go", i), "why": "w"}
	}
	return out
}

func setTest(f func(tc map[string]any)) func(m map[string]any) {
	return func(m map[string]any) {
		tc := map[string]any{"path": "tests/fix_test.sh", "proves": "p"}
		f(tc)
		m["tests"] = []any{tc}
	}
}

// TestReportAndQuestionLimitsHoldAtTheirBoundaries_TS_09_27 verifies TS-09-27:
// each limit accepts the value just inside it and names the field of the value
// just outside it.
// Verifies: 09-REQ-4.2
func TestReportAndQuestionLimitsHoldAtTheirBoundaries_TS_09_27(t *testing.T) {
	setSubject := func(s string) func(m map[string]any) { return func(m map[string]any) { m["subject"] = s } }
	set := func(key string, v any) func(m map[string]any) { return func(m map[string]any) { m[key] = v } }
	cases := []limit{
		{name: "subject length", ok: setSubject(str(72)), bad: setSubject(str(73)), pointer: "/subject"},
		{name: "subject in characters", ok: setSubject(strings.Repeat("é", 72)), bad: setSubject(strings.Repeat("é", 73)), pointer: "/subject"},
		{name: "subject period", ok: setSubject("No period"), bad: setSubject("A period."), pointer: "/subject"},
		{name: "subject type prefix", ok: setSubject("fixes the thing"), bad: setSubject("fix: the thing"), pointer: "/subject"},
		{name: "subject scoped prefix", ok: setSubject("the (fix) thing"), bad: setSubject("feat(cli): the thing"), pointer: "/subject"},
		{name: "subject line", ok: setSubject("one line"), bad: setSubject("two\nlines"), pointer: "/subject"},
		{name: "subject empty", ok: setSubject("a"), bad: setSubject("  "), pointer: "/subject"},
		{name: "type", ok: set("type", "refactor"), bad: set("type", "bugfix"), pointer: "/type"},
		{name: "root_cause", ok: set("root_cause", str(4000)), bad: set("root_cause", str(4001)), pointer: "/root_cause"},
		{name: "root_cause empty", ok: set("root_cause", "x"), bad: set("root_cause", ""), pointer: "/root_cause"},
		{name: "change", ok: set("change", str(4000)), bad: set("change", str(4001)), pointer: "/change"},
		{name: "change empty", ok: set("change", "x"), bad: set("change", " \n "), pointer: "/change"},
		{name: "files count", ok: set("files", fileList(500)), bad: set("files", fileList(501)), pointer: "/files"},
		{name: "files none", ok: set("files", fileList(1)), bad: set("files", []any{}), pointer: "/files"},
		{name: "file under .git", ok: setFile(func(f map[string]any) { f["path"] = "src/.gitignore" }), bad: setFile(func(f map[string]any) { f["path"] = ".git/config" }), pointer: "/files/0/path"},
		{name: "file under .agent-fox", ok: setFile(func(f map[string]any) { f["path"] = "src/.agent-fox" }), bad: setFile(func(f map[string]any) { f["path"] = ".agent-fox/gate.json" }), pointer: "/files/0/path"},
		{name: "file absolute", ok: setFile(func(f map[string]any) { f["path"] = "src/a.go" }), bad: setFile(func(f map[string]any) { f["path"] = "/etc/passwd" }), pointer: "/files/0/path"},
		{name: "file up", ok: setFile(func(f map[string]any) { f["path"] = "src/a..go" }), bad: setFile(func(f map[string]any) { f["path"] = "src/../../x" }), pointer: "/files/0/path"},
		{name: "file up from the root", ok: setFile(func(f map[string]any) { f["path"] = "src/a.go" }), bad: setFile(func(f map[string]any) { f["path"] = "../a.go" }), pointer: "/files/0/path"},
		{name: "file not clean", ok: setFile(func(f map[string]any) { f["path"] = "src/a.go" }), bad: setFile(func(f map[string]any) { f["path"] = "src//a.go" }), pointer: "/files/0/path"},
		{name: "file with a backslash", ok: setFile(func(f map[string]any) { f["path"] = "src/a.go" }), bad: setFile(func(f map[string]any) { f["path"] = `src\a.go` }), pointer: "/files/0/path"},
		{name: "file why", ok: setFile(func(f map[string]any) { f["why"] = str(300) }), bad: setFile(func(f map[string]any) { f["why"] = str(301) }), pointer: "/files/0/why"},
		{name: "file why empty", ok: setFile(func(f map[string]any) { f["why"] = "x" }), bad: setFile(func(f map[string]any) { f["why"] = "" }), pointer: "/files/0/why"},
		{name: "duplicate files", ok: set("files", []any{map[string]any{"path": "a", "why": "w"}, map[string]any{"path": "b", "why": "w"}}),
			bad: set("files", []any{map[string]any{"path": "a", "why": "w"}, map[string]any{"path": "a", "why": "w2"}}), pointer: "/files/1/path"},
		{name: "test file in the tree", ok: setTest(func(tc map[string]any) {}), bad: setTest(func(tc map[string]any) { tc["path"] = "tests/missing.sh" }), pointer: "/tests/0/path"},
		{name: "test that is a directory", ok: setTest(func(tc map[string]any) {}), bad: setTest(func(tc map[string]any) { tc["path"] = "tests/dir" }), pointer: "/tests/0/path"},
		{name: "tests count", ok: set("tests", repeatTest(100)), bad: set("tests", repeatTest(101)), pointer: "/tests"},
		{name: "criteria count", ok: set("criteria", criteria(50)), bad: set("criteria", criteria(51)), pointer: "/criteria"},
		{name: "criterion status", ok: set("criteria", []any{map[string]any{"criterion": "c", "status": "not_applicable", "evidence": "e"}}),
			bad: set("criteria", []any{map[string]any{"criterion": "c", "status": "done", "evidence": "e"}}), pointer: "/criteria/0/status"},
		{name: "decisions count", ok: set("decisions", decisions(30, 1)), bad: set("decisions", decisions(31, 1)), pointer: "/decisions"},
		{name: "decision length", ok: set("decisions", decisions(1, 500)), bad: set("decisions", decisions(1, 501)), pointer: "/decisions/0"},
		{name: "unknown field", ok: set("type", "fix"), bad: set("extra", 1), pointer: "/extra"},
		{name: "unknown field of a file", ok: setFile(func(f map[string]any) {}), bad: setFile(func(f map[string]any) { f["note"] = "x" }), pointer: "/files/0/note"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, problems := validate(t, with(c.ok)); len(problems) != 0 {
				t.Errorf("inside the limit: problems = %v, want none", problems)
			}
			_, problems := validate(t, with(c.bad))
			if !slices.Contains(paths(problems), c.pointer) {
				t.Errorf("outside the limit: problems = %v, want one at %s", problems, c.pointer)
			}
		})
	}

	questionCases := []struct {
		name    string
		ok, bad map[string]any
		pointer string
	}{
		{"text", question(str(2000)), question(str(2001)), "/question/text"},
		{"empty text", question("x"), question(""), "/question/text"},
		{"options", question("q", "1", "2", "3", "4", "5", "6"), question("q", "1", "2", "3", "4", "5", "6", "7"), "/question/options"},
		{"an option", question("q", str(200)), question("q", str(201)), "/question/options/0"},
		{"an empty option", question("q", "a"), question("q", ""), "/question/options/0"},
		{"root_cause", map[string]any{"question": map[string]any{"text": "q", "root_cause": str(8000)}}, map[string]any{"question": map[string]any{"text": "q", "root_cause": str(8001)}}, "/question/root_cause"},
		{"unknown field", question("q"), map[string]any{"question": map[string]any{"text": "q", "hint": "h"}}, "/question/hint"},
		{"no text", question("q"), map[string]any{"question": map[string]any{"options": []any{"a"}}}, "/question/text"},
	}
	for _, c := range questionCases {
		t.Run("question "+c.name, func(t *testing.T) {
			if _, problems := validate(t, c.ok); len(problems) != 0 {
				t.Errorf("inside the limit: problems = %v, want none", problems)
			}
			if _, problems := validate(t, c.bad); !slices.Contains(paths(problems), c.pointer) {
				t.Errorf("outside the limit: problems = %v, want one at %s", problems, c.pointer)
			}
		})
	}
}

func repeatTest(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"path": "tests/fix_test.sh", "proves": "p"}
	}
	return out
}

func criteria(n int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = map[string]any{"criterion": fmt.Sprintf("c%d", i), "status": "met", "evidence": "e"}
	}
	return out
}

func decisions(n, length int) []any {
	out := make([]any, n)
	for i := range out {
		out[i] = str(length)
	}
	return out
}

// TestAnInvalidPayloadYieldsEveryProblemAtOnce_TS_09_28 verifies TS-09-28:
// every violation is a problem of its own, nothing is normalised, and the
// submit core writes no file for it.
// Verifies: 09-REQ-4.3
func TestAnInvalidPayloadYieldsEveryProblemAtOnce_TS_09_28(t *testing.T) {
	bad := with(func(m map[string]any) {
		m["extra"] = true
		m["subject"] = ""
		m["files"] = []any{map[string]any{"path": "src/a.go", "why": "w"}, map[string]any{"path": "src/a.go", "why": "w"}}
		m["criteria"] = []any{map[string]any{"criterion": "c", "status": "maybe", "evidence": "e"}}
	})

	out, problems := validate(t, bad)

	if out != nil || len(problems) < 4 {
		t.Fatalf("out %s, problems %v, want no payload and at least four problems", out, problems)
	}
	want := []string{"/criteria/0/status", "/extra", "/files/1/path", "/subject"}
	if got := paths(problems); !slices.Equal(got, want) {
		t.Errorf("pointers = %v, want %v, in this order", got, want)
	}

	// Through the submit core: a refusal names them all, and nothing is written.
	written := map[string][]byte{}
	target := submission.Target{
		Tree:  tree(),
		Write: func(_ context.Context, rel string, data []byte) error { written[rel] = data; return nil },
		RunID: "20261020_100000_aaaaaa",
	}
	target.Tree = withRun(tree(), target.RunID)
	_, err := submission.Submit(context.Background(), target, FixReport, payload(t, bad), fixedNow)
	var refused *submission.Refused
	if !asRefused(err, &refused) || len(refused.Problems) != len(problems) {
		t.Fatalf("Submit error = %v, want a refusal with %d problems", err, len(problems))
	}
	if len(written) != 0 {
		t.Errorf("a refused submission wrote %v", written)
	}
}

// TestEveryViolationOfAFieldIsAProblemOfItsOwn guards 09-REQ-4.3 inside one field: a subject
// that ends with a period and starts with a type, and a path that is absolute, has a
// backslash and goes up, are each refused for every rule they break, so that a brain that is
// told to correct them all does it in one round.
func TestEveryViolationOfAFieldIsAProblemOfItsOwn(t *testing.T) {
	_, problems := validate(t, with(func(m map[string]any) {
		m["subject"] = "fix: add thing."
		m["files"] = []any{map[string]any{"path": `/a\..\.git/config`, "why": "w"}}
	}))

	var subject, file []string
	for _, p := range problems {
		switch p.Path {
		case "/subject":
			subject = append(subject, p.Message)
		case "/files/0/path":
			file = append(file, p.Message)
		}
	}
	if len(subject) != 2 || !strings.Contains(strings.Join(subject, "|"), "period") || !strings.Contains(strings.Join(subject, "|"), "type") {
		t.Errorf("subject problems = %v, want the period and the type, each once", subject)
	}
	if len(file) < 2 || !strings.Contains(strings.Join(file, "|"), "backslash") || !strings.Contains(strings.Join(file, "|"), "absolute") {
		t.Errorf("path problems = %v, want the backslashes and the absolute path at least", file)
	}
}

// TestNormalisationIsIdempotentAndApplied_TS_09_29 verifies TS-09-29: for
// reports with white space, CRLF, no type and files out of order, the
// normalised payload has none of them, and validating it returns it unchanged.
// Verifies: 09-REQ-4.4
func TestNormalisationIsIdempotentAndApplied_TS_09_29(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	pad := func(s string) string {
		white := []string{"", " ", "\n", "\r\n", "\t ", "\t", "\v\f", "\u00a0", "\u2003\n", " \t\r\n "}
		return white[rng.Intn(len(white))] + s + white[rng.Intn(len(white))]
	}
	text := func(n int) string {
		words := []string{"alpha", "beta", "gamma", "delta"}
		var b strings.Builder
		for i := range n {
			if i > 0 {
				b.WriteString([]string{" ", "\r\n", "\n", "\r"}[rng.Intn(4)])
			}
			b.WriteString(words[rng.Intn(len(words))])
		}
		return b.String()
	}
	for i := range 300 {
		m := map[string]any{
			"subject": pad("Subject " + text(1)), "root_cause": pad(text(rng.Intn(6) + 1)), "change": pad(text(rng.Intn(6) + 1)),
			"files": []any{
				map[string]any{"path": pad("src/z.go"), "why": pad(text(2))},
				map[string]any{"path": pad("src/a.go"), "why": pad(text(1))},
				map[string]any{"path": pad("tests/fix_test.sh"), "why": pad(text(1))},
			},
			"decisions": []any{pad(text(3))},
		}
		if rng.Intn(2) == 0 {
			m["type"] = []string{"fix", "feat", "docs"}[rng.Intn(3)]
		}
		if rng.Intn(2) == 0 {
			m["criteria"] = []any{map[string]any{"criterion": pad(text(2)), "status": "met", "evidence": pad(text(1))}}
		}

		first, problems := validate(t, m)
		if len(problems) != 0 {
			t.Fatalf("case %d: problems = %v for %v", i, problems, m)
		}
		var n struct {
			Type  string `json:"type"`
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if err := json.Unmarshal(first, &n); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(first), `\r`) || n.Type == "" {
			t.Fatalf("case %d: the normalised payload has a CR or no type: %s", i, first)
		}
		if !slices.IsSortedFunc(n.Files, func(a, b struct {
			Path string `json:"path"`
		}) int {
			return strings.Compare(a.Path, b.Path)
		}) {
			t.Fatalf("case %d: files are not sorted: %s", i, first)
		}
		var every any
		if err := json.Unmarshal(first, &every); err != nil {
			t.Fatal(err)
		}
		if s, ok := untrimmed(every); ok {
			t.Fatalf("case %d: the string %q is not trimmed: %s", i, s, first)
		}
		second, problems := FixReport.Validate(context.Background(), tree(), first)
		if len(problems) != 0 || string(second) != string(first) {
			t.Fatalf("case %d: validating the normalised payload gave %s (%v), want it unchanged: %s", i, second, problems, first)
		}
		if _, ok := m["type"]; !ok && !strings.Contains(string(first), `"type":"fix"`) {
			t.Fatalf("case %d: the type was not defaulted: %s", i, first)
		}
	}
}

// untrimmed is a string of the value that has white space at either end, if there is one.
func untrimmed(v any) (string, bool) {
	switch v := v.(type) {
	case string:
		return v, strings.TrimSpace(v) != v
	case []any:
		for _, item := range v {
			if s, ok := untrimmed(item); ok {
				return s, true
			}
		}
	case map[string]any:
		for _, item := range v {
			if s, ok := untrimmed(item); ok {
				return s, true
			}
		}
	}
	return "", false
}

var fixedNow = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

// withRun adds the run.json that the submit core reads the attempt and the nonce
// of a submission from.
func withRun(tree fstest.MapFS, runID string) fstest.MapFS {
	run, _ := json.Marshal(submission.Run{RunID: runID, Kind: "fix_report", Attempt: 1, Nonce: "0123456789abcdef"})
	tree[submission.RunPath(runID)] = &fstest.MapFile{Data: run}
	return tree
}

func asRefused(err error, target **submission.Refused) bool { return errors.As(err, target) }

// TestLineEndingsBecomeOneLFEach guards 09-REQ-4.4: CRLF is one line break, and
// not two.
func TestLineEndingsBecomeOneLFEach(t *testing.T) {
	out, problems := validate(t, with(func(m map[string]any) { m["root_cause"] = " first\r\nsecond\rthird\nfourth\r\n " }))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	var r Report
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	if r.RootCause != "first\nsecond\nthird\nfourth" {
		t.Errorf("root_cause = %q", r.RootCause)
	}
}

// TestDecodeReadsWhatValidateWrote guards the typed form later steps read an
// accepted submission as.
func TestDecodeReadsWhatValidateWrote(t *testing.T) {
	out, _ := validate(t, report())
	sub, err := Decode(out)
	if err != nil || sub.Report == nil || sub.Question != nil || sub.Report.Subject != "Check the arguments before help runs" || sub.Report.Type != "fix" || len(sub.Report.Files) != 2 || sub.Report.Files[0].Path != "src/a.go" {
		t.Errorf("Decode = %+v, %v", sub, err)
	}
	out, _ = validate(t, question("Which?", "a", "b"))
	if sub, err := Decode(out); err != nil || sub.Question == nil || sub.Report != nil || sub.Question.Text != "Which?" || !slices.Equal(sub.Question.Options, []string{"a", "b"}) {
		t.Errorf("Decode of a question = %+v, %v", sub, err)
	}
	if _, err := Decode([]byte(`[1]`)); err == nil {
		t.Error("Decode of an array succeeded")
	}
}
