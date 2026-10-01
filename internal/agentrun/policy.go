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

// ReadOnlyPrograms is a read-only phase's shell allowlist: programs that
// report and do not change anything.
//
// `find` is not here on purpose — its -exec and -delete make it a write tool,
// and find_files covers the reading half. The guard refuses those flags
// anyway, for a phase that adds find back through --allow.
//
// echo, printf, pwd, true, test and du only print or compare. env, rm and perl
// are left out: env runs any program, perl any code, and rm deletes.
var ReadOnlyPrograms = []string{
	"git", "ls", "cat", "head", "tail", "wc", "rg", "grep", "file",
	"echo", "printf", "pwd", "true", "test", "du",
}

// BuildPrograms is what an implementing phase needs on top of that: the
// toolchains that compile, format and test. The verification command's own
// program is appended by the pipeline at run time, because a phase that
// cannot run the suite it will be judged by is a phase set up to fail.
var BuildPrograms = []string{
	"go", "gofmt", "goimports", "make", "npm", "npx", "node", "yarn", "pnpm",
	"python", "python3", "pytest", "uv", "pip", "cargo", "rustfmt",
	"mkdir", "cp", "mv", "sed", "awk", "diff", "sort", "uniq", "touch",
}

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

// SelectTools picks the named tools out of a built set and tells each one the
// rules of the phase it is in.
//
// A model that is not told the rules learns them from refusals: in one run
// the first `execute` of nearly every task was `cd <repo> && ...`, refused,
// and the same went for `find`, `env`, heredocs and a scratch file under
// /tmp. The shipped descriptions say none of this, and a read-only phase's
// even advertises pipes and redirection its policy refuses. programs is the
// phase's allowlist, so the text is generated from the same list the guard
// enforces and cannot drift from it.
func SelectTools(all []core.Tool, readOnly bool, programs []string, names ...string) []core.Tool {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := make([]core.Tool, 0, len(names))
	for _, t := range all {
		if !want[t.Name] {
			continue
		}
		t.Description = describeForPhase(t, readOnly, programs)
		out = append(out, t)
	}
	return out
}

// describeForPhase is a tool's description with the phase's rules in it.
func describeForPhase(t core.Tool, readOnly bool, programs []string) string {
	list := strings.Join(programs, ", ")
	switch t.Name {
	case "execute":
		if readOnly {
			return "Run one plain command from the repository root. Pipes, redirection, &&, ; and $() are " +
				"refused in this phase, so run one program per call. The only programs allowed are: " + list +
				". Use find_files instead of find. Output is truncated from the END if it is large, so the " +
				"tail of a long listing is preserved."
		}
		return t.Description + " The shell already starts at the repository root: do not `cd` to it. " +
			"The only programs allowed are: " + list + "; anything else (find, rm, env, perl, curl) is " +
			"refused. Heredocs are not for writing files: use write_file for a multi-line file. Do not " +
			"leave scratch files or .bak copies in the repository."
	case "run_command":
		return t.Description + " It starts at the repository root. The only programs allowed are: " + list + "."
	case "write_file":
		return t.Description + " The path must be inside the repository: a path outside it (/tmp " +
			"included) is refused, and there is no scratch directory. Use this, not a heredoc, for " +
			"multi-line files."
	case "edit_file":
		return t.Description + ` The edits argument is a JSON array of {"old_string", "new_string"} ` +
			"objects, never a string. The path must be inside the repository."
	}
	return t.Description
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
