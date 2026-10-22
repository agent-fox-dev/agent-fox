package fix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The warnings of measuring a change.
const (
	WarnProtectedPathReverted = "protected_path_reverted"
	WarnScopeMismatch         = "scope_mismatch"
	WarnFixNotProven          = "fix_not_proven"
	WarnTreeChangedDuringGate = "tree_changed_during_gate"
)

// VerdictNoChange is the verdict of a change that is nothing once the protected paths
// are put back.
const VerdictNoChange = "no_change"

// maxNamedCommits is how many foreign commits a refusal names.
const maxNamedCommits = 5

// GateRef is a gate run, as a result names it.
type GateRef struct {
	GateID string `json:"gate_id"`
	Passed bool   `json:"passed"`
}

// RevertRef is the outcome of a revert check, as a result names it.
type RevertRef struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// Scope compares the files a report declares with the paths that changed.
type Scope struct {
	Undeclared []string `json:"undeclared"`
	Unchanged  []string `json:"unchanged"`
}

// MeasureParams is what a change on the checkout is measured against.
type MeasureParams struct {
	Dir   string
	Git   *gitx.Git
	State State
	// Report is the report the change was submitted with, or nil when there is none.
	Report *Report
	Warn   func(code, message string)
	// Gate runs the checks and Conform the revert check; checks.Run and conform.Check
	// when nil.
	Gate    func(ctx context.Context, dir string, o checks.Options) (checks.Result, error)
	Conform func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error)
}

// Measurement is what `af` measured of a change: facts of the checkout, never claims of
// the brain.
type Measurement struct {
	// Verdict is the verdict of the comparison with the baseline, unverified when the
	// gate was not run, or no_change. Landable is the verdict's own say, and Lands
	// whether the change goes on: a landable verdict does, and so does any change that
	// was not verified because the run said so.
	Verdict  string
	Landable bool
	Lands    bool
	// Closes is true for a revert check that proved the change.
	Closes bool
	After  *GateRef
	// Rows are the checks one by one: what each said before the change and after it.
	Rows        []checks.Row
	RevertCheck *RevertRef
	// Changed are the paths of the change, the protected ones put back left out, and
	// Reverted those that were put back.
	Changed  []string
	Reverted []string
	Scope    Scope
	// Refusal is why a change does not land, and nil when it does.
	Refusal *Refusal
}

func (p MeasureParams) warn(code, message string) {
	if p.Warn != nil {
		p.Warn(code, message)
	}
}

// ScopeOf compares the files a report declares with the paths that changed, both lists
// sorted, and never nil.
func ScopeOf(declared, changed []string) Scope {
	declared, changed = sortedUnique(declared), sortedUnique(changed)
	s := Scope{Undeclared: []string{}, Unchanged: []string{}}
	for _, c := range changed {
		if !slices.Contains(declared, c) {
			s.Undeclared = append(s.Undeclared, c)
		}
	}
	for _, d := range declared {
		if !slices.Contains(changed, d) {
			s.Unchanged = append(s.Unchanged, d)
		}
	}
	return s
}

func sortedUnique(list []string) []string {
	out := slices.Clone(list)
	slices.Sort(out)
	return slices.Compact(out)
}

