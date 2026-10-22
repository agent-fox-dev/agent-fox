package triage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/aflib/issuex"
)

// The landing modes of --land.
const (
	LandIssue = "issue"
	LandNone  = "none"
)

// WarnIssueClosed is the warning of a run on an issue that is closed.
const WarnIssueClosed = "issue_closed"

// The categories of a refusal; internal/afcmd maps them to the shell's.
const (
	CategoryUsage = "usage"
	CategoryAuth  = "auth"
	CategoryForge = "forge"
)

const (
	maxLabels      = 10
	maxLabelChars  = 50
	renderedMargin = 16896 // the largest rendered diagnosis, and the framing around it
)

// Refusal is why a run is refused before it spends anything.
type Refusal struct {
	Category  string
	Message   string
	Retryable bool
	// FixHintEnv names the variable to set, when one would help.
	FixHintEnv string
}

func (r *Refusal) Error() string { return r.Message }

func usage(format string, args ...any) *Refusal {
	return &Refusal{Category: CategoryUsage, Message: fmt.Sprintf(format, args...)}
}

// Flags are the values the preflight checks.
type Flags struct {
	Budget   float64
	MaxTurns int
	Labels   []string
	Land     string
}

// CheckFlags checks the flags, and returns the labels with exact duplicates
// collapsed in first-seen order. Any violation is a usage refusal that names
// the label or the flag.
func CheckFlags(f Flags) ([]string, *Refusal) {
	switch {
	case math.IsNaN(f.Budget) || math.IsInf(f.Budget, 0) || f.Budget <= 0:
		return nil, usage("--budget must be a finite number of dollars above 0, not %v", f.Budget)
	case f.MaxTurns < 1:
		return nil, usage("--max-turns must be at least 1, not %d", f.MaxTurns)
	}
	return CheckLandFlags(f.Land, f.Labels)
}

// CheckLandFlags checks --land and --label, which `af triage land` takes as well,
// and returns the labels with exact duplicates collapsed in first-seen order.
func CheckLandFlags(land string, labelFlags []string) ([]string, *Refusal) {
	if land != "" && land != LandIssue && land != LandNone {
		return nil, usage("--land must be issue or none, not %q", land)
	}
	var labels []string
	for _, l := range labelFlags {
		switch {
		case l == "":
			return nil, usage(`--label "" is empty: give the name of a label`)
		case utf8.RuneCountInString(l) > maxLabelChars:
			return nil, usage("--label %q is longer than %d characters", l, maxLabelChars)
		case strings.Contains(l, ","):
			return nil, usage("--label %q has a comma: give one label for each --label", l)
		case strings.TrimSpace(l) != l:
			return nil, usage("--label %q has a leading or trailing space", l)
		}
		if !slices.Contains(labels, l) {
			labels = append(labels, l)
		}
	}
	if len(labels) > maxLabels {
		return nil, usage("--label was given %d distinct labels, and at most %d are allowed", len(labels), maxLabels)
	}
	if len(labels) > 0 && land == LandNone {
		return nil, usage("--label cannot be used with --land none: nothing is written to the forge, so no label is added")
	}
	return labels, nil
}

// CheckInput refuses what triage does not take, a pull or merge request, and
// warns of an issue that is closed.
func CheckInput(r Report) ([]Warning, *Refusal) {
	if r.Kind != KindIssue {
		if !utf8.ValidString(r.Body) {
			return nil, usage("the report is not valid UTF-8 text, and a brain and a forge take text: convert it (iconv -f <encoding> -t UTF-8) and run again")
		}
		return nil, nil
	}
	if r.Ref.IsPullRequest {
		return nil, usage("%s is a pull or merge request: triage takes an issue, not a pull or merge request", r.Ref.URL())
	}
	if strings.EqualFold(r.Thread.Issue.State, "closed") {
		return []Warning{{Code: WarnIssueClosed, Message: fmt.Sprintf("%s is closed; the run goes on", r.Ref.URL())}}, nil
	}
	return nil, nil
}

// LandCheck is what the landing checks are made of: the forge the diagnosis is
// written to, the repository it goes to, and whether this is a dry run.
type LandCheck struct {
	Forge  issuex.Client
	Target issuex.Repo
	DryRun bool
}

