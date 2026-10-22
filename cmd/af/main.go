// Command af is the agent-fox command line. Every command speaks one
// contract, which internal/toolio implements: one JSON envelope on stdout,
// a fixed table of statuses and exit codes, a resumable needs_human stop, a
// --dry-run that cannot write to the forge, and a schema that af --schema
// prints. The commands are in internal/afcmd; this package passes the
// process's arguments and streams to the shell they are registered on.
package main

import (
	"context"
	"os"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The identity of the build, set with -ldflags -X by make build.
var (
	version   string
	commit    string
	buildTime string
)

func main() {
	os.Exit(newApp().Main(context.Background(), os.Args, os.Stdin, os.Stdout, os.Stderr))
}

// newApp returns the shell with the commands of the binary registered.
func newApp() *toolio.App {
	return afcmd.New(&afcmd.Config{Version: version, Commit: commit, BuildTime: buildTime})
}
