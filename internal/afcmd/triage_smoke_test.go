package afcmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// The smoke tests of the seven paths of 08_af_triage. Each runs the commands the
// way a person types them, through the shell, against the real triage loop, brief
// renderer, validator, body renderer, local box and the issuex clients, with an
// httptest server for the forge and a git repository: the only things that are
// not the product are the forge's server and the model (the faux provider, or a
// script).

// landed is the body of an issue the loop wrote: the original report in it, and
// whether it is the diagnosis of a run.
func landed(t *testing.T, e *triageEnv, number int) (body, original string) {
	t.Helper()
	body = e.forge.Issue(number).Body
	original, _, warning := triage.Extract(body)
	if warning != "" {
		t.Fatalf("the body of issue %d does not parse: %s\n%s", number, warning, body)
	}
	return body, original
}

// loopbackOnly replaces the transport of the process with one that fails a
// request to any host but this machine's, and keeps what it saw.
type loopbackOnly struct {
	next http.RoundTripper
	mu   sync.Mutex
	seen []string
}

func (l *loopbackOnly) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Hostname()
	l.mu.Lock()
	l.seen = append(l.seen, host)
	l.mu.Unlock()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return nil, &os.PathError{Op: "dial", Path: r.URL.String(), Err: os.ErrPermission}
	}
	return l.next.RoundTrip(r)
}

// TestTheTriageSuiteRunsOffline_TS_08_69 verifies TS-08-69: with no token and no
// key in the environment, a loop on GitHub and one on GitLab, each with the
// scripted brain or the agentkit brain on the faux provider and the local box,
// end done, and every request they make goes to this machine.
// Verifies: 08-REQ-10.5
func TestTheTriageSuiteRunsOffline_TS_08_69(t *testing.T) {
	requireGit(t) // the suite skips without git, and so does this guard of it
	guard := &loopbackOnly{next: http.DefaultTransport}
	old := http.DefaultTransport
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport = old })

	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		for _, brainName := range []string{"scripted", "agentkit"} {
			t.Run(kind+" with "+brainName, func(t *testing.T) {
				e := newTriageEnv(t, kind) // clears the tokens and the key of the environment, and skips without git
				for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
					if v := os.Getenv(name); v != "" {
						t.Fatalf("%s is set", name)
					}
				}
				args := []string{e.forge.IssueURL(7), "--dir", e.dir, "--brain", brainName, "--sandbox", "local", "--label", "triaged"}
				if brainName == "scripted" {
					args = append(args, "--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))
				} else {
					fauxBrains(t, faux.New(call("c1", "file_diagnosis", diagnosisPayload(nil))))
				}

				code, env := e.loop(args...)

				if code != 0 || env["status"] != "done" {
					t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
				}
				if n := len(e.forge.Requests()); n == 0 {
					t.Error("the forge saw no request")
				}
			})
		}
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if len(guard.seen) == 0 {
		t.Error("the guard saw no request: it is not in the way of the forge clients")
	}
	for _, host := range guard.seen {
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			t.Errorf("a request went to %q", host)
		}
	}
}

// TestAnIssueIsTriagedEndToEnd_TS_08_71 verifies TS-08-71 (08-PATH-1): the agentkit
// brain, on the faux provider, reads a file and files a diagnosis; the body of the
// issue is replaced once with the original report kept between the markers, and
// the label is added. On GitHub and on GitLab.
// Verifies: 08-PATH-1, 08-REQ-8.3, 08-REQ-9.3
func TestAnIssueIsTriagedEndToEnd_TS_08_71(t *testing.T) {
	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run(kind, func(t *testing.T) {
			e := newTriageEnv(t, kind)
			before := e.forge.Issue(7).Body
			p := faux.New(call("c1", "read_file", `{"path":"main.go"}`), call("c2", "file_diagnosis", diagnosisPayload(nil)))
			fauxBrains(t, p)

			code, env := e.loop(e.forge.IssueURL(7), "--brain", "agentkit", "--sandbox", "local", "--dir", e.dir, "--label", "triaged")

			if code != 0 || env["status"] != "done" || resultOf(t, env)["action"] != "replaced_body" {
				t.Fatalf("exit code %d, status %v, error %v, result %v", code, env["status"], env["error"], env["result"])
			}
			if n := e.forge.Count(fakeforge.OpBody); n != 1 {
				t.Errorf("%d body writes, want exactly 1", n)
			}
			body, original := landed(t, e, 7)
			if original != before || !strings.Contains(body, "Run calls Help before it checks its arguments.") {
				t.Errorf("the original between the markers is %q, want the body read at the start, %q; body:\n%s", original, before, body)
			}
			if !slices.Contains(e.forge.Issue(7).Labels, "triaged") {
				t.Errorf("labels = %v, want triaged", e.forge.Issue(7).Labels)
			}
			if p.Calls() != 2 {
				t.Errorf("the model was asked %d times, want 2 (a read, then the diagnosis)", p.Calls())
			}
			dir := e.runDir(env)
			for _, name := range []string{"brief.md", "original.md", "triage.json", "submission.json", "diagnosis.md"} {
				if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
			if kinds := ledgerKinds(t, dir); !slices.Contains(kinds, "triage.land") {
				t.Errorf("ledger = %v", kinds)
			}
		})
	}
}

