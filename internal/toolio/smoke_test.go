package toolio

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

// TestSmokeDryRunReadsAnIssueAndIsRefusedEveryWrite_TS_05_75 verifies TS-05-75:
// A dry run reads a GitHub issue and is refused every forge write.
// Verifies: 05-PATH-2, 05-REQ-8.8, 05-REQ-9.1, 05-REQ-9.4
func TestSmokeDryRunReadsAnIssueAndIsRefusedEveryWrite_TS_05_75(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	forge := newRecordingForge(t)
	githubEnv(t, forge.URL)
	checkout := t.TempDir()
	git(t, checkout, "init", "-q", "-b", "main")

	var writeErrs []error
	app := newTestApp() // the default NewForge: a real issuex client
	app.Register(Command{
		Path: "X", Input: InputRequired, UsesForge: true, UsesRunDir: true, DryRun: true,
		Run: func(r *Run) (Outcome, error) {
			ref := issuex.IssueRef{Repo: r.Target, Number: 7}
			_, commentErr := r.Forge.AddComment(r.Ctx, ref, "a diagnosis")
			_, prErr := r.Forge.CreatePullRequest(r.Ctx, r.Target, issuex.CreatePullRequestRequest{Title: "t", Head: "h", Base: "main"})
			writeErrs = []error{commentErr, prErr}
			return Outcome{Status: StatusDone, Summary: "planned two writes"}, nil
		},
	})

	var out, errb bytes.Buffer
	code := app.Main(context.Background(), []string{"af", "X", forge.URL + "/o/r/issues/7", "--dry-run", "--dir", checkout},
		strings.NewReader(""), &out, &errb)
	env := decode(t, out.String())

	if code != 0 || env["status"] != "done" || at(env, "input", "kind") != "issue" || errb.Len() != 0 {
		t.Fatalf("exit code %d, status %v, input kind %v, stderr %q; want 0, done, issue, nothing\n%s",
			code, env["status"], at(env, "input", "kind"), errb.String(), out.String())
	}
	for i, err := range writeErrs {
		if !errors.Is(err, ErrDryRun) {
			t.Errorf("write %d returned %v, want an error wrapping ErrDryRun", i, err)
		}
	}
	wantEffects := []any{
		map[string]any{"kind": "add_comment", "target": "o/r#7", "state": "planned"},
		map[string]any{"kind": "create_pull_request", "target": "o/r", "state": "planned"},
	}
	if got := env["side_effects"]; !reflect.DeepEqual(got, wantEffects) {
		t.Errorf("side_effects = %v, want %v", got, wantEffects)
	}

	// The forge was read and not written.
	forge.mu.Lock()
	requests := slices.Clone(forge.requests)
	forge.mu.Unlock()
	if forge.writes() != 0 || !slices.Contains(requests, "GET /repos/o/r/issues/7") || !slices.Contains(requests, "GET /repos/o/r/issues/7/comments") {
		t.Errorf("the forge saw %v; want the reads of the issue and its comments and no write", requests)
	}

	// The local state: the run directory, ignored by Git, holding the
	// envelope that stdout got and an event stream from start to end.
	runID, _ := env["run_id"].(string)
	runDir := filepath.Join(checkout, ".agent-fox", "run", runID)
	if data, err := os.ReadFile(filepath.Join(checkout, ".agent-fox", ".gitignore")); err != nil || string(data) != "*\n" {
		t.Errorf(".agent-fox/.gitignore = %q (%v)", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(runDir, "envelope.json")); err != nil || string(data) != out.String() {
		t.Errorf("envelope.json = %q (%v), want what stdout got", data, err)
	}
	events := readEvents(t, filepath.Join(runDir, "events.jsonl"))
	if types := eventTypes(events); types[0] != "run_start" || types[len(types)-1] != "run_end" {
		t.Errorf("event types = %v, want run_start … run_end", types)
	}
	if status := git(t, checkout, "status", "--porcelain"); status != "" {
		t.Errorf("git status --porcelain shows:\n%s", status)
	}
}

// TestSmokeNeedsHumanIsResumedWithContext_TS_05_76 verifies TS-05-76:
// A command stops on a question and is resumed with --context.
// Verifies: 05-PATH-3, 05-REQ-5.1, 05-REQ-5.2, 05-REQ-5.3
func TestSmokeNeedsHumanIsResumedWithContext_TS_05_76(t *testing.T) {
	t.Chdir(t.TempDir())
	var contexts [][]string
	app := newTestApp()
	app.Register(Command{Path: "X", Input: InputRequired, Context: true, Run: func(r *Run) (Outcome, error) {
		contexts = append(contexts, r.Context)
		if len(r.Context) == 0 {
			return Outcome{Status: StatusNeedsHuman, Summary: "needs a decision",
				NeedsHuman: &Question{Text: "Which service is affected?", Options: []string{"api", "web"}}}, nil
		}
		return Outcome{Status: StatusDone, Summary: "resumed"}, nil
	}})

	var out bytes.Buffer
	first := app.Main(context.Background(), []string{"af", "X", "the login page crashes"}, strings.NewReader(""), &out, &bytes.Buffer{})
	env := decode(t, out.String())
	if first != 3 || env["status"] != "needs_human" {
		t.Fatalf("exit code %d, status %v; want 3, needs_human", first, env["status"])
	}
	wantOptions := []any{map[string]any{"id": "A", "label": "api"}, map[string]any{"id": "B", "label": "web"}}
	if got := at(env, "needs_human", "options"); !reflect.DeepEqual(got, wantOptions) {
		t.Errorf("options = %v, want %v", got, wantOptions)
	}
	if at(env, "needs_human", "resume", "flag") != "--context" {
		t.Errorf("resume.flag = %v, want --context", at(env, "needs_human", "resume", "flag"))
	}
	var resume []string
	for _, word := range at(env, "needs_human", "resume", "argv").([]any) {
		resume = append(resume, word.(string))
	}
	if resume[0] != "af" {
		t.Fatalf("resume.argv = %v, want it to start with af", resume)
	}

	out.Reset()
	second := app.Main(context.Background(), append(resume, "--context", "B"), strings.NewReader(""), &out, &bytes.Buffer{})
	env = decode(t, out.String())
	if second != 0 || env["status"] != "done" || !reflect.DeepEqual(contexts, [][]string{nil, {"B"}}) {
		t.Errorf("resumed: exit code %d, status %v, contexts %q; want 0, done, [] then [B]", second, env["status"], contexts)
	}
}

// TestSmokeUnauthenticatedPrivateMergeRequest_TS_05_78 verifies TS-05-78:
// An unauthenticated read of a private GitLab merge request fails with an
// auth hint.
// Verifies: 05-PATH-5, 05-REQ-7.6
func TestSmokeUnauthenticatedPrivateMergeRequest_TS_05_78(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	forge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		http.Error(w, `{"message":"404 Not Found"}`, http.StatusNotFound)
	}))
	defer forge.Close()
	gitlabEnv(t, forge.URL)

	var rec inputRecorder
	app := newTestApp()
	app.Register(issueCommand(&rec))

	code, stdout, _ := invoke(t, app, "X", forge.URL+"/g/p/-/merge_requests/3")
	env := decode(t, stdout)

	if code != 1 || env["status"] != "failed" || at(env, "error", "category") != CategoryAuth || rec.ran != 0 {
		t.Fatalf("exit code %d, status %v, category %v, ran %d; want 1, failed, auth, 0", code, env["status"], at(env, "error", "category"), rec.ran)
	}
	if msg, _ := at(env, "error", "message").(string); !strings.Contains(msg, "private") {
		t.Errorf("error.message = %q, want it to say the merge request may be private", msg)
	}
	if got := at(env, "error", "fix_hint"); !reflect.DeepEqual(got, map[string]any{"env": "GITLAB_TOKEN"}) {
		t.Errorf("fix_hint = %v, want {env: GITLAB_TOKEN}", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 || !strings.HasPrefix(requests[0], "GET ") || !strings.HasSuffix(requests[0], "/merge_requests/3") {
		t.Errorf("the forge saw %v, want only the read of the merge request", requests)
	}
}
