package triage

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
	"github.com/agent-fox-dev/agentfox/skills"
)

const testMarker = "\n[... input truncated at 131072 bytes ...]\n"

// plainTree is a directory of files. It is not a git repository, so the file
// view is a walk and the tests need no git.
func plainTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func goTree(t *testing.T) string {
	return plainTree(t, map[string]string{
		"go.mod":       "module example.com/x\n",
		"main.go":      "package main\n\nfunc Run() {}\n",
		"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
	})
}

func issueRef() issuex.IssueRef {
	return issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}, Number: 7}
}

var day = time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)

// issueReport is an issue by alice with two comments, one by alice and one by
// bob, whose body may be a body we rendered earlier.
func issueReport(body string) Report {
	return Report{
		Kind:   KindIssue,
		Origin: issueRef().URL(),
		Ref:    issueRef(),
		Thread: issuex.IssueThread{
			Issue: issuex.Issue{Number: 7, Title: "Login fails", Body: body, State: "open", Author: issuex.User{Login: "alice"}, CreatedAt: day},
			Comments: []issuex.Comment{
				{Body: "It also fails on Safari.", User: issuex.User{Login: "alice"}, CreatedAt: day.Add(24 * time.Hour)},
				{Body: "Works for me.", User: issuex.User{Login: "bob"}, CreatedAt: day.Add(48 * time.Hour)},
			},
		},
	}
}

func params(dir string, report Report) BriefParams {
	return BriefParams{
		Dir: dir, Report: report, RunID: testRun,
		Bounds:           Bounds{MaxTurns: 100, MaxCostUSD: 2, Timeout: 15 * time.Minute},
		TruncationMarker: testMarker,
	}
}

func build(t *testing.T, p BriefParams) Brief {
	t.Helper()
	b, err := BuildBrief(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// section is the text of doc from the heading start up to the heading next, or
// to the end.
func section(t *testing.T, doc, start, next string) string {
	t.Helper()
	i := strings.Index(doc, start)
	if i < 0 {
		t.Fatalf("the document has no %q", start)
	}
	rest := doc[i:]
	if next != "" {
		if j := strings.Index(rest[len(start):], next); j >= 0 {
			return rest[:len(start)+j]
		}
	}
	return rest
}

// TestThePrefixHoldsMandateSkillMapAndReportInOrder_TS_08_16 verifies TS-08-16:
// the prefix is the mandate, the body of the skill without its frontmatter, the
// repository map and the report, in that order.
// Verifies: 08-REQ-3.1
func TestThePrefixHoldsMandateSkillMapAndReportInOrder_TS_08_16(t *testing.T) {
	dir := goTree(t)
	b := build(t, params(dir, issueReport("It crashes on submit.")))

	files, err := checks.FileView(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]repomap.Entry, len(files))
	for i, f := range files {
		entries[i] = repomap.Entry{Path: f, Size: -1}
		if info, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			entries[i].Size = info.Size()
		}
	}
	m, err := repomap.Build(context.Background(), entries, repomap.NewOutliner(dir), 4000)
	if err != nil {
		t.Fatal(err)
	}

	skill := skills.Triage()
	at := []int{
		strings.Index(b.Document, "# Triage mandate"),
		strings.Index(b.Document, skill),
		strings.Index(b.Document, m.Map),
		strings.Index(b.Document, "## Report"),
	}
	for i, n := range at {
		if n < 0 || (i > 0 && n <= at[i-1]) {
			t.Fatalf("the mandate, the skill, the map and the report are at %v, want all present and increasing", at)
		}
	}
	for _, line := range []string{"name: af-triage", "argument-hint:", "description: Diagnose"} {
		if strings.Contains(b.Document, line) {
			t.Errorf("the skill's frontmatter line %q is in the brief", line)
		}
	}
	if !strings.HasPrefix(b.Document, b.Prefix) || b.Document != b.Prefix+b.Task || b.PrefixBytes != len(b.Prefix) {
		t.Error("the document is not the prefix followed by the task block")
	}
	if at[3] > len(b.Prefix) || !strings.Contains(b.Prefix, "It crashes on submit.") {
		t.Error("the report is not in the prefix")
	}
	if b.MapLevel != m.Level || b.MapTruncated != m.Truncated {
		t.Errorf("map level %d truncated %v, want %d %v", b.MapLevel, b.MapTruncated, m.Level, m.Truncated)
	}
	for _, want := range []string{"read-only", "verify", "never", "cite only", "exactly one diagnosis", "/affected_files/", "<!-- agent-fox:"} {
		if !strings.Contains(strings.ToLower(section(t, b.Document, "# Triage mandate", "# af-triage")), strings.ToLower(want)) {
			t.Errorf("the mandate does not say %q", want)
		}
	}
}

