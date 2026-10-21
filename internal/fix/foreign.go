package fix

import (
	"context"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// WarnForeignCommitsReset is the warning of commits a brain made on the checkout that were
// taken out of its branch, and LedgerForeignCommitsReset the entry of the ledger that says
// how many.
const (
	WarnForeignCommitsReset   = "foreign_commits_reset"
	LedgerForeignCommitsReset = "foreign_commits_reset"
)

// Checkout is where the checkout stood before a brain ran on it in place: the full hash of
// HEAD and the branch that was checked out.
type Checkout struct{ Head, Branch string }

// RecordCheckout reads where the checkout stands, to be compared with after a brain ran on
// the checkout itself, which is the run of the none provider: there is no box for a commit
// of the brain to stay in.
func RecordCheckout(ctx context.Context, g *gitx.Git) (Checkout, error) {
	head, err := g.RevParse(ctx, "HEAD")
	if err != nil {
		return Checkout{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot read HEAD of the checkout: %v", err)}
	}
	branch, err := g.CurrentBranch(ctx)
	if err != nil {
		return Checkout{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot read the branch of the checkout: %v", err)}
	}
	return Checkout{Head: head, Branch: branch}, nil
}

// ForeignParams is what the check of a checkout after a brain ran on it in place uses: the
// checkout's git, the warnings of the run and its ledger.
type ForeignParams struct {
	Git    *gitx.Git
	Warn   func(code, message string)
	Record func(kind string, data any) error
}

// MovedError is the failure of a checkout that a brain moved, which stops the run: af
// makes the commit that counts, from a checkout that is where the run began, and nothing is
// measured, landed or parked on one that is not.
type MovedError struct{ Failure *toolio.Failure }

func (e *MovedError) Error() string { return e.Failure.Error() }
func (e *MovedError) Unwrap() error { return e.Failure }

func moved(format string, args ...any) error {
	return &MovedError{Failure: &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf(format, args...)}}
}

// Check compares the checkout with what was recorded, after a brain ran on it in place, and
// before anything is measured.
//
// Where it was, nothing is done to git. On the same branch with the recorded commit an
// ancestor of HEAD, the commits the brain made are taken off the branch with git reset
// --soft, so that their change is the change af measures and makes its own commit of;
// the count is returned, warned and noted. Anything else (another branch, a detached HEAD,
// history that was rewritten) is a MovedError that names where the checkout was and where
// it is, and the checkout is left as the brain left it.
func (c Checkout) Check(ctx context.Context, p ForeignParams) (int, error) {
	head, err := p.Git.RevParse(ctx, "HEAD")
	if err != nil {
		return 0, moved("cannot read HEAD of the checkout after the brain ran: %v", err)
	}
	branch, err := p.Git.CurrentBranch(ctx)
	if err != nil {
		return 0, moved("cannot read the branch of the checkout after the brain ran: %v", err)
	}
	if head == c.Head && branch == c.Branch {
		return 0, nil
	}
	if branch == c.Branch && head != c.Head {
		ancestor, err := p.Git.IsAncestor(ctx, c.Head, head)
		if err != nil {
			return 0, moved("cannot tell whether %s is an ancestor of %s: %v", c.Head, head, err)
		}
		if ancestor {
			return c.foldIn(ctx, p, head)
		}
	}
	return 0, moved("the brain moved the checkout: it was on %s at %s, and is on %s at %s; af makes the commit that counts, so nothing was measured, landed or parked, and the checkout is as the brain left it",
		describeBranch(c.Branch), c.Head, describeBranch(branch), head)
}

// foldIn takes the commits above the recorded one off the branch, keeping their change.
func (c Checkout) foldIn(ctx context.Context, p ForeignParams, head string) (int, error) {
	commits, err := p.Git.CommitsSince(ctx, c.Head)
	if err != nil {
		return 0, moved("cannot list the commits the brain made on %s: %v", c.Branch, err)
	}
	if err := p.Git.ResetSoft(ctx, c.Head); err != nil {
		return 0, moved("cannot take the commits the brain made off %s (it is at %s, and was at %s): %v", c.Branch, head, c.Head, err)
	}
	count := len(commits)
	if p.Warn != nil {
		p.Warn(WarnForeignCommitsReset, fmt.Sprintf("the brain made %d commit(s) on %s, which af does not keep: they were taken off the branch, back to %s, and their change is the change that is measured", count, c.Branch, c.Head))
	}
	if p.Record != nil {
		if err := p.Record(LedgerForeignCommitsReset, map[string]any{"count": count, "from": head, "to": c.Head, "branch": c.Branch}); err != nil && p.Warn != nil {
			p.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", LedgerForeignCommitsReset, err))
		}
	}
	return count, nil
}

// describeBranch is a branch as a sentence has it: git calls a detached HEAD HEAD.
func describeBranch(branch string) string {
	if branch == "HEAD" {
		return "a detached HEAD"
	}
	return "branch " + branch
}
