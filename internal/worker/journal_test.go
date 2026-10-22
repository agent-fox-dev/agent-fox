package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// journalOf reads the journal that the attempt of the environment left.
func journalOf(t *testing.T, e *env) recovery.Journal {
	t.Helper()
	j, err := recovery.NewStore(e.dir).Load()
	if err != nil {
		t.Fatalf("the journal cannot be read: %v", err)
	}
	return j
}

// recoveryFiles are the files under recovery/ of the attempt directory, by path.
func recoveryFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	root := filepath.Join(dir, recovery.DirName)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[path] = data
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return out
}

// userinfoURL is a URL with user information.
var userinfoURL = regexp.MustCompile(`://[^/@\s"]+@`)

// requireNoCredential fails the test for every file under recovery/ that holds the PAT, the token of a
// lease the hub granted, an Authorization header or a URL with user information.
func requireNoCredential(t *testing.T, e *env) {
	t.Helper()
	secrets := []string{pat}
	for _, l := range e.hub.Leases() {
		secrets = append(secrets, l.Token)
	}
	for path, data := range recoveryFiles(t, e.dir) {
		for _, secret := range secrets {
			if secret != "" && bytes.Contains(data, []byte(secret)) {
				t.Errorf("%s holds a credential (%.6s…)", path, secret)
			}
		}
		if bytes.Contains(bytes.ToLower(data), []byte("authorization:")) || userinfoURL.Match(data) {
			t.Errorf("%s holds an Authorization header or a URL with user information", path)
		}
	}
}

// receivePacks are the pushes that hub's git server took.
func receivePacks(e *env) int {
	n := 0
	for _, r := range e.hub.GitRequests() {
		if r.Method == "POST" && strings.HasSuffix(r.Path, "git-receive-pack") {
			n++
		}
	}
	return n
}

// slowGateFiles is a repository whose fix is slow to measure: the test the brain writes touches the
// marker and then waits, so that a run can be stopped while the change is measured.
func slowGateFiles(marker string) (map[string]string, []map[string]any) {
	steps := []map[string]any{
		writeStep("src/greet.sh", "echo hello\n"),
		writeStep("tests/hello_test.sh", "touch '"+marker+"'\nsleep 120\n"+helloTest),
		{"tool": "execute", "args": map[string]any{"command": "make box-commit"}, "expect": "ok"},
		submitStep(fixReport),
	}
	return fixFiles, steps
}

// localBranches are the branches of the clone that start with fix/.
func localBranches(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "-C", filepath.Join(dir, "clone"), "branch", "--list", "fix/*", "--format=%(refname:short)")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch: %v", err)
	}
	return strings.Fields(string(out))
}

