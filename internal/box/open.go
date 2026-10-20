package box

import (
	"context"
	"fmt"
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
	// Env is the environment of the box's tools, and nothing else.
	Env map[string]string
	// TTL bounds the box's lifetime; zero is the provider's default.
	TTL time.Duration
	// Record notes what happened in the run's ledger.
	Record func(kind string, data any) error
	// Warn raises a warning of the run.
	Warn func(code, message string)
	// Registry is where Provider is looked up; nil is Default.
	Registry *Registry
}

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

	provider := entry.New()
	spec := sandbox.Spec{
		Env:     req.Env,
		Labels:  map[string]string{"af.run_id": req.RunID, "af.tool": req.Tool},
		TTL:     req.TTL,
		Network: networkFor(provider.Capabilities()),
	}
	sb, err := provider.Create(ctx, spec)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("the %s provider could not create the box: %v", entry.Name, err)}
	}
	s := &Session{req: req, sb: sb, provider: entry.Name, hostDir: hostDir}
	s.record("box_open", map[string]any{"provider": entry.Name})

	if err := s.sync(ctx); err != nil {
		return nil, s.abandon(ctx, err)
	}
	tools, err := sb.Tools(ctx, sandbox.ToolOptions{})
	if err != nil {
		return nil, s.abandon(ctx, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("the %s provider could not give the box's tools: %v", entry.Name, err)})
	}
	s.tools = tools

	if entry.Unconfined && req.Warn != nil {
		req.Warn("box_unconfined", unconfinedMessage)
	}
	return s, nil
}

// topLevel checks that dir is the top level of a git work tree, and returns
// it. Both paths are compared after their links are resolved, because git
// prints the real path of the top level.
func topLevel(ctx context.Context, dir string) (string, error) {
	g := gitx.New(dir, nil)
	if !g.IsRepo(ctx) {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s is not inside a git work tree: the box needs the top level of one, with its .git", dir)}
	}
	top, err := g.TopLevel(ctx)
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot find the top level of the work tree of %s: %v", dir, err)}
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s: %v", dir, err)}
	}
	if realTop, err := filepath.EvalSymlinks(top); err == nil {
		top = realTop
	}
	if filepath.Clean(real) != filepath.Clean(top) {
		return "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--dir %s is inside a work tree but is not its top level, %s: the box needs the top level, so that .git is at its root", dir, top)}
	}
	return real, nil
}
