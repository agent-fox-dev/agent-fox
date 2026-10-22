package jobs_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// listed is a page of a list, as the routes answer it.
type listed struct {
	Jobs       []jobs.Doc `json:"jobs"`
	NextCursor *string    `json:"next_cursor"`
}

// page decodes the body of a list that was answered 200, and checks its shape: a jobs
// array (never null), a next_cursor that is there even when it is null, and entries that
// carry no attempts.
func (r response) page(t *testing.T) listed {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("status %d, want 200\n%s", r.Status, r.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(r.Body, &raw); err != nil {
		t.Fatalf("the body is not a JSON object: %v\n%s", err, r.Body)
	}
	if len(raw) != 2 || string(raw["jobs"]) == "" || string(raw["jobs"]) == "null" || raw["next_cursor"] == nil {
		t.Fatalf("the body has the keys %v, want jobs (an array) and next_cursor (a string or null)\n%s", keysOf(raw), r.Body)
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(raw["jobs"], &entries); err != nil {
		t.Fatalf("jobs is not an array of objects: %v", err)
	}
	for _, entry := range entries {
		if _, has := entry["attempts"]; has {
			t.Errorf("an entry of a list has attempts: %.80s", raw["jobs"])
		}
	}
	var out listed
	if err := json.Unmarshal(r.Body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ids are the ids of the jobs of the page, in order.
func (p listed) ids() []string {
	out := make([]string, 0, len(p.Jobs))
	for _, j := range p.Jobs {
		out = append(out, j.ID)
	}
	return out
}

// slugs are the workspaces of the jobs of the page, once each and sorted.
func (p listed) slugs() []string {
	set := map[string]bool{}
	for _, j := range p.Jobs {
		set[j.Workspace] = true
	}
	return keysOf(set)
}

// createDirect makes a job of the kind for the issue through the Go entry point, which does
// not ask hub who the caller is: it is how a job gets onto a workspace that the caller cannot
// see.
func (e *env) createDirect(workspace string, kind jobs.Kind, issue int) jobs.Doc {
	e.t.Helper()
	doc, err := e.Svc.Create(ctx, jobs.CreateRequest{
		Workspace: workspace, Kind: kind, Body: jobs.Body{Issue: json.RawMessage(fmt.Sprint(issue))},
		Source: jobs.Source{Kind: "api", By: callerID},
	})
	if err != nil {
		e.t.Fatalf("create %s %s %d: %v", workspace, kind, issue, err)
	}
	return doc
}

// TestListingJobsShowsEveryWorkspaceTheCallerSeesSubscribedOrNotAndNoOtherTS_12_57
// verifies TS-12-57: GET /api/v1/jobs answers 200 with {jobs, next_cursor}; the jobs of
// widgets and of gadgets, which was unsubscribed since, are in it, and the job of secret,
// which hub does not show the caller, is not.
// Verifies: 12-REQ-9.1
func TestListingJobsShowsEveryWorkspaceTheCallerSeesSubscribedOrNotAndNoOther_TS_12_57(t *testing.T) {
	e := newEnv(t)
	e.addWorkspace("gadgets")
	e.subscribe("secret") // subscribed, and the caller is not shown it
	widgets := e.create("widgets", jobs.KindFix, 1)
	gadgets := e.create("gadgets", jobs.KindFix, 2)
	secret := e.createDirect("secret", jobs.KindFix, 3)
	if r := e.call("DELETE", "/api/v1/workspaces/gadgets", nil); r.Status != 204 {
		t.Fatalf("unsubscribe gadgets: %d %s", r.Status, r.Body)
	}

	got := e.call("GET", "/api/v1/jobs", nil).page(t)
	if want := []string{"gadgets", "widgets"}; !slices.Equal(got.slugs(), want) {
		t.Errorf("workspaces in the list = %v, want %v", got.slugs(), want)
	}
	ids := got.ids()
	if !slices.Contains(ids, widgets.ID) || !slices.Contains(ids, gadgets.ID) || slices.Contains(ids, secret.ID) || len(ids) != 2 {
		t.Errorf("ids = %v, want %s and %s and not %s", ids, widgets.ID, gadgets.ID, secret.ID)
	}
	if got.NextCursor != nil {
		t.Errorf("next_cursor = %q, want null on the only page", *got.NextCursor)
	}
	for _, j := range got.Jobs {
		if j.Workspace == "gadgets" && j.State != jobs.StateCancelled {
			t.Errorf("the job of the unsubscribed workspace is %s, want the cancelled job it was left as", j.State)
		}
	}

	// A caller who sees nothing is shown nothing, and the answer is still a list.
	e.Hub.AddUser("other-token", "user-2", nil)
	if nothing := e.callAs("other-token", "GET", "/api/v1/jobs", nil).page(t); len(nothing.Jobs) != 0 || nothing.NextCursor != nil {
		t.Errorf("a caller who sees no workspace got %+v, want an empty list", nothing)
	}
	// Who is calling is the first question.
	if r := e.callAs("", "GET", "/api/v1/jobs", nil); r.Status != 401 {
		t.Errorf("without a token: %d, want 401", r.Status)
	}
}

// TestListingJobsAnswers502WithNoPartialPageWhenHubFailsForAWorkspaceItNeeds_TS_12_58
// verifies TS-12-58: with hub failing for gadgets alone, and nothing known of the caller's
// view of it, GET /api/v1/jobs is 502 hub_unavailable and has no jobs array; once hub
// answers, the list is whole.
// Verifies: 12-REQ-9.2
func TestListingJobsAnswers502WithNoPartialPageWhenHubFailsForAWorkspaceItNeeds_TS_12_58(t *testing.T) {
	e := newEnv(t)
	e.addWorkspace("gadgets")
	e.createDirect("widgets", jobs.KindFix, 1)
	e.createDirect("gadgets", jobs.KindFix, 2)
	e.Hub.FailFor("/api/v1/workspaces/gadgets", callerToken, 503)

	r := e.call("GET", "/api/v1/jobs", nil)
	body := r.JSON(t)
	if code, _ := r.errorOf(t); r.Status != 502 || code != "hub_unavailable" {
		t.Fatalf("status %d, code %q, want 502 hub_unavailable\n%s", r.Status, code, r.Body)
	}
	if _, has := body["jobs"]; has {
		t.Errorf("the answer has a jobs array, want no partial page\n%s", r.Body)
	}

	e.Hub.Clear()
	if got := e.call("GET", "/api/v1/jobs", nil).page(t); len(got.Jobs) != 2 {
		t.Errorf("once hub answers the list has %d jobs, want 2 (a failure is not kept)", len(got.Jobs))
	}

	// A workspace that the filter leaves no job on is not one the list needs. (A caller whose
	// view of gadgets is not kept yet, because the caller's has been asked for.)
	e.createDirect("widgets", jobs.KindTriage, 9)
	e.Hub.AddUser("second-token", "user-2", []string{"widgets", "gadgets"})
	e.Hub.FailFor("/api/v1/workspaces/gadgets", "second-token", 503)
	if got := e.callAs("second-token", "GET", "/api/v1/jobs?kind=triage", nil).page(t); len(got.Jobs) != 1 || got.Jobs[0].Kind != jobs.KindTriage {
		t.Errorf("a list of triage jobs, which only widgets has: %+v, want its one job without asking hub about gadgets", got.ids())
	}
}

// TestAWorkspaceListsItsJobsAfterUnsubscribeAnd404sWhenHubHidesIt_TS_12_59 verifies
// TS-12-59: GET /workspaces/:slug/jobs of a workspace that was unsubscribed answers 200
// with the jobs it has left, and only its own; a workspace that is shown and has no jobs is
// an empty list; a workspace that hub hides answers the 404 of any workspace that is not
// there.
// Verifies: 12-REQ-9.3
func TestAWorkspaceListsItsJobsAfterUnsubscribeAnd404sWhenHubHidesIt_TS_12_59(t *testing.T) {
	e := newEnv(t)
	e.addWorkspace("gadgets")
	e.subscribe("hidden")
	mine := e.create("widgets", jobs.KindFix, 1)
	e.create("gadgets", jobs.KindFix, 2)
	e.createDirect("hidden", jobs.KindFix, 3)
	if r := e.call("DELETE", "/api/v1/workspaces/widgets", nil); r.Status != 204 {
		t.Fatalf("unsubscribe widgets: %d %s", r.Status, r.Body)
	}

	got := e.call("GET", "/api/v1/workspaces/widgets/jobs", nil).page(t)
	if !slices.Equal(got.ids(), []string{mine.ID}) || got.NextCursor != nil {
		t.Errorf("widgets lists %v (cursor %v), want its one job", got.ids(), got.NextCursor)
	}
	if got.Jobs[0].State != jobs.StateCancelled {
		t.Errorf("the job left is %s, want cancelled by the unsubscribe", got.Jobs[0].State)
	}

	e.Hub.Grant(callerID, "empty")
	if empty := e.call("GET", "/api/v1/workspaces/empty/jobs", nil).page(t); len(empty.Jobs) != 0 || empty.NextCursor != nil {
		t.Errorf("a workspace with no jobs lists %+v, want an empty list", empty)
	}

	hidden, absent := e.call("GET", "/api/v1/workspaces/hidden/jobs", nil), e.call("GET", "/api/v1/workspaces/nowhere/jobs", nil)
	if code, _ := hidden.errorOf(t); hidden.Status != 404 || code != "not_found" || string(hidden.Body) != string(absent.Body) {
		t.Errorf("a hidden workspace: %d %s, a missing one: %d %s, want the same 404 not_found", hidden.Status, hidden.Body, absent.Status, absent.Body)
	}
	// What is behind the guard is not looked at first: a bad parameter on a hidden workspace is no 400.
	if r := e.call("GET", "/api/v1/workspaces/hidden/jobs?limit=0", nil); r.Status != 404 {
		t.Errorf("a hidden workspace with a bad limit: %d, want 404", r.Status)
	}
}

// seeded is a job as the property test stores it.
type seeded struct {
	id, workspace, kind, state, createdAt string
	issue                                 int // 0: none
}

// seedJobs stores jobs of the shapes in the rows directly, so that their times and ids are the
// test's.
func (e *env) seedJobs(rows []seeded) {
	e.t.Helper()
	for _, s := range rows {
		var issue any
		input := `{"text":"x","title":"t"}`
		if s.issue != 0 {
			issue, input = s.issue, fmt.Sprintf(`{"issue":%d}`, s.issue)
		}
		if _, err := e.DB.ExecContext(ctx,
			`INSERT INTO af_jobs (id, workspace, kind, input, context, issue, source_kind, source_by, state, created_at)
			 VALUES (?, ?, ?, ?, '[]', ?, 'api', 'seed', ?, ?)`,
			s.id, s.workspace, s.kind, input, issue, s.state, s.createdAt); err != nil {
			e.t.Fatalf("seed %s: %v", s.id, err)
		}
	}
}

// follow gets the pages of the list at the path, from no cursor, with the query, and returns
// them. Every page but the last has next_cursor; none but the first is empty.
func (e *env) follow(path string, query url.Values) []listed {
	e.t.Helper()
	var pages []listed
	q := url.Values{}
	for k, v := range query {
		q[k] = slices.Clone(v)
	}
	for {
		p := e.call("GET", path+"?"+q.Encode(), nil).page(e.t)
		if len(pages) > 0 && len(p.Jobs) == 0 {
			e.t.Fatalf("%s?%s: page %d is empty after a next_cursor", path, query.Encode(), len(pages)+1)
		}
		pages = append(pages, p)
		if p.NextCursor == nil {
			return pages
		}
		if len(pages) > 1000 {
			e.t.Fatalf("%s?%s: the cursors do not end", path, query.Encode())
		}
		q.Set("cursor", *p.NextCursor)
	}
}

// TestFollowingNextCursorYieldsEveryFilteredJobOnceNewestFirst_TS_12_60 verifies TS-12-60:
// for 150 jobs of two kinds, six states, several issues and times that repeat, on two visible
// workspaces and one that is hidden, and for filters of kind, state and issue and limits from
// 1 to 200 (seeded), the pages followed from no cursor concatenate to the filtered jobs
// ordered by created_at descending and then id descending, with no job twice, a next_cursor
// exactly on the pages that are not the last, and 50 jobs to a page when there is no limit.
// Verifies: 12-REQ-9.4
func TestFollowingNextCursorYieldsEveryFilteredJobOnceNewestFirst_TS_12_60(t *testing.T) {
	e := newEnv(t)
	e.addWorkspace("gadgets")
	e.subscribe("secret")
	rng := rand.New(rand.NewSource(1202))
	states := []string{"queued", "running", "blocked", "done", "failed", "cancelled"}
	taken := map[string]bool{}
	var rows []seeded
	for i := range 150 {
		s := seeded{
			id:        fmt.Sprintf("job_%020x", rng.Uint64()>>8|uint64(i)<<56),
			workspace: []string{"widgets", "gadgets", "secret"}[rng.Intn(3)],
			kind:      []string{"fix", "triage"}[rng.Intn(2)],
			state:     states[rng.Intn(len(states))],
			// Ten distinct times for 150 jobs: ties are the rule.
			createdAt: fmt.Sprintf("2026-10-22T08:00:00.%03dZ", rng.Intn(10)),
			issue:     rng.Intn(6), // 0: no issue
		}
		if s.kind == "fix" && s.issue == 0 {
			s.issue = 1 + rng.Intn(5)
		}
		// One open job of a kind to an issue: the index would refuse the second.
		if key := fmt.Sprint(s.workspace, s.kind, s.issue); s.issue != 0 && slices.Contains(states[:3], s.state) {
			if taken[key] {
				s.state = "done"
			}
			taken[key] = true
		}
		rows = append(rows, s)
	}
	e.seedJobs(rows)

	oracle := func(workspaces []string, kind, state string, issue int) []string {
		var match []seeded
		for _, s := range rows {
			if slices.Contains(workspaces, s.workspace) && (kind == "" || s.kind == kind) && (state == "" || s.state == state) && (issue == 0 || s.issue == issue) {
				match = append(match, s)
			}
		}
		slices.SortFunc(match, func(a, b seeded) int {
			if c := strings.Compare(b.createdAt, a.createdAt); c != 0 {
				return c
			}
			return strings.Compare(b.id, a.id)
		})
		ids := make([]string, 0, len(match))
		for _, s := range match {
			ids = append(ids, s.id)
		}
		return ids
	}

	visible := []string{"widgets", "gadgets"}
	for range 45 {
		query := url.Values{}
		var kind, state string
		var issue int
		if rng.Intn(2) == 0 {
			kind = []string{"fix", "triage"}[rng.Intn(2)]
			query.Set("kind", kind)
		}
		if rng.Intn(2) == 0 {
			state = states[rng.Intn(len(states))]
			query.Set("state", state)
		}
		if rng.Intn(3) == 0 {
			issue = 1 + rng.Intn(5)
			query.Set("issue", fmt.Sprint(issue))
		}
		limit := 0
		switch rng.Intn(4) {
		case 0: // the default
		case 1:
			limit = 150 + rng.Intn(51)
		default:
			limit = 1 + rng.Intn(30)
		}
		if limit != 0 {
			query.Set("limit", fmt.Sprint(limit))
		}

		path, workspaces := "/api/v1/jobs", visible
		if rng.Intn(3) == 0 {
			slug := visible[rng.Intn(2)]
			path, workspaces = "/api/v1/workspaces/"+slug+"/jobs", []string{slug}
		}
		pages := e.follow(path, query)
		want := oracle(workspaces, kind, state, issue)
		var got []string
		for i, p := range pages {
			size := limit
			if size == 0 {
				size = 50
			}
			if last := i == len(pages)-1; !last && len(p.Jobs) != size {
				t.Errorf("%s?%s: page %d has %d jobs, want %d", path, query.Encode(), i+1, len(p.Jobs), size)
			} else if last && len(p.Jobs) > size {
				t.Errorf("%s?%s: the last page has %d jobs, over the limit %d", path, query.Encode(), len(p.Jobs), size)
			}
			got = append(got, p.ids()...)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s?%s: the pages hold %d jobs in an order that is not the filtered, ordered set of %d\n got  %v\n want %v",
				path, query.Encode(), len(got), len(want), got, want)
		}
		if len(want) > 0 && len(want) <= limit && len(pages) != 1 {
			t.Errorf("%s?%s: %d jobs fit one page of %d, and there are %d pages", path, query.Encode(), len(want), limit, len(pages))
		}
	}

	// With no limit a page holds 50; with the largest, 200 (all 150 here, so no cursor).
	all := oracle(visible, "", "", 0)
	if len(all) <= 50 {
		t.Fatalf("the seed has %d jobs on the visible workspaces, want more than 50", len(all))
	}
	if first := e.call("GET", "/api/v1/jobs", nil).page(t); len(first.Jobs) != 50 || first.NextCursor == nil {
		t.Errorf("the default page has %d jobs and cursor %v, want 50 and a cursor", len(first.Jobs), first.NextCursor)
	}
	if big := e.call("GET", "/api/v1/jobs?limit=200", nil).page(t); len(big.Jobs) != len(all) || big.NextCursor != nil {
		t.Errorf("limit=200: %d jobs and cursor %v, want all %d and none", len(big.Jobs), big.NextCursor, len(all))
	}
	// The order is the one the spec names: the newest first, and within a time the id, descending.
	if first := e.call("GET", "/api/v1/jobs?limit=200", nil).page(t); !slices.Equal(first.ids(), all) {
		t.Errorf("the order of a whole list is not created_at then id, newest first")
	}
	// Pages of 7 over the whole list.
	cursorPages := e.follow("/api/v1/jobs", url.Values{"limit": {"7"}})
	if n := len(cursorPages); n != (len(all)+6)/7 {
		t.Errorf("%d jobs in pages of 7: %d pages, want %d", len(all), n, (len(all)+6)/7)
	}
}

// TestInvalidParametersOfAListAnswer400NamingTheParameter_TS_12_61 verifies TS-12-61: on both
// list routes, a kind, state, issue, limit or cursor that is not valid answers 400
// invalid_request, and the message names the parameter.
// Verifies: 12-REQ-9.5
func TestInvalidParametersOfAListAnswer400NamingTheParameter_TS_12_61(t *testing.T) {
	e := newEnv(t)
	good := e.create("widgets", jobs.KindFix, 1)
	e.create("widgets", jobs.KindFix, 2)
	first := e.call("GET", "/api/v1/jobs?limit=1", nil).page(t)
	if first.NextCursor == nil {
		t.Fatal("no cursor to build on")
	}
	garbage := base64.RawURLEncoding.EncodeToString([]byte("hello"))
	notAJob := base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-10-22T08:00:00.000Z","id":"../../x"}`))
	notATime := base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"yesterday","id":"job_00000000000000000000"}`))
	// A cursor with a field more than the service writes, and one with a second value after the first.
	valid := `{"created_at":"2026-10-22T08:00:00.000Z","id":"job_00000000000000000000"`
	withExtra := base64.RawURLEncoding.EncodeToString([]byte(valid + `,"x":1}`))
	withTrailer := base64.RawURLEncoding.EncodeToString([]byte(valid + `}{}`))
	// A cursor spelled differently from the one the service gave, though it decodes to the same
	// bytes: the last character of a base64 text that does not fill its last group carries bits
	// that are not used.
	var respelled []string
	given := *first.NextCursor
	wantBytes, err := base64.RawURLEncoding.DecodeString(given)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_" {
		other := given[:len(given)-1] + string(c)
		if bytes, err := base64.RawURLEncoding.DecodeString(other); err == nil && other != given && string(bytes) == string(wantBytes) {
			respelled = append(respelled, other)
		}
	}
	if len(respelled) == 0 {
		t.Fatalf("no respelling of the cursor %s decodes to the same bytes; the test needs a cursor with spare bits", given)
	}

	cases := []struct{ query, param string }{
		{"kind=bug", "kind"}, {"state=paused", "state"}, {"issue=0", "issue"}, {"issue=x", "issue"},
		{"limit=0", "limit"}, {"limit=201", "limit"}, {"limit=abc", "limit"}, {"cursor=!!!", "cursor"},
		// Neighbours of those: what is close to valid is not.
		{"kind=", "kind"}, {"kind=FIX", "kind"}, {"kind=fix&kind=triage", "kind"}, {"state=", "state"}, {"state=Done", "state"},
		{"issue=-1", "issue"}, {"issue=1.5", "issue"}, {"issue=%2B3", "issue"}, {"issue=007", "issue"}, {"issue=99999999999999999999", "issue"}, {"issue=1&issue=2", "issue"},
		{"limit=-1", "limit"}, {"limit=", "limit"}, {"limit=1.0", "limit"}, {"limit=1e2", "limit"}, {"limit=%205", "limit"}, {"limit=5&limit=6", "limit"},
		{"cursor=", "cursor"}, {"cursor=" + garbage, "cursor"}, {"cursor=" + notAJob, "cursor"}, {"cursor=" + notATime, "cursor"},
		{"cursor=" + *first.NextCursor + "&cursor=" + *first.NextCursor, "cursor"},
		{"cursor=" + *first.NextCursor + "=", "cursor"}, {"cursor=" + withExtra, "cursor"}, {"cursor=" + withTrailer, "cursor"}, {"cursor=" + respelled[0], "cursor"},
		{"limit=05", "limit"}, {"limit=%2B5", "limit"},
		// The first that is wrong is named, in the order kind, state, issue, limit, cursor.
		{"limit=0&state=paused", "state"}, {"cursor=!!!&issue=0", "issue"},
	}
	for _, path := range []string{"/api/v1/jobs", "/api/v1/workspaces/widgets/jobs"} {
		for _, tc := range cases {
			r := e.call("GET", path+"?"+tc.query, nil)
			code, message := r.errorOf(t)
			if r.Status != 400 || code != "invalid_request" || !strings.Contains(message, tc.param) {
				t.Errorf("GET %s?%s: %d %s %q, want 400 invalid_request naming %q", path, tc.query, r.Status, code, message, tc.param)
			}
		}
		// What is valid, with the edges, is not refused; the cursor is the one the service gave.
		for _, query := range []string{"", "kind=fix", "kind=triage", "state=queued", "state=cancelled", "issue=1", "limit=1", "limit=200", "kind=fix&state=queued&issue=2&limit=5",
			"cursor=" + *first.NextCursor, "unknown=1"} {
			if r := e.call("GET", path+"?"+query, nil); r.Status != 200 {
				t.Errorf("GET %s?%s: %d %s, want 200", path, query, r.Status, r.Body)
			}
		}
	}
	// A query string that cannot be read at all is refused too.
	for _, query := range []string{"limit=%zz", "limit=5;kind=fix"} {
		if r := e.call("GET", "/api/v1/jobs?"+query, nil); r.Status != 400 {
			t.Errorf("GET /api/v1/jobs?%s: %d, want 400", query, r.Status)
		}
	}
	// The cursor of one page leads to the rest of it.
	rest := e.call("GET", "/api/v1/jobs?limit=1&cursor="+url.QueryEscape(*first.NextCursor), nil).page(t)
	if len(rest.Jobs) != 1 || rest.Jobs[0].ID == first.Jobs[0].ID || rest.NextCursor != nil {
		t.Errorf("the second page = %v (cursor %v), want the other job and the end; the first was %s of %s", rest.ids(), rest.NextCursor, first.Jobs[0].ID, good.ID)
	}
	// Who is calling comes before the parameters.
	if r := e.callAs("", "GET", "/api/v1/jobs?limit=0", nil); r.Status != 401 {
		t.Errorf("a bad limit without a token: %d, want 401", r.Status)
	}
}
