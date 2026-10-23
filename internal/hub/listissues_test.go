package hub

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// openFix is the filter of the poll: the open issues that carry one label, at most limit of them.
func openFix(limit int) issuex.IssueFilter {
	return issuex.IssueFilter{State: "open", Labels: []string{"af:fix"}, Limit: limit}
}

// numbersOf are the numbers of the issues, in order.
func numbersOf(list issuex.IssueList) []int {
	out := []int{}
	for _, i := range list.Issues {
		out = append(out, i.Number)
	}
	return out
}

// addFix adds count open issues numbered from first, each carrying af:fix, to the workspace acme.
func addFix(e *forgeEnv, first, count int) {
	for n := first; n < first+count; n++ {
		e.hub.AddIssue("acme", fakehub.Issue{Number: n, Title: fmt.Sprintf("issue %d", n), Labels: []string{"af:fix"}})
	}
}

// listRequests are the requests for a list of the issues of acme that were made so far.
func listRequests(e *forgeEnv) []fakehub.Request { return e.hub.ListRequests("acme") }

// TestListIssuesSendsTheOpenFilterWithAnEscapedLabelTheBearerPATAndNoSort_TS_15_15 verifies TS-15-15: one request
// for the open issues of the label, with the label query-escaped, the PAT as the bearer token, a page of 100 and no
// sort, assignee or direction; the issues come back in the order hub gave them.
// Verifies: 15-REQ-3.1
func TestListIssuesSendsTheOpenFilterWithAnEscapedLabelTheBearerPATAndNoSort_TS_15_15(t *testing.T) {
	e := newForgeEnv(t)
	addFix(e, 5, 1)
	addFix(e, 9, 1)
	addFix(e, 7, 1)
	e.hub.SetListOrder("acme", 9, 5, 7)
	before := e.hub.Total()

	list, err := e.forge.ListIssues(ctx, e.repo, openFix(100))

	if err != nil {
		t.Fatal(err)
	}
	got := e.hub.Requests()[before:]
	if len(got) != 1 {
		t.Fatalf("%d requests, want one: %+v", len(got), got)
	}
	r := got[0]
	if r.Method != "GET" || r.Path != "/api/v1/workspaces/acme/issues" {
		t.Errorf("the request is %s %s", r.Method, r.Path)
	}
	if !strings.HasSuffix(r.URI, "/issues?state=open&labels=af%3Afix&limit=100") {
		t.Errorf("the request URI is %s, want the state, the escaped label and the page size of 100", r.URI)
	}
	for _, name := range []string{"sort=", "direction=", "assignee=", "page="} {
		if strings.Contains(r.URI, name) {
			t.Errorf("the request URI %s carries %s", r.URI, name)
		}
	}
	if r.TokenHash != fakehub.TokenHash(workerPAT) || r.Header.Get("Authorization") != "Bearer "+workerPAT {
		t.Errorf("the request is not made with the PAT as the bearer token: %v", r.Header)
	}
	if !slices.Equal(numbersOf(list), []int{9, 5, 7}) || list.Incomplete {
		t.Errorf("the list is %v (incomplete %v), want 9, 5, 7 in hub's order and complete", numbersOf(list), list.Incomplete)
	}
	if list.Issues[0].Labels[0] != "af:fix" || list.Issues[0].Title != "issue 9" {
		t.Errorf("the issue is %+v, want its title and labels read", list.Issues[0])
	}
}