// fenceOpener matches the opening line of a fence.
var fenceOpener = regexp.MustCompile("^(`{3,})$")

// fencesOf finds the fences of text and the line before each.
func fencesOf(text string) (before, content []string) {
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceOpener.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j] != m[1] {
			j++
		}
		before = append(before, lines[i-1])
		content = append(content, strings.Join(lines[i+1:j], "\n"))
		i = j
	}
	return
}

// TestEachReportPartIsFencedAfterOneProvenanceLine_TS_08_17 verifies TS-08-17:
// the title, the original report, the previous diagnosis (labelled as possibly
// stale) and every comment each get one fence, preceded by one provenance line;
// text, file and stdin get one fence labelled with their source.
// Verifies: 08-REQ-3.2
func TestEachReportPartIsFencedAfterOneProvenanceLine_TS_08_17(t *testing.T) {
	dir := goTree(t)
	previous := Render(fullDiagnosis(), testRun, "The login button does nothing.\n")
	b := build(t, params(dir, issueReport(previous)))
	report := section(t, b.Prefix, "## Report", "")

	before, content := fencesOf(report)
	if len(before) != 5 {
		t.Fatalf("%d fences in the report, want 5 (title, original, previous diagnosis, two comments):\n%s", len(before), report)
	}
	wantContent := []string{"Login fails", "The login button does nothing.\n", "", "It also fails on Safari.", "Works for me."}
	for i, w := range wantContent {
		if w != "" && content[i] != w {
			t.Errorf("fence %d holds %q, want %q", i, content[i], w)
		}
		if strings.TrimSpace(before[i]) == "" || !strings.HasSuffix(before[i], ":") {
			t.Errorf("the line before fence %d is %q, want a provenance line", i, before[i])
		}
	}
	if !strings.Contains(content[2], "## Diagnosis") || strings.Contains(content[2], "<!-- agent-fox:triage") {
		t.Errorf("the previous diagnosis fence holds %q", content[2])
	}
	if !strings.Contains(before[2], "possibly stale") {
		t.Errorf("the previous diagnosis is labelled %q, want it called possibly stale", before[2])
	}
	if b.Original != "The login button does nothing.\n" {
		t.Errorf("Original = %q, want the report between the markers", b.Original)
	}
	for i, want := range []string{"alice", "alice", "", "alice", "bob"} {
		if want != "" && !strings.Contains(before[i], want) {
			t.Errorf("provenance line %d %q does not name %s", i, before[i], want)
		}
	}
	for i, date := range map[int]string{0: "2026-10-01", 3: "2026-10-02", 4: "2026-10-03"} {
		if !strings.Contains(before[i], date) {
			t.Errorf("provenance line %d %q lacks the date %s", i, before[i], date)
		}
	}
	if !strings.Contains(before[0], "reporter") || !strings.Contains(before[3], "reporter") || strings.Contains(before[4], "reporter") {
		t.Errorf("the reporter is marked on %q, %q and not on %q, want the two of alice and not bob's", before[0], before[3], before[4])
	}

	for name, tc := range map[string]struct {
		report Report
		label  string
	}{
		"text":  {Report{Kind: KindText, Body: "the app crashes"}, "command line"},
		"file":  {Report{Kind: KindFile, Origin: filepath.Join(dir, "reports", "crash.md"), Body: "the app crashes"}, "crash.md"},
		"stdin": {Report{Kind: KindStdin, Body: "the app crashes"}, "stdin"},
	} {
		t.Run(name, func(t *testing.T) {
			b := build(t, params(dir, tc.report))
			before, content := fencesOf(section(t, b.Prefix, "## Report", ""))
			if len(before) != 1 || content[0] != "the app crashes" || !strings.Contains(before[0], tc.label) || !strings.HasSuffix(before[0], ":") {
				t.Errorf("fences = %q preceded by %q, want one holding the input, labelled with %q", content, before, tc.label)
			}
			if b.Original != "the app crashes" {
				t.Errorf("Original = %q", b.Original)
			}
		})
	}
}

