package agentrun

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/agentfox/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/internal/project"
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

// ReadOnlyFileTools are the built-in tools that only read. file_outline and
// find_symbol navigate by declaration; neither writes, so neither is in
// MutatingTools.
var ReadOnlyFileTools = []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol"}

// ToolCodeSearch names the indexed search tool. It is granted only when the
// run has an index, so it is not part of ReadOnlyFileTools.
const ToolCodeSearch = "code_search"

// WithCodeSearch returns the grant with code_search appended when the run has an
// index (16-REQ-2.1), and the grant itself when it has none (16-REQ-2.2). The
// result is a copy, so the shared read-only list is never grown.
func WithCodeSearch(grant []string, on bool) []string {
	if !on {
		return grant
	}
	return append(append([]string(nil), grant...), ToolCodeSearch)
}

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
	"mkdir", "cp", "mv", "rm", "sed", "awk", "diff", "sort", "uniq", "touch",
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
	shell := false
	for _, n := range names {
		shell = shell || slices.Contains(ShellTools, n)
	}
	out := make([]core.Tool, 0, len(names))
	for _, t := range all {
		if !want[t.Name] {
			continue
		}
		t.Description = describeForPhase(t, readOnly, programs)
		if !shell {
			// A guideline like "prefer search_files over execute+grep" points
			// at a tool this phase does not have.
			kept := make([]string, 0, len(t.PromptGuidelines))
			for _, g := range t.PromptGuidelines {
				if !strings.Contains(g, "execute") {
					kept = append(kept, g)
				}
			}
			t.PromptGuidelines = kept
		}
		out = append(out, t)
	}
	return out
}

// describeForPhase is a tool's description with the phase's rules in it.
func describeForPhase(t core.Tool, readOnly bool, programs []string) string {
	list := strings.Join(uniquePrograms(programs), ", ")
	switch t.Name {
	case "execute":
		if readOnly {
			return "Run one plain command from the repository root. Pipes, redirection, &&, ; and $() are " +
				"refused in this phase, so run one program per call. Paths must be inside the repository, as " +
				"for the file tools. The only programs allowed are: " + list +
				". Use find_files instead of find. Output is truncated from the END if it is large, so the " +
				"tail of a long listing is preserved."
		}
		return t.Description + " The shell already starts at the repository root: do not `cd` to it. " +
			"The only programs allowed are: " + list + "; anything else (find, env, perl, curl) is " +
			"refused, and rm only removes paths inside the repository, named one by one. Heredocs are " +
			"not for writing files: use write_file for a multi-line file. Do not leave scratch files or " +
			".bak copies in the repository itself."
	case "run_command":
		return t.Description + " It starts at the repository root. The only programs allowed are: " + list + "."
	case "write_file":
		return t.Description + " The path must be inside the repository: a path outside it (/tmp " +
			"included) is refused. Use this, not a heredoc, for multi-line files."
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

// noteReadRoots tells the shell and read_file where the read roots are and how
// to reach them: the file tools are confined to the repository, the shell's
// read programs are not, and `go doc` reads a dependency's API when the phase
// may run go.
func noteReadRoots(ts []core.Tool, roots []project.ReadRoot, programs []string) []core.Tool {
	if len(roots) == 0 {
		return ts
	}
	var named []string
	for _, r := range roots {
		named = append(named, fmt.Sprintf("%s (replace of %s)", r.Path, r.Module))
	}
	how := "the shell (ls, cat, grep -rn)"
	if slices.Contains(programs, "go") {
		how += " or `go doc <package> <Symbol>`"
	}
	note := fmt.Sprintf(" You may also read, but not change: %s. The file tools cannot reach it; read it with %s.",
		strings.Join(named, ", "), how)
	out := make([]core.Tool, len(ts))
	for i, t := range ts {
		if t.Name == "execute" || t.Name == "read_file" {
			t.Description += note
		}
		out[i] = t
	}
	return out
}

// noteScratch tells the writing tools where the phase's scratch directory is
// and what it is for. rel is empty when the phase has none.
func noteScratch(ts []core.Tool, rel string) []core.Tool {
	if rel == "" {
		return ts
	}
	note := fmt.Sprintf(" Scratch space: `%s/`, for a throwaway script, a copy made before an edit or a "+
		"test harness. It is not committed and not checked, and it is deleted when this phase ends, so "+
		"nothing the work needs may live there.", rel)
	out := make([]core.Tool, len(ts))
	for i, t := range ts {
		switch t.Name {
		case "execute", "write_file", "edit_file":
			t.Description += note
		}
		out[i] = t
	}
	return out
}
