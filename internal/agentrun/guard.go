package agentrun

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/guard"

	"github.com/agent-fox-dev/agentfox/internal/project"
)

// GuardOptions configures the authorization boundary a phase with a shell
// runs under.
type GuardOptions struct {
	// Programs is the allowlist of program names the shell may run.
	Programs []string
	// AllowOperators permits pipes, redirection, `&&`, `;` and command
	// substitution. A read-only phase gets none of them, because a
	// redirection is a write.
	AllowOperators bool
	// ReadOnlyFiles refuses write_file and edit_file outright. It is the
	// second check on a phase that also excludes them by policy.
	ReadOnlyFiles bool
	// ProtectedPaths are directories the file tools may not write under,
	// even in a phase that writes everywhere else. A pipeline names the
	// directory it owns — the spec package whose state it maintains — so
	// that "do not modify the spec" is a refusal rather than a request.
	// Entries are absolute paths; a relative one is resolved through
	// ResolvePath like the model's own arguments are.
	ProtectedPaths []string
	// ResolvePath turns a model-supplied path into the absolute path the
	// workspace would write to, symlinks included. Nil means filepath.Abs,
	// which is right for a test and wrong for a workspace under a symlink.
	ResolvePath func(string) (string, error)
	// Suite is the command that runs the project's whole test suite, in a
	// phase the program verifies itself after it ends: the shell refuses it,
	// and the equivalents named at suiteRun. Empty means the phase may run it.
	Suite []string
	// ReadRoots are absolute directories outside the workspace a read-only
	// phase's shell may still read under.
	ReadRoots []string
	// OnBlock is called with the tool's name and the reason for each
	// refusal.
	OnBlock func(name, reason string)
}

// Guard is the authorization boundary for a phase that has a shell.
//
// It is two layers. AgentKit's guard.Restricted supplies the floor: an
// allowlist of program names, and a ban on shell operators when they are not
// wanted. On top of it sit the rules that are specific to these tools and
// that a generic policy cannot know:
//
//   - git is limited to its read-only subcommands, because the branch, the
//     commit and the push belong to the pipeline. "committed as abc123" in a
//     summary then means one thing, and a failed attempt can be discarded by
//     resetting a branch nobody else wrote to.
//   - gh is refused outright, so every write to an issue goes through the
//     audited path in Go rather than through a model's shell.
//   - find may not -exec or -delete, which turn it into a write tool.
//
// Every simple command on a line is judged, not only the first: `ls; git
// push` is two commands and the second is the one that matters, and an
// environment assignment in front of a program (`GIT_AUTHOR_NAME=x git push`)
// does not hide it.
//
// A refusal is a blocked tool result, not a crash: the model reads it and
// adapts, which is why each reason says what to do instead.
func Guard(o GuardOptions) core.BeforeToolCall {
	o.Programs = uniquePrograms(o.Programs)
	base := guard.Restricted(guard.Options{
		AllowedPrograms:     o.Programs,
		AllowShellOperators: o.AllowOperators,
		TerminateOnBlock:    false,
	})
	log := o.OnBlock
	if log == nil {
		log = func(string, string) {}
	}
	block := func(name, reason string) core.BeforeToolCallDecision {
		log(name, reason)
		return core.BeforeToolCallDecision{Block: true, Reason: reason}
	}

	return func(ctx context.Context, in core.BeforeToolCallContext) core.BeforeToolCallDecision {
		switch in.ToolName {
		case "write_file", "edit_file":
			if o.ReadOnlyFiles {
				return block(in.ToolName,
					"this phase is read-only: read the code and report, do not change it")
			}
			if p, _ := in.Arguments["path"].(string); p != "" {
				if dir, hit := o.protectedDir(p); hit {
					return block(in.ToolName, fmt.Sprintf(
						"%s is under %s, which the tool maintains itself: the spec package is "+
							"not yours to edit. Implement what it says; if the implementation has "+
							"to diverge from it, say so in your report.", p, dir))
				}
			}
			return base(ctx, in)

		case "execute", "run_command", "powershell":
			// A leading `cd <workspace>` changes nothing the guard protects,
			// and it is the first thing most calls do, so it is not judged.
			if in.ToolName != "run_command" {
				if cmd, _ := in.Arguments["command"].(string); cmd != "" {
					if stripped, ok := o.stripLeadingCd(cmd); ok {
						in = withCommand(in, stripped)
					}
				}
			}
			vectors := CommandVectors(in.ToolName, in.Arguments)
			cmd, _ := in.Arguments["command"].(string)
			if reason, blocked := o.refusal(vectors, cmd); blocked {
				return block(in.ToolName, reason)
			}
			first := 1
			if d := base(ctx, in); d.Block {
				// The shipped policy misreads an assignment from a command
				// substitution — `d=$(go list ...)` — and names an argument
				// as the program. This guard's own parse has already judged
				// every program on the line, so a complaint about a word that
				// is no command's program is the misreading, not a finding.
				misread := o.misreadProgram(d.Reason, vectors)
				switch {
				case misread && o.AllowOperators && in.ToolName == "execute":
					first = 0 // every segment, the first included, is judged below
				case misread:
					d.Reason = "this phase runs one plain command per call: command substitution ($(...) " +
						"or backticks), pipes, && and ; are refused. Run the inner command on its own."
					log(in.ToolName, d.Reason)
					return d
				default:
					d.Reason = o.restate(d.Reason)
					log(in.ToolName, d.Reason)
					return d
				}
			}
			// The shipped policy inspects only the first program once
			// operators are allowed, so every later segment is offered to it
			// separately.
			if in.ToolName == "execute" && o.AllowOperators {
				cmd, _ := in.Arguments["command"].(string)
				if segs := ShellSegments(cmd); len(segs) > first {
					for _, seg := range segs[first:] {
						if len(commandWords(strings.Fields(seg))) == 0 {
							continue // an assignment alone runs no program
						}
						if d := base(ctx, withCommand(in, seg)); d.Block {
							d.Reason = o.restate(d.Reason)
							log(in.ToolName, d.Reason)
							return d
						}
					}
				}
			}
			return core.BeforeToolCallDecision{}
		}
		return base(ctx, in)
	}
}

