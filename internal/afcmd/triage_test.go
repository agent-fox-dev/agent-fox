package afcmd

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// steps are the names of the step events of a run, in order.
func steps(t *testing.T, runDir string) []string {
	t.Helper()
	var names []string
	for _, ev := range testutil.JSONLines(t, filepath.Join(runDir, "events.jsonl")) {
		if data, _ := ev["data"].(map[string]any); ev["type"] == "step" {
			names = append(names, data["name"].(string))
		}
	}
	return names
}

// triageLedger are the triage records of the ledger of a run, in order.
func triageLedger(t *testing.T, runDir string) []string {
	t.Helper()
	var out []string
	for _, kind := range ledger(t, runDir) {
		if strings.HasPrefix(kind, "triage.") {
			out = append(out, kind)
		}
	}
	return out
}

func TestTriage_LandsTheDiagnosisOnTheIssueAndSuggestsTheFix(t *testing.T) {
	e := newEnv(t, repoFiles(), false)

	o := e.triage(e.forge.IssueURL(7), append(scripted(t, submitStep(diagnosis(nil), "accepted")), "--label", "bug", "--label", "bug")...)

	if o.code != 0 || o.res()["action"] != "replaced_body" || !strings.HasPrefix(o.Summary, "replaced the body of "+e.forge.IssueURL(7)) {
		t.Fatalf("%v, summary %q; want the body of the issue replaced", o, o.Summary)
	}
	if issue := e.forge.Issue(7); !strings.Contains(issue.Body, "src/greet.sh prints helo.") || !slices.Equal(issue.Labels, []string{"bug"}) {
		t.Errorf("the issue is %+v, want the diagnosis and the label bug once", issue)
	}
	dir := e.runDir(o)
	if got := steps(t, dir); !slices.Equal(got, []string{"preflight", "brief", "brain", "land"}) {
		t.Errorf("steps %v", got)
	}
	if got := triageLedger(t, dir); !slices.Equal(got, []string{"triage.input", "triage.brief", "triage.submission", "triage.land"}) {
		t.Errorf("ledger %v", got)
	}
	file := testutil.Decode[submission.File](t, []byte(testutil.ReadFile(t, filepath.Join(dir, "submission.json"))))
	if got := ledgerData(t, dir, "triage.submission"); got["sha256"] != submission.PayloadSHA256(file.Payload) {
		t.Errorf("triage.submission = %v, want the hash of the payload of submission.json", got)
	}
	if o.artifacts("brief") != 1 || o.artifacts("submission") != 1 {
		t.Errorf("artifacts %v, want the brief and the submission once each", o.Artifacts)
	}
	if want := []string{"af", "fix", e.forge.IssueURL(7), "--dir", e.dir}; len(o.Next) != 1 || !slices.Equal(o.Next[0].Argv, want) {
		t.Errorf("next %v, want %v", o.Next, want)
	}
}

