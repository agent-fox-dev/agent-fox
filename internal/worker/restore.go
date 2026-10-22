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

// resetClone puts the clone of a resumed fix back to the base the run began at, for a brain that runs
// again: any change is discarded, the files that are not tracked and not ignored go, and so does every
// local branch that the run made, with the run directory kept. A triage clone is reused as it is.
func (r *Runner) resetClone(ctx context.Context, a *attempt) *outcome {
	if a.req.Kind != jobs.KindFix {
		return nil
	}
	runID := a.resume.journal.Run.ID
	state, err := fix.LoadState(filepath.Join(a.cloneDir, ".agent-fox", "run", runID))
	if err != nil {
		return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf("the pinned file of the run %s cannot be read: %v", runID, err)})
	}
	git := func(args ...string) (string, error) {
		out, code, err := a.git(ctx, a.cloneDir, append([]string{"git"}, args...))
		if err == nil && code != 0 {
			err = fmt.Errorf("git %s exited %d: %s", args[0], code, gitDetail(a, out, nil))
		}
		return out, err
	}
	for _, args := range [][]string{{"checkout", "-f", state.Base}, {"reset", "--hard", state.BaseHead}, {"clean", "-fdq"}} {
		if _, err := git(args...); err != nil {
			if ctx.Err() != nil {
				return &outcome{err: context.Cause(ctx)}
			}
			return transientFailure(toolio.CategoryGit, fmt.Sprintf("the clone cannot be put back to the base of the run: %v", err))
		}
	}
	branches, err := git("for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return transientFailure(toolio.CategoryGit, fmt.Sprintf("the branches of the clone cannot be listed: %v", err))
	}
	for _, name := range strings.Fields(branches) {
		if name == state.Base {
			continue
		}
		if msg, err := git("log", "-1", "--format=%B", name); err == nil && strings.Contains(msg, "Agent-Fox-Run: "+runID) {
			if _, err := git("branch", "-D", name); err != nil {
				return transientFailure(toolio.CategoryGit, fmt.Sprintf("the branch %s of the run cannot be deleted: %v", name, err))
			}
		}
	}
	return nil
}