// refusal judges every command on the line and returns every reason in one
// message: the git and gh rules and the allowlist are checked together, so a
// line with `git push` and `curl` is told about both, and the model fixes it
// in one retry instead of two.
func (o GuardOptions) refusal(vectors [][]string, cmd string) (string, bool) {
	var reasons []string
	seen := map[string]bool{}
	for _, argv := range vectors {
		if reason, blocked := guardProgram(argv); blocked && !seen[reason] {
			seen[reason] = true
			reasons = append(reasons, reason)
		}
	}
	if reason, blocked := o.disallowedPrograms(vectors, cmd); blocked {
		reasons = append(reasons, reason)
	}
	if reason, blocked := o.operandEscapes(vectors); blocked {
		reasons = append(reasons, reason)
	}
	if reason, blocked := o.suiteRun(vectors); blocked {
		reasons = append(reasons, reason)
	}
	if reason, blocked := o.rmOutside(vectors); blocked {
		reasons = append(reasons, reason)
	}
	if len(reasons) == 0 {
		return "", false
	}
	// A backtick opens a command substitution even inside double quotes; an
	// unclosed one swallows the rest of the line, whose last word is then
	// "the program". Say what happened instead of naming that word.
	switch n := backticks(cmd); {
	case n%2 == 1:
		return backtickReason, true
	case n > 0:
		reasons = append([]string{backtickReason}, reasons...)
	}
	return strings.Join(reasons, " Also: "), true
}

const backtickReason = "a backtick (`) outside single quotes starts a command substitution, even inside " +
	"double quotes; put the pattern in single quotes or escape it as \\`."

// backticks counts the backticks on the line that start or end a command
// substitution: unescaped and outside single quotes.
func backticks(cmd string) int {
	n, inSingle := 0, false
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case c == '\\':
			i++
		case c == '\'':
			inSingle = true
		case c == '`':
			n++
		}
	}
	return n
}

// misreadProgram reports whether a refusal from the shipped policy names, as
// the program not on the allowlist, a word that is no command's program in
// this guard's own parse of the line.
func (o GuardOptions) misreadProgram(reason string, vectors [][]string) bool {
	const marker = `program "`
	i := strings.Index(reason, marker)
	if i < 0 || !strings.Contains(reason, "is not on the allowlist") {
		return false
	}
	name := reason[i+len(marker):]
	if j := strings.IndexByte(name, '"'); j >= 0 {
		name = name[:j]
	}
	for _, argv := range vectors {
		if baseName(argv[0]) == baseName(name) {
			return false
		}
	}
	return true
}

