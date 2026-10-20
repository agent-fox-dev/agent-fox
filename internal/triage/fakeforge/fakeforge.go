// Package fakeforge is a forge for tests: an httptest server that speaks the
// part of the GitHub or GitLab API a triage run uses (the repository, an issue
// with its comments, and the writes), keeps what was written, and records every
// request, so that a test can say exactly what a run sent. It is used by the
// tests of internal/triage and cmd/af, and by nothing else.
package fakeforge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"
)

// The kinds of forge.
const (
	GitHub = "github"
	GitLab = "gitlab"
)

// The operations a request is one of. The first three only read.
const (
	OpRepo     = "repo"
	OpRead     = "read"
	OpComments = "comments"
	OpBody     = "body"
	OpLabels   = "labels"
	OpComment  = "comment"
	OpCreate   = "create"
)

// Comment is a comment on an issue.
type Comment struct {
	Author  string
	Body    string
	Created time.Time
}

// Issue is an issue of the forge.
type Issue struct {
	Number   int
	Title    string
	Body     string
	State    string // open or closed
	Author   string
	Labels   []string
	Created  time.Time
	Comments []Comment
}

// Request is a request the forge received.
type Request struct {
	Method string
	Path   string
	Op     string
	// JSON is the body of a write, decoded; Raw is it as it was sent.
	JSON map[string]any
	Raw  string
}

// Failure is how an operation fails: with a status and a message, or by hanging
// up.
type Failure struct {
	Status  int
	Message string
	Hangup  bool
}

// Forge is a fake forge.
type Forge struct {
	// URL is the base of the server.
	URL string
	// Kind is GitHub or GitLab, and Repo the repository it serves, owner/name.
	Kind string
	Repo string
	// Archived is what the repository answers.
	Archived bool

	srv      *httptest.Server
	mu       sync.Mutex
	issues   map[int]*Issue
	next     int
	requests []Request
	fail     map[string]Failure
}

// New starts a forge of the kind that serves the repository, and stops it when
// the test ends.
func New(t testing.TB, kind, repo string) *Forge {
	t.Helper()
	f := &Forge{Kind: kind, Repo: repo, issues: map[int]*Issue{}, next: 100, fail: map[string]Failure{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	f.URL = f.srv.URL
	t.Cleanup(f.srv.Close)
	return f
}

// AddIssue puts an issue on the forge. A zero Created is a fixed day.
func (f *Forge) AddIssue(i Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i.State == "" {
		i.State = "open"
	}
	if i.Created.IsZero() {
		i.Created = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	}
	if i.Author == "" {
		i.Author = "alice"
	}
	f.issues[i.Number] = &i
}

// Issue is the issue as it is now.
func (f *Forge) Issue(number int) Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.issues[number]
	if !ok {
		return Issue{}
	}
	cp := *i
	cp.Labels = append([]string(nil), i.Labels...)
	cp.Comments = append([]Comment(nil), i.Comments...)
	return cp
}

// Fail makes every request of the operation fail from now on.
func (f *Forge) Fail(op string, fl Failure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[op] = fl
}

// Requests are all the requests received, in order.
func (f *Forge) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.requests...)
}

// Writes are the requests that change something.
func (f *Forge) Writes() []Request {
	var out []Request
	for _, r := range f.Requests() {
		switch r.Op {
		case OpBody, OpLabels, OpComment, OpCreate:
			out = append(out, r)
		}
	}
	return out
}

// Count is the number of requests of the operation.
func (f *Forge) Count(op string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.Op == op {
			n++
		}
	}
	return n
}

// NewForge builds the client of the shell's App.NewForge for this forge, with a
// token, or none when the token is empty.
func (f *Forge) NewForge(token string) func(issuex.Options) (issuex.Client, error) {
	return func(o issuex.Options) (issuex.Client, error) {
		o.BaseURL, o.Token = f.URL, token
		if f.Kind == GitLab {
			return issuex.NewGitLab(o)
		}
		return issuex.NewGitHub(o)
	}
}