func TestTriage_LandsWhereItIsToldAndSaysSo(t *testing.T) {
	for name, tc := range map[string]struct {
		text    bool
		args    []string
		noToken bool
		action  string
		summary string
		writes  int
	}{
		"a text with a repository": {true, []string{"--repo", "o/r"}, false, "created_issue", "created ", 1},
		"--land none":              {false, []string{"--land", "none"}, false, "none", "wrote the diagnosis to diagnosis.md only", 0},
		"a dry run, with no token": {false, []string{"--dry-run"}, true, "planned", "planned the writes of the diagnosis; a dry run sent none", 0},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, repoFiles(), false)
			if tc.noToken {
				e.cfg.Forge = e.forge.NewForge("")
			}
			input := e.forge.IssueURL(7)
			if tc.text {
				input = "the greeting says helo"
			}

			o := e.triage(input, append(scripted(t, submitStep(diagnosis(nil), "accepted")), tc.args...)...)

			if o.code != 0 || o.res()["action"] != tc.action || !strings.HasPrefix(o.Summary, tc.summary) {
				t.Fatalf("%v, summary %q; want the action %s and the summary %q", o, o.Summary, tc.action, tc.summary)
			}
			if writes := len(e.forge.Writes()); writes != tc.writes || e.git("status", "--porcelain") != "" {
				t.Errorf("%d writes to the forge, git status %q; want %d, and the checkout as it was", writes, e.git("status", "--porcelain"), tc.writes)
			}
			if _, err := os.Stat(filepath.Join(e.runDir(o), "diagnosis.md")); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestTriage_AFailedLandingStillListsWhatTheRunMade(t *testing.T) {
	e := newEnv(t, repoFiles(), false)
	e.forge.Fail(fakeforge.OpBody, fakeforge.Failure{Status: 500, Message: "boom"})

	o := e.triage(e.forge.IssueURL(7), scripted(t, submitStep(diagnosis(nil), "accepted"))...)

	if o.code != 1 || o.category() != "forge" || o.artifacts("brief") != 1 || o.artifacts("submission") != 1 || o.artifacts("diagnosis") != 1 {
		t.Errorf("%v, artifacts %v; want failed/forge with the brief, the submission and the diagnosis", o, o.Artifacts)
	}
}

func TestTriage_AQuestionStopsTheRunAndTheAnswerResumesIt(t *testing.T) {
	e := newEnv(t, repoFiles(), false)
	question := diagnosis(func(m map[string]any) {
		m["confidence"] = "suspected"
		m["question"] = map[string]any{"text": "Which session?", "options": []any{"old", "new"}}
	})

	o := e.triage(e.forge.IssueURL(7), scripted(t, submitStep(question, "accepted"))...)

	if o.code != 3 || o.NeedsHuman == nil || o.NeedsHuman.Question != "Which session?" || len(o.NeedsHuman.Options) != 2 || o.NeedsHuman.Resume.Flag != "--context" {
		t.Fatalf("%v, needs_human %+v; want the question with its two options", o, o.NeedsHuman)
	}
	if got := triageLedger(t, e.runDir(o)); !slices.Equal(got, []string{"triage.input", "triage.brief", "triage.submission", "triage.question"}) || len(e.forge.Writes()) != 0 {
		t.Errorf("ledger %v and %d writes, want the question recorded and nothing landed", got, len(e.forge.Writes()))
	}

	spy := e.spy(&spyBrain{})
	e.triage(e.forge.IssueURL(7), "--brain", "spy", "--land", "none", "--context", "B")
	if spy.brief == nil || !strings.Contains(spy.brief.Task, "Which session?") {
		t.Errorf("the brief of the run that answers does not hold the question it answers: %+v", spy.brief)
	}
}

func TestTriage_TheQuestionsOfTheCallerFrameTheContext(t *testing.T) {
	e := newEnv(t, repoFiles(), false)
	spy := e.spy(&spyBrain{})
	e.cfg.Earlier = &Earlier{Questions: []*toolio.Question{{Text: "Question one?", Options: []string{"x"}}, {Text: "Question two?", Options: []string{"y", "z"}}, nil}}

	o := e.triage(e.forge.IssueURL(7), "--brain", "spy", "--land", "none", "--context", "first", "--context", "second", "--context", "third")

	if spy.brief == nil {
		t.Fatalf("the brain did not run: %v", o)
	}
	brief := spy.brief.Prefix + spy.brief.Task
	if strings.Count(brief, "The question you asked:") != 1 || !strings.Contains(brief, "Question two?") || strings.Contains(brief, "Question one?") {
		t.Errorf("the brief does not hold exactly the question of the last value that has one:\n%s", brief)
	}
	if strings.Count(brief, "A person's answer (") != 2 || !strings.Contains(brief, "Material from a person (3 of 3)") {
		t.Errorf("the values are not two answers and one piece of material:\n%s", brief)
	}
}

func TestTriage_RefusesBeforeTheBriefAndSpendsNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		input    func(e *env) string
		args     []string
		noToken  bool
		category string
		mention  string
	}{
		"a label that is not one": {nil, []string{"--label", "a,b"}, false, "usage", "a,b"},
		"a pull request": {func(e *env) string { return strings.Replace(e.forge.IssueURL(7), "/issues/", "/pull/", 1) }, nil, false, "usage",
			"pull or merge request"},
		"an unknown brain":          {nil, []string{"--brain", "nosuch"}, false, "usage", "nosuch"},
		"an unknown sandbox":        {nil, []string{"--sandbox", "nosuch"}, false, "usage", "nosuch"},
		"no credential":             {nil, nil, true, "auth", "credential"},
		"a text with no repository": {func(*env) string { return "the greeting says helo" }, nil, false, "usage", "--land none"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, repoFiles(), false)
			spy := e.spy(&spyBrain{})
			if tc.noToken {
				e.cfg.Forge = e.forge.NewForge("")
			}
			input := e.forge.IssueURL(7)
			if tc.input != nil {
				input = tc.input(e)
			}

			o := e.triage(input, append([]string{"--brain", "spy"}, tc.args...)...)

			if o.code == 0 || o.category() != tc.category || !strings.Contains(o.message(), tc.mention) {
				t.Fatalf("%v; want a %s refusal that mentions %q", o, tc.category, tc.mention)
			}
			dir := e.runDir(o)
			if _, err := os.Stat(filepath.Join(dir, "brief.md")); err == nil || spy.made != 0 || e.opened.Load() != 0 || len(e.forge.Writes()) != 0 {
				t.Errorf("a brief (%v), %d brains made, %d boxes, %d writes: a refused run spends nothing", err, spy.made, e.opened.Load(), len(e.forge.Writes()))
			}
			if got := steps(t, dir); !slices.Equal(got, []string{"preflight"}) || !slices.Equal(triageLedger(t, dir), []string{"triage.input"}) {
				t.Errorf("steps %v, ledger %v; want the preflight, and the input recorded", got, triageLedger(t, dir))
			}
		})
	}
}

