package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The sentinels of the process: values that must never reach hub, the git server or the gateway.
var sentinels = map[string]string{
	"ANTHROPIC_API_KEY": "sentinel-anthropic-key-0001",
	"GITHUB_TOKEN":      "sentinel-github-token-0002",
	"GH_TOKEN":          "sentinel-gh-token-0003",
	"GITLAB_TOKEN":      "sentinel-gitlab-token-0004",
}

// settleProcess puts the sentinels, and settings that no attempt may read, in the environment of
// the process.
func settleProcess(t *testing.T) {
	t.Helper()
	for name, value := range sentinels {
		t.Setenv(name, value)
	}
	for name, value := range map[string]string{
		"AF_MODEL": "sentinel-model-0005", "AF_BRAIN": "bogus", "AF_SANDBOX": "bogus", "AF_TIMEOUT": "bogus",
		"AF_LABEL_PREFIX": "sentinel-prefix:", "AF_FIX_LAND": "bogus", "AF_MAX_ATTEMPTS": "bogus",
	} {
		t.Setenv(name, value)
	}
}

// bothFiles is a repository that a fix and a triage can both work on.
func bothFiles() map[string]string {
	files := map[string]string{}
	for name, content := range fixFiles {
		files[name] = content
	}
	for name, content := range triageFiles {
		files[name] = content
	}
	return files
}

// twoWorkspaces is an environment with the workspaces acme and other, on two repositories of the
// forge, each with its own PAT and its own issue 42, whose loops are the scripted brain of the
// kind of the job.
func twoWorkspaces(t *testing.T) (*env, map[string]string) {
	t.Helper()
	e := loopEnv(t, bothFiles(), nil, nil, fixSteps()...)
	pats := map[string]string{slug: pat, "other": "pat-of-the-other-workspace"}
	e.hub.AddWorkspace(t, fakehub.Workspace{Slug: "other", PAT: pats["other"], Branch: "main", Files: bothFiles(), ForgeURL: "https://github.example.com/Org/Other"})
	e.hub.AddIssue("other", fakehub.Issue{Number: 42, Title: "Greeting is wrong", Body: "The greeting should say hello."})
	e.subs.mu.Lock()
	e.subs.tokens["other"] = pats["other"]
	e.subs.mu.Unlock()
	flags := map[jobs.Kind][]string{
		jobs.KindFix:    {"--brain", "scripted", "--script", scriptFile(t, fixSteps()...)},
		jobs.KindTriage: {"--brain", "scripted", "--script", scriptFile(t, submitStep(probableDiagnosis))},
	}
	e.runner.loopFlags = func(a *attempt) []string { return flags[a.req.Kind] }
	e.runner.sleep = func(context.Context, time.Duration) error { return nil }
	return e, pats
}

// scenario is an attempt of a property test.
type scenario struct {
	slug  string
	kind  jobs.Kind
	input jobs.Input
	fault func(h *fakehub.Hub)
}

// leaks says which of the needles are in the text.
func leaks(text string, needles ...string) []string {
	var found []string
	for _, n := range needles {
		if n != "" && strings.Contains(text, n) {
			found = append(found, n)
		}
	}
	return found
}

