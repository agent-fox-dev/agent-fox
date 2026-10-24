package box

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// Request is what a box is opened for.
type Request struct {
	// Provider is the name of the provider, as --sandbox gives it.
	Provider string
	// HostDir is the checkout, which must be the top level of a work tree.
	HostDir string
	// RunID and Tool label the sandbox: af.run_id and af.tool.
	RunID string
	Tool  string
	// Labels are put on the box beside af.run_id and af.tool: the job and the attempt that a
	// leased box is for. A label of these two names that is given here is the one given.
	Labels map[string]string
	// Env is what the box's tools are given on top of what the provider gives
	// them, which for `local` is the host's PATH, LANG and TERM and a HOME and a
	// TMPDIR of the box's own. No token and no key is among it unless a brain asks
	// for it by name.
	Env map[string]string
	// TTL bounds the box's lifetime; zero is the provider's default.
	TTL time.Duration
	// Image is the image of a provider that runs one; empty is the provider's own.
	Image string
	// Network is what the tools may reach: NetworkNone, NetworkOpen, or
	// NetworkHost, which says the provider enforces nothing. Empty asks for none
	// where the provider can, and takes the mode it offers otherwise.
	Network string
	// Placement is where the brain runs for this run, as the ledger records it.
	Placement string
	// RequireLandlock makes the creation fail unless the sandbox confines itself
	// with Landlock.
	RequireLandlock bool
	// Defaulted says the provider was not asked for with --sandbox but chosen by
	// AF_SANDBOX or by the platform, so that the failure to open it says how to choose
	// another.
	Defaulted bool
	// ExtraRO are the directories a process started in the box has to read and run
	// (af), for a provider that confines the box to its workspace.
	ExtraRO []string
	// Record notes what happened in the run's ledger.
	Record func(kind string, data any) error
	// Warn raises a warning of the run.
	Warn func(code, message string)
	// Registry is where Provider is looked up; nil is Default.
	Registry *Registry
}

// The networks a request asks for, and a session reports.
const (
	NetworkNone = "none"
	NetworkOpen = "open"
	NetworkHost = "host"
)

// unconfinedMessage is what box_unconfined says.
const unconfinedMessage = "the box has no process or network isolation: the local provider runs the tools in this process, with its privileges, on this machine's file system and network"

// Open creates the box, syncs the host directory into it with its .git and
// returns the session. The provider is looked up first, then HostDir is
// checked, so that a refused request creates nothing.
//
// A provider that cannot create the sandbox is failed/sandbox and not
// retryable. A sandbox that cannot be synced or given its tools is closed
// before Open returns, and a sync failure is retryable.
func Open(ctx context.Context, req Request) (*Session, error) {
	s, err := openBox(ctx, req)
	return s, req.defaultHint(err)
}

// defaultHint adds to the fix_hint of a sandbox failure how to choose another
// provider, when the provider was chosen by default: the flag and the variable.
func (r Request) defaultHint(err error) error {
	var failure *toolio.Failure
	if !r.Defaulted || !errors.As(err, &failure) || failure.Category != toolio.CategorySandbox {
		return err
	}
	out := *failure
	hint := toolio.FixHint{}
	if failure.FixHint != nil {
		hint = *failure.FixHint
	}
	hint.Flag, hint.Env = "--sandbox", "AF_SANDBOX"
	out.FixHint = &hint
	return &out
}

// explain is the failure of an error of the provider: a failure already is one, and
// otherwise the entry says what it was, and failing that it is a sandbox failure
// that carries the error.
func (e Entry) explain(req Request, what string, err error) error {
	var failure *toolio.Failure
	if errors.As(err, &failure) {
		return err
	}
	if e.Explain != nil {
		return e.Explain(req, err)
	}
	return &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("the %s provider %s: %v", e.Name, what, err)}
}

