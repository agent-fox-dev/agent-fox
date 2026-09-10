package toolio

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// ModelEnv is the variable that selects the model. AGENTKIT_MODEL is honoured
// as a fallback so a shell already configured for the SDK's own examples
// works here too.
const (
	ModelEnv         = "AF_MODEL"
	ModelEnvFallback = "AGENTKIT_MODEL"
	// VendorEnv selects which tier table SIMPLE/STANDARD/ADVANCED resolve
	// against. It has no effect on a model named by id.
	VendorEnv = "AF_MODEL_VENDOR"
)

// DefaultModel is the tier every tool runs on unless told otherwise. It is a
// tier rather than a model id so that the tools keep working when a vendor
// retires an id, and so that nobody has to know which id is current this
// month.
const DefaultModel = "STANDARD"

// Common is the flag set every agent-fox tool shares.
//
// The tools take exactly one positional argument — text, a file path, a
// GitHub URL, or "-" for stdin — and everything else is a flag. Keeping the
// flags identical across the tools is deliberate: a caller that can drive one
// can drive all of them.
type Common struct {
	Dir          string
	Model        string
	Vendor       string
	Variant      string
	MaxTurns     int
	Budget       float64
	Timeout      time.Duration
	TrustProject bool
	Verbose      bool
	Quiet        bool
	ShowText     bool
	Version      bool
}

// Register adds the shared flags to fs.
func (c *Common) Register(fs *flag.FlagSet) {
	fs.StringVar(&c.Dir, "dir", ".", "the repository to work in; the file tools cannot reach outside it")
	fs.StringVar(&c.Model, "model", "", "model tier (SIMPLE, STANDARD, ADVANCED) or catalog spec; default $"+ModelEnv+", else "+DefaultModel)
	fs.StringVar(&c.Vendor, "vendor", "", "vendor whose tier table the tier names resolve against; default $"+VendorEnv)
	fs.StringVar(&c.Variant, "variant", "", "tier variant, e.g. extended")
	fs.IntVar(&c.MaxTurns, "max-turns", 0, "per-phase turn ceiling")
	fs.Float64Var(&c.Budget, "budget", 0, "per-phase spend ceiling, in dollars")
	fs.DurationVar(&c.Timeout, "phase-timeout", 0, "wall-clock ceiling on one phase")
	fs.BoolVar(&c.TrustProject, "trust-project", false, "admit AGENTS.md, CLAUDE.md and .specs/steering.md into the system prompt")
	fs.BoolVar(&c.Verbose, "verbose", false, "trace tool calls and timings on stderr")
	fs.BoolVar(&c.Quiet, "quiet", false, "print nothing on stderr")
	fs.BoolVar(&c.ShowText, "show-text", false, "stream the model's prose to stderr")
	fs.BoolVar(&c.Version, "version", false, "print the build identity and exit")
}

// ModelSpec is the model the run will use, after the environment is consulted.
func (c *Common) ModelSpec() string {
	if c.Model != "" {
		return c.Model
	}
	if v := os.Getenv(ModelEnv); v != "" {
		return v
	}
	if v := os.Getenv(ModelEnvFallback); v != "" {
		return v
	}
	return DefaultModel
}

// VendorName is the tier table to resolve against.
func (c *Common) VendorName() string {
	if c.Vendor != "" {
		return c.Vendor
	}
	return os.Getenv(VendorEnv)
}

// Bounds are the per-phase ceilings, with the tool's own defaults filled in
// where the operator set nothing.
func (c *Common) Bounds(defaults agentrun.Bounds) agentrun.Bounds {
	b := defaults
	if c.MaxTurns > 0 {
		b.MaxTurns = c.MaxTurns
	}
	if c.Budget > 0 {
		b.MaxBudgetUSD = c.Budget
	}
	if c.Timeout > 0 {
		b.Timeout = c.Timeout
	}
	return b
}

// Workspace resolves --dir and roots the file tools at it. Every path a tool
// is handed is resolved against this root, symlinks included, so a path that
// escapes is refused by the tool rather than by a paragraph in a prompt.
func (c *Common) Workspace() (*tools.Workspace, error) {
	abs, err := filepath.Abs(c.Dir)
	if err != nil {
		return nil, fmt.Errorf("resolving --dir %q: %w", c.Dir, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("--dir %s: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--dir %s is not a directory", abs)
	}
	ws, err := tools.NewWorkspace(abs)
	if err != nil {
		return nil, fmt.Errorf("rooting the file tools at %s: %w", abs, err)
	}
	return ws, nil
}

// ResolveModel resolves the model spec and checks that this shell can
// authenticate to its vendor.
//
// Both happen before a prompt is built. An operator learns about a retired
// platform variable, an unknown model or a missing key in the first second,
// rather than in the tenth minute of a run they are paying for.
func (c *Common) ResolveModel() (*ModelChoice, error) {
	return c.ResolveModelNamed(c.ModelSpec())
}

// ResolveModelNamed resolves one model by tier name or catalog spec, against
// the run's vendor and variant, and checks its credential. It is what a tool
// that runs one phase on another model than the rest resolves that model
// with, so the second choice obeys the same rules as the first.
func (c *Common) ResolveModelNamed(spec string) (*ModelChoice, error) {
	if err := agentrun.CheckRetiredPlatformVars(); err != nil {
		return nil, err
	}
	m, thinking, err := agentrun.ResolveModel(spec, c.Variant, c.VendorName())
	if err != nil {
		return nil, err
	}
	if err := agentrun.CheckCredentials(m); err != nil {
		return nil, err
	}
	return &ModelChoice{Model: m, Thinking: thinking, Spec: spec}, nil
}

// ModelChoice is the resolved model and the spec string that produced it.
// The envelope reports both, so a surprising answer can be traced back to
// what was asked for.
type ModelChoice struct {
	Model    *core.Model
	Thinking core.ThinkingLevel
	Spec     string
}

// SplitArgs separates the one positional argument from the flags, allowing
// flags on either side of it.
//
// A report is frequently a multi-word string, and requiring it last is a
// papercut a caller hits every time. Anything after the first non-flag token
// that itself starts with "-" is still parsed as a flag, except the bare "-",
// which means stdin.
func SplitArgs(fs *flag.FlagSet, argv []string) (positional string, err error) {
	var rest []string
	var positionals []string

	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--":
			positionals = append(positionals, argv[i+1:]...)
			i = len(argv)
		case a == "-":
			positionals = append(positionals, a)
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
			// A flag written as "--name value" consumes the next token; one
			// written as "--name=value" does not. Booleans consume nothing.
			if !strings.Contains(a, "=") && i+1 < len(argv) && needsValue(fs, a) {
				i++
				rest = append(rest, argv[i])
			}
		default:
			positionals = append(positionals, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		return "", err
	}
	switch len(positionals) {
	case 0:
		return "", nil
	case 1:
		return positionals[0], nil
	default:
		return "", fmt.Errorf("expected one input, got %d: %s — quote a multi-word report",
			len(positionals), strings.Join(positionals, " "))
	}
}

// needsValue reports whether a flag takes a separate value token. A boolean
// flag does not, which is what makes `--dry-run ./crash.log` work.
func needsValue(fs *flag.FlagSet, arg string) bool {
	name := strings.TrimLeft(arg, "-")
	f := fs.Lookup(name)
	if f == nil {
		return false // unknown: let Parse report it
	}
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !bf.IsBoolFlag()
}
