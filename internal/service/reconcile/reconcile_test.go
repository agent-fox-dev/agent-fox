package reconcile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/reconcile"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
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

// fakeJobs is the jobs layer as the reconciliation pass sees it.
type fakeJobs struct {
	mu      sync.Mutex
	known   map[string]bool
	running map[string]bool
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
	w.jobs = &fakeJobs{known: map[string]bool{}, running: map[string]bool{}}
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

// TestEachReleaseIsLoggedAtInfoWithTheWorkspaceTheIdTheJobAndTheAttempt_TS_14_56 verifies TS-14-56.
// Verifies: 14-REQ-8.7
func TestEachReleaseIsLoggedAtInfoWithTheWorkspaceTheIdTheJobAndTheAttempt_TS_14_56(t *testing.T) {
	w := newWorld(t)
	w.know("job_1")
	orphan := w.seedLease(acme, "job_1", 3)

	w.pass()

	infos := w.logs.level("INFO")
	if len(infos) != 1 {
		t.Fatalf("%d INFO lines, want one for the release: %s", len(infos), w.logs.String())
	}
	release := infos[0]
	if release["workspace"] != acme || release["sandbox_id"] != orphan.ID || release["job"] != "job_1" || fmt.Sprint(release["attempt"]) != "3" {
		t.Errorf("the release is logged as %v, want the workspace, the sandbox, the job and the attempt", release)
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
