package fakehub

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIssue_IsReadWithItsComments(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddIssue("acme", Issue{Number: 42, Title: "Nil map", Body: "It panics.", Labels: []string{"bug"}, Comments: []Comment{{Author: "bob", Body: "me too"}}})
	h.AddIssue("acme", Issue{Number: 43, IsPR: true})
	thread := do(t, h, "GET", "/api/v1/workspaces/acme/issues/42", pat, nil).Object(t)
	want := map[string]any{
		"issue": map[string]any{
			"number": 42.0, "title": "Nil map", "body": "It panics.", "state": "open", "labels": []any{"bug"}, "is_pr": false,
			"url": "https://api.example.com/repos/acme/widgets/issues/42", "html_url": "https://github.example.com/acme/widgets/issues/42",
			"author": map[string]any{"login": "reporter"}, "created_at": "2026-01-02T03:04:05Z", "updated_at": "2026-01-02T03:04:05Z",
		},
		"comments":  []any{map[string]any{"id": 1.0, "body": "me too", "user": map[string]any{"login": "bob"}, "created_at": "2026-01-02T03:04:06Z"}},
		"truncated": false,
	}
	if !reflect.DeepEqual(thread, want) {
		t.Errorf("the thread = %v\nwant %v", thread, want)
	}
	if got := do(t, h, "GET", "/api/v1/workspaces/acme/issues/42/comments", pat, nil).Objects(t); !reflect.DeepEqual(got[0], want["comments"].([]any)[0]) {
		t.Errorf("the comments = %v", got)
	}
	if pr := do(t, h, "GET", "/api/v1/workspaces/acme/issues/43", pat, nil).Object(t)["issue"].(map[string]any); pr["is_pr"] != true {
		t.Errorf("a pull request = %v, want it marked", pr)
	}
	for path, want := range map[string]string{"/issues/99": "forge_not_found", "/issues/x": "bad_request", "/issues/42/other": "not_found"} {
		if r := do(t, h, "GET", "/api/v1/workspaces/acme"+path, pat, nil); errorType(t, r) != want {
			t.Errorf("%s: %d %s, want %s", path, r.Status, r.Body, want)
		}
	}
}

// The writes change the issue as hub holds it, and only in what the request names.
func TestIssue_WritesChangeTheIssue(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddIssue("acme", Issue{Number: 42, Title: "T", Body: "B", Labels: []string{"af:ready"}})
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	h.SetClock(func() time.Time { return now })
	const issue = "/api/v1/workspaces/acme/issues/42"
	for _, c := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"PATCH", issue, map[string]any{"body": "new body"}, 200},
		{"PATCH", issue, map[string]any{"title": "new title", "state": "closed", "labels": []string{"af:ready", "bug"}}, 200},
		{"POST", issue + "/labels", []string{"bug", "af:in-progress"}, 204},
		{"DELETE", issue + "/labels/af:ready", nil, 204},
		{"POST", issue + "/comments", map[string]string{"body": "a comment"}, 201},
		{"POST", issue + "/comments", map[string]string{"body": ""}, 400},
		{"PATCH", issue, "{", 400},
		{"POST", issue + "/labels", "{", 400},
		{"POST", issue + "/comments", "{", 400},
	} {
		if r := do(t, h, c.method, c.path, pat, c.body); r.Status != c.want {
			t.Errorf("%s %s %v: %d %s, want %d", c.method, c.path, c.body, r.Status, r.Body, c.want)
		}
	}
	got, ok := h.IssueOf("acme", 42)
	want := Issue{Number: 42, Title: "new title", Body: "new body", State: "closed", Author: "reporter", Labels: []string{"bug", "af:in-progress"},
		Comments: []Comment{{ID: 1, Author: "agent-fox", Body: "a comment", CreatedAt: now}}, CreatedAt: got.CreatedAt}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("the issue = %+v\nwant %+v", got, want)
	}
	if _, ok := h.IssueOf("acme", 7); ok {
		t.Error("an issue that is not there was found")
	}
}

