package fakehub

import (
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
)

// GitUser is the user name of the Basic credential that the git server takes.
const GitUser = "agent-fox"

// GitRequest is a request the git server received.
type GitRequest struct {
	Seq           int
	Slug          string
	Method, Path  string
	Authorization string // the header as it was sent
	Status        int
}

// GitRequests are the requests the git server received, in order.
func (h *Hub) GitRequests() []GitRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]GitRequest(nil), h.gitLog...)
}

// statusRecorder remembers the status of an answer.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// serveGit serves /git/<slug>.git/… with git http-backend, to a request that carries
// Authorization: Basic with the user agent-fox and the PAT of the workspace, and refuses every
// other credential with 401. A fault that was injected for the request comes first.
func (h *Hub) serveGit(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.EscapedPath(), gitPrefix)
	name, _, _ := strings.Cut(rest, "/")
	slug, err := url.PathUnescape(strings.TrimSuffix(name, ".git"))
	if err != nil {
		slug = strings.TrimSuffix(name, ".git")
	}
	entry := GitRequest{Seq: h.next(), Slug: slug, Method: r.Method, Path: r.URL.Path, Authorization: r.Header.Get("Authorization")}
	rec := &statusRecorder{ResponseWriter: w}
	defer func() {
		entry.Status = rec.status
		h.mu.Lock()
		h.gitLog = append(h.gitLog, entry)
		h.mu.Unlock()
	}()

	// A fault is injected for a path of the repository, or for its root, which is every request
	// under it. It comes before the credential: a server that is down does not look at it.
	h.mu.Lock()
	ws := h.workspaces[slug]
	fault := h.take(r.Method, r.URL.Path, "")
	if fault == nil {
		fault = h.take(r.Method, gitPrefix+slug+".git", "")
	}
	h.mu.Unlock()
	if fault != nil {
		if fault.Delay > 0 {
			select {
			case <-time.After(fault.Delay):
			case <-r.Context().Done():
				return
			}
		}
		if applies(*fault) {
			answerFault(rec, *fault)
			return
		}
	}
	if ws == nil || ws.gitDir == "" || !basicIs(r, GitUser, ws.PAT) {
		rec.Header().Set("WWW-Authenticate", `Basic realm="hub"`)
		http.Error(rec, "unauthorized", http.StatusUnauthorized)
		return
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		http.Error(rec, "git is not installed", http.StatusInternalServerError)
		return
	}
	(&cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Root: strings.TrimSuffix(gitPrefix, "/"),
		Dir:  path.Dir(ws.gitDir),
		Env: []string{
			"GIT_PROJECT_ROOT=" + path.Dir(ws.gitDir),
			"GIT_HTTP_EXPORT_ALL=1",
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=" + os.DevNull,
			"HOME=" + path.Dir(ws.gitDir),
		},
	}).ServeHTTP(rec, r)
}

// basicIs says whether the request carries Basic credentials for the user and the password.
func basicIs(r *http.Request, user, password string) bool {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Basic") || password == "" {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	return err == nil && string(decoded) == user+":"+password
}
