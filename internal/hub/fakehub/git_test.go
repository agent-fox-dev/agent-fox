package fakehub

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// gitHub is a hub whose workspace acme has a repository with a README on main.
func gitHub(t *testing.T) *Hub {
	t.Helper()
	testutil.Require(t, "git")
	return newHub(t, Workspace{Files: map[string]string{"README.md": "hello\n"}})
}

// refs asks the git server for the refs of acme with the Authorization header, none when it is empty.
func refs(t *testing.T, h *Hub, slug, authorization string) (int, error) {
	t.Helper()
	req, err := http.NewRequest("GET", h.CloneURL(slug)+"/info/refs?service=git-upload-pack", nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

func TestGitServer_TakesOnlyTheBasicCredentialOfThePAT(t *testing.T) {
	h := gitHub(t)
	h.AddWorkspace(t, Workspace{Slug: "open", Files: map[string]string{"a": "b"}})
	for _, c := range []struct {
		name, slug, header string
		want               int
	}{
		{"no credential", "acme", "", 401},
		{"a bearer token", "acme", "Bearer " + pat, 401},
		{"another user", "acme", basic("someone", pat), 401},
		{"another PAT", "acme", basic(GitUser, "not-the-pat"), 401},
		{"an empty PAT", "acme", basic(GitUser, ""), 401},
		{"the empty PAT of a workspace with none", "open", basic(GitUser, ""), 401},
		{"a workspace that is not there", "missing", basic(GitUser, pat), 401},
		{"the credential", "acme", basic(GitUser, pat), 200},
	} {
		if got, err := refs(t, h, c.slug, c.header); err != nil || got != c.want {
			t.Errorf("%s: %d, %v; want %d", c.name, got, err, c.want)
		}
	}
	log := h.GitRequests()
	if len(log) != 8 || log[7].Status != 200 || log[7].Slug != "acme" || log[7].Authorization != basic(GitUser, pat) || log[0].Status != 401 || log[0].Path != "/git/acme.git/info/refs" {
		t.Errorf("the git server recorded %+v", log)
	}
}

// A clone with the header that the worker sends gets the files, and a push of a new branch lands in
// the repository, which the branch push route then finds.
func TestGitServer_ClonesAndTakesAPushThatTheBranchPushThenFinds(t *testing.T) {
	h := gitHub(t)
	header := "http.extraHeader=Authorization: " + basic(GitUser, pat)
	work := filepath.Join(t.TempDir(), "work")
	testutil.Git(t, filepath.Dir(work), "-c", header, "clone", "-q", h.CloneURL("acme"), work)
	if got := testutil.ReadFile(t, filepath.Join(work, "README.md")); got != "hello\n" {
		t.Errorf("the clone holds %q", got)
	}
	if _, err := testutil.GitErr(t, t.TempDir(), "-c", "http.extraHeader=Authorization: Bearer "+pat, "clone", "-q", h.CloneURL("acme"), "bad"); err == nil {
		t.Error("a clone with a bearer token worked")
	}

	testutil.Git(t, work, "switch", "-q", "-c", "fix/42")
	testutil.WriteFile(t, work, "fix.txt", "fixed\n")
	head := testutil.Commit(t, work, "fix")
	testutil.Git(t, work, "-c", header, "push", "-q", "origin", "fix/42")
	if got := strings.TrimSpace(testutil.Git(t, h.GitDir("acme"), "rev-parse", "refs/heads/fix/42")); got != head {
		t.Errorf("the repository has fix/42 at %q, want %s", got, head)
	}

	for _, c := range []struct {
		branch string
		want   int
	}{{"fix%2F42", 200}, {"nothing", 404}} {
		if r := do(t, h, "POST", "/api/v1/workspaces/acme/branches/"+c.branch+"/push", pat, map[string]any{}); r.Status != c.want {
			t.Errorf("the push of %s: %d %s, want %d", c.branch, r.Status, r.Body, c.want)
		}
	}
	pushes := h.BranchPushes()
	if len(pushes) != 2 || pushes[0].Branch+pushes[1].Branch != "fix/42nothing" || pushes[1].BranchWasThere || pushes[1].Status != 404 || !pushes[0].BranchWasThere {
		t.Errorf("BranchPushes = %+v", pushes)
	}
}

func TestBranchPush_OfAWorkspaceWithNoRepositoryAlwaysFindsTheBranch(t *testing.T) {
	h := newHub(t, Workspace{})
	if r := do(t, h, "POST", "/api/v1/workspaces/acme/branches/anything/push", pat, nil); r.Status != 200 || r.Object(t)["result"] != "pushed" {
		t.Errorf("the push: %d %s", r.Status, r.Body)
	}
}

// A fault for the root of the repository applies to every request under it, before the
// credential is looked at, and is recorded with the status it answered.
func TestGitServer_AnswersTheFaultsOfItsRepositoryFirst(t *testing.T) {
	h := gitHub(t)
	const root = "/git/acme.git"
	credential := basic(GitUser, pat)
	for _, status := range []int{403, 503} {
		h.Inject("", root, Fault{Status: status})
		if got, err := refs(t, h, "acme", credential); err != nil || got != status {
			t.Errorf("a fault of %d answered %d, %v", status, got, err)
		}
	}
	if log := h.GitRequests(); len(log) != 2 || log[1].Status != 503 {
		t.Errorf("the git server recorded %+v", log)
	}
	h.Inject("", root+"/info/refs", Fault{Hangup: true})
	if _, err := refs(t, h, "acme", credential); err == nil {
		t.Error("a hang-up for a path of the repository answered")
	}
	h.Clear()
	h.Inject("", root, Fault{Delay: 30 * time.Millisecond})
	if start := time.Now(); func() bool { got, err := refs(t, h, "acme", credential); return err != nil || got != 200 }() || time.Since(start) < 30*time.Millisecond {
		t.Errorf("a delay answered after %v", time.Since(start))
	}
}
