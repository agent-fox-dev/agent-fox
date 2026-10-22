package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

// TestEachHandoverAddsOneDeliveryAndTheClaimIsAllOrNothing_TS_12_27 is the claim of
// TS-12-27: a handover moves the attempt and the job to running and adds one to
// the deliveries, with the events that say so; a handover of an attempt that is
// running already (one that was interrupted) adds exactly one more; and a claim
// that fails changes none of the three.
// Verifies: 12-REQ-4.5
func TestEachHandoverAddsOneDeliveryAndTheClaimIsAllOrNothing_TS_12_27(t *testing.T) {
	db := openDB(t)
	clk := newTestClock()
	st := NewStore(db, clk.Now)
	id, err := st.CreateJob(ctx, newJob(KindFix, 1))
	if err != nil {
		t.Fatal(err)
	}

	read := func() (attempt AttemptState, deliveries int, job State, started sql.NullString, events []string) {
		t.Helper()
		if err := db.QueryRow("SELECT state, deliveries FROM af_attempts WHERE job_id = ? AND n = 1", id).Scan(&attempt, &deliveries); err != nil {
			t.Fatal(err)
		}
		if err := db.QueryRow("SELECT state, started_at FROM af_jobs WHERE id = ?", id).Scan(&job, &started); err != nil {
			t.Fatal(err)
		}
		rows, err := db.Query("SELECT type FROM af_events WHERE job_id = ? ORDER BY seq", id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var typ string
			rows.Scan(&typ)
			events = append(events, typ)
		}
		return
	}

	// A claim that fails in the middle of the transaction changes nothing.
	st.SetFault(func(op string) error {
		if op == OpClaim {
			return errors.New("injected")
		}
		return nil
	})
	if _, err := st.Claim(ctx, id, 1); err == nil {
		t.Fatal("a claim that was made to fail succeeded")
	}
	attempt, deliveries, job, started, events := read()
	if attempt != AttemptQueued || deliveries != 0 || job != StateQueued || started.Valid || fmt.Sprint(events) != "[job_state]" {
		t.Errorf("after a failed claim: attempt %s, deliveries %d, job %s, started %v, events %v, want it all as it was", attempt, deliveries, job, started, events)
	}
	st.SetFault(nil)

	// The first handover.
	clk.Advance(1500_000_000)
	c, err := st.Claim(ctx, id, 1)
	if err != nil || c.Delivery != 1 {
		t.Fatalf("Claim = %+v, %v, want delivery 1", c, err)
	}
	attempt, deliveries, job, started, events = read()
	if attempt != AttemptRunning || deliveries != 1 || job != StateRunning || !started.Valid || fmt.Sprint(events) != "[job_state job_state attempt_start]" {
		t.Errorf("after the claim: attempt %s, deliveries %d, job %s, started %v, events %v", attempt, deliveries, job, started, events)
	}

	// An attempt that is running is handed over again, as after a restart: one more
	// delivery, no new transition of the job, started_at as it was.
	clk.Advance(1500_000_000)
	c, err = st.Claim(ctx, id, 1)
	if err != nil || c.Delivery != 2 {
		t.Fatalf("the second Claim = %+v, %v, want delivery 2", c, err)
	}
	_, deliveries2, _, started2, events2 := read()
	if deliveries2 != 2 || started2 != started || fmt.Sprint(events2) != "[job_state job_state attempt_start attempt_start]" {
		t.Errorf("after the second claim: deliveries %d, started %v (was %v), events %v", deliveries2, started2, started, events2)
	}
	if c, err = st.Claim(ctx, id, 1); err != nil || c.Delivery != 3 {
		t.Errorf("the third Claim = %+v, %v, want delivery 3", c, err)
	}

	// What cannot be claimed.
	issue := int64(100)
	for name, setup := range map[string]string{
		"a job that is cancelled": "UPDATE af_jobs SET state = 'cancelled', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?",
		"a job that is done":      "UPDATE af_jobs SET state = 'done', finished_at = '2026-10-22T08:00:00.000Z' WHERE id = ?",
		"an attempt that is done": "UPDATE af_attempts SET state = 'done' WHERE job_id = ?",
	} {
		issue++
		other, err := st.CreateJob(ctx, newJob(KindFix, issue))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(setup, other); err != nil {
			t.Fatal(err)
		}
		var before, after int
		db.QueryRow("SELECT count(*) FROM af_events WHERE job_id = ?", other).Scan(&before)
		if _, err := st.Claim(ctx, other, 1); !errors.Is(err, ErrNotClaimable) {
			t.Errorf("%s: Claim err = %v, want ErrNotClaimable", name, err)
		}
		var deliveriesOther int
		db.QueryRow("SELECT deliveries FROM af_attempts WHERE job_id = ?", other).Scan(&deliveriesOther)
		db.QueryRow("SELECT count(*) FROM af_events WHERE job_id = ?", other).Scan(&after)
		if deliveriesOther != 0 || after != before {
			t.Errorf("%s: a refused claim left %d deliveries and %d new events", name, deliveriesOther, after-before)
		}
	}
	if _, err := st.Claim(ctx, "job_00000000000000000000", 1); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("a job that is not there: Claim err = %v, want ErrNotClaimable", err)
	}
	if _, err := st.Claim(ctx, id, 7); !errors.Is(err, ErrNotClaimable) {
		t.Errorf("an attempt that is not there: Claim err = %v, want ErrNotClaimable", err)
	}
}
