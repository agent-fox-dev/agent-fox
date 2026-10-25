package jobs_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// seeded is a job as the tests of the list store it, with a time and an id of their own.
type seeded struct {
	id, workspace, kind, state, createdAt string
	issue                                 int // 0: none
}

func (e *env) seed(rows []seeded) {
	e.t.Helper()
	for _, s := range rows {
		var issue any
		input := `{"text":"x"}`
		if s.issue != 0 {
			issue, input = s.issue, fmt.Sprintf(`{"issue":%d}`, s.issue)
		}
		testutil.Exec(e.t, e.DB, `INSERT INTO af_jobs (id, workspace, kind, input, context, issue, source_kind, source_by, state, created_at)
			VALUES (?, ?, ?, ?, '[]', ?, 'api', 'seed', ?, ?)`, s.id, s.workspace, s.kind, input, issue, s.state, s.createdAt)
	}
}

// pages are the ids of every page of the list, followed from no cursor.
func (e *env) pages(workspaces []string, query url.Values) [][]string {
	e.t.Helper()
	var out [][]string
	for {
		f, err := jobs.ParseList(query)
		if err != nil {
			e.t.Fatal(err)
		}
		page, err := e.Svc.List(ctx, workspaces, f)
		if err != nil {
			e.t.Fatal(err)
		}
		var ids []string
		for _, j := range page.Jobs {
			ids = append(ids, j.ID)
			if j.Attempts != nil {
				e.t.Errorf("an entry of a list has attempts")
			}
		}
		out = append(out, ids)
		if page.NextCursor == nil {
			return out
		}
		query.Set("cursor", *page.NextCursor)
	}
}

func TestList_PagesThroughTheFilteredJobsNewestFirstEachOnce(t *testing.T) {
	e := newEnv(t)
	rng := rand.New(rand.NewPCG(12, 60))
	states := []string{"queued", "running", "blocked", "done", "failed", "cancelled"}
	open := map[string]bool{}
	var rows []seeded
	for i := range 150 {
		s := seeded{
			id: fmt.Sprintf("job_%020x", rng.Uint64()>>8|uint64(i)<<56), workspace: []string{"widgets", "gadgets", "secret"}[rng.IntN(3)],
			kind: []string{"fix", "triage"}[rng.IntN(2)], state: states[rng.IntN(6)], issue: 1 + rng.IntN(5),
			createdAt: fmt.Sprintf("2026-10-22T08:00:00.%03dZ", rng.IntN(10)), // ten times for 150 jobs: ties are the rule
		}
		if key := fmt.Sprint(s.workspace, s.kind, s.issue); slices.Contains(states[:3], s.state) {
			if open[key] { // one open job of a kind for an issue
				s.state = "done"
			}
			open[key] = true
		}
		rows = append(rows, s)
	}
	e.seed(rows)
	visible := []string{"widgets", "gadgets"}
	want := func(workspaces []string, kind, state string, issue int) []string {
		var match []seeded
		for _, s := range rows {
			if slices.Contains(workspaces, s.workspace) && (kind == "" || s.kind == kind) && (state == "" || s.state == state) && (issue == 0 || s.issue == issue) {
				match = append(match, s)
			}
		}
		slices.SortFunc(match, func(a, b seeded) int {
			return strings.Compare(b.createdAt+b.id, a.createdAt+a.id)
		})
		var ids []string
		for _, s := range match {
			ids = append(ids, s.id)
		}
		return ids
	}
	for range 30 {
		query := url.Values{}
		var kind, state string
		var issue int
		if rng.IntN(2) == 0 {
			kind = []string{"fix", "triage"}[rng.IntN(2)]
			query.Set("kind", kind)
		}
		if rng.IntN(2) == 0 {
			state = states[rng.IntN(6)]
			query.Set("state", state)
		}
		if rng.IntN(3) == 0 {
			issue = 1 + rng.IntN(5)
			query.Set("issue", fmt.Sprint(issue))
		}
		size := 50
		if rng.IntN(3) > 0 {
			size = 1 + rng.IntN(30)
			query.Set("limit", fmt.Sprint(size))
		}
		workspaces := visible
		if rng.IntN(3) == 0 {
			workspaces = visible[rng.IntN(2):][:1]
		}
		label := query.Encode()
		pages := e.pages(workspaces, query)
		var got []string
		for i, p := range pages {
			if last := i == len(pages)-1; !last && len(p) != size || last && len(p) > size || i > 0 && len(p) == 0 {
				t.Errorf("%s: page %d of %d has %d jobs, want %d on every page but the last, which is not empty", label, i+1, len(pages), len(p), size)
			}
			got = append(got, p...)
		}
		if w := want(workspaces, kind, state, issue); !slices.Equal(got, w) {
			t.Fatalf("%s on %v: the pages hold\n%v\nwant\n%v", label, workspaces, got, w)
		}
	}
	slugs, err := e.Svc.Workspaces(ctx, jobs.ListFilter{Kind: jobs.KindFix})
	slices.Sort(slugs)
	if err != nil || !slices.Equal(slugs, []string{"gadgets", "secret", "widgets"}) {
		t.Errorf("Workspaces = %v, %v, want the three with a fix", slugs, err)
	}
	if none, err := e.Svc.Workspaces(ctx, jobs.ListFilter{Issue: 99}); err != nil || none == nil || len(none) != 0 {
		t.Errorf("Workspaces of no job = %#v, %v, want an empty list", none, err)
	}
	if page, err := e.Svc.List(ctx, []string{"nowhere"}, jobs.ListFilter{}); err != nil || page.Jobs == nil || len(page.Jobs) != 0 || page.NextCursor != nil {
		t.Errorf("a list of nothing = %+v, %v, want an empty page and no cursor", page, err)
	}
}

