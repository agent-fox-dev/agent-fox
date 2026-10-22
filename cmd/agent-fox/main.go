// Command agent-fox is the agent-fox service: one long-running process that
// starts from one config.toml, keeps its state in one SQLite file, and treats hub
// as the only authority on who is calling and which workspaces they may see.
//
// Its stderr is a log, one JSON record on each line (log/slog), from the first
// line it writes; the one exception is the plain-text usage that a command line
// it cannot parse gets. docs/cli.md describes the flags and the exit codes,
// docs/configuration.md the file, and docs/api.md the routes.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The identity of the build, set with -ldflags -X by make build.
var (
	version   string
	commit    string
	buildTime string
)

// name is the program's name, in the version document and the usage.
const name = "agent-fox"

// usage is what --help prints, and what a command line that cannot be parsed is
// followed by.
const usage = `Usage: agent-fox [--config PATH]
       agent-fox --version
       agent-fox --help

agent-fox is the long-running service of agent-fox: it keeps the workspaces that
callers subscribe, and asks hub who is calling.

Flags:
  --config PATH  the configuration file. Without it: ./config.toml, then
                 $XDG_CONFIG_HOME/agent-fox/config.toml
                 ($HOME/.config/agent-fox/config.toml if XDG_CONFIG_HOME is unset).
  --version      print the version document, one JSON object, and exit.
  --help         print this usage and exit.

Exit codes:
  0  it stopped on SIGINT or SIGTERM, or it printed the version or this usage.
  1  the listener could not be bound, or it failed once it was serving.
  2  the command line, the configuration, the key file or the database is wrong.
`

func main() {
	// Whatever else writes a log line, the standard library's log package
	// included, writes a record.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is agent-fox given its arguments and streams. It returns the exit code and
// never ends the process itself.
func run(args []string, stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))

	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard) // the errors and the usage are written below
	configPath := flags.String("config", "", "")
	showVersion := flags.Bool("version", false, "")
	switch err := flags.Parse(args); {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, usage)
		return 0
	case err != nil:
		fmt.Fprintf(stderr, "%s: %v\n\n%s", name, err, usage)
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n\n%s", name, flags.Arg(0), usage)
		return 2
	}

	if *showVersion {
		return printVersion(stdout)
	}
	return startService(*configPath, logger)
}

// printVersion writes the version document, the one that af --version prints
// with its own name: the version, commit and build time that the build set, else
// what its build information has.
func printVersion(stdout io.Writer) int {
	app := &toolio.App{Name: name, Version: version, Commit: commit, BuildTime: buildTime}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(app.VersionDocument()); err != nil {
		return 1
	}
	return 0
}
