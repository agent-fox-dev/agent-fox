package issuex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// pagedServer serves pages (JSON arrays) in order, one per ?page=N, and
// answers an unknown page with []. announceNext is called for every response
// with the next page number when one exists, and is where a test says how its
// forge signals "there is another page": GitHub's Link header, GitLab's
// X-Next-Page. A server whose last page is exactly full signals nothing.
func pagedServer(t *testing.T, pages []string, announceNext func(w http.ResponseWriter, r *http.Request, next int)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 {
			page = 1
		}
		if page < len(pages) {
			announceNext(w, r, page+1)
		}
		if page > len(pages) {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(pages[page-1]))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func githubNext(w http.ResponseWriter, r *http.Request, next int) {
	w.Header().Set("Link", fmt.Sprintf(`<http://%s%s?page=%d>; rel="next", <http://%s%s?page=99>; rel="last"`, r.Host, r.URL.Path, next, r.Host, r.URL.Path))
}

func gitlabNext(w http.ResponseWriter, _ *http.Request, next int) {
	w.Header().Set("X-Next-Page", strconv.Itoa(next))
}

// jsonItems renders n objects of the form format, which takes the object's
// running number, numbered from first.
func jsonItems(first, n int, format string) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(format, first+i)
	}
	return "[" + strings.Join(items, ",") + "]"
}

const (
	ghIssueJSON   = `{"number": %d, "title": "issue"}`
	ghPullJSON    = `{"number": %d, "title": "pull", "pull_request": {}}`
	ghCommentJSON = `{"body": "comment %d"}`
	glIssueJSON   = `{"iid": %d, "title": "issue", "state": "opened"}`
)

