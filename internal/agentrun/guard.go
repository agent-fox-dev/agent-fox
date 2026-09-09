package agentrun

import (
	"context"
	"strings"

	agentkit "github.com/agentfox/agentkit-go"
	"github.com/agentfox/agentkit-go/core"
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
	// OnBlock is called with the reason for each refusal.
	OnBlock func(string)
}

// Guard is the authorization boundary for a phase that has a shell.
//
// It is two layers. AgentKit's RestrictedPolicy supplies the floor: an
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
	base := agentkit.RestrictedPolicy(agentkit.RestrictedOptions{
		AllowedPrograms:     o.Programs,
		AllowShellOperators: o.AllowOperators,
		TerminateOnBlock:    false,
	})
	log := o.OnBlock
	if log == nil {
		log = func(string) {}
	}
	block := func(name, reason string) core.BeforeToolCallDecision {
		log(name + ": " + reason)
		return core.BeforeToolCallDecision{Block: true, Reason: reason}
	}

	return func(ctx context.Context, in core.BeforeToolCallContext) core.BeforeToolCallDecision {
		switch in.ToolName {
		case "write_file", "edit_file":
			if o.ReadOnlyFiles {
				return block(in.ToolName,
					"this phase is read-only: read the code and report, do not change it")
			}
			return base(ctx, in)

		case "execute", "run_command", "powershell":
			for _, argv := range CommandVectors(in.ToolName, in.Arguments) {
				if reason, blocked := guardProgram(argv); blocked {
					return block(in.ToolName, reason)
				}
			}
			if d := base(ctx, in); d.Block {
				log(in.ToolName + ": " + d.Reason)
				return d
			}
			// The shipped policy inspects only the first program once
			// operators are allowed, so every later segment is offered to it
			// separately.
			if in.ToolName == "execute" && o.AllowOperators {
				cmd, _ := in.Arguments["command"].(string)
				if segs := ShellSegments(cmd); len(segs) > 1 {
					for _, seg := range segs[1:] {
						if d := base(ctx, withCommand(in, seg)); d.Block {
							log(in.ToolName + ": " + d.Reason)
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
			case ';', '\n', '|', '(', ')', '`':
				flush()
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
