package poll

import (
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

const workspacePath = "/api/v1/workspaces/" + slug

// failuresOf are the records of the level whose message contains the text, of the workspace.
func (e *env) failuresOf(level, text string) []map[string]any {
	var out []map[string]any
	for _, rec := range e.Logs.find(level, text) {
		if rec["workspace"] == slug {
			out = append(out, rec)
		}
	}
	return out
}

// msgOf is the message of a record.
func msgOf(rec map[string]any) string { s, _ := rec["msg"].(string); return s }

// TestANotSubscribedAnswerOfTheStoreStopsTheLoop_TS_15_52 verifies TS-15-52: when the subscription is gone from the store
// and the next cycle reads the prefix and the token, the cycle says to stop the loop and asks hub nothing, and a loop that
// waits for that cycle ends.
// Verifies: 15-REQ-8.1
func TestANotSubscribedAnswerOfTheStoreStopsTheLoop_TS_15_52(t *testing.T) {
	t.Run("the cycle", func(t *testing.T) {
		e := newEnv(t)
		if _, err := e.Store.Delete(ctx, slug); err != nil {
			t.Fatal(err)
		}
		if _, stop := e.Poll.Cycle(ctx, slug); !stop {
			t.Error("the cycle did not say to stop the loop")
		}
		if n := e.hits(pat); n != 0 {
			t.Errorf("hub had %d requests for a workspace that is not subscribed", n)
		}
	})

	t.Run("the loop", func(t *testing.T) {
		// The scan would end the loop too, so it is put off, and only the cycle can.
		e := newEnv(t, withoutStream(), withPoll(func(c *Config) { c.ScanPeriod = 1000 * time.Hour }))
		e.started()
		waitFor(t, "the first cycle", func() bool { return len(e.Hub.ListRequests(slug)) == 2 })
		// The first delay, the cut-off of the cycle and the wait for the next cycle are the three waits of the loop.
		if !e.Clock.WaitForWaits(3, 10*time.Second) {
			t.Fatal("the loop did not wait for its next cycle")
		}
		if _, err := e.Store.Delete(ctx, slug); err != nil {
			t.Fatal(err)
		}
		before := e.hits(pat)

		e.Clock.Advance(5 * time.Minute)

		waitFor(t, "the loop to stop", func() bool { return e.Poll.loopCount(slug) == 0 })
		e.Clock.Advance(time.Hour)
		settle()
		if e.hits(pat) != before {
			t.Errorf("hub had %d requests after the loop stopped", e.hits(pat)-before)
		}
	})
}

// TestAnUnreadableTokenMakesNoRequestOfHubAndLogsOneError_TS_15_53 verifies TS-15-53.
// Verifies: 15-REQ-8.2
func TestAnUnreadableTokenMakesNoRequestOfHubAndLogsOneError_TS_15_53(t *testing.T) {
	e := newEnv(t)
	if _, err := e.DB.ExecContext(ctx, "UPDATE af_subscriptions SET sealed_token = x'00' WHERE slug = ?", slug); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		if _, stop := e.Poll.Cycle(ctx, slug); stop {
			t.Fatal("the loop was stopped")
		}
		e.Clock.Advance(5 * time.Minute)
	}

	if n := e.Hub.Total(); n != 0 {
		t.Errorf("hub had %d requests, want none", n)
	}
	if errs := e.Logs.level("ERROR"); len(errs) != 1 || errs[0]["workspace"] != slug {
		t.Errorf("%d ERROR records: %v, want exactly one that names the workspace", len(errs), errs)
	}
}

// TestAHub503OnTheListCreatesNothingAndTheNextCycleOneIntervalLaterCreatesTheJob_TS_15_54 verifies TS-15-54 and the first
// part of goal 6.
// Verifies: 15-REQ-8.3
func TestAHub503OnTheListCreatesNothingAndTheNextCycleOneIntervalLaterCreatesTheJob_TS_15_54(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{"AF_POLL_INTERVAL": "3m"}))
	e.issue(42, "af:fix")
	e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 503, Once: true})

	next := e.cycle()

	if next != 3*time.Minute {
		t.Errorf("the next cycle is due in %s, want one interval: 3m0s", next)
	}
	if len(e.jobList()) != 0 || len(e.removals()) != 0 {
		t.Errorf("jobs %v and removals %v after the failing call, want none", e.jobList(), e.removals())
	}
	if warns := e.Logs.level("WARN"); len(warns) != 1 {
		t.Errorf("%d warnings, want exactly one: %v", len(warns), warns)
	}
	if errs := e.Logs.level("ERROR"); len(errs) != 0 {
		t.Errorf("a 503 logged errors: %v", errs)
	}
	e.Clock.Advance(next)
	e.cycle()
	if len(e.jobList()) != 1 {
		t.Errorf("%d jobs after the next cycle, want the job", len(e.jobList()))
	}
}