// Measure measures the change on the checkout, from the facts of the checkout and of the
// checks. The checkout must be where the run left it, or it is failed/git. The protected
// paths that changed are put back, and a change that is nothing then is refused with no
// check run. The report's files are compared with the paths git sees, and a difference
// warns and refuses nothing. Unless the run is not verified, the pinned table is run and
// compared with the baseline, and a change that may land is given the revert check, which
// alone decides whether the commit says Closes.
func Measure(ctx context.Context, p MeasureParams) (Measurement, error) {
	if err := checkBase(ctx, p); err != nil {
		return Measurement{}, err
	}
	changed, reverted, err := revertProtected(ctx, p)
	if err != nil {
		return Measurement{}, err
	}
	m := Measurement{Changed: changed, Reverted: reverted, Scope: Scope{Undeclared: []string{}, Unchanged: []string{}}}
	if len(changed) == 0 {
		m.Verdict = VerdictNoChange
		m.Refusal = &Refusal{Kind: RefusalNoChange, Verdict: VerdictNoChange, Reverted: reverted}
		return m, nil
	}
	if p.Report != nil {
		declared := make([]string, len(p.Report.Files))
		for i, f := range p.Report.Files {
			declared[i] = f.Path
		}
		if m.Scope = ScopeOf(declared, changed); len(m.Scope.Undeclared)+len(m.Scope.Unchanged) > 0 {
			p.warn(WarnScopeMismatch, fmt.Sprintf("the report declares files that did not change (%s) and the change has files it does not declare (%s)",
				strings.Join(m.Scope.Unchanged, ", "), strings.Join(m.Scope.Undeclared, ", ")))
		}
	}

	if p.State.NoVerify {
		m.Verdict, m.Lands = checks.VerdictUnverified, true
		return m, nil
	}
	if err := measureGate(ctx, p, &m); err != nil {
		return Measurement{}, err
	}
	if m.Landable {
		if err := revertCheck(ctx, p, &m); err != nil {
			return Measurement{}, err
		}
	}
	return m, nil
}

// checkBase checks that the checkout is the base branch at the commit the run began at,
// since af makes the commit that counts: a commit made since is named, and nothing is
// measured.
func checkBase(ctx context.Context, p MeasureParams) error {
	branch, berr := p.Git.CurrentBranch(ctx)
	head, herr := p.Git.RevParse(ctx, "HEAD")
	if berr == nil && herr == nil && branch == p.State.Base && head == p.State.BaseHead {
		return nil
	}
	short := p.State.BaseHead[:min(12, len(p.State.BaseHead))]
	var what []string
	switch {
	case berr != nil:
		what = append(what, "the branch cannot be read: "+berr.Error())
	case branch == "HEAD":
		what = append(what, "HEAD is detached")
	case branch != p.State.Base:
		what = append(what, fmt.Sprintf("%s is checked out, and the run is on %s", branch, p.State.Base))
	}
	if commits, err := p.Git.CommitsSince(ctx, p.State.BaseHead); err == nil && len(commits) > 0 {
		var named []string
		for _, c := range commits[:min(len(commits), maxNamedCommits)] {
			named = append(named, fmt.Sprintf("%s %s", c.Hash[:min(7, len(c.Hash))], firstLine(c.Message)))
		}
		list := strings.Join(named, "; ")
		if more := len(commits) - len(named); more > 0 {
			list += fmt.Sprintf("; and %d more", more)
		}
		what = append(what, fmt.Sprintf("%d commit(s) since the run began: %s", len(commits), list))
	}
	if len(what) == 0 {
		what = append(what, fmt.Sprintf("HEAD is %s", head))
	}
	return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf(
		"the checkout is not where the run began (%s at %s), and af makes the commit that counts, so nothing was measured: %s",
		p.State.Base, short, strings.Join(what, "; "))}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// revertProtected puts the protected paths that changed back to their content at the base,
// or deletes those the base did not have, and says which, with a warning for each. It
// returns the paths that are left of the change.
func revertProtected(ctx context.Context, p MeasureParams) (changed, reverted []string, err error) {
	paths, err := p.Git.ChangedPaths(ctx, p.State.BaseHead)
	if err != nil {
		return nil, nil, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot list what changed since %s: %v", p.State.BaseHead, err)}
	}
	protected := policy.DefaultProtectedPaths()
	changed, reverted = []string{}, []string{}
	var removed []string
	for _, rel := range paths {
		if !policy.IsProtected(protected, rel) {
			changed = append(changed, rel)
			continue
		}
		_, atBase, err := p.Git.ShowFile(ctx, p.State.BaseHead, rel)
		if err != nil {
			return nil, nil, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot read %s at %s: %v", rel, p.State.BaseHead, err)}
		}
		if atBase {
			err = p.Git.CheckoutPaths(ctx, p.State.BaseHead, rel)
		} else {
			err = removePath(p.Dir, rel)
			removed = append(removed, rel)
		}
		if err != nil {
			return nil, nil, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot put the protected path %s back: %v", rel, err)}
		}
		reverted = append(reverted, rel)
		p.warn(WarnProtectedPathReverted, fmt.Sprintf("%s is a protected path, only af writes there, and the change to it was put back", rel))
	}
	if len(removed) > 0 {
		if root, err := os.OpenRoot(p.Dir); err == nil {
			pruneEmptied(root, removed)
			_ = root.Close()
		}
	}
	return changed, reverted, nil
}