func backtickText(rng *rand.Rand) string {
	pieces := []string{"", "a", "\n", "`", "``", "```", "````", "`````", "\n```\n", "\n````", "```go\n", " ", "text", "\r\n"}
	var b strings.Builder
	for range rng.Intn(10) {
		b.WriteString(pieces[rng.Intn(len(pieces))])
	}
	return b.String()
}

func longestRun(s string) int {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return longest
}

// TestAFenceIsLongerThanAnyRunInItsContent_TS_08_18 verifies TS-08-18:
// a fence is one backtick longer than the longest run in its content, at least
// three long, and no line of the content closes it.
// Verifies: 08-REQ-3.3
func TestAFenceIsLongerThanAnyRunInItsContent_TS_08_18(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	cases := []string{"", "plain", "```", "````", "a\n```\nb", "`````````"}
	for range 3000 {
		cases = append(cases, backtickText(rng))
	}
	for _, s := range cases {
		open, closing := Fence(s)

		if open != closing {
			t.Fatalf("the fence opens with %q and closes with %q", open, closing)
		}
		if want := max(3, longestRun(s)+1); len(open) != want || strings.Trim(open, "`") != "" {
			t.Fatalf("fence %q for content with a longest run of %d, want %d backticks", open, longestRun(s), want)
		}
		for _, line := range strings.Split(s, "\n") {
			if line == closing {
				t.Fatalf("a line of %q closes its fence", s)
			}
		}
	}
}

// TestAnOversizedReportIsCutAtARuneBoundaryWithTheMarker_TS_08_19 verifies TS-08-19:
// a report over 131072 bytes is cut at a UTF-8 boundary, followed by the
// shell's marker, with the warning input_truncated.
// Verifies: 08-REQ-3.4
func TestAnOversizedReportIsCutAtARuneBoundaryWithTheMarker_TS_08_19(t *testing.T) {
	dir := goTree(t)
	for name, unit := range map[string]string{"two-byte runes": "é", "three-byte runes": "日", "four-byte runes": "😀", "ascii": "x"} {
		t.Run(name, func(t *testing.T) {
			// Long enough to be cut, and not a multiple of the limit in runes.
			body := strings.Repeat(unit, 131072/len(unit)+5) + "end"
			b := build(t, params(dir, issueReport(body)))

			report := section(t, b.Document, "## Report", "## Task")
			cut := strings.Index(report, testMarker)
			if cut < 0 {
				t.Fatal("the report holds no truncation marker")
			}
			if !utf8.ValidString(b.Document) || !utf8.ValidString(report[:cut]) {
				t.Error("the brief is not valid UTF-8")
			}
			if cut > 131072 {
				t.Errorf("the report is %d bytes before the marker, want at most 131072", cut)
			}
			if !b.ReportTruncated {
				t.Error("ReportTruncated is false")
			}
			var warned bool
			for _, w := range b.Warnings {
				warned = warned || w.Code == WarnInputTruncated
			}
			if !warned {
				t.Errorf("warnings = %+v, want input_truncated", b.Warnings)
			}
			if strings.Contains(report, "end\n") && strings.Contains(report[cut:], "```\nend") {
				t.Error("the report goes on after the cut")
			}
			// The cut part's fence is closed, so the task block is not inside it.
			before, _ := fencesOf(report)
			if strings.Count(report, "\n```") < len(before)*2 {
				t.Error("a fence of the report is not closed")
			}
		})
	}

	b := build(t, params(dir, issueReport("a short report")))
	if b.ReportTruncated || strings.Contains(b.Document, testMarker) {
		t.Error("a short report was cut")
	}
}