// TestTextAndFileEachCreateOneIssue_TS_08_72 verifies TS-08-72 (08-PATH-2).
// Verifies: 08-PATH-2, 08-REQ-8.5
func TestTextAndFileEachCreateOneIssue_TS_08_72(t *testing.T) {
	const report = "The app crashes on submit\nwith a nil pointer."
	e := newTriageEnv(t, fakeforge.GitHub)
	file := filepath.Join(t.TempDir(), "crash.txt")
	if err := os.WriteFile(file, []byte(report+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i, input := range []string{report, file} {
		code, env := e.loop(input, "--repo", "o/r", "--dir", e.dir, "--brain", "scripted", "--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

		if code != 0 || resultOf(t, env)["action"] != "created_issue" {
			t.Fatalf("input %d: exit code %d, error %v, result %v", i+1, code, env["error"], env["result"])
		}
		if n := e.forge.Count(fakeforge.OpCreate); n != i+1 {
			t.Errorf("input %d: %d creates in all, want %d", i+1, n, i+1)
		}
		created, _ := resultOf(t, env)["issue"].(map[string]any)
		number := int(created["number"].(float64))
		issue := e.forge.Issue(number)
		if issue.Title != "Run starts the wrong thing" {
			t.Errorf("input %d: title = %q, want the title of the submission", i+1, issue.Title)
		}
		if _, original := landed(t, e, number); original != report {
			t.Errorf("input %d: the original-report section holds %q, want %q", i+1, original, report)
		}
		if len(artifactsOf(env, "issue")) != 1 {
			t.Errorf("input %d: artifacts = %v, want one issue", i+1, env["artifacts"])
		}
	}
	if n := e.forge.Count(fakeforge.OpBody); n != 0 {
		t.Errorf("%d body writes: a text report has no body to replace", n)
	}
}

// TestAMissingPathIsRefusedAsTheNextTurnAndCorrected_TS_08_73 verifies TS-08-73
// (08-PATH-3), with the scripted brain, which refuses the first diagnosis, and
// with the faux provider, whose second request carries the refusal.
// Verifies: 08-PATH-3, 08-REQ-6.2
func TestAMissingPathIsRefusedAsTheNextTurnAndCorrected_TS_08_73(t *testing.T) {
	missing := diagnosisPayload(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "nowhere.go", "note": "x"}}
	})
	check := func(t *testing.T, e *triageEnv, code int, env map[string]any) {
		t.Helper()
		if code != 0 || env["status"] != "done" {
			t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
		}
		got := resultOf(t, env)
		if got["rejected_path_calls"] != float64(1) || got["rejected_submissions"] != float64(1) {
			t.Errorf("result = %v, want 1 rejected path call", got)
		}
		diagnosis, _ := got["diagnosis"].(map[string]any)
		for _, f := range diagnosis["affected_files"].([]any) {
			path := f.(map[string]any)["path"].(string)
			if _, err := os.Stat(filepath.Join(e.dir, filepath.FromSlash(path))); err != nil {
				t.Errorf("the landed diagnosis cites %s: %v", path, err)
			}
		}
		line := string(readFile(t, filepath.Join(e.runDir(env), "rejections.jsonl")))
		if strings.Count(line, "\n") != 1 || !strings.Contains(line, "/affected_files/0/path") || !strings.Contains(line, `"nowhere.go"`) {
			t.Errorf("rejections.jsonl:\n%s", line)
		}
		if issue := e.forge.Issue(7); strings.Contains(issue.Body, "nowhere.go") {
			t.Errorf("the refused diagnosis was landed:\n%s", issue.Body)
		}
	}

	t.Run("the scripted brain", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script",
			script(t, submitStep(missing, "refused"), submitStep(diagnosisPayload(nil), "accepted")))
		check(t, e, code, env)
	})
	t.Run("the agentkit brain", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		p := faux.New(call("c1", "file_diagnosis", missing), call("c2", "file_diagnosis", diagnosisPayload(nil)))
		fauxBrains(t, p)

		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir)

		check(t, e, code, env)
		reqs := p.Requests()
		if len(reqs) != 2 {
			t.Fatalf("%d requests, want 2", len(reqs))
		}
		var results []string
		for _, m := range reqs[1].Messages {
			if tr, ok := m.(core.ToolResultMessage); ok {
				results = append(results, tr.Content.Text())
			}
		}
		if len(results) != 1 || !strings.Contains(results[0], "/affected_files/0/path") || !strings.Contains(results[0], `"nowhere.go"`) {
			t.Errorf("the second request carries %v, want the refusal that names the path", results)
		}
	})
}