// removePath deletes a file or a link of the checkout that the base did not have, and not
// what a link in its path leads to.
func removePath(dir, rel string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(filepath.FromSlash(rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// measureGate runs the pinned table on the change and compares it with the baseline by id.
func measureGate(ctx context.Context, p MeasureParams, m *Measurement) error {
	if p.State.BaselineGateID == "" {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: "fix.json has no baseline gate, and the run is verified: there is nothing to compare the change with"}
	}
	run := p.Gate
	if run == nil {
		run = checks.Run
	}
	after, err := run(ctx, p.Dir, checks.Options{Table: p.State.Table})
	if err != nil {
		return GateError(ctx, "the gate after the change", err)
	}
	if after.TreeChanged {
		p.warn(WarnTreeChangedDuringGate, "the checks changed the tree; the result is kept under the hash of the tree they ran on")
	}
	m.After = &GateRef{GateID: after.ID, Passed: after.Passed}
	cmp, err := checks.Compare(p.Dir, p.State.BaselineGateID, after.ID)
	var uerr *checks.UsageError
	switch {
	case errors.As(err, &uerr):
		m.Verdict = checks.VerdictUnverified
		m.Refusal = &Refusal{Kind: RefusalGate, Verdict: m.Verdict, Detail: "the checks that ran are not the checks of the baseline: " + uerr.Message, Failing: failing(after), Reverted: m.Reverted}
		return nil
	case err != nil:
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot compare the gate after the change with the baseline: %v", err)}
	}
	m.Verdict, m.Landable, m.Lands, m.Rows = cmp.Verdict, cmp.Landable, cmp.Landable, cmp.Rows
	if m.Lands {
		return nil
	}
	m.Refusal = &Refusal{Kind: RefusalGate, Verdict: m.Verdict, Failing: failing(after), Reverted: m.Reverted}
	if m.Verdict == checks.VerdictUnverified {
		m.Refusal.Detail = "no check of the gate could be run after the change, so nothing was measured"
	}
	return nil
}

// failing are the checks that did not pass, each with the end of its output.
func failing(res checks.Result) []FailingCheck {
	var out []FailingCheck
	for _, c := range res.Checks {
		if c.Status != checks.StatusPass {
			out = append(out, FailingCheck{Name: c.Name, Tail: tail(c.Tail)})
		}
	}
	return out
}

// revertCheck asks whether the tests of the change fail without the rest of it. Only a
// proof closes the issue; any other outcome lets the change land with a warning.
func revertCheck(ctx context.Context, p MeasureParams, m *Measurement) error {
	run := p.Conform
	if run == nil {
		run = conform.Check
	}
	res, err := run(ctx, p.Dir, p.State.BaseHead, conform.Options{Table: p.State.Table})
	if err != nil {
		var restore *conform.RestoreError
		var gitErr *conform.GitError
		var usageErr *checks.UsageError
		var failure *toolio.Failure
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &failure):
			return failure // the box that measured has said what it is, and whether it may be tried again
		case errors.As(err, &restore):
			return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("the tree could not be put back after the revert check: %v", restore)}
		case errors.As(err, &gitErr):
			return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("the revert check failed in git: %v", gitErr)}
		case errors.As(err, &usageErr):
			return usage("%s", usageErr.Message)
		}
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("the revert check could not run: %v", err)}
	}
	m.RevertCheck = &RevertRef{Outcome: res.Outcome, Reason: res.Reason}
	if m.Closes = res.Outcome == conform.OutcomeProven && res.Closes; !m.Closes {
		p.warn(WarnFixNotProven, fmt.Sprintf("the tests of the change do not fail without the fix (%s: %s), so it refers to the issue and does not close it", res.Outcome, res.Reason))
	}
	return nil
}
