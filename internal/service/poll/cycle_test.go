package poll

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

const (
	varsPath = "/api/v1/workspaces/" + slug + "/vars/resolved"
	listPath = "/api/v1/workspaces/" + slug + "/issues"
)

// recorder is a handler that notes what a cycle hands it and does nothing with it.
type recorder struct {
	mu       sync.Mutex
	listings []listing
}

func (r *recorder) handle(_ context.Context, _ *cycleRun, l *listing) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listings = append(r.listings, *l)
	return true
}

// seen are the issues handed over for the label, in the order they came, over all the listings.
func (r *recorder) seen(label string) []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []int{}
	for _, l := range r.listings {
		if l.Label == label {
			out = append(out, l.Issues...)
		}
	}
	return out
}

// reconcilerFunc is a reconciler that is a function.
type reconcilerFunc func(ctx context.Context, workspace string)

func (f reconcilerFunc) reconcile(ctx context.Context, workspace string) { f(ctx, workspace) }

// listsOf are the paths that the poll listed, in order, as "issues(<label>)".
func (e *env) listsOf() []string {
	var out []string
	for _, p := range e.fromPoll() {
		if strings.HasPrefix(p, "issues(") {
			out = append(out, p)
		}
	}
	return out
}

// TestUnsetPollIntervalPollsFiveMinutesApartAndAValueInRangeIsTheInterval_TS_15_9 verifies TS-15-9: with the variable
// unset the next cycle is due 5 minutes after the cycle, and every cycle lists; set to 2m it is 2 minutes; 24h is 24h
// and 1m is 1m.
// Verifies: 15-REQ-2.1, 15-REQ-2.3
func TestUnsetPollIntervalPollsFiveMinutesApartAndAValueInRangeIsTheInterval_TS_15_9(t *testing.T) {
	e := newEnv(t)
	for i := range 3 {
		if got := e.cycle(); got != 5*time.Minute {
			t.Errorf("cycle %d with the variable unset: next in %s, want 5m0s", i+1, got)
		}
	}
	if lists := e.listsOf(); len(lists) != 6 {
		t.Errorf("%d lists in 3 cycles, want the triage list and the fix list of each: %v", len(lists), lists)
	}
	for value, want := range map[string]time.Duration{"2m": 2 * time.Minute, "24h": 24 * time.Hour, "1m": time.Minute, "90m": 90 * time.Minute, "1h30m": 90 * time.Minute} {
		e.setVar("AF_POLL_INTERVAL", value)
		before := len(e.listsOf())
		if got := e.cycle(); got != want {
			t.Errorf("AF_POLL_INTERVAL %s: next in %s, want %s", value, got, want)
		}
		if n := len(e.listsOf()) - before; n != 2 {
			t.Errorf("AF_POLL_INTERVAL %s: %d lists, want 2", value, n)
		}
	}
	e.setVar("AF_POLL_INTERVAL", "")
	if got := e.cycle(); got != 5*time.Minute {
		t.Errorf("the variable unset again: next in %s, want 5m0s", got)
	}
	if warns := e.Logs.level("WARN"); len(warns) != 0 {
		t.Errorf("valid values logged %v", warns)
	}
}

// TestAZeroIntervalReadsTheVariableAndNothingElseEveryFiveMinutes_TS_15_10 verifies TS-15-10: with 0, 0s or 0m the cycle
// ends right after the variables are read, with no read of the workspace record, no list, no job and no label removed,
// and the next is due in 5 minutes; changing the variable to 1m makes the next cycle list.
// Verifies: 15-REQ-2.2
func TestAZeroIntervalReadsTheVariableAndNothingElseEveryFiveMinutes_TS_15_10(t *testing.T) {
	for _, zero := range []string{"0", "0s", "0m"} {
		t.Run(zero, func(t *testing.T) {
			e := newEnv(t, withVars(map[string]string{"AF_POLL_INTERVAL": zero}))
			e.Hub.AddIssue(slug, fakehub.Issue{Number: 7, Labels: []string{"af:triage"}})
			e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
			var total time.Duration
			for range 6 { // 30 minutes of the clock, a cycle at each 5
				got := e.cycle()
				total += got
				e.Clock.Advance(got)
			}
			if total != 30*time.Minute {
				t.Errorf("the cycles were %s apart in all, want 30 minutes", total)
			}
			if got, want := e.fromPoll(), slices.Repeat([]string{"vars"}, 6); !slices.Equal(got, want) {
				t.Errorf("hub was asked %v, want only the variables, once in each cycle", got)
			}
			if jobs := e.jobList(); len(jobs) != 0 {
				t.Errorf("a job was made while the poll is off: %+v", jobs)
			}
			if got := e.cycleAfterEnabling("1m"); got != time.Minute {
				t.Errorf("after the variable was set to 1m the next cycle is due in %s, want 1m0s", got)
			}
			want := []string{"vars", "workspace", "issues(af:triage)", "issues(af:fix)"}
			if got := e.fromPoll(); len(got) != 6+len(want) || !slices.Equal(got[6:], want) {
				t.Errorf("hub was asked %v in all, want the cycle after the change to ask %v", got, want)
			}
		})
	}
}

