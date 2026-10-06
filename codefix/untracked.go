package codefix

import (
	"context"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The checkout may hold files the run did not create — a person's notes, a
// spec package `spec` is writing — and `git add -A` would commit whatever it
// found. So the run notes the untracked files before its implementation phase
// and leaves those out of its commits, and in the tree.

// untrackedNow is the set of untracked files, or nil when git cannot say.
func untrackedNow(ctx context.Context, git *gitx.Git) map[string]bool {
	files, err := git.UntrackedFiles(ctx)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	return set
}

// commitOwn commits everything but the untracked files that were there before
// the implementation phase, which it names and leaves. A new file the phase's
// report does not list is committed — the checks ran with it — and named.
func commitOwn(ctx context.Context, o Options, git *gitx.Git, before map[string]bool, message string,
	changes []FileChange) (string, error) {

	files, err := git.UntrackedFiles(ctx)
	if err != nil {
		return "", err
	}
	var leave, unlisted []string
	for _, f := range files {
		switch {
		case before[f]:
			leave = append(leave, f)
		case !listed(f, changes):
			unlisted = append(unlisted, f)
		}
	}
	if len(leave) > 0 {
		o.Run.Warn(toolio.WarnUntrackedFilesLeftAlone, "low", "left out of the commit and in the tree, because "+
			"the run did not create them: %s", strings.Join(leave, ", "))
	}
	if len(unlisted) > 0 {
		o.Run.Warn(toolio.WarnUnlistedFileCommitted, "low", "committed new file(s) the phase's report does "+
			"not list among its changes; check that they are its work: %s", strings.Join(unlisted, ", "))
	}
	return git.CommitAllExcept(ctx, message, false, leave)
}

// listed reports whether changes names f, by path or by a directory ending in
// a slash.
func listed(f string, changes []FileChange) bool {
	for _, c := range changes {
		p := strings.TrimPrefix(strings.TrimSpace(c.Path), "./")
		if p == f || (strings.HasSuffix(p, "/") && strings.HasPrefix(f, p)) {
			return true
		}
	}
	return false
}
