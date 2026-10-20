// Command af is the agent-fox command line. Every command speaks one
// contract, which internal/toolio implements: one JSON envelope on stdout,
// a fixed table of statuses and exit codes, a resumable needs_human stop, a
// --dry-run that cannot write to the forge, and a schema that af --schema
// prints. This package registers the commands and passes the process's
// arguments and streams to the shell.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"

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
	app := &toolio.App{Version: version, Commit: commit, BuildTime: buildTime}
	register(app)
	return app
}

// schemaFiles are the JSON Schemas of the commands' results, embedded in the
// binary so that af --schema prints them.
//
//go:embed schemas/*.json
var schemaFiles embed.FS

// resultSchema is the embedded schema schemas/<name>.json. A missing file is
// a mistake in the build, which a test finds, and not a condition to handle.
func resultSchema(name string) json.RawMessage {
	data, err := schemaFiles.ReadFile("schemas/" + name + ".json")
	if err != nil {
		panic(fmt.Sprintf("the result schema %q is not embedded: %v", name, err))
	}
	return data
}

// register registers the commands of the binary on app.
func register(app *toolio.App) {
	registerLang(app)
	registerGate(app)
	registerGateCompare(app)
	registerRevertCheck(app)
	registerGuard(app)
	registerMap(app)
	registerTriage(app)
}