// cycleAfterEnabling sets the interval and runs a cycle.
func (e *env) cycleAfterEnabling(value string) time.Duration {
	e.setVar("AF_POLL_INTERVAL", value)
	return e.cycle()
}

// TestAnInvalidPollIntervalFallsBackToFiveMinutesWithOneWarningPerDistinctValue_TS_15_11 verifies TS-15-11.
// Verifies: 15-REQ-2.4
func TestAnInvalidPollIntervalFallsBackToFiveMinutesWithOneWarningPerDistinctValue_TS_15_11(t *testing.T) {
	e := newEnv(t)
	for _, value := range []string{"30s", "abc", "-1m", "25h", "59s", "24h1s", "1x"} {
		e.setVar("AF_POLL_INTERVAL", value)
		before := len(e.listsOf())
		for i := range 3 {
			if got := e.cycle(); got != 5*time.Minute {
				t.Errorf("%q, cycle %d: next in %s, want 5m0s", value, i+1, got)
			}
		}
		if n := len(e.listsOf()) - before; n != 6 {
			t.Errorf("%q: %d lists in 3 cycles, want the poll to go on at 5 minutes", value, n)
		}
		var named []map[string]any
		for _, rec := range e.Logs.level("WARN") {
			if rec["value"] == value {
				named = append(named, rec)
			}
		}
		if len(named) != 1 || named[0]["workspace"] != slug || named[0]["variable"] != "AF_POLL_INTERVAL" {
			t.Errorf("%q: warnings %v, want exactly one that names the workspace, the variable and the value", value, named)
		}
	}
	if n := len(e.Logs.level("WARN")); n != 7 {
		t.Errorf("%d warnings in all, want one for each of the 7 distinct values", n)
	}
	// A value that was warned of is not warned of again when it comes back.
	e.setVar("AF_POLL_INTERVAL", "2m")
	e.cycle()
	e.setVar("AF_POLL_INTERVAL", "abc")
	e.cycle()
	if n := len(e.Logs.level("WARN")); n != 7 {
		t.Errorf("a value that came back was warned of again: %d warnings", n)
	}
}

// TestACycleThatEndsBeforeItReadTheVariableWaitsTheLastIntervalReadOrFiveMinutes_TS_15_12 verifies TS-15-12.
// Verifies: 15-REQ-2.5
func TestACycleThatEndsBeforeItReadTheVariableWaitsTheLastIntervalReadOrFiveMinutes_TS_15_12(t *testing.T) {
	for name, tc := range map[string]struct {
		first string // the value read by the first cycle, "" for none
		want  time.Duration
	}{"after 2m was read": {"2m", 2 * time.Minute}, "after 0 was read": {"0", 5 * time.Minute}, "after 24h was read": {"24h", 24 * time.Hour}, "when none was read": {"", 5 * time.Minute}} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			if tc.first != "" {
				e.setVar("AF_POLL_INTERVAL", tc.first)
				e.cycle()
			}
			e.Hub.FailOnce(varsPath, 503)
			if got := e.cycle(); got != tc.want {
				t.Errorf("the cycle whose variables read failed: next in %s, want %s", got, tc.want)
			}
			if lists := e.listsOf(); tc.first != "0" && tc.first != "" && len(lists) != 2 {
				t.Errorf("lists %v: the failed cycle listed", lists)
			}
		})
	}
	t.Run("the interval of a cycle that read it is its own", func(t *testing.T) {
		e := newEnv(t, withVars(map[string]string{"AF_POLL_INTERVAL": "3m"}))
		e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 503, Once: true})
		if got := e.cycle(); got != 3*time.Minute {
			t.Errorf("the cycle whose list failed after the variable was read: next in %s, want 3m0s", got)
		}
	})
}

