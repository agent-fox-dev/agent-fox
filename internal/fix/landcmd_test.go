package fix

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// runWorld is a measured checkout with a bare origin, a forge, the directory of a run in it
// and what a run of land is given.
type runWorld struct {
	*measureWorld
	forge  *fakeforge.Forge
	client issuex.Client
	target issuex.Repo
	rep    *recorder
	runID  string
	runDir string
	ledger []string
}

func newRunWorld(t *testing.T) *runWorld {
	t.Helper()
	m := newMeasureWorldWith(t, measuredFiles(), true)
	f := fakeforge.New(t, fakeforge.GitHub, "o/r")
	target := issuex.Repo{Owner: "o", Name: "r"}
	client, err := f.NewForge("token")(issuex.Options{Repo: target})
	if err != nil {
		t.Fatal(err)
	}
	w := &runWorld{measureWorld: m, forge: f, client: client, target: target, rep: &recorder{}, runID: "20261020_100000_aaaaaa"}
	w.runDir = filepath.Join(m.repo.Dir, ".agent-fox", "run", w.runID)
	if err := os.MkdirAll(w.runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	w.state.RunID = w.runID
	return w
}

// submit writes the submission file of the run, as the submit core leaves it.
func (w *runWorld) submit(t *testing.T, payload string) {
	t.Helper()
	var compact json.RawMessage
	if err := json.Unmarshal([]byte(payload), &compact); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(compact)
	file := submission.File{Kind: KindName, RunID: w.runID, Attempt: 1, Nonce: "n", SubmittedAt: "2026-10-20T10:00:00Z", PayloadSHA256: submission.PayloadSHA256(data), Payload: data}
	if err := os.WriteFile(filepath.Join(w.runDir, "submission.json"), submission.Marshal(file), 0o644); err != nil {
		t.Fatal(err)
	}
}

const helloPayload = `{"subject":"say hello","type":"fix","root_cause":"the script prints nothing useful","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}]}`

// run lands the run, with the seams that count the runs of the gate and the revert check.
func (w *runWorld) run(mode string) (LandRunResult, error) {
	w.warnings = nil
	return LandRun(context.Background(), LandRunParams{
		Dir: w.repo.Dir, Git: w.repo.Git, RunID: w.runID, RunPath: w.runDir, State: w.state, Mode: mode, Forge: w.client, Target: w.target, Reporter: w.rep,
		Record:      func(kind string, _ any) { w.ledger = append(w.ledger, kind) },
		WriteReport: func(string) error { return nil },
		Gate: func(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
			w.gateRuns++
			return checks.Run(ctx, dir, o)
		},
		Conform: func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error) {
			w.conformRun++
			return conform.Check(ctx, dir, since, o)
		},
	})
}

func (w *runWorld) branches(t *testing.T) []string {
	t.Helper()
	return strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
}

// TestFixLandParksARefusalAndStartsNoBrain_TS_09_45 verifies TS-09-45.
// Verifies: 09-REQ-6.8
func TestFixLandParksARefusalAndStartsNoBrain_TS_09_45(t *testing.T) {
	w := newRunWorld(t)
	w.edit(t, "src/greet.sh", ":\n") // a change by hand that leaves the gate red
	w.submit(t, helloPayload)

	got, err := w.run(LandBranch)

	if err != nil {
		t.Fatal(err)
	}
	if got.Status != toolio.StatusUnverified || toolio.ExitCode(got.Status) != 4 || got.Result.Outcome != OutcomeParked {
		t.Errorf("status %q, outcome %q, want unverified (4) and parked", got.Status, got.Result.Outcome)
	}
	branch := *got.Result.Branch
	if subject := strings.TrimSpace(gitDo(t, w.repo.Dir, "log", "-1", "--format=%s", branch)); subject != "wip: say hello" {
		t.Errorf("subject = %q, want the wip commit", subject)
	}
	if msg := gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "verdict regressed") || !strings.Contains(msg, "- make check") {
		t.Errorf("message = %q, want the verdict and the failing check", msg)
	}
	if now, _ := w.repo.Git.CurrentBranch(context.Background()); now != "main" || !remoteHasBranch(t, w.repo.Dir, branch) {
		t.Errorf("checked out %q, pushed %v", now, remoteHasBranch(t, w.repo.Dir, branch))
	}
	if v := got.Result.Verdict; v == nil || *v != checks.VerdictRegressed || got.Result.Landable || got.Result.Land != LandBranch || got.Result.Rounds != 0 {
		t.Errorf("result = %+v", got.Result)
	}
	for _, kind := range w.ledger {
		if strings.HasPrefix(kind, "brain") {
			t.Errorf("ledger = %v: no brain was started", w.ledger)
		}
	}
	if !slices.Contains(w.ledger, "measure") || !slices.Contains(w.ledger, "refusal") || !slices.Contains(w.rep.warnings, WarnFixNotLanded) {
		t.Errorf("ledger %v, warnings %v, want the measure, the refusal and fix_not_landed", w.ledger, w.rep.warnings)
	}
	if w.forge.Count(fakeforge.OpPullRequest) != 0 {
		t.Error("branch mode asks nothing of the forge")
	}
}

