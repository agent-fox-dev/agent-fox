package worker

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs/fakerunner"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// leakedPath finds an absolute path of a machine.
var leakedPath = regexp.MustCompile(`(^|[\s'"(\x60])/(tmp|var|private|Users|home|root|opt|workspace)/[^\s'")\x60]*`)

// The same attempt run again finds its marker on the issue; another attempt posts its own.
func TestComment_CarriesItsMarkerAndIsPostedOnce(t *testing.T) {
	e := newEnv(t, fakehub.Workspace{})
	e.endsWith("failed", fakerunner.WithError("git", "cannot commit in "+filepath.Join(e.dir, "clone")+"/src and "+e.dir+"/envelope.json", false))

	for _, attempt := range []int{1, 1, 2} {
		if _, err := e.run(e.fix(func(r *jobs.Request) { r.Attempt = attempt })); err != nil {
			t.Fatal(err)
		}
	}

	comments := e.issueNow().Comments
	if len(comments) != 2 {
		t.Fatalf("%d comments, want one for each attempt", len(comments))
	}
	for i, c := range comments {
		if marker := "<!-- agent-fox job=" + jobID + " attempt=" + string(rune('1'+i)) + " kind=failure -->"; !strings.Contains(c.Body, marker) {
			t.Errorf("comment %d has no marker %q:\n%s", i, marker, c.Body)
		}
		if m := leakedPath.FindString(c.Body); m != "" || strings.Contains(c.Body, e.dir) || !strings.Contains(c.Body, "cannot commit in <worker>/src") {
			t.Errorf("comment %d holds a path of the worker (%q):\n%s", i, m, c.Body)
		}
	}
}

// composer is an attempt of the kind on issue 42, with the label triggers on or off.
func composer(t *testing.T, kind jobs.Kind, triggersOn bool) (*attempt, issuex.IssueRef) {
	ref := issuex.IssueRef{Repo: issuex.Repo{Host: "github.example.com", Owner: "Org", Name: "Repo"}, Number: 42}
	return &attempt{
		req:      jobs.Request{JobID: "job_1", Attempt: 1, Workspace: slug, Kind: kind, Dir: t.TempDir()},
		settings: Settings{TriggersOn: triggersOn, Land: "pr"},
		issue:    &ref,
	}, ref
}

func parkedResult() loopResult { return loopResult{Outcome: "parked"} }

func TestCompose_NamesTheForgePathOnlyWhenTheLabelTriggersAreOn(t *testing.T) {
	question := toolio.Envelope{Status: toolio.StatusNeedsHuman, NeedsHuman: &toolio.NeedsHuman{Question: "Which greeting?"}}
	failed := toolio.Envelope{Status: toolio.StatusFailed, Error: &toolio.ErrorInfo{Category: "sandbox", Message: "gone"}}
	for _, kind := range []jobs.Kind{jobs.KindFix, jobs.KindTriage} {
		for _, on := range []bool{true, false} {
			a, ref := composer(t, kind, on)
			label := "`af:" + string(kind) + "`"
			for name, tc := range map[string]struct {
				text       string
				always, on string
			}{
				"question": {a.compose(commentQuestion, ref, question, loopResult{}), "Answer with `af jobs answer job_1`, or with `POST /api/v1/jobs/job_1/answer`.",
					" Or reply on this issue, then add the label " + label + " again: agent-fox takes the comments after this one as the answer."},
				"failure": {a.compose(commentFailure, ref, failed, loopResult{}), "To ask again, `POST /api/v1/jobs/job_1/retry`.",
					" Or add the label " + label + " again; comments after this one go with the new attempt."},
				"parked": {a.compose(commentFixSummary, ref, toolio.Envelope{}, parkedResult()), "`af jobs retry job_1`, or `POST /api/v1/jobs/job_1/retry`.",
					" Or add the label " + label + " again;"},
			} {
				if strings.Contains(tc.text, tc.on) != on || !strings.Contains(tc.text, tc.always) {
					t.Errorf("%s %s, triggers on %v:\n%s", kind, name, on, tc.text)
				}
			}
		}
	}
}