// TestTheRequestLabelsComeFromTheSubscriptionPrefixReadAtTheStartOfEachCycle_TS_15_13 verifies TS-15-13.
// Verifies: 15-REQ-2.6
func TestTheRequestLabelsComeFromTheSubscriptionPrefixReadAtTheStartOfEachCycle_TS_15_13(t *testing.T) {
	e := newEnv(t, withVars(map[string]string{"AF_LABEL_PREFIX": "other:"}))
	e.cycle()
	if got, want := e.listsOf(), []string{"issues(af:triage)", "issues(af:fix)"}; !slices.Equal(got, want) {
		t.Errorf("the first cycle listed %v, want %v", got, want)
	}
	if _, _, err := e.Store.Upsert(ctx, slug, "ci:", pat, "usr_1", "usr_1", epoch); err != nil {
		t.Fatal(err)
	}
	e.cycle()
	if got, want := e.listsOf()[2:], []string{"issues(ci:triage)", "issues(ci:fix)"}; !slices.Equal(got, want) {
		t.Errorf("the second cycle listed %v, want %v", got, want)
	}
	for _, p := range e.fromPoll() {
		if strings.Contains(p, "other") {
			t.Errorf("the poll asked for %s, which names the prefix of the variable", p)
		}
	}
}

// TestADifferingLabelPrefixVariableLogsOneWarningPerDistinctValueAndIsIgnored_TS_15_14 verifies TS-15-14.
// Verifies: 15-REQ-2.7
func TestADifferingLabelPrefixVariableLogsOneWarningPerDistinctValueAndIsIgnored_TS_15_14(t *testing.T) {
	e := newEnv(t)
	run := func(value string, n int) {
		e.setVar("AF_LABEL_PREFIX", value)
		for range n {
			e.cycle()
		}
	}
	run("x:", 3)
	run("y:", 2)
	run("af:", 1)
	warned := map[any]int{}
	for _, rec := range e.Logs.level("WARN") {
		if rec["variable"] != "AF_LABEL_PREFIX" || rec["workspace"] != slug {
			t.Errorf("a warning that is not about the prefix: %v", rec)
		}
		warned[rec["value"]]++
	}
	if len(warned) != 2 || warned["x:"] != 1 || warned["y:"] != 1 {
		t.Errorf("warnings by value %v, want exactly one for x: and one for y:, and none for af:", warned)
	}
	for _, l := range e.listsOf() {
		if l != "issues(af:triage)" && l != "issues(af:fix)" {
			t.Errorf("the poll listed %s, want the labels of the subscription's prefix", l)
		}
	}
}

// TestOneEnabledCycleReadsTheVariablesTheWorkspaceRecordAndListsTriageThenFixWithALimitOf100_TS_15_22 verifies TS-15-22:
// the order of the requests, the limit of each list, and that the poll's records were reconciled before the first
// request of hub.
// Verifies: 15-REQ-4.1
func TestOneEnabledCycleReadsTheVariablesTheWorkspaceRecordAndListsTriageThenFixWithALimitOf100_TS_15_22(t *testing.T) {
	e := newEnv(t)
	var reconciled []int
	e.Poll.reconciler = reconcilerFunc(func(_ context.Context, workspace string) {
		reconciled = append(reconciled, e.Hub.Total())
		if workspace != slug {
			t.Errorf("the records of %s were reconciled", workspace)
		}
	})

	e.cycle()

	if want := []string{"vars", "workspace", "issues(af:triage)", "issues(af:fix)"}; !slices.Equal(e.fromPoll(), want) {
		t.Errorf("hub was asked %v, want %v", e.fromPoll(), want)
	}
	if !slices.Equal(reconciled, []int{0}) {
		t.Errorf("the records were reconciled when hub had had %v requests, want once and before the first", reconciled)
	}
	for _, r := range e.Hub.ListRequests(slug) {
		if !strings.Contains(r.URI, "state=open") || !strings.Contains(r.URI, "limit=100") {
			t.Errorf("the list %s is not for the open issues with a limit of 100", r.URI)
		}
	}
}