// rmOutside refuses an rm that reaches outside the workspace, the workspace
// itself, the git directory or a protected directory, or that names its
// targets by a glob or an expansion the guard cannot see through. Inside the
// workspace rm is allowed: python3 can delete a file anyway, and a refusal
// that protects nothing only costs a turn.
func (o GuardOptions) rmOutside(vectors [][]string) (string, bool) {
	resolve := o.ResolvePath
	if resolve == nil {
		resolve = filepath.Abs
	}
	root, rootErr := resolve(".")
	var bad []string
	for _, argv := range vectors {
		if baseName(argv[0]) != "rm" {
			continue
		}
		operands := false
		for _, a := range argv[1:] {
			if !operands && a == "--" {
				operands = true
				continue
			}
			if !operands && strings.HasPrefix(a, "-") {
				continue
			}
			if strings.ContainsAny(a, "*?[$~`") || rootErr != nil {
				bad = append(bad, a)
				continue
			}
			abs, err := resolve(a)
			if err != nil {
				bad = append(bad, a)
				continue
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
				rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				bad = append(bad, a)
				continue
			}
			if _, hit := o.protectedDir(a); hit || o.containsProtected(abs, resolve) {
				bad = append(bad, a)
			}
		}
	}
	if len(bad) == 0 {
		return "", false
	}
	return fmt.Sprintf("rm may remove only files and directories inside the repository, named one by one: "+
		"not %s. The repository itself, .git, the spec package, a path outside, a glob and an expansion "+
		"are refused.", strings.Join(bad, ", ")), true
}

