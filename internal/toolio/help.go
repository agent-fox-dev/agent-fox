package toolio

import (
	"flag"
	"fmt"
	"slices"
	"strings"
)

// paths returns the registered command paths, sorted.
func (a *App) paths() []string {
	paths := make([]string, len(a.commands))
	for i, c := range a.commands {
		paths[i] = c.Path
	}
	slices.Sort(paths)
	return paths
}

// listHelp is the plain-text help of the program: its usage and its commands.
func (a *App) listHelp() string {
	var b strings.Builder
	name := a.name()
	fmt.Fprintf(&b, "%s runs the agent-fox commands.\n\n", name)
	fmt.Fprintf(&b, "Usage:\n  %s <command> [flags] [argument]\n  %s help [command]\n  %s --version\n  %s --schema\n\n", name, name, name, name)
	b.WriteString("Commands:\n")
	if len(a.commands) == 0 {
		b.WriteString("  (none registered)\n")
	}
	commands := slices.Clone(a.commands)
	slices.SortFunc(commands, func(x, y *Command) int { return strings.Compare(x.Path, y.Path) })
	width := 0
	for _, c := range commands {
		width = max(width, len(c.Path))
	}
	for _, c := range commands {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, c.Path, c.Summary)
	}
	fmt.Fprintf(&b, "\nEvery command prints one JSON envelope on stdout. %q lists a command's flags.\n", name+" help <command>")
	return b.String()
}

// commandHelp is the plain-text help of one command: its synopsis, what it
// does and its flags.
func (a *App) commandHelp(cmd *Command) string {
	var b strings.Builder
	usage := cmd.Usage
	if usage == "" {
		usage = fmt.Sprintf("%s %s [flags]", a.name(), cmd.Path)
	}
	fmt.Fprintf(&b, "Usage: %s\n", usage)
	if cmd.Summary != "" {
		fmt.Fprintf(&b, "\n%s\n", cmd.Summary)
	}

	type row struct{ left, right string }
	var rows []row
	width := 0
	flagSet(cmd, &flagValues{}).VisitAll(func(f *flag.Flag) {
		argName, usage := flag.UnquoteUsage(f)
		left := "--" + f.Name
		if argName != "" {
			left += " " + argName
		}
		if !isZeroDefault(f.DefValue) {
			usage += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		rows = append(rows, row{left, usage})
		width = max(width, len(left))
	})
	b.WriteString("\nFlags:\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, r.left, r.right)
	}
	return b.String()
}

// isZeroDefault reports whether a flag's default is not worth printing.
func isZeroDefault(def string) bool {
	switch def {
	case "", "0", "false", "0s":
		return true
	}
	return false
}