// TestA403OnTheListGivesOneErrorAcrossThreeCyclesThatNamesTheOperationAndThePutRoute_TS_15_55 verifies TS-15-55 and the
// second part of goal 6: a second workspace is polled in the same cycles.
// Verifies: 15-REQ-8.4
func TestA403OnTheListGivesOneErrorAcrossThreeCyclesThatNamesTheOperationAndThePutRoute_TS_15_55(t *testing.T) {
	t.Run("403 on the list", func(t *testing.T) {
		e := newEnv(t)
		e.issue(42, "af:fix")
		e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 403})
		for range 3 {
			if _, stop := e.Poll.Cycle(ctx, slug); stop {
				t.Fatal("the loop was stopped")
			}
		}
		errs := e.failuresOf("ERROR", "")
		if len(errs) != 1 {
			t.Fatalf("%d ERROR records across three cycles, want exactly one: %v", len(errs), errs)
		}
		if msg := msgOf(errs[0]); !strings.Contains(msg, "PUT /api/v1/workspaces/widgets") || !strings.Contains(msg, "list the issues of af:triage") {
			t.Errorf("the record says %q, want the operation and the route that replaces the token", msg)
		}
		if len(e.jobList()) != 0 || len(e.removals()) != 0 {
			t.Error("a job was made or a label removed after the list failed")
		}
	})

	for name, tc := range map[string]struct {
		path   string
		status int
		route  bool
		op     string
	}{
		"401 on the variables":        {workspacePath + "/vars/resolved", 401, true, "read the resolved variables"},
		"403 on the workspace record": {workspacePath, 403, true, "read the workspace record"},
		"404 on the workspace record": {workspacePath, 404, false, "read the workspace record"},
		"404 on the variables":        {workspacePath + "/vars/resolved", 404, false, "read the resolved variables"},
		"404 on the list":             {listPath, 404, false, "list the issues"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Hub.Inject("GET", tc.path, fakehub.Fault{Status: tc.status})
			for range 2 {
				e.cycle()
			}
			errs := e.failuresOf("ERROR", "")
			if len(errs) != 1 {
				t.Fatalf("%d ERROR records, want one: %v", len(errs), errs)
			}
			msg := msgOf(errs[0])
			if !strings.Contains(msg, tc.op) || strings.Contains(msg, "PUT /api/v1/workspaces/widgets") != tc.route {
				t.Errorf("the record says %q, want it to name %q and, for 401 and 403, the PUT route", msg, tc.op)
			}
		})
	}
}

// TestARejectedReadOrListEndsTheCycleWithOneErrorThatCarriesHubsMessage_TS_15_56 verifies TS-15-56.
// Verifies: 15-REQ-8.5
func TestARejectedReadOrListEndsTheCycleWithOneErrorThatCarriesHubsMessage_TS_15_56(t *testing.T) {
	body := `{"error":{"code":409,"message":"conflict text of hub","error_type":"forge_conflict"}}`
	for name, tc := range map[string]struct{ path, op string }{
		"the variables read":        {workspacePath + "/vars/resolved", "read the resolved variables"},
		"the workspace record read": {workspacePath, "read the workspace record"},
		"the list":                  {listPath, "list the issues"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			e.Hub.Inject("GET", tc.path, fakehub.Fault{Status: 409, Body: body})

			e.cycle()
			e.cycle()

			errs := e.failuresOf("ERROR", "")
			if len(errs) != 1 {
				t.Fatalf("%d ERROR records, want one: %v", len(errs), errs)
			}
			if msg := msgOf(errs[0]); !strings.Contains(msg, "conflict text of hub") || !strings.Contains(msg, tc.op) {
				t.Errorf("the record says %q, want the operation and hub's message", msg)
			}
			if len(e.jobList()) != 0 {
				t.Error("a job was made")
			}
		})
	}
}

// TestA409OnALabelRemovalLeavesItOwedLogsOneErrorAndGoesOnToTheNextIssue_TS_15_57 verifies TS-15-57 and the fourth part of
// goal 6.
// Verifies: 15-REQ-8.6
func TestA409OnALabelRemovalLeavesItOwedLogsOneErrorAndGoesOnToTheNextIssue_TS_15_57(t *testing.T) {
	e := newEnv(t)
	e.issue(42, "af:fix")
	e.issue(43, "af:fix")
	e.Hub.Inject("DELETE", removalPath(42, "af:fix"), fakehub.Fault{Status: 409, Once: true,
		Body: `{"error":{"code":409,"message":"the label is locked by hub","error_type":"forge_conflict"}}`})

	e.cycle()

	if n := len(e.jobList()); n != 2 {
		t.Errorf("%d jobs, want one for 42 and one for 43: the job for 42 stands and issue 43 is still processed", n)
	}
	errs := e.failuresOf("ERROR", "")
	if len(errs) != 1 {
		t.Fatalf("%d ERROR records, want exactly one: %v", len(errs), errs)
	}
	msg := msgOf(errs[0])
	for _, want := range []string{"af:fix", "42", "the label is locked by hub"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the record says %q, want it to contain %q", msg, want)
		}
	}
	if len(e.labelsOf(43)) != 0 || len(e.labelsOf(42)) == 0 {
		t.Errorf("labels of 42: %v, of 43: %v; want the label of 42 left and that of 43 removed", e.labelsOf(42), e.labelsOf(43))
	}

	e.cycle() // hub accepts now
	if n := len(e.jobList()); n != 2 || len(e.labelsOf(42)) != 0 {
		t.Errorf("after the next cycle: %d jobs and labels %v on 42, want the label removed and no job made", n, e.labelsOf(42))
	}
}

