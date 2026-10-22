// Package box holds the tree a brain works in: the checkout goes in with its
// .git, run files are placed under .agent-fox/, and the tree comes out again
// without .git. Git and the credentials stay on the host.
package box

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// Entry is a provider that the box can open.
type Entry struct {
	// Name is what --sandbox calls it.
	Name string
	// Unconfined says the provider puts no process or network boundary around
	// the tools, so that every run that opens it raises box_unconfined.
	Unconfined bool
	// InPlace says the provider works on the checkout itself, with no copy of it:
	// only `none` does.
	InPlace bool
	// ByName says the programs of a box are called by name and found on the PATH of its
	// image, as under podman, and are not run from the host's file system.
	ByName bool
	// SuppliesCredentials says the box has the credentials of its brain in an environment
	// of its own (hub's profile gives it the vendor key), so that none is needed on the
	// host, none is sent, and a process in the box is told only the names of them.
	SuppliesCredentials bool
	// Remote says the box is on another machine: a call that cannot reach it is an outage
	// to try again, and not a fault of the box.
	Remote bool
	// New returns the provider.
	New func() sandbox.Provider
	// Build, when it is set, makes the provider instead of New, for one that can
	// refuse to be made (it checks the host it is on) or needs paths of the run.
	Build func(ProviderConfig) (sandbox.Provider, error)
	// Explain, when it is set, turns an error of making the provider, or of creating
	// a sandbox with it, that is not a failure already, into the failure to report.
	Explain func(req Request, err error) error
}

// ProviderConfig is what a provider is made with, beyond what its spec says.
type ProviderConfig struct {
	// Ctx is the context of the opening of the box, for a provider that has to connect
	// before it can create one.
	Ctx context.Context
	// ExtraRO are the directories a process started in the box has to read and run:
	// the provider grants them read and execute. A provider that does not confine
	// has no use for them.
	ExtraRO []string
}

// provider makes the provider of the entry.
func (e Entry) provider(cfg ProviderConfig) (sandbox.Provider, error) {
	if e.Build != nil {
		return e.Build(cfg)
	}
	return e.New(), nil
}

// Isolates says the provider puts a process and network boundary around the
// tools: `landlock` and `podman` do, `local` and `none` do not.
func (e Entry) Isolates() bool { return !e.Unconfined }

// Registry holds providers by name.
type Registry struct {
	mu      sync.Mutex
	entries map[string]Entry
}

// NewRegistry returns a registry with no provider.
func NewRegistry() *Registry { return &Registry{entries: map[string]Entry{}} }

// Default is the registry that a Request without a Registry uses: the four
// providers, landlock, podman, local and none.
var Default = func() *Registry {
	r := NewRegistry()
	r.Register(landlockEntry())
	r.Register(podmanEntry())
	r.Register(localEntry())
	r.Register(noneEntry())
	return r
}()

// Register adds a provider, replacing one of the same name.
func (r *Registry) Register(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[e.Name] = e
}

// Names lists the registered providers, sorted.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// Lookup finds a provider. A name that is not registered is a usage failure
// that lists every registered name.
func (r *Registry) Lookup(name string) (Entry, error) {
	r.mu.Lock()
	e, ok := r.entries[name]
	r.mu.Unlock()
	if ok {
		return e, nil
	}
	return Entry{}, &toolio.Failure{
		Category: toolio.CategoryUsage,
		Message:  fmt.Sprintf("--sandbox %q is not a box provider; the providers are %s", name, strings.Join(r.Names(), ", ")),
	}
}
