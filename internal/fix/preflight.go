package fix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The ways a fix is landed.
const (
	LandPR     = "pr"
	LandBranch = "branch"
	LandNone   = "none"
)

// The warnings of the checks before a run.
const (
	WarnNoForgeToken    = "no_forge_token"
	WarnIssueClosed     = "issue_closed"
	WarnBaselineFailing = "baseline_failing"
	// WarnQuestionNotFound: an answer was given to a question no earlier run asked.
	WarnQuestionNotFound = "question_not_found"
)

// maxDirtyNamed is how many dirty paths a refusal names.
const maxDirtyNamed = 20

// PreflightParams is what the checks before a run are made against.
type PreflightParams struct {
	// Dir is --dir, absolute. Base is --base, or empty for the branch that is
	// checked out. Land is one of the ways of landing, or empty for a command that
	// lands nothing.
	Dir, Base, Land string
	NoVerify        bool
	DryRun          bool
	// Forge and Target are the client of the forge and the repository it works
	// on, which a pull request is opened in.
	Forge  issuex.Client
	Target issuex.Repo
	// NewBrain makes the brain of the run: the last of the checks, since it is
	// the one that needs a credential of a model. A command that runs no brain
	// has none.
	NewBrain func() (brain.Brain, error)
	// Gate runs the checks of the baseline; checks.Run when it is nil.
	Gate func(ctx context.Context, dir string, o checks.Options) (checks.Result, error)
	// Warn raises a warning of the run.
	Warn func(code, message string)
}

// Preflighted is what a run goes on with once the checks have passed.
type Preflighted struct {
	Git *gitx.Git
	// Base is the branch the fix is made on, and BaseHead the full hash of its commit.
	Base, BaseHead string
	// Detection holds the gate table, which is pinned for the run, and Files the
	// file view of the checkout it was detected from.
	Detection project.Detection
	Files     []string
	// Baseline is the gate on the unchanged tree, and nil under --no-verify.
	Baseline *checks.Result
	Brain    brain.Brain
}

// Preflight runs the checks that need no model, in order, and stops at the first
// that fails: the work tree, the branch, a clean tree, a gate, the landing, the
// brain. A failure makes no model call and no write. A command that lands nothing
// has no Land and a command that runs no brain has no NewBrain, and those two checks
// are not made. When every check has passed it records the full hash of the base and
// runs the gate once on the tree as it is, which is the baseline a change is
// compared with, unless the run is not verified.
func Preflight(ctx context.Context, p PreflightParams) (Preflighted, error) {
	g := gitx.New(p.Dir, gitx.ExecRunner)
	if err := checkTopLevel(ctx, g, p.Dir); err != nil {
		return Preflighted{}, err
	}
	base, err := checkBranch(ctx, g, p.Base)
	if err != nil {
		return Preflighted{}, err
	}
	if err := checkClean(ctx, g); err != nil {
		return Preflighted{}, err
	}
	detection, files, err := checkGate(ctx, p)
	if err != nil {
		return Preflighted{}, err
	}
	if p.Land != "" {
		if err := checkLanding(ctx, g, p); err != nil {
			return Preflighted{}, err
		}
	}
	var b brain.Brain
	if p.NewBrain != nil {
		if b, err = p.NewBrain(); err != nil {
			return Preflighted{}, err
		}
	}
	head, err := g.RevParse(ctx, "HEAD")
	if err != nil {
		return Preflighted{}, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot read the commit of %s: %v", base, err)}
	}
	res := Preflighted{Git: g, Base: base, BaseHead: head, Detection: detection, Files: files, Brain: b}
	if !p.NoVerify {
		if res.Baseline, err = runBaseline(ctx, p, detection.Gate); err != nil {
			return Preflighted{}, err
		}
	}
	return res, nil
}

