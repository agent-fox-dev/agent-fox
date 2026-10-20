package brain

import (
	"flag"
	"fmt"
	"math"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The names of the brains this release has.
const (
	AgentkitName = "agentkit"
	ScriptedName = "scripted"
)

// DefaultSandbox is the box provider that is chosen without --sandbox, until
// the scope that adds more providers makes another the default where it can.
const DefaultSandbox = "local"

// Defaults are what a command supplies for the shared flags.
type Defaults struct {
	// Brain is the brain that is chosen without --brain; agentkit when empty.
	Brain string
	// Sandbox is the provider chosen without --sandbox; local when empty.
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
}

// Flags registers --brain, --sandbox, --budget, --max-turns and --script on a
// command's flags, with the defaults the command supplies, and returns where
// they are parsed to. Validate checks them after the parse.
func Flags(fs *flag.FlagSet, d Defaults) *Settings {
	if d.Brain == "" {
		d.Brain = AgentkitName
	}
	if d.Sandbox == "" {
		d.Sandbox = DefaultSandbox
	}
	s := &Settings{}
	fs.StringVar(&s.Brain, "brain", d.Brain, "the `brain` that runs the brief: agentkit or scripted")
	fs.StringVar(&s.Sandbox, "sandbox", d.Sandbox, "the box `provider` that holds the tree; local has no isolation")
	fs.Float64Var(&s.Budget, "budget", d.Budget, "stop the brain after this many `dollars` of model cost")
	fs.IntVar(&s.MaxTurns, "max-turns", d.MaxTurns, "stop the brain after this many `turns`")
	fs.StringVar(&s.Script, "script", "", "the `file` the scripted brain replays; only with --brain scripted")
	return s
}

// Validate checks the values of the flags, before any box is opened: a budget
// that is not a finite number of dollars above 0, fewer than 1 turn, and a
// script that does not go with the brain.
func (s *Settings) Validate() error {
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
	}
	return nil
}
