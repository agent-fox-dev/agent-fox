package codeimpl

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// A run shares its checkout with whoever else works in it. `git add -A` and
// `git clean` would take what they found — a spec package written by `spec`
// while a task ran, a person's notes — into a task's commit, or delete it
// with a discarded attempt. So the run commits and removes only what it can
// own: before each writing phase it notes the untracked files already there,
// and those, and anything new under the specs directory outside its own
// spec package, are left in the tree, out of every commit, and named once.

// noteUntracked records the untracked files at the start of a writing phase.
func (st *RunState) noteUntracked(ctx context.Context) {
	files, err := st.git.UntrackedFiles(ctx)
	if err != nil {
		return
	}
	st.untrackedBefore = map[string]bool{}
	for _, f := range files {
		st.untrackedBefore[f] = true
	}
}

// leftAlone is the untracked files the run did not create: there before the
// phase started, or under the specs directory outside the run's own spec
// package, which no task writes.
func (st *RunState) leftAlone(ctx context.Context) ([]string, error) {
	files, err := st.git.UntrackedFiles(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		if st.untrackedBefore[f] || st.foreignSpecPath(f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// foreignSpecPath reports whether p is under the specs directory and outside
// the run's own spec package.
func (st *RunState) foreignSpecPath(p string) bool {
	rel, err := filepath.Rel(st.root, st.specsDir)
	if err != nil || st.specsDir == "" {
		return false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return strings.HasPrefix(p, rel+"/") && !underSpec(st, p)
}

// commit is every commit the run makes: everything in the tree but the files
// it did not create, which are named once and left where they are. changes
// is the phase's report of what it changed, when there is one; a new file it
// does not list is committed — the gate ran with it — and named.
func (st *RunState) commit(ctx context.Context, o Options, message string, noVerify bool, changes []FileChange) (string, error) {
	leave, err := st.leftAlone(ctx)
	if err != nil {
		return "", err
	}
	var fresh []string
	for _, f := range leave {
		if !st.leftWarned[f] {
			fresh = append(fresh, f)
		}
	}
	if len(fresh) > 0 {
		if st.leftWarned == nil {
			st.leftWarned = map[string]bool{}
		}
		for _, f := range fresh {
			st.leftWarned[f] = true
		}
		o.Run.Warn(toolio.WarnUntrackedFilesLeftAlone, "low", "left out of the commit and in the tree, because "+
			"the run did not create them: %s", strings.Join(fresh, ", "))
	}
	if changes != nil {
		if unlisted := st.unlisted(ctx, leave, changes); len(unlisted) > 0 {
			o.Run.Warn(toolio.WarnUnlistedFileCommitted, "low", "committed new file(s) the phase's report does "+
				"not list among its changes; check that they are its work: %s", strings.Join(unlisted, ", "))
		}
	}
	return st.git.CommitAllExcept(ctx, message, noVerify, leave)
}

// unlisted is the new files the commit is about to carry that changes does
// not name, by path or by a directory ending in a slash.
func (st *RunState) unlisted(ctx context.Context, leave []string, changes []FileChange) []string {
	files, err := st.git.UntrackedFiles(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		if slices.Contains(leave, f) {
			continue
		}
		listed := false
		for _, c := range changes {
			p := strings.TrimPrefix(strings.TrimSpace(c.Path), "./")
			if p == f || (strings.HasSuffix(p, "/") && strings.HasPrefix(f, p)) {
				listed = true
				break
			}
		}
		if !listed {
			out = append(out, f)
		}
	}
	return out
}

// dirtyAfterCommit is what the tree has that the commit just made does not
// carry, other than the files the run left alone on purpose.
func (st *RunState) dirtyAfterCommit(ctx context.Context) ([]string, error) {
	lines, err := st.git.DirtyFiles(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "??") {
			out = append(out, l)
		}
	}
	files, err := st.git.UntrackedFiles(ctx)
	if err != nil {
		return nil, err
	}
	leave, err := st.leftAlone(ctx)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if !slices.Contains(leave, f) {
			out = append(out, "?? "+f)
		}
	}
	return out, nil
}