// gateError is the failure for an error of a gate run, which is what names: a mistake in
// the options is usage, a run that was stopped is the stop, and anything else is a failure
// to read or write.
func gateError(ctx context.Context, what string, err error) error {
	var usageErr *checks.UsageError
	switch {
	case errors.As(err, &usageErr):
		return usage("%s", usageErr.Message)
	case ctx.Err() != nil:
		return ctx.Err()
	}
	return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("%s could not run: %v", what, err)}
}

// runBaseline runs the pinned table once on the unchanged tree, in the default
// mode of the runner and with no label: the label file is shared by every run in
// the checkout, and a run that sets one could take another's. A red baseline is a
// fact, and a warning.
func runBaseline(ctx context.Context, p PreflightParams, table project.Table) (*checks.Result, error) {
	run := p.Gate
	if run == nil {
		run = checks.Run
	}
	res, err := run(ctx, p.Dir, checks.Options{Table: table})
	if err != nil {
		return nil, gateError(ctx, "the baseline gate", err)
	}
	if !res.Passed {
		p.Warn(WarnBaselineFailing, "the gate fails on the tree as it is, before any change; a check that already fails is not counted against the change")
	}
	return &res, nil
}

func usage(format string, args ...any) *toolio.Failure {
	return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf(format, args...)}
}

func preflight(format string, args ...any) *toolio.Failure {
	return &toolio.Failure{Category: toolio.CategoryPreflight, Message: fmt.Sprintf(format, args...)}
}

// checkTopLevel checks that dir is the top level of a git work tree.
func checkTopLevel(ctx context.Context, g *gitx.Git, dir string) error {
	if !g.IsRepo(ctx) {
		return usage("--dir %s is not inside a git work tree", dir)
	}
	top, err := g.TopLevel(ctx)
	if err != nil {
		return usage("--dir %s: %v", dir, err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = dir
	}
	if filepath.Clean(top) != filepath.Clean(resolved) {
		return usage("--dir %s is not the top level of a git work tree: %s is", dir, top)
	}
	return nil
}

// checkBranch checks that a branch is checked out, and that it is the base.
func checkBranch(ctx context.Context, g *gitx.Git, base string) (string, error) {
	branch, err := g.CurrentBranch(ctx)
	switch {
	case err != nil:
		return "", usage("the repository has no commit to fix from: %v", err)
	case branch == "HEAD":
		return "", usage("HEAD is detached: check out the branch to fix on")
	case base != "" && base != branch:
		return "", usage("check out %s first: %s is checked out, and a fix is made on the base as it stands", base, branch)
	}
	return branch, nil
}

// checkClean checks that the working tree holds nothing: every change in it must
// be the run's own. It names the first 20 dirty paths.
func checkClean(ctx context.Context, g *gitx.Git) error {
	dirty, err := g.DirtyFiles(ctx)
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryGit, Message: err.Error()}
	}
	if len(dirty) == 0 {
		return nil
	}
	named := dirty[:min(len(dirty), maxDirtyNamed)]
	paths := make([]string, len(named))
	for i, line := range named {
		paths[i] = strings.TrimSpace(line[min(len(line), 2):])
	}
	list := strings.Join(paths, ", ")
	if more := len(dirty) - len(named); more > 0 {
		list += fmt.Sprintf(", and %d more", more)
	}
	return preflight("the working tree is not clean, and every change in it must be the run's own: commit, stash or remove %s", list)
}

