package afcmd

import (
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/brain"
)

// LoopOptions are the options of af triage and af fix for a caller that runs them in its own
// process, as toolio.Call.Options, in place of their flags: the brain and its bounds, and where
// the work lands. A zero field keeps the command's default.
type LoopOptions struct {
	// Brain, Sandbox, Budget, MaxTurns and Script are --brain, --sandbox, --budget,
	// --max-turns and --script.
	Brain    string
	Sandbox  string
	Budget   float64
	MaxTurns int
	Script   string
	// Land is --land: pr, branch or none for a fix, issue or none for a triage.
	Land string
}

// loopOptions is the value of a call to the loop command as LoopOptions.
func loopOptions(command string, v any) (LoopOptions, error) {
	lo, ok := v.(LoopOptions)
	if !ok {
		return LoopOptions{}, fmt.Errorf("%s takes afcmd.LoopOptions from a call, not %T", command, v)
	}
	return lo, nil
}

// apply sets the brain settings that are not zero over the defaults.
func (lo LoopOptions) apply(s *brain.Settings) {
	if lo.Brain != "" {
		s.Brain = lo.Brain
	}
	if lo.Sandbox != "" {
		s.Sandbox = lo.Sandbox
	}
	if lo.Budget != 0 {
		s.Budget = lo.Budget
	}
	if lo.MaxTurns != 0 {
		s.MaxTurns = lo.MaxTurns
	}
	if lo.Script != "" {
		s.Script = lo.Script
	}
}