// TestListIssuesFollowsFurtherPagesAndIsIncompleteOnlyWhenItStoppedAtTheLimitWithMoreToRead_TS_15_16 verifies
// TS-15-16: the pages of 100 are followed until Limit issues are held or hub reports no further page; the list is
// incomplete when the client stopped at Limit while hub reported a further page; and, as real hub reports no further
// page at all and cannot say that it cut a list short, a last page as long as the page asked for is incomplete too.
// Verifies: 15-REQ-3.2, 15-REQ-3.3
func TestListIssuesFollowsFurtherPagesAndIsIncompleteOnlyWhenItStoppedAtTheLimitWithMoreToRead_TS_15_16(t *testing.T) {
	e := newForgeEnv(t)
	addFix(e, 1000, 250)

	for _, c := range []struct {
		limit, want, pages int
		incomplete         bool
	}{
		{limit: 100, want: 100, pages: 1, incomplete: true},
		{limit: 250, want: 250, pages: 3, incomplete: false},
		{limit: 300, want: 250, pages: 3, incomplete: false},
		{limit: 150, want: 150, pages: 2, incomplete: true},
	} {
		before := len(listRequests(e))
		list, err := e.forge.ListIssues(ctx, e.repo, openFix(c.limit))
		if err != nil {
			t.Fatalf("limit %d: %v", c.limit, err)
		}
		if len(list.Issues) != c.want || list.Incomplete != c.incomplete {
			t.Errorf("limit %d: %d issues, incomplete %v; want %d and %v", c.limit, len(list.Issues), list.Incomplete, c.want, c.incomplete)
		}
		made := listRequests(e)[before:]
		if len(made) != c.pages {
			t.Errorf("limit %d: %d requests, want %d", c.limit, len(made), c.pages)
		}
		for i, r := range made {
			if want := i > 0; strings.Contains(r.URI, "&page=") != want {
				t.Errorf("limit %d: request %d is %s", c.limit, i+1, r.URI)
			}
			if i > 0 && !strings.HasSuffix(r.URI, fmt.Sprintf("&page=%d", i+1)) {
				t.Errorf("limit %d: request %d is %s, want page %d", c.limit, i+1, r.URI, i+1)
			}
		}
		seen := map[int]bool{}
		for _, i := range list.Issues {
			if seen[i.Number] {
				t.Errorf("limit %d: issue %d is listed twice", c.limit, i.Number)
			}
			seen[i.Number] = true
		}
	}

	// Hub that says nothing of a further page: the page it answered is the last.
	e.hub.SetPageLinks("acme", false)
	list, err := e.forge.ListIssues(ctx, e.repo, openFix(250))
	if err != nil || len(list.Issues) != 100 || !list.Incomplete {
		t.Errorf("a first page of 100 that says nothing of a further page: %d issues, incomplete %v, %v; want 100 and incomplete, as hub cannot say it cut the list", len(list.Issues), list.Incomplete, err)
	}

	short := newForgeEnv(t)
	addFix(short, 1, 40)
	short.hub.SetPageLinks("acme", false)
	list, err = short.forge.ListIssues(ctx, short.repo, openFix(100))
	if err != nil || len(list.Issues) != 40 || list.Incomplete {
		t.Errorf("a page shorter than the page asked for that says nothing of a further page: %d issues, incomplete %v, %v; want 40 and complete", len(list.Issues), list.Incomplete, err)
	}
}

// TestListIssuesLeavesOutWhatHubMarksAsAPullOrMergeRequest_TS_15_17 verifies TS-15-17.
// Verifies: 15-REQ-3.4
func TestListIssuesLeavesOutWhatHubMarksAsAPullOrMergeRequest_TS_15_17(t *testing.T) {
	e := newForgeEnv(t)
	e.hub.AddIssue("acme", fakehub.Issue{Number: 5, Title: "an issue", Labels: []string{"af:fix"}})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 6, Title: "a pull request", Labels: []string{"af:fix"}, IsPR: true})
	e.hub.AddIssue("acme", fakehub.Issue{Number: 7, Title: "a merge request", Labels: []string{"af:fix"}, IsPR: true})

	list, err := e.forge.ListIssues(ctx, e.repo, openFix(100))

	if err != nil || !slices.Equal(numbersOf(list), []int{5}) || list.Incomplete {
		t.Errorf("the list is %v (incomplete %v), %v; want the issue 5 only", numbersOf(list), list.Incomplete, err)
	}
}

// TestListIssuesWithAnyOtherFilterSendsNothingAndIsNotAvailable_TS_15_18 verifies TS-15-18.
// Verifies: 15-REQ-3.5
func TestListIssuesWithAnyOtherFilterSendsNothingAndIsNotAvailable_TS_15_18(t *testing.T) {
	e := newForgeEnv(t)
	for name, filter := range map[string]issuex.IssueFilter{
		"an assignee":        {State: "open", Labels: []string{"af:fix"}, Assignee: "bob"},
		"a sort":             {State: "open", Labels: []string{"af:fix"}, Sort: "created"},
		"a direction":        {State: "open", Labels: []string{"af:fix"}, Direction: "asc"},
		"state closed":       {State: "closed", Labels: []string{"af:fix"}},
		"no state":           {Labels: []string{"af:fix"}},
		"no label":           {State: "open"},
		"two labels":         {State: "open", Labels: []string{"af:fix", "bug"}},
		"an empty label":     {State: "open", Labels: []string{""}},
		"the zero filter":    {},
		"a limit and no one": {Limit: 10},
	} {
		before := e.hub.Total()
		_, err := e.forge.ListIssues(ctx, e.repo, filter)
		if err == nil || !strings.Contains(err.Error(), "not available through hub") {
			t.Errorf("%s: %v, want the not available through hub error", name, err)
		}
		if e.hub.Total() != before {
			t.Errorf("%s: %d requests were sent", name, e.hub.Total()-before)
		}
	}
	// Another repository than the workspace's is refused before a request, as for the other methods.
	before := e.hub.Total()
	if _, err := e.forge.ListIssues(ctx, issuex.Repo{Host: forgeHost, Owner: "acme", Name: "other"}, openFix(100)); err == nil || e.hub.Total() != before {
		t.Errorf("another repository: %v, %d requests", err, e.hub.Total()-before)
	}
}

