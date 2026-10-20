package brain

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
)

// Getenv looks a host variable up: its value, and whether it is set.
type Getenv func(name string) (string, bool)

// modelVar is the variable that names the model of the agentkit brain.
const modelVar = "AF_MODEL"

// Options is what a brain is constructed from.
type Options struct {
	// Model is the model to use, from AF_MODEL; empty is the driver's default.
	Model string
	// Script is the --script path of the scripted brain.
	Script string
	// NewProvider makes the model provider of the driver; faux in tests. Nil is
	// the provider of the model, with the credentials of the environment.
	NewProvider func(model string) (core.ProviderClient, error)
	// Getenv reads the host's environment; nil is os.LookupEnv.
	Getenv Getenv
}

// lookup is the Getenv of the options.
func (o Options) lookup() Getenv {
	if o.Getenv != nil {
		return o.Getenv
	}
	return os.LookupEnv
}

// Entry is a brain that can be chosen.
type Entry struct {
	Name string
	// CostSource says where the cost of its runs comes from: measured, reported
	// or none.
	CostSource string
	// BoxEnv names the host variables that reach the box of this brain, and no
	// others do. It is empty for the brains that run in the process.
	BoxEnv []string
	New    func(Options) (Brain, error)
}

// Registry holds the brains by name.
type Registry struct {
	mu      sync.Mutex
	entries map[string]Entry
}

// NewRegistry returns a registry with no brain.
func NewRegistry() *Registry { return &Registry{entries: map[string]Entry{}} }

// Default is the registry the commands use: agentkit and scripted.
var Default = NewRegistry()

// Register adds a brain, replacing one of the same name.
func (r *Registry) Register(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[e.Name] = e
}

// Names lists the registered brains, sorted.
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

// Lookup finds a brain. A name that is not registered is a usage failure that
// lists every registered name.
func (r *Registry) Lookup(name string) (Entry, error) {
	r.mu.Lock()
	e, ok := r.entries[name]
	r.mu.Unlock()
	if ok {
		return e, nil
	}
	return Entry{}, &toolio.Failure{
		Category: toolio.CategoryUsage,
		Message:  fmt.Sprintf("--brain %q is not a brain; the brains are %s", name, strings.Join(r.Names(), ", ")),
	}
}

// New constructs the brain of the name, or refuses the name and constructs
// nothing.
func (r *Registry) New(name string, opts Options) (Brain, error) {
	e, err := r.Lookup(name)
	if err != nil {
		return nil, err
	}
	return e.New(opts)
}

// BuildBoxEnv is the environment of the box of a brain: the host values of the
// names in the entry's BoxEnv that are set, and nothing else. It is never nil.
func BuildBoxEnv(e Entry, getenv Getenv) map[string]string {
	env := map[string]string{}
	if getenv == nil {
		getenv = os.LookupEnv
	}
	for _, name := range e.BoxEnv {
		if v, ok := getenv(name); ok {
			env[name] = v
		}
	}
	return env
}
