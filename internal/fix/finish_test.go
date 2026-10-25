package fix

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// finishing is the end of a run of issue 7 in the world, whose checkout holds the change a
// round applied, after the loop came to ended.
func (w *landWorld) finishing(mode string, ended LoopResult) FinishParams {
	input := w.withIssue()
	return FinishParams{
		Git: w.repo.Git, State: w.state, Input: input, Mode: mode, Forge: w.client, Target: w.target,
		Ended: ended, Reporter: w.rep, Box: &BoxInfo{Provider: "local"}, WriteReport: func(string) error { return nil },
	}
}

func reportOf(r Report) *Report { return &r }

func TestFinish_ALandableChangeLandsThroughThePostPushHook(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	h := &postPush{w: w, t: t}
	p := w.finishing(LandPR, LoopResult{End: EndLandable, Report: reportOf(addX()), Measured: proven(), HoldsChange: true, Rounds: 2})
	p.PostPush = h.hook

	end, err := Finish(context.Background(), p)

	r := end.Result
	if err != nil || end.Status != toolio.StatusDone || r.Outcome != OutcomeLanded || r.Rounds != 2 || r.Branch == nil || *r.Branch != "fix/7-greeting-wrong" || r.Commit == nil || !r.Pushed || r.PullRequest == nil || r.Box == nil || !r.Closes {
		t.Fatalf("Finish = %+v (result %+v), %v, want a landed change", end, r, err)
	}
	if end.Summary != "landed the change on the branch fix/7-greeting-wrong, pull request "+r.PullRequest.URL || len(h.branches) != 1 || h.pulls[0] != 0 {
		t.Errorf("summary %q, hook calls %v, want the branch and the pull request, opened after the hook", end.Summary, h.branches)
	}
	w.onBase(t)
}

// A run whose bounds are spent, or whose last round failed, parks the change in the tree under
// the last refusal; with no change in it, the run fails.
func TestFinish_AnExhaustedRunParksItsChangeOrFails(t *testing.T) {
	refusal := &Measurement{Verdict: "still_failing", Changed: []string{"src/greet.sh"}, Refusal: &Refusal{Kind: RefusalGate, Failing: []FailingCheck{{Name: "make check", Tail: "FAIL"}}}}
	broke := &toolio.Failure{Category: toolio.CategoryAuth, Message: "no key"}
	for name, tc := range map[string]struct {
		ended    LoopResult
		verdict  string // of the parked commit; "" for none
		category string // of the failure; "" for none
		warn     string
	}{
		"refused":                         {LoopResult{End: EndExhausted, Report: reportOf(addX()), HoldsChange: true, LastRefusal: refusal}, "still_failing", "", ""},
		"a last round that failed":        {LoopResult{End: EndExhausted, HoldsChange: true, LastRefusal: refusal, LastFailure: broke}, "still_failing", "", WarnAttemptFailed},
		"a round that failed, unmeasured": {LoopResult{End: EndExhausted, HoldsChange: true, LastFailure: broke}, "unverified", "", WarnAttemptFailed},
		"no change and a failure":         {LoopResult{End: EndExhausted, LastFailure: broke}, "", toolio.CategoryAuth, ""},
		"no change at all":                {LoopResult{End: EndExhausted}, "", toolio.CategoryNoResult, ""},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			if !tc.ended.HoldsChange {
				w.repo.git(t, "checkout", "-q", "--", "src/greet.sh")
			}
			h := &postPush{w: w, t: t}
			p := w.finishing(LandPR, tc.ended)
			p.PostPush = h.hook

			end, err := Finish(context.Background(), p)

			if tc.category != "" {
				if f := failure(t, err); f.Category != tc.category || len(w.repo.branches(t)) != 1 {
					t.Errorf("Finish = %v, branches %v, want %s and nothing parked", err, w.repo.branches(t), tc.category)
				}
				return
			}
			if err != nil || end.Status != toolio.StatusUnverified || end.Result.Outcome != OutcomeParked || end.Result.PullRequest == nil || len(h.branches) != 1 {
				t.Fatalf("Finish = %+v, %v, hook calls %v, want the change parked and offered as a draft", end, err, h.branches)
			}
			msg := w.repo.message(t, *end.Result.Branch)
			if !strings.HasPrefix(msg, "wip: ") || !strings.Contains(msg, "verdict "+tc.verdict) || (tc.ended.LastRefusal != nil) != strings.Contains(msg, "- make check") {
				t.Errorf("message = %q, want the verdict %s and the failing checks of the refusal", msg, tc.verdict)
			}
			if _, warned := w.rep.warning(WarnAttemptFailed); warned != (tc.warn != "") {
				t.Errorf("warnings = %v, want %q", w.rep.warnings, tc.warn)
			}
			w.onBase(t)
		})
	}
}

