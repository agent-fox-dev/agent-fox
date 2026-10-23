package reconcile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/reconcile"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

var ctx = context.Background()

// The two workspaces of the tests, and their tokens.
const (
	acme    = "acme"
	beta    = "beta"
	patAcme = "pat-ACME-0123456789"
	patBeta = "pat-BETA-9876543210"
)

const sandboxes = "/api/v1/workspaces/%s/sandboxes"

// syncBuffer is a log destination that the goroutines of the reconciler may write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// records are the log records written so far.
func (s *syncBuffer) records() []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.String()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil {
			out = append(out, rec)
		}
	}
	return out
}

// level are the records of the level.
func (s *syncBuffer) level(level string) []map[string]any {
	var out []map[string]any
	for _, rec := range s.records() {
		if rec["level"] == level {
			out = append(out, rec)
		}
	}
	return out
}

// fakeJobs is the jobs layer as the reconciliation pass sees it, over a directory of attempts.
type fakeJobs struct {
	dir string

	mu      sync.Mutex
	known   map[string]bool
	running map[string]bool
	ended   []jobs.AttemptRef
}

func (f *fakeJobs) Known(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.known[id], nil
}

func (f *fakeJobs) Running(id string, n int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[id+"/"+strconv.Itoa(n)]
}

func (f *fakeJobs) EndedAttempts(context.Context) ([]jobs.AttemptRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ended), nil
}

func (f *fakeJobs) AttemptDir(id string, n int) string {
	return filepath.Join(f.dir, id, strconv.Itoa(n))
}

// fakeSubs are the subscriptions, with the tokens that open and the ones that do not.
type fakeSubs struct {
	slugs    []string
	tokens   map[string]string
	tokenErr map[string]error
}

func (f *fakeSubs) List(context.Context) ([]store.Subscription, error) {
	var out []store.Subscription
	for _, s := range f.slugs {
		out = append(out, store.Subscription{Slug: s})
	}
	return out, nil
}

func (f *fakeSubs) Token(_ context.Context, slug string) (string, error) {
	if err := f.tokenErr[slug]; err != nil {
		return "", err
	}
	return f.tokens[slug], nil
}

// world is the fake hub, the jobs and the subscriptions, and a reconciler over them.
type world struct {
	t      *testing.T
	hub    *fakehub.Hub
	client *hub.Client
	jobs   *fakeJobs
	subs   *fakeSubs
	logs   *syncBuffer
	rec    *reconcile.Reconciler
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t, hub: fakehub.New(t), logs: &syncBuffer{}}
	w.hub.AddWorkspace(t, fakehub.Workspace{Slug: acme, PAT: patAcme})
	w.hub.AddWorkspace(t, fakehub.Workspace{Slug: beta, PAT: patBeta})
	w.client = hub.New(w.hub.URL, "1")
	w.jobs = &fakeJobs{dir: t.TempDir(), known: map[string]bool{}, running: map[string]bool{}}
	w.subs = &fakeSubs{slugs: []string{acme, beta}, tokens: map[string]string{acme: patAcme, beta: patBeta}, tokenErr: map[string]error{}}
	w.rec = reconcile.New(reconcile.Config{Subscriptions: w.subs, Hub: w.client, Jobs: w.jobs, Logger: slog.New(slog.NewJSONHandler(w.logs, nil))})
	return w
}

// pass runs one pass.
func (w *world) pass() { w.t.Helper(); w.rec.Pass(ctx) }

// know makes the job one of the database.
func (w *world) know(id string) { w.jobs.mu.Lock(); w.jobs.known[id] = true; w.jobs.mu.Unlock() }

// seedLease puts a lease of the attempt in the hub.
func (w *world) seedLease(slug, job string, attempt int) fakehub.Lease {
	w.t.Helper()
	return w.hub.SeedLease(slug, map[string]string{"job": job, "attempt": strconv.Itoa(attempt)}, "fix")
}