func TestParseList_ReadsEachParameter(t *testing.T) {
	f, err := jobs.ParseList(url.Values{"kind": {"fix"}, "state": {"cancelled"}, "issue": {"12"}, "limit": {"200"}, "unknown": {"x", "y"}})
	if err != nil || f.Kind != jobs.KindFix || f.State != jobs.StateCancelled || f.Issue != 12 || f.Limit != 200 {
		t.Errorf("ParseList = %+v, %v", f, err)
	}
	if f, err := jobs.ParseList(url.Values{}); err != nil || f != (jobs.ListFilter{}) {
		t.Errorf("ParseList of nothing = %+v, %v, want no restriction", f, err)
	}
}

func TestParseList_RefusesTheFirstParameterThatIsNotValidNamingIt(t *testing.T) {
	cursor := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	valid := `{"created_at":"2026-10-22T08:00:00.000Z","id":"job_00000000000000000000"}`
	given := cursor(valid)
	if _, err := jobs.ParseList(url.Values{"cursor": {given}}); err != nil {
		t.Fatalf("a cursor of the service's form is refused: %v", err)
	}
	// The last character of a base64 text that does not fill its last group carries bits that are not used: a cursor
	// spelled otherwise decodes to the same bytes, and is not one the service gave.
	var respelled string
	for _, c := range "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_" {
		other := given[:len(given)-1] + string(c)
		if b, err := base64.RawURLEncoding.DecodeString(other); err == nil && other != given && string(b) == valid {
			respelled = other
		}
	}
	for _, tc := range []struct{ query, param string }{
		{"kind=bug", "kind"}, {"kind=", "kind"}, {"kind=FIX", "kind"}, {"kind=fix&kind=triage", "kind"},
		{"state=paused", "state"}, {"state=", "state"}, {"state=Done", "state"},
		{"issue=0", "issue"}, {"issue=x", "issue"}, {"issue=-1", "issue"}, {"issue=1.5", "issue"}, {"issue=%2B3", "issue"}, {"issue=007", "issue"},
		{"issue=99999999999999999999", "issue"}, {"issue=1&issue=2", "issue"},
		{"limit=0", "limit"}, {"limit=201", "limit"}, {"limit=abc", "limit"}, {"limit=", "limit"}, {"limit=05", "limit"}, {"limit=1e2", "limit"},
		{"limit=99999999999999999999", "limit"}, {"limit=5&limit=6", "limit"},
		{"cursor=", "cursor"}, {"cursor=!!!", "cursor"}, {"cursor=" + given + "=", "cursor"}, {"cursor=" + respelled, "cursor"},
		{"cursor=" + cursor("hello"), "cursor"}, {"cursor=" + given + "&cursor=" + given, "cursor"},
		{"cursor=" + cursor(`{"created_at":"2026-10-22T08:00:00.000Z","id":"../../x"}`), "cursor"},
		{"cursor=" + cursor(`{"created_at":"yesterday","id":"job_00000000000000000000"}`), "cursor"},
		{"cursor=" + cursor(`{"created_at":"2026-10-22T08:00:00Z","id":"job_00000000000000000000"}`), "cursor"},
		{"cursor=" + cursor(valid[:len(valid)-1]+`,"x":1}`), "cursor"},
		{"cursor=" + cursor(valid+`{}`), "cursor"},
		{"limit=0&state=paused", "state"}, {"cursor=!!!&issue=0", "issue"},
	} {
		query, err := url.ParseQuery(tc.query)
		if err != nil {
			t.Fatal(err)
		}
		_, err = jobs.ParseList(query)
		var invalid *jobs.ValidationError
		if !errors.As(err, &invalid) || invalid.Field != tc.param {
			t.Errorf("%s: %v, want a *ValidationError of %s", tc.query, err, tc.param)
		}
	}
}
