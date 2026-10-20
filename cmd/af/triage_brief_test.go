package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// triageEnv is a git repository, a fake forge with an issue 7 in it, and the
// shell wired to the forge.
type triageEnv struct {
	t     *testing.T
	dir   string
	forge *fakeforge.Forge
	app   *toolio.App
}

const originalReport = "The login button does nothing.\nIt used to work."

// newTriageEnv makes the environment for a forge of the kind. The token is the
// forge's; the environment of the test has none.
func newTriageEnv(t *testing.T, kind string) *triageEnv {
	t.Helper()
	requireGit(t)
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL", "ANTHROPIC_API_KEY"} {
		t.Setenv(name, "")
	}
	e := &triageEnv{t: t, forge: fakeforge.New(t, kind, "o/r")}
	e.dir = gitRepo(t, map[string]string{
		"go.mod":       "module example.com/x\n",
		"main.go":      "package main\n\nimport \"example.com/x/util\"\n\n// Run starts the program.\nfunc Run() {\n\tutil.Help()\n}\n\nfunc main() { Run() }\n",
		"util/util.go": "package util\n\n// Help helps.\nfunc Help() {}\n",
	})
	e.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: originalReport, Comments: []fakeforge.Comment{
		{Author: "bob", Body: "Works for me."},
	}})
	e.app = newApp()
	e.app.NewForge = e.forge.NewForge("test-token")
	return e
}

// run runs the shell with args and returns the exit code and the envelope.
func (e *triageEnv) run(stdin string, args ...string) (int, map[string]any) {
	e.t.Helper()
	var out, errb bytes.Buffer
	code := e.app.Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	env := oneDocument(e.t, out.String())
	see(env)
	return code, env
}

// runDir is the run directory of an envelope.
func (e *triageEnv) runDir(env map[string]any) string {
	e.t.Helper()
	id, _ := env["run_id"].(string)
	if id == "" {
		e.t.Fatalf("the envelope has no run_id: %v", env)
	}
	return filepath.Join(e.dir, ".agent-fox", "run", id)
}

func sha256Of(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// ledgerKinds are the kinds of the ledger of a run directory.
func ledgerKinds(t *testing.T, runDir string) []string {
	t.Helper()
	var kinds []string
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, filepath.Join(runDir, "ledger.jsonl")))), "\n") {
		var entry struct{ Kind string }
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, entry.Kind)
	}
	return kinds
}

// resultOf is the result of an envelope as a map.
func resultOf(t *testing.T, env map[string]any) map[string]any {
	t.Helper()
	m, ok := env["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %v, want an object (status %v, error %v)", env["result"], env["status"], env["error"])
	}
	return m
}

func artifactsOf(env map[string]any, kind string) []map[string]any {
	var out []map[string]any
	list, _ := env["artifacts"].([]any)
	for _, a := range list {
		if m, ok := a.(map[string]any); ok && m["kind"] == kind {
			out = append(out, m)
		}
	}
	return out
}

func warningCodes(env map[string]any) []string {
	var out []string
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		if m, ok := w.(map[string]any); ok {
			out = append(out, m["code"].(string))
		}
	}
	return out
}