// listed says whether the lease is in the listing of the workspace.
func (w *world) listed(slug, pat, id string) bool {
	w.t.Helper()
	leases, err := w.client.ListSandboxes(ctx, pat, slug)
	if err != nil {
		w.t.Fatal(err)
	}
	return slices.ContainsFunc(leases, func(l hub.LeaseInfo) bool { return l.ID == id })
}

// attempt is an ended attempt, with the journal it left.
type attempt struct {
	job       string
	n         int
	slug, pat string
	state     jobs.AttemptState
	journal   func(j *recovery.Journal) // changes the journal
	outcome   *recovery.Outcome         // the outcome it stored, or none
	garbled   bool
}

// end makes an ended attempt: a session on hub that is open, and the journal and the outcome files of the attempt.
func (w *world) end(a attempt) (session string, runID string) {
	w.t.Helper()
	runID = fmt.Sprintf("20261022_10%04d_abcdef", a.n)
	session, err := w.client.OpenSession(ctx, a.pat, hub.SessionRequest{WorkspaceSlug: a.slug, Archetype: "fix", Model: "model-1", RunID: runID})
	if err != nil {
		w.t.Fatal(err)
	}
	if a.state == "" {
		a.state = jobs.AttemptFailed
	}
	w.know(a.job)
	w.jobs.mu.Lock()
	w.jobs.ended = append(w.jobs.ended, jobs.AttemptRef{JobID: a.job, Attempt: a.n, Workspace: a.slug, State: a.state})
	w.jobs.mu.Unlock()
	dir := w.jobs.AttemptDir(a.job, a.n)
	if err := os.MkdirAll(dir, 0o700); err != nil { // the jobs layer makes the directory of an attempt
		w.t.Fatal(err)
	}
	store := recovery.NewStore(dir)
	if a.garbled {
		if err := os.MkdirAll(store.Dir(), 0o700); err != nil {
			w.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(store.Dir(), recovery.JournalFile), []byte("{garbage"), 0o600); err != nil {
			w.t.Fatal(err)
		}
		return session, runID
	}
	j := recovery.Journal{
		Version: recovery.FormatVersion, Job: a.job, Attempt: a.n, Kind: "fix",
		Settings: recovery.Settings{Brain: "scripted", Model: "model-1"},
		Session:  recovery.Session{ID: session, RunID: runID},
		Spend:    recovery.Spend{CostUSD: 0.5, CostSource: "reported", InputTokens: 1200, OutputTokens: 300, Turns: 7},
	}
	if a.journal != nil {
		a.journal(&j)
	}
	if err := store.Write(j); err != nil {
		w.t.Fatal(err)
	}
	if a.outcome != nil {
		o := *a.outcome
		o.Version, o.Job, o.Attempt = recovery.FormatVersion, a.job, a.n
		if err := store.WriteOutcome(o); err != nil {
			w.t.Fatal(err)
		}
	}
	return session, runID
}

// journalOf is the journal of the attempt.
func (w *world) journalOf(job string, n int) recovery.Journal {
	w.t.Helper()
	j, err := recovery.NewStore(w.jobs.AttemptDir(job, n)).Load()
	if err != nil {
		w.t.Fatal(err)
	}
	return j
}

// sessionOf is the session as the hub holds it.
func (w *world) sessionOf(id string) fakehub.Session {
	w.t.Helper()
	for _, s := range w.hub.Sessions() {
		if s.ID == id {
			return s
		}
	}
	w.t.Fatalf("hub has no session %s", id)
	return fakehub.Session{}
}

