package main

import (
	"os"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/afclient"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// modeHook is the Mode of the af binary. af triage and af fix run in one of two modes, chosen from
// the command line before anything else happens: --local runs the loop on this machine, and
// --workspace <slug> queues a job on the agent-fox service. service maps the path of each of the two
// commands to the command of its service mode; the loops stay the commands that afcmd registered, and
// stay as they are for a caller that runs them in its own process, which has no hook. Every other
// command is left alone.
func modeHook(service map[string]*toolio.Command) func(cmd *toolio.Command, words []string) (*toolio.Command, []string, *toolio.Failure) {
	return func(cmd *toolio.Command, words []string) (*toolio.Command, []string, *toolio.Failure) {
		queue, ok := service[cmd.Path]
		if !ok {
			return cmd, words, nil
		}
		scan := scanMode(words)
		switch {
		case scan.local && scan.workspace:
			return nil, nil, &toolio.Failure{
				Category: toolio.CategoryUsage,
				Message:  "--local and --workspace both say where the run goes: give one of them",
				FixHint:  &toolio.FixHint{Flag: "--local"},
			}
		case scan.local:
			return cmd, scan.withoutLocal(words), nil
		case scan.workspace:
			return queue, words, nil
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
	local     bool  // --local was given
	localAt   []int // the indexes of the words that are --local
	workspace bool  // --workspace was given
	server    bool  // --server was given
}

// scanMode reads the words for the flags that choose the mode.
func scanMode(words []string) modeWords {
	var m modeWords
	for i := 0; i < len(words); i++ {
		name, hasValue := flagName(words[i])
		switch name {
		case "local":
			m.local = true
			m.localAt = append(m.localAt, i)
		case "workspace":
			m.workspace = true
			if !hasValue {
				i++ // its value is the next word
			}
		case "server":
			m.server = true
		}
	}
	return m
}

// withoutLocal is words without the words that are --local, which the loop does not know.
func (m modeWords) withoutLocal(words []string) []string {
	out := make([]string, 0, len(words))
	for i, w := range words {
		skip := false
		for _, at := range m.localAt {
			skip = skip || at == i
		}
		if !skip {
			out = append(out, w)
		}
	}
	return out
}

// flagName reads a word as a flag, with one dash or two and an optional =value: its name, and
// whether it carries its value. A word that is no flag has no name.
func flagName(w string) (name string, hasValue bool) {
	if len(w) < 2 || w[0] != '-' {
		return "", false
	}
	name = strings.TrimPrefix(strings.TrimPrefix(w, "-"), "-")
	name, _, hasValue = strings.Cut(name, "=")
	return name, hasValue
}
