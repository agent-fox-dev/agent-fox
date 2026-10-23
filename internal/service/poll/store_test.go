package poll

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

var ctx = context.Background()

// at is a time of the tests, a number of minutes after a fixed instant.
func at(minutes int) time.Time {
	return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC).Add(time.Duration(minutes) * time.Minute)
}

// openState opens the state file at the path, with the migrations of the store.
func openState(t *testing.T, path string) *store.Store {
	t.Helper()
	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// newRecords is the records over a new state file, closed when the test ends.
func newRecords(t *testing.T) (*Records, *sql.DB) {
	t.Helper()
	st := openState(t, filepath.Join(t.TempDir(), "agent-fox.db"))
	t.Cleanup(func() { st.Close() })
	return NewRecords(st.DB()), st.DB()
}

// column is the first column of the rows of the query.
func column(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// sameRecord says whether the records are the same, the times by the instant they name.
func sameRecord(a, b Record) bool {
	return a.ID == b.ID && a.Workspace == b.Workspace && a.Issue == b.Issue && a.Label == b.Label && a.JobID == b.JobID &&
		a.Owed == b.Owed && a.Seen == b.Seen && a.ReleaseClock.Equal(b.ReleaseClock) && a.CreatedAt.Equal(b.CreatedAt)
}

// TestThePollRecordsLiveInATableWithNoForeignKeyThatSurvivesAReopen_TS_15_37 verifies TS-15-37, the part that the
// poll shows: the migration that the store applied created af_poll_requests, the table has no foreign key to
// af_jobs, a record that was written is read back whole, and the records are there when the state file is opened
// again. The part that the store shows, that the migration is the last of the list and the earlier ones are as
// they were, is in the store package.
// Verifies: 15-REQ-6.1
func TestThePollRecordsLiveInATableWithNoForeignKeyThatSurvivesAReopen_TS_15_37(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	st := openState(t, path)
	db := st.DB()
	if got := column(t, db, "SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'af_poll_requests'"); !slices.Equal(got, []string{"af_poll_requests"}) {
		t.Fatalf("tables = %v, want af_poll_requests", got)
	}
	if got := column(t, db, "SELECT \"table\" FROM pragma_foreign_key_list('af_poll_requests')"); len(got) != 0 {
		t.Errorf("af_poll_requests has foreign keys to %v, want none: a record must outlive the purge of its job", got)
	}
	records := NewRecords(db)
	held, err := records.Insert(ctx, Record{Workspace: "widgets", Issue: 42, Label: "af:fix", JobID: "job_aaaaaaaaaaaaaaaaaaaa", Owed: true, Seen: SeenOpen, CreatedAt: at(0)})
	if err != nil {
		t.Fatal(err)
	}
	absorbed, err := records.Insert(ctx, Record{Workspace: "widgets", Issue: 7, Label: "af:triage", Owed: true, Seen: SeenEnded, CreatedAt: at(1)})
	if err != nil {
		t.Fatal(err)
	}
	if held.ID == 0 || absorbed.ID <= held.ID {
		t.Errorf("ids %d and %d, want them to be assigned in order", held.ID, absorbed.ID)
	}
	if err := records.SetSeen(ctx, held.ID, SeenHolding); err != nil {
		t.Fatal(err)
	}
	if err := records.StartClock(ctx, held.ID, at(5)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openState(t, path)
	defer reopened.Close()
	got, err := NewRecords(reopened.DB()).ForWorkspace(ctx, "widgets")
	if err != nil {
		t.Fatal(err)
	}
	want := []Record{
		{ID: held.ID, Workspace: "widgets", Issue: 42, Label: "af:fix", JobID: "job_aaaaaaaaaaaaaaaaaaaa", Owed: true, Seen: SeenHolding, ReleaseClock: at(5), CreatedAt: at(0)},
		{ID: absorbed.ID, Workspace: "widgets", Issue: 7, Label: "af:triage", Owed: true, Seen: SeenEnded, CreatedAt: at(1)},
	}
	if !slices.EqualFunc(got, want, sameRecord) {
		t.Errorf("after a reopen the records are\n%+v\nwant\n%+v", got, want)
	}
	if versions := column(t, reopened.DB(), "SELECT version FROM af_schema_migrations ORDER BY version"); len(versions) < 5 {
		t.Errorf("migrations %v, want the poll's among them", versions)
	}
}

// TestTheStoreFindsClearsAndDropsRecordsByWorkspaceIssueAndLabel is the behaviour of the store that the
// rules of the poll rest on: a workspace sees only its own records, the owed flags of an issue and a label are
// cleared together and no others, and a record is dropped once nothing is owed and nothing holds.
// Verifies: 15-REQ-6.1, 15-REQ-6.5, 15-REQ-6.8
func TestTheStoreFindsClearsAndDropsRecordsByWorkspaceIssueAndLabel(t *testing.T) {
	records, _ := newRecords(t)
	add := func(workspace string, issue int, label, job string, owed bool, seen Seen) Record {
		t.Helper()
		r, err := records.Insert(ctx, Record{Workspace: workspace, Issue: issue, Label: label, JobID: job, Owed: owed, Seen: seen, CreatedAt: at(0)})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	owedDone := add("widgets", 42, "af:fix", "job_1", true, SeenEnded)
	owedOpen := add("widgets", 42, "af:fix", "job_2", true, SeenOpen)
	otherLabel := add("widgets", 42, "af:triage", "job_3", true, SeenEnded)
	otherIssue := add("widgets", 43, "af:fix", "job_4", true, SeenEnded)
	otherWorkspace := add("gadgets", 42, "af:fix", "job_5", true, SeenEnded)
	noJob := add("widgets", 44, "af:fix", "", true, SeenEnded)

	found, err := records.Find(ctx, "widgets", 42, "af:fix")
	if err != nil || len(found) != 2 || found[0].ID != owedDone.ID || found[1].ID != owedOpen.ID {
		t.Fatalf("Find = %+v, %v, want the two records of widgets, 42 and af:fix in order", found, err)
	}

	n, err := records.ClearOwed(ctx, "widgets", 42, "af:fix")
	if err != nil || n != 2 {
		t.Fatalf("ClearOwed = %d, %v, want the two records of the issue and the label", n, err)
	}
	for _, r := range []Record{otherLabel, otherIssue, otherWorkspace, noJob} {
		got, err := records.Find(ctx, r.Workspace, r.Issue, r.Label)
		if err != nil || len(got) != 1 || !got[0].Owed {
			t.Errorf("the record of %s %d %s is %+v, %v, want it still owed", r.Workspace, r.Issue, r.Label, got, err)
		}
	}

	// Dropped: nothing is owed and nothing holds. The done record, and the others once they are cleared; not the one
	// whose job is open, and not an owed one.
	if n, err := records.Drop(ctx, "widgets"); err != nil || n != 1 {
		t.Fatalf("Drop = %d, %v, want only the record that is ended with nothing owed", n, err)
	}
	if got, _ := records.ForWorkspace(ctx, "widgets"); len(got) != 5-1 {
		t.Errorf("widgets has %d records left, want 4", len(got))
	}
	if _, err := records.ClearOwed(ctx, "widgets", 44, "af:fix"); err != nil {
		t.Fatal(err)
	}
	if err := records.SetSeen(ctx, otherIssue.ID, SeenHolding); err != nil {
		t.Fatal(err)
	}
	if _, err := records.ClearOwed(ctx, "widgets", 43, "af:fix"); err != nil {
		t.Fatal(err)
	}
	if n, err := records.Drop(ctx, "widgets"); err != nil || n != 1 {
		t.Fatalf("Drop = %d, %v, want the record with no job, whose removal is confirmed, and not the one that holds", n, err)
	}
	left, _ := records.ForWorkspace(ctx, "widgets")
	var ids []int64
	for _, r := range left {
		ids = append(ids, r.ID)
	}
	if want := []int64{owedOpen.ID, otherLabel.ID, otherIssue.ID}; !slices.Equal(ids, want) {
		t.Errorf("the records left are %v, want the open one, the owed one and the one that holds: %v", ids, want)
	}
	if got, _ := records.ForWorkspace(ctx, "gadgets"); len(got) != 1 || got[0].ID != otherWorkspace.ID {
		t.Errorf("gadgets has %+v, want its own record untouched", got)
	}

	if err := records.Delete(ctx, otherLabel.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := records.Find(ctx, "widgets", 42, "af:triage"); len(got) != 0 {
		t.Errorf("the record is still there after Delete: %+v", got)
	}
}