// checkGate checks that there is something to measure the change by, and gives the
// table and the file view it was detected from.
func checkGate(ctx context.Context, p PreflightParams) (project.Detection, []string, error) {
	files, err := checks.FileView(ctx, p.Dir)
	if err != nil {
		return project.Detection{}, nil, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	detection, err := project.Detect(p.Dir, files)
	if err != nil {
		category := toolio.CategoryInternal
		if errors.Is(err, project.ErrGateConfig) {
			category = toolio.CategoryConfig
		}
		return project.Detection{}, nil, &toolio.Failure{Category: category, Message: err.Error()}
	}
	if p.NoVerify {
		return detection, files, nil
	}
	for _, c := range detection.Gate.Checks {
		if c.Available {
			return detection, files, nil
		}
	}
	return project.Detection{}, nil, &toolio.Failure{
		Category: toolio.CategoryPreflight,
		Message:  "no check of the gate can be run here, so the change could not be measured: add a Makefile check target or a .agent-fox/gate.json, or pass --no-verify",
		FixHint:  &toolio.FixHint{Flag: "--no-verify"},
	}
}

// checkLanding checks what landing needs: an origin to push to, a repository to
// open the pull request in, and a forge that can be written to.
func checkLanding(ctx context.Context, g *gitx.Git, p PreflightParams) error {
	if p.Land == LandNone {
		return nil
	}
	if !p.DryRun && !g.HasRemote(ctx) {
		return preflight("--land %s pushes the branch, and there is no origin remote to push to: add one, or use --land none", p.Land)
	}
	if p.Land != LandPR {
		return nil
	}
	if !p.Target.Valid() {
		return &toolio.Failure{
			Category: toolio.CategoryUsage,
			Message:  "there is no repository to open the pull request in: pass --repo owner/name, or use --land branch or none",
			FixHint:  &toolio.FixHint{Flag: "--repo"},
		}
	}
	if !p.Forge.Authenticated() {
		if p.DryRun {
			p.Warn(WarnNoForgeToken, fmt.Sprintf("opening a pull request in %s needs a credential, and none is set; a dry run goes on without one", p.Target))
			return nil
		}
		return &toolio.Failure{
			Category: toolio.CategoryAuth,
			Message:  fmt.Sprintf("opening a pull request in %s needs a credential: set GITHUB_TOKEN, GH_TOKEN or GITLAB_TOKEN, or pass --dry-run, --land branch or --land none", p.Target),
			FixHint:  &toolio.FixHint{Env: tokenVariable(p)},
		}
	}
	return checkRepository(ctx, p)
}

// tokenVariable is the variable that holds the credential of the forge: a GitLab
// host, or a forge that reports GitLab's body limit, takes GITLAB_TOKEN.
func tokenVariable(p PreflightParams) string {
	if strings.Contains(strings.ToLower(p.Target.Host), "gitlab") || issuex.MaxBodyLength(p.Forge) == issuex.GitLabMaxBodyLength {
		return "GITLAB_TOKEN"
	}
	return "GITHUB_TOKEN"
}

// checkRepository checks that the repository can be read and takes a pull request.
func checkRepository(ctx context.Context, p PreflightParams) error {
	repo, err := p.Forge.GetRepository(ctx, p.Target)
	switch {
	case err == nil && repo.Archived:
		return &toolio.Failure{Category: toolio.CategoryForge, Message: fmt.Sprintf("repository is archived: %s takes no pull request", p.Target)}
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return ctx.Err()
	case issuex.IsNoToken(err), isUnauthorized(err):
		return &toolio.Failure{Category: toolio.CategoryAuth, Message: fmt.Sprintf("the credential cannot read %s: %v", p.Target, err), FixHint: &toolio.FixHint{Env: tokenVariable(p)}}
	case issuex.IsNotFound(err):
		return &toolio.Failure{Category: toolio.CategoryForge, Message: fmt.Sprintf("repository %s was not found: %v", p.Target, err)}
	}
	return &toolio.Failure{Category: toolio.CategoryForge, Message: fmt.Sprintf("cannot read repository %s: %v", p.Target, err), Retryable: retryable(err)}
}

// isUnauthorized reports a forge that refused the credential it was given.
func isUnauthorized(err error) bool {
	var he *issuex.HTTPError
	return errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) && !issuex.IsRateLimited(err)
}

// retryable reports whether trying again may succeed: it may after a rate limit,
// a server error or a lost connection, and may not after the forge refused the
// request.
func retryable(err error) bool {
	if issuex.IsRateLimited(err) {
		return true
	}
	var he *issuex.HTTPError
	return !(errors.As(err, &he) && he.Status >= 400 && he.Status < 500 && he.Status != http.StatusRequestTimeout)
}
