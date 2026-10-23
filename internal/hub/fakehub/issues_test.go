package fakehub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// listAnswer is an answer of the issues list: the numbers of the issues, whether each is marked as a pull
// request, and the Link header.
type listAnswer struct {
	Status  int
	Numbers []int
	PRs     map[int]bool
	Link    string
	Body    string
}

// list calls the issues list of the workspace with the query.
func list(t *testing.T, h *Hub, slug, query, token string) listAnswer {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, h.URL+"/api/v1/workspaces/"+slug+"/issues?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := listAnswer{Status: resp.StatusCode, Link: resp.Header.Get("Link"), Body: string(raw), PRs: map[int]bool{}, Numbers: []int{}}
	var items []struct {
		Number int  `json:"number"`
		IsPR   bool `json:"is_pr"`
	}
	if resp.StatusCode == 200 {
		if err := json.Unmarshal(raw, &items); err != nil {
			t.Fatalf("the answer is no list: %v\n%s", err, raw)
		}
	}
	for _, i := range items {
		out.Numbers = append(out.Numbers, i.Number)
		out.PRs[i.Number] = i.IsPR
	}
	return out
}

// listHub is a hub with the workspace widgets.
func listHub(t *testing.T) *Hub {
	t.Helper()
	h := New(t)
	h.AddWorkspace(t, Workspace{Slug: "widgets", PAT: "pat-widgets"})
	return h
}

