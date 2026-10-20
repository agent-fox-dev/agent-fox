package fix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// finishing is the parameters of the end of a run in the world: its checkout holds a change
// that a round applied, and Ended is what the loop came to.
func (w *landWorld) finishing(mode string, ended LoopResult) FinishParams {
	return FinishParams{
		Git: w.repo.Git, State: w.state, Input: Input{Kind: "issue", Origin: w.state.Issue.URL, Body: "x"}, Mode: mode, Forge: w.client, Target: w.target,
		Ended: ended, AttemptsMax: 2, Reporter: w.rep, WriteReport: func(string) error { return nil },
	}
}

// TestAQuestionAfterAnAppliedRoundPutsTheCheckoutBack guards 09-REQ-9.6 for a run that asks
// in a later round: the change that an earlier round applied and that was refused is not the
// checkout's to keep, since the run starts again from the base with the answer, and it could
// not otherwise: the tree is not clean.
func TestAQuestionAfterAnAppliedRoundPutsTheCheckoutBack(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "a run", true: "a dry run"}[dry], func(t *testing.T) {
			w := newLandWorld(t, fakeforge.GitHub)
			w.issueInput()
			p := w.finishing(LandPR, LoopResult{End: EndQuestion, Question: &Question{Text: "Which?", Options: []string{"x", "y"}}, HoldsChange: true, Attempts: 1, Rounds: 2})
			p.DryRun = dry

			end, err := Finish(context.Background(), p)

			if err != nil || end.Status != toolio.StatusNeedsHuman || end.Question == nil {
				t.Fatalf("ending %+v, %v, want needs_human with the question", end, err)
			}
			w.onBase(t)
			if head := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD")); head != w.state.BaseHead {
				t.Errorf("HEAD = %s, want the base %s", head, w.state.BaseHead)
			}
			if data, _ := os.ReadFile(filepath.Join(w.repo.Dir, "src", "greet.sh")); string(data) != "echo hello\n" {
				t.Errorf("greet.sh = %q, want the base's content: the refused change goes", data)
			}
			if len(w.rep.warnings) != 0 || len(w.forge.Writes()) != 0 {
				t.Errorf("warnings %v, %d writes to the forge, want none", w.rep.warnings, len(w.forge.Writes()))
			}
		})
	}

	t.Run("a checkout that was moved is left as it is", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()
		gitDo(t, w.repo.Dir, "commit", "-q", "-am", "chore: someone else's commit")
		moved := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD"))

		_, err := Finish(context.Background(), w.finishing(LandPR, LoopResult{End: EndQuestion, Question: &Question{Text: "Which?"}, HoldsChange: true}))

		if err != nil {
			t.Fatal(err)
		}
		if head := strings.TrimSpace(gitDo(t, w.repo.Dir, "rev-parse", "HEAD")); head != moved {
			t.Errorf("HEAD = %s, want it left at %s: the checkout is no longer where the run began", head, moved)
		}
		if len(w.rep.warnings) != 1 || w.rep.warnings[0] != WarnQuestionTreeKept {
			t.Errorf("warnings = %v, want %s", w.rep.warnings, WarnQuestionTreeKept)
		}
	})

	t.Run("a question in the first round has nothing to put back", func(t *testing.T) {
		w := newLandWorld(t, fakeforge.GitHub)
		w.issueInput()
		gitDo(t, w.repo.Dir, "checkout", "-q", "--", "src/greet.sh")

		end, err := Finish(context.Background(), w.finishing(LandPR, LoopResult{End: EndQuestion, Question: &Question{Text: "Which?"}}))

		if err != nil || end.Status != toolio.StatusNeedsHuman || len(w.rep.warnings) != 0 {
			t.Errorf("ending %+v, %v, warnings %v", end, err, w.rep.warnings)
		}
		w.onBase(t)
	})
}

// TestAStopDuringTheLandingParksTheChangeLocally guards 09-REQ-7.6 for a run that is stopped
// while it lands: the landing fails, because its git commands are cancelled, and the change
// that was measured is parked on a local branch on a context the stop did not reach, and not
// left uncommitted in the tree of the base.
func TestAStopDuringTheLandingParksTheChangeLocally(t *testing.T) {
	w := newLandWorld(t, fakeforge.GitHub)
	w.issueInput()
	report := addX()
	p := w.finishing(LandPR, LoopResult{End: EndLandable, Report: &report, Measured: proven(), HoldsChange: true, Attempts: 1, Rounds: 1})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Finish(ctx, p)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the stop", err)
	}
	w.onBase(t)
	branches := strings.Fields(gitDo(t, w.repo.Dir, "for-each-ref", "--format=%(refname:short)", "refs/heads"))
	if len(branches) != 2 {
		t.Fatalf("branches = %v, want the base and the branch that holds the change", branches)
	}
	branch := branches[0]
	if branch == "main" {
		branch = branches[1]
	}
	if subject := strings.TrimSpace(gitDo(t, w.repo.Dir, "log", "-1", "--format=%s", branch)); !strings.HasPrefix(subject, "wip: ") {
		t.Errorf("the subject on %s is %q, want a wip commit", branch, subject)
	}
	if w.remoteHas(t, branch) || len(w.forge.Writes()) != 0 {
		t.Errorf("a stopped run pushed %s or wrote to the forge (%d writes)", branch, len(w.forge.Writes()))
	}
}
