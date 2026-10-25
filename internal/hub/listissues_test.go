package hub

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// openIssues is the filter of the label poll: the open issues of one label, at most limit of them.
func openIssues(label string, limit int) issuex.IssueFilter {
	return issuex.IssueFilter{State: "open", Labels: []string{label}, Limit: limit}
}

func numbers(list issuex.IssueList) []int {
	out := []int{}
	for _, i := range list.Issues {
		out = append(out, i.Number)
	}
	return out
}

// addFix adds count open issues numbered from first, each with the label af:fix.
func addFix(e *forgeEnv, first, count int) {
	for n := first; n < first+count; n++ {
		e.hub.AddIssue("acme", fakehub.Issue{Number: n, Title: fmt.Sprintf("issue %d", n), Labels: []string{"af:fix"}})
	}
}

func TestListIssues_ReadsTheOpenIssuesOfTheLabelInHubsOrder(t *testing.T) {
	e := newForgeEnv(t)
	addFix(e, 5, 1)
	addFix(e, 9, 1)
	addFix(e, 7, 1)
	e.hub.SetListOrder("acme", 9, 5, 7)

	list, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 0))

	if err != nil || !slices.Equal(numbers(list), []int{9, 5, 7}) || list.Incomplete || list.Issues[0].Title != "issue 9" || list.Issues[0].Labels[0] != "af:fix" {
		t.Errorf("the list is %+v, %v; want 9, 5, 7 in hub's order, read whole", list, err)
	}
	if reqs := e.hub.ListRequests("acme"); len(reqs) != 1 || reqs[0].URI != "/api/v1/workspaces/acme/issues?state=open&labels=af%3Afix&limit=100" {
		t.Errorf("the requests are %+v, want one for the open issues of the escaped label, 100 a page", reqs)
	}
}

// The list is incomplete when it stopped at the limit with more to read, and when hub's last page
// is as long as a page, since hub cannot say that it cut the list short.
func TestListIssues_FollowsThePagesUpToTheLimit(t *testing.T) {
	e := newForgeEnv(t)
	addFix(e, 1000, 250)
	for _, c := range []struct {
		limit, want, pages int
		incomplete         bool
	}{
		{limit: 100, want: 100, pages: 1, incomplete: true},
		{limit: 150, want: 150, pages: 2, incomplete: true},
		{limit: 250, want: 250, pages: 3, incomplete: false},
		{limit: 300, want: 250, pages: 3, incomplete: false},
	} {
		before := len(e.hub.ListRequests("acme"))
		list, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", c.limit))
		made := e.hub.ListRequests("acme")[before:]
		if err != nil || len(list.Issues) != c.want || list.Incomplete != c.incomplete || len(made) != c.pages {
			t.Errorf("limit %d: %d issues, incomplete %v, %d pages, %v; want %d, %v, %d", c.limit, len(list.Issues), list.Incomplete, len(made), err, c.want, c.incomplete, c.pages)
		}
		for i, r := range made {
			if page := fmt.Sprintf("&page=%d", i+1); (i > 0) != strings.HasSuffix(r.URI, page) {
				t.Errorf("limit %d: request %d is %s", c.limit, i+1, r.URI)
			}
		}
		if len(slices.Compact(slices.Sorted(slices.Values(numbers(list))))) != len(list.Issues) {
			t.Errorf("limit %d: an issue is listed twice", c.limit)
		}
	}

	e.hub.SetPageLinks("acme", false)
	if list, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 250)); err != nil || len(list.Issues) != 100 || !list.Incomplete {
		t.Errorf("a full page that names no next one: %d issues, incomplete %v, %v; want 100 and incomplete", len(list.Issues), list.Incomplete, err)
	}
	short := newForgeEnv(t)
	addFix(short, 1, 40)
	short.hub.SetPageLinks("acme", false)
	if list, err := short.forge.ListIssues(ctx, short.repo, openIssues("af:fix", 0)); err != nil || len(list.Issues) != 40 || list.Incomplete {
		t.Errorf("a short page that names no next one: %d issues, incomplete %v, %v; want 40 and complete", len(list.Issues), list.Incomplete, err)
	}
}