// TestBriefWritesTheRunFilesWithConsistentHashes_TS_08_3 verifies TS-08-3:
// af triage brief writes brief.md, original.md and triage.json into the run
// directory, and the hashes in triage.json are those of the files.
// Verifies: 08-REQ-1.3
func TestBriefWritesTheRunFilesWithConsistentHashes_TS_08_3(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	dir := e.runDir(env)
	brief, original := readFile(t, filepath.Join(dir, "brief.md")), readFile(t, filepath.Join(dir, "original.md"))
	var facts struct {
		FormatVersion int `json:"format_version"`
		Input         struct{ Kind, Origin, Fingerprint string }
		Target        struct{ Repo, Host string }
		Issue         struct {
			Repo, Host, URL string
			Number          int
		}
		OriginalReportSHA256 string `json:"original_report_sha256"`
		Prefix               struct {
			Bytes  int
			SHA256 string
		}
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, "triage.json")), &facts); err != nil {
		t.Fatal(err)
	}

	if facts.FormatVersion != 1 || facts.Input.Kind != "issue" || facts.Input.Origin != e.forge.IssueURL(7) || facts.Input.Fingerprint != "o/r#7" {
		t.Errorf("triage.json input = %+v (format %d)", facts.Input, facts.FormatVersion)
	}
	if facts.Target.Repo != "o/r" || facts.Target.Host != "github.com" || facts.Issue.Repo != "o/r" || facts.Issue.Number != 7 || facts.Issue.Host != "github.com" || facts.Issue.URL != e.forge.IssueURL(7) {
		t.Errorf("triage.json target %+v, issue %+v", facts.Target, facts.Issue)
	}
	if string(original) != originalReport {
		t.Errorf("original.md = %q, want the issue body", original)
	}
	if facts.OriginalReportSHA256 != sha256Of(original) {
		t.Errorf("original_report_sha256 = %s, want the hash of original.md", facts.OriginalReportSHA256)
	}
	if facts.Prefix.Bytes <= 0 || facts.Prefix.Bytes >= len(brief) || sha256Of(brief[:facts.Prefix.Bytes]) != facts.Prefix.SHA256 {
		t.Errorf("prefix %+v: want its hash to be that of the first %d bytes of brief.md", facts.Prefix, facts.Prefix.Bytes)
	}
	for _, name := range []string{"brief.md", "original.md", "triage.json"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || info.Mode().Perm()&0o200 == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, ent := range entries {
		if strings.HasPrefix(ent.Name(), ".tmp") {
			t.Errorf("a temporary file is left in the run directory: %s", ent.Name())
		}
	}
}

// TestBriefPlacesTheEarlierQuestionAndTheAnswer_TS_08_21 verifies TS-08-21:
// --context puts the question of the newest earlier run with the same
// fingerprint, and then the answer, in the task block; with no earlier question
// the answers stand alone; a run of another input is ignored.
// Verifies: 08-REQ-3.6
func TestBriefPlacesTheEarlierQuestionAndTheAnswer_TS_08_21(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	earlier := func(id, fingerprint, question string) {
		t.Helper()
		dir := filepath.Join(e.dir, ".agent-fox", "run", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		facts, _ := json.Marshal(map[string]any{"format_version": 1, "input": map[string]any{"kind": "issue", "fingerprint": fingerprint}})
		ledger := `{"seq":1,"ts":"2026-10-19T10:00:00.000Z","kind":"triage.input","data":{}}` + "\n"
		if question != "" {
			ledger += `{"seq":2,"ts":"2026-10-19T10:01:00.000Z","kind":"triage.question","data":{"text":"` + question + `","options":["The old one","The new one"]}}` + "\n"
		}
		for name, content := range map[string][]byte{"triage.json": facts, "ledger.jsonl": []byte(ledger)} {
			if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("with no earlier question the answer stands alone", func(t *testing.T) {
		code, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir, "--context", "B")
		if code != 0 {
			t.Fatalf("exit code %d: %v", code, env["error"])
		}
		if resultOf(t, env)["previous_question"] != false {
			t.Errorf("previous_question = %v, want false", resultOf(t, env)["previous_question"])
		}
		task := taskBlockOf(t, readFile(t, filepath.Join(e.runDir(env), "brief.md")))
		if !strings.Contains(task, "A person's answer:\n```\nB\n```") || strings.Contains(task, "The question you asked") {
			t.Errorf("the task block should hold the answer alone, as the answer of a person:\n%s", task)
		}
	})

	earlier("20261019_100000_aaaaaa", "o/r#7", "Which session was meant, first?")
	earlier("20261019_110000_bbbbbb", "o/r#7", "Which session was meant?")
	earlier("20261019_120000_cccccc", "o/r#8", "A question about another issue?")
	earlier("20261019_130000_dddddd", "o/r#7", "") // a newer run of this issue that asked nothing

	t.Run("the newest question of the same input comes before the answer", func(t *testing.T) {
		code, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir, "--context", "B")
		if code != 0 {
			t.Fatalf("exit code %d: %v", code, env["error"])
		}
		if resultOf(t, env)["previous_question"] != true {
			t.Errorf("previous_question = %v, want true", resultOf(t, env)["previous_question"])
		}
		task := taskBlockOf(t, readFile(t, filepath.Join(e.runDir(env), "brief.md")))
		q, a := strings.Index(task, "Which session was meant?"), strings.Index(task, "\nB\n")
		if q < 0 || a < 0 || q > a {
			t.Errorf("the question is at %d and the answer at %d, want both, the question first:\n%s", q, a, task)
		}
		for _, absent := range []string{"first?", "another issue"} {
			if strings.Contains(task, absent) {
				t.Errorf("the task block holds %q: an older run, or another input's", absent)
			}
		}
		if !strings.Contains(task, "A. The old one") || !strings.Contains(task, "B. The new one") {
			t.Errorf("the options of the question are missing:\n%s", task)
		}
	})

	t.Run("without --context no question is looked for", func(t *testing.T) {
		_, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir)
		if resultOf(t, env)["previous_question"] != false {
			t.Errorf("previous_question = %v, want false without an answer to place", resultOf(t, env)["previous_question"])
		}
	})
}