// TestAnOrphanLeaseOfAKnownAttemptWithNoRunnerIsReleasedAndAbsentFromTheNextListing_TS_14_51 verifies TS-14-51.
// Verifies: 14-REQ-8.2
func TestAnOrphanLeaseOfAKnownAttemptWithNoRunnerIsReleasedAndAbsentFromTheNextListing_TS_14_51(t *testing.T) {
	t.Run("a lease", func(t *testing.T) {
		w := newWorld(t)
		w.know("job_1")
		orphan := w.seedLease(acme, "job_1", 1)

		w.pass()

		if got := w.hub.Deleted(); !slices.Equal(got, []string{orphan.ID}) {
			t.Errorf("the hub released %v, want the orphan %s once", got, orphan.ID)
		}
		if w.listed(acme, patAcme, orphan.ID) {
			t.Error("the orphan is still listed")
		}
		if n := len(w.hub.RequestsTo("DELETE", fmt.Sprintf(sandboxes, acme)+"/"+orphan.ID)); n != 1 {
			t.Errorf("%d DELETE requests, want one", n)
		}
	})

	t.Run("a lease that hub does not find", func(t *testing.T) {
		w := newWorld(t)
		w.know("job_1")
		orphan := w.seedLease(acme, "job_1", 1)
		w.hub.Inject("DELETE", fmt.Sprintf(sandboxes, acme)+"/"+orphan.ID, fakehub.Fault{Status: 404})

		w.pass()

		if warns := w.logs.level("WARN"); len(warns) != 0 {
			t.Errorf("the pass logged %v, want a lease that hub does not find to be a lease that is released", warns)
		}
		if infos := w.logs.level("INFO"); len(infos) != 1 || infos[0]["sandbox_id"] != orphan.ID {
			t.Errorf("the pass logged %v at INFO, want the release", infos)
		}
	})

	t.Run("a lease that cannot be released is tried again by the next pass", func(t *testing.T) {
		w := newWorld(t)
		w.know("job_1")
		orphan := w.seedLease(acme, "job_1", 1)
		w.hub.Inject("DELETE", fmt.Sprintf(sandboxes, acme)+"/"+orphan.ID, fakehub.Fault{Status: 500})

		w.pass()

		if len(w.hub.Deleted()) != 0 || len(w.logs.level("WARN")) != 1 || len(w.logs.level("INFO")) != 0 {
			t.Fatalf("deleted %v, warnings %v, info %v, want the failure logged and the lease held, with no release said", w.hub.Deleted(), w.logs.level("WARN"), w.logs.level("INFO"))
		}
		w.hub.Clear()
		w.pass()
		if got := w.hub.Deleted(); !slices.Equal(got, []string{orphan.ID}) {
			t.Errorf("the hub released %v, want the orphan released by the next pass", got)
		}
	})
}

// TestLeasesWithNoJobLabelAnUnknownJobOrARunningAttemptAreNeverReleased_TS_14_52 verifies TS-14-52, a property: for
// generated leases that the pass has to keep, the hub is asked to release none of them across several passes, and
// the orphan among them is released once.
// Verifies: 14-REQ-8.3
func TestLeasesWithNoJobLabelAnUnknownJobOrARunningAttemptAreNeverReleased_TS_14_52(t *testing.T) {
	rng := rand.New(rand.NewSource(14052))
	for i := range 8 {
		t.Run(fmt.Sprintf("generation %d", i), func(t *testing.T) {
			w := newWorld(t)
			w.know("job_known")
			w.jobs.running["job_known/2"] = true
			w.jobs.running["job_known/3"] = true
			var keep []string
			for range 4 + rng.Intn(8) {
				var l fakehub.Lease
				switch rng.Intn(7) {
				case 0:
					l = w.hub.SeedLease(acme, nil, "fix")
				case 1:
					l = w.hub.SeedLease(acme, map[string]string{"owner": "someone else"}, "fix")
				case 2:
					l = w.seedLease(acme, fmt.Sprintf("job_unknown_%d", rng.Intn(100)), 1+rng.Intn(3))
				case 3:
					l = w.seedLease(acme, "job_known", 2+rng.Intn(2)) // an attempt a runner of this process carries out
				case 4:
					l = w.hub.SeedLease(acme, map[string]string{"job": "job_known"}, "fix") // no attempt is named
				case 5:
					l = w.hub.SeedLease(acme, map[string]string{"job": "job_known", "attempt": "next"}, "fix") // none that is a number
				default:
					l = w.hub.SeedLease(acme, map[string]string{"job": "job_known", "attempt": "0"}, "fix") // attempts begin at 1
				}
				keep = append(keep, l.ID)
			}
			orphan := w.seedLease(acme, "job_known", 1)

			for range 3 {
				w.pass()
			}

			for _, id := range keep {
				if slices.Contains(w.hub.Deleted(), id) {
					t.Errorf("the lease %s was released", id)
				}
			}
			if got := w.hub.Deleted(); !slices.Equal(got, []string{orphan.ID}) {
				t.Errorf("the hub released %v, want the orphan alone, once", got)
			}
		})
	}
}