// The run starts again from the base with the answer, so the change an earlier round applied
// and that was refused goes, unless someone moved the checkout since.
func TestFinish_AQuestionPutsTheCheckoutBackAsTheRunFoundIt(t *testing.T) {
	question := &Question{Text: "Which?", Options: []string{"x", "y"}}
	for name, tc := range map[string]struct {
		holds, moved bool
		warn         []string
	}{
		"after a round that applied a change": {true, false, nil},
		"in the first round":                  {false, false, nil},
		"on a checkout that was moved":        {true, true, []string{WarnQuestionTreeKept}},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			head := w.state.BaseHead
			switch {
			case tc.moved:
				head = testutil.Commit(t, w.repo.Dir, "someone else's commit")
			case !tc.holds:
				w.repo.git(t, "checkout", "-q", "--", "src/greet.sh")
			}

			end, err := Finish(context.Background(), w.finishing(LandPR, LoopResult{End: EndQuestion, Question: question, HoldsChange: tc.holds, Rounds: 2}))

			if err != nil || end.Status != toolio.StatusNeedsHuman || end.Question != question || end.Result.Outcome != OutcomeNeedsHuman || end.Result.Rounds != 2 {
				t.Fatalf("Finish = %+v, %v, want needs_human with the question", end, err)
			}
			if !slices.Equal(w.rep.warnings, tc.warn) || len(w.forge.Writes()) != 0 || w.repo.git(t, "rev-parse", "HEAD") != head {
				t.Errorf("warnings %v, %d writes, HEAD %s, want %v, none, and %s", w.rep.warnings, len(w.forge.Writes()), w.repo.git(t, "rev-parse", "HEAD"), tc.warn, head)
			}
			if !tc.moved {
				w.onBase(t)
			}
		})
	}
}

// A run that was stopped, in its loop or while it landed, keeps what it applied on a local
// branch, on a context the stop did not reach, and returns the stop; a loop that failed
// otherwise leaves the work where it is.
func TestFinish_AStoppedRunParksItsChangeLocally(t *testing.T) {
	landable := LoopResult{End: EndLandable, Report: reportOf(addX()), Measured: proven(), HoldsChange: true, Rounds: 1}
	for name, tc := range map[string]struct {
		loopErr error
		cancel  bool
		parked  bool
	}{
		"stopped in the loop":    {context.Canceled, false, true},
		"out of time":            {context.DeadlineExceeded, false, true},
		"stopped while it lands": {nil, true, true},
		"failed to measure":      {&toolio.Failure{Category: toolio.CategoryGit, Message: "moved"}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancel {
				cancel()
			}
			defer cancel()
			p := w.finishing(LandPR, landable)
			p.LoopErr = tc.loopErr

			_, err := Finish(ctx, p)

			if want := tc.loopErr; (want != nil && !errors.Is(err, want)) || (want == nil && !errors.Is(err, context.Canceled)) {
				t.Errorf("Finish = %v, want the cause", err)
			}
			branches := w.repo.branches(t)
			if (len(branches) == 2) != tc.parked || len(w.forge.Writes()) != 0 {
				t.Fatalf("branches %v, %d writes, want parked: %v, and nothing sent", branches, len(w.forge.Writes()), tc.parked)
			}
			if !tc.parked {
				if w.repo.read(t, "src/greet.sh") != "echo hello, world\n" {
					t.Error("the change is not in the tree")
				}
				return
			}
			branch := slices.DeleteFunc(branches, func(b string) bool { return b == "main" })[0]
			if subject := w.repo.git(t, "log", "-1", "--format=%s", branch); subject != "wip: Add x" || w.repo.remoteHas(t, branch) || !strings.Contains(w.repo.message(t, branch), "verdict interrupted") {
				t.Errorf("%s holds %q, want a local wip commit under the verdict interrupted", branch, subject)
			}
			w.onBase(t)
		})
	}

	w := newLandWorld(t, fakeforge.GitHub)
	w.repo.git(t, "checkout", "-q", "--", "src/greet.sh")
	p := w.finishing(LandPR, LoopResult{End: EndExhausted})
	p.LoopErr = context.Canceled
	if _, err := Finish(context.Background(), p); !errors.Is(err, context.Canceled) || len(w.rep.warnings) != 0 {
		t.Errorf("a stop with nothing to park: %v, warnings %v, want the stop and no warning", err, w.rep.warnings)
	}
}

func TestRecordMeasurement_NotesTheFactsAndTheRefusal(t *testing.T) {
	var kinds []string
	var data []map[string]any
	record := func(kind string, d any) {
		kinds = append(kinds, kind)
		data = append(data, testutil.Decode[map[string]any](t, testutil.JSON(t, d)))
	}

	RecordMeasurement(record, Measurement{Verdict: "pass", Closes: true, After: &GateRef{GateID: "g2"}, RevertCheck: &RevertRef{Outcome: "proven"}, Changed: []string{"a"}, Reverted: []string{"b"}})
	RecordMeasurement(record, Measurement{Verdict: "regressed", Refusal: &Refusal{Kind: RefusalGate}})
	RecordMeasurement(nil, Measurement{})

	if !slices.Equal(kinds, []string{LedgerMeasure, LedgerMeasure, LedgerRefusal}) {
		t.Fatalf("kinds = %v", kinds)
	}
	if m := data[0]; m["verdict"] != "pass" || m["after_gate_id"] != "g2" || m["revert_outcome"] != "proven" || m["closes"] != true || len(m["changed"].([]any)) != 1 || len(m["reverted"].([]any)) != 1 {
		t.Errorf("measure = %v", m)
	}
	if _, has := data[1]["after_gate_id"]; has || data[2]["kind"] != RefusalGate {
		t.Errorf("a refusal: measure %v, refusal %v", data[1], data[2])
	}
}