// taskBlockOf is the task block of a brief.
func taskBlockOf(t *testing.T, brief []byte) string {
	t.Helper()
	i := bytes.LastIndex(brief, []byte("\n## Task\n"))
	if i < 0 {
		t.Fatalf("the brief has no task block:\n%s", brief)
	}
	return string(brief[i:])
}

// TestBriefReturnsItsResultArtifactAndNextEntries_TS_08_23 verifies TS-08-23:
// the result, the artifact, the next entries and the ledger of af triage brief,
// and that it sends nothing to the forge.
// Verifies: 08-REQ-3.8
func TestBriefReturnsItsResultArtifactAndNextEntries_TS_08_23(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir)

	if code != 0 || env["status"] != "done" || env["tool"] != "triage brief" {
		t.Fatalf("exit code %d, status %v, tool %v, error %v", code, env["status"], env["tool"], env["error"])
	}
	dir := e.runDir(env)
	brief := readFile(t, filepath.Join(dir, "brief.md"))
	result := resultOf(t, env)
	for _, key := range []string{"brief", "prefix_bytes", "prefix_sha256", "document_bytes", "estimated_tokens", "map", "report_truncated", "previous_question"} {
		if _, ok := result[key]; !ok {
			t.Errorf("the result has no %s: %v", key, result)
		}
	}
	if result["brief"] != filepath.Join(dir, "brief.md") || int(result["document_bytes"].(float64)) != len(brief) {
		t.Errorf("brief %v, document_bytes %v, want %s and %d", result["brief"], result["document_bytes"], filepath.Join(dir, "brief.md"), len(brief))
	}
	prefix := int(result["prefix_bytes"].(float64))
	if result["prefix_sha256"] != sha256Of(brief[:prefix]) {
		t.Errorf("prefix_sha256 = %v, want the hash of the first %d bytes", result["prefix_sha256"], prefix)
	}
	if want := (len(brief) + 3) / 4; int(result["estimated_tokens"].(float64)) != want {
		t.Errorf("estimated_tokens = %v, want %d", result["estimated_tokens"], want)
	}
	mp, _ := result["map"].(map[string]any)
	if _, ok := mp["level"]; !ok || mp["truncated"] == nil {
		t.Errorf("map = %v, want level and truncated", mp)
	}
	if result["report_truncated"] != false || result["previous_question"] != false {
		t.Errorf("report_truncated %v, previous_question %v, want false and false", result["report_truncated"], result["previous_question"])
	}

	if arts := artifactsOf(env, "brief"); len(arts) != 1 || arts[0]["path"] != filepath.Join(dir, "brief.md") {
		t.Errorf("artifacts = %v, want one brief", env["artifacts"])
	}
	var nexts []string
	for _, n := range env["next"].([]any) {
		argv := n.(map[string]any)["argv"].([]any)
		var words []string
		for _, w := range argv {
			words = append(words, w.(string))
		}
		nexts = append(nexts, strings.Join(words, " "))
	}
	id := env["run_id"].(string)
	wantSubmit, wantLand := "af triage submit --run-id "+id+" --dir "+e.dir, "af triage land --run-id "+id+" --dir "+e.dir
	if !slices.Contains(nexts, wantSubmit) || !slices.Contains(nexts, wantLand) || len(nexts) != 2 {
		t.Errorf("next = %v, want %q and %q", nexts, wantSubmit, wantLand)
	}
	if kinds := ledgerKinds(t, dir); !slices.Equal(kinds, []string{"triage.input", "triage.brief"}) {
		t.Errorf("ledger = %v, want triage.input then triage.brief", kinds)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge, want none", n)
	}
	if !strings.Contains(string(brief), "af triage submit --run-id "+id) || strings.Contains(string(brief), "file_diagnosis") {
		t.Error("the brief of af triage brief should name the command route and not the tool")
	}
}

