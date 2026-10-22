package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

var ctx = context.Background()

// testClock is the clock of a test: a time in a zone that is not UTC, with
// nanoseconds, so that a time written as UTC with milliseconds shows the conversion.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	zone := time.FixedZone("test", 2*3600+1800)
	return &testClock{t: time.Date(2026, 10, 22, 8, 0, 0, 123456789, zone)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// openDB opens the state file of a test with the core's migrations applied.
func openDB(t *testing.T) *sql.DB {
	t.Helper()
	key, err := secrets.NewKey(make([]byte, 32))
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

var (
	idShape   = regexp.MustCompile(`^job_[0-9a-f]{20}$`)
	timeShape = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)
)

// newJob is a job of widgets with the number as its issue, or none when issue is 0.
func newJob(kind Kind, issue int64) NewJob {
	j := NewJob{Workspace: "widgets", Kind: kind, Source: Source{Kind: "api", By: "user-1"}, Context: []string{"a note"}}
	if issue > 0 {
		j.Issue = &issue
		raw, _ := json.Marshal(issue)
		j.Input = Input{Issue: raw}
	} else {
		j.Input = Input{Text: "the build is broken", Title: "broken"}
	}
	return j
}

// force puts a job in a state, the way the end of an attempt would, so that a
// test can drive the store through transitions whose code comes later.
func force(t *testing.T, db *sql.DB, st *Store, id string, state State) {
	t.Helper()
	finished := any(nil)
	if state.Finished() {
		finished = formatTime(st.now())
	}
	if _, err := db.ExecContext(ctx, "UPDATE af_jobs SET state = ?, finished_at = ? WHERE id = ?", string(state), finished, id); err != nil {
		t.Fatal(err)
	}
}

// answerTo is an answer to a question of two options.
func answerTo(q string) *Answer {
	b := "B"
	return &Answer{Question: q, Options: []Option{{ID: "A", Label: "one"}, {ID: "B", Label: "two"}}, Option: &b, Text: "two"}
}

// TestJobIdsAttemptNumbersAndTimesHaveTheirShape_TS_12_4 verifies TS-12-4:
// every created job has an id of job_ and 20 lowercase hex characters, its
// attempts are numbered 1 to k, and every time is RFC 3339 in UTC with milliseconds.
// Verifies: 12-REQ-1.4
func TestJobIdsAttemptNumbersAndTimesHaveTheirShape_TS_12_4(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 4))
	clk, db := newTestClock(), openDB(t)
	st := NewStore(db, clk.Now)

	from := map[TriggerKind]State{TriggerAnswer: StateBlocked, TriggerRetry: StateFailed, TriggerAuto: StateRunning}
	triggers := []TriggerKind{TriggerAnswer, TriggerRetry, TriggerAuto}
	// The clock reads 08:00:00.123456789 in a zone 2h30 east of UTC: the time that
	// is stored is the UTC one, cut to the millisecond.
	probe, err := st.CreateJob(ctx, newJob(KindTriage, 0))
	if err != nil {
		t.Fatal(err)
	}
	if doc, err := st.Doc(ctx, probe, false); err != nil || doc.CreatedAt != "2026-10-22T05:30:00.123Z" {
		t.Fatalf("created_at = %q (%v), want 2026-10-22T05:30:00.123Z", doc.CreatedAt, err)
	}

	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		clk.Advance(time.Duration(rng.IntN(90_000_000)) * time.Nanosecond)
		kind := []Kind{KindTriage, KindFix}[rng.IntN(2)]
		issue := int64(0)
		if rng.IntN(2) == 0 {
			issue = int64(i + 1)
		}
		id, err := st.CreateJob(ctx, newJob(kind, issue))
		if err != nil {
			t.Fatalf("CreateJob: %v", err)
		}
		if !idShape.MatchString(id) || seen[id] {
			t.Fatalf("job id %q: want job_ and 20 lowercase hex characters, and unique", id)
		}
		seen[id] = true

		attempts := 1
		for range rng.IntN(6) {
			clk.Advance(time.Duration(rng.IntN(3_000_000_000)))
			trigger := triggers[rng.IntN(len(triggers))]
			var answer *Answer
			if trigger == TriggerAnswer {
				answer = answerTo("which?")
			}
			force(t, db, st, id, from[trigger])
			n, err := st.Requeue(ctx, id, from[trigger], Trigger{Kind: trigger, By: "user-2"}, answer)
			if err != nil {
				t.Fatalf("Requeue(%s): %v", trigger, err)
			}
			attempts++
			if n != attempts {
				t.Fatalf("Requeue(%s) = attempt %d, want %d", trigger, n, attempts)
			}
		}

		doc, err := st.Doc(ctx, id, true)
		if err != nil {
			t.Fatal(err)
		}
		if doc.AttemptCount != attempts || doc.CurrentAttempt != attempts || len(doc.Attempts) != attempts {
			t.Errorf("%s: attempt_count %d, current_attempt %d, %d attempts listed, want %d", id, doc.AttemptCount, doc.CurrentAttempt, len(doc.Attempts), attempts)
		}
		for k, a := range doc.Attempts {
			if a.N != k+1 {
				t.Errorf("%s: the attempts are numbered %d at position %d, want %d", id, a.N, k, k+1)
			}
		}
		if !timeShape.MatchString(doc.CreatedAt) {
			t.Errorf("%s: created_at %q is not RFC 3339 UTC with milliseconds", id, doc.CreatedAt)
		}
	}

	// Every time that the file holds has the shape, whatever table it is in.
	for _, q := range []string{
		"SELECT created_at FROM af_jobs",
		"SELECT started_at FROM af_jobs WHERE started_at IS NOT NULL",
		"SELECT finished_at FROM af_jobs WHERE finished_at IS NOT NULL",
		"SELECT started_at FROM af_attempts WHERE started_at IS NOT NULL",
		"SELECT finished_at FROM af_attempts WHERE finished_at IS NOT NULL",
		"SELECT ts FROM af_events",
	} {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			var stamp string
			if err := rows.Scan(&stamp); err != nil {
				t.Fatal(err)
			}
			n++
			if !timeShape.MatchString(stamp) {
				t.Errorf("%s: %q is not RFC 3339 UTC with milliseconds", q, stamp)
			}
		}
		rows.Close()
		if n == 0 && q == "SELECT created_at FROM af_jobs" || n == 0 && q == "SELECT ts FROM af_events" {
			t.Errorf("%s: no rows", q)
		}
	}

	// A transition that the state does not allow queues nothing and says why.
	id, err := st.CreateJob(ctx, newJob(KindFix, 1000))
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil)
	var stateErr *StateError
	if !errors.As(err, &stateErr) || stateErr.State != StateQueued {
		t.Errorf("Requeue of a queued job as if failed: err = %v, want a *StateError for queued", err)
	}
	if doc, _ := st.Doc(ctx, id, true); doc.AttemptCount != 1 {
		t.Errorf("a refused Requeue left %d attempts, want 1", doc.AttemptCount)
	}
}