// TestAFailureIsLoggedOnceUntilRecoveryADifferentOneIsLoggedAndRecoveryLogsOneInfo_TS_15_58 verifies TS-15-58: a 503 list
// in one cycle and a 403 variables read in the next give one WARN and one ERROR; the 403 again logs nothing; a cycle with
// no failure logs one INFO record that the poll has recovered; and after it the same failure is logged again.
// Verifies: 15-REQ-8.7
func TestAFailureIsLoggedOnceUntilRecoveryADifferentOneIsLoggedAndRecoveryLogsOneInfo_TS_15_58(t *testing.T) {
	e := newEnv(t)
	recovered := func() int { return len(e.failuresOf("INFO", "recovered")) }

	e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 503, Once: true})
	e.cycle()
	e.Hub.Inject("GET", workspacePath+"/vars/resolved", fakehub.Fault{Status: 403})
	e.cycle()
	if w, er := len(e.failuresOf("WARN", "")), len(e.failuresOf("ERROR", "")); w != 1 || er != 1 {
		t.Fatalf("%d WARN and %d ERROR records, want one of each", w, er)
	}
	e.cycle()
	e.cycle()
	if w, er := len(e.failuresOf("WARN", "")), len(e.failuresOf("ERROR", "")); w != 1 || er != 1 {
		t.Errorf("the 403 again: %d WARN and %d ERROR records, want it not logged again", w, er)
	}
	if recovered() != 0 {
		t.Error("recovery was logged while the cycles still failed")
	}

	e.Hub.Clear()
	e.cycle()
	if recovered() != 1 {
		t.Errorf("%d records of recovery after a cycle with no failure, want one", recovered())
	}
	e.cycle()
	if recovered() != 1 {
		t.Errorf("%d records of recovery after another cycle with no failure, want still one", recovered())
	}

	e.Hub.Inject("GET", workspacePath+"/vars/resolved", fakehub.Fault{Status: 403})
	e.cycle()
	if er := len(e.failuresOf("ERROR", "")); er != 2 {
		t.Errorf("%d ERROR records after recovery and the same failure again, want it logged again: 2", er)
	}
}

// TestAFailingWorkspaceDoesNotStopAnotherWorkspacesLoop_TS_15_59 verifies TS-15-59.
// Verifies: 15-REQ-8.8
func TestAFailingWorkspaceDoesNotStopAnotherWorkspacesLoop_TS_15_59(t *testing.T) {
	const gadgetsPAT = "pat-GADGETS-0123456789"
	e := newEnv(t)
	e.addWorkspace("gadgets", gadgetsPAT)
	e.Hub.AddIssue("gadgets", fakehub.Issue{Number: 5, Labels: []string{"af:fix"}})
	e.Hub.Inject("", workspacePath+"/*", fakehub.Fault{Status: 403})
	e.Hub.Inject("", workspacePath, fakehub.Fault{Status: 403})

	for range 3 {
		if _, stop := e.Poll.Cycle(ctx, slug); stop {
			t.Fatal("the loop of the failing workspace was stopped")
		}
		if _, stop := e.Poll.Cycle(ctx, "gadgets"); stop {
			t.Fatal("the loop of the other workspace was stopped")
		}
	}

	if n := len(e.Hub.ListRequests("gadgets")); n != 6 {
		t.Errorf("gadgets was listed %d times in three cycles, want 6: the triage list and the fix list of each", n)
	}
	var made int
	if err := e.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM af_jobs WHERE workspace = 'gadgets'").Scan(&made); err != nil || made != 1 {
		t.Errorf("gadgets has %d jobs (%v), want its one", made, err)
	}
	if errs := e.failuresOf("ERROR", ""); len(errs) != 1 {
		t.Errorf("%d ERROR records for widgets, want one", len(errs))
	}
	if errs := e.Logs.level("ERROR"); len(errs) != 1 {
		t.Errorf("%d ERROR records in all, want the one of widgets", len(errs))
	}
}