func TestIssuesAndPulls_AreCreated(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddIssue("acme", Issue{Number: 120})
	r := do(t, h, "POST", "/api/v1/workspaces/acme/issues", pat, map[string]any{"title": "New", "body": "b", "labels": []string{"af:fix"}})
	if issue := r.Object(t); r.Status != 201 || issue["number"] != 121.0 || issue["title"] != "New" {
		t.Errorf("the issue: %d %s, want the number after the highest", r.Status, r.Body)
	}
	r = do(t, h, "POST", "/api/v1/workspaces/acme/pulls", pat, map[string]any{"title": "Fix", "body": "Closes #121", "head": "fix/121", "base": "main", "draft": true})
	if pull := r.Object(t); r.Status != 201 || pull["number"] != 122.0 || pull["draft"] != true || pull["head_branch"] != "fix/121" || pull["html_url"] != "https://github.example.com/acme/widgets/pull/122" {
		t.Errorf("the pull request: %d %s", r.Status, r.Body)
	}
	for path, body := range map[string]any{"/issues": map[string]string{"body": "no title"}, "/pulls": map[string]string{"title": "no head"}} {
		if r := do(t, h, "POST", "/api/v1/workspaces/acme"+path, pat, body); r.Status != 400 {
			t.Errorf("%s with %v: %d, want 400", path, body, r.Status)
		}
		if r := do(t, h, "POST", "/api/v1/workspaces/acme"+path, pat, "{"); r.Status != 400 {
			t.Errorf("%s with a malformed body: %d, want 400", path, r.Status)
		}
	}
	if issues := h.Issues("acme"); len(issues) != 2 || issues[1].Number != 121 || issues[1].Author != "agent-fox" || !slices.Equal(issues[1].Labels, []string{"af:fix"}) {
		t.Errorf("Issues = %+v", issues)
	}
	if pulls := h.Pulls("acme"); !reflect.DeepEqual(pulls, []Pull{{Number: 122, Title: "Fix", Body: "Closes #121", Head: "fix/121", Base: "main", Draft: true}}) {
		t.Errorf("Pulls = %+v", pulls)
	}
	if h.Issues("missing") != nil || h.Pulls("missing") != nil {
		t.Error("a workspace that is not there has issues or pull requests")
	}
}

// A person's comment and the service's each carry the time they were added at, in that order.
func TestComments_OfAPersonAndOfTheServiceAreInTheOrderTheyWereAdded(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddIssue("acme", Issue{Number: 42})
	now := time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)
	h.SetClock(func() time.Time { return now })
	h.AddPersonComment("acme", 42, "alice", "a")
	now = now.Add(time.Minute)
	do(t, h, "POST", "/api/v1/workspaces/acme/issues/42/comments", pat, map[string]string{"body": "b"})

	comments := do(t, h, "GET", "/api/v1/workspaces/acme/issues/42/comments", pat, nil).Objects(t)
	if len(comments) != 2 || comments[0]["body"] != "a" || comments[0]["user"].(map[string]any)["login"] != "alice" ||
		comments[0]["created_at"] != "2026-10-24T08:00:00Z" || comments[1]["body"] != "b" || comments[1]["created_at"] != "2026-10-24T08:01:00Z" {
		t.Errorf("the comments = %v, want alice's, then the service's a minute later", comments)
	}
	h.SetClock(nil)
	h.AddPersonComment("acme", 42, "bob", "c")
	if issue, _ := h.IssueOf("acme", 42); time.Since(issue.Comments[2].CreatedAt) > time.Minute {
		t.Errorf("with no clock the comment is stamped %v, want now", issue.Comments[2].CreatedAt)
	}
}

// list are the numbers of the issues the list with the query answers, and its Link header.
func list(t *testing.T, h *Hub, query string) ([]int, string) {
	t.Helper()
	r := do(t, h, "GET", "/api/v1/workspaces/acme/issues?"+query, pat, nil)
	if r.Status != 200 {
		t.Fatalf("%s: %d %s", query, r.Status, r.Body)
	}
	out := []int{}
	for _, issue := range r.Objects(t) {
		out = append(out, int(issue["number"].(float64)))
	}
	return out, r.Header.Get("Link")
}

func TestListIssues_FiltersByStateAndLabels(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddIssue("acme", Issue{Number: 1, Labels: []string{"af:fix"}})
	h.AddIssue("acme", Issue{Number: 2, State: "closed", Labels: []string{"af:fix"}})
	h.AddIssue("acme", Issue{Number: 3, Labels: []string{"bug"}})
	h.AddIssue("acme", Issue{Number: 4, Labels: []string{"af:fix"}, IsPR: true})
	h.AddIssue("acme", Issue{Number: 5, Labels: []string{"af:fix", "bug"}})
	for query, want := range map[string][]int{
		"state=open&labels=af%3Afix":   {1, 4, 5},
		"state=closed&labels=af%3Afix": {2},
		"state=all&labels=af%3Afix":    {1, 2, 4, 5},
		"labels=af%3Afix":              {1, 4, 5},
		"labels=af%3Afix%2Cbug":        {5},
		"labels=af%3Afix&labels=bug":   {5},
		"state=open":                   {1, 3, 4, 5},
		"state=open&labels=nothing":    {},
	} {
		if got, _ := list(t, h, query); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", query, got, want)
		}
	}
	for _, query := range []string{"state=pending", "limit=0", "limit=101", "limit=x", "page=0", "page=x"} {
		name, _, _ := strings.Cut(query, "=")
		if r := do(t, h, "GET", "/api/v1/workspaces/acme/issues?"+query, pat, nil); r.Status != 400 || !strings.Contains(r.Body, name) {
			t.Errorf("%s: %d %s, want 400 naming the parameter", query, r.Status, r.Body)
		}
	}
}

