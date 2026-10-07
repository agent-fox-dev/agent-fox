package codefix

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agentfox/agentkit-go/tools"
)

// editThenFail is a brain whose implementation phase writes its change and
// then fails, as a phase cancelled or cut off by the provider does.
type editThenFail struct {
	*scriptedBrain
	err error
}

func (b *editThenFail) Implement(ctx context.Context, in implementInput) (Implementation, agentrun.Result, error) {
	impl, res, err := b.scriptedBrain.Implement(ctx, in)
	if err != nil {
		return impl, res, err
	}
	return Implementation{}, res, b.err
}

// assertParked checks the state a parked run leaves: the checkout on main
// with a clean tree, and the branch's tip a wip: commit carrying the change.
func assertParked(t *testing.T, ws *tools.Workspace, branch string) {
	t.Helper()
	if cur := gitOutFix(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
	if dirty := gitOutFix(t, ws.Root, "status", "--porcelain"); dirty != "" {
		t.Errorf("the tree is dirty after the run:\n%s", dirty)
	}
	if subject := gitOutFix(t, ws.Root, "log", "-1", "--format=%s", branch); !strings.HasPrefix(subject, "wip:") {
		t.Errorf("%s tip = %q, want a wip: commit", branch, subject)
	}
	if body := gitOutFix(t, ws.Root, "show", branch+":count.go"); !strings.Contains(body, "fixed") {
		t.Errorf("the change is not on %s: count.go = %q", branch, body)
	}
}

// Issue #216 (1): an implementation phase that fails after writing files —
// Ctrl-C, --phase-timeout, a provider error — parks the work, so the identical
// command can be run again.
func TestAnImplementPhaseThatFailsAfterEditingParksTheWork(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := &editThenFail{scriptedBrain: defaultBrain(), err: &agentrun.Error{Phase: "implement",
		Cat: agentrun.CategoryAborted, Cause: context.Canceled}}
	res, err := Run(context.Background(), newOptions(ws, g, b))
	var f *Failure
	if !errors.As(err, &f) || f.Category != agentrun.CategoryAborted || !f.Parked {
		t.Fatalf("failure = %+v (%v), want a parked aborted run", f, err)
	}
	assertParked(t, ws, res.Branch)
}

// Issue #216 (2): a check the cancellation killed is aborted, not the
// command's failure: the run is not "regressed", and the work is parked under
// a context the cancellation does not reach.
func TestACancelledVerificationIsAbortedNotRegressed(t *testing.T) {
	ws, g := newRepo(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := newOptions(ws, g, defaultBrain())
	calls := 0
	o.CheckRunner = func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		calls++
		if calls == 2 { // the verification after the change
			cancel()
			<-c.Done()
			return "", -1, nil
		}
		return gitx.ExecRunner(c, dir, argv, stdin...)
	}
	res, err := Run(ctx, o)
	var f *Failure
	if !errors.As(err, &f) || f.Category != agentrun.CategoryAborted || !f.Parked {
		t.Fatalf("failure = %+v (%v), want a parked aborted run", f, err)
	}
	if res.Verdict == "regressed" {
		t.Error("a cancelled check was reported as a regression")
	}
	assertParked(t, ws, res.Branch)
}

// Issue #216 (3): while the revert check has the fix taken out, the fix is
// held in a commit, not only in the process's memory; once the check is done
// the change lands as one commit.
func TestTheRevertCheckRunsWithTheChangeCommitted(t *testing.T) {
	ws, g := newRepo(t, 0)
	o := newOptions(ws, g, defaultBrain())
	var mu sync.Mutex
	calls := 0
	var held string
	o.CheckRunner = func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 3 { // the revert check
			out, _, _ := gitx.ExecRunner(c, dir, []string{"git", "show", "HEAD:count.go"})
			held = out
		}
		return gitx.ExecRunner(c, dir, argv, stdin...)
	}
	res, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(held, "fixed") {
		t.Errorf("during the revert check HEAD:count.go = %q; the fix was held only in memory", held)
	}
	if res.RevertCheck == nil || !res.RevertCheck.Proves {
		t.Errorf("revert check = %+v", res.RevertCheck)
	}
	if log := gitOutFix(t, ws.Root, "log", "--format=%s", "main.."+res.Branch); strings.Contains(log, "wip:") ||
		strings.Count(log, "\n") != 0 {
		t.Errorf("the branch carries %q, want the one landed commit", log)
	}
	if body, _ := os.ReadFile(filepath.Join(ws.Root, "count.go")); !strings.Contains(string(body), "fixed") {
		t.Errorf("count.go = %q after the run", body)
	}
}

// Issue #216 (3): a revert check that is cancelled ends the run as aborted,
// with the fix parked on the branch rather than lost.
func TestACancelledRevertCheckParksTheFix(t *testing.T) {
	ws, g := newRepo(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := newOptions(ws, g, defaultBrain())
	calls := 0
	o.CheckRunner = func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		calls++
		if calls == 3 { // the revert check
			cancel()
			<-c.Done()
			return "", -1, nil
		}
		return gitx.ExecRunner(c, dir, argv, stdin...)
	}
	res, err := Run(ctx, o)
	var f *Failure
	if !errors.As(err, &f) || f.Category != agentrun.CategoryAborted || !f.Parked {
		t.Fatalf("failure = %+v (%v), want a parked aborted run", f, err)
	}
	assertParked(t, ws, res.Branch)
}

// Issue #216 (1): a run that changed nothing has nothing to park, and returns
// the checkout to the base branch.
func TestAnEmptyChangeReturnsTheCheckout(t *testing.T) {
	ws, g := newRepo(t, 0)
	b := defaultBrain()
	b.edit = nil
	_, err := Run(context.Background(), newOptions(ws, g, b))
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryEmpty {
		t.Fatalf("failure = %+v (%v)", f, err)
	}
	if cur := gitOutFix(t, ws.Root, "rev-parse", "--abbrev-ref", "HEAD"); cur != "main" {
		t.Errorf("checked out %q, want main", cur)
	}
}
