package poll

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// recorder is a handler that notes the listings a cycle hands it and does nothing with them.
type recorder struct{ listings []listing }

func (r *recorder) handle(_ context.Context, _ *cycleRun, l *listing) bool {
	r.listings = append(r.listings, *l)
	return true
}

type reconcilerFunc func(ctx context.Context, workspace string)

func (f reconcilerFunc) reconcile(ctx context.Context, workspace string) { f(ctx, workspace) }

func TestCycle_ReconcilesThenReadsTheVariablesAndTheRecordAndListsTriageThenFixInAscendingOrder(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}
	var hubAtReconcile []int
	e.Poll.handler = rec
	e.Poll.reconciler = reconcilerFunc(func(context.Context, string) { hubAtReconcile = append(hubAtReconcile, e.Hub.Total()) })
	for _, n := range []int{42, 7, 19} {
		e.issue(n, "af:fix")
	}
	e.Hub.SetListOrder(slug, 42, 19, 7)

	e.cycle()

	if !slices.Equal(hubAtReconcile, []int{0}) {
		t.Errorf("the records were reconciled when hub had had %v requests, want once, before the first", hubAtReconcile)
	}
	if got, want := e.routes(), []string{"vars", "workspace", "issues(af:triage)", "issues(af:fix)"}; !slices.Equal(got, want) {
		t.Errorf("hub was asked %v, want %v", got, want)
	}
	if u := e.Hub.ListRequests(slug)[1].URI; !strings.Contains(u, "state=open") || !strings.Contains(u, "limit=100") {
		t.Errorf("the list %s is not of the open issues, 100 at most", u)
	}
	if l := rec.listings; len(l) != 2 || l[0].Kind != "triage" || l[1].Kind != "fix" || !l[1].Complete || !slices.Equal(l[1].Issues, []int{7, 19, 42}) {
		t.Errorf("the listings are %+v, want the triage one and then the complete fix one of 7, 19 and 42", l)
	}
}

func TestCycle_HandsOverOnlyTheOpenIssuesThatCarryTheLabelInAnyCase(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}
	e.Poll.handler = rec
	e.Hub.Inject("GET", listPath, fakehub.Fault{Status: 200, Body: `[
		{"number": 3, "state": "open", "labels": ["af:fix"], "is_pr": true},
		{"number": 4, "state": "closed", "labels": ["af:fix"]},
		{"number": 5, "state": "open", "labels": ["bug"]},
		{"number": 6, "state": "open"},
		{"number": 7, "state": "open", "labels": ["AF:Fix", "bug"]}]`})

	e.cycle()

	if l := rec.listings; len(l[0].Issues) != 0 || !slices.Equal(l[1].Issues, []int{7}) {
		t.Errorf("the listings are %+v, want none for af:triage and only 7 for af:fix", l)
	}
}

func TestCycle_TheNextCycleIsDueTheIntervalLastReadAfterIt(t *testing.T) {
	all := []string{"vars", "workspace", "issues(af:triage)", "issues(af:fix)"}
	for name, tc := range map[string]struct {
		value, fail string // AF_POLL_INTERVAL, and the path that fails in the second cycle
		want        time.Duration
		routes      []string // what the second cycle asks of hub
	}{
		"no value":                     {"", "", 5 * time.Minute, all},
		"3m":                           {"3m", "", 3 * time.Minute, all},
		"0s, which turns the poll off": {"0s", "", 5 * time.Minute, []string{"vars"}},
		"the variables fail after 2m":  {"2m", varsPath, 2 * time.Minute, []string{"vars"}},
		"the variables fail after 0":   {"0", varsPath, 5 * time.Minute, []string{"vars"}},
		"the record fails after 4m":    {"4m", wsPath, 4 * time.Minute, []string{"vars", "workspace"}},
		"the list fails after 3m":      {"3m", listPath, 3 * time.Minute, []string{"vars", "workspace", "issues(af:triage)"}},
	} {
		e := newEnv(t, withVars(map[string]string{intervalVar: tc.value}))
		e.cycle()
		before := len(e.routes())
		if tc.fail != "" {
			e.Hub.FailOnce(tc.fail, 503)
		}
		if next := e.cycle(); next != tc.want || !slices.Equal(e.routes()[before:], tc.routes) {
			t.Errorf("%s: next in %s after asking %v, want %s after %v", name, next, e.routes()[before:], tc.want, tc.routes)
		}
	}
}