// TestAnEndedAttemptWhoseJournalRecordsAnOpenSessionGetsItsUnpostedUsageAndACompletionExactlyOnce_TS_14_53 verifies
// TS-14-53.
// Verifies: 14-REQ-8.4
func TestAnEndedAttemptWhoseJournalRecordsAnOpenSessionGetsItsUnpostedUsageAndACompletionExactlyOnce_TS_14_53(t *testing.T) {
	w := newWorld(t)
	failed, failedRun := w.end(attempt{job: "job_a", n: 1, slug: acme, pat: patAcme, state: jobs.AttemptFailed})
	needsHuman, humanRun := w.end(attempt{job: "job_b", n: 1, slug: acme, pat: patAcme, state: jobs.AttemptNeedsHuman, outcome: &recovery.Outcome{
		Envelope: `{"schema_version":"5.0.0","tool":"fix","status":"needs_human","error":null}`,
	}})
	posted, _ := w.end(attempt{job: "job_c", n: 2, slug: acme, pat: patAcme, state: jobs.AttemptFailed, journal: func(j *recovery.Journal) {
		j.Report.UsagePosted = true // a delivery posted the usage, and was stopped before it completed the session
	}})
	idle, _ := w.end(attempt{job: "job_e", n: 1, slug: acme, pat: patAcme, state: jobs.AttemptFailed, journal: func(j *recovery.Journal) {
		j.Spend = recovery.Spend{} // no brain ran
	}})
	timedOut, _ := w.end(attempt{job: "job_d", n: 1, slug: acme, pat: patAcme, state: jobs.AttemptFailed, outcome: &recovery.Outcome{
		Envelope: `{"schema_version":"5.0.0","tool":"fix","status":"failed","error":{"category":"timeout","message":"ran out of time","retryable":false}}`,
	}})

	w.pass()
	w.pass()

	for _, c := range []struct {
		name, id, status string
		usage            int
	}{{"no outcome", failed, "failed", 1}, {"an outcome that is a question", needsHuman, "completed", 1}, {"usage posted already", posted, "failed", 0}, {"no brain ran", idle, "failed", 0}, {"a timeout", timedOut, "timeout", 1}} {
		s := w.sessionOf(c.id)
		if n := len(w.hub.RequestsTo("POST", "/api/v1/sessions/"+c.id+"/complete")); n != 1 {
			t.Errorf("%s: the session was completed %d times, want once across both passes", c.name, n)
		}
		if s.Status != c.status || s.CompletedSeq == 0 || len(s.Usage) != c.usage {
			t.Errorf("%s: the session is %q with %d usage records, want %q completed once with %d", c.name, s.Status, len(s.Usage), c.status, c.usage)
		}
		if c.usage == 1 && (s.Usage[0].Model != "model-1" || s.Usage[0].Brain != "scripted" || s.Usage[0].Turns != 7 || s.Usage[0].CostUSD != 0.5 || s.Usage[0].InputTokens != 1200) {
			t.Errorf("%s: the usage is %+v, want what the journal records as spent", c.name, s.Usage[0])
		}
	}
	if s := w.sessionOf(failed); !strings.Contains(s.Message, "without") {
		t.Errorf("the failed session says %q, want why", s.Message)
	}
	for _, c := range []struct {
		job string
		n   int
	}{{"job_a", 1}, {"job_b", 1}, {"job_c", 2}, {"job_d", 1}, {"job_e", 1}} {
		j := w.journalOf(c.job, c.n)
		if !j.Session.Completed || !j.Report.UsagePosted || !j.Report.SessionCompleted {
			t.Errorf("the journal of %s/%d records session %+v and report %+v, want both steps", c.job, c.n, j.Session, j.Report)
		}
	}
	for _, run := range []string{failedRun, humanRun} {
		if batches := w.hub.EventBatches(acme, run); len(batches) != 0 {
			t.Errorf("%d batches of events were posted for %s, want none", len(batches), run)
		}
	}
}