// containsProtected reports whether abs is an ancestor of a protected
// directory, which removing it would remove too.
func (o GuardOptions) containsProtected(abs string, resolve func(string) (string, error)) bool {
	for _, dir := range o.ProtectedPaths {
		d, err := resolve(dir)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(abs, d); err == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// uniquePrograms is the list with each program once, in first-seen order: a
// phase's allowlist is assembled from several lists that overlap.
func uniquePrograms(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range list {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// suiteRun refuses the whole test suite in a phase that names it. The
// program runs the suite after the phase and judges the work by that run
// alone, so a run inside the phase repeats it — minutes each, on a context
// that is paid for while it waits. A prompt that said so was not enough.
//
// Refused, in any segment of the line: the suite command itself (the same
// program with all of its arguments, so `make test lint` contains `make
// test`); `make check` when the suite is a make target, the conventional
// wrapper around lint and tests; and `go test` over a whole module (`./...`).
// The linter and targeted runs stay allowed.
func (o GuardOptions) suiteRun(vectors [][]string) (string, bool) {
	if len(o.Suite) == 0 {
		return "", false
	}
	var hit []string
	for _, argv := range vectors {
		if len(argv) == 0 {
			continue
		}
		name := baseName(argv[0])
		match := false
		for _, suite := range o.Suite {
			want := strings.Fields(suite)
			if len(want) == 0 || baseName(want[0]) != name {
				continue
			}
			if containsAll(argv[1:], want[1:]) || (name == "make" && slices.Contains(argv[1:], "check")) {
				match = true
			}
		}
		if name == "go" && len(argv) > 1 && argv[1] == "test" &&
			(slices.Contains(argv[2:], "./...") || slices.Contains(argv[2:], "...")) {
			match = true
		}
		if match {
			hit = append(hit, "`"+strings.Join(argv, " ")+"`")
		}
	}
	if len(hit) == 0 {
		return "", false
	}
	return fmt.Sprintf("%s runs the whole test suite. The program runs `%s` after you submit and "+
		"judges the work by that run alone, so running it here only repeats it. Run the tests you wrote "+
		"and their package instead (`go test ./pkg -run Name`, or the equivalent; a targeted Go run "+
		"needs no -count=1). The linter is allowed.", strings.Join(hit, ", "), strings.Join(o.Suite, "`, `")), true
}

// containsAll reports whether every word of want appears in have.
func containsAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

// readOperandPrograms are the programs whose operands are paths they read. In a
// read-only phase they are held to the workspace like the file tools are, so
// the shell is not the unconfined route to what read_file refuses.
var readOperandPrograms = map[string]bool{
	"cat": true, "head": true, "tail": true, "wc": true, "ls": true, "file": true,
	"du": true, "rg": true, "grep": true, "tree": true, "stat": true,
}

// operandEscapes refuses a read-only phase's command that names a path outside
// the workspace. The file tools resolve every path against the workspace root,
// symlinks included, and refuse an escape; without this check `cat` of an
// absolute path through the shell reached the same file those tools refused.
//
// It is a classifier like the rest of this file: an operand that is absolute,
// starts with `~` or `$`, or climbs with `..` is resolved (or, for an
// expansion it cannot know, refused). The pattern operand of grep and rg is
// not a path and is skipped, so `grep "/api/v1"` is fine. A phase that can
// write files is not held to this, because a build legitimately reads outside
// the repository.
func (o GuardOptions) operandEscapes(vectors [][]string) (string, bool) {
	if !o.ReadOnlyFiles {
		return "", false
	}
	resolve := o.ResolvePath
	if resolve == nil {
		resolve = filepath.Abs
	}
	root, rootErr := resolve(".")
	var bad []string
	for _, argv := range vectors {
		name := baseName(argv[0])
		if !readOperandPrograms[name] {
			continue
		}
		skipPattern := name == "grep" || name == "rg"
		for _, a := range argv[1:] {
			if a == "--" {
				continue
			}
			if strings.HasPrefix(a, "-") {
				if skipPattern && (a == "-e" || a == "-f" || strings.HasPrefix(a, "--regexp") ||
					strings.HasPrefix(a, "--file")) {
					skipPattern = false // the pattern is given by flag; every operand is a path
				}
				continue
			}
			if skipPattern {
				skipPattern = false
				continue
			}
			if !pathLooksOutside(a) {
				continue
			}
			if strings.HasPrefix(a, "$") || strings.HasPrefix(a, "~") {
				bad = append(bad, a)
				continue
			}
			if rootErr == nil && o.underReadRoot(root, a) {
				continue
			}
			abs, err := resolve(a)
			if err != nil || rootErr != nil {
				bad = append(bad, a)
				continue
			}
			if rel, err := filepath.Rel(root, abs); err != nil || rel == ".." ||
				strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				bad = append(bad, a)
			}
		}
	}
	if len(bad) == 0 {
		return "", false
	}
	return fmt.Sprintf("path outside the workspace: %s. The shell is confined to the repository like "+
		"the file tools: use paths inside it (relative paths start at the repository root).",
		strings.Join(bad, ", ")), true
}

// underReadRoot reports whether operand a, relative to the workspace root or
// absolute, lies under one of the read roots once symlinks are resolved. The
// workspace's own resolver refuses every path outside the root, so it is not
// the one asked.
func (o GuardOptions) underReadRoot(root, a string) bool {
	if len(o.ReadRoots) == 0 {
		return false
	}
	p := a
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return project.UnderReadRoot(o.ReadRoots, filepath.Clean(p))
}

// pathLooksOutside reports an operand worth resolving: absolute, home- or
// variable-relative, or climbing with `..`.
func pathLooksOutside(a string) bool {
	if strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") || strings.HasPrefix(a, "$") || a == ".." {
		return true
	}
	return strings.HasPrefix(a, "../") || strings.Contains(a, "/../") || strings.HasSuffix(a, "/..")
}

// allowedList is the allowlist, sorted, for a reason that says what would have
// been accepted.
func (o GuardOptions) allowedList() []string {
	list := append([]string(nil), o.Programs...)
	sort.Strings(list)
	return list
}

// restate puts a refusal from AgentKit's floor policy in the shape this
// guard's own refusals have: no `guard.Restricted:` prefix, and an allowlist
// refusal that names what is allowed.
func (o GuardOptions) restate(reason string) string {
	reason = strings.TrimPrefix(reason, "guard.Restricted: ")
	if strings.Contains(reason, "is not on the allowlist") && len(o.Programs) > 0 {
		reason += ". Allowed: " + strings.Join(o.allowedList(), ", ") + "."
	}
	return reason
}

// disallowedPrograms names every program in the vectors that is not on the
// allowlist, and the allowlist itself, in one reason. The shipped policy stops
// at the first offender and does not say what would have been accepted, so a
// model fixes one command per retry. The write_file hint is added only when
// the command wrote a file by heredoc or redirection and the phase has the
// tool: telling a read-only phase, or someone who ran `curl`, to use it is
// noise.
func (o GuardOptions) disallowedPrograms(vectors [][]string, cmd string) (string, bool) {
	if len(o.Programs) == 0 {
		return "", false
	}
	allowed := make(map[string]bool, len(o.Programs))
	for _, p := range o.Programs {
		allowed[p] = true
	}
	seen := map[string]bool{}
	var bad []string
	for _, argv := range vectors {
		name := baseName(argv[0])
		if allowed[name] || allowed[strings.TrimSuffix(name, ".exe")] || seen[name] {
			continue
		}
		seen[name] = true
		bad = append(bad, name)
	}
	if len(bad) == 0 {
		return "", false
	}
	reason := fmt.Sprintf("programs not allowed: %s. Allowed: %s.",
		strings.Join(bad, ", "), strings.Join(o.allowedList(), ", "))
	if !o.ReadOnlyFiles && (strings.Contains(cmd, "<<") || strings.Contains(cmd, ">")) {
		reason += " To create a file use write_file, not a heredoc or redirection."
	}
	return reason, true
}

// stripLeadingCd removes leading `cd <dir>` commands, joined to what follows by
// `&&`, `;` or a newline, when <dir> is the workspace root or under it. It
// reports whether it removed anything.
//
// cd is on no allowlist: it is a shell builtin that reads and writes nothing.
// It is ignored only where its target is knowable — a plain word, with no
// expansion, that resolves inside the workspace — so `cd /etc && ls` and
// `cd $X && ls` are still refused. A cd that is not the first command stays
// refused, as does one with no command after it.
func (o GuardOptions) stripLeadingCd(cmd string) (string, bool) {
	resolve := o.ResolvePath
	if resolve == nil {
		resolve = filepath.Abs
	}
	root, err := resolve(".")
	if err != nil {
		return cmd, false
	}
	rest := cmd
	stripped := false
	for {
		dir, after, ok := leadingCd(rest)
		if !ok {
			break
		}
		abs, err := resolve(dir)
		if err != nil {
			break
		}
		if rel, err := filepath.Rel(root, abs); err != nil || rel == ".." ||
			strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			break
		}
		rest, stripped = after, true
	}
	if !stripped || strings.TrimSpace(rest) == "" {
		return cmd, false
	}
	return rest, true
}

// leadingCd parses `cd <word>` followed by `&&`, `;` or a newline at the start
// of cmd. It returns the target and what follows the separator.
func leadingCd(cmd string) (dir, rest string, ok bool) {
	s := strings.TrimLeft(cmd, " \t\n")
	if len(s) < 3 || s[:2] != "cd" || (s[2] != ' ' && s[2] != '\t') {
		return "", "", false
	}
	s = strings.TrimLeft(s[2:], " \t")
	if s == "" {
		return "", "", false
	}
	i := 0
	if q := s[0]; q == '\'' || q == '"' {
		end := strings.IndexByte(s[1:], q)
		if end < 0 {
			return "", "", false
		}
		dir, i = s[1:1+end], end+2
	} else {
		for i < len(s) && !strings.ContainsRune(" \t\n;&|()<>", rune(s[i])) {
			i++
		}
		dir = s[:i]
	}
	if dir == "" || strings.HasPrefix(dir, "-") || strings.ContainsAny(dir, "$`~\\*?[{!\"'") {
		return "", "", false
	}
	s = strings.TrimLeft(s[i:], " \t")
	switch {
	case strings.HasPrefix(s, "&&"):
		return dir, s[2:], true
	case strings.HasPrefix(s, ";") || strings.HasPrefix(s, "\n"):
		return dir, s[1:], true
	}
	return "", "", false
}

// protectedDir reports whether p resolves to a file under one of the
// protected directories, and which.
func (o GuardOptions) protectedDir(p string) (string, bool) {
	if len(o.ProtectedPaths) == 0 {
		return "", false
	}
	resolve := o.ResolvePath
	if resolve == nil {
		resolve = filepath.Abs
	}
	abs, err := resolve(p)
	if err != nil {
		// A path the workspace cannot resolve is refused by the tool itself;
		// there is nothing here to protect it from.
		return "", false
	}
	for _, dir := range o.ProtectedPaths {
		d, err := resolve(dir)
		if err != nil {
			continue
		}
		if rel, err := filepath.Rel(d, abs); err == nil && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return dir, true
		}
	}
	return "", false
}