// TestTheJournalIsWrittenMode0600InA0700DirectoryByAtomicReplacementAndTheWorkerNeverRemovesIt_TS_14_1
// verifies TS-14-1: recovery/ has mode 0700 and journal.json 0600; a reader that polls the file finds
// the first document or the second and never part of one; no temporary file is left; and the cleanup
// of the worker leaves recovery/ and the journal where they are.
// Verifies: 14-REQ-1.1
func TestTheJournalIsWrittenMode0600InA0700DirectoryByAtomicReplacementAndTheWorkerNeverRemovesIt_TS_14_1(t *testing.T) {
	dir := t.TempDir()
	store := recovery.NewStore(dir)
	first := recovery.Journal{Version: recovery.FormatVersion, Job: "job_1", Attempt: 1, Kind: "fix", Session: recovery.Session{ID: "ses_0001"}}
	second := first
	second.Session = recovery.Session{ID: "ses_0002", RunID: "20261022_101010_abcdef", Completed: true}
	second.Deliveries = []recovery.Delivery{{Count: 1, Started: time.Date(2026, 10, 22, 10, 10, 10, 0, time.UTC), Ending: recovery.EndingOutcome}}
	// A document that is long enough to be written in more than one piece, if it were written in place.
	for range 2000 {
		second.Leases = append(second.Leases, recovery.Lease{SandboxID: "sbx_0123456789abcdef0123456789abcdef", ExpiresAt: time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)})
	}
	if err := store.Write(first); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, recovery.DirName, recovery.JournalFile)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var torn []string
	reads := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var j recovery.Journal
			mu.Lock()
			reads++
			if json.Unmarshal(data, &j) != nil || (j.Session.ID != "ses_0001" && j.Session.ID != "ses_0002") {
				torn = append(torn, string(data[:min(len(data), 80)]))
			}
			mu.Unlock()
		}
	}()
	for range 20 {
		if err := store.Write(second); err != nil {
			t.Fatal(err)
		}
		if err := store.Write(first); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Write(second); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	if len(torn) > 0 {
		t.Errorf("a reader found %d documents that are neither the first nor the second (of %d reads): %q", len(torn), reads, torn[0])
	}
	for _, c := range []struct {
		path string
		want fs.FileMode
	}{{filepath.Join(dir, recovery.DirName), 0o700}, {path, 0o600}} {
		info, err := os.Stat(c.path)
		if err != nil || info.Mode().Perm() != c.want {
			t.Errorf("%s: %v, %v, want mode %o", c.path, info, err, c.want)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, recovery.DirName))
	if len(entries) != 1 || entries[0].Name() != recovery.JournalFile {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("recovery/ holds %v, want the journal alone: no temporary file is left behind", names)
	}
	got, err := store.Load()
	if err != nil || got.Session.ID != "ses_0002" || len(got.Leases) != 2000 {
		t.Errorf("Load = %v, %v, want the second document", got.Session, err)
	}

	// The cleanup of the worker removes input/ and clone/ and leaves recovery/ alone.
	for _, name := range []string{inputDirName, cloneDirName} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r := New(Config{})
	a := &attempt{req: jobs.Request{Dir: dir}}
	r.cleanup(context.Background(), a, &outcome{})
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the journal is gone after the cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cloneDirName)); err == nil {
		t.Error("the cleanup left clone/ where it was")
	}
}

// TestAJournalWrittenAfterAFullFixDeliveryDecodesWithEveryDocumentedField_TS_14_2 verifies TS-14-2.
// Verifies: 14-REQ-1.2
func TestAJournalWrittenAfterAFullFixDeliveryDecodesWithEveryDocumentedField_TS_14_2(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("the delivery: %s, %v", res.Envelope, err)
	}
	j := journalOf(t, e)
	req := e.request(jobs.KindFix, issueInput("42"))
	if j.Version != recovery.FormatVersion || j.Job != req.JobID || j.Attempt != 1 || j.Kind != "fix" {
		t.Errorf("version %d job %q attempt %d kind %q", j.Version, j.Job, j.Attempt, j.Kind)
	}
	if j.CloneDir != "clone" || j.HubURL != e.hub.CloneURL(slug) || strings.Contains(j.HubURL, "@") || j.Forge != "Org/Repo" {
		t.Errorf("clone dir %q, hub url %q, forge %q, want clone, the clone URL without user information, and Org/Repo", j.CloneDir, j.HubURL, j.Forge)
	}
	s := j.Settings
	if s.Brain != "claude" || s.Model == "" || s.Land != "pr" || s.Budget != 10 || s.MaxTurns != 150 {
		t.Errorf("settings = %+v, want the brain, the model, the landing mode, the budget and the turn bound of the fix", s)
	}
	sessions := e.hub.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("%d sessions", len(sessions))
	}
	if j.Run.ID != sessions[0].RunID || j.Run.Base != "main" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(j.Run.BaseHead) {
		t.Errorf("run = %+v, want the run id of the session, the base main and its head", j.Run)
	}
	if j.Session.ID != sessions[0].ID || j.Session.RunID != sessions[0].RunID || !j.Session.Completed {
		t.Errorf("session = %+v, want the session of the hub, completed", j.Session)
	}
	leases := e.hub.Leases()
	if len(j.Leases) != 1 || len(leases) != 1 || j.Leases[0].SandboxID != leases[0].ID || !j.Leases[0].Released || j.Leases[0].ExpiresAt.IsZero() {
		t.Errorf("leases = %+v, want the one lease, with its end, released", j.Leases)
	}
	if len(j.Deliveries) != 1 || j.Deliveries[0].Count != 1 || j.Deliveries[0].Started.IsZero() || j.Deliveries[0].Ending != recovery.EndingOutcome {
		t.Errorf("deliveries = %+v, want one, started, that ended in an outcome", j.Deliveries)
	}
	plan := j.Landing.Plan
	if plan == nil || plan.Branch == "" || plan.Commit == "" || plan.Outcome != "landed" || plan.Status == "" || plan.Verdict == "" || plan.Title == "" || plan.Body == "" {
		t.Fatalf("landing plan = %+v, want the branch, the commit, the outcome, the status, the verdict and the pull request", plan)
	}
	if !j.Landing.Pushed || !j.Landing.HubPush || j.Landing.PullRequest == nil || j.Landing.PullRequest.Number <= 0 || j.Landing.PullRequest.URL == "" {
		t.Errorf("landing = %+v, want pushed, published, and the pull request", j.Landing)
	}
	if !j.Mirror.StatusLabels || !j.Mirror.Comment {
		t.Errorf("mirror = %+v, want both steps done", j.Mirror)
	}
	if !j.Report.UsagePosted || !j.Report.EventsPosted || !j.Report.SessionCompleted {
		t.Errorf("report = %+v, want the three steps done", j.Report)
	}

	// Every documented field is in the document under its name.
	var raw map[string]json.RawMessage
	data, _ := os.ReadFile(filepath.Join(e.dir, recovery.DirName, recovery.JournalFile))
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "job", "attempt", "kind", "clone_dir", "clone_url", "forge_repository", "settings", "run", "session", "leases", "deliveries", "spend", "landing", "mirror", "report"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("journal.json has no %q", key)
		}
	}
	var spend map[string]json.RawMessage
	if err := json.Unmarshal(raw["spend"], &spend); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cost_usd", "cost_source", "input_tokens", "output_tokens", "turns"} {
		if _, ok := spend[key]; !ok {
			t.Errorf("the spend has no %q", key)
		}
	}
}

