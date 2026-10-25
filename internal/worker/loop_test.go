package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/poll"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// The tests of this file run the real loop in this process; only the brain is scripted.

// leaks are the needles that the text holds.
func leaks(text string, needles ...string) []string {
	var found []string
	for _, n := range needles {
		if n != "" && strings.Contains(text, n) {
			found = append(found, n[:min(len(n), 12)]+"...")
		}
	}
	return found
}

func readOrNil(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

// The envelope is the loop's with what the worker added, and no credential is in anything the attempt left or sent.
func TestLoop_AFixLandsAPullRequest(t *testing.T) {
	e := loopEnv(t, fixFiles, fakehub.Workspace{Vars: map[string]string{"AF_MAX_ATTEMPTS": "4"}}, fixSteps()...)
	basic := base64.StdEncoding.EncodeToString([]byte("agent-fox:" + pat))
	var loopEnvelope, loopReport string
	var loopEvents, leakedFiles []string
	e.sink.hook = func(typ string, data json.RawMessage) {
		if typ == "step" && strings.Contains(string(data), `"release"`) { // the run directory is still there
			loopEnvelope = string(readOrNil(filepath.Join(e.runDir(), "envelope.json")))
			loopReport = string(readOrNil(filepath.Join(e.runDir(), "report.md")))
			loopEvents = strings.Split(strings.TrimSpace(string(readOrNil(filepath.Join(e.runDir(), "events.jsonl")))), "\n")
			for path, content := range testutil.Files(t, e.dir) {
				if len(leaks(content, pat, basic, e.hub.Leases()[0].Token)) > 0 {
					leakedFiles = append(leakedFiles, path)
				}
			}
		}
	}

	res, err := e.run(e.fix())

	env := envelope(t, res)
	if err != nil || env["status"] != "done" || res.MaxAttempts != 4 || res.Report == "" || res.Report != loopReport {
		t.Fatalf("error %v, status %v, max attempts %d, report of %d bytes, warnings %v", err, env["status"], res.MaxAttempts, len(res.Report), codes(env))
	}
	session, lease := e.session(), e.hub.Leases()[0]
	var clone, push int
	for _, g := range e.hub.GitRequests() {
		if clone == 0 {
			clone = g.Seq
		}
		if g.Method == "POST" && strings.HasSuffix(g.Path, "/git-receive-pack") {
			push = g.Seq
		}
	}
	order := []int{e.seqOf("GET", issuePath, ""), session.Seq, e.seqOf("POST", issuePath+"/labels", ""), clone, lease.Seq, push,
		e.seqOf("POST", "/api/v1/workspaces/acme/branches/", "/push"), e.seqOf("POST", pullsPath, ""), e.seqOf("POST", issuePath+"/comments", ""),
		lease.ReleasedSeq, e.seqOf("POST", sessionsPath+"/"+session.ID+"/usage", ""), e.seqOf("POST", "/api/v1/workspaces/acme/runs/", "/events/batch"), session.CompletedSeq}
	if slices.Contains(order, 0) || !slices.IsSorted(order) {
		t.Errorf("read the issue, open the session, mark, clone, lease, push, publish, open the pull request, comment, release, post the usage, the events, complete: %v", order)
	}

	// The events of the loop reached the recorder as they were written, among the steps of the worker.
	var got, steps []string
	for _, ev := range e.sink.emitted() {
		var step struct{ Name, Source string }
		if ev.Type == "step" && json.Unmarshal(ev.Data, &step) == nil && step.Source == "worker" {
			steps = append(steps, step.Name)
		} else {
			got = append(got, ev.Type+" "+string(ev.Data))
		}
	}
	var written []string
	for _, line := range loopEvents {
		ev := testutil.Decode[sunk](t, []byte(line))
		written = append(written, ev.Type+" "+string(ev.Data))
	}
	if len(written) < 2 || !strings.HasPrefix(written[0], "run_start") || !slices.Equal(got, written) {
		t.Errorf("the recorder got the events\n%v\nthe loop wrote\n%v", got, written)
	}
	if want := []string{"resolve", "session", "clone", "lease", "run", "mirror", "release", "report"}; !slices.Equal(steps, want) {
		t.Errorf("the steps of the worker are %v, want %v", steps, want)
	}
	if e.sink.hubs[0] != [3]string{session.ID, session.RunID, ""} || e.sink.lastHub() != [3]string{session.ID, session.RunID, lease.ID} {
		t.Errorf("the recorder was told %v", e.sink.hubs)
	}

	// The envelope is the loop's, but for the entries the worker added to three lists.
	before, after := testutil.Decode[map[string]json.RawMessage](t, []byte(loopEnvelope)), testutil.Decode[map[string]json.RawMessage](t, res.Envelope)
	for field, raw := range before {
		if was, now := testutil.Decode[any](t, raw), testutil.Decode[any](t, after[field]); field == "warnings" || field == "artifacts" || field == "side_effects" {
			if w, n := was.([]any), now.([]any); len(n) < len(w) || string(testutil.JSON(t, n[:len(w)])) != string(testutil.JSON(t, w)) {
				t.Errorf("%s lost what the loop wrote: %s, was %s", field, after[field], raw)
			}
		} else if string(raw) != string(after[field]) {
			t.Errorf("%s changed: %s, was %s", field, after[field], raw)
		}
	}
	for _, want := range []string{`"kind":"hub_session","name":"` + session.ID, `"kind":"sandbox","name":"` + lease.ID} {
		if !strings.Contains(string(testutil.JSON(t, testutil.Decode[any](t, after["artifacts"]))), want) {
			t.Errorf("the artifacts %s lack %s", after["artifacts"], want)
		}
	}

	if !slices.Equal(e.issueNow().Labels, []string{"af:landed"}) || !slices.Equal(e.commentKinds(), []string{"fix_summary"}) ||
		!slices.Equal(res.LabelsSet, []string{"af:landed"}) || !slices.Equal(res.LabelsRemoved, []string{"af:in-progress"}) {
		t.Errorf("issue 42 has %v and the comments %v, the result sets %v and removes %v", e.issueNow().Labels, e.commentKinds(), res.LabelsSet, res.LabelsRemoved)
	}
	if pulls := e.hub.Pulls(slug); len(pulls) != 1 || !strings.HasPrefix(pulls[0].Head, "fix/42-") || session.Status != "completed" || len(session.Usage) != 1 || session.Usage[0].Model != session.Model {
		t.Errorf("pull requests %+v, session %+v", pulls, session)
	}

	sent := append([]string{string(res.Envelope), res.Report, string(testutil.JSON(t, e.sink.emitted())), string(testutil.JSON(t, e.hub.EventBatches(slug, session.RunID)))}, got...)
	for _, c := range e.issueNow().Comments {
		sent = append(sent, c.Body)
	}
	for _, r := range e.hub.Requests() {
		sent = append(sent, r.URI+string(r.Body))
	}
	for _, c := range e.hub.Gateway().Created() {
		sent = append(sent, string(testutil.JSON(t, c.Spec)))
	}
	if found := leaks(strings.Join(sent, "\n"), pat, basic, lease.Token); len(found) > 0 || len(leakedFiles) > 0 {
		t.Errorf("a credential (%v) is in what the attempt sent or wrote, or in the files %v", found, leakedFiles)
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 0 || e.subs.reads != 1 {
		t.Errorf("the attempt directory holds %d entries after the attempt, and the token was read %d times, want none and once", len(entries), e.subs.reads)
	}
}

// The delivery after it goes on in the same session, from a fresh clone, to the end.
func TestLoop_ALostBoxIsTransientAndTheNextDeliveryRuns(t *testing.T) {
	e := loopEnv(t, fixFiles, fakehub.Workspace{}, fixSteps()...)
	e.hub.Gateway().RefuseBoxes("no room for another box")
	req := e.fix()

	res, err := e.run(req)

	if f := transient(err); f == nil || f.Category != "sandbox" || !f.Retryable || !strings.Contains(f.Message, "no room for another box") || len(res.Envelope) != 0 {
		t.Fatalf("result %s, error %v, want a transient sandbox error with the reason of the gateway", res.Envelope, err)
	}
	session := e.session()
	if issue := e.issueNow(); len(issue.Labels) != 0 || len(issue.Comments) != 0 || !e.hub.Leases()[0].Released || session.Status != "open" || len(session.Usage) != 0 || len(e.hub.EventBatches(slug, session.RunID)) != 0 {
		t.Errorf("issue %v with %d comments, lease %+v, session %+v: want nothing written, the lease released and nothing reported", issue.Labels, len(issue.Comments), e.hub.Leases()[0], session)
	}

	e.hub.Gateway().RefuseBoxes("")
	testutil.WriteFile(t, e.dir, "clone/left-by-the-first-delivery", "x")
	req.Delivery, req.SessionID, req.RunID = 2, session.ID, session.RunID
	res, err = e.run(req)

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("the second delivery: %s, %v, want done", res.Envelope, err)
	}
	if s := e.session(); s.Status != "completed" || len(s.Usage) != 1 || len(e.hub.Leases()) != 2 || !slices.Equal(e.issueNow().Labels, []string{"af:landed"}) {
		t.Errorf("session %+v, %d leases, labels %v, want the one session completed, a lease for each delivery, and landed", s, len(e.hub.Leases()), e.issueNow().Labels)
	}
}

// A fix and a triage of a text on two workspaces at once, in a process whose environment holds keys and settings of its own.
func TestLoop_AttemptsSideBySideCarryOnlyTheirOwnCredentials(t *testing.T) {
	sentinels := []string{"sentinel-anthropic-key-0001", "sentinel-github-token-0002"}
	for name, value := range map[string]string{"ANTHROPIC_API_KEY": sentinels[0], "GITHUB_TOKEN": sentinels[1], "AF_MODEL": "sentinel-model", "AF_TIMEOUT": "bogus", "AF_LABEL_PREFIX": "sentinel:"} {
		t.Setenv(name, value)
	}
	e := loopEnv(t, fixFiles, fakehub.Workspace{})
	pats := map[string]string{slug: pat, "other": "pat-of-the-other-workspace"}
	e.hub.AddWorkspace(t, fakehub.Workspace{Slug: "other", PAT: pats["other"], Files: triageFiles, ForgeURL: "https://github.example.com/Org/Other"})
	e.subs.tokens["other"] = pats["other"]
	scripts := map[jobs.Kind]string{jobs.KindFix: scriptFile(t, fixSteps()...), jobs.KindTriage: scriptFile(t, submitStep(probableDiagnosis))}
	e.runner.loopOptions = func(a *attempt, o *afcmd.LoopOptions) { o.Brain, o.Script = "scripted", scripts[a.req.Kind] }
	triageReq := e.request(jobs.KindTriage, jobs.Input{Title: "Greeting is wrong", Text: "The greeting says helo."}, func(r *jobs.Request) {
		r.JobID, r.Workspace, r.Dir = "job_other_0123456789abcdef", "other", t.TempDir()
	})
	var wg sync.WaitGroup
	var triaged jobs.Result
	for _, req := range []jobs.Request{e.fix(), triageReq} {
		wg.Go(func() {
			res, err := e.runner.Run(context.Background(), req)
			if err != nil || len(res.Envelope) == 0 {
				t.Errorf("%s on %s: %v", req.Kind, req.Workspace, err)
			}
			if req.Kind == jobs.KindTriage {
				triaged = res
			}
		})
	}
	wg.Wait()

	owner, secrets := map[string]string{}, append(slices.Clone(sentinels), pats[slug], pats["other"])
	for _, s := range e.hub.Sessions() {
		owner[s.ID] = s.Slug
		if s.Model != catalog.Default().DefaultModelID("anthropic") {
			t.Errorf("a session has the model %q: the environment of the process was read", s.Model)
		}
	}
	for _, l := range e.hub.Leases() {
		owner[l.ID], secrets = l.Slug, append(secrets, l.Token)
	}
	for _, r := range e.hub.Requests() {
		ws := ""
		switch parts := strings.Split(strings.Trim(r.Path, "/"), "/"); {
		case len(parts) >= 4 && parts[2] == "workspaces":
			ws = parts[3]
		case len(parts) >= 4 && parts[2] == "sessions":
			ws = owner[parts[3]]
		case r.Path == sessionsPath:
			ws, _ = testutil.Decode[map[string]any](t, r.Body)["workspace_slug"].(string)
		}
		header := r.Header.Clone()
		header.Del("Authorization")
		if found := leaks(r.URI+string(r.Body)+string(testutil.JSON(t, header)), secrets...); r.TokenHash != fakehub.TokenHash(pats[ws]) || len(found) > 0 {
			t.Errorf("%s %s (of %q) does not carry the PAT of its workspace, or carries %v", r.Method, r.Path, ws, found)
		}
	}
	for _, g := range e.hub.GitRequests() {
		if want := "Basic " + base64.StdEncoding.EncodeToString([]byte("agent-fox:"+pats[g.Slug])); g.Authorization != want {
			t.Errorf("git %s %s of %s carries %q", g.Method, g.Path, g.Slug, g.Authorization)
		}
	}
	created := e.hub.Gateway().Created()
	for _, c := range created {
		job := c.Spec.Labels["af.job"]
		if len(c.Spec.Env) != 0 || (job == jobID) != (owner[c.Lease] == slug) || len(leaks(string(testutil.JSON(t, c.Spec)), secrets...)) > 0 {
			t.Errorf("the box of %s was made under the lease of %q with %+v", job, owner[c.Lease], c.Spec)
		}
	}
	env := envelope(t, triaged)
	number, _ := env["result"].(map[string]any)["issue"].(map[string]any)["number"].(float64)
	made := e.hub.RequestsTo("POST", "/api/v1/workspaces/other/issues")
	if len(created) != 2 || env["input"].(map[string]any)["kind"] != "stdin" || len(made) != 1 || !strings.Contains(string(made[0].Body), `# Greeting is wrong\n\nThe greeting says helo.`) {
		t.Errorf("%d boxes, the triage of %v made the issues %v, want a box each and one issue of the heading and the text", len(created), env["input"], made)
	}
	if fixed, made := e.issueNow(), e.issue("other", int(number)); !slices.Equal(fixed.Labels, []string{"af:landed"}) || !slices.Equal(made.Labels, []string{"af:ready"}) {
		t.Errorf("issue 42 of acme has %v and the issue the triage made %v, want landed and ready", fixed.Labels, made.Labels)
	}
}

// The label poll and the jobs layer on a state file run the worker, which reports to their recorder.
func TestLoop_ALabelledIssueIsTriagedThroughTheJobsLayer(t *testing.T) {
	e := loopEnv(t, triageFiles, fakehub.Workspace{Mode: "carry_patch"}, submitStep(probableDiagnosis))
	original := "The app crashes on submit."
	e.hub.AddIssue(slug, fakehub.Issue{Number: 44, Title: "App crashes", Body: original, Labels: []string{"af:triage"}})
	key, err := secrets.NewKey(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "agent-fox.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, _, err := st.Upsert(context.Background(), slug, pat, "u1", "u1", time.Now()); err != nil {
		t.Fatal(err)
	}
	e.runner.reporter = nil
	svc, err := jobs.New(jobs.Config{
		DB: st.DB(), Subscriptions: st, Logger: e.log.Logger(), Workers: 1, AttemptsDir: filepath.Join(t.TempDir(), "attempts"),
		Runner: e.runner, PollInterval: 10 * time.Millisecond, RetryDelay: 10 * time.Millisecond, Retain: 24 * time.Hour,
	})
	if err != nil || svc.Start() != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	poller := poll.New(poll.Config{Subscriptions: st, Hub: hub.New(e.hub.URL, "1"), Jobs: svc, DB: st.DB(), Logger: e.log.Logger()})

	poller.Cycle(context.Background(), slug)

	var doc jobs.Doc
	testutil.Eventually(t, "the job to end", func() bool {
		ids := testutil.Column(t, st.DB(), "SELECT id FROM af_jobs WHERE workspace = ?", slug)
		if len(ids) != 1 {
			return false
		}
		doc, err = svc.Get(context.Background(), ids[0])
		return err == nil && (doc.State == jobs.StateDone || doc.State == jobs.StateFailed)
	})
	if doc.State != jobs.StateDone || doc.Source != (jobs.Source{Kind: "label", By: "label af:triage"}) {
		t.Fatalf("the job is %s from %+v, want a done triage of the label", doc.State, doc.Source)
	}
	if told := testutil.Column(t, st.DB(), "SELECT hub_session FROM af_attempts WHERE job_id = ?", doc.ID); !slices.Equal(told, []string{e.session().ID}) {
		t.Errorf("the attempt holds the session %v, want %s, which the worker told its recorder", told, e.session().ID)
	}
	issue := e.issue(slug, 44)
	if kept, _, _ := triage.Extract(issue.Body); kept != original || !slices.Equal(issue.Labels, []string{"af:ready"}) {
		t.Errorf("issue 44 has %v and the body\n%s", issue.Labels, issue.Body)
	}
}
