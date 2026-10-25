package poll

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func at(minutes int) time.Time { return epoch.Add(time.Duration(minutes) * time.Minute) }

func sameRecord(a, b Record) bool {
	return a.ID == b.ID && a.Workspace == b.Workspace && a.Issue == b.Issue && a.Label == b.Label && a.JobID == b.JobID &&
		a.Owed == b.Owed && a.Seen == b.Seen && a.ReleaseClock.Equal(b.ReleaseClock) && a.CreatedAt.Equal(b.CreatedAt)
}

// A record names a job that the state file need not hold, since retention purges jobs and the records outlive them.
func TestRecords_AreReadBackWholeAfterAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	st := openStore(t, path)
	records := NewRecords(st.DB())
	held, err := records.Insert(ctx, Record{Workspace: slug, Issue: 42, Label: "af:fix", JobID: "job_purged", Owed: true, CreatedAt: at(0)})
	if err != nil {
		t.Fatal(err)
	}
	absorbed, err := records.Insert(ctx, Record{Workspace: slug, Issue: 7, Label: "af:triage", Seen: SeenEnded, ReleaseClock: at(2), CreatedAt: at(1)})
	if err != nil || absorbed.ID <= held.ID || held.Seen != SeenOpen {
		t.Fatalf("Insert = %+v, %v after %+v: want ids in order and an empty Seen stored as open", absorbed, err, held)
	}
	if err := records.SetSeen(ctx, held.ID, SeenHolding); err != nil {
		t.Fatal(err)
	}
	for _, clock := range []time.Time{at(5), at(9)} { // the clock starts once
		if err := records.StartClock(ctx, held.ID, clock); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	reopened := openStore(t, path)
	defer reopened.Close()
	got, err := NewRecords(reopened.DB()).ForWorkspace(ctx, slug)
	want := []Record{
		{ID: held.ID, Workspace: slug, Issue: 42, Label: "af:fix", JobID: "job_purged", Owed: true, Seen: SeenHolding, ReleaseClock: at(5), CreatedAt: at(0)},
		{ID: absorbed.ID, Workspace: slug, Issue: 7, Label: "af:triage", Seen: SeenEnded, ReleaseClock: at(2), CreatedAt: at(1)},
	}
	if err != nil || !slices.EqualFunc(got, want, sameRecord) {
		t.Errorf("after a reopen the records are %+v, %v, want %+v", got, err, want)
	}
	if last, err := NewRecords(reopened.DB()).Last(ctx); err != nil || last != absorbed.ID {
		t.Errorf("Last = %d, %v, want %d", last, err, absorbed.ID)
	}
}

func TestRecords_FindClearOwedDropAndDeleteKeepToTheirWorkspaceIssueAndLabel(t *testing.T) {
	st := openStore(t, filepath.Join(t.TempDir(), "agent-fox.db"))
	defer st.Close()
	records := NewRecords(st.DB())
	if last, err := records.Last(ctx); err != nil || last != 0 {
		t.Errorf("Last of no records = %d, %v, want 0", last, err)
	}
	add := func(workspace string, issue int, label, job string, owed bool, seen Seen) Record {
		r, err := records.Insert(ctx, Record{Workspace: workspace, Issue: issue, Label: label, JobID: job, Owed: owed, Seen: seen, CreatedAt: at(0)})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	ended := add(slug, 42, "af:fix", "job_1", true, SeenEnded)
	opened := add(slug, 42, "af:fix", "job_2", true, SeenOpen)
	otherLabel := add(slug, 42, "af:triage", "", true, SeenEnded)
	holding := add(slug, 43, "af:fix", "job_4", false, SeenHolding)
	gadgets := add("gadgets", 42, "af:fix", "", false, SeenEnded)

	if found, err := records.Find(ctx, slug, 42, "af:fix"); err != nil || len(found) != 2 || found[0].ID != ended.ID || found[1].ID != opened.ID {
		t.Fatalf("Find = %+v, %v, want the two records of 42 and af:fix in order", found, err)
	}
	if n, err := records.ClearOwed(ctx, slug, 42, "af:fix"); err != nil || n != 2 {
		t.Fatalf("ClearOwed = %d, %v, want the two records of the issue and the label", n, err)
	}
	// Nothing is owed and nothing holds: the ended record goes; the open job, the owed label and the hold stay.
	if n, err := records.Drop(ctx, slug); err != nil || n != 1 {
		t.Fatalf("Drop = %d, %v, want the ended record", n, err)
	}
	if err := records.Delete(ctx, holding.ID); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, workspace := range []string{slug, "gadgets"} {
		left, err := records.ForWorkspace(ctx, workspace)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range left {
			ids = append(ids, r.ID)
		}
	}
	if want := []int64{opened.ID, otherLabel.ID, gadgets.ID}; !slices.Equal(ids, want) {
		t.Errorf("the records left are %v, want %v", ids, want)
	}
	if err := records.SetSeen(ctx, holding.ID, SeenEnded); !errors.Is(err, errNoRecord) {
		t.Errorf("SetSeen of a deleted record = %v, want errNoRecord", err)
	}
}