// TestFixLandLandsAChangeThatAfMeasuredToBeGood guards the other end of the land: a hand-made
// change with a test that proves it lands, closes the issue, and is a result with every fact.
func TestFixLandLandsAChangeThatAfMeasuredToBeGood(t *testing.T) {
	w := newRunWorld(t)
	w.state.Issue = &StateIssue{URL: "https://github.com/o/r/issues/7", Title: "Greeting is wrong", Number: 7}
	w.state.Input = StateInput{Kind: "issue", Origin: w.state.Issue.URL, Key: w.state.Issue.URL}
	w.state.Rounds, w.state.Brain, w.state.CostUSD, w.state.CostSource = 2, "agentkit", 0.25, "measured"
	w.edit(t, "src/greet.sh", "echo hello\n")
	w.edit(t, "tests/hello_test.sh", "[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n")
	w.submit(t, strings.Replace(helloPayload, `"files":[`, `"files":[{"path":"tests/hello_test.sh","why":"the proof"},{"path":"docs/missing.md","why":"a file that did not change"},`, 1))

	got, err := w.run(LandPR)

	if err != nil {
		t.Fatal(err)
	}
	r := got.Result
	if got.Status != toolio.StatusDone || r.Outcome != OutcomeLanded || !r.Closes || !r.Landable || r.PullRequest == nil || !r.Pushed || r.Rounds != 2 || r.Attempts != 0 || r.AttemptsMax != 0 {
		t.Errorf("status %q, result %+v", got.Status, r)
	}
	if !slices.Equal(r.Scope.Unchanged, []string{"docs/missing.md"}) || len(r.Scope.Undeclared) != 0 || !slices.Contains(w.rep.warnings, WarnScopeMismatch) {
		t.Errorf("scope = %+v, warnings %v, want the declared file that did not change, and the warning", r.Scope, w.rep.warnings)
	}
	if r.After == nil || r.RevertCheck == nil || r.RevertCheck.Outcome != "proven" || !slices.Equal(r.ChangedFiles, []string{"src/greet.sh", "tests/hello_test.sh"}) || r.Box != nil {
		t.Errorf("result = %+v, want the facts af measured, and no box for a land", r)
	}
	if msg := gitDo(t, w.repo.Dir, "log", "-1", "--format=%B", *r.Branch); !strings.Contains(msg, "Closes https://github.com/o/r/issues/7") {
		t.Errorf("message = %q", msg)
	}
	if prs := w.forge.PullRequests(); len(prs) != 1 || !strings.Contains(prs[0].Body, "brain `agentkit` · cost $0.2500 (measured)") {
		t.Errorf("pull requests = %+v, want the footer to say what the rounds used", prs)
	}
}