// TestTheBriefSchemaMatchesItsResult guards 08-REQ-10.4 for af triage brief: the
// embedded schema has the fields of the Go type, and requires the ones that are
// never left out.
func TestTheBriefSchemaMatchesItsResult(t *testing.T) {
	checkSchema(t, "triage_brief", triageBriefResult{})
}

// checkSchema compares the embedded schema of a command with the fields of its
// result type.
func checkSchema(t *testing.T, name string, typ any) {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(resultSchema(name), &schema); err != nil {
		t.Fatal(err)
	}
	for _, diff := range schemaDiff(schema, reflect.TypeOf(typ), name) {
		t.Error(diff)
	}
}

// git2 runs git in dir and returns what it printed.
func git2(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitDo(t, dir, args...))
}

// TestTheOriginalReportIsKeptExactlyAsTheIssueHasIt guards 08-REQ-1.3: original.md
// holds the bytes of the report, edge white space and line endings included, since
// the body of the issue is built around them.
func TestTheOriginalReportIsKeptExactlyAsTheIssueHasIt(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	const body = "  line one\r\nline two  \n\n"
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Edges", Body: body})

	code, env := e.run("", "triage", "brief", e.forge.IssueURL(9), "--dir", e.dir)

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	if got := string(readFile(t, filepath.Join(e.runDir(env), "original.md"))); got != body {
		t.Errorf("original.md = %q, want %q", got, body)
	}
}

// TestABriefThatCutsTheReportSaysSoInTheEnvelope guards 08-REQ-3.4 at the command:
// the result says the report was cut, and so does a warning that is the brief's
// own, besides the one the shell raised for the input.
func TestABriefThatCutsTheReportSaysSoInTheEnvelope(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	e.forge.AddIssue(fakeforge.Issue{Number: 12, Title: "Big", Body: strings.Repeat("x", 140000)})

	code, env := e.run("", "triage", "brief", e.forge.IssueURL(12), "--dir", e.dir)

	if code != 0 || resultOf(t, env)["report_truncated"] != true {
		t.Fatalf("exit code %d, result %v, want report_truncated", code, env["result"])
	}
	var own bool
	for _, w := range env["warnings"].([]any) {
		m := w.(map[string]any)
		own = own || (m["code"] == "input_truncated" && strings.Contains(m["message"].(string), "in the brief"))
	}
	if !own {
		t.Errorf("warnings = %v, want input_truncated from the brief", env["warnings"])
	}
}
