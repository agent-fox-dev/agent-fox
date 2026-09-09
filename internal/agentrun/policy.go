package agentrun

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/agentfox/agentkit-go/core"
)

// MutatingTools is the read-only mandate as a list of names rather than a
// sentence in a prompt.
//
// The skills these tools replace state the mandate three times — "analysis
// only", "the codebase is read-only to you", a Guardrails bullet listing the
// permitted commands — and a model that ignores all three still edits the
// file. Excluding the tools is what makes the mandate true, because there is
// then nothing to call.
//
// fetch_url is absent for a different reason: it is not in tools.All() to
// begin with, so reaching it takes a second affirmative act no tool in this
// repository makes. None of these agents has outbound network of any kind.
var MutatingTools = []string{"write_file", "edit_file", "execute", "run_command", "powershell"}

// ShellTools are the tools that run a program. A phase that resolves one of
// them must install an interceptor; AgentKit refuses the run otherwise.
var ShellTools = []string{"execute", "run_command", "powershell"}

// ReadOnlyFileTools are the built-in tools that only read.
var ReadOnlyFileTools = []string{"read_file", "list_files", "find_files", "search_files"}

// WriteFileTools are what an implementing phase gets on top of them.
var WriteFileTools = []string{"write_file", "edit_file"}

// ErrNotReadOnly is returned when a mutating tool reaches a read-only phase's
// resolved set.
var ErrNotReadOnly = errors.New("read-only invariant violated")

// AssertReadOnly is the invariant, checked in Go before the first request.
//
// It is deliberately redundant with the ExcludeTools policy that produced the
// resolved set, and with AgentKit's own unguarded-shell guard. That is the
// point: widen the exclusions by mistake and the run does not start, rather
// than starting with a shell.
func AssertReadOnly(resolved []core.Tool) error {
	deny := make(map[string]bool, len(MutatingTools))
	for _, n := range MutatingTools {
		deny[n] = true
	}
	var found []string
	for _, t := range resolved {
		if deny[t.Name] {
			found = append(found, t.Name)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Strings(found)
	return fmt.Errorf("%w: %s reached the resolved tool set", ErrNotReadOnly, strings.Join(found, ", "))
}

// SelectTools picks the named tools out of a built set.
//
// A read-only phase that keeps `execute` has its description rewritten,
// because the shipped one advertises pipes and redirection that the read-only
// policy refuses — and a model told they work wastes turns finding out that
// they do not.
func SelectTools(all []core.Tool, readOnly bool, names ...string) []core.Tool {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := make([]core.Tool, 0, len(names))
	for _, t := range all {
		if !want[t.Name] {
			continue
		}
		if readOnly && t.Name == "execute" {
			t.Description = "Run one plain command. Pipes, redirection, &&, ; and $() are " +
				"refused in this phase, so run one program per call. Output is truncated " +
				"from the END if it is large, so the tail of a long listing is preserved."
		}
		out = append(out, t)
	}
	return out
}

func hasShell(ts []core.Tool) bool {
	for _, t := range ts {
		for _, s := range ShellTools {
			if t.Name == s {
				return true
			}
		}
	}
	return false
}