// TestFixLandRefusesAQuestionAndWhatItCannotLand_TS_09_31 verifies TS-09-31: a question is
// not a report, and nothing is made of it.
// Verifies: 09-REQ-4.6
func TestFixLandRefusesAQuestionAndWhatItCannotLand_TS_09_31(t *testing.T) {
	w := newRunWorld(t)
	w.state.Input = StateInput{Kind: "issue", Origin: "https://github.com/o/r/issues/7", Key: "k"}
	w.edit(t, "docs/readme.md", "readme, changed\n")
	w.submit(t, `{"question":{"text":"Which greeting?","options":["hello","hi"]}}`)

	_, err := w.run(LandBranch)

	f := failure(t, err)
	if f.Category != toolio.CategorySubmission || f.Retryable || !strings.Contains(f.Message, "af fix brief") || !strings.Contains(f.Message, "--context") || !strings.Contains(f.Message, "https://github.com/o/r/issues/7") {
		t.Errorf("failure = %+v, want a submission failure that points at af fix brief --context", f)
	}
	if !slices.Equal(w.branches(t), []string{"main"}) || w.gateRuns != 0 {
		t.Errorf("branches %v, %d gate runs: nothing is made of a question", w.branches(t), w.gateRuns)
	}

	// No submission at all, and a submission that is not a fix report, are told apart.
	w2 := newRunWorld(t)
	w2.edit(t, "docs/readme.md", "readme, changed\n")
	if _, err := w2.run(LandBranch); failure(t, err).Category != toolio.CategoryUsage || !strings.Contains(err.Error(), "af fix submit") {
		t.Errorf("no submission: %v, want a usage failure that points at af fix submit", err)
	}
	if err := os.WriteFile(filepath.Join(w2.runDir, "submission.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w2.run(LandBranch); failure(t, err).Category != toolio.CategoryUsage {
		t.Errorf("a submission that is not JSON: %v", err)
	}
	// The submission of another kind of run is not a fix report, whatever its payload says.
	w2.submit(t, helloPayload)
	other := strings.Replace(string(mustRead(t, filepath.Join(w2.runDir, "submission.json"))), `"kind":"fix_report"`, `"kind":"diagnosis"`, 1)
	if err := os.WriteFile(filepath.Join(w2.runDir, "submission.json"), []byte(other), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := w2.run(LandBranch); failure(t, err).Category != toolio.CategoryUsage || !strings.Contains(err.Error(), "not a fix report") {
		t.Errorf("a submission of another kind: %v", err)
	}
}

// TestAForeignCommitOrBranchIsRefusedBeforeAnythingIsMadeOfTheChange_TS_09_38 verifies
// TS-09-38 for the land: nothing is created and nothing is parked.
// Verifies: 09-REQ-6.1
func TestAForeignCommitOrBranchIsRefusedBeforeAnythingIsMadeOfTheChange_TS_09_38(t *testing.T) {
	for name, tc := range map[string]struct {
		move    func(t *testing.T, w *runWorld)
		branch  string
		mention string
	}{
		"a commit made outside af": {func(t *testing.T, w *runWorld) {
			w.edit(t, "docs/other.md", "someone's\n")
			gitDo(t, w.repo.Dir, "add", "docs/other.md")
			gitDo(t, w.repo.Dir, "commit", "-q", "-m", "chore: made by hand", "docs/other.md")
		}, "main", "made by hand"},
		"another branch": {func(t *testing.T, w *runWorld) { gitDo(t, w.repo.Dir, "checkout", "-q", "-b", "elsewhere") }, "elsewhere", "elsewhere"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newRunWorld(t)
			tc.move(t, w)
			w.edit(t, "docs/readme.md", "readme, changed\n")
			w.submit(t, helloPayload)
			before := w.branches(t)

			_, err := w.run(LandBranch)

			if f := failure(t, err); f.Category != toolio.CategoryGit || !strings.Contains(f.Message, tc.mention) {
				t.Errorf("failure = %v, want git naming %q", err, tc.mention)
			}
			if !slices.Equal(w.branches(t), before) || w.gateRuns != 0 {
				t.Errorf("branches %v, were %v; %d gate runs: nothing is created and nothing is parked", w.branches(t), before, w.gateRuns)
			}
			if now, _ := w.repo.Git.CurrentBranch(context.Background()); now != tc.branch {
				t.Errorf("checked out %q", now)
			}
			if _, there := w.read("docs/readme.md"); !there {
				t.Error("the change was touched")
			}
		})
	}
}

// TestFixLandHasNothingToLandWhenNothingChanged guards a land that has no change in the
// checkout: it is a usage failure, with nothing made.
func TestFixLandHasNothingToLandWhenNothingChanged(t *testing.T) {
	w := newRunWorld(t)
	w.edit(t, ".specs/a.md", "# spec, rewritten\n") // only a protected path changed
	w.submit(t, helloPayload)

	_, err := w.run(LandBranch)

	if f := failure(t, err); f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, "no change") {
		t.Errorf("failure = %v, want usage that says there is no change", err)
	}
	if !slices.Equal(w.branches(t), []string{"main"}) || w.gateRuns != 0 {
		t.Errorf("branches %v, %d gate runs", w.branches(t), w.gateRuns)
	}
}

