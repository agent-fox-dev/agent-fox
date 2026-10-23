package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// stopAfter runs the first delivery of the fix of issue 42 and stops it after the stage, as a process that
// died there is stopped.
func (e *env) stopAfter(stage string, mutate ...func(*jobs.Request)) {
	e.t.Helper()
	e.runner.abortAfter = stage
	_, err := e.runner.Run(context.Background(), e.request(jobs.KindFix, issueInput("42"), mutate...))
	e.runner.abortAfter = ""
	var stopped stageAbort
	name, _, _ := strings.Cut(stage, "#")
	if !errors.As(err, &stopped) || stopped.stage != name {
		e.t.Fatalf("the first delivery returned %v, want it stopped after %s", err, stage)
	}
}

// deliver runs the delivery n of the fix of issue 42, within half an hour: the loop of a fix gives a
// second round only the time that the deadline leaves, and the two minutes of run are too few for it.
func (e *env) deliver(n int, mutate ...func(*jobs.Request)) (jobs.Result, error) {
	e.t.Helper()
	mutate = append([]func(*jobs.Request){func(r *jobs.Request) { r.Delivery = n }}, mutate...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	return e.runner.Run(ctx, e.request(jobs.KindFix, issueInput("42"), mutate...))
}

// spyOnGit records the git commands of the attempts the environment runs.
func (e *env) spyOnGit() *gitSpy {
	spy := &gitSpy{}
	e.runner.gitWrap = spy.wrap
	return spy
}

// clonesOf is how many clones the attempts made.
func clonesOf(spy *gitSpy) int { return len(spy.with("clone")) }

// sessionOpens are the sessions that were opened on hub.
func sessionOpens(e *env) int { return len(e.hub.RequestsTo("POST", sessionsPath)) }

// editJournal changes the journal of the attempt the way a crash, a person or a later release could.
func editJournal(t *testing.T, e *env, change func(*recovery.Journal)) {
	t.Helper()
	store := recovery.NewStore(e.dir)
	j, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	change(&j)
	if err := store.Write(j); err != nil {
		t.Fatal(err)
	}
}

// warningOf is the first warning of the envelope with the code, or nil.
func warningOf(env map[string]any, code string) map[string]any {
	list, _ := env["warnings"].([]any)
	for _, w := range list {
		if m, _ := w.(map[string]any); m["code"] == code {
			return m
		}
	}
	return nil
}

// settingsSeen makes the loop of the environment report the settings it is started with.
func (e *env) settingsSeen() *Settings {
	var seen Settings
	flags := e.runner.loopFlags
	e.runner.loopFlags = func(a *attempt) []string {
		seen = a.settings
		return flags(a)
	}
	return &seen
}

// runDirOf is the run directory of the attempt: the one of the session's run id.
func (e *env) runDirOf() string {
	return filepath.Join(e.dir, cloneDirName, ".agent-fox", "run", e.hub.Sessions()[0].RunID)
}

// TestAFirstDeliveryRemovesALeftoverCloneAndJournalAndStartsFresh_TS_14_8 verifies TS-14-8.
// Verifies: 14-REQ-2.1
func TestAFirstDeliveryRemovesALeftoverCloneAndJournalAndStartsFresh_TS_14_8(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	spy := e.spyOnGit()
	req := e.request(jobs.KindFix, issueInput("42"))
	if err := os.MkdirAll(filepath.Join(e.dir, cloneDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, cloneDirName, "stale.txt"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := recovery.Journal{
		Version: recovery.FormatVersion, Job: req.JobID, Attempt: req.Attempt, Kind: "fix", CloneDir: cloneDirName,
		Session: recovery.Session{ID: "ses_STALE", RunID: "20200101_000000_aaaaaa"},
	}
	if err := recovery.NewStore(e.dir).Write(stale); err != nil {
		t.Fatal(err)
	}
	staleOutcome := filepath.Join(e.dir, recovery.DirName, recovery.OutcomeFile)
	if err := os.WriteFile(staleOutcome, []byte(`{"stale": "STALE-OUTCOME"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := e.deliver(1)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if n := clonesOf(spy); n != 1 {
		t.Errorf("%d clones, want 1", n)
	}
	if n := sessionOpens(e); n != 1 || e.hub.Sessions()[0].ID == "ses_STALE" {
		t.Errorf("%d sessions opened, want one that is not the stale one", n)
	}
	if got := journalOf(t, e); got.Session.ID == "ses_STALE" || len(got.Deliveries) != 1 {
		t.Errorf("the journal still holds the stale one: session %+v, %d deliveries", got.Session, len(got.Deliveries))
	}
	if codes := codes(envelope(t, res)); slices.Contains(codes, "resume_discarded") {
		t.Errorf("a first delivery raised resume_discarded: %v", codes)
	}
	if data, err := os.ReadFile(staleOutcome); err == nil && strings.Contains(string(data), "STALE-OUTCOME") {
		t.Error("the stored outcome of an earlier attempt is still in recovery/")
	}
}

// TestALaterDeliveryWithNothingToDiscardStartsFreshWithoutAWarningAndReusesAnOpenSession_TS_14_9
// verifies TS-14-9.
// Verifies: 14-REQ-2.2
func TestALaterDeliveryWithNothingToDiscardStartsFreshWithoutAWarningAndReusesAnOpenSession_TS_14_9(t *testing.T) {
	t.Run("a transient error at resolve left neither a journal nor a clone", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		spy := e.spyOnGit()
		e.hub.Inject("GET", "/api/v1/workspaces/acme/vars/resolved", fakehub.Fault{Status: 503, Once: true})
		if _, err := e.deliver(1); !jobs.IsTransient(err) {
			t.Fatalf("the first delivery returned %v, want a transient error", err)
		}
		if _, err := recovery.NewStore(e.dir).Load(); !errors.Is(err, recovery.ErrNotFound) {
			t.Fatalf("the journal after a failure at resolve: %v, want none", err)
		}

		res, err := e.deliver(2)

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if codes := codes(envelope(t, res)); slices.Contains(codes, "resume_discarded") {
			t.Errorf("the warnings %v hold resume_discarded", codes)
		}
		if n := clonesOf(spy); n != 1 {
			t.Errorf("%d clones, want 1", n)
		}
	})

	t.Run("a journal that records a session and no completed clone, and a partial clone", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		spy := e.spyOnGit()
		e.stopAfter("session")
		if j := journalOf(t, e); j.Session.ID == "" || j.CloneDir != "" {
			t.Fatalf("journal = %+v, want a session and no completed clone", j)
		}
		partial := filepath.Join(e.dir, cloneDirName, "partial.txt")
		if err := os.MkdirAll(filepath.Dir(partial), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(partial, []byte("half a clone"), 0o600); err != nil {
			t.Fatal(err)
		}
		first := e.hub.Sessions()[0]

		res, err := e.deliver(2)

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		if codes := codes(envelope(t, res)); slices.Contains(codes, "resume_discarded") {
			t.Errorf("the warnings %v hold resume_discarded", codes)
		}
		if _, err := os.Stat(partial); err == nil {
			t.Error("the partial clone is still there")
		}
		if n := clonesOf(spy); n != 1 {
			t.Errorf("%d clones, want 1", n)
		}
		if sessions := e.hub.Sessions(); len(sessions) != 1 || sessions[0].ID != first.ID || sessions[0].Status != "completed" {
			t.Errorf("sessions = %+v, want the one the journal recorded, completed, and no other", sessions)
		}
	})
}

// TestAResumedDeliveryKeepsTheSettingsTheRunStartedWithWhateverChangedOnHub_TS_14_10 verifies
// TS-14-10: the clone is not made again; the brain, the model, the turn bound and the landing mode are the
// journal's, the base is the pinned file's; the timeout is read afresh and AF_MAX_ATTEMPTS is the current
// value.
// Verifies: 14-REQ-2.3
func TestAResumedDeliveryKeepsTheSettingsTheRunStartedWithWhateverChangedOnHub_TS_14_10(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "3", "AF_MODEL": "model-one"}, nil, fixSteps()...)
	spy := e.spyOnGit()
	e.stopAfter("applied")
	pinned := journalOf(t, e).Settings
	if pinned.Model != "model-one" || pinned.Budget != 3 || pinned.Land != "pr" {
		t.Fatalf("the journal pins %+v", pinned)
	}
	e.hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) {
		w.Vars = map[string]string{
			"AF_MAX_BUDGET_USD": "0.01", "AF_MODEL": "model-two", "AF_MAX_TURNS": "1", "AF_FIX_LAND": "none",
			"AF_TIMEOUT": "20m", "AF_MAX_ATTEMPTS": "7",
		}
		w.Branch = "trunk"
	})
	seen := e.settingsSeen()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if n := clonesOf(spy); n != 1 {
		t.Errorf("%d clones, want only the first delivery's", n)
	}
	result := resultMap(t, res)
	if result["land"] != "pr" || result["base"] != "main" || result["pull_request"] == nil {
		t.Errorf("the run landed as %v on %v with %v, want the pinned landing mode and base, and a pull request", result["land"], result["base"], result["pull_request"])
	}
	// No brain runs in the second delivery (the tree was applied), so the bounds are the pinned ones; the 1 turn
	// that hub says now is not among them.
	if seen.Brain != pinned.Brain || seen.Model != "model-one" || seen.MaxTurns != pinned.MaxTurns || seen.Land != "pr" || seen.Budget != 3 {
		t.Errorf("the loop ran with %+v, want the settings of the journal", *seen)
	}
	if seen.Timeout != 20*time.Minute || res.MaxAttempts != 7 {
		t.Errorf("timeout %s and most attempts %d, want the current 20m and 7", seen.Timeout, res.MaxAttempts)
	}
	if got := journalOf(t, e).Settings; got != pinned {
		t.Errorf("the journal settings changed to %+v", got)
	}
}

// TestTheRefusalsOfResolveApplyToTheCurrentValuesOnAResumedDelivery_TS_14_11 verifies TS-14-11.
// Verifies: 14-REQ-2.3
func TestTheRefusalsOfResolveApplyToTheCurrentValuesOnAResumedDelivery_TS_14_11(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("applied")
	leases := len(e.hub.Leases())
	e.hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) { w.Mode = "carry_patch" })

	res, err := e.deliver(2)

	if err != nil {
		t.Fatal(err)
	}
	if category, _, retryable, _ := errorOf(envelope(t, res)); category != categoryUnsupported || retryable {
		t.Errorf("envelope %s, want the refusal of carry-patch landing", res.Envelope)
	}
	if got := len(e.hub.Leases()); got != leases {
		t.Errorf("%d leases, want the %d of the first delivery: a refused delivery takes none", got, leases)
	}
}

// TestEachRejectedResumeConditionRaisesResumeDiscardedWithItsReasonAndStartsFromAFreshClone_TS_14_12
// verifies TS-14-12.
// Verifies: 14-REQ-2.4
func TestEachRejectedResumeConditionRaisesResumeDiscardedWithItsReasonAndStartsFromAFreshClone_TS_14_12(t *testing.T) {
	for i, tc := range []struct {
		reason string
		break_ func(t *testing.T, e *env)
		// known says the journal reads, so that the session and the lease it records are the delivery's.
		known bool
	}{
		{"journal_unreadable", func(t *testing.T, e *env) {
			if err := os.WriteFile(filepath.Join(e.dir, recovery.DirName, recovery.JournalFile), []byte("{garbage"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"journal_unreadable", func(t *testing.T, e *env) { // a journal that reads and is of another job
			editJournal(t, e, func(j *recovery.Journal) { j.Job = "job_ffffffffffffffffffff" })
		}, false},
		{"journal_unreadable", func(t *testing.T, e *env) { // a journal that reads and is of another attempt
			editJournal(t, e, func(j *recovery.Journal) { j.Attempt = 2 })
		}, false},
		{"clone_invalid", func(t *testing.T, e *env) {
			if err := os.RemoveAll(filepath.Join(e.dir, cloneDirName, ".git")); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"clone_invalid", func(t *testing.T, e *env) { // a directory in a work tree that is not its top level
			if err := os.RemoveAll(filepath.Join(e.dir, cloneDirName, ".git")); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("git", "-C", e.dir, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v\n%s", err, out)
			}
		}, true},
		{"origin_changed", func(t *testing.T, e *env) {
			out, err := exec.Command("git", "-C", filepath.Join(e.dir, cloneDirName), "remote", "set-url", "origin", "https://elsewhere.invalid/git/acme.git").CombinedOutput()
			if err != nil {
				t.Fatalf("git remote set-url: %v\n%s", err, out)
			}
		}, true},
		{"forge_repository_changed", func(t *testing.T, e *env) {
			editJournal(t, e, func(j *recovery.Journal) { j.Forge = "Other/Repo" })
		}, true},
		{"run_missing", func(t *testing.T, e *env) {
			if err := os.RemoveAll(e.runDirOf()); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"ledger_unreadable", func(t *testing.T, e *env) {
			f, err := os.OpenFile(filepath.Join(e.runDirOf(), "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteString("this is not a ledger line\n"); err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(fmt.Sprintf("%s %d", tc.reason, i), func(t *testing.T) {
			e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
			spy := e.spyOnGit()
			e.stopAfter("briefed")
			tc.break_(t, e)
			clones := clonesOf(spy)

			res, err := e.deliver(2)

			if err != nil || envelope(t, res)["status"] != "done" {
				t.Fatalf("result %s, error %v", res.Envelope, err)
			}
			w := warningOf(envelope(t, res), "resume_discarded")
			if w == nil || !strings.Contains(w["message"].(string), tc.reason) {
				t.Errorf("the warnings %v hold %v, want resume_discarded naming %s", codes(envelope(t, res)), w, tc.reason)
			}
			if n := clonesOf(spy) - clones; n != 1 {
				t.Errorf("%d clones in the second delivery, want 1", n)
			}
			if j := journalOf(t, e); j.Attempt != 1 {
				t.Errorf("the journal is of attempt %d, want the same attempt", j.Attempt)
			}
			leases, sessions := e.hub.Leases(), e.hub.Sessions()
			if tc.known {
				if !leases[0].Released {
					t.Error("the lease of the first delivery was not released")
				}
				if len(sessions) != 1 || sessions[0].Status != "completed" {
					t.Errorf("sessions = %+v, want the one the journal recorded, completed, and none opened", sessions)
				}
			} else if len(sessions) != 2 || sessions[0].Status != "open" {
				t.Errorf("sessions = %+v, want a new session and the first left open: nothing of the first delivery is known", sessions)
			}
		})
	}
}

// TestAnUnreadableLedgerDiscardsTheResumeButTheSpendTheSessionAndTheRunIDCarryOver_TS_14_13
// verifies TS-14-13.
// Verifies: 14-REQ-2.4, 14-REQ-4.5
func TestAnUnreadableLedgerDiscardsTheResumeButTheSpendTheSessionAndTheRunIDCarryOver_TS_14_13(t *testing.T) {
	e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "5", "AF_MAX_TURNS": "50"}, nil, fixSteps()...)
	e.stopAfter("applied")
	editJournal(t, e, func(j *recovery.Journal) {
		j.Spend = recovery.Spend{CostUSD: 0.40, CostSource: "reported", InputTokens: 900, OutputTokens: 100, Turns: 6}
	})
	first := e.hub.Sessions()[0]
	f, err := os.OpenFile(filepath.Join(e.runDirOf(), "ledger.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("torn\n")
	_ = f.Close()
	seen := e.settingsSeen()

	res, err := e.deliver(2)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	if w := warningOf(envelope(t, res), "resume_discarded"); w == nil || !strings.Contains(w["message"].(string), "ledger_unreadable") {
		t.Errorf("the warnings %v, want resume_discarded for ledger_unreadable", codes(envelope(t, res)))
	}
	if seen.Budget < 4.599 || seen.Budget > 4.601 || seen.MaxTurns != 44 {
		t.Errorf("the brain was given $%.2f and %d turns, want the pinned 5 and 50 less the 0.40 and 6 of the first delivery", seen.Budget, seen.MaxTurns)
	}
	usage, _ := envelope(t, res)["usage"].(map[string]any)
	if cost, _ := usage["cost_usd"].(float64); cost < 0.399 {
		t.Errorf("usage = %v, want the 0.40 of the first delivery in it", usage)
	}
	sessions := e.hub.Sessions()
	if len(sessions) != 1 || sessions[0].ID != first.ID || sessions[0].RunID != first.RunID || sessions[0].Status != "completed" {
		t.Errorf("sessions = %+v, want the first delivery's session, with its run id, completed, and none other", sessions)
	}
	if j := journalOf(t, e); j.Carried.CostUSD < 0.399 || j.Run.ID != first.RunID {
		t.Errorf("journal: carried %+v, run %+v, want the spend carried and the run id kept", j.Carried, j.Run)
	}
}

// TestAResumedDeliveryReportsToTheOpenSessionOfTheJournalOrOpensOneWhenItWasCompleted_TS_14_14
// verifies TS-14-14.
// Verifies: 14-REQ-2.5
func TestAResumedDeliveryReportsToTheOpenSessionOfTheJournalOrOpensOneWhenItWasCompleted_TS_14_14(t *testing.T) {
	t.Run("an open session", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.stopAfter("applied")
		first := e.hub.Sessions()[0]

		if _, err := e.deliver(2); err != nil {
			t.Fatal(err)
		}

		if n := sessionOpens(e); n != 1 {
			t.Errorf("%d sessions opened, want none by the second delivery", n)
		}
		if got := e.sink.lastHub(); got[0] != first.ID || got[1] != first.RunID || got[2] == "" {
			t.Errorf("the recorder holds %v, want the session %s, its run %s and the sandbox", got, first.ID, first.RunID)
		}
		if s := e.hub.Sessions()[0]; s.Status != "completed" || len(s.Usage) != 1 {
			t.Errorf("session %+v, want it completed with the one usage record", s)
		}
	})

	t.Run("a session that was completed", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.stopAfter("applied")
		first := e.hub.Sessions()[0]
		editJournal(t, e, func(j *recovery.Journal) {
			j.Session.Completed, j.Report.SessionCompleted, j.Report.UsagePosted = true, true, false
		})

		if _, err := e.deliver(2); err != nil {
			t.Fatal(err)
		}

		sessions := e.hub.Sessions()
		if len(sessions) != 2 || sessions[1].RunID != first.RunID {
			t.Fatalf("sessions = %+v, want a second one opened with the run id of the first", sessions)
		}
		if got := e.sink.lastHub(); got[0] != sessions[1].ID || got[1] != first.RunID {
			t.Errorf("the recorder holds %v, want the new session %s", got, sessions[1].ID)
		}
		if len(sessions[1].Usage) != 1 || len(sessions[0].Usage) != 0 {
			t.Errorf("usage records: %d on the new session and %d on the old one, want the usage posted to the new one alone", len(sessions[1].Usage), len(sessions[0].Usage))
		}
		if sessions[1].Status != "completed" || sessions[1].CompletedSeq == 0 {
			t.Errorf("the new session is %q, want it completed by the delivery that reported to it", sessions[1].Status)
		}
		if j := journalOf(t, e); j.Session.ID != sessions[1].ID || !j.Session.Completed || !j.Report.SessionCompleted {
			t.Errorf("the journal records the session %+v and the report %+v, want the new session, completed", j.Session, j.Report)
		}
	})
}

// TestAResumedOrDiscardedDeliveryReleasesTheLeasesTheJournalRecordsBeforeItTakesItsOwn_TS_14_15
// verifies TS-14-15.
// Verifies: 14-REQ-2.6
func TestAResumedOrDiscardedDeliveryReleasesTheLeasesTheJournalRecordsBeforeItTakesItsOwn_TS_14_15(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("lease")
	const gone = "sbx_00000000000000000000000000000000" // hub does not know it: it answers 404
	editJournal(t, e, func(j *recovery.Journal) {
		j.Leases = append(j.Leases, recovery.Lease{SandboxID: gone, ExpiresAt: time.Now().Add(time.Hour)})
	})
	held := e.hub.Leases()[0].ID

	if _, err := e.deliver(2); err != nil {
		t.Fatal(err)
	}

	deleted := func(id string) int {
		for _, r := range e.hub.RequestsTo("DELETE", sandboxesPath+"/"+id) {
			return r.Seq
		}
		return 0
	}
	// The lease of the second delivery is the last that was asked for.
	posts := e.hub.RequestsTo("POST", sandboxesPath)
	second := posts[len(posts)-1].Seq
	if deleted(held) == 0 || deleted(gone) == 0 || deleted(held) > second || deleted(gone) > second {
		t.Errorf("the releases of %s and %s are at %d and %d, and the lease was asked for at %d: want both before it", held, gone, deleted(held), deleted(gone), second)
	}
	j := journalOf(t, e)
	for _, l := range j.Leases {
		if !l.Released {
			t.Errorf("lease %s is recorded as not released", l.SandboxID)
		}
	}
	if len(j.Leases) != 3 {
		t.Errorf("the journal records %d leases, want the two of the first delivery and the one of the second", len(j.Leases))
	}
}

// TestADisagreementTheResumeRulesDoNotCoverEndsTheAttemptFailedWithCategoryInternal_TS_14_16
// verifies TS-14-16.
// Verifies: 14-REQ-2.7
func TestADisagreementTheResumeRulesDoNotCoverEndsTheAttemptFailedWithCategoryInternal_TS_14_16(t *testing.T) {
	check := func(t *testing.T, e *env, res jobs.Result, err error, found string) {
		t.Helper()
		if err != nil {
			t.Fatalf("error = %v, want an envelope", err)
		}
		env := envelope(t, res)
		category, message, retryable, _ := errorOf(env)
		if env["status"] != "failed" || category != "internal" || retryable || !strings.Contains(message, found) {
			t.Errorf("envelope %s, want a failed one of category internal, not retryable, that names %q", res.Envelope, found)
		}
		if n := receivePacks(e); n != 0 {
			t.Errorf("%d pushes, want none", n)
		}
		if pulls := e.hub.Pulls(slug); len(pulls) != 0 {
			t.Errorf("%d pull requests, want none", len(pulls))
		}
		leases := e.hub.Leases()
		for _, l := range leases {
			if !l.Released {
				t.Errorf("the lease %s is held after the attempt: cleaned up as for any failed attempt", l.ID)
			}
		}
	}

	t.Run("the base branch has a commit without the trailer of the run", func(t *testing.T) {
		e := loopEnv(t, fixFiles, map[string]string{"AF_MAX_BUDGET_USD": "1.00"}, nil, fixSteps()...)
		e.usePricing(scriptFile(t, fixSteps()...), map[int]pricedRun{1: {cost: 0.25, turns: 5, costSource: "reported"}})
		e.stopAfter("applied")
		for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=x", "-c", "user.email=x@example.com", "commit", "-q", "-m", "a commit of somebody else"}} {
			if out, err := exec.Command("git", append([]string{"-C", filepath.Join(e.dir, cloneDirName)}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		res, err := e.deliver(2)

		check(t, e, res, err, "Agent-Fox-Run")
		// The attempt has spent what its first delivery did, though this one ended before it ran anything.
		if usage, _ := envelope(t, res)["usage"].(map[string]any); !near(usage["cost_usd"].(float64), 0.25) || usage["turns"] != float64(5) {
			t.Errorf("the envelope's usage is %v, want the spend of the first delivery", usage)
		}
	})

	t.Run("the branch of the run is on hub's git server at another commit", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.stopAfter("plan_recorded")
		plan := journalOf(t, e).Landing.Plan
		if plan == nil {
			t.Fatal("the journal holds no plan")
		}
		// Somebody put the branch on hub's git server at the commit the run began at.
		bare := e.hub.GitDir(slug)
		head, err := exec.Command("git", "-C", bare, "rev-parse", "main").Output()
		if err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("git", "-C", bare, "branch", plan.Branch, strings.TrimSpace(string(head))).CombinedOutput(); err != nil {
			t.Fatalf("git branch: %v\n%s", err, out)
		}

		res, err := e.deliver(2)

		check(t, e, res, err, plan.Branch)
	})
}

// TestTheSpendThatOutlivesADiscardedCloneIsTheLargerOfWhatTheJournalAndTheLedgerSay_TS_14_26
// verifies TS-14-26.
// Verifies: 14-REQ-4.5
func TestTheSpendThatOutlivesADiscardedCloneIsTheLargerOfWhatTheJournalAndTheLedgerSay_TS_14_26(t *testing.T) {
	spend := func(cost float64) recovery.Spend {
		return recovery.Spend{CostUSD: cost, CostSource: "reported", Turns: int(cost * 10)}
	}
	removed := func(cost float64) *recovery.Spend { s := spend(cost); return &s }
	near := func(got recovery.Spend, want float64) bool {
		return got.CostUSD > want-0.0001 && got.CostUSD < want+0.0001
	}

	if got := carry(spend(0.30), spend(0.10), removed(0.50)); !near(got, 0.60) {
		t.Errorf("a journal of 0.30, a carry of 0.10 and a ledger of 0.50 carry %+v, want 0.60", got)
	}
	if got := carry(spend(0.90), spend(0.10), removed(0.20)); !near(got, 0.90) {
		t.Errorf("a journal of 0.90, a carry of 0.10 and a ledger of 0.20 carry %+v, want 0.90", got)
	}
	if got := carry(spend(0.70), spend(0.10), nil); !near(got, 0.70) {
		t.Errorf("a ledger that does not read carries %+v, want what the journal records, 0.70", got)
	}
	// The spend that is carried is whole: the tokens and the turns go with the cost.
	got := carry(recovery.Spend{CostUSD: 0.30, InputTokens: 5}, recovery.Spend{CostUSD: 0.10, InputTokens: 1, OutputTokens: 2, Turns: 3}, &recovery.Spend{CostUSD: 0.50, InputTokens: 7, OutputTokens: 11, Turns: 13})
	if !near(got, 0.60) || got.InputTokens != 8 || got.OutputTokens != 13 || got.Turns != 16 {
		t.Errorf("carry = %+v, want the sum of the carried spend and the ledger's, tokens and turns included", got)
	}
}

// TestAResumedFixThatRunsTheBrainAgainStartsFromACleanCloneAtTheBaseHead pins what the clone is like
// when the loop begins on a resumed delivery: on the base at the commit the run began at, with the change
// of the earlier delivery gone, the files it left untracked gone, the branches of the run deleted, and the
// run directory kept.
// Verifies: 14-REQ-3.3
func TestAResumedFixThatRunsTheBrainAgainStartsFromACleanCloneAtTheBaseHead(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	e.stopAfter("briefed") // no submission yet: the brain runs again
	clone := filepath.Join(e.dir, cloneDirName)
	runID := e.hub.Sessions()[0].RunID
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", clone, "-c", "user.name=x", "-c", "user.email=x@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	head := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(clone, "stray.txt"), []byte("left by the first delivery"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, "src", "greet.sh"), []byte("echo half a change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("branch", "fix/stray-of-the-run", "HEAD")
	git("commit", "--allow-empty", "-q", "-m", "a commit on a branch of the run\n\nAgent-Fox-Run: "+runID)
	git("branch", "fix/the-run", "HEAD")
	git("reset", "-q", "--hard", head)
	git("branch", "-D", "fix/stray-of-the-run")
	git("branch", "fix/somebody-elses", head)

	var status, branches, current string
	var ledgerErr error
	flags := e.runner.loopFlags
	e.runner.loopFlags = func(a *attempt) []string {
		_, ledgerErr = os.Stat(filepath.Join(clone, ".agent-fox", "run", runID, "ledger.jsonl"))
		status = git("status", "--porcelain", "--ignored=no")
		branches = git("for-each-ref", "--format=%(refname:short)", "refs/heads")
		current = git("rev-parse", "HEAD")
		return flags(a)
	}

	if _, err := e.deliver(2); err != nil {
		t.Fatal(err)
	}

	if status != "" || current != head {
		t.Errorf("when the loop began the clone was at %.8s (want %.8s) with the status %q, want it at the base head and clean", current, head, status)
	}
	if got := strings.Fields(branches); !slices.Equal(got, []string{"fix/somebody-elses", "main"}) {
		t.Errorf("the branches were %v, want the branch of the run gone and the one that is not the run's kept", got)
	}
	if ledgerErr != nil {
		t.Errorf("the run directory was gone when the loop began: %v", ledgerErr)
	}
}

// TestEqualCostsAreToldApartByTheirTurnsAndThenTheirTokens pins the order of two spends of one cost: a run
// of a brain that is not priced is still spent.
func TestEqualCostsAreToldApartByTheirTurnsAndThenTheirTokens(t *testing.T) {
	two, six := recovery.Spend{Turns: 2}, recovery.Spend{Turns: 6}
	if got := carry(two, recovery.Spend{}, &six); got.Turns != 6 {
		t.Errorf("a journal of 2 turns and a ledger of 6 carry %d turns, want 6", got.Turns)
	}
	if got := carry(six, recovery.Spend{}, &two); got.Turns != 6 {
		t.Errorf("a journal of 6 turns and a ledger of 2 carry %d turns, want 6", got.Turns)
	}
	few, many := recovery.Spend{Turns: 2, InputTokens: 10}, recovery.Spend{Turns: 2, OutputTokens: 400}
	if got := carry(few, recovery.Spend{}, &many); got.OutputTokens != 400 {
		t.Errorf("of two spends of equal cost and turns the one with more tokens is %+v", got)
	}
}
