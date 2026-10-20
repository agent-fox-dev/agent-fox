package toolio

import (
	"errors"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/aflib/issuex"
)

// newForge is the function that builds forge clients.
func (a *App) newForge() func(issuex.Options) (issuex.Client, error) {
	if a.NewForge != nil {
		return a.NewForge
	}
	return issuex.NewWithOptions
}

// forgeFor builds the forge client of a command that uses one, and says
// which repository it works on.
//
// The client is built for the repository of the issue URL when that is the
// input, else for --repo when the command takes it and it is given, else for
// whatever the environment and the origin of --dir say. If issuex cannot tell
// which forge that is, an issue URL is a usage error (its host is unknown to
// us), and anything else gets a client that does nothing and no repository:
// the commands that need a forge refuse in their own preflight.
func (a *App) forgeFor(cmd *Command, values *flagValues, positional []string) (issuex.Client, issuex.Repo, error) {
	var issue *issuex.IssueRef
	if cmd.Input != InputNone && len(positional) > 0 && positional[0] != "-" {
		if ref, ok := issuex.ParseIssueURL(positional[0]); ok {
			issue = &ref
		}
	}

	var repo issuex.Repo
	if cmd.Repo && values.repo != "" {
		parsed, ok := issuex.ParseRepo(values.repo)
		if !ok {
			return nil, issuex.Repo{}, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("--repo %q is not of the form owner/repo", values.repo)}
		}
		repo = parsed
	}

	opts := issuex.Options{UserAgent: a.name() + "/" + a.version().Version, Dir: values.dir}
	var target issuex.Repo
	switch {
	case issue != nil:
		if repo.Valid() && !sameRepo(repo, issue.Repo) {
			return nil, issuex.Repo{}, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("--repo %s is not the repository of %s", repo, issue.URL())}
		}
		opts.Repo, target = issue.Repo, issue.Repo
	case repo.Valid():
		opts.Repo, target = repo, repo
	}

	client, err := a.newForge()(opts)
	switch {
	case err == nil:
	case !errors.Is(err, issuex.ErrAmbiguousForge) && !errors.Is(err, issuex.ErrUnsupportedForge):
		return nil, issuex.Repo{}, &Failure{Category: CategoryForge, Message: fmt.Sprintf("cannot build the forge client: %v", err)}
	case issue != nil:
		return nil, issuex.Repo{}, &Failure{
			Category: CategoryUsage,
			Message:  err.Error(),
			FixHint:  &FixHint{Env: "GITHUB_API_URL"},
		}
	default:
		return issuex.NewNoOp(), issuex.Repo{}, nil
	}

	if !target.Valid() {
		if detected, ok := issuex.DetectRepo(values.dir); ok {
			target = detected
		}
	}
	return client, target, nil
}

// sameRepo reports whether a and b name the same repository. Forges do not
// tell "Owner/Repo" from "owner/repo".
func sameRepo(a, b issuex.Repo) bool {
	return strings.EqualFold(a.Owner, b.Owner) && strings.EqualFold(a.Name, b.Name)
}