// CheckLandTarget checks, before the first write, that a landing can write to the
// target: the target is a repository, the forge has a credential, and the
// repository can be read and is not archived. A dry run writes nothing and
// needs neither the credential nor the repository.
func CheckLandTarget(ctx context.Context, c LandCheck) *Refusal {
	if !c.Target.Valid() {
		return usage("there is no repository to write the diagnosis to: pass --repo owner/name, or --land none to write nothing")
	}
	if c.DryRun {
		return nil
	}
	if !c.Forge.Authenticated() {
		return &Refusal{
			Category: CategoryAuth,
			Message: fmt.Sprintf("creating or updating an issue in %s needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run or --land none",
				c.Target),
			FixHintEnv: tokenVariable(c),
		}
	}
	repo, err := c.Forge.GetRepository(ctx, c.Target)
	switch {
	case err == nil && repo.Archived:
		return &Refusal{Category: CategoryForge, Message: fmt.Sprintf("repository is archived: %s takes no new issue or edit", c.Target)}
	case err == nil:
		return nil
	case issuex.IsNoToken(err), isUnauthorized(err):
		return &Refusal{Category: CategoryAuth, Message: fmt.Sprintf("the credential cannot read %s: %v", c.Target, err), FixHintEnv: tokenVariable(c)}
	case issuex.IsNotFound(err):
		return &Refusal{Category: CategoryForge, Message: fmt.Sprintf("repository %s was not found: %v", c.Target, err)}
	}
	return &Refusal{Category: CategoryForge, Message: fmt.Sprintf("cannot read repository %s: %v", c.Target, err), Retryable: retryable(err)}
}

// tokenVariable is the variable that holds the credential of the forge: a
// GitLab host, or a forge that reports GitLab's body limit, takes GITLAB_TOKEN.
func tokenVariable(c LandCheck) string {
	if strings.Contains(strings.ToLower(c.Target.Host), "gitlab") || issuex.MaxBodyLength(c.Forge) == issuex.GitLabMaxBodyLength {
		return "GITLAB_TOKEN"
	}
	return "GITHUB_TOKEN"
}

// isUnauthorized reports a forge that refused the credential it was given.
func isUnauthorized(err error) bool {
	var he *issuex.HTTPError
	return errors.As(err, &he) && (he.Status == http.StatusUnauthorized || he.Status == http.StatusForbidden) && !issuex.IsRateLimited(err)
}

// retryable reports whether trying again may succeed: it may after a rate
// limit, a server error or a lost connection, and may not after the forge
// refused the request.
func retryable(err error) bool {
	if issuex.IsRateLimited(err) {
		return true
	}
	var he *issuex.HTTPError
	if errors.As(err, &he) && he.Status >= 400 && he.Status < 500 && he.Status != http.StatusRequestTimeout {
		return false
	}
	return true
}

// PreflightParams is everything the preflight looks at.
type PreflightParams struct {
	Flags  Flags
	Report Report
	// Truncated says the shell cut the input.
	Truncated bool
	LandCheck
}

// PreflightResult is what a run goes on with.
type PreflightResult struct {
	Labels   []string
	Warnings []Warning
}

// Preflight runs the checks in order, before a brief is rendered or a brain
// costs anything: the flags, the input, the landing. The first refusal ends it.
func Preflight(ctx context.Context, p PreflightParams) (PreflightResult, *Refusal) {
	var res PreflightResult
	labels, refusal := CheckFlags(p.Flags)
	if refusal != nil {
		return PreflightResult{}, refusal
	}
	res.Labels = labels

	warnings, refusal := CheckInput(p.Report)
	if refusal != nil {
		return PreflightResult{}, refusal
	}
	res.Warnings = warnings

	if p.Flags.Land == LandNone {
		return res, nil
	}
	c := p.LandCheck
	if p.Report.Kind == KindIssue {
		c.Target = p.Report.Ref.Repo
	}
	if refusal := CheckLandTarget(ctx, c); refusal != nil {
		return PreflightResult{}, refusal
	}
	if p.Report.Kind != KindIssue {
		limit := issuex.MaxBodyLength(c.Forge)
		if length := len(p.Report.Body); p.Truncated || length+renderedMargin > limit {
			return PreflightResult{}, usage("the report cannot land whole: it is %d bytes%s, and an issue in %s takes %d with the diagnosis around it (%d bytes of room are kept for that); pass --land none to keep the diagnosis in the run directory",
				length, cutNote(p.Truncated), c.Target, limit, renderedMargin)
		}
	}
	return res, nil
}

func cutNote(truncated bool) string {
	if truncated {
		return " after it was cut"
	}
	return ""
}