// TestListIssuesMapsTheErrorsOfAListOntoTheIssuexPredicates_TS_15_19 verifies TS-15-19.
// Verifies: 15-REQ-3.6
func TestListIssuesMapsTheErrorsOfAListOntoTheIssuexPredicates_TS_15_19(t *testing.T) {
	e := newForgeEnv(t)
	route := "/api/v1/workspaces/acme/issues"
	inject := func(f fakehub.Fault) error {
		e.hub.Clear()
		e.hub.Inject("GET", route, f)
		_, err := e.forge.ListIssues(ctx, e.repo, openFix(100))
		return err
	}
	for _, status := range []int{401, 403} {
		var he *issuex.HTTPError
		if err := inject(fakehub.Fault{Status: status}); !errors.As(err, &he) || he.Status != status {
			t.Errorf("%d: %v, want an *issuex.HTTPError of that status", status, err)
		}
	}
	if err := inject(fakehub.Fault{Status: 404}); !issuex.IsNotFound(err) {
		t.Errorf("404: %v, want issuex's not found", err)
	}
	if err := inject(fakehub.Fault{Status: 409}); !errors.Is(err, issuex.ErrConflict) {
		t.Errorf("409: %v, want issuex's conflict", err)
	}
	var he *issuex.HTTPError
	if err := inject(fakehub.Fault{Status: 422, Body: `{"error":{"code":422,"message":"labels is not valid","error_type":"forge_rejected"}}`}); !errors.As(err, &he) || he.Status != 422 || he.Message != "labels is not valid" {
		t.Errorf("422: %v, want a rejection carrying hub's message", err)
	}
	if err := inject(fakehub.Fault{Status: 429}); !issuex.IsRateLimited(err) {
		t.Errorf("429: %v, want issuex's rate limited", err)
	}
	for name, f := range map[string]fakehub.Fault{
		"503":                       {Status: 503},
		"a dropped connection":      {Hangup: true},
		"a body that is not JSON":   {Status: 200, Body: "<html>not json</html>"},
		"a body that is not a list": {Status: 200, Body: `{"issues": []}`},
		"a body that is null":       {Status: 200, Body: `null`},
	} {
		err := inject(f)
		var unavailable *UnavailableError
		if !errors.As(err, &unavailable) || errors.As(err, &he) || issuex.IsNotFound(err) || issuex.IsRateLimited(err) || errors.Is(err, issuex.ErrConflict) {
			t.Errorf("%s: %v, want a retryable error that is none of the typed ones", name, err)
		}
	}
	short, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	e.hub.Clear()
	e.hub.Inject("GET", route, fakehub.Fault{Delay: time.Second})
	_, err := e.forge.ListIssues(short, e.repo, openFix(100))
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a timeout: %v, want a retryable error that is the error of the context", err)
	}

	// A page after the first that fails is an error of the list, not a short list.
	e.hub.Clear()
	addFix(e, 2000, 150)
	e.hub.Inject("GET", route, fakehub.Fault{Status: 503, Skip: 1})
	if list, err := e.forge.ListIssues(ctx, e.repo, openFix(250)); err == nil {
		t.Errorf("the second page failed, and the list is %d issues without an error", len(list.Issues))
	}
}

// TestHubsNamesForTheIssuesListStayInsideInternalHubAndListIssuesIsAvailable_TS_15_20 verifies TS-15-20: no
// Go file outside internal/hub names hub's route, its page or its size parameter; ListIssues is not among the
// methods that send nothing; and the other methods that are not available still send nothing.
// Verifies: 15-REQ-3.7
func TestHubsNamesForTheIssuesListStayInsideInternalHubAndListIssuesIsAvailable_TS_15_20(t *testing.T) {
	root := filepath.Join("..", "..")
	names := []string{"/issues?state=", "&page=", "&limit=100"}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case d.IsDir() && (d.Name() == ".git" || d.Name() == ".specs"):
			return filepath.SkipDir
		case d.IsDir() || filepath.Ext(path) != ".go" || strings.HasPrefix(filepath.ToSlash(rel), "internal/hub/"):
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range names {
			if strings.Contains(string(src), name) {
				t.Errorf("%s names %q, which belongs to hub's issues list and stays inside internal/hub", rel, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	e := newForgeEnv(t)
	before := e.hub.Total()
	if _, err := e.forge.ListIssues(ctx, e.repo, openFix(100)); err != nil || e.hub.Total() != before+1 {
		t.Errorf("a list of the open issues of a label: %v, %d requests, want it sent", err, e.hub.Total()-before)
	}
	ref := e.ref(42)
	before = e.hub.Total()
	errs := map[string]error{"CreateLabel": e.forge.CreateLabel(ctx, e.repo, issuex.Label{Name: "x"}), "CloseIssue": e.forge.CloseIssue(ctx, ref, "bye")}
	_, errs["ReadPullRequest"] = e.forge.ReadPullRequest(ctx, ref)
	for name, err := range errs {
		if err == nil || !strings.Contains(err.Error(), name+" is not available through hub in this release") {
			t.Errorf("%s: %v, want it not available through hub", name, err)
		}
	}
	if e.hub.Total() != before {
		t.Errorf("%d requests were sent by methods that are not available", e.hub.Total()-before)
	}
}