// TestNoFileUnderRecoveryEverHoldsACredential_TS_14_3 verifies TS-14-3: whatever a delivery stops at,
// and whether it is then run again, shut down or ended in a transient error, no file under recovery/ holds
// the PAT, a token of a lease, an Authorization header or a URL with user information. The sequences
// are every stage that a delivery can be stopped at, each followed by the next delivery, and the two
// endings that have no outcome.
// Verifies: 14-REQ-1.3
func TestNoFileUnderRecoveryEverHoldsACredential_TS_14_3(t *testing.T) {
	stages := []string{"session", "clone", "lease", "briefed", "submission_accepted", "applied", "plan_recorded", "pushed", "pull_request"}
	for _, stage := range stages {
		t.Run("stopped at "+stage+" and run again", func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			e.runner.abortAfter = stage

			_, err := e.runner.Run(context.Background(), e.request(jobs.KindFix, issueInput("42")))
			var stopped stageAbort
			if !errors.As(err, &stopped) || stopped.stage != stage {
				t.Fatalf("the first delivery returned %v, want it stopped at %s", err, stage)
			}
			requireNoCredential(t, e)

			e.runner.abortAfter = ""
			if _, err := e.run(e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Delivery = 2 })); err != nil {
				t.Fatalf("the second delivery returned %v", err)
			}
			requireNoCredential(t, e)
		})
	}
	t.Run("a transient error and then a delivery", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Gateway().RefuseBoxes("no room")
		if _, err := e.run(e.request(jobs.KindFix, issueInput("42"))); !jobs.IsTransient(err) {
			t.Fatalf("the first delivery returned %v, want a transient error", err)
		}
		requireNoCredential(t, e)
		e.hub.Gateway().RefuseBoxes("")
		if _, err := e.run(e.request(jobs.KindFix, issueInput("42"), func(r *jobs.Request) { r.Delivery = 2 })); err != nil {
			t.Fatalf("the second delivery returned %v", err)
		}
		requireNoCredential(t, e)
	})
}