// Pages are linked to the next unless the hub is told to say nothing, as hub's own list does.
func TestListIssues_AnswersPagesInTheOrderAsked(t *testing.T) {
	h := newHub(t, Workspace{})
	for n := 1; n <= 150; n++ {
		h.AddIssue("acme", Issue{Number: n, Labels: []string{"af:fix"}})
	}
	first, link := list(t, h, "labels=af%3Afix&limit=100")
	if len(first) != 100 || first[99] != 100 || link != `</api/v1/workspaces/acme/issues?labels=af%3Afix&limit=100&page=2>; rel="next"` {
		t.Errorf("the first page: %d issues, Link %q", len(first), link)
	}
	if second, link := list(t, h, "labels=af%3Afix&limit=100&page=2"); len(second) != 50 || second[0] != 101 || link != "" {
		t.Errorf("the second page: %d issues from %d, Link %q", len(second), second[0], link)
	}
	if got, _ := list(t, h, "labels=af%3Afix&page=9"); len(got) != 0 {
		t.Errorf("a page past the end holds %v", got)
	}
	if got, _ := list(t, h, "labels=af%3Afix"); len(got) != 30 {
		t.Errorf("a list with no limit holds %d, want 30", len(got))
	}
	h.SetPageLinks("acme", false)
	if got, link := list(t, h, "labels=af%3Afix&limit=100"); len(got) != 100 || link != "" {
		t.Errorf("with no links: %d issues, Link %q", len(got), link)
	}
	h.SetListOrder("acme", 150, 3)
	if got, _ := list(t, h, "limit=4"); !slices.Equal(got, []int{150, 3, 1, 2}) {
		t.Errorf("the order is %v, want the numbers named first, then the rest by number", got)
	}
	if n := len(h.ListRequests("acme")); n != 6 {
		t.Errorf("%d list requests recorded, want 6", n)
	}
}

// A forge whose index lags lists an issue under a label for some lists after the label went, and
// leaves it out for some after it came; only a first page of that label counts as a list.
func TestListIssues_LagBehindTheLabels(t *testing.T) {
	const fix = "labels=af%3Afix"
	t.Run("removal", func(t *testing.T) {
		h := newHub(t, Workspace{})
		h.AddIssue("acme", Issue{Number: 42, Labels: []string{"af:fix"}})
		h.SetRemovalLag("acme", 2)
		do(t, h, "DELETE", "/api/v1/workspaces/acme/issues/42/labels/af:fix", pat, nil)
		var got [][]int
		for range 3 {
			n, _ := list(t, h, fix)
			got = append(got, n)
		}
		if !reflect.DeepEqual(got, [][]int{{42}, {42}, {}}) {
			t.Errorf("the lists after the removal = %v, want the issue in the two first", got)
		}
		h.RemoveLabel("acme", 42, "af:fix")
		h.AddLabel("acme", 42, "af:fix")
		h.AddLabel("acme", 42, "af:fix")
		if got, _ := list(t, h, fix); !slices.Equal(got, []int{42}) {
			t.Errorf("a label put back: %v, want the issue once", got)
		}
		if issue, _ := h.IssueOf("acme", 42); !slices.Equal(issue.Labels, []string{"af:fix"}) {
			t.Errorf("the labels = %v", issue.Labels)
		}
	})
	t.Run("addition", func(t *testing.T) {
		h := newHub(t, Workspace{})
		h.AddIssue("acme", Issue{Number: 42})
		for n := 100; n < 250; n++ {
			h.AddIssue("acme", Issue{Number: n, Labels: []string{"bug"}})
		}
		h.SetAdditionLag("acme", 1)
		h.AddIssue("acme", Issue{Number: 1, Labels: []string{"af:fix"}})
		do(t, h, "POST", "/api/v1/workspaces/acme/issues/42/labels", pat, []string{"af:fix"})
		list(t, h, "labels=bug")
		list(t, h, fix+"&page=2")
		if got, _ := list(t, h, fix); !slices.Equal(got, []int{1}) {
			t.Errorf("the list after the addition = %v, want the issue added with its label alone", got)
		}
		if got, _ := list(t, h, fix); !slices.Equal(got, []int{1, 42}) {
			t.Errorf("the list after the lag = %v, want both", got)
		}
	})
}