// TestFixLandMakesTheIssueFromTheOriginalReportThatTheRunKept guards 09-REQ-8.1 for a land:
// the report of a text is in the run directory, and the issue is made from it.
func TestFixLandMakesTheIssueFromTheOriginalReportThatTheRunKept(t *testing.T) {
	w := newRunWorld(t)
	w.state.Input = StateInput{Kind: "text", Key: "k"}
	if err := os.WriteFile(filepath.Join(w.runDir, "original.md"), []byte("the greeting says helo"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.edit(t, "docs/readme.md", "readme, changed\n")
	w.submit(t, helloPayload)

	got, err := w.run(LandPR)

	if err != nil || got.Result.IssueCreated == nil {
		t.Fatalf("result %+v, %v", got.Result, err)
	}
	if issue := w.forge.Issue(got.Result.IssueCreated.Number); !strings.Contains(issue.Body, "the greeting says helo") || issue.Title != "say hello" {
		t.Errorf("issue = %+v", issue)
	}
	if prs := w.forge.PullRequests(); len(prs) != 1 || !strings.Contains(prs[0].Body, "cost $0.0000 (none)") {
		t.Errorf("pull requests = %+v, want a footer that says no round was paid for", prs)
	}
}

// TestWhatTheForgeAndTheRemoteNeedAreCheckedBeforeAnythingIsMeasured guards the order of a
// land: a run that cannot push is told so before the gate has been run on the change.
func TestWhatTheForgeAndTheRemoteNeedAreCheckedBeforeAnythingIsMeasured(t *testing.T) {
	w := newMeasureWorldWith(t, measuredFiles(), false) // no origin
	rw := &runWorld{measureWorld: w, rep: &recorder{}, runID: "20261020_100000_aaaaaa"}
	rw.runDir = filepath.Join(w.repo.Dir, ".agent-fox", "run", rw.runID)
	if err := os.MkdirAll(rw.runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rw.state.RunID = rw.runID
	rw.edit(t, "docs/readme.md", "readme, changed\n")
	rw.submit(t, helloPayload)

	_, err := rw.run(LandBranch)

	if f := failure(t, err); f.Category != toolio.CategoryPreflight || !strings.Contains(f.Message, "origin") || rw.gateRuns != 0 {
		t.Errorf("failure %v, %d gate runs, want the missing origin, before the gate", err, rw.gateRuns)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestRecordMeasurementNotesTheFactsAndTheRefusal guards the ledger of a measuring: the
// verdict, the gate after, the outcome of the revert check, whether it closes, the paths, and
// the refusal when there is one.
func TestRecordMeasurementNotesTheFactsAndTheRefusal(t *testing.T) {
	var kinds []string
	var data []map[string]any
	record := func(kind string, d any) {
		kinds = append(kinds, kind)
		raw, _ := json.Marshal(d)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		data = append(data, m)
	}

	RecordMeasurement(record, Measurement{Verdict: "pass", Closes: true, After: &GateRef{GateID: "g2"}, RevertCheck: &RevertRef{Outcome: "proven"}, Changed: []string{"a"}, Reverted: []string{"b"}})
	RecordMeasurement(record, Measurement{Verdict: "regressed", Refusal: &Refusal{Kind: RefusalGate}})
	RecordMeasurement(nil, Measurement{})

	if !slices.Equal(kinds, []string{"measure", "measure", "refusal"}) {
		t.Fatalf("kinds = %v", kinds)
	}
	first := data[0]
	if first["verdict"] != "pass" || first["after_gate_id"] != "g2" || first["revert_outcome"] != "proven" || first["closes"] != true || len(first["changed"].([]any)) != 1 || len(first["reverted"].([]any)) != 1 {
		t.Errorf("measure = %v", first)
	}
	if _, has := data[1]["after_gate_id"]; has || data[2]["kind"] != "gate" {
		t.Errorf("a refusal: measure %v, refusal %v", data[1], data[2])
	}
}