// TestEveryRequestCarriesOnlyItsOwnAttemptsCredentials_TS_13_78 verifies TS-13-78: for attempts of
// both kinds and of several endings, run two at a time on two workspaces in one process whose
// environment holds the keys and the tokens of a vendor and of a forge, no request to hub, to the
// git server or to the gateway carries one of them; every request of a workspace carries its own
// PAT and no other, and its box is made under its own lease; every box spec has an empty
// environment; and the settings of the environment of the process change nothing.
// Verifies: 13-REQ-10.5
func TestEveryRequestCarriesOnlyItsOwnAttemptsCredentials_TS_13_78(t *testing.T) {
	settleProcess(t)
	e, pats := twoWorkspaces(t)
	defaultModel := catalog.Default().DefaultModelID("anthropic")

	pushFails := func(slug string) func(h *fakehub.Hub) {
		return func(h *fakehub.Hub) {
			h.Inject("POST", "/api/v1/workspaces/"+slug+"/branches/*", fakehub.Fault{Status: 503})
		}
	}
	text := jobs.Input{Title: "Run starts the wrong thing", Text: "The app crashes on submit."}
	pairs := [][2]scenario{
		{{slug, jobs.KindFix, issueInput("42"), nil}, {"other", jobs.KindTriage, issueInput("42"), nil}},
		{{slug, jobs.KindTriage, text, nil}, {"other", jobs.KindFix, issueInput("42"), nil}},
		{{slug, jobs.KindFix, issueInput("42"), pushFails(slug)}, {"other", jobs.KindFix, issueInput("42"), nil}},
		{{slug, jobs.KindTriage, issueInput("42"), nil}, {"other", jobs.KindTriage, text, nil}},
	}

	jobSlug := map[string]string{}
	var mu sync.Mutex
	for i, pair := range pairs {
		var wg sync.WaitGroup
		for j, sc := range pair {
			if sc.fault != nil {
				sc.fault(e.hub)
			}
			job := fmt.Sprintf("job_%02d%02d56789abcdef01234", i, j)
			mu.Lock()
			jobSlug[job] = sc.slug
			mu.Unlock()
			req := e.request(sc.kind, sc.input, func(r *jobs.Request) {
				r.JobID, r.Workspace, r.Dir = job, sc.slug, t.TempDir()
			})
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := e.runner.Run(context.Background(), req)
				if err != nil {
					t.Errorf("%s on %s: error %v", sc.kind, sc.slug, err)
				} else if envelope(t, res)["status"] == nil {
					t.Errorf("%s on %s: no envelope", sc.kind, sc.slug)
				}
			}()
		}
		wg.Wait()
		e.hub.Clear()
	}

	// The slug of the session each request belongs to.
	sessionSlug := map[string]string{}
	for _, s := range e.hub.Sessions() {
		sessionSlug[s.ID] = s.Slug
		if s.Model != defaultModel {
			t.Errorf("a session has the model %q: the environment of the process was read, want %q", s.Model, defaultModel)
		}
	}
	all := []string{}
	for _, v := range sentinels {
		all = append(all, v)
	}
	for _, v := range pats {
		all = append(all, v, base64.StdEncoding.EncodeToString([]byte("agent-fox:"+v)))
	}
	leases := e.hub.Leases()
	for _, l := range leases {
		all = append(all, l.Token)
	}
	if len(leases) < 8 {
		t.Fatalf("%d leases, want one for each of the 8 attempts", len(leases))
	}

	for _, r := range e.hub.Requests() {
		owner := ""
		switch parts := strings.Split(strings.Trim(r.Path, "/"), "/"); {
		case len(parts) >= 4 && parts[0] == "api" && parts[2] == "workspaces":
			owner = parts[3]
		case len(parts) >= 4 && parts[0] == "api" && parts[2] == "sessions":
			owner = sessionSlug[parts[3]]
		case r.Method == "POST" && r.Path == sessionsPath:
			var body struct {
				Slug string `json:"workspace_slug"`
			}
			_ = json.Unmarshal(r.Body, &body)
			owner = body.Slug
		}
		if owner == "" {
			t.Errorf("%s %s: no workspace to attribute the request to", r.Method, r.Path)
			continue
		}
		if r.TokenHash != fakehub.TokenHash(pats[owner]) {
			t.Errorf("%s %s (workspace %s) carries a token that is not the PAT of the workspace", r.Method, r.Path, owner)
		}
		text := r.URI + " " + string(r.Body)
		for name, values := range r.Header {
			if name == "Authorization" {
				continue // the PAT, as checked
			}
			text += " " + name + "=" + strings.Join(values, ",")
		}
		var foreign []string
		for slug, p := range pats {
			if slug != owner {
				foreign = append(foreign, p)
			}
		}
		for _, l := range leases {
			foreign = append(foreign, l.Token)
		}
		for _, v := range sentinels {
			foreign = append(foreign, v)
		}
		foreign = append(foreign, pats[owner]) // outside the Authorization header, a PAT is a leak too
		if found := leaks(text, foreign...); len(found) > 0 {
			t.Errorf("%s %s carries %v outside its credential", r.Method, r.Path, found)
		}
	}
	for _, g := range e.hub.GitRequests() {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("agent-fox:"+pats[g.Slug]))
		if g.Authorization != want {
			t.Errorf("git %s %s of %s carries %q, want its own credential", g.Method, g.Path, g.Slug, g.Authorization)
		}
	}

	byLease := map[string]fakehub.Lease{}
	for _, l := range leases {
		byLease[l.ID] = l
	}
	created := e.hub.Gateway().Created()
	if len(created) < 8 {
		t.Fatalf("%d boxes, want one for each of the 8 attempts", len(created))
	}
	for _, c := range created {
		spec, _ := json.Marshal(c.Spec)
		if len(c.Spec.Env) != 0 {
			t.Errorf("a box was made with the environment %v", c.Spec.Env)
		}
		if found := leaks(string(spec), all...); len(found) > 0 {
			t.Errorf("a box spec holds %v: %s", found, spec)
		}
		lease, job := byLease[c.Lease], c.Spec.Labels["af.job"]
		if lease.Slug == "" || lease.Slug != jobSlug[job] || lease.Labels["job"] != job {
			t.Errorf("a box for the job %s (workspace %q) was made under the lease %s of the workspace %q", job, jobSlug[job], c.Lease, lease.Slug)
		}
	}

	// What the attempts wrote to the forge has the prefix of the subscription.
	for _, slug := range []string{slug, "other"} {
		issue, _ := e.hub.IssueOf(slug, 42)
		for _, label := range issue.Labels {
			if !strings.HasPrefix(label, "af:") {
				t.Errorf("issue 42 of %s has the label %q: AF_LABEL_PREFIX of the process was read", slug, label)
			}
		}
	}
}