// TestAQuestionStopsTheRunAndTheAnswerResumesIt_TS_08_74 verifies TS-08-74
// (08-PATH-4): the run stops with needs_human and no write, and its resume argv
// with --context B renders a brief with the same prefix that holds the question and
// the answer.
// Verifies: 08-PATH-4, 08-REQ-3.6
func TestAQuestionStopsTheRunAndTheAnswerResumesIt_TS_08_74(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	scriptPath := script(t, submitStep(questionPayload(), "accepted"))

	code, first := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", scriptPath)

	if code != 3 || first["status"] != "needs_human" {
		t.Fatalf("exit code %d, status %v, error %v", code, first["status"], first["error"])
	}
	nh, _ := first["needs_human"].(map[string]any)
	if options, _ := nh["options"].([]any); len(options) != 2 {
		t.Errorf("needs_human = %v, want the two options", nh)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Fatalf("%d writes to the forge", n)
	}
	var argv []string
	for _, a := range nh["resume"].(map[string]any)["argv"].([]any) {
		argv = append(argv, a.(string))
	}

	// The person answers; the same invocation runs again, and this time the brain has a diagnosis.
	if err := os.WriteFile(scriptPath, mustRead(t, script(t, submitStep(diagnosisPayload(nil), "accepted"))), 0o644); err != nil {
		t.Fatal(err)
	}
	code, second := e.run("", append(argv[1:], "--context", "B")...)

	if code != 0 || second["status"] != "done" {
		t.Fatalf("resumed: exit code %d, status %v, error %v", code, second["status"], second["error"])
	}
	var firstFacts, secondFacts triage.Facts
	mustJSON(t, readFile(t, filepath.Join(e.runDir(first), "triage.json")), &firstFacts)
	mustJSON(t, readFile(t, filepath.Join(e.runDir(second), "triage.json")), &secondFacts)
	if firstFacts.Prefix.SHA256 == "" || firstFacts.Prefix != secondFacts.Prefix {
		t.Errorf("prefix of the first run %+v, of the resumed one %+v, want the same", firstFacts.Prefix, secondFacts.Prefix)
	}
	brief := string(readFile(t, filepath.Join(e.runDir(second), "brief.md")))
	task := brief[secondFacts.Prefix.Bytes:]
	for _, want := range []string{"Which session?", "A. old", "B. new", "B\n"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block of the resumed run does not hold %q:\n%s", want, task)
		}
	}
	if strings.Contains(brief[:secondFacts.Prefix.Bytes], "Which session?") {
		t.Error("the question is in the prefix, which has to be the same for every run")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	return readFile(t, path)
}

func mustJSON(t *testing.T, data []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatal(err)
	}
}