func TestListIssues_LeavesOutPullRequests(t *testing.T) {
	e := newForgeEnv(t)
	e.hub.AddIssue("acme", fakehub.Issue{Number: 5, Labels: []string{"af:fix"}})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 6, Labels: []string{"af:fix"}, IsPR: true})
	if list, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 0)); err != nil || !slices.Equal(numbers(list), []int{5}) {
		t.Errorf("the list is %v, %v; want the issue 5 only", numbers(list), err)
	}
}

func TestListIssues_AnyOtherFilterSendsNothingAndIsNotAvailable(t *testing.T) {
	e := newForgeEnv(t)
	for name, filter := range map[string]issuex.IssueFilter{
		"an assignee":  {State: "open", Labels: []string{"af:fix"}, Assignee: "bob"},
		"a sort":       {State: "open", Labels: []string{"af:fix"}, Sort: "created"},
		"a direction":  {State: "open", Labels: []string{"af:fix"}, Direction: "asc"},
		"closed":       {State: "closed", Labels: []string{"af:fix"}},
		"no state":     {Labels: []string{"af:fix"}},
		"no label":     {State: "open"},
		"two labels":   {State: "open", Labels: []string{"af:fix", "bug"}},
		"empty label":  {State: "open", Labels: []string{""}},
		"a limit only": {Limit: 10},
	} {
		if _, err := e.forge.ListIssues(ctx, e.repo, filter); err == nil || err.Error() != "ListIssues with this filter is not available through hub in this release" {
			t.Errorf("%s: %v, want not available", name, err)
		}
	}
	if n := e.hub.Total(); n != 0 {
		t.Errorf("%d requests were sent", n)
	}
}

func TestListIssues_AnAnswerThatIsNoListOrAPageThatFailsIsAnError(t *testing.T) {
	e := newForgeEnv(t)
	for _, body := range []string{`{"issues":[]}`, `null`, `<html>`} {
		e.answer("GET", e.path("/issues"), fakehub.Fault{Status: 200, Body: body})
		if _, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 0)); !isUnavailable(err) {
			t.Errorf("%s: %v, want unavailable", body, err)
		}
	}
	addFix(e, 2000, 150)
	e.answer("GET", e.path("/issues"), fakehub.Fault{Status: 503, Skip: 1})
	if list, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 250)); err == nil {
		t.Errorf("the second page failed, and the list is %d issues without an error", len(list.Issues))
	}
}

// A hundred issues with long bodies are more than the 1 MiB that the other calls read: a list that
// was refused for its size would never get shorter, since its labels are only taken off once listed.
func TestListIssues_ReadsALongerAnswerThanTheOtherCalls(t *testing.T) {
	e := newForgeEnv(t)
	body := strings.Repeat("x", 12<<10)
	for n := 1000; n < 1100; n++ {
		e.hub.AddIssue("acme", fakehub.Issue{Number: n, Body: body, Labels: []string{"af:fix"}})
	}
	if list, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 0)); err != nil || len(list.Issues) != 100 || list.Issues[99].Body != body {
		t.Fatalf("%d issues, %v; want the hundred with their bodies", len(list.Issues), err)
	}
	e.answer("GET", e.path("/issues"), fakehub.Fault{Oversize: maxListBody + 1})
	if _, err := e.forge.ListIssues(ctx, e.repo, openIssues("af:fix", 0)); !isUnavailable(err) {
		t.Errorf("an answer longer than any list: %v, want unavailable", err)
	}
	e.answer("GET", e.path("/issues/42"), fakehub.Fault{Oversize: 2 << 20})
	if _, err := e.forge.ReadIssue(ctx, e.ref(42)); !isUnavailable(err) {
		t.Errorf("an issue of 2 MiB: %v, want unavailable", err)
	}
}

func TestNextPage_IsALinkWhoseRelationIsNext(t *testing.T) {
	for _, c := range []struct {
		links []string
		want  bool
	}{
		{nil, false},
		{[]string{`</issues?page=2>; rel="next"`}, true},
		{[]string{`</issues?page=1>; rel="prev"`}, false},
		{[]string{`</issues?page=1>; rel="prev", </issues?page=3>; rel = "next"`}, true},
		{[]string{`</issues?page=1>; rel="prev"`, `</issues?page=3>;rel="next"`}, true},
		{[]string{`rel="next"`}, false},
	} {
		if got := nextPage(http.Header{"Link": c.links}); got != c.want {
			t.Errorf("Link %q: %v, want %v", c.links, got, c.want)
		}
	}
}