func TestTriage_TheBrainGetsTheBriefTheToolsToReadWithAndTheBounds(t *testing.T) {
	e := newEnv(t, repoFiles(), false)
	spy := e.spy(&spyBrain{})
	e.cfg.BoxLabels = map[string]string{"af.job": "job_1"}

	o := e.triage(e.forge.IssueURL(7), "--brain", "spy", "--budget", "3", "--max-turns", "40", "--timeout", "10m")

	b := spy.brief
	if o.code != 1 || o.category() != "no_result" || b == nil || o.artifacts("brief") != 1 {
		t.Fatalf("%v; want failed/no_result from a brain that submitted nothing, with the brief listed", o)
	}
	dir := e.runDir(o)
	if brief := testutil.ReadFile(t, filepath.Join(dir, "brief.md")); b.Prefix+b.Task != brief || b.Prefix == "" {
		t.Errorf("the brain was not given brief.md")
	}
	if !slices.Contains(b.Tools, "read_file") || slices.Contains(b.Tools, "write_file") || slices.Contains(b.Tools, "execute") || b.Kind.Name != "triage" {
		t.Errorf("tools %v, kind %q; want the tools that read, for a triage", b.Tools, b.Kind.Name)
	}
	if l := b.Box.Spec().Labels; l["af.tool"] != "triage" || l["af.run_id"] != *o.RunID || l["af.job"] != "job_1" {
		t.Errorf("the box is labelled %v", l)
	}
	if b.MaxCostUSD != 3 || b.MaxTurns != 40 || b.Timeout <= 0 || b.Timeout > 10*time.Minute || !strings.Contains(b.Task, "You have at most 40 turns, $3.00 of model cost") {
		t.Errorf("bounds: cost %v, turns %d, timeout %v; task:\n%s", b.MaxCostUSD, b.MaxTurns, b.Timeout, b.Task)
	}
	if got := steps(t, dir); !slices.Equal(got, []string{"preflight", "brief", "brain"}) {
		t.Errorf("steps %v", got)
	}
}

func TestTriage_ASubmissionTheCheckoutNoLongerBearsOutIsTakenAway(t *testing.T) {
	e := newEnv(t, repoFiles(), false)
	submit := submitting(diagnosis(nil))
	e.spy(&spyBrain{run: func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		out, err := submit(ctx, b)
		testutil.WriteFile(t, e.dir, "tests/run.sh", "one line\n") // the line the diagnosis cites is gone
		return out, err
	}})

	o := e.triage(e.forge.IssueURL(7), "--brain", "spy")

	if o.code != 1 || o.category() != "no_result" || !o.Error.Retryable || !strings.Contains(o.message(), "/affected_files/0") || !o.warned("submission_invalid") {
		t.Fatalf("%v; want a retryable no_result that names the field", o)
	}
	if _, err := os.Stat(filepath.Join(e.runDir(o), "submission.json")); err == nil || o.artifacts("submission") != 0 || len(e.forge.Writes()) != 0 {
		t.Errorf("the submission is still there (%v), listed %d times, or landed", err, o.artifacts("submission"))
	}
}