// TestWhenTheLandingPlanCannotBeJournalledNothingGoesToHubsGitServerOrTheForge_TS_14_4 verifies
// TS-14-4: the journal cannot be written when the plan of the landing is, so the delivery pushes
// nothing, does not call hub's branch push route, opens no pull request, posts no comment, keeps the clone
// and returns a transient error of category io. The labels of the mark and of the transient error are
// the worker's own and are not a landing.
// Verifies: 14-REQ-1.4
func TestWhenTheLandingPlanCannotBeJournalledNothingGoesToHubsGitServerOrTheForge_TS_14_4(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.runner.journalFault = func(step string) error {
		if step == stepLandingPlan {
			return errors.New("no space left on the device")
		}
		return nil
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	f := transient(err)
	if f == nil || f.Category != toolio.CategoryIO || !f.Retryable || len(res.Envelope) != 0 {
		t.Fatalf("result %q, error %v, want a transient error of category io that is retryable and no envelope", res.Envelope, err)
	}
	if n := receivePacks(e); n != 0 {
		t.Errorf("%d pushes to hub's git server, want none", n)
	}
	if pushes := e.hub.BranchPushes(); len(pushes) != 0 {
		t.Errorf("hub's branch push route was called %d times, want never", len(pushes))
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 0 {
		t.Errorf("%d pull requests, want none", len(pulls))
	}
	if issue := e.issueNow(); len(issue.Comments) != 0 {
		t.Errorf("issue 42 has %d comments, want none", len(issue.Comments))
	}
	if _, err := os.Stat(filepath.Join(e.dir, cloneDirName)); err != nil {
		t.Errorf("clone/ is not kept: %v", err)
	}
	if got := localBranches(t, e.dir); len(got) != 1 {
		t.Errorf("the clone has the branches %v, want the one the landing committed on, kept for the next delivery", got)
	}
}

// TestAJournalWriteThatFailsOtherThanThePlanIsOneWarningAndTheDeliveryGoesOn_TS_14_5 verifies TS-14-5.
// Verifies: 14-REQ-1.5
func TestAJournalWriteThatFailsOtherThanThePlanIsOneWarningAndTheDeliveryGoesOn_TS_14_5(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.runner.journalFault = func(step string) error {
		if step != stepLandingPlan {
			return errors.New("input/output error")
		}
		return nil
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil {
		t.Fatal(err)
	}
	env := envelope(t, res)
	n := 0
	for _, c := range codes(env) {
		if c == "journal_unwritable" {
			n++
		}
	}
	if env["status"] != "done" || n != 1 {
		t.Errorf("status %v with %d journal_unwritable warnings (%v), want a done envelope with exactly one", env["status"], n, codes(env))
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 {
		t.Errorf("%d pull requests, want the one an uninterrupted delivery opens", len(pulls))
	}
}

// TestADeliveryStoppedForAShutdownOrEndedInATransientErrorPostsNothingAndLeavesTheSessionOpen_TS_14_6
// verifies TS-14-6.
// Verifies: 14-REQ-1.6
func TestADeliveryStoppedForAShutdownOrEndedInATransientErrorPostsNothingAndLeavesTheSessionOpen_TS_14_6(t *testing.T) {
	check := func(t *testing.T, e *env, ending string) {
		t.Helper()
		sessions := e.hub.Sessions()
		if len(sessions) != 1 {
			t.Fatalf("%d sessions", len(sessions))
		}
		s := sessions[0]
		if s.Status != "open" || len(s.Usage) != 0 || s.CompletedSeq != 0 {
			t.Errorf("session %+v, want it open with no usage and no completion", s)
		}
		if batches := e.hub.EventBatches(slug, s.RunID); len(batches) != 0 {
			t.Errorf("%d batches of events were posted, want none", len(batches))
		}
		j := journalOf(t, e)
		if len(j.Leases) != 1 || !j.Leases[0].Released || len(j.Deliveries) != 1 || j.Deliveries[0].Ending != ending {
			t.Errorf("journal: leases %+v, deliveries %+v, want the lease released and the delivery ended %s", j.Leases, j.Deliveries, ending)
		}
		if j.Session.Completed || j.Report.SessionCompleted || j.Report.UsagePosted || j.Report.EventsPosted {
			t.Errorf("journal: session %+v, report %+v, want nothing reported", j.Session, j.Report)
		}
		if _, err := os.Stat(filepath.Join(e.dir, recovery.DirName, recovery.OutcomeFile)); err == nil {
			t.Error("recovery/outcome.json exists")
		}
		if _, err := os.Stat(filepath.Join(e.dir, cloneDirName)); err != nil {
			t.Errorf("clone/ is gone: %v", err)
		}
	}

	t.Run("a shutdown while the change is measured", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "measuring")
		files, steps := slowGateFiles(marker)
		e := loopEnv(t, files, nil, nil, steps...)
		measuring := func() bool { _, err := os.Stat(marker); return err == nil }

		res, err, _, _ := runUntil(t, e, e.request(jobs.KindFix, issueInput("42")), measuring, jobs.ErrShutdown)

		if !errors.Is(err, jobs.ErrShutdown) || len(res.Envelope) != 0 {
			t.Fatalf("result %q, error %v, want the shutdown", res.Envelope, err)
		}
		check(t, e, recovery.EndingShutdown)
		if got := localBranches(t, e.dir); len(got) != 1 {
			t.Errorf("the clone has the branches %v, want the one the interrupted change was parked on", got)
		}
		runs, _ := filepath.Glob(filepath.Join(e.dir, cloneDirName, ".agent-fox", "run", "*", "ledger.jsonl"))
		if len(runs) != 1 {
			t.Errorf("the run directories hold %d ledgers, want the one of the run", len(runs))
		}
	})

	t.Run("a transient error", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Gateway().RefuseBoxes("no room for another box")

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if !jobs.IsTransient(err) || len(res.Envelope) != 0 {
			t.Fatalf("result %q, error %v, want a transient error", res.Envelope, err)
		}
		check(t, e, recovery.EndingTransient)
	})
}

// TestAShutdownMakesNoWriteToTheForgeAndATransientErrorRemovesTheInProgressLabel_TS_14_7 verifies
// TS-14-7.
// Verifies: 14-REQ-1.7
func TestAShutdownMakesNoWriteToTheForgeAndATransientErrorRemovesTheInProgressLabel_TS_14_7(t *testing.T) {
	t.Run("a shutdown", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "begun")
		e := loopEnv(t, blockFiles(marker), nil, nil, blockStep())
		begun := func() bool { _, err := os.Stat(marker); return err == nil }

		_, err, _, before := runUntil(t, e, e.request(jobs.KindFix, issueInput("42")), begun, jobs.ErrShutdown)

		if !errors.Is(err, jobs.ErrShutdown) {
			t.Fatalf("error = %v, want the shutdown", err)
		}
		reqs := e.hub.Requests()
		for _, r := range reqs[before:] {
			if r.Method != "GET" && (strings.Contains(r.Path, "/issues/") || strings.HasSuffix(r.Path, "/pulls")) {
				t.Errorf("a write to the forge after the shutdown: %s %s", r.Method, r.Path)
			}
		}
		if issue := e.issueNow(); !slices.Equal(issue.Labels, []string{"af:in-progress"}) {
			t.Errorf("issue 42 has the labels %v, want in-progress kept", issue.Labels)
		}
	})

	t.Run("a transient error", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Gateway().RefuseBoxes("no room for another box")

		_, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if !jobs.IsTransient(err) {
			t.Fatalf("error = %v, want a transient error", err)
		}
		if issue := e.issueNow(); slices.Contains(issue.Labels, "af:in-progress") {
			t.Errorf("issue 42 has the labels %v, want in-progress taken off", issue.Labels)
		}
	})
}