// guardProgram applies this repository's rules to one argument vector.
func guardProgram(argv []string) (reason string, blocked bool) {
	if len(argv) == 0 {
		return "", false
	}
	switch baseName(argv[0]) {
	case "gh":
		return "gh is not available to the agent: every issue comment, label and pull " +
			"request is written by the tool itself so that it is audited", true
	case "git":
		if reason, ok := gitReadOnly(argv[1:]); !ok {
			return reason, true
		}
	case "find":
		for _, a := range argv[1:] {
			for _, f := range findWriteFlags {
				if a == f {
					return "find " + f + " changes or runs things; use find_files to look, " +
						"and the file tools to change", true
				}
			}
		}
	}
	return "", false
}

// readOnlyGit are the git subcommands an agent may run: the ones that report.
//
// Reading history is how you understand a bug; writing it is how a run stops
// being reproducible. It is an allowlist rather than a list of mutating verbs
// because git grows verbs — `pull`, `fetch`, `clone`, `bisect` and `notes`
// were all missing from the first denylist anyone writes.
var readOnlyGit = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "blame": true,
	"rev-parse": true, "rev-list": true, "ls-files": true, "ls-tree": true,
	"grep": true, "cat-file": true, "describe": true, "shortlog": true, "name-rev": true,
}

// readOnlyGitFlags are the arguments under which branch, remote and config
// only read.
var readOnlyGitFlags = map[string]map[string]bool{
	"branch": {"-a": true, "--all": true, "-r": true, "--remotes": true, "-l": true, "--list": true,
		"-v": true, "-vv": true, "--verbose": true, "--show-current": true, "--no-color": true},
	"remote": {"-v": true, "--verbose": true, "show": true, "get-url": true},
	"config": {"--get": true, "--get-all": true, "--get-regexp": true, "--list": true, "-l": true},
}

