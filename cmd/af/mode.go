package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The flags that only the loop reads, which service mode refuses: those of both commands, and
// those of each.
var (
	loopFlags    = []string{"dir", "repo", "dry-run", "land", "brain", "sandbox", "budget", "max-turns", "script"}
	loopFlagsOf  = map[string][]string{"triage": {"label"}, "fix": {"base", "attempts", "no-verify"}}
	serviceFlags = []string{"server", "wait"}
)

// modeHook is the Mode of the af binary. af triage and af fix run in one of two modes, chosen from
// the command line before anything else happens: --local runs the loop on this machine, and
// --workspace <slug> queues a job on the agent-fox service. service maps the path of each of the two
// commands to the command of its service mode; the loops stay the commands that afcmd registered, and
// stay as they are for a caller that runs them in its own process, which has no hook. Every other
// command is left alone.
//
// The words are read the way the shell's flag parse reads them: only those before the first --, with
// one dash or two, and the value of a flag that takes one, in either mode, is that flag's value and
// never a mode. A mode flag the hook finds is taken out of the words that are parsed.
func modeHook(service map[string]*toolio.Command) func(cmd *toolio.Command, words []string) (*toolio.Command, []string, *toolio.Failure) {
	return func(cmd *toolio.Command, words []string) (*toolio.Command, []string, *toolio.Failure) {
		queue, ok := service[cmd.Path]
		if !ok {
			return cmd, words, nil
		}
		scan, failure := scanMode(cmd, queue, words)
		if failure != nil {
			return nil, nil, failure
		}
		switch {
		case scan.local && scan.workspace:
			return nil, nil, &toolio.Failure{
				Category: toolio.CategoryUsage,
				Message:  "--local and --workspace both say where the run goes: give one of them",
				FixHint:  &toolio.FixHint{Flag: "--local"},
			}
		case scan.local:
			for _, name := range scan.seen {
				if slices.Contains(serviceFlags, name) {
					return nil, nil, &toolio.Failure{
						Category: toolio.CategoryUsage,
						Message:  fmt.Sprintf("--%s is a flag of service mode (--workspace <slug>); with --local the run stays on this machine", name),
						FixHint:  &toolio.FixHint{Flag: "--workspace"},
					}
				}
			}
			return cmd, scan.without(words), nil
		case scan.workspace:
			for _, name := range scan.seen {
				if slices.Contains(loopFlags, name) || slices.Contains(loopFlagsOf[cmd.Path], name) {
					return nil, nil, &toolio.Failure{
						Category: toolio.CategoryUsage,
						Message:  fmt.Sprintf("--%s is read only by the loop that runs on this machine, so it has no use with --workspace; drop it, or run the loop with --local", name),
						FixHint:  &toolio.FixHint{Flag: "--local"},
					}
				}
			}
			return queue, scan.without(words), nil
		}
		return nil, nil, noModeFailure(scan.server || os.Getenv(afclient.SourceEnv) != "")
	}
}

// noModeFailure is the usage failure for a command that names neither mode. serverNamed says that a
// service is named, by --server or AF_URL, so that --workspace is the suggestion; otherwise it is
// --local.
func noModeFailure(serverNamed bool) *toolio.Failure {
	hint := &toolio.FixHint{Flag: "--local"}
	if serverNamed {
		hint = &toolio.FixHint{Flag: "--workspace"}
	}
	return &toolio.Failure{
		Category: toolio.CategoryUsage,
		Message:  "choose where the run goes: pass --workspace <slug> to queue a job on the agent-fox service that --server or AF_URL names, or --local to run on this machine",
		FixHint:  hint,
	}
}

// modeWords is what the scan found among the words of an invocation.
type modeWords struct {
	local     bool     // --local was given with a value that counts
	workspace bool     // --workspace was given
	server    bool     // --server was given
	seen      []string // the names of the other flags, in the order they were given
	modeAt    []int    // the words that are --local, which neither mode parses
}

// scanMode reads the words of the invocation of cmd (the loop) and queue (its service mode) for the
// flags that choose the mode, and for the names of all the others. A --workspace with no value or
// an empty one, and a --local with a value that strconv.ParseBool refuses, are usage failures.
func scanMode(cmd, queue *toolio.Command, words []string) (modeWords, *toolio.Failure) {
	var m modeWords
	loopFlag, serviceFlag := cmd.FlagLookup(), queue.FlagLookup()
	for i := 0; i < len(words); i++ {
		if words[i] == "--" {
			break
		}
		name, hasValue, isFlag := toolio.SplitFlag(words[i])
		if !isFlag {
			continue
		}
		_, attached, _ := strings.Cut(words[i], "=")
		switch name {
		case "local":
			m.modeAt = append(m.modeAt, i)
			value := true
			if hasValue {
				var err error
				if value, err = strconv.ParseBool(attached); err != nil {
					return m, &toolio.Failure{
						Category: toolio.CategoryUsage,
						Message:  fmt.Sprintf("--local=%s is not a boolean: use --local, --local=true or --local=false", attached),
						FixHint:  &toolio.FixHint{Flag: "--local"},
					}
				}
			}
			m.local = m.local || value
		case "workspace":
			m.workspace = true
			value, given := attached, hasValue
			if !hasValue && i+1 < len(words) {
				i++
				value, given = words[i], true
			}
			if !given || value == "" {
				return m, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--workspace needs the slug of a workspace", FixHint: &toolio.FixHint{Flag: "--workspace"}}
			}
		default:
			if !hasValue && (isValueFlag(loopFlag, name) || isValueFlag(serviceFlag, name)) {
				i++ // its value is the next word, whatever the word looks like
			}
			if name == "server" {
				m.server = true
			}
			m.seen = append(m.seen, name)
		}
	}
	return m, nil
}

// isValueFlag says whether the flag of the name exists in the lookup and takes the next word as its value.
func isValueFlag(lookup func(string) (bool, bool), name string) bool {
	known, takes := lookup(name)
	return known && takes
}

// without is words without the words that are --local, which neither mode knows as a flag.
func (m modeWords) without(words []string) []string {
	out := make([]string, 0, len(words))
	for i, w := range words {
		if !slices.Contains(m.modeAt, i) {
			out = append(out, w)
		}
	}
	return out
}

// modeHelp is the HelpExtra of the af binary: the help of af triage and af fix ends with the two
// modes and the flags of the service mode, the help of the loop being what comes before.
func modeHelp(app *toolio.App, service map[string]*toolio.Command) func(cmd *toolio.Command) string {
	return func(cmd *toolio.Command) string {
		queue, ok := service[cmd.Path]
		if !ok || cmd == queue { // not a command with two modes, or the service mode itself
			return ""
		}
		var b strings.Builder
		fmt.Fprintf(&b, "\nModes: af %s runs in one of two modes, and needs one of --local and --workspace.\n", cmd.Path)
		b.WriteString("  --local            run the loop on this machine, with the flags above\n")
		b.WriteString("  --workspace slug   queue a job on a workspace of the agent-fox service that --server or AF_URL names, with the flags below\n")
		b.WriteString("\nService mode:\n")
		for _, line := range strings.Split(strings.TrimRight(app.HelpFor(queue), "\n"), "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("  " + line + "\n")
		}
		loopOnly := append(slices.Clone(loopFlags), loopFlagsOf[cmd.Path]...)
		for i, name := range loopOnly {
			loopOnly[i] = "--" + name
		}
		fmt.Fprintf(&b, "\nWith --workspace the flags that only the loop reads are refused: %s.\nWith --local the flags --server and --wait are refused.\n", strings.Join(loopOnly, ", "))
		return b.String()
	}
}