func glNotes(first, n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"id": %d, "body": "note", "system": false}`, first+i)
	}
	return "[" + strings.Join(items, ",") + "]"
}

// TestListIssues_IncompleteMeansMoreRemain verifies 02-REQ-3.8 and 03-REQ-3.8:
// Incomplete is set when results reach the limit while further issues remain,
// not when the last page happens to hold exactly a page of them.
func TestListIssues_IncompleteMeansMoreRemain(t *testing.T) {
	cases := []struct {
		name           string
		forge          string
		pages          []string
		limit          int
		wantIssues     int
		wantIncomplete bool
	}{
		{"github: last page exactly the limit", "github", []string{jsonItems(1, 100, ghIssueJSON)}, 100, 100, false},
		{"github: limit reached, a further page has an issue", "github", []string{jsonItems(1, 100, ghIssueJSON), jsonItems(101, 1, ghIssueJSON)}, 100, 100, true},
		{"github: limit reached, further pages hold only pull requests", "github", []string{jsonItems(1, 100, ghIssueJSON), jsonItems(101, 3, ghPullJSON)}, 100, 100, false},
		{"github: limit reached mid page", "github", []string{jsonItems(1, 100, ghIssueJSON)}, 50, 50, true},
		{"github: under the limit across pages", "github", []string{jsonItems(1, 100, ghIssueJSON), jsonItems(101, 20, ghIssueJSON)}, 500, 120, false},
		{"gitlab: last page exactly the limit", "gitlab", []string{jsonItems(1, 100, glIssueJSON)}, 100, 100, false},
		{"gitlab: limit reached, a further page exists", "gitlab", []string{jsonItems(1, 100, glIssueJSON), jsonItems(101, 1, glIssueJSON)}, 100, 100, true},
		{"gitlab: limit reached mid page", "gitlab", []string{jsonItems(1, 100, glIssueJSON)}, 50, 50, true},
		{"gitlab: under the limit across pages", "gitlab", []string{jsonItems(1, 100, glIssueJSON), jsonItems(101, 20, glIssueJSON)}, 500, 120, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				list IssueList
				err  error
			)
			repo := Repo{Owner: "org", Name: "repo"}
			filter := IssueFilter{Limit: tc.limit}
			if tc.forge == "github" {
				srv := pagedServer(t, tc.pages, githubNext)
				list, err = newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"}).ListIssues(context.Background(), repo, filter)
			} else {
				srv := pagedServer(t, tc.pages, gitlabNext)
				c, cErr := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
				if cErr != nil {
					t.Fatalf("NewGitLab failed: %v", cErr)
				}
				list, err = c.ListIssues(context.Background(), repo, filter)
			}
			if err != nil {
				t.Fatalf("ListIssues failed: %v", err)
			}
			if len(list.Issues) != tc.wantIssues {
				t.Errorf("got %d issues, want %d", len(list.Issues), tc.wantIssues)
			}
			if list.Incomplete != tc.wantIncomplete {
				t.Errorf("Incomplete = %v, want %v", list.Incomplete, tc.wantIncomplete)
			}
		})
	}
}

// TestListComments_TruncatedMeansMoreRemain verifies 02-REQ-4.2 and 03-REQ-4.2:
// Truncated is set when the 500-comment cap is hit and further comments exist
// on the server, not when the 500th comment is the last one.
func TestListComments_TruncatedMeansMoreRemain(t *testing.T) {
	five := func(format string) []string {
		pages := make([]string, 5)
		for i := range pages {
			pages[i] = jsonItems(i*100+1, 100, format)
		}
		return pages
	}
	cases := []struct {
		name          string
		forge         string
		pages         []string
		wantComments  int
		wantTruncated bool
	}{
		{"github: exactly 500 comments", "github", five(ghCommentJSON), 500, false},
		{"github: more than 500 comments", "github", append(five(ghCommentJSON), jsonItems(501, 1, ghCommentJSON)), 500, true},
		{"github: under the cap across pages", "github", []string{jsonItems(1, 100, ghCommentJSON), jsonItems(101, 30, ghCommentJSON)}, 130, false},
		{"gitlab: exactly 500 notes", "gitlab", []string{glNotes(1, 100), glNotes(101, 100), glNotes(201, 100), glNotes(301, 100), glNotes(401, 100)}, 500, false},
		{"gitlab: more than 500 notes", "gitlab", []string{glNotes(1, 100), glNotes(101, 100), glNotes(201, 100), glNotes(301, 100), glNotes(401, 100), glNotes(501, 1)}, 500, true},
		{"gitlab: under the cap across pages", "gitlab", []string{glNotes(1, 100), glNotes(101, 30)}, 130, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				list CommentList
				err  error
			)
			ref := IssueRef{Repo: Repo{Owner: "org", Name: "repo"}, Number: 1}
			if tc.forge == "github" {
				srv := pagedServer(t, tc.pages, githubNext)
				list, err = newTestGitHubClient(Options{BaseURL: srv.URL, Token: "tok"}).ListComments(context.Background(), ref)
			} else {
				srv := pagedServer(t, tc.pages, gitlabNext)
				c, cErr := NewGitLab(Options{BaseURL: srv.URL, Token: "tok"})
				if cErr != nil {
					t.Fatalf("NewGitLab failed: %v", cErr)
				}
				list, err = c.ListComments(context.Background(), ref)
			}
			if err != nil {
				t.Fatalf("ListComments failed: %v", err)
			}
			if len(list.Comments) != tc.wantComments {
				t.Errorf("got %d comments, want %d", len(list.Comments), tc.wantComments)
			}
			if list.Truncated != tc.wantTruncated {
				t.Errorf("Truncated = %v, want %v", list.Truncated, tc.wantTruncated)
			}
		})
	}
}

func TestHasNextPage(t *testing.T) {
	cases := []struct {
		name  string
		links []string
		want  bool
	}{
		{"no header", nil, false},
		{"next and last", []string{`<https://api.github.com/r?page=2>; rel="next", <https://api.github.com/r?page=9>; rel="last"`}, true},
		{"last page: prev and first", []string{`<https://api.github.com/r?page=8>; rel="prev", <https://api.github.com/r?page=1>; rel="first"`}, false},
		{"next in a second header line", []string{`<https://api.github.com/r?page=1>; rel="prev"`, `<https://api.github.com/r?page=3>; rel="next"`}, true},
		{"unquoted rel", []string{`<https://api.github.com/r?page=2>; rel=next`}, true},
		{"next is not a substring match", []string{`<https://api.github.com/r?page=2>; rel="nextish"`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{"Link": tc.links}}
			if got := hasNextPage(resp); got != tc.want {
				t.Errorf("hasNextPage = %v, want %v", got, tc.want)
			}
		})
	}
	if hasNextPage(nil) {
		t.Error("hasNextPage(nil) = true, want false")
	}
}
