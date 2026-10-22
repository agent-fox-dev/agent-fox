package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

const (
	// cloneDirName is where the clone of the attempt is, in the attempt directory.
	cloneDirName = "clone"
	// dirMode is the mode of the directories the worker makes in the attempt directory.
	dirMode = 0o700
	// cloneTimeout bounds the clone, which a git server that does not answer would hold forever.
	cloneTimeout = 10 * time.Minute
	// maxGitDetail is how much of what git printed a failure keeps.
	maxGitDetail = 300
)

// clone makes the checkout the attempt works in: a fresh clone of the workspace from hub's git
// server into clone/ of the attempt directory, on the base branch of the workspace, with the
// identity of the commits set in its own configuration. A clone/ that an earlier delivery kept is
// removed first. Every git command of the attempt is run by the runner this step makes, which
// holds the PAT in the environment of the commands that reach hub.
//
// The server refusing the credential, 401 or 403, is the credential and ends the attempt with an
// envelope. Any other failure of the clone, a transport error, an answer of 5xx, a timeout, is
// the infrastructure and a transient error, and the attempt has no envelope.
func (r *Runner) clone(ctx context.Context, a *attempt) *outcome {
	a.step("clone")
	if a.resume != nil { // the clone of the attempt is the one that was kept
		if out := r.prepareResumedClone(ctx, a); out != nil {
			return out
		}
		return r.nothingLeft(a)
	}
	if out := r.nothingLeft(a); out != nil {
		return out
	}
	dir := a.cloneDir
	if err := os.RemoveAll(dir); err != nil {
		return transientFailure(toolio.CategoryIO, fmt.Sprintf("the clone of an earlier delivery cannot be removed: %v", err))
	}
	if err := os.Mkdir(dir, dirMode); err != nil {
		return transientFailure(toolio.CategoryIO, fmt.Sprintf("the directory of the clone cannot be made: %v", err))
	}

	timeout := r.cloneTimeout
	if timeout <= 0 {
		timeout = cloneTimeout
	}
	cloneCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, code, err := a.git(cloneCtx, a.req.Dir, []string{"git", "clone", "--quiet", "--branch=" + a.record.Branch, "--", a.record.CloneURL, cloneDirName})
	if ctx.Err() != nil {
		return &outcome{err: context.Cause(ctx)}
	}
	if err != nil || code != 0 {
		return r.cloneFailure(a, out, code, err)
	}

	for _, kv := range [][2]string{{"user.name", committerName}, {"user.email", committerEmail}} {
		if out, code, err := a.git(ctx, dir, []string{"git", "config", "--local", kv[0], kv[1]}); err != nil || code != 0 {
			if ctx.Err() != nil {
				return &outcome{err: context.Cause(ctx)}
			}
			return transientFailure(toolio.CategoryGit, fmt.Sprintf("the identity of the commits cannot be set in the clone: %s", gitDetail(a, out, err)))
		}
	}
	a.note(stepClone, func(j *recovery.Journal) { j.CloneDir = cloneDirName })
	return nil
}

// prepareGit makes the runner of every git command of the attempt, which holds the PAT in the environment
// of the commands that reach hub, and names the clone.
func (r *Runner) prepareGit(a *attempt) *outcome {
	home, err := os.MkdirTemp("", "agent-fox-git-home-")
	if err != nil {
		return transientFailure(toolio.CategoryIO, fmt.Sprintf("a directory for git cannot be made: %v", err))
	}
	a.scratch = append(a.scratch, home)
	a.git = gitRunner(home, a.record.CloneURL, a.pat, r.gitWrap)
	a.cloneDir = filepath.Join(a.req.Dir, cloneDirName)
	return nil
}

// cloneFailure is the outcome of a clone that did not come to be.
func (r *Runner) cloneFailure(a *attempt, out string, code int, err error) *outcome {
	if refusedCredential(out) {
		return r.envelopeOf(a, authRefusal(a.req.Workspace, "hub's git server does not take the token to clone the workspace"))
	}
	return transientFailure(toolio.CategoryGit, fmt.Sprintf("the workspace cannot be cloned from hub's git server: %s", gitDetail(a, out, err)))
}

// refusedCredential says whether what git printed is a git server refusing the credential: a 401,
// which git can only answer by asking for a credential and finding that it may not, and a 403.
func refusedCredential(out string) bool {
	out = strings.ToLower(out)
	for _, s := range []string{"returned error: 401", "returned error: 403", "authentication failed", "terminal prompts disabled", "could not read username", "could not read password"} {
		if strings.Contains(out, s) {
			return true
		}
	}
	return false
}

// gitDetail is what git printed, or the error that kept it from running, cut short and with the
// PAT taken out, for a message that is shown.
func gitDetail(a *attempt, out string, err error) string {
	detail := strings.Join(strings.Fields(out), " ")
	if detail == "" && err != nil {
		detail = err.Error()
	}
	if len(detail) > maxGitDetail {
		detail = strings.ToValidUTF8(detail[:maxGitDetail], "") + "..."
	}
	return redact(detail, a.pat)
}
