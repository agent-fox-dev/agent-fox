package brain

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// goos is the operating system the default sandbox is chosen for; a test replaces it.
var goos = runtime.GOOS

// Where the sandbox of a run was chosen: --sandbox, AF_SANDBOX or the default.
const (
	SandboxFlag    = "flag"
	SandboxEnv     = "env"
	SandboxDefault = "default"
)

// The names of the brains this release has.
const (
	AgentkitName = "agentkit"
	ScriptedName = "scripted"
)

// sandboxVar is the variable that names the box provider when --sandbox does not.
const sandboxVar = "AF_SANDBOX"

// Defaults are what a command supplies for the shared flags.
type Defaults struct {
	// Brain is the brain that is chosen without --brain; agentkit when empty.
	Brain string
	// Sandbox is the provider chosen without --sandbox, in place of AF_SANDBOX and of
	// the default of the operating system; empty is to choose by those.
	Sandbox string
	// Budget is the cost bound in dollars, and MaxTurns the turn bound.
	Budget   float64
	MaxTurns int
}

// Settings are what the shared flags parsed to.
type Settings struct {
	Brain    string
	Sandbox  string
	Budget   float64
	MaxTurns int
	Script   string
	// BorrowedHands says the agentkit driver runs on the host over the box's tools
	// instead of inside the box.
	BorrowedHands bool
	// SandboxSource says where Sandbox came from once Validate has chosen it.
	SandboxSource string

	registry *box.Registry
}

// Flags registers --brain, --sandbox, --budget, --max-turns and --script on a
// command's flags, with the defaults the command supplies, and returns where
// they are parsed to. Validate checks them after the parse.
func Flags(fs *flag.FlagSet, d Defaults) *Settings {
	if d.Brain == "" {
		d.Brain = AgentkitName
	}
	s := &Settings{}
	fs.StringVar(&s.Brain, "brain", d.Brain, "the `brain` that runs the brief: agentkit or scripted")
	fs.StringVar(&s.Sandbox, "sandbox", d.Sandbox, "the box `provider` that holds the tree: landlock, podman, local or none; without it AF_SANDBOX, else landlock on Linux and podman anywhere else. local and none have no isolation")
	fs.Float64Var(&s.Budget, "budget", d.Budget, "stop the brain after this many `dollars` of model cost")
	fs.IntVar(&s.MaxTurns, "max-turns", d.MaxTurns, "stop the brain after this many `turns`")
	fs.StringVar(&s.Script, "script", "", "the `file` the scripted brain replays; only with --brain scripted")
	fs.BoolVar(&s.BorrowedHands, "borrowed-hands", false, "run the agentkit driver on the host over the box's tools, not inside the box; only with --brain agentkit")
	return s
}

// Validate checks the values of the flags, before any box is opened: a budget
// that is not a finite number of dollars above 0, fewer than 1 turn, a script that
// does not go with the brain, borrowed hands with a brain that has none to
// borrow, and a box provider that is not one. It then chooses the provider (see
// ValidateWith).
func (s *Settings) Validate() error { return s.ValidateWith(os.LookupEnv) }

// ValidateWith is Validate with the host's environment given. The provider is the
// first of: --sandbox (or the default a command pinned for it), AF_SANDBOX, and
// landlock on Linux and podman on any other operating system. Choosing opens no box
// and never falls back to another provider; Sandbox and SandboxSource say what was
// chosen. An empty AF_SANDBOX is unset, and one that --sandbox makes unused is not
// read.
func (s *Settings) ValidateWith(get Getenv) error {
	usage := func(format string, args ...any) error {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf(format, args...)}
	}
	switch {
	case math.IsNaN(s.Budget) || math.IsInf(s.Budget, 0) || s.Budget <= 0:
		return usage("--budget must be a finite number of dollars above 0, not %v", s.Budget)
	case s.MaxTurns < 1:
		return usage("--max-turns must be at least 1, not %d", s.MaxTurns)
	case s.Brain == ScriptedName && s.Script == "":
		return usage("--script is required with --brain scripted: the file of steps to replay")
	case s.Brain != ScriptedName && s.Script != "":
		return usage("--script goes only with --brain scripted, not with --brain %s", s.Brain)
	case s.BorrowedHands && s.Brain != AgentkitName:
		return usage("--borrowed-hands goes only with --brain %s, the driver that has hands to borrow, not with --brain %s", AgentkitName, s.Brain)
	}
	return s.chooseSandbox(get)
}

// chooseSandbox sets Sandbox and SandboxSource from --sandbox, AF_SANDBOX or the
// default of the operating system, and refuses a name that is not a provider.
func (s *Settings) chooseSandbox(get Getenv) error {
	reg := s.registry
	if reg == nil {
		reg = box.Default
	}
	name, source := s.Sandbox, SandboxFlag
	if name == "" {
		if v, ok := get(sandboxVar); ok && v != "" {
			if _, err := reg.Lookup(v); err != nil {
				return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("%s=%q is not a box provider; the providers are %s", sandboxVar, v, strings.Join(reg.Names(), ", "))}
			}
			name, source = v, SandboxEnv
		} else {
			name, source = defaultSandbox(), SandboxDefault
		}
	}
	if _, err := reg.Lookup(name); err != nil {
		return err
	}
	s.Sandbox, s.SandboxSource = name, source
	return nil
}

// defaultSandbox is the provider of an operating system: landlock on Linux, where
// it confines a process with no engine, and podman anywhere else.
func defaultSandbox() string {
	if goos == "linux" {
		return "landlock"
	}
	return "podman"
}