// finish records an attempt as finished with the usage, the way the end of an attempt would.
func finish(t *testing.T, db *sql.DB, id string, n int, state AttemptState, usage *toolio.Usage) {
	t.Helper()
	raw, err := json.Marshal(usage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"UPDATE af_attempts SET state = ?, status = 'done', exit_code = 0, summary = 'ok', usage = ?, finished_at = '2026-10-22T08:00:00.000Z' WHERE job_id = ? AND n = ?",
		string(state), string(raw), id, n); err != nil {
		t.Fatal(err)
	}
}

// TestAJobsUsageIsTheSumOverItsFinishedAttempts_TS_12_11 verifies TS-12-11:
// the usage of a job is the sum of cost_usd, input_tokens, output_tokens and
// turns over its finished attempts, and an attempt that is not finished adds
// nothing.
// Verifies: 12-REQ-2.4
func TestAJobsUsageIsTheSumOverItsFinishedAttempts_TS_12_11(t *testing.T) {
	rng := rand.New(rand.NewPCG(12, 11))
	db := openDB(t)
	st := NewStore(db, newTestClock().Now)

	for i := range 40 {
		id, err := st.CreateJob(ctx, newJob(KindFix, int64(i+1)))
		if err != nil {
			t.Fatal(err)
		}
		finished := 1 + rng.IntN(6)
		var want UsageDoc
		for n := 1; n <= finished; n++ {
			if n > 1 {
				force(t, db, st, id, StateFailed)
				if _, err := st.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			u := &toolio.Usage{Brain: "scripted", CostSource: toolio.CostMeasured, CostUSD: math.Round(rng.Float64()*1000) / 100, InputTokens: rng.Int64N(1_000_000), OutputTokens: rng.Int64N(100_000), Turns: rng.IntN(50)}
			finish(t, db, id, n, AttemptDone, u)
			want.CostUSD += u.CostUSD
			want.InputTokens += u.InputTokens
			want.OutputTokens += u.OutputTokens
			want.Turns += u.Turns
		}
		// An attempt that is queued or running adds nothing, whatever its row holds.
		force(t, db, st, id, StateFailed)
		n, err := st.Requeue(ctx, id, StateFailed, Trigger{Kind: TriggerRetry, By: "u"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if i%2 == 0 {
			if _, err := db.ExecContext(ctx, "UPDATE af_attempts SET state = 'running', usage = ? WHERE job_id = ? AND n = ?", `{"cost_usd":99,"input_tokens":99,"output_tokens":99,"turns":99}`, id, n); err != nil {
				t.Fatal(err)
			}
		}

		doc, err := st.Doc(ctx, id, false)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(doc.Usage.CostUSD-want.CostUSD) > 1e-9 || doc.Usage.InputTokens != want.InputTokens || doc.Usage.OutputTokens != want.OutputTokens || doc.Usage.Turns != want.Turns {
			t.Errorf("%s with %d finished attempts: usage = %+v, want %+v", id, finished, doc.Usage, want)
		}
	}

	// A job nothing has finished for has zero usage, and a list entry has no attempts.
	id, err := st.CreateJob(ctx, newJob(KindTriage, 0))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := st.Doc(ctx, id, false)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Usage != (UsageDoc{}) || doc.Attempts != nil {
		t.Errorf("a new job: usage %+v and attempts %v, want zero usage and no attempts field", doc.Usage, doc.Attempts)
	}
}

// TestAJobIsFinishedAtOnlyInTheFinishedStates verifies the store's half of
// 12-REQ-2.5: started_at is set when the job first runs and kept, finished_at is
// set by every entry into done, failed or cancelled (a cancel from queued or
// blocked included) and cleared when the job queues again, and a job that is in
// another state than the transition says is left as it is.
// Verifies: 12-REQ-2.5
func TestAJobIsFinishedAtOnlyInTheFinishedStates(t *testing.T) {
	clk, db := newTestClock(), openDB(t)
	st := NewStore(db, clk.Now)
	id, err := st.CreateJob(ctx, newJob(KindFix, 7))
	if err != nil {
		t.Fatal(err)
	}

	read := func() (state State, started, finished sql.NullString) {
		t.Helper()
		if err := db.QueryRowContext(ctx, "SELECT state, started_at, finished_at FROM af_jobs WHERE id = ?", id).Scan(&state, &started, &finished); err != nil {
			t.Fatal(err)
		}
		return
	}
	move := func(from []State, to State) bool {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		changed, err := st.transition(ctx, tx, id, from, to)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return changed
	}
	check := func(label string, want State) (started, finished sql.NullString) {
		t.Helper()
		state, started, finished := read()
		if state != want {
			t.Fatalf("%s: state %s, want %s", label, state, want)
		}
		if finished.Valid != want.Finished() {
			t.Errorf("%s: finished_at %v in state %s, want it set exactly when the state is finished", label, finished, state)
		}
		return started, finished
	}

	if _, started, _ := read(); started.Valid {
		t.Fatalf("a queued job has started_at %v", started)
	}
	if move([]State{StateRunning}, StateDone) {
		t.Fatal("a transition from running changed a queued job")
	}
	check("refused", StateQueued)

	clk.Advance(time.Second)
	if !move([]State{StateQueued, StateRunning}, StateRunning) {
		t.Fatal("queued to running did not change the job")
	}
	first, _ := check("first claim", StateRunning)

	clk.Advance(time.Second)
	if !move([]State{StateRunning}, StateDone) {
		t.Fatal("running to done did not change the job")
	}
	_, finished := check("done", StateDone)

	clk.Advance(time.Second)
	if !move([]State{StateDone}, StateQueued) {
		t.Fatal("done to queued did not change the job")
	}
	check("queued again", StateQueued)

	clk.Advance(time.Second)
	if !move([]State{StateQueued}, StateRunning) {
		t.Fatal("second claim did not change the job")
	}
	if second, _ := check("second claim", StateRunning); second != first {
		t.Errorf("started_at moved from %v to %v at the second claim, want it kept", first, second)
	}

	clk.Advance(time.Second)
	move([]State{StateRunning}, StateBlocked)
	check("blocked", StateBlocked)
	clk.Advance(time.Second)
	if !move([]State{StateBlocked}, StateCancelled) {
		t.Fatal("blocked to cancelled did not change the job")
	}
	if _, cancelled := check("cancelled from blocked", StateCancelled); cancelled == finished {
		t.Errorf("finished_at of the cancel is %v, the time of the earlier finish", cancelled)
	}
}

// TestEnsureAttemptsDirMakesItPrivateAndLeavesWhatIsThere verifies 12-REQ-1.3 for
// the directory: attempts/ beside the database is made with the mode 0700 when it
// is missing, and a directory that is there, with what is in it, is left as it is.
// Verifies: 12-REQ-1.3
func TestEnsureAttemptsDirMakesItPrivateAndLeavesWhatIsThere(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "agent-fox.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := EnsureAttemptsDir(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(dbPath), "attempts"); dir != want {
		t.Errorf("directory = %s, want %s", dir, want)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("stat %s = %v, %v, want a directory with the mode 0700", dir, info, err)
	}

	kept := filepath.Join(dir, "job_x", "1", "envelope.json")
	if err := os.MkdirAll(filepath.Dir(kept), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureAttemptsDir(dbPath); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Errorf("a directory that was there has the mode %v after the second call, want it unchanged", info.Mode().Perm())
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("a file in the directory is gone after the second call: %v", err)
	}
}