// IssueURL is the URL of an issue as a person types it, on a host the issuex
// parser knows.
func (f *Forge) IssueURL(number int) string {
	host := "github.com"
	if f.Kind == GitLab {
		host = "gitlab.com"
	}
	owner, name, _ := strings.Cut(f.Repo, "/")
	return issuex.IssueRef{Repo: issuex.Repo{Owner: owner, Name: name, Host: host}, Number: number}.URL()
}

var (
	githubPath = regexp.MustCompile(`^/repos/([^/]+/[^/]+)(?:/issues(?:/(\d+))?(/comments|/labels)?)?$`)
	gitlabPath = regexp.MustCompile(`^/api/v4/projects/([^/]+/[^/]+)(?:/(?:issues|merge_requests)(?:/(\d+))?(/notes)?)?$`)
)

// serve answers a request.
func (f *Forge) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	op, number := f.classify(r, raw)
	req := Request{Method: r.Method, Path: r.URL.Path, Op: op, Raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req.JSON)
	}
	f.mu.Lock()
	f.requests = append(f.requests, req)
	fl, failing := f.fail[op]
	f.mu.Unlock()

	if failing {
		if fl.Hangup {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		w.WriteHeader(fl.Status)
		_, _ = fmt.Fprintf(w, `{"message":%q}`, fl.Message)
		return
	}

	switch op {
	case OpRepo:
		f.writeRepo(w)
	case OpRead:
		f.writeIssue(w, number, http.StatusOK)
	case OpComments:
		f.writeComments(w, number)
	case OpBody:
		f.mu.Lock()
		if i, ok := f.issues[number]; ok {
			if body, ok := req.JSON[f.bodyField()].(string); ok {
				i.Body = body
			}
		}
		f.mu.Unlock()
		f.writeIssue(w, number, http.StatusOK)
	case OpLabels:
		f.addLabels(number, req.JSON)
		if f.Kind == GitLab {
			f.writeIssue(w, number, http.StatusOK)
		} else {
			_, _ = fmt.Fprint(w, `[]`)
		}
	case OpComment:
		f.mu.Lock()
		if i, ok := f.issues[number]; ok {
			i.Comments = append(i.Comments, Comment{Author: "agent-fox", Body: fmt.Sprint(req.JSON["body"]), Created: time.Now()})
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"id":1,"html_url":%q,"body":"x"}`, fmt.Sprintf("%s/%s/issues/%d#comment-1", f.URL, f.Repo, number))
	case OpCreate:
		f.create(w, req.JSON)
	default:
		http.NotFound(w, r)
	}
}

func (f *Forge) bodyField() string {
	if f.Kind == GitLab {
		return "description"
	}
	return "body"
}

// classify says which operation a request is, and on which issue.
func (f *Forge) classify(r *http.Request, raw []byte) (op string, number int) {
	re := githubPath
	if f.Kind == GitLab {
		re = gitlabPath
	}
	m := re.FindStringSubmatch(r.URL.Path)
	if m == nil || m[1] != f.Repo {
		return "", 0
	}
	number, _ = strconv.Atoi(m[2])
	tail := m[3]
	isIssues := strings.Contains(r.URL.Path, "/issues")
	isThread := isIssues || strings.Contains(r.URL.Path, "/merge_requests") // a merge request is read as the issue of its number
	switch {
	case r.Method == http.MethodGet && !isThread:
		return OpRepo, 0
	case r.Method == http.MethodGet && tail == "":
		return OpRead, number
	case r.Method == http.MethodGet:
		return OpComments, number
	case r.Method == http.MethodPost && m[2] == "" && isIssues:
		return OpCreate, 0
	case r.Method == http.MethodPost && (tail == "/comments" || tail == "/notes"):
		return OpComment, number
	case r.Method == http.MethodPost && tail == "/labels":
		return OpLabels, number
	case (r.Method == http.MethodPatch || r.Method == http.MethodPut) && tail == "":
		if f.Kind == GitLab && strings.Contains(string(raw), `"add_labels"`) {
			return OpLabels, number
		}
		return OpBody, number
	}
	return "", 0
}

func (f *Forge) addLabels(number int, body map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i, ok := f.issues[number]
	if !ok {
		return
	}
	switch v := body["labels"].(type) {
	case []any:
		for _, l := range v {
			i.Labels = append(i.Labels, fmt.Sprint(l))
		}
	}
	if s, ok := body["add_labels"].(string); ok {
		i.Labels = append(i.Labels, strings.Split(s, ",")...)
	}
}

func (f *Forge) create(w http.ResponseWriter, body map[string]any) {
	f.mu.Lock()
	n := f.next
	f.next++
	i := &Issue{Number: n, Title: fmt.Sprint(body["title"]), Body: fmt.Sprint(body[f.bodyField()]), State: "open", Author: "agent-fox", Created: time.Now()}
	switch v := body["labels"].(type) {
	case []any:
		for _, l := range v {
			i.Labels = append(i.Labels, fmt.Sprint(l))
		}
	case string:
		i.Labels = strings.Split(v, ",")
	}
	f.issues[n] = i
	f.mu.Unlock()
	f.writeIssue(w, n, http.StatusCreated)
}

func (f *Forge) writeRepo(w http.ResponseWriter) {
	if f.Kind == GitLab {
		_, _ = fmt.Fprintf(w, `{"path_with_namespace":%q,"default_branch":"main","archived":%v,"visibility":"public"}`, f.Repo, f.Archived)
		return
	}
	_, _ = fmt.Fprintf(w, `{"full_name":%q,"default_branch":"main","archived":%v,"private":false,"permissions":{"push":true,"pull":true}}`, f.Repo, f.Archived)
}

func (f *Forge) writeIssue(w http.ResponseWriter, number, status int) {
	f.mu.Lock()
	i, ok := f.issues[number]
	var cp Issue
	if ok {
		cp = *i
	}
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"message":"Not Found"}`)
		return
	}
	var doc map[string]any
	url := fmt.Sprintf("%s/%s/issues/%d", f.URL, f.Repo, cp.Number)
	if f.Kind == GitLab {
		state := "opened"
		if cp.State == "closed" {
			state = "closed"
		}
		doc = map[string]any{"id": cp.Number, "iid": cp.Number, "title": cp.Title, "description": cp.Body, "state": state, "web_url": url,
			"author": map[string]any{"username": cp.Author}, "labels": append([]string{}, cp.Labels...), "created_at": cp.Created, "updated_at": cp.Created}
	} else {
		labels := []map[string]string{}
		for _, l := range cp.Labels {
			labels = append(labels, map[string]string{"name": l})
		}
		doc = map[string]any{"number": cp.Number, "title": cp.Title, "body": cp.Body, "state": cp.State, "html_url": url,
			"user": map[string]any{"login": cp.Author}, "labels": labels, "created_at": cp.Created, "updated_at": cp.Created}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(doc)
}

func (f *Forge) writeComments(w http.ResponseWriter, number int) {
	f.mu.Lock()
	var comments []Comment
	if i, ok := f.issues[number]; ok {
		comments = append(comments, i.Comments...)
	}
	f.mu.Unlock()
	out := []map[string]any{}
	for n, c := range comments {
		if f.Kind == GitLab {
			out = append(out, map[string]any{"id": n + 1, "body": c.Body, "author": map[string]any{"username": c.Author}, "created_at": c.Created, "system": false})
		} else {
			out = append(out, map[string]any{"id": n + 1, "body": c.Body, "user": map[string]any{"login": c.Author}, "created_at": c.Created,
				"html_url": fmt.Sprintf("%s/%s/issues/%d#comment-%d", f.URL, f.Repo, number, n+1)})
		}
	}
	_ = json.NewEncoder(w).Encode(out)
}