// TestThreeIncompleteListingsInARowProduceOneWarningThatNamesTheWorkspaceAndTheLabel_TS_15_24 verifies TS-15-24.
// Verifies: 15-REQ-4.3
func TestThreeIncompleteListingsInARowProduceOneWarningThatNamesTheWorkspaceAndTheLabel_TS_15_24(t *testing.T) {
	e := newEnv(t)
	for n := 1; n <= 101; n++ {
		e.Hub.AddIssue(slug, fakehub.Issue{Number: n, Labels: []string{"af:fix"}})
	}
	warnings := func() []map[string]any {
		var out []map[string]any
		for _, rec := range e.Logs.level("WARN") {
			if rec["label"] == "af:fix" && rec["workspace"] == slug {
				out = append(out, rec)
			}
		}
		return out
	}
	for range 3 {
		e.cycle()
	}
	if got := warnings(); len(got) != 1 {
		t.Fatalf("%d warnings for af:fix after 3 incomplete listings, want exactly one: %v", len(got), got)
	}
	for _, rec := range e.Logs.level("WARN") {
		if rec["label"] == "af:triage" {
			t.Errorf("a warning for the label that listed complete: %v", rec)
		}
	}

	e.Hub.RemoveLabel(slug, 100, "af:fix")
	e.Hub.RemoveLabel(slug, 101, "af:fix")
	e.cycle() // a complete listing of 99
	if got := warnings(); len(got) != 1 {
		t.Errorf("%d warnings after a complete listing, want still the one", len(got))
	}
	e.Hub.AddLabel(slug, 100, "af:fix")
	e.Hub.AddLabel(slug, 101, "af:fix")
	e.cycle()
	if got := warnings(); len(got) != 2 {
		t.Errorf("%d warnings after a complete listing and an incomplete one, want 2: the warning comes again once a listing has come back complete", len(got))
	}
}

// TestAPullRequestAClosedIssueAndAnIssueWithoutARequestLabelNeverReachTheRulesOfTheCycle_TS_15_26 verifies TS-15-26:
// what the cycle hands over for a label is the open issues that carry it, and it sends no removal and makes no job.
// Verifies: 15-REQ-4.5
func TestAPullRequestAClosedIssueAndAnIssueWithoutARequestLabelNeverReachTheRulesOfTheCycle_TS_15_26(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}
	e.Poll.handler = rec
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 3, Title: "a pull request", Labels: []string{"af:fix"}, IsPR: true})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 4, Title: "a closed issue", Labels: []string{"af:triage"}, State: "closed"})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 5, Title: "a bug", Labels: []string{"bug"}})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 6, Title: "an issue to fix", Labels: []string{"af:fix", "bug"}})

	e.cycle()

	if got := rec.seen("af:fix"); !slices.Equal(got, []int{6}) {
		t.Errorf("the issues handed over for af:fix are %v, want only the open issue 6", got)
	}
	if got := rec.seen("af:triage"); len(got) != 0 {
		t.Errorf("the issues handed over for af:triage are %v, want none", got)
	}
	for _, p := range e.fromPoll() {
		if strings.HasPrefix(p, "remove(") {
			t.Errorf("the poll sent %s", p)
		}
	}
	if jobs := e.jobList(); len(jobs) != 0 {
		t.Errorf("jobs %+v were made", jobs)
	}
	for n, label := range map[int]string{3: "af:fix", 4: "af:triage", 5: "bug"} {
		if issue, _ := e.Hub.IssueOf(slug, n); !slices.Contains(issue.Labels, label) {
			t.Errorf("issue %d lost %s: %v", n, label, issue.Labels)
		}
	}
}

// ascending says whether the issues of every listing handed over come in ascending number, as the cycle takes them
// whatever order hub returned them in.
func TestTheCycleHandsOverTheIssuesOfAListingInAscendingNumberWhateverOrderHubReturned(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}
	e.Poll.handler = rec
	for _, n := range []int{42, 7, 19} {
		e.Hub.AddIssue(slug, fakehub.Issue{Number: n, Labels: []string{"af:fix"}})
	}
	e.Hub.SetListOrder(slug, 42, 19, 7)

	e.cycle()

	if got := rec.seen("af:fix"); !slices.Equal(got, []int{7, 19, 42}) {
		t.Errorf("the issues handed over are %v, want 7, 19, 42", got)
	}
	var kinds []string
	for _, l := range rec.listings {
		kinds = append(kinds, fmt.Sprint(l.Kind, ":", l.Label, ":", l.Complete))
	}
	if want := []string{"triage:af:triage:true", "fix:af:fix:true"}; !slices.Equal(kinds, want) {
		t.Errorf("the listings are %v, want the triage list and then the fix list, both complete", kinds)
	}
}