// TestAnEndedAttemptWhoseJournalDoesNotReadOrIsNotItsOwnIsLeftUntouched_TS_14_54 verifies TS-14-54.
// Verifies: 14-REQ-8.5
func TestAnEndedAttemptWhoseJournalDoesNotReadOrIsNotItsOwnIsLeftUntouched_TS_14_54(t *testing.T) {
	w := newWorld(t)
	garbled, _ := w.end(attempt{job: "job_a", n: 1, slug: acme, pat: patAcme, garbled: true})
	other, _ := w.end(attempt{job: "job_b", n: 1, slug: acme, pat: patAcme, journal: func(j *recovery.Journal) { j.Job = "job_other" }})
	running, _ := w.end(attempt{job: "job_c", n: 1, slug: acme, pat: patAcme})
	w.jobs.running["job_c/1"] = true
	noJournal := attempt{job: "job_d", n: 1, slug: acme, pat: patAcme}
	w.know(noJournal.job)
	w.jobs.ended = append(w.jobs.ended, jobs.AttemptRef{JobID: "job_d", Attempt: 1, Workspace: acme, State: jobs.AttemptFailed})

	w.pass()

	for name, id := range map[string]string{"a journal that does not read": garbled, "a journal of another job": other, "an attempt in a runner": running} {
		if s := w.sessionOf(id); s.Status != "open" || len(s.Usage) != 0 || s.CompletedSeq != 0 {
			t.Errorf("%s: the session is %+v, want it untouched", name, s)
		}
	}
}