// TestPrefixesAreByteIdenticalAcrossRuns_TS_08_20 verifies TS-08-20:
// the prefix is the same for any run id, context, brain bound and submission
// route, and holds none of them.
// Verifies: 08-REQ-3.5
func TestPrefixesAreByteIdenticalAcrossRuns_TS_08_20(t *testing.T) {
	dir := goTree(t)
	report := issueReport("The login button does nothing.")
	base := build(t, params(dir, report))
	rng := rand.New(rand.NewSource(20))
	for range 60 {
		p := params(dir, report)
		p.RunID = fmt.Sprintf("%08d_%06d_%06x", rng.Intn(99999999), rng.Intn(999999), rng.Intn(0xffffff))
		for range rng.Intn(4) {
			p.Context = append(p.Context, fmt.Sprintf("answer-%d-%x", rng.Intn(1000), rng.Int63()))
		}
		if rng.Intn(2) == 0 {
			p.PreviousQuestion = &Question{Text: fmt.Sprintf("question %x?", rng.Int63()), Options: []string{"first", "second"}}
		}
		p.InProcess = rng.Intn(2) == 0
		p.Bounds = Bounds{MaxTurns: 1 + rng.Intn(500), MaxCostUSD: float64(1+rng.Intn(50)) / 4, Timeout: time.Duration(1+rng.Intn(120)) * time.Minute}

		b := build(t, p)

		if b.Prefix != base.Prefix {
			t.Fatalf("the prefix differs for run %s", p.RunID)
		}
		for _, run := range append([]string{p.RunID, dir, "file_diagnosis"}, p.Context...) {
			if strings.Contains(b.Prefix, run) {
				t.Fatalf("the prefix holds %q", run)
			}
		}
		if !strings.Contains(b.Task, p.RunID) {
			t.Fatalf("the task block lacks the run id %s", p.RunID)
		}
	}
}

// TestTheTaskBlockEndsWithTheRunIDOneRouteAndTheBounds_TS_08_22 verifies TS-08-22:
// the task block names the run, exactly one way to submit, and the bounds.
// Verifies: 08-REQ-3.7
func TestTheTaskBlockEndsWithTheRunIDOneRouteAndTheBounds_TS_08_22(t *testing.T) {
	dir := goTree(t)
	for name, inProcess := range map[string]bool{"an in-process brain": true, "a brain that runs af": false} {
		t.Run(name, func(t *testing.T) {
			p := params(dir, issueReport("body"))
			p.InProcess = inProcess
			p.Bounds = Bounds{MaxTurns: 40, MaxCostUSD: 3.5, Timeout: 10 * time.Minute}

			task := build(t, p).Task

			if !strings.Contains(task, testRun) {
				t.Error("the task block lacks the run id")
			}
			tool, command := strings.Contains(task, "file_diagnosis"), strings.Contains(task, "af triage submit --run-id "+testRun)
			if tool == command || tool != inProcess {
				t.Errorf("file_diagnosis %v, af triage submit %v: want exactly the route of an in-process brain = %v", tool, command, inProcess)
			}
			if command && !strings.Contains(task, "stdin") {
				t.Error("the command route does not say the JSON goes on stdin")
			}
			for _, bound := range []string{"40 turns", "$3.50", "10m0s"} {
				if !strings.Contains(task, bound) {
					t.Errorf("the task block lacks the bound %q:\n%s", bound, task)
				}
			}
		})
	}
}

// TestTheTaskBlockCarriesTheEarlierQuestionAndTheAnswers guards 08-REQ-3.6: the
// question of an earlier run comes before the answers, each answer fenced and
// labelled as a person's.
func TestTheTaskBlockCarriesTheEarlierQuestionAndTheAnswers(t *testing.T) {
	dir := goTree(t)
	p := params(dir, issueReport("body"))
	p.Context = []string{"B", "and the second\n```\nanswer"}
	p.PreviousQuestion = &Question{Text: "Which session?", Options: []string{"The old one", "The new one"}}

	task := build(t, p).Task

	q, a := strings.Index(task, "Which session?"), strings.Index(task, "\nB\n")
	if q < 0 || a < 0 || q > a {
		t.Fatalf("the question is at %d and the answer at %d, want both, the question first:\n%s", q, a, task)
	}
	for _, want := range []string{"A. The old one", "B. The new one"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block lacks the option %q", want)
		}
	}
	before, content := fencesOf(task)
	var answers []string
	for i, b := range before {
		if strings.Contains(b, "person's answer") {
			answers = append(answers, content[i])
		}
	}
	if len(answers) != 2 || answers[0] != "B" || answers[1] != "and the second\n```\nanswer" {
		t.Errorf("answers = %q, want both, in order, fenced", answers)
	}

	p.Context, p.PreviousQuestion = nil, nil
	if task := build(t, p).Task; strings.Contains(task, "person's answer") || strings.Contains(task, "Which session?") {
		t.Error("a task block with no context carries an answer or a question")
	}
}