// TestRetriagingKeepsTheFirstOriginalReport_TS_08_75 verifies TS-08-75
// (08-PATH-5): the second triage of an issue sees the first diagnosis as possibly
// stale, replaces it, and keeps the original report byte for byte.
// Verifies: 08-PATH-5, 08-REQ-7.6
func TestRetriagingKeepsTheFirstOriginalReport_TS_08_75(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	first := diagnosisPayload(nil)
	second := diagnosisPayload(func(m map[string]any) {
		m["title"] = "Help is called too early"
		m["root_cause"] = "Help runs before Run has looked at its arguments, in a second reading."
	})

	if code, env := scriptedLoop(e, e.forge.IssueURL(7), first); code != 0 {
		t.Fatalf("first triage: exit code %d, error %v", code, env["error"])
	}
	_, original := landed(t, e, 7)

	code, env := scriptedLoop(e, e.forge.IssueURL(7), second)

	if code != 0 || env["status"] != "done" {
		t.Fatalf("second triage: exit code %d, error %v", code, env["error"])
	}
	brief := string(readFile(t, filepath.Join(e.runDir(env), "brief.md")))
	i := strings.Index(brief, "Previous agent-fox diagnosis, from the body of the issue; possibly stale")
	if i < 0 || !strings.Contains(brief[i:], "Run calls Help before it checks its arguments.") {
		t.Errorf("the second brief does not fence the first diagnosis as possibly stale:\n%s", brief)
	}
	if strings.Count(brief, "The login button does nothing.") != 1 {
		t.Errorf("the original report is in the brief %d times, want once: the previous diagnosis does not carry it again", strings.Count(brief, "The login button does nothing."))
	}
	body, again := landed(t, e, 7)
	if again != original || original != originalReport {
		t.Errorf("the original after the second triage is %q, after the first %q, at the start %q", again, original, originalReport)
	}
	if !strings.Contains(body, "in a second reading") || strings.Contains(body, "Run calls Help before it checks its arguments.") {
		t.Errorf("the body should hold the second diagnosis only:\n%s", body)
	}
	if n := e.forge.Count(fakeforge.OpBody); n != 2 {
		t.Errorf("%d body writes, want one for each triage", n)
	}
}

// TestADryRunAndLandNoneWriteNothingToTheForge_TS_08_76 verifies TS-08-76
// (08-PATH-6).
// Verifies: 08-PATH-6, 08-REQ-8.8, 08-REQ-8.2
func TestADryRunAndLandNoneWriteNothingToTheForge_TS_08_76(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := scriptedLoop(e, e.forge.IssueURL(7), diagnosisPayload(nil), "--dry-run", "--label", "triaged")

	if code != 0 || resultOf(t, env)["action"] != "planned" {
		t.Fatalf("dry run: exit code %d, error %v, result %v", code, env["error"], env["result"])
	}
	effects := sideEffects(env)
	if !slices.Equal(effects["update_issue"], []string{"planned"}) || !slices.Equal(effects["add_labels"], []string{"planned"}) {
		t.Errorf("side effects = %v, want update_issue and add_labels planned", effects)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("dry run: %d writes to the forge", n)
	}

	// No token, a text report and --land none: nothing is asked of the forge at all.
	e = newTriageEnv(t, fakeforge.GitHub)
	e.app.NewForge = e.forge.NewForge("")
	code, env = scriptedLoop(e, "the app crashes on submit", diagnosisPayload(nil), "--repo", "o/r", "--land", "none")

	if code != 0 || resultOf(t, env)["action"] != "none" || env["status"] != "done" {
		t.Fatalf("--land none: exit code %d, error %v, result %v", code, env["error"], env["result"])
	}
	if n := len(e.forge.Requests()); n != 0 {
		t.Errorf("--land none: %d requests to the forge: %v", n, e.forge.Requests())
	}
	if _, err := os.Stat(filepath.Join(e.runDir(env), "diagnosis.md")); err != nil {
		t.Errorf("diagnosis.md: %v", err)
	}
	if len(artifactsOf(env, "diagnosis")) != 1 {
		t.Errorf("artifacts = %v, want diagnosis.md", env["artifacts"])
	}
}