// TestAFailingWorkspaceIsLoggedWithoutItsTokenAndDoesNotStopTheOthersAndTheNextPassCatchesUp_TS_14_55 verifies
// TS-14-55.
// Verifies: 14-REQ-8.6
func TestAFailingWorkspaceIsLoggedWithoutItsTokenAndDoesNotStopTheOthersAndTheNextPassCatchesUp_TS_14_55(t *testing.T) {
	cases := map[string]func(w *world){
		"an unreadable token": func(w *world) { w.subs.tokenErr[acme] = store.ErrUnreadable },
		"a subscription that is gone": func(w *world) {
			w.subs.tokenErr[acme] = store.ErrNotSubscribed
		},
		"hub answers 500": func(w *world) { w.hub.Inject("GET", fmt.Sprintf(sandboxes, acme), fakehub.Fault{Status: 500}) },
		"hub answers 401": func(w *world) { w.hub.Inject("GET", fmt.Sprintf(sandboxes, acme), fakehub.Fault{Status: 401}) },
		"hub answers 403": func(w *world) { w.hub.Inject("GET", fmt.Sprintf(sandboxes, acme), fakehub.Fault{Status: 403}) },
		"hub hangs up":    func(w *world) { w.hub.Inject("GET", fmt.Sprintf(sandboxes, acme), fakehub.Fault{Hangup: true}) },
		"hub answers with junk": func(w *world) {
			w.hub.Inject("GET", fmt.Sprintf(sandboxes, acme), fakehub.Fault{Status: 200, Body: "{not a list"})
		},
		"hub echoes the token": func(w *world) {
			w.hub.Inject("GET", fmt.Sprintf(sandboxes, acme), fakehub.Fault{Status: 500, Body: `{"error":{"code":"internal","message":"cannot serve ` + patAcme + `"}}`})
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.know("job_1")
			orphanA := w.seedLease(acme, "job_1", 1)
			orphanB := w.seedLease(beta, "job_1", 1)
			spoil(w)

			w.pass()

			if got := w.hub.Deleted(); !slices.Equal(got, []string{orphanB.ID}) {
				t.Fatalf("the hub released %v, want only the orphan of the workspace that works", got)
			}
			var named bool
			for _, rec := range w.logs.level("WARN") {
				if rec["workspace"] == acme {
					named = true
				}
			}
			if !named {
				t.Errorf("no WARN names the workspace: %s", w.logs.String())
			}
			for _, token := range []string{patAcme, patBeta} {
				if strings.Contains(w.logs.String(), token) {
					t.Errorf("a log line holds the token %s: %s", token, w.logs.String())
				}
			}

			w.hub.Clear()
			delete(w.subs.tokenErr, acme)
			w.pass()

			if got := w.hub.Deleted(); !slices.Equal(got, []string{orphanB.ID, orphanA.ID}) {
				t.Errorf("the hub released %v, want the orphan that was missed released by the next pass", got)
			}
		})
	}
}

// TestEachReleaseAndEachCompletedSessionIsLoggedAtInfoWithTheWorkspaceTheIdTheJobAndTheAttempt_TS_14_56 verifies
// TS-14-56.
// Verifies: 14-REQ-8.7
func TestEachReleaseAndEachCompletedSessionIsLoggedAtInfoWithTheWorkspaceTheIdTheJobAndTheAttempt_TS_14_56(t *testing.T) {
	w := newWorld(t)
	w.know("job_1")
	orphan := w.seedLease(acme, "job_1", 3)
	session, _ := w.end(attempt{job: "job_2", n: 2, slug: acme, pat: patAcme})

	w.pass()

	infos := w.logs.level("INFO")
	if len(infos) != 2 {
		t.Fatalf("%d INFO lines, want one for the release and one for the completion: %s", len(infos), w.logs.String())
	}
	release, completion := infos[0], infos[1]
	if release["workspace"] != acme || release["sandbox_id"] != orphan.ID || release["job"] != "job_1" || fmt.Sprint(release["attempt"]) != "3" {
		t.Errorf("the release is logged as %v, want the workspace, the sandbox, the job and the attempt", release)
	}
	if completion["workspace"] != acme || completion["session_id"] != session || completion["job"] != "job_2" || fmt.Sprint(completion["attempt"]) != "2" {
		t.Errorf("the completion is logged as %v, want the workspace, the session, the job and the attempt", completion)
	}
}

// slowHub is the hub client with a listing that takes its time, and that counts how many are in it at once.
type slowHub struct {
	*hub.Client
	delay    time.Duration
	inFlight atomic.Int32
	most     atomic.Int32
	listings atomic.Int32
}

func (s *slowHub) ListSandboxes(c context.Context, pat, slug string) ([]hub.LeaseInfo, error) {
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		most := s.most.Load()
		if n <= most || s.most.CompareAndSwap(most, n) {
			break
		}
	}
	s.listings.Add(1)
	select {
	case <-time.After(s.delay):
	case <-c.Done():
		return nil, c.Err()
	}
	return s.Client.ListSandboxes(c, pat, slug)
}

