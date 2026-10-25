package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var ctx = context.Background()

// openDB opens a state file with the service's migrations applied.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	key, err := secrets.NewKey(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st.DB()
}

// newStore is a store over a new state file, on a clock that reads a time in a zone east of UTC, with nanoseconds,
// so that what is stored shows the conversion to UTC and the cut to milliseconds.
func newStore(t *testing.T) (*Store, *fakeclock.Clock) {
	t.Helper()
	clk := fakeclock.New(time.Date(2026, 10, 22, 8, 0, 0, 123456789, time.FixedZone("test", 2*3600+1800)))
	return NewStore(openDB(t), clk.Now), clk
}

// newJob is a fix of widgets for the issue, or a triage of a text when issue is 0.
func newJob(issue int64) NewJob {
	j := NewJob{Workspace: "widgets", Kind: KindFix, Source: Source{Kind: "api", By: "user-1"}, Context: []string{"a note"}}
	if issue == 0 {
		j.Kind, j.Input = KindTriage, Input{Text: "the build is broken", Title: "broken"}
		return j
	}
	raw, _ := json.Marshal(issue)
	j.Issue, j.Input = &issue, Input{Issue: raw}
	return j
}

// mustCreate stores the job and returns its id.
func mustCreate(t *testing.T, st *Store, j NewJob) string {
	t.Helper()
	id, err := st.CreateJob(ctx, j)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// force puts the job in the state, the way the code that moves it would.
func force(t *testing.T, st *Store, id string, state State) {
	t.Helper()
	var finished any
	if state.Finished() {
		finished = formatTime(st.now())
	}
	testutil.Exec(t, st.db, "UPDATE af_jobs SET state = ?, finished_at = ? WHERE id = ?", string(state), finished, id)
}

// eventTypes are the types of the job's events, in order.
func eventTypes(t *testing.T, st *Store, id string) string {
	t.Helper()
	return strings.Join(testutil.Column(t, st.db, "SELECT type FROM af_events WHERE job_id = ? ORDER BY seq", id), " ")
}

func TestCreateJob_StoresTheJobItsFirstAttemptAndItsFirstEventInUTCMilliseconds(t *testing.T) {
	st, _ := newStore(t)
	id := mustCreate(t, st, newJob(7))
	if !regexp.MustCompile(`^job_[0-9a-f]{20}$`).MatchString(id) {
		t.Errorf("id %q, want job_ and 20 lowercase hex characters", id)
	}
	if other := mustCreate(t, st, newJob(8)); other == id {
		t.Error("two jobs have the same id")
	}
	doc, err := st.Doc(ctx, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if doc.State != StateQueued || doc.CreatedAt != "2026-10-22T05:30:00.123Z" || doc.StartedAt != nil || doc.FinishedAt != nil {
		t.Errorf("job %s created %s started %v finished %v, want queued at 05:30:00.123Z UTC and neither time", doc.State, doc.CreatedAt, doc.StartedAt, doc.FinishedAt)
	}
	if len(doc.Attempts) != 1 || doc.Attempts[0].Trigger != (Trigger{Kind: TriggerCreate, By: "user-1"}) || doc.Attempts[0].State != AttemptQueued {
		t.Errorf("attempts = %+v, want attempt 1 queued, made by the creator", doc.Attempts)
	}
	evs, err := st.EventsAfter(ctx, id, 0, 10)
	if err != nil || len(evs) != 1 || evs[0].Seq != 1 || evs[0].TS != "2026-10-22T05:30:00.123Z" || string(evs[0].Data) != `{"state":"queued","attempt":1,"at_rest":false}` {
		t.Errorf("events = %+v (%v), want one job_state queued, not at rest", evs, err)
	}
	if got := testutil.Column(t, st.db, "SELECT context FROM af_jobs WHERE id = ?", id); got[0] != `["a note"]` {
		t.Errorf("context = %s", got[0])
	}
	// A job without context stores an empty list.
	j := newJob(9)
	j.Context = nil
	if got := testutil.Column(t, st.db, "SELECT context FROM af_jobs WHERE id = ?", mustCreate(t, st, j)); got[0] != `[]` {
		t.Errorf("context = %s, want []", got[0])
	}
}

func TestCreateJob_StoresNothingWhenAnyOfItsInsertsFails(t *testing.T) {
	for _, op := range []string{OpInsertJob, OpInsertAttempt, OpInsertEvent} {
		st, _ := newStore(t)
		st.SetFault(func(got string) error {
			if got == op {
				return errors.New("injected")
			}
			return nil
		})
		if _, err := st.CreateJob(ctx, newJob(1)); err == nil {
			t.Errorf("%s: CreateJob succeeded", op)
		}
		for _, table := range []string{"af_jobs", "af_attempts", "af_events"} {
			if n := testutil.Count(t, st.db, table, ""); n != 0 {
				t.Errorf("%s failed: %d rows in %s", op, n, table)
			}
		}
	}
}

func TestCreateJob_RefusesASecondOpenJobOfAKindForAnIssue(t *testing.T) {
	st, _ := newStore(t)
	first := mustCreate(t, st, newJob(42))
	if _, err := st.CreateJob(ctx, newJob(42)); !errors.Is(err, ErrDuplicate) {
		t.Errorf("a second fix of #42: %v, want ErrDuplicate", err)
	}
	triage := newJob(42)
	triage.Kind = KindTriage
	mustCreate(t, st, triage)
	if id, err := st.OpenJobID(ctx, "widgets", KindFix, 42); err != nil || id != first {
		t.Errorf("OpenJobID = %s, %v, want %s", id, err, first)
	}
	force(t, st, first, StateDone)
	if _, err := st.OpenJobID(ctx, "widgets", KindFix, 42); !errors.Is(err, ErrNotFound) {
		t.Errorf("OpenJobID of a done job: %v, want ErrNotFound", err)
	}
	second := mustCreate(t, st, newJob(42))
	// Queuing the done one again would make two open jobs for the issue.
	if _, err := st.Requeue(ctx, first, StateDone, Trigger{Kind: TriggerRetry, By: "u"}, nil); !errors.Is(err, ErrDuplicate) {
		t.Errorf("requeue beside %s: %v, want ErrDuplicate", second, err)
	}
}

func TestRequeue_NumbersTheNextAttemptAndRefusesAJobInAnotherState(t *testing.T) {
	st, _ := newStore(t)
	id := mustCreate(t, st, newJob(1))
	b := "B"
	answer := &Answer{Question: "Which?", Options: []Option{{ID: "A", Label: "one"}, {ID: "B", Label: "two"}}, Option: &b, Text: "two"}
	for n, step := range []struct {
		from    State
		trigger TriggerKind
		answer  *Answer
	}{{StateBlocked, TriggerAnswer, answer}, {StateFailed, TriggerRetry, nil}, {StateRunning, TriggerAuto, nil}} {
		force(t, st, id, step.from)
		got, err := st.Requeue(ctx, id, step.from, Trigger{Kind: step.trigger, By: "user-2"}, step.answer)
		if err != nil || got != n+2 {
			t.Fatalf("Requeue from %s = %d, %v, want attempt %d", step.from, got, err, n+2)
		}
	}
	doc, err := st.Doc(ctx, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if doc.AttemptCount != 4 || doc.CurrentAttempt != 4 || doc.State != StateQueued || doc.FinishedAt != nil {
		t.Errorf("job %s with %d attempts, current %d, finished %v, want queued with 4 and no finished_at", doc.State, doc.AttemptCount, doc.CurrentAttempt, doc.FinishedAt)
	}
	if a := doc.Attempts[1].Answer; a == nil || a.Question != "Which?" || a.Option == nil || *a.Option != "B" || a.Text != "two" {
		t.Errorf("the answer of attempt 2 = %+v", a)
	}
	var state *StateError
	if _, err := st.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil); !errors.As(err, &state) || state.State != StateQueued || err.Error() != "the job is queued" {
		t.Errorf("Requeue of a queued job as failed: %v, want a *StateError for queued", err)
	}
	if _, err := st.Requeue(ctx, "job_00000000000000000000", StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("Requeue of no job: %v, want ErrNotFound", err)
	}
	if n := testutil.Count(t, st.db, "af_attempts", "job_id = ?", id); n != 4 {
		t.Errorf("%d attempts after the refusals, want 4", n)
	}
}

func TestTransition_SetsStartedAtOnceAndFinishedAtExactlyWhileFinished(t *testing.T) {
	st, clk := newStore(t)
	id := mustCreate(t, st, newJob(7))
	move := func(from []State, to State) bool {
		t.Helper()
		tx, err := st.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		changed, err := st.transition(ctx, tx, id, from, to)
		if err != nil || tx.Commit() != nil {
			t.Fatal(err)
		}
		return changed
	}
	times := func() (started, finished string) {
		t.Helper()
		row := testutil.Column(t, st.db, "SELECT COALESCE(started_at, '') || '|' || COALESCE(finished_at, '') FROM af_jobs WHERE id = ?", id)[0]
		started, finished, _ = strings.Cut(row, "|")
		return started, finished
	}
	if move([]State{StateRunning}, StateDone) {
		t.Fatal("a move from running changed a queued job")
	}
	for i, step := range []struct {
		from, to State
	}{{StateQueued, StateRunning}, {StateRunning, StateDone}, {StateDone, StateQueued}, {StateQueued, StateRunning}, {StateRunning, StateBlocked}, {StateBlocked, StateCancelled}} {
		clk.Advance(time.Second)
		if !move([]State{step.from}, step.to) {
			t.Fatalf("%s to %s changed nothing", step.from, step.to)
		}
		started, finished := times()
		if started != "2026-10-22T05:30:01.123Z" {
			t.Errorf("after %s: started_at %q, want the time of the first run", step.to, started)
		}
		if want := formatTime(clk.Now()); step.to.Finished() && finished != want || !step.to.Finished() && finished != "" {
			t.Errorf("step %d to %s: finished_at %q", i, step.to, finished)
		}
	}
}

func TestClaim_HandsTheAttemptOverWithOneMoreDeliveryAllOrNothing(t *testing.T) {
	st, _ := newStore(t)
	id := mustCreate(t, st, newJob(1))
	st.SetFault(func(op string) error {
		if op == OpClaim {
			return errors.New("injected")
		}
		return nil
	})
	if _, err := st.Claim(ctx, id, 1); err == nil {
		t.Fatal("a claim made to fail succeeded")
	}
	if got := testutil.Column(t, st.db, "SELECT a.state || a.deliveries || j.state FROM af_attempts a JOIN af_jobs j ON j.id = a.job_id"); got[0] != "queued0queued" || eventTypes(t, st, id) != "job_state" {
		t.Errorf("after a failed claim: %v, events %s, want everything as it was", got, eventTypes(t, st, id))
	}
	st.SetFault(nil)
	for delivery := 1; delivery <= 3; delivery++ {
		if c, err := st.Claim(ctx, id, 1); err != nil || c.Delivery != delivery {
			t.Fatalf("claim %d = %+v, %v", delivery, c, err)
		}
	}
	// The job moved to running once; each handover says it started.
	if got := eventTypes(t, st, id); got != "job_state job_state attempt_start attempt_start attempt_start" {
		t.Errorf("events = %s", got)
	}
	if state, _ := st.JobState(ctx, id); state != StateRunning {
		t.Errorf("job %s, want running", state)
	}

	for name, setup := range map[string]string{
		"a cancelled job":     "UPDATE af_jobs SET state = 'cancelled', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?",
		"a done job":          "UPDATE af_jobs SET state = 'done', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?",
		"an attempt now done": "UPDATE af_attempts SET state = 'done' WHERE job_id = ?",
	} {
		other := mustCreate(t, st, newJob(100+int64(len(name))))
		testutil.Exec(t, st.db, setup, other)
		if _, err := st.Claim(ctx, other, 1); !errors.Is(err, ErrNotClaimable) {
			t.Errorf("%s: %v, want ErrNotClaimable", name, err)
		}
		if got := testutil.Column(t, st.db, "SELECT deliveries FROM af_attempts WHERE job_id = ?", other); got[0] != "0" || eventTypes(t, st, other) != "job_state" {
			t.Errorf("%s: a refused claim changed the attempt or the log", name)
		}
	}
	for _, key := range []struct {
		id string
		n  int
	}{{"job_00000000000000000000", 1}, {id, 7}} {
		if _, err := st.Claim(ctx, key.id, key.n); !errors.Is(err, ErrNotClaimable) {
			t.Errorf("attempt %d of %s: %v, want ErrNotClaimable", key.n, key.id, err)
		}
	}
}

func TestPlan_RunsTheAttemptsOfAWorkspaceInTheOrderTheyWereMade(t *testing.T) {
	st, _ := newStore(t)
	first, second := mustCreate(t, st, newJob(1)), mustCreate(t, st, newJob(2))
	other := newJob(3)
	other.Workspace = "gadgets"
	elsewhere := mustCreate(t, st, other)
	plan := func(id string, n int) disposition {
		t.Helper()
		d, err := st.Plan(ctx, id, n)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if plan(first, 1) != dispClaim || plan(second, 1) != dispYield || plan(elsewhere, 1) != dispClaim {
		t.Error("want the first of each workspace claimed and the second handed back")
	}
	if plan(first, 2) != dispAck || plan("job_00000000000000000000", 1) != dispAck {
		t.Error("an attempt or a job that is not there is not acknowledged")
	}
	// A finished job neither runs nor keeps the next one waiting, even with an attempt a crash left queued.
	force(t, st, first, StateCancelled)
	if plan(first, 1) != dispAck || plan(second, 1) != dispClaim {
		t.Error("a cancelled job's queued attempt is not acknowledged, or keeps the next one waiting")
	}
	// A cancelled job whose attempt is still running had a runner when the process ended: it is reaped.
	testutil.Exec(t, st.db, "UPDATE af_attempts SET state = 'running' WHERE job_id = ?", first)
	if plan(first, 1) != dispReap {
		t.Error("a running attempt of a cancelled job is not reaped")
	}
	testutil.Exec(t, st.db, "UPDATE af_attempts SET state = 'done' WHERE job_id = ?", second)
	if plan(second, 1) != dispAck {
		t.Error("an attempt that is done is not acknowledged")
	}
}

func TestDoc_UsageIsTheSumOverTheAttemptsThatEnded(t *testing.T) {
	st, _ := newStore(t)
	id := mustCreate(t, st, newJob(1))
	end := func(n int, state AttemptState, u toolio.Usage) {
		t.Helper()
		testutil.Exec(t, st.db, "UPDATE af_attempts SET state = ?, status = 'failed', usage = ? WHERE job_id = ? AND n = ?", string(state), string(testutil.JSON(t, u)), id, n)
	}
	if doc, _ := st.Doc(ctx, id, false); doc.Usage != (UsageDoc{}) || doc.Attempts != nil {
		t.Errorf("a new job: usage %+v, attempts %v, want zero and none", doc.Usage, doc.Attempts)
	}
	end(1, AttemptFailed, toolio.Usage{CostUSD: 1.25, InputTokens: 100, OutputTokens: 10, Turns: 3})
	force(t, st, id, StateFailed)
	if _, err := st.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil); err != nil {
		t.Fatal(err)
	}
	end(2, AttemptDone, toolio.Usage{CostUSD: 0.5, InputTokens: 50, OutputTokens: 5, Turns: 1})
	force(t, st, id, StateFailed)
	if _, err := st.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil); err != nil {
		t.Fatal(err)
	}
	end(3, AttemptRunning, toolio.Usage{CostUSD: 99, InputTokens: 99, OutputTokens: 99, Turns: 99}) // adds nothing yet
	doc, err := st.Doc(ctx, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Usage != (UsageDoc{CostUSD: 1.75, InputTokens: 150, OutputTokens: 15, Turns: 4}) {
		t.Errorf("usage = %+v, want the sum of attempts 1 and 2", doc.Usage)
	}
	if doc.Attempts[2].Usage != nil || doc.Attempts[0].Usage == nil {
		t.Errorf("the usage of the attempts: %v %v, want it shown once an attempt has ended", doc.Attempts[0].Usage, doc.Attempts[2].Usage)
	}
	if _, err := st.Doc(ctx, "job_00000000000000000000", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("Doc of no job: %v, want ErrNotFound", err)
	}
}

func TestEnsureAttemptsDir_MakesAPrivateDirectoryBesideTheDatabaseAndLeavesOneThatIsThere(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agent-fox.db")
	dir, err := EnsureAttemptsDir(dbPath)
	if err != nil || dir != filepath.Join(filepath.Dir(dbPath), "attempts") {
		t.Fatalf("EnsureAttemptsDir = %s, %v", dir, err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("stat = %v, %v, want a directory with the mode 0700", info, err)
	}
	kept := testutil.WriteFile(t, dir, "job_x/1/envelope.json", "{}")
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAttemptsDir(dbPath); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 || testutil.ReadFile(t, kept) != "{}" {
		t.Errorf("the second call changed the directory or what is in it")
	}
	blocked := testutil.WriteFile(t, t.TempDir(), "file", "")
	if _, err := EnsureAttemptsDir(filepath.Join(blocked, "agent-fox.db")); err == nil {
		t.Error("a directory under a file was made")
	}
}