// scan walks the files under dir and returns the paths of those that hold one of the needles.
func scan(t *testing.T, dir string, needles ...string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err != nil || info.Size() > 32<<20 || !info.Mode().IsRegular() {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		for _, n := range needles {
			if n != "" && bytes.Contains(data, []byte(n)) {
				found = append(found, path+" holds "+n[:min(len(n), 12)]+"…")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestNeitherThePATNorTheLeaseTokenIsInAnyOutputOrFileAfterAFullFixAttempt_TS_13_79 verifies
// TS-13-79: a full fix attempt on the fake hub, with the PAT and the token of the lease known,
// leaves neither in the envelope, in the events (as the recorder got them and as hub did), in the
// comments, in a file of the attempt tree while the clone is there and after the attempt has
// returned (a transient error keeps the clone), in a request to the forge, or in a box spec.
// Verifies: 13-REQ-10.6
func TestNeitherThePATNorTheLeaseTokenIsInAnyOutputOrFileAfterAFullFixAttempt_TS_13_79(t *testing.T) {
	settleProcess(t)
	basic := base64.StdEncoding.EncodeToString([]byte("agent-fox:" + pat))

	check := func(t *testing.T, e *env, res jobs.Result, whileThere []string) {
		t.Helper()
		leases := e.hub.Leases()
		if len(leases) != 1 {
			t.Fatalf("%d leases", len(leases))
		}
		needles := append([]string{pat, basic, leases[0].Token}, sortedValues(sentinels)...)
		if found := leaks(string(res.Envelope)+res.Report, needles...); len(found) > 0 {
			t.Errorf("the envelope or the report holds %v", found)
		}
		for _, ev := range e.sink.emitted() {
			if found := leaks(string(ev.Data), needles...); len(found) > 0 {
				t.Errorf("an event (%s) holds %v", ev.Type, found)
			}
		}
		for _, batch := range e.hub.EventBatches(slug, e.hub.Sessions()[0].RunID) {
			for _, ev := range batch {
				if found := leaks(string(ev.Payload), needles...); len(found) > 0 {
					t.Errorf("an event posted to hub (%s) holds %v", ev.Type, found)
				}
			}
		}
		for _, c := range e.issueNow().Comments {
			if found := leaks(c.Body, needles...); len(found) > 0 {
				t.Errorf("a comment holds %v", found)
			}
		}
		for _, r := range e.hub.Requests() {
			if found := leaks(r.URI+string(r.Body), needles...); len(found) > 0 {
				t.Errorf("%s %s carries %v in its path or body", r.Method, r.Path, found)
			}
		}
		for _, c := range e.hub.Gateway().Created() {
			spec, _ := json.Marshal(c.Spec)
			if len(c.Spec.Env) != 0 || len(leaks(string(spec), needles...)) > 0 {
				t.Errorf("a box spec holds the environment %v or a token: %s", c.Spec.Env, spec)
			}
		}
		for _, found := range append(whileThere, scan(t, e.dir, needles...)...) {
			t.Errorf("a file of the attempt tree: %s", found)
		}
	}

	t.Run("a fix that lands", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		var while []string
		e.sink.hook = func(typ string, data json.RawMessage) {
			var step struct{ Name string }
			if typ != "step" || json.Unmarshal(data, &step) != nil || step.Name != "release" {
				return
			}
			leases := e.hub.Leases()
			while = scan(t, e.dir, pat, basic, leases[0].Token)
			// The git state of the clone, where a credential helper or a URL would leave the PAT.
			while = append(while, scan(t, filepath.Join(e.dir, "clone", ".git"), pat, basic, leases[0].Token)...)
		}

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if err != nil || envelope(t, res)["status"] != "done" {
			t.Fatalf("result %s, error %v", res.Envelope, err)
		}
		// recovery/ is the one entry the worker leaves in the directory of an attempt: it never removes it.
		if entries, _ := os.ReadDir(filepath.Join(e.dir)); len(entries) != 1 || entries[0].Name() != "recovery" {
			t.Errorf("the attempt directory holds %d entries after a normal return, want recovery/ alone", len(entries))
		}
		check(t, e, res, while)
	})

	t.Run("an attempt whose clone is kept", func(t *testing.T) {
		e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
		e.hub.Gateway().RefuseBoxes("no room for another box")

		res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

		if !jobs.IsTransient(err) {
			t.Fatalf("error = %v, want a transient error", err)
		}
		if _, err := os.Stat(filepath.Join(e.dir, "clone", ".git", "config")); err != nil {
			t.Fatalf("the clone is not kept: %v", err)
		}
		if cfg, _ := os.ReadFile(filepath.Join(e.dir, "clone", ".git", "config")); !strings.Contains(string(cfg), e.hub.CloneURL(slug)) {
			t.Errorf(".git/config = %s, want the clone URL, with no credential", cfg)
		}
		check(t, e, res, nil)
	})
}

func sortedValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