var _ = fakehub.Issue{}

// TestWhatTheJournalHoldsOfAURLAndOfATextIsFreeOfCredentials pins the two functions the journal is
// kept free of credentials with: the URL of the workspace loses its user information, and a text loses
// the PAT, the token of the lease, the user information of a URL and an Authorization header.
// Verifies: 14-REQ-1.3
func TestWhatTheJournalHoldsOfAURLAndOfATextIsFreeOfCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://agent-fox:s3cret@hub.example.com/git/acme.git": "https://hub.example.com/git/acme.git",
		"https://hub.example.com/git/acme.git":                  "https://hub.example.com/git/acme.git",
		"http://127.0.0.1:8080/git/a%2Fb.git":                   "http://127.0.0.1:8080/git/a%2Fb.git",
	} {
		if got := stripUserinfo(in); got != want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", in, got, want)
		}
	}
	a := &attempt{pat: "pat-0123456789", lease: nil}
	text := "clone of https://agent-fox:pat-0123456789@hub.example.com/x failed with Authorization: Basic YWdlbnQ6cGF0 and the pat-0123456789 itself"
	got := a.secretFree(text)
	for _, leak := range []string{"pat-0123456789", "agent-fox:", "YWdlbnQ6cGF0"} {
		if strings.Contains(got, leak) {
			t.Errorf("secretFree left %q in %q", leak, got)
		}
	}
	if !strings.Contains(got, "https://hub.example.com/x") {
		t.Errorf("secretFree = %q, want the URL kept without its user information", got)
	}
}
