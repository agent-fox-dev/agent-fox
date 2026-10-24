package policy

import (
	"context"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/guard"
)

// handsGuard asks the hands' guard whether it refuses a shell command line,
// and returns its reason when it does. It is a variable so that a test can put
// a stub in its place.
//
// The hands' guard is a floor under the rules of this package: it is asked
// only for what it knows and they do not. It reads the line with the reader
// parse uses, so a line it cannot read never reaches it, and it refuses an
// assignment to a variable that changes which program runs or what is loaded
// into it (PATH, LD_PRELOAD, BASH_ENV, …), and a line that runs no program.
// Which programs may run is a decision of the policy (rule 6,
// program_not_allowed), so the programs the line runs are the ones it is told
// it may use. It can only deny: whatever it allows, the rules after it still
// apply.
var handsGuard = func(command string) (reason string, refused bool) {
	cmds, err := guard.Commands(command)
	if err != nil {
		return err.Error(), true
	}
	var programs []string
	for _, c := range cmds {
		if len(c.Argv) > 0 {
			programs = append(programs, c.Argv[0])
		}
	}
	restricted := guard.Restricted(guard.Options{
		AllowedPrograms:     programs,
		AllowShellOperators: true,
		AllowEnvPrefixes:    true,
	})
	d := restricted(context.Background(), core.BeforeToolCallContext{
		ToolName:  "execute",
		Arguments: map[string]any{"command": command},
	})
	return d.Reason, d.Block
}
