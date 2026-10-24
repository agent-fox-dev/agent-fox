package reconcile_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/service/reconcile"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// TestThePassReleasesTheOrphanAndKeepsTheOtherLeasesOverTheRealJobsLayer_TS_14_68 verifies
// TS-14-68 and 14-PATH-7: over the real store and the real jobs layer, whose runner registry knows an attempt that is
// being carried out, the fake hub holds four leases of a workspace (one for a known attempt with no runner, one for a
// job the database does not have, one without labels and one for the attempt in the runner), a second workspace that
// answers 5xx. The first pass releases the orphan only, and logs the failing workspace without its token; the second, after the failure clears, releases what that
// workspace missed and does nothing else.
// Verifies: 14-PATH-7, 14-REQ-8.2, 14-REQ-8.3, 14-REQ-8.4, 14-REQ-8.6
func TestThePassReleasesTheOrphanAndKeepsTheOtherLeasesOverTheRealJobsLayer_TS_14_68(t *testing.T) {
	fh := fakehub.New(t)
	fh.AddWorkspace(t, fakehub.Workspace{Slug: acme, PAT: patAcme})
	fh.AddWorkspace(t, fakehub.Workspace{Slug: beta, PAT: patBeta})
	key, err := secrets.NewKey(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for slug, pat := range map[string]string{acme: patAcme, beta: patBeta} {
		if _, _, err := st.Upsert(ctx, slug, pat, "usr_1", "usr_1", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	logs := &syncBuffer{}
	fake := fakerunner.New(fakerunner.Done(), fakerunner.Block(fakerunner.Done()), fakerunner.Done())
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: slog.New(slog.NewJSONHandler(logs, nil)), Workers: 3, AttemptsDir: filepath.Join(dir, "attempts"),
		Runner: fake, PollInterval: 5 * time.Millisecond, RetryDelay: time.Millisecond, Retain: 100 * 365 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.ReleaseAll(); svc.Close() })

	create := func(workspace string, issue int) string {
		t.Helper()
		doc, err := svc.Create(ctx, jobs.CreateRequest{
			Workspace: workspace, Kind: jobs.KindFix, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))}, Source: jobs.Source{Kind: "api", By: "usr_1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return doc.ID
	}
	await := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); !cond(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("gave up waiting for %s", what)
			}
		}
	}
	done := func(id string) func() bool {
		return func() bool { doc, err := svc.Get(ctx, id); return err == nil && doc.State == jobs.StateDone }
	}
	ended := create(acme, 1)
	await("the first job to be done", done(ended))
	running := create(acme, 2)
	await("the second job to be in the runner", func() bool { return svc.Running(running, 1) })
	endedBeta := create(beta, 3)
	await("the job of the other workspace to be done", done(endedBeta))

	// The leases of the world.
	client := hub.New(fh.URL, "1")
	label := func(job string) map[string]string { return map[string]string{"job": job, "attempt": "1"} }
	orphan := fh.SeedLease(acme, label(ended), "fix")
	keepers := []string{
		fh.SeedLease(acme, label("job_00000000000000000000"), "fix").ID, // a job the database does not have
		fh.SeedLease(acme, nil, "fix").ID,                               // none of ours
		fh.SeedLease(acme, label(running), "fix").ID,                    // an attempt that is in the runner
	}
	orphanBeta := fh.SeedLease(beta, label(endedBeta), "fix")
	fh.Inject("GET", fmt.Sprintf(sandboxes, beta), fakehub.Fault{Status: 503})

	rec := reconcile.New(reconcile.Config{Subscriptions: st, Hub: client, Jobs: svc, Logger: slog.New(slog.NewJSONHandler(logs, nil))})
	rec.Pass(ctx)

	if got := fh.Deleted(); !slices.Equal(got, []string{orphan.ID}) {
		t.Fatalf("the hub released %v, want the orphan alone: %s", got, orphan.ID)
	}
	held, err := client.ListSandboxes(ctx, patAcme, acme)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range keepers {
		if !slices.ContainsFunc(held, func(l hub.LeaseInfo) bool { return l.ID == id }) {
			t.Errorf("the lease %s is not held any more", id)
		}
	}
	warned := false
	for _, rec := range logs.level("WARN") {
		warned = warned || rec["workspace"] == beta
	}
	if !warned || strings.Contains(logs.String(), patAcme) || strings.Contains(logs.String(), patBeta) {
		t.Errorf("the log %s, want a WARN that names %s and no token", logs.String(), beta)
	}

	fh.Clear()
	rec.Pass(ctx)

	if got := fh.Deleted(); !slices.Equal(got, []string{orphan.ID, orphanBeta.ID}) {
		t.Errorf("the hub released %v, want the orphan of the workspace that failed released by the second pass, and no other", got)
	}
}