func TestCompose_TheFixSummarySaysWhatBecameOfTheChangeAndWhatWasNotProven(t *testing.T) {
	unmet := toolio.Warning{Code: "criteria_unmet", Message: "the usage is not printed, see /home/x/run.log"}
	for name, tc := range map[string]struct {
		land, result string
		warnings     []toolio.Warning
		says, not    []string
	}{
		"a draft pull request that closes": {"pr", `{"branch":"fix/42","verdict":"pass","closes":true,"pull_request":{"url":"` + forgeRepo + `/pull/9","draft":true}}`, nil,
			[]string{"**agent-fox fix: landed.**", "- Outcome: landed\n", "- Branch: `fix/42`", "- Pull request: " + forgeRepo + "/pull/9 (a draft)", "- Verdict: `pass`", "- Closes " + issueURL}, []string{"not proven"}},
		"a branch with no pull request":    {"branch", `{"branch":"fix/42","closes":true}`, nil, []string{"- Pull request: none: no pull request was asked for", "- Verdict: `none`"}, nil},
		"a change that was only measured":  {"none", `{"branch":"fix/42","closes":true}`, nil, []string{"measured, and nothing kept", "- Outcome: measured, nothing kept", "- Pull request: none\n"}, []string{"- Branch:"}},
		"a change the result says it kept": {"none", `{"land":"branch","closes":true}`, nil, []string{"- Outcome: landed"}, nil},
		"a change that is not proven": {"pr", `{"revert_check":{"outcome":"not_proven","reason":"tests_pass_without_the_change"}}`, []toolio.Warning{unmet},
			[]string{"- Refs " + issueURL, "What was not proven:\n- the change is not proven by a test of its own: the revert check says tests_pass_without_the_change\n- the usage is not printed, see <worker path>"}, nil},
		"a parked change": {"pr", `{"outcome":"parked","branch":"af/fix-42","verdict":"still_failing"}`,
			[]toolio.Warning{{Code: "fix_not_landed", Message: "the gate failed"}, {Code: "commit_hook_failed", Message: "the hook said no"}, {Code: "other", Message: "unrelated"}},
			[]string{"fix: parked.", "- Branch: `af/fix-42`", "no check showed that a test of its own fails", "What failed:\n- the verdict was `still_failing`\n- the gate failed\n- the hook said no\n"}, []string{"unrelated"}},
	} {
		a, ref := composer(t, jobs.KindFix, false)
		a.settings.Land = tc.land

		text := a.compose(commentFixSummary, ref, toolio.Envelope{Warnings: tc.warnings}, testutil.Decode[loopResult](t, []byte(tc.result)))

		for _, s := range tc.says {
			if !strings.Contains(text, s) {
				t.Errorf("%s: the summary lacks %q:\n%s", name, s, text)
			}
		}
		for _, s := range tc.not {
			if strings.Contains(text, s) {
				t.Errorf("%s: the summary holds %q:\n%s", name, s, text)
			}
		}
	}
}

// A route of the API and a URL are not paths of the worker.
func TestScrub_TakesThePathsOfTheWorkerOut(t *testing.T) {
	a := &attempt{cloneDir: "/srv/state/attempts/job_1/2/clone", req: jobs.Request{Dir: "/srv/state/attempts/job_1/2"}}
	home, _ := os.UserHomeDir()
	for text, want := range map[string]string{
		"cannot read /srv/state/attempts/job_1/2/clone/src/greet.sh": "cannot read <worker>/src/greet.sh",
		"wrote /srv/state/attempts/job_1/2/envelope.json":            "wrote <worker>/envelope.json",
		"in " + os.TempDir() + "/x/y":                                "in <worker>/x/y",
		"in " + home + "/go/pkg":                                     "in <worker>/go/pkg",
		"saw /opt/tools/bin/make and '/var/lib/x/y'":                 "saw <worker path> and '<worker path>'",
		"replace the token with PUT /api/v1/workspaces/acme":         "replace the token with PUT /api/v1/workspaces/acme",
		"see https://github.example.com/var/lib/x/issues/1":          "see https://github.example.com/var/lib/x/issues/1",
	} {
		if got := a.scrub(text); got != want {
			t.Errorf("scrub(%q) = %q, want %q", text, got, want)
		}
	}
}
