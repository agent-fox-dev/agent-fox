// Command af is the agent-fox command line. Every command speaks one
// contract, which internal/toolio implements: one JSON envelope on stdout,
// a fixed table of statuses and exit codes, a resumable needs_human stop, and a
// --dry-run that cannot write to the forge. The commands of the loops and the ring-1 commands are in
// internal/afcmd; the commands of the agent-fox service client (login,
// workspace, jobs) are in this package, which passes the process's arguments
// and streams to the shell they are registered on.
package main

import (
	"context"
	"os"
	"strings"

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

// newApp returns the shell with the commands of the binary registered: those of afcmd, and
// the client of the agent-fox service, which only the binary has.
func newApp() *toolio.App {
	app := afcmd.New(&afcmd.Config{Version: version, Commit: commit, BuildTime: buildTime})
	registerClient(app)
	return app
}

// registerClient registers the commands of the service client on app.
func registerClient(app *toolio.App) {
	service := map[string]*toolio.Command{"triage": queueCommand(app, "triage"), "fix": queueCommand(app, "fix")}
	app.Mode = modeHook(service)
	app.HelpExtra = modeHelp(app, service)
	for name := range service {
		if loop := app.Command(name); loop != nil {
			// The synopsis of the loop is the one of the local mode.
			loop.Usage = strings.Replace(loop.Usage, "<issue|file|text|-> ", "<issue|file|text|-> --local ", 1)
		}
	}
	registerLogin(app)
	registerWorkspaceList(app)
	registerWorkspaceSubscribe(app)
	registerWorkspaceGet(app)
	registerWorkspaceUnsubscribe(app)
	registerJobsList(app)
	registerJobsGet(app)
	registerJobsWatch(app)
	registerJobsCancel(app)
	registerJobsAnswer(app)
	registerJobsRetry(app)
}