func TestCycle_AnInvalidIntervalIsWarnedOfOncePerValue(t *testing.T) {
	e := newEnv(t)
	for _, value := range []string{"abc", "abc", "25h", "2m", "abc"} {
		e.Hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) { w.Vars = map[string]string{intervalVar: value} })
		e.cycle()
	}
	warns := e.Log.Find("WARN", "AF_POLL_INTERVAL")
	if len(warns) != 2 || warns[0]["value"] != "abc" || warns[1]["value"] != "25h" || warns[0]["workspace"] != slug {
		t.Errorf("the warnings are %v, want one for abc and one for 25h", warns)
	}
}

func TestCycle_WarnsOnceOfAnIncompleteListingUntilOneIsCompleteAgain(t *testing.T) {
	e := newEnv(t)
	rec := &recorder{}
	e.Poll.handler = rec
	for n := 1; n <= 101; n++ {
		e.issue(n, "af:fix")
	}
	warnings := func() int { return len(e.Log.Find("WARN", "incomplete")) }
	for range 3 {
		e.cycle()
	}
	if warnings() != 1 || rec.listings[1].Complete || len(rec.listings[1].Issues) != 100 {
		t.Fatalf("%d warnings after 3 incomplete listings, the last %d issues complete %v; want 1 and 100 issues, incomplete",
			warnings(), len(rec.listings[1].Issues), rec.listings[1].Complete)
	}
	e.Hub.RemoveLabel(slug, 100, "af:fix")
	e.Hub.RemoveLabel(slug, 101, "af:fix")
	e.cycle()
	e.Hub.AddLabel(slug, 100, "af:fix")
	e.Hub.AddLabel(slug, 101, "af:fix")
	e.cycle()
	if warnings() != 2 {
		t.Errorf("%d warnings, want a second once a listing was complete in between", warnings())
	}
}

func TestCycle_AWorkspaceThatIsNotSubscribedStopsItsLoopWithNoRequestOfHub(t *testing.T) {
	e := newEnv(t)
	if _, err := e.Store.Delete(ctx, slug); err != nil {
		t.Fatal(err)
	}
	if _, stop := e.Poll.Cycle(ctx, slug); !stop || e.Hub.Total() != 0 {
		t.Errorf("stop %v with %d requests of hub, want the loop stopped and none", stop, e.Hub.Total())
	}
	if len(e.Log.Find("INFO", "not subscribed")) != 1 {
		t.Errorf("logs %s, want the stop said", e.Log)
	}
}

func TestCycle_ATokenTheStoreCannotGiveMakesNoRequestOfHubAndOneError(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"it does not open": {store.ErrUnreadable, "does not open with the key"},
		"the store fails":  {errors.New("disk I/O error"), "could not read the token of the subscription"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Subs.fail("Token", tc.err)
			for range 3 {
				if next := e.cycle(); next != 5*time.Minute {
					t.Errorf("next in %s, want 5m", next)
				}
			}
			if errs := e.Log.Find("ERROR", ""); e.Hub.Total() != 0 || len(errs) != 1 || !strings.Contains(errs[0]["msg"].(string), tc.want) {
				t.Errorf("%d requests of hub and the errors %v, want none and one that says %q", e.Hub.Total(), errs, tc.want)
			}
		})
	}
}

func TestCycle_EndsBeforeAListWhenTheForgeOrTheRecordsCannotBeRead(t *testing.T) {
	for name, tc := range map[string]struct {
		opt   option
		level string
		want  string
	}{
		"the forge URL is none of a repository": {func(e *env, _ *Config) {
			e.Hub.UpdateWorkspace(slug, func(w *fakehub.Workspace) { w.ForgeURL = "not a repository" })
		}, "WARN", "read the forge repository"},
		"the records cannot be read": {func(e *env, c *Config) { c.DB = testutil.OpenDB(e.t) }, "ERROR", "read the records of the poll"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, tc.opt)
			e.cycle()
			if got := e.routes(); !slices.Equal(got, []string{"vars", "workspace"}) {
				t.Errorf("hub was asked %v, want no list", got)
			}
			if len(e.Log.Find(tc.level, tc.want)) != 1 {
				t.Errorf("logs %s, want one %s that the cycle could not %s", e.Log, tc.level, tc.want)
			}
		})
	}
}