// openBox is Open before the hint of a default provider is added to its failure.
func openBox(ctx context.Context, req Request) (*Session, error) {
	registry := req.Registry
	if registry == nil {
		registry = Default
	}
	entry, err := registry.Lookup(req.Provider)
	if err != nil {
		return nil, err
	}
	hostDir, err := topLevel(ctx, req.HostDir)
	if err != nil {
		return nil, err
	}

	if entry.InPlace {
		return openInPlace(req, entry, hostDir)
	}
	provider, err := entry.provider(ProviderConfig{ExtraRO: req.ExtraRO, Ctx: ctx})
	if err != nil {
		return nil, entry.explain(req, "cannot be made", err)
	}
	labels := map[string]string{"af.run_id": req.RunID, "af.tool": req.Tool}
	maps.Copy(labels, req.Labels)
	spec := sandbox.Spec{
		Env:             req.Env,
		Labels:          labels,
		TTL:             req.TTL,
		Image:           req.Image,
		RequireLandlock: req.RequireLandlock,
		Network:         networkFor(provider.Capabilities(), req.Network),
	}
	sb, err := provider.Create(ctx, spec)
	if err != nil {
		closeProvider(provider)
		return nil, entry.explain(req, "could not create the box", err)
	}
	made := sb.Spec()
	image := made.Image
	if image == "" {
		image = req.Image
	}
	s := &Session{req: req, sb: sb, provider: entry.Name, hostDir: hostDir, network: networkName(req.Network, entry, made.Network.Mode), image: image,
		byName: entry.ByName, supplies: entry.SuppliesCredentials, remote: entry.Remote, closer: providerCloser(provider)}
	s.record("box_open", map[string]any{"provider": entry.Name, "image": image, "network": s.network, "placement": req.Placement})

	if err := s.sync(ctx); err != nil {
		return nil, s.abandon(ctx, err)
	}
	tools, err := sb.Tools(ctx, sandbox.ToolOptions{})
	if err != nil {
		return nil, s.abandon(ctx, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("the %s provider could not give the box's tools: %v", entry.Name, err)})
	}
	s.tools = tools
	if err := s.scrubGit(ctx); err != nil {
		return nil, s.abandon(ctx, err)
	}

	if entry.Unconfined && req.Warn != nil {
		req.Warn("box_unconfined", unconfinedMessage)
	}
	return s, nil
}

// topLevel checks that dir is the top level of a git work tree that keeps
// its .git in it, and returns it with its links resolved. Both are compared as
// the files they are, because git prints the real path of the top level, and
// the file system may not tell two spellings of one path apart.
//
// A work tree whose .git is a file (a linked worktree, a submodule) keeps git's
// state outside the tree, in the host's repository: a commit made in a box that
// held it would write there. The box has no such tree.
func topLevel(ctx context.Context, dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s: %v", dir, err)}
	}
	g := gitx.New(abs, nil)
	if !g.IsRepo(ctx) {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s is not inside a git work tree: the box needs the top level of one, with its .git", dir)}
	}
	top, err := g.TopLevel(ctx)
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot find the top level of the work tree of %s: %v", dir, err)}
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s: %v", dir, err)}
	}
	given, gerr := os.Stat(real)
	found, ferr := os.Stat(top)
	if gerr != nil || ferr != nil || !os.SameFile(given, found) {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s is inside a work tree but is not its top level, %s: the box needs the top level, so that .git is at its root", dir, top)}
	}
	if info, err := os.Lstat(filepath.Join(real, ".git")); err != nil || !info.IsDir() {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s does not hold its own .git directory (a linked worktree or a submodule keeps git's state in another repository, which a commit in the box would write): the box needs a checkout of its own", dir)}
	}
	return real, nil
}

// providerCloser is the provider as something to close once the box is closed, or nil when
// the provider holds nothing (a gateway holds a connection).
func providerCloser(p sandbox.Provider) io.Closer {
	if c, ok := p.(io.Closer); ok {
		return c
	}
	return nil
}

// closeProvider closes a provider that made no box.
func closeProvider(p sandbox.Provider) {
	if c := providerCloser(p); c != nil {
		_ = c.Close() // nothing was made with it
	}
}