const readOnlyGitHint = "read-only git is allowed: status, log, diff, show, blame, rev-parse, " +
	"ls-files, grep, cat-file, describe, shortlog, name-rev, branch --list, remote -v, config --get"

// gitReadOnly decides one git invocation (argv without the leading "git").
func gitReadOnly(args []string) (reason string, ok bool) {
	sub, i := gitSubcommand(args)
	for _, a := range args {
		if strings.HasPrefix(a, "--output") {
			return "git --output writes a file; run the command without it", false
		}
	}
	for _, a := range args[:i] {
		// `-c core.fsmonitor=…`, `--config-env` and `--exec-path` make git run
		// a program of the model's choosing before any subcommand does.
		if a == "-c" || strings.HasPrefix(a, "--config-env") || strings.HasPrefix(a, "--exec-path") {
			return "git " + a + " is not allowed; " + readOnlyGitHint, false
		}
	}
	if readOnlyGit[sub] {
		return "", true
	}
	if flags, known := readOnlyGitFlags[sub]; known {
		rest := args[i+1:]
		switch sub {
		case "remote":
			if len(rest) == 0 || flags[rest[0]] {
				return "", true
			}
		case "config":
			if len(rest) > 0 && flags[rest[0]] {
				return "", true
			}
		default: // branch: listing only
			listing := true
			for _, a := range rest {
				listing = listing && flags[a]
			}
			if listing {
				return "", true
			}
		}
	}
	if sub == "" {
		sub = "(no subcommand)"
	}
	return "git " + sub + " is the tool's job, not the agent's; " + readOnlyGitHint, false
}

// gitSubcommand skips git's global flags (`git -C dir commit`) to find the
// verb and its index. A guard that read argv[0] blindly would wave that call
// straight through.
func gitSubcommand(args []string) (string, int) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return a, i
		}
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" {
			i++ // this flag takes a value
		}
	}
	return "", len(args)
}

// findWriteFlags turn find into a write tool.
var findWriteFlags = []string{"-exec", "-execdir", "-ok", "-okdir", "-delete",
	"-fprint", "-fprint0", "-fprintf", "-fls"}

// CommandVectors normalizes the shell tools into argument vectors, one per
// simple command.
//
// Leading NAME=value assignments are dropped the way a shell drops them:
// `GIT_AUTHOR_NAME=x git commit` runs git, and a guard that read argv[0]
// would see an environment variable.
func CommandVectors(tool string, args map[string]any) [][]string {
	switch tool {
	case "execute", "powershell":
		cmd, _ := args["command"].(string)
		var out [][]string
		for _, seg := range ShellSegments(cmd) {
			if argv := commandWords(strings.Fields(seg)); len(argv) > 0 {
				out = append(out, argv)
			}
		}
		return out
	case "run_command":
		raw, _ := args["argv"].([]any)
		words := make([]string, 0, len(raw))
		for _, v := range raw {
			if s, ok := v.(string); ok {
				words = append(words, s)
			}
		}
		if argv := commandWords(words); len(argv) > 0 {
			return [][]string{argv}
		}
	}
	return nil
}

