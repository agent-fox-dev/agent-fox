package worker

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// cloneGit runs a git command in the clone and returns what it printed. A command that exits with a
// code other than 0 is an error that carries what it printed.
func (a *attempt) cloneGit(ctx context.Context, args ...string) (string, error) {
	out, code, err := a.git(ctx, a.cloneDir, append([]string{"git"}, args...))
	if err == nil && code != 0 {
		err = fmt.Errorf("git %s exited %d: %s", args[0], code, gitDetail(a, out, nil))
	}
	return out, err
}

// pinnedRun is the pinned file of the run of a resumed fix.
func (a *attempt) pinnedRun() (fix.State, string, error) {
	runID := a.resume.journal.Run.ID
	state, err := fix.LoadState(filepath.Join(a.cloneDir, ".agent-fox", "run", runID))
	return state, runID, err
}

// runBranches are the local branches of the clone, but the base, whose tip carries the trailer of the run.
func (a *attempt) runBranches(ctx context.Context, base, runID string) ([]string, error) {
	listed, err := a.cloneGit(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, name := range strings.Fields(listed) {
		if name == base {
			continue
		}
		if msg, err := a.cloneGit(ctx, "log", "-1", "--format=%B", name); err == nil && strings.Contains(msg, "Agent-Fox-Run: "+runID) {
			out = append(out, name)
		}
	}
	return out, nil
}

// madeByTheRun says whether the landing of the run marked the branch as its own (see fix.BranchRunKey).
func (a *attempt) madeByTheRun(ctx context.Context, name, runID string) bool {
	marked, err := a.cloneGit(ctx, "config", "--local", "--get", "branch."+name+"."+fix.BranchRunKey)
	return err == nil && strings.TrimSpace(marked) == runID
}

// dropEmptyBranches deletes the local branches that the landing of the run made and that are still at the commit the run
// began at: a landing makes its branch before it commits, and a process that died between the two leaves a branch that
// holds no change and carries no trailer to be known by, but the mark of the run. A branch of that name would make the
// landing take another with a suffix. A branch the run did not make is not touched. The clone is left on the base, with a
// change in its tree where it is, which a checkout of a branch at the same commit does not touch.
func (a *attempt) dropEmptyBranches(ctx context.Context, base, baseHead, runID string) error {
	listed, err := a.cloneGit(ctx, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return err
	}
	var empty []string
	for _, line := range strings.Split(listed, "\n") {
		if name, sha, ok := strings.Cut(strings.TrimSpace(line), " "); ok && name != base && sha == baseHead && a.madeByTheRun(ctx, name, runID) {
			empty = append(empty, name)
		}
	}
	if len(empty) == 0 {
		return nil
	}
	if _, err := a.cloneGit(ctx, "checkout", base); err != nil {
		return err
	}
	for _, name := range empty {
		if _, err := a.cloneGit(ctx, "branch", "-D", name); err != nil {
			return err
		}
	}
	return nil
}

// failure is the outcome of a clone that cannot be put to rights: infrastructure while the context is
// alive.
func (r *Runner) cloneTrouble(ctx context.Context, what string, err error) *outcome {
	if ctx.Err() != nil {
		return &outcome{err: context.Cause(ctx)}
	}
	return transientFailure(toolio.CategoryGit, fmt.Sprintf("%s: %v", what, err))
}

// resetClone puts the clone of a resumed fix back to the base the run began at, for a brain that runs
// again: any change is discarded, the files that are not tracked and not ignored go, and so does every
// local branch that the run made, with the run directory kept. A triage clone is reused as it is.
func (r *Runner) resetClone(ctx context.Context, a *attempt) *outcome {
	if a.req.Kind != jobs.KindFix {
		return nil
	}
	state, runID, err := a.pinnedRun()
	if err != nil {
		return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf("the pinned file of the run %s cannot be read: %v", runID, err)})
	}
	for _, args := range [][]string{{"checkout", "-f", state.Base}, {"reset", "--hard", state.BaseHead}, {"clean", "-fdq"}} {
		if _, err := a.cloneGit(ctx, args...); err != nil {
			return r.cloneTrouble(ctx, "the clone cannot be put back to the base of the run", err)
		}
	}
	branches, err := a.runBranches(ctx, state.Base, runID)
	if err != nil {
		return r.cloneTrouble(ctx, "the branches of the clone cannot be listed", err)
	}
	for _, name := range branches {
		if _, err := a.cloneGit(ctx, "branch", "-D", name); err != nil {
			return r.cloneTrouble(ctx, fmt.Sprintf("the branch %s of the run cannot be deleted", name), err)
		}
	}
	if err := a.dropEmptyBranches(ctx, state.Base, state.BaseHead, runID); err != nil {
		return r.cloneTrouble(ctx, "a branch of the clone that holds no change cannot be deleted", err)
	}
	return nil
}