// TestThePassRunsAtStartAndOnEveryTickButNeverTwoAtOnceAndStopsWhenItIsStopped_TS_14_50 verifies the part of TS-14-50
// that does not need the service: the first pass is made at once, ticks that come while a pass is running start no
// other, and a pass that is waiting for hub is stopped by the context.
// Verifies: 14-REQ-8.1
func TestThePassRunsAtStartAndOnEveryTickButNeverTwoAtOnceAndStopsWhenItIsStopped_TS_14_50(t *testing.T) {
	w := newWorld(t)
	slow := &slowHub{Client: w.client, delay: 80 * time.Millisecond}
	w.subs.slugs = []string{acme}
	w.rec = reconcile.New(reconcile.Config{Subscriptions: w.subs, Hub: slow, Jobs: w.jobs, Logger: slog.New(slog.NewJSONHandler(w.logs, nil)), Interval: 5 * time.Millisecond})
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); w.rec.Run(run) }()

	deadline := time.Now().Add(10 * time.Second)
	for slow.listings.Load() < 3 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d passes in 10 seconds", slow.listings.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if most := slow.most.Load(); most != 1 {
		t.Errorf("%d listings were in flight at once, want the passes to follow one another", most)
	}
	stopped := time.Now()
	stop()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after it was stopped")
	}
	if took := time.Since(stopped); took > 2*time.Second {
		t.Errorf("Run took %s to stop, want it to stop the pass that was waiting for hub", took)
	}
	if n := slow.inFlight.Load(); n != 0 {
		t.Errorf("%d listings are still in flight after Run returned", n)
	}
}

// TestADirectPassWhileOneIsRunningStartsNoSecond pins that Pass, which a caller may call, is one pass at a time too.
// Verifies: 14-REQ-8.1
func TestADirectPassWhileOneIsRunningStartsNoSecond(t *testing.T) {
	w := newWorld(t)
	slow := &slowHub{Client: w.client, delay: 150 * time.Millisecond}
	w.subs.slugs = []string{acme}
	w.rec = reconcile.New(reconcile.Config{Subscriptions: w.subs, Hub: slow, Jobs: w.jobs, Logger: slog.New(slog.NewJSONHandler(w.logs, nil))})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); w.rec.Pass(ctx) }()
	}
	wg.Wait()

	if most := slow.most.Load(); most != 1 {
		t.Errorf("%d listings were in flight at once, want one", most)
	}
}

