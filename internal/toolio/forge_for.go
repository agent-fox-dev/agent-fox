package toolio

import (
	"errors"
	"fmt"

	"github.com/agent-fox-dev/aflib/issuex"
)

// ForgeFor builds a forge client for a repository the command learned at run
// time, such as the one a run recorded: the shell builds the client of a command
// from the input and the flags before the command runs, and a command that
// carries on an earlier run knows its repository only from that run. The client
// is built the way the shell builds its own: through App.NewForge, with the
// repository and its host, the user agent of the shell and the --dir of the
// command, refusing every write under --dry-run, and closed once, when the
// command has returned, whatever it returned.
//
// A host that names neither forge is a usage failure that says where to give its
// API URL; any other error is a forge failure.
func (r *Run) ForgeFor(repo issuex.Repo) (issuex.Client, error) {
	opts := issuex.Options{UserAgent: r.app.name() + "/" + r.app.version().Version, Dir: r.Dir, Repo: repo}
	client, err := r.app.newForge()(opts)
	switch {
	case err == nil:
	case errors.Is(err, issuex.ErrAmbiguousForge), errors.Is(err, issuex.ErrUnsupportedForge):
		return nil, &Failure{Category: CategoryUsage, Message: err.Error(), FixHint: &FixHint{Env: "GITHUB_API_URL"}}
	default:
		return nil, &Failure{Category: CategoryForge, Message: fmt.Sprintf("cannot build the forge client: %v", err)}
	}
	r.mu.Lock()
	r.closers = append(r.closers, func() { _ = client.Close() }) // a client that fails to close its idle connections has nothing more to tell
	r.mu.Unlock()
	if r.DryRun {
		return DryRunForge(client, r.SideEffect), nil
	}
	return client, nil
}

// closeClients closes the clients ForgeFor built.
func (r *Run) closeClients() {
	r.mu.Lock()
	closers := r.closers
	r.closers = nil
	r.mu.Unlock()
	for _, closeClient := range closers {
		closeClient()
	}
}