// restoreChange puts the clone of a resumed fix, whose brain is skipped, into the state that measuring
// asks for: the base branch checked out at the commit the run began at, with the change of the run in the
// working tree. The change is in the working tree already, or it is the commits of an unpushed local branch
// that all carry the trailer of the run (a landing that was begun, a change that was parked when the run was
// stopped): then it is brought back into the working tree and the branch is deleted, so that the landing
// names its branch as it did.
func (r *Runner) restoreChange(ctx context.Context, a *attempt) *outcome {
	state, runID, err := a.pinnedRun()
	if err != nil {
		return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf("the pinned file of the run %s cannot be read: %v", runID, err)})
	}
	disagree := func(format string, args ...any) *outcome {
		return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf(format, args...)})
	}
	if err := a.dropEmptyBranches(ctx, state.Base, state.BaseHead, runID); err != nil {
		return r.cloneTrouble(ctx, "a branch of the clone that holds no change cannot be deleted", err)
	}
	branches, err := a.runBranches(ctx, state.Base, runID)
	if err != nil {
		return r.cloneTrouble(ctx, "the branches of the clone cannot be listed", err)
	}
	status, err := a.cloneGit(ctx, "status", "--porcelain")
	if err != nil {
		return r.cloneTrouble(ctx, "the state of the clone cannot be read", err)
	}
	dirty := strings.TrimSpace(status) != ""

	switch {
	case len(branches) == 0:
		if _, err := a.cloneGit(ctx, "checkout", state.Base); err != nil {
			return r.cloneTrouble(ctx, fmt.Sprintf("the base %s cannot be checked out", state.Base), err)
		}
	case len(branches) > 1 || dirty:
		return disagree("the clone holds the change of the run in more than one place (the branches %s, and a working tree that is %s): it is not clear which is the run's, and the attempt is not carried on",
			strings.Join(branches, ", "), map[bool]string{true: "not clean", false: "clean"}[dirty])
	default:
		branch := branches[0]
		commits, err := a.cloneGit(ctx, "rev-list", state.BaseHead+".."+branch)
		if err != nil {
			return r.cloneTrouble(ctx, fmt.Sprintf("the commits of the branch %s cannot be listed", branch), err)
		}
		for _, commit := range strings.Fields(commits) {
			if msg, _ := a.cloneGit(ctx, "log", "-1", "--format=%B", commit); !strings.Contains(msg, "Agent-Fox-Run: "+runID) {
				return disagree("the branch %s of the clone has the commit %.12s after the commit %.12s that the run began at, and it has no Agent-Fox-Run trailer for the run %s: it is not the run's, and the attempt is not carried on",
					branch, commit, state.BaseHead, runID)
			}
		}
		for _, args := range [][]string{{"checkout", branch}, {"reset", "--soft", state.BaseHead}, {"reset", "-q"}, {"checkout", state.Base}, {"branch", "-D", branch}} {
			if _, err := a.cloneGit(ctx, args...); err != nil {
				return r.cloneTrouble(ctx, fmt.Sprintf("the change of the branch %s cannot be brought back into the working tree", branch), err)
			}
		}
	}
	if head, err := a.cloneGit(ctx, "rev-parse", "HEAD"); err != nil || strings.TrimSpace(head) != state.BaseHead {
		return disagree("the base %s of the clone is at %.12s, and the run began at %.12s: the attempt is not carried on", state.Base, strings.TrimSpace(head), state.BaseHead)
	}
	return nil
}