// commandWords drops leading environment assignments and the quoting a
// segment may carry, leaving the words the guard classifies.
func commandWords(words []string) []string {
	var out []string
	for _, w := range words {
		if out == nil {
			if i := strings.IndexByte(w, '='); i > 0 && !strings.ContainsAny(w[:i], "/") {
				continue
			}
		}
		w = strings.TrimRight(strings.Trim(w, `"'`), ")")
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// ShellSegments splits a POSIX-sh command line into its simple commands at
// the unquoted `;`, `|`, `||`, `&&`, `&`, newline, subshell and
// command-substitution boundaries. Redirections (`2>&1`, `&>`) are not
// boundaries.
//
// It is a classifier, not a parser: an odd construct yields a fragment that
// looks like a program name and gets refused, which is the safe direction.
//
// A heredoc is the exception to "every newline is a boundary": the lines
// between `<<DELIM` and the line that is DELIM are data, not commands, and are
// skipped. A quoted delimiter (`<<'EOF'`) makes the body inert. An unquoted
// one still expands `$(...)` and backticks, so those are segments of their
// own. A heredoc with no terminating line is not skipped at all — reading it
// as ordinary commands is the safe direction.
func ShellSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	inSingle, inDouble := false, false
	var pending []heredoc
	// arith is the number of parentheses still open in a `$((...))`, inside
	// which `<<` is a shift and not a heredoc.
	arith := 0
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		var next byte
		if i+1 < len(cmd) {
			next = cmd[i+1]
		}
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
			cur.WriteByte(c)
		case inDouble:
			switch c {
			case '"':
				inDouble = false
				cur.WriteByte(c)
			case '\\':
				cur.WriteByte(c)
				if i+1 < len(cmd) {
					i++
					cur.WriteByte(cmd[i])
				}
			case '`':
				flush()
			case '$':
				if next == '(' {
					flush()
					i++
				} else {
					cur.WriteByte(c)
				}
			default:
				cur.WriteByte(c)
			}
		default:
			switch c {
			case '\'':
				inSingle = true
				cur.WriteByte(c)
			case '"':
				inDouble = true
				cur.WriteByte(c)
			case '\\':
				cur.WriteByte(c)
				if i+1 < len(cmd) {
					i++
					cur.WriteByte(cmd[i])
				}
			case '\n':
				flush()
				if len(pending) > 0 {
					pos := i + 1
					skipped := true
					for _, h := range pending {
						end, body, ok := h.body(cmd, pos)
						if !ok {
							break
						}
						if !h.quoted {
							subs, clear := substitutions(body)
							if !clear {
								// The body has an expansion this scanner
								// cannot delimit with certainty. Leave it
								// unskipped: its lines are read as commands,
								// which is the safe direction.
								skipped = false
								break
							}
							for _, inner := range subs {
								segs = append(segs, ShellSegments(inner)...)
							}
						}
						pos = end
					}
					pending = nil
					if skipped {
						i = pos - 1
					}
				}
			case '(':
				if arith > 0 {
					arith++
				} else if next == '(' && strings.TrimSpace(cur.String()) == "" {
					// `((expr))` at the start of a command is arithmetic, in
					// which `<<` is a shift and not a heredoc.
					arith = 1
				}
				flush()
			case ')':
				if arith > 0 {
					arith--
				}
				flush()
			case ';', '|', '`':
				flush()
			case '#':
				// An unquoted # at the start of a word comments out the rest
				// of the line, `<<` included: bash runs the lines after it as
				// commands, so a heredoc operator in the comment must not make
				// the scanner skip them.
				if i == 0 || strings.IndexByte(" \t\n;|&()", cmd[i-1]) >= 0 {
					for i+1 < len(cmd) && cmd[i+1] != '\n' {
						i++
					}
				} else {
					cur.WriteByte(c)
				}
			case '<':
				if next != '<' {
					cur.WriteByte(c)
					break
				}
				if i+2 < len(cmd) && cmd[i+2] == '<' { // <<< is a here-string
					cur.WriteString("<<<")
					i += 2
					break
				}
				h, end, ok := parseHeredoc(cmd, i)
				if !ok || arith > 0 {
					cur.WriteString("<<")
					i++
					break
				}
				pending = append(pending, h)
				cur.WriteString(cmd[i:end])
				i = end - 1
			case '&':
				var prev byte
				if i > 0 {
					prev = cmd[i-1]
				}
				if prev == '>' || prev == '<' || next == '>' {
					cur.WriteByte(c) // a redirection, not a list operator
				} else {
					flush()
				}
			case '$':
				if next == '(' {
					if arith > 0 || (i+2 < len(cmd) && cmd[i+2] == '(') {
						if arith == 0 {
							arith = 1 // the second parenthesis is counted when reached
						} else {
							arith++
						}
					}
					flush()
					i++
				} else {
					cur.WriteByte(c)
				}
			default:
				cur.WriteByte(c)
			}
		}
	}
	flush()
	return segs
}

// heredoc is one pending `<<DELIM` on the line being scanned.
type heredoc struct {
	delim  string
	strip  bool // <<-: leading tabs are ignored when looking for the terminator
	quoted bool // any part of the delimiter was quoted or escaped: the body is inert
}