// TestFakehubServesTheIssuesListWithFiltersPagingPullRequestsAndBothLags_TS_15_21 verifies TS-15-21: the state and
// labels filters narrow the list; a list of more issues than a page reports a further page, and says nothing of one
// when it is told not to; a pull request is returned with the marker; an issue stays listed under a label for the
// lists after its removal, and is left out of the lists of a label for the lists after its addition; and the lags
// apply to the removals and additions that the routes make as well as to those a test makes.
// Verifies: 15-REQ-3.8
func TestFakehubServesTheIssuesListWithFiltersPagingPullRequestsAndBothLags_TS_15_21(t *testing.T) {
	const pat = "pat-widgets"

	t.Run("the filters", func(t *testing.T) {
		h := listHub(t)
		h.AddIssue("widgets", Issue{Number: 1, Labels: []string{"af:fix"}})
		h.AddIssue("widgets", Issue{Number: 2, State: "closed", Labels: []string{"af:fix"}})
		h.AddIssue("widgets", Issue{Number: 3, Labels: []string{"bug"}})
		h.AddIssue("widgets", Issue{Number: 4, Labels: []string{"af:fix"}, IsPR: true})
		h.AddIssue("widgets", Issue{Number: 5, Labels: []string{"af:fix", "bug"}})
		for query, want := range map[string][]int{
			"state=open&labels=af%3Afix":            {1, 4, 5},
			"state=closed&labels=af%3Afix":          {2},
			"state=all&labels=af%3Afix":             {1, 2, 4, 5},
			"labels=af%3Afix":                       {1, 4, 5},
			"state=open&labels=af%3Afix%2Cbug":      {5},
			"state=open&labels=bug":                 {3, 5},
			"state=open":                            {1, 3, 4, 5},
			"state=open&labels=nothing":             {},
			"state=open&labels=af%3Afix&labels=bug": {5},
		} {
			if got := list(t, h, "widgets", query, pat); got.Status != 200 || !slices.Equal(got.Numbers, want) {
				t.Errorf("%s: %d %v, want 200 %v", query, got.Status, got.Numbers, want)
			}
		}
		if got := list(t, h, "widgets", "state=open&labels=af%3Afix", pat); !got.PRs[4] || got.PRs[1] {
			t.Errorf("the pull request 4 is marked %v and the issue 1 %v, want the marker on the pull request only", got.PRs[4], got.PRs[1])
		}
		for _, query := range []string{"state=pending", "limit=0", "limit=101", "limit=x", "page=0", "page=x"} {
			got := list(t, h, "widgets", query, pat)
			if name, _, _ := strings.Cut(query, "="); got.Status != 400 || !strings.Contains(got.Body, name) {
				t.Errorf("%s: %d %s, want 400 with a message that names the parameter", query, got.Status, got.Body)
			}
		}
		if got := list(t, h, "widgets", "", "someone-else"); got.Status != 401 {
			t.Errorf("a token that hub does not know: %d, want 401", got.Status)
		}
	})

	t.Run("paging", func(t *testing.T) {
		h := listHub(t)
		for n := 1; n <= 150; n++ {
			h.AddIssue("widgets", Issue{Number: n, Labels: []string{"af:fix"}})
		}
		first := list(t, h, "widgets", "state=open&labels=af%3Afix&limit=100", pat)
		if len(first.Numbers) != 100 || first.Numbers[0] != 1 || first.Numbers[99] != 100 {
			t.Errorf("the first page holds %v, want 100 issues from 1 to 100", first.Numbers)
		}
		if !strings.Contains(first.Link, `rel="next"`) || !strings.Contains(first.Link, "page=2") {
			t.Errorf("the first page says %q of a further page, want a link to page 2", first.Link)
		}
		second := list(t, h, "widgets", "state=open&labels=af%3Afix&limit=100&page=2", pat)
		if len(second.Numbers) != 50 || second.Numbers[0] != 101 || second.Link != "" {
			t.Errorf("the second page holds %d issues and says %q, want 50 from 101 and no further page", len(second.Numbers), second.Link)
		}
		if got := list(t, h, "widgets", "labels=af%3Afix", pat); len(got.Numbers) != 30 {
			t.Errorf("a list with no limit holds %d issues, want hub's default of 30", len(got.Numbers))
		}
		h.SetPageLinks("widgets", false)
		if got := list(t, h, "widgets", "state=open&labels=af%3Afix&limit=100", pat); len(got.Numbers) != 100 || got.Link != "" {
			t.Errorf("a hub that says nothing of a further page answered %d issues and %q", len(got.Numbers), got.Link)
		}
		h.SetPageLinks("widgets", true)
		h.SetListOrder("widgets", 150, 3)
		if got := list(t, h, "widgets", "labels=af%3Afix&limit=5", pat); !slices.Equal(got.Numbers, []int{150, 3, 1, 2, 4}) {
			t.Errorf("the order is %v, want the numbers that were named first and then the rest by number", got.Numbers)
		}
		if n := len(h.ListRequests("widgets")); n != 5 {
			t.Errorf("%d list requests are recorded, want 5", n)
		}
	})

	t.Run("the removal lag", func(t *testing.T) {
		h := listHub(t)
		h.AddIssue("widgets", Issue{Number: 42, Labels: []string{"af:fix"}})
		h.SetRemovalLag("widgets", 2)
		h.RemoveLabel("widgets", 42, "af:fix")
		for i, want := range [][]int{{42}, {42}, {}, {}} {
			if got := list(t, h, "widgets", "state=open&labels=af%3Afix", pat); !slices.Equal(got.Numbers, want) {
				t.Errorf("list %d after the removal: %v, want %v", i+1, got.Numbers, want)
			}
		}
		if issue, _ := h.IssueOf("widgets", 42); len(issue.Labels) != 0 {
			t.Errorf("the issue still carries %v", issue.Labels)
		}

		// The removal the service makes through the route lags as well, and a label put back ends the lag.
		h.AddLabel("widgets", 42, "af:fix")
		h.SetRemovalLag("widgets", 3)
		req, _ := http.NewRequest(http.MethodDelete, h.URL+"/api/v1/workspaces/widgets/issues/42/labels/af:fix", nil)
		req.Header.Set("Authorization", "Bearer "+pat)
		if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 204 {
			t.Fatalf("DELETE: %v %v", resp, err)
		}
		if got := list(t, h, "widgets", "labels=af%3Afix", pat); !slices.Equal(got.Numbers, []int{42}) {
			t.Errorf("a list after the removal through the route: %v, want the issue still listed", got.Numbers)
		}
		h.AddLabel("widgets", 42, "af:fix")
		h.RemoveLabel("widgets", 42, "af:fix")
		h.AddLabel("widgets", 42, "af:fix")
		if got := list(t, h, "widgets", "labels=af%3Afix", pat); !slices.Equal(got.Numbers, []int{42}) {
			t.Errorf("a list after the label was put back: %v, want the issue once", got.Numbers)
		}
	})

	t.Run("the addition lag", func(t *testing.T) {
		h := listHub(t)
		h.AddIssue("widgets", Issue{Number: 42})
		h.SetAdditionLag("widgets", 2)
		h.AddLabel("widgets", 42, "af:fix")
		for i, want := range [][]int{{}, {}, {42}, {42}} {
			if got := list(t, h, "widgets", "state=open&labels=af%3Afix", pat); !slices.Equal(got.Numbers, want) {
				t.Errorf("list %d after the addition: %v, want %v", i+1, got.Numbers, want)
			}
		}
		if issue, _ := h.IssueOf("widgets", 42); !slices.Equal(issue.Labels, []string{"af:fix"}) {
			t.Errorf("the issue carries %v", issue.Labels)
		}

		// A page after the first is not a list of its own, and a list of another label does not count.
		for n := 100; n < 250; n++ {
			h.AddIssue("widgets", Issue{Number: n, Labels: []string{"bug"}})
		}
		h.SetAdditionLag("widgets", 1)
		h.RemoveLabel("widgets", 42, "af:fix")
		h.AddLabel("widgets", 42, "af:fix")
		list(t, h, "widgets", "labels=bug&limit=100", pat)
		if got := list(t, h, "widgets", "labels=af%3Afix", pat); len(got.Numbers) != 0 {
			t.Errorf("a list of another label ended the lag: %v", got.Numbers)
		}
		list(t, h, "widgets", "labels=af%3Afix&page=2", pat)
		if got := list(t, h, "widgets", "labels=af%3Afix", pat); !slices.Equal(got.Numbers, []int{42}) {
			t.Errorf("after the list that counted, the list is %v, want the issue", got.Numbers)
		}
	})

	t.Run("an issue created with labels", func(t *testing.T) {
		h := listHub(t)
		h.SetAdditionLag("widgets", 5)
		h.AddIssue("widgets", Issue{Number: 1, Labels: []string{"af:fix"}})
		if got := list(t, h, "widgets", "labels=af%3Afix", pat); !slices.Equal(got.Numbers, []int{1}) {
			t.Errorf("an issue that was added with its labels is listed %v at once, want [1]", got.Numbers)
		}
	})
}
