package labelcleanup_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakeclock"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/labelcleanup"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestCancel_OfABlockedJobOfALabelPutsTheLabelsBack runs the cleaner as the service does, in the real jobs layer over the
// state file: the cancel of a job the poll made, blocked with needs-human, takes needs-human off and puts af:fix back.
func TestCancel_OfABlockedJobOfALabelPutsTheLabelsBack(t *testing.T) {
	w := newWorld(t)
	w.issue("af:needs-human")
	key, _ := secrets.NewKey(testutil.Key())
	dir := t.TempDir()
	st, err := store.Open(t.Context(), filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, _, _ = st.Upsert(t.Context(), "widgets", pat, "", "usr_1", time.Now())
	blocked := fakerunner.Return(jobs.Result{
		Envelope:  fakerunner.Env(toolio.StatusNeedsHuman, fakerunner.WithQuestion("Which?", "a")),
		LabelsSet: []string{"af:needs-human"}, LabelsRemoved: []string{"af:in-progress"},
	})
	logs := &testutil.Log{}
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: logs.Logger(), Clock: fakeclock.New(time.Now()), Workers: 1,
		AttemptsDir: filepath.Join(dir, "attempts"), Runner: fakerunner.New(blocked), PollInterval: 5 * time.Millisecond,
		Cleaner: &labelcleanup.Cleaner{Subscriptions: st, Hub: w.cleaner.Hub},
	})
	if err == nil {
		err = svc.Start()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	body := jobs.Body{Issue: json.RawMessage("42")}
	job, err := svc.Create(t.Context(), jobs.CreateRequest{Workspace: "widgets", Kind: jobs.KindFix, Body: body, Source: jobs.Source{Kind: "label", By: "poll"}, RequestLabel: "af:fix"})
	if err != nil {
		t.Fatal(err)
	}
	doc := func() jobs.Doc { d, _ := svc.Get(t.Context(), job.ID); return d }
	testutil.Eventually(t, "the job to be blocked", func() bool { return doc().State == jobs.StateBlocked })
	if _, err := svc.Cancel(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "the cleanup to be done", func() bool {
		d := doc()
		return d.Labels != nil && d.Labels.Cleanup != nil && *d.Labels.Cleanup == "done"
	})
	if got := w.labels(); !slices.Equal(got, []string{"af:fix"}) {
		t.Errorf("the issue carries %v, want af:fix and no status label", got)
	}
	if l := doc().Labels; !slices.Equal(l.Set, []string{"af:fix"}) || !slices.Equal(l.Removed, []string{"af:needs-human"}) || l.Restore != nil {
		t.Errorf("labels = %+v, want af:fix set, af:needs-human removed and nothing left to restore", l)
	}
	if recs := logs.Find("INFO", "cancel cleanup"); len(recs) != 1 || recs[0]["job"] != job.ID || recs[0]["issue"] != float64(42) {
		t.Errorf("the cleanup is logged as %v, want one INFO record of the job and the issue", recs)
	}
}