// parseHeredoc reads the operator at cmd[i:] (which starts with `<<`, not
// `<<<`) and its delimiter word. end is the index after the word. It declines
// a delimiter that is empty or starts with a digit or `$`, which is more likely
// arithmetic or an expansion than a heredoc.
func parseHeredoc(cmd string, i int) (h heredoc, end int, ok bool) {
	j := i + 2
	if j < len(cmd) && cmd[j] == '-' {
		h.strip = true
		j++
	}
	for j < len(cmd) && (cmd[j] == ' ' || cmd[j] == '\t') {
		j++
	}
	if j >= len(cmd) || strings.IndexByte("0123456789$", cmd[j]) >= 0 {
		return h, 0, false
	}
	var word strings.Builder
scan:
	for j < len(cmd) {
		c := cmd[j]
		switch {
		case c == '\'' || c == '"':
			k := strings.IndexByte(cmd[j+1:], c)
			if k < 0 {
				return h, 0, false
			}
			word.WriteString(cmd[j+1 : j+1+k])
			h.quoted = true
			j += k + 2
		case c == '\\':
			h.quoted = true
			if j+1 < len(cmd) {
				word.WriteByte(cmd[j+1])
			}
			j += 2
		case strings.IndexByte(" \t\r\n;|&()<>", c) >= 0:
			break scan
		default:
			word.WriteByte(c)
			j++
		}
	}
	if j > len(cmd) {
		j = len(cmd)
	}
	h.delim = word.String()
	if h.delim == "" {
		return h, 0, false
	}
	return h, j, true
}

// body finds the terminating line at or after start. end is the index just
// past that line and its newline; body is what lay between. ok is false when
// no line is the delimiter.
func (h heredoc) body(cmd string, start int) (end int, body string, ok bool) {
	for pos := start; pos < len(cmd); {
		eol := strings.IndexByte(cmd[pos:], '\n')
		next := len(cmd)
		line := cmd[pos:]
		if eol >= 0 {
			line, next = cmd[pos:pos+eol], pos+eol+1
		}
		line = strings.TrimSuffix(line, "\r")
		if h.strip {
			line = strings.TrimLeft(line, "\t")
		}
		if line == h.delim {
			return next, cmd[start:pos], true
		}
		pos = next
	}
	return 0, "", false
}

// substitutions returns the text inside each `$(...)` and backtick pair of an
// unquoted heredoc body, where the shell expands them. A backslash escapes the
// character after it, and inside a `$(...)` quotes and escapes hide a `)`.
//
// clear is false when a substitution is one this scan cannot delimit with
// certainty — an unterminated one, or one holding a `case` (whose patterns
// carry unbalanced parentheses), a comment, or a nested heredoc. The caller
// then does not skip the body.
func substitutions(body string) (out []string, clear bool) {
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\':
			i++
		case '`':
			j := i + 1
			for j < len(body) && body[j] != '`' {
				if body[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(body) {
				return nil, false
			}
			out = append(out, body[i+1:j])
			i = j
		case '$':
			if i+1 >= len(body) || body[i+1] != '(' {
				continue
			}
			end := substitutionEnd(body, i+2)
			if end < 0 {
				return nil, false
			}
			inner := body[i+2 : end]
			if ambiguousSubstitution(inner) {
				return nil, false
			}
			out = append(out, inner)
			i = end
		}
	}
	return out, true
}

// substitutionEnd returns the index of the `)` closing a `$(` whose contents
// start at from, or -1. Parentheses inside single quotes, double quotes and
// after a backslash do not count.
func substitutionEnd(body string, from int) int {
	depth := 1
	for j := from; j < len(body); j++ {
		switch body[j] {
		case '\\':
			j++
		case '\'':
			k := strings.IndexByte(body[j+1:], '\'')
			if k < 0 {
				return -1
			}
			j += k + 1
		case '"':
			for j++; j < len(body) && body[j] != '"'; j++ {
				if body[j] == '\\' {
					j++
				}
			}
			if j >= len(body) {
				return -1
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// ambiguousSubstitution reports a `$(...)` body whose extent the quote-aware
// scan cannot be trusted with: a `case` statement, a comment, or a heredoc.
func ambiguousSubstitution(inner string) bool {
	if strings.Contains(inner, "<<") || strings.Contains(inner, "#") {
		return true
	}
	for _, w := range strings.FieldsFunc(inner, func(r rune) bool {
		return strings.ContainsRune(" \t\n;|&()", r)
	}) {
		if w == "case" {
			return true
		}
	}
	return false
}

// withCommand is the interceptor context for one segment of a command.
func withCommand(in core.BeforeToolCallContext, cmd string) core.BeforeToolCallContext {
	args := make(map[string]any, len(in.Arguments))
	for k, v := range in.Arguments {
		args[k] = v
	}
	args["command"] = cmd
	in.Arguments = args
	return in
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}