// TestTheFirstPassIsMadeAtOnceAndNotAfterTheFirstInterval pins that Run does not wait for a tick: a service that is
// started gives back what the process before it left held without waiting a quarter of an hour.
// Verifies: 14-REQ-8.1
func TestTheFirstPassIsMadeAtOnceAndNotAfterTheFirstInterval(t *testing.T) {
	w := newWorld(t)
	w.know("job_1")
	orphan := w.seedLease(acme, "job_1", 1)
	w.rec = reconcile.New(reconcile.Config{Subscriptions: w.subs, Hub: w.client, Jobs: w.jobs, Logger: slog.New(slog.NewJSONHandler(w.logs, nil)), Interval: time.Hour})
	run, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); w.rec.Run(run) }()
	defer func() { stop(); <-done }()

	deadline := time.Now().Add(10 * time.Second)
	for !slices.Contains(w.hub.Deleted(), orphan.ID) {
		if time.Now().After(deadline) {
			t.Fatal("the orphan was not released: no pass was made at the start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAFailureOfHubDuringTheEndOfASessionLeavesItToTheNextPassWithoutPostingTheUsageTwice pins the order of the two
// steps and their record: the usage is posted before the session is completed, a session is not completed when the
// usage could not be posted, and a usage that was posted is not posted again when the completion failed.
// Verifies: 14-REQ-8.4
func TestAFailureOfHubDuringTheEndOfASessionLeavesItToTheNextPassWithoutPostingTheUsageTwice(t *testing.T) {
	t.Run("the usage cannot be posted", func(t *testing.T) {
		w := newWorld(t)
		session, _ := w.end(attempt{job: "job_a", n: 1, slug: acme, pat: patAcme})
		w.hub.Inject("POST", "/api/v1/sessions/"+session+"/usage", fakehub.Fault{Status: 500})

		w.pass()

		if s := w.sessionOf(session); s.Status != "open" || len(s.Usage) != 0 {
			t.Fatalf("the session is %+v, want it untouched: it is not completed before its usage is posted", s)
		}
		if j := w.journalOf("job_a", 1); j.Report.UsagePosted || j.Session.Completed {
			t.Errorf("the journal records %+v and %+v, want no step", j.Report, j.Session)
		}
		w.hub.Clear()
		w.pass()
		if s := w.sessionOf(session); s.Status != "failed" || len(s.Usage) != 1 {
			t.Errorf("the session is %+v after the next pass, want it completed with its usage", s)
		}
	})

	t.Run("the session cannot be completed", func(t *testing.T) {
		w := newWorld(t)
		session, _ := w.end(attempt{job: "job_a", n: 1, slug: acme, pat: patAcme})
		w.hub.Inject("POST", "/api/v1/sessions/"+session+"/complete", fakehub.Fault{Status: 500})

		w.pass()

		if s := w.sessionOf(session); s.Status != "open" || len(s.Usage) != 1 {
			t.Fatalf("the session is %+v, want it open with its usage posted", s)
		}
		if j := w.journalOf("job_a", 1); !j.Report.UsagePosted || j.Session.Completed {
			t.Errorf("the journal records %+v and %+v, want the usage as posted and the session as open", j.Report, j.Session)
		}
		w.hub.Clear()
		w.pass()
		if s := w.sessionOf(session); s.Status != "failed" || len(s.Usage) != 1 {
			t.Errorf("the session is %+v after the next pass, want it completed, with its usage posted once", s)
		}
	})
}

// TestAJournalThatCannotBeWrittenMakesNeitherTheUsageNorTheCompletionBeMadeAgain pins that what a pass has done is not
// done again by the next because it could not be written down: the usage of a session is posted once, and the session
// is completed, whatever the state of the journal.
// Verifies: 14-REQ-8.4
func TestAJournalThatCannotBeWrittenMakesNeitherTheUsageNorTheCompletionBeMadeAgain(t *testing.T) {
	w := newWorld(t)
	session, _ := w.end(attempt{job: "job_a", n: 1, slug: acme, pat: patAcme})
	unwritable := true
	w.rec = reconcile.New(reconcile.Config{
		Subscriptions: w.subs, Hub: w.client, Jobs: w.jobs, Logger: slog.New(slog.NewJSONHandler(w.logs, nil)),
		JournalFault: func() error {
			if unwritable {
				return errors.New("the disk is full")
			}
			return nil
		},
	})

	w.pass()
	w.pass()
	w.pass()

	s := w.sessionOf(session)
	if s.Status != "failed" || len(s.Usage) != 1 {
		t.Errorf("the session is %q with %d usage records, want it completed, with its usage posted once", s.Status, len(s.Usage))
	}
	if n := len(w.hub.RequestsTo("POST", "/api/v1/sessions/"+session+"/complete")); n != 1 {
		t.Errorf("the session was completed %d times, want once", n)
	}
	if warns := w.logs.level("WARN"); len(warns) == 0 {
		t.Error("nothing was logged of a journal that cannot be written")
	}

	unwritable = false
	w.pass()
	if j := w.journalOf("job_a", 1); !j.Session.Completed || !j.Report.UsagePosted || !j.Report.SessionCompleted {
		t.Errorf("the journal records %+v and %+v once it can be written, want both steps", j.Session, j.Report)
	}
	if s := w.sessionOf(session); len(s.Usage) != 1 {
		t.Errorf("the session has %d usage records after the journal was written, want one", len(s.Usage))
	}
}
