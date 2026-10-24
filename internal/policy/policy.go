// Package policy decides whether a brain may make a tool call: it may not run
// the suite the gate caches, mutate git, call the commands of the loop or write
// where only af writes.
//
// It is prevention and not a boundary. It reads a command line as a shell
// would, but it does not run it: a program chosen by a variable, a script a
// command writes and then runs, or a program that runs others is out of its
// sight. The gate and the box are the invariants.
package policy

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Policy is what a brain may not do.
type Policy struct {
	// Suite lists the command forms that count as the full test suite.
	Suite [][]string `json:"suite"`
	// ProtectedPaths are the paths, relative to the checkout, that only af
	// writes. One that ends in a slash is a directory.
	ProtectedPaths []string `json:"protected_paths"`
	// AllowPrograms, when it is not empty, are the only programs a shell
	// command may run, besides af and git.
	AllowPrograms []string `json:"allow_programs"`
}

// The decisions.
const (
	Allow = "allow"
	Deny  = "deny"
)

// The rules that decide a denial.
const (
	RuleUnparseable       = "unparseable"
	RuleHandsGuard        = "hands_guard"
	RuleGitMutation       = "git_mutation"
	RuleSuiteCommand      = "suite_command"
	RuleAfCommand         = "af_command"
	RuleProgramNotAllowed = "program_not_allowed"
	RuleProtectedPath     = "protected_path"
	RuleOutsideDir        = "outside_dir"
)

// Decision is the verdict on one tool call.
type Decision struct {
	Decision string `json:"decision"`
	Rule     string `json:"rule"`
	Segment  string `json:"segment"`
	Reason   string `json:"reason"`
}

// DefaultProtectedPaths are the paths only af writes.
func DefaultProtectedPaths() []string {
	return []string{".git/", ".agent-fox/", ".specs/", ".github/workflows/", ".gitlab-ci.yml"}
}

// IsProtected reports whether the path rel of the checkout, slash-separated, is one of
// the protected paths or below one, by the rules a write is judged by.
func IsProtected(protected []string, rel string) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	for _, prot := range protected {
		if covers(rel, prot, false) {
			return true
		}
	}
	return false
}

// shellTools are the tools whose input is a line for a shell: the hands' execute.
var shellTools = []string{"execute"}

// maxCommandBytes is the longest command line that is judged. The hands' guard
// takes time that grows with the square of the length of the line (a line of
// 150 KB takes it seconds), and every call of a brain waits for it. A command
// this long is a file written through a shell: use the tool that writes files.
const maxCommandBytes = 64 << 10

// deny is a denial.
func deny(rule, segment, format string, args ...any) Decision {
	return Decision{Decision: Deny, Rule: rule, Segment: segment, Reason: fmt.Sprintf(format, args...)}
}

// writeTools are the tools that write a file, and the field of their input that
// names it.
var writeTools = map[string]string{"write_file": "path", "edit_file": "path"}

// Decide decides the tool call tool with the input input, made in the
// checkout dir. A shell command is read as the shell would read it, every
// command it runs is judged by the rules in order, and the first denial wins.
// A call to a tool that writes a file is denied when the file is in a protected
// path, or outside the checkout and outside the system temporary directory.
// Every other tool is allowed.
func Decide(p Policy, dir, tool string, input map[string]any) Decision {
	return DecideAt(p, dir, dir, tool, input)
}

// DecideAt is Decide for a call made in the directory cwd, which may be below
// the checkout: the shell of a session keeps the directory a cd went to, and a
// relative path means what it means there. One that is not below the checkout
// is not a place to judge from.
func DecideAt(p Policy, dir, cwd, tool string, input map[string]any) Decision {
	if dir == "" {
		dir = "."
	}
	bases := baseDirs(dir, cwd)
	switch {
	case slices.Contains(shellTools, tool):
		return decideShell(p, dir, bases, input)
	case writeTools[tool] != "":
		return decideWrite(p, dir, bases, tool, input)
	}
	return Decision{Decision: Allow}
}

// baseDirs are the directories a relative path may mean: the checkout, and the
// directory the call is made in when that is below it.
func baseDirs(dir, cwd string) []string {
	if cwd == "" {
		return []string{dir}
	}
	cwd = resolve(dir, cwd)
	if cwd != filepath.Clean(dir) && within(dir, cwd) {
		return []string{dir, cwd}
	}
	return []string{dir}
}

// decideWrite decides a call of a tool that writes the file its input names.
func decideWrite(p Policy, dir string, bases []string, tool string, input map[string]any) Decision {
	field := writeTools[tool]
	file, ok := input[field].(string)
	if !ok || strings.TrimSpace(file) == "" {
		return deny(RuleUnparseable, "(no path)", "the %s call names no path in %s, so it cannot be judged and nothing may be written", tool, field)
	}
	if protected(p.ProtectedPaths, dir, bases, file, false, false) {
		return deny(RuleProtectedPath, file, "%s is a protected path: only af writes there (%s).", file, strings.Join(p.ProtectedPaths, ", "))
	}
	where := resolve(bases[len(bases)-1], file)
	if !within(dir, where) && !within(os.TempDir(), where) {
		return deny(RuleOutsideDir, file, "%s is outside the checkout %s: a run may write there and in the system temporary directory, %s, and nowhere else.", file, dir, os.TempDir())
	}
	return Decision{Decision: Allow}
}

// within reports whether p is root or below it, links resolved if they must be.
func within(root, p string) bool {
	if lexicallyWithin(root, p) {
		return true
	}
	return lexicallyWithin(realPath(root), realPath(p))
}

func lexicallyWithin(root, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath is p with the links resolved of as much of it as exists.
func realPath(p string) string {
	for q := p; ; q = filepath.Dir(q) {
		if resolved, err := filepath.EvalSymlinks(q); err == nil {
			rel, err := filepath.Rel(q, p)
			if err != nil {
				return p
			}
			return filepath.Join(resolved, rel)
		}
		if q == filepath.Dir(q) {
			return p
		}
	}
}

// decideShell decides a shell call.
func decideShell(p Policy, dir string, bases []string, input map[string]any) Decision {
	command, ok := input["command"].(string)
	if !ok {
		return deny(RuleUnparseable, "(no command)", "the call has no command to read, so nothing in it may run")
	}
	if len(command) > maxCommandBytes {
		return deny(RuleUnparseable, shorten(command), "the command is %d bytes long, more than the %d that can be read with certainty, so nothing in it may run; write long content with a tool that writes files", len(command), maxCommandBytes)
	}
	segments, err := flatten(command, 0)
	if err != nil {
		return deny(RuleUnparseable, shorten(command), "the command cannot be read with certainty, so nothing in it may run: %v", err)
	}
	if reason, refused := handsGuard(command); refused {
		return deny(RuleHandsGuard, shorten(command), "%s", reason)
	}

	bases = slices.Clone(bases)
	var exported []string // what the commands before this one put in the environment of the ones after
	for _, s := range segments {
		s.assigns = append(s.assigns, exported...)
		if d, denied := judge(p, dir, bases, s); denied {
			return d
		}
		exported = append(exported, s.exports()...)
		if cd, ok := s.changesDirectory(); ok {
			bases = append(bases, resolve(bases[len(bases)-1], cd))
		}
	}
	return Decision{Decision: Allow}
}

// shorten is a command shortened to something a denial can show.
func shorten(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return "(empty command)"
	}
	const limit = 200
	if len(command) <= limit {
		return command
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(command[cut]) {
		cut--
	}
	return command[:cut] + "…"
}

// judge applies the rules that are about one command, in order.
func judge(p Policy, dir string, bases []string, s segment) (Decision, bool) {
	for _, rule := range []func() (Decision, bool){
		func() (Decision, bool) { return inputRule(s) },
		func() (Decision, bool) { return gitRule(s) },
		func() (Decision, bool) { return suiteRule(p, s) },
		func() (Decision, bool) { return afRule(s) },
		func() (Decision, bool) { return programRule(p, s) },
		func() (Decision, bool) { return pathRule(p, dir, bases, s) },
	} {
		if d, denied := rule(); denied {
			return d, true
		}
	}
	return Decision{}, false
}

// segment is a command, with what runs found in it.
type segment struct {
	text      string
	prog      string   // the program, as written, after assignments and wrappers
	args      []string // its arguments
	redirects []redirect
	assigns   []string // the NAME=value words in front of the program, and the ones env was given
	script    string   // what the command makes a shell or a builtin read as commands
	hasScript bool
	fromInput bool // it makes a shell read its commands from its input
}

// flatten lists every command a line runs, outer commands before the ones they
// contain, and the scripts of shells, eval and trap read in turn.
func flatten(src string, depth int) ([]segment, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("scripts nested more than %d deep", maxDepth)
	}
	cmds, err := parseAt(src, depth)
	if err != nil {
		return nil, err
	}
	var out []segment
	for _, c := range cmds {
		more, err := runs(newSegment(c), depth)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
	}
	return out, nil
}

// runs is a command and what it runs besides: the commands find executes, and
// the script it hands to a shell.
func runs(s segment, depth int) ([]segment, error) {
	out := []segment{s}
	for _, e := range s.embedded() {
		more, err := runs(e, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, more...)
	}
	if s.hasScript {
		nested, err := flatten(s.script, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

// newSegment finds the program of a command: the first word that is not an
// assignment, a wrapper (and its options) or a keyword that starts a command.
func newSegment(c command) segment {
	argv, assigns := skipPrefix(c.words)
	s := segment{text: c.text, redirects: c.redirects, assigns: assigns}
	if len(argv) > 0 {
		s.prog, s.args = argv[0], argv[1:]
	}
	s.script, s.hasScript, s.fromInput = s.reads()
	return s
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\+?=`)

// keywords are the words that start a command in a compound command, and
// are not a program.
var keywords = []string{"if", "then", "else", "elif", "while", "until", "do", "{", "!", "}", "fi", "done", "esac"}

// skipPrefix drops what comes before the program of a command, and returns the
// assignments it dropped.
func skipPrefix(words []string) (argv, assigns []string) {
	for len(words) > 0 {
		switch {
		case assignment.MatchString(words[0]):
			assigns = append(assigns, words[0])
			words = words[1:]
		case words[0] == "function":
			words = words[min(2, len(words)):] // function NAME
		case words[0] == "coproc":
			words = words[1:]
			if len(words) > 1 && words[1] == "{" {
				words = words[1:] // coproc NAME { …; }
			}
		case slices.Contains(keywords, words[0]):
			words = words[1:]
		case isWrapper(words[0]):
			var more []string
			words, more = skipWrapper(words)
			assigns = append(assigns, more...)
		default:
			return words, assigns
		}
	}
	return words, assigns
}

var wrappers = []string{"env", "time", "timeout", "nice", "nohup", "command", "exec", "builtin", "xargs"}

func isWrapper(word string) bool {
	return slices.Contains(wrappers, baseName(word))
}

// baseName is the name of a program less its directory, as the file system of
// a Mac or a Windows machine would find it: in lower case, without .exe.
func baseName(word string) string {
	return strings.TrimSuffix(strings.ToLower(path.Base(word)), ".exe")
}

// skipWrapper drops a wrapper and its options, and what the wrapper takes
// before the command: the duration of timeout, the strings of env -S. It
// returns the assignments env was given.
func skipWrapper(words []string) (argv, assigns []string) {
	// valued are the options that take the next word as their value.
	var valued []string
	positional := 0 // words the wrapper takes before the command
	switch baseName(words[0]) {
	case "env":
		return skipEnv(words)
	case "time":
		valued = []string{"-f", "-o", "--format", "--output"}
	case "timeout":
		valued, positional = []string{"-s", "-k", "--signal", "--kill-after"}, 1
	case "nice":
		valued = []string{"-n", "--adjustment"}
	case "exec":
		valued = []string{"-a"}
	case "xargs":
		valued = []string{"-a", "-d", "-E", "-I", "-L", "-n", "-P", "-s", "--arg-file", "--delimiter", "--eof", "--max-lines", "--max-args", "--max-procs", "--max-chars"}
	case "command":
		for _, a := range words[1:] {
			if a == "-v" || a == "-V" {
				return nil, nil // it describes a command and runs none
			}
		}
	}

	i := 1
	for i < len(words) {
		a := words[i]
		switch {
		case a == "--":
			return rest(words, i+1, positional), nil
		case slices.Contains(valued, a):
			i += 2
		case strings.HasPrefix(a, "-") && a != "-":
			i++
		default:
			return rest(words, i, positional), nil
		}
	}
	return nil, nil
}

// skipEnv drops env and its options. What -S and --split-string take is a
// command line that env splits into words and runs, and so it goes in front of
// the words that follow the options.
func skipEnv(words []string) (argv, assigns []string) {
	var lead []string
	i := 1
options:
	for i < len(words) {
		a := words[i]
		switch {
		case a == "--":
			i++
			break options
		case strings.HasPrefix(a, "--split-string="):
			lead = append(lead, splitString(strings.TrimPrefix(a, "--split-string="))...)
			i++
		case a == "--split-string":
			if i+1 < len(words) {
				lead = append(lead, splitString(words[i+1])...)
			}
			i += 2
		case a == "--unset" || a == "--chdir":
			i += 2
		case strings.HasPrefix(a, "--"):
			i++
		case strings.HasPrefix(a, "-") && a != "-":
			// Options in a bundle: -i, -0, -v, and the ones that take a value,
			// which is the rest of the bundle or the next word.
			i++
			for j := 1; j < len(a); j++ {
				if c := a[j]; c == 'u' || c == 'C' || c == 'S' {
					value := a[j+1:]
					if value == "" && i < len(words) {
						value = words[i]
						i++
					}
					if c == 'S' {
						lead = append(lead, splitString(value)...)
					}
					break
				}
			}
		default:
			break options
		}
	}
	return append(lead, words[min(i, len(words)):]...), nil
}

// splitString splits the string of env -S into words, at blanks, with quotes
// and backslashes as the shell has them.
func splitString(s string) []string {
	var words []string
	var word []byte
	inWord := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'' || c == '"':
			end := strings.IndexByte(s[i+1:], c)
			if end < 0 {
				end = len(s) - i - 1
			}
			word = append(word, s[i+1:i+1+end]...)
			i += end + 1
			inWord = true
		case c == '\\' && i+1 < len(s):
			word = append(word, s[i+1])
			i++
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			if inWord {
				words = append(words, string(word))
				word, inWord = nil, false
			}
		default:
			word = append(word, c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, string(word))
	}
	return words
}

// rest is the words from i on, less the positional words a wrapper takes.
func rest(words []string, i, positional int) []string {
	if i+positional > len(words) {
		return nil
	}
	return words[i+positional:]
}

// shells are the programs whose -c option takes a script.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh", "ash"}

// reads is what a command makes the shell read as commands: the script of eval,
// trap and of a shell given -c, or the input of a shell that is given neither
// a script nor a file, which cannot be read before it runs.
func (s segment) reads() (script string, ok, fromInput bool) {
	switch prog := s.program(); {
	case prog == "eval":
		args := s.args
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		return strings.Join(args, " "), len(args) > 0, false
	case prog == "trap":
		return trapScript(s.args)
	case prog == "source" || prog == ".":
		return "", false, len(s.args) > 0 && readsInput(s.args[0])
	case slices.Contains(shells, prog):
		return shellScript(s.args)
	}
	return "", false, false
}

// trapScript is the action of trap ACTION SIGNAL…, when there is one: a number
// or a dash resets the signals.
func trapScript(args []string) (string, bool, bool) {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "-" {
		opt := args[0]
		args = args[1:]
		if opt == "-l" || opt == "-p" {
			return "", false, false
		}
		if opt == "--" {
			break
		}
	}
	if len(args) < 2 || args[0] == "-" || args[0] == "" || isDigits(args[0]) {
		return "", false, false
	}
	return args[0], true, false
}

// shellScript is what the arguments of a shell make it read: the first operand
// after -c, a file, or its input.
func shellScript(args []string) (script string, ok, fromInput bool) {
	cmdString, stdin := false, false
	i := 0
options:
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--" || a == "-":
			i++
			break options
		case a == "--version" || a == "--help":
			return "", false, false
		case a == "--rcfile" || a == "--init-file":
			i++
		case strings.HasPrefix(a, "--"):
		case len(a) > 1 && (a[0] == '-' || a[0] == '+'):
			for _, c := range a[1:] {
				switch c {
				case 'c':
					cmdString = cmdString || a[0] == '-'
				case 's':
					stdin = stdin || a[0] == '-'
				case 'o', 'O':
					i++ // the option it sets is the next word
				}
			}
		default:
			break options
		}
	}
	operand := i < len(args)
	switch {
	case cmdString:
		if operand {
			return args[i], true, false
		}
		return "", false, false
	case stdin || !operand:
		return "", false, true
	}
	return "", false, readsInput(args[i])
}

// readsInput reports whether a file name is the input of the shell: a process
// substitution, standard input or a descriptor.
func readsInput(name string) bool {
	return name == "-" || strings.HasPrefix(name, "<(") || name == "/dev/stdin" || strings.HasPrefix(name, "/dev/fd/") || strings.HasPrefix(name, "/proc/self/fd/")
}

// embedded lists the commands find executes with -exec, -execdir, -ok and
// -okdir: the words up to the ; or + that ends them.
func (s segment) embedded() []segment {
	if s.program() != "find" {
		return nil
	}
	var out []segment
	for i := 0; i < len(s.args); i++ {
		if !slices.Contains([]string{"-exec", "-execdir", "-ok", "-okdir"}, s.args[i]) {
			continue
		}
		end := i + 1
		for end < len(s.args) && s.args[end] != ";" && s.args[end] != "+" {
			end++
		}
		if end > i+1 {
			e := newSegment(command{text: s.text, words: s.args[i+1 : end]})
			out = append(out, e)
		}
		i = end
	}
	return out
}

// exports lists the NAME=value words a command puts in the environment of the
// commands after it.
func (s segment) exports() []string {
	switch s.program() {
	case "export", "declare", "typeset":
		var out []string
		for _, a := range s.args {
			if assignment.MatchString(a) {
				out = append(out, a)
			}
		}
		return out
	}
	return nil
}

// changesDirectory reports where a cd goes, when it says.
func (s segment) changesDirectory() (string, bool) {
	if s.prog != "cd" {
		return "", false
	}
	var operands []string
	for _, a := range s.args {
		if !strings.HasPrefix(a, "-") || a == "-" {
			operands = append(operands, a)
		}
	}
	if len(operands) != 1 || operands[0] == "-" || strings.HasPrefix(operands[0], "~") || strings.Contains(operands[0], "$") {
		return "", false
	}
	return operands[0], true
}

// program is the name of the program for the rules about af, git and the
// suite: as it is spelled, less the directory it is run from, and in lower
// case, because the file system of a Mac finds GIT where it finds git.
func (s segment) program() string {
	return baseName(s.prog)
}

// inputRule (rule 1): a shell that reads its commands from its input runs what
// the policy cannot see.
func inputRule(s segment) (Decision, bool) {
	if !s.fromInput {
		return Decision{}, false
	}
	return deny(RuleUnparseable, s.text, "%s reads its commands from its input, so they cannot be read with certainty and nothing in the line may run: give it the script with -c, or run a script file.", s.program()), true
}

// gitReadOnly are the subcommands of git that only read.
var gitReadOnly = []string{
	"status", "log", "diff", "show", "blame", "ls-files", "ls-tree", "rev-parse", "rev-list", "grep",
	"describe", "cat-file", "shortlog", "merge-base", "name-rev", "for-each-ref", "show-ref",
}

// gitRule (rule 3): git may read, and af makes every commit, branch and push.
func gitRule(s segment) (Decision, bool) {
	if s.prog == "" || s.program() != "git" {
		return Decision{}, false
	}
	sub, args, global := gitSubcommand(s.args)
	if name := gitEnvironment(s.assigns); name != "" {
		global = name
	}
	switch {
	case global != "":
		return deny(RuleGitMutation, s.text, "git %s is refused: af makes every commit, branch and push, and sets the configuration it needs itself. Read the repository with git status, log, diff or show.", global), true
	case sub == "grep" && slices.ContainsFunc(args, opensPager):
		return deny(RuleGitMutation, s.text, "git grep with a pager is refused: it runs the program the option names. Read the matches from its output."), true
	case sub == "":
		return Decision{}, false // git --version
	case slices.Contains(gitReadOnly, sub):
		return Decision{}, false
	case sub == "branch" && branchLists(args), sub == "remote" && remoteLists(args):
		return Decision{}, false
	}
	return deny(RuleGitMutation, s.text, "git %s is refused: af makes every commit, branch and push. Read the repository with git status, log, diff or show, and leave changing it to af.", sub), true
}

// gitSubcommand finds the subcommand of a git command line, skipping the global
// options. It returns the global option that is refused instead, if there is
// one: -c sets configuration for the command.
func gitSubcommand(args []string) (sub string, rest []string, refused string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-c" || (strings.HasPrefix(a, "-c") && !strings.HasPrefix(a, "--") && len(a) > 2):
			return "", nil, "-c"
		case strings.HasPrefix(a, "--config-env"):
			return "", nil, "--config-env"
		case strings.HasPrefix(a, "--exec-path="):
			return "", nil, "--exec-path"
		case slices.Contains([]string{"-C", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--attr-source"}, a):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:], ""
		}
	}
	return "", nil, ""
}

// gitEnvironment names the first variable among the assignments that sets
// configuration for git or a program git runs, as -c does, or "".
func gitEnvironment(assigns []string) string {
	for _, a := range assigns {
		name, _, _ := strings.Cut(a, "=")
		name = strings.TrimSuffix(name, "+")
		if strings.HasPrefix(name, "GIT_CONFIG") || slices.Contains(gitPrograms, name) {
			return name + "="
		}
	}
	return ""
}

// gitPrograms are the variables that make git run a program.
var gitPrograms = []string{
	"GIT_EXTERNAL_DIFF", "GIT_EXEC_PATH", "GIT_ASKPASS", "GIT_SSH", "GIT_SSH_COMMAND", "GIT_PROXY_COMMAND",
	"GIT_EDITOR", "GIT_SEQUENCE_EDITOR", "GIT_TEMPLATE_DIR",
}

// opensPager reports whether an argument of git grep makes it run a program
// over the files that match: -O, alone or in a bundle, and --open-files-in-pager.
func opensPager(a string) bool {
	return strings.HasPrefix(a, "--open-files-in-pager") || len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.Contains(a, "O")
}

// branchLists reports whether the arguments of git branch only list.
func branchLists(args []string) bool {
	listing, operands := false, 0
	plain := []string{
		"-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "--show-current", "--color", "--no-color", "--column", "--no-column",
		"-i", "--ignore-case", "--no-abbrev", "--omit-empty",
	}
	valued := []string{"--contains", "--no-contains", "--points-at", "--format", "--sort"}
	attached := []string{"--abbrev=", "--color=", "--format=", "--sort=", "--column=", "--contains=", "--no-contains=", "--merged=", "--no-merged=", "--points-at="}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--list" || a == "-l":
			listing = true
		case slices.Contains(plain, a):
		case slices.Contains(valued, a):
			i++
		case a == "--merged" || a == "--no-merged":
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case slices.ContainsFunc(attached, func(p string) bool { return strings.HasPrefix(a, p) }):
		case len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.Trim(a[1:], "avrli") == "":
			listing = listing || strings.Contains(a, "l")
		case strings.HasPrefix(a, "-"):
			return false
		default:
			operands++
		}
	}
	return operands == 0 || listing
}

// remoteLists reports whether the arguments of git remote only list.
func remoteLists(args []string) bool {
	i := 0
	for i < len(args) && (args[i] == "-v" || args[i] == "--verbose") {
		i++
	}
	if i == len(args) {
		return true
	}
	return args[i] == "show" || args[i] == "get-url"
}

// suiteValued are, for the programs that run suites, the options that take
// their value as the next word. Left in, the value would be taken for what
// the command runs, and go test -timeout 5m ./... for something else than
// the suite. The names are without their dashes. Options that choose what to
// run (-run, -k, -m, -pl) are not here: a run they choose is a targeted one.
var suiteValued = map[string][]string{
	"go":     {"timeout", "p", "parallel", "count", "cpu", "tags", "mod", "ldflags", "gcflags", "asmflags", "covermode", "coverprofile", "coverpkg", "exec", "shuffle", "benchtime", "vet", "C", "o", "overlay"},
	"make":   {"j", "f", "C", "I", "l", "o", "W", "jobs", "file", "directory", "include-dir", "load-average", "old-file", "what-if"},
	"gmake":  {"j", "f", "C", "I", "l", "o", "W", "jobs", "file", "directory", "include-dir", "load-average", "old-file", "what-if"},
	"cargo":  {"j", "jobs", "target", "features", "F", "manifest-path", "profile", "target-dir"},
	"pytest": {"n", "p", "c", "rootdir"},
}

// suiteForm is the tokens of a command, less the directory of its program, the
// tokens that start with a dash, the values of the options that take one and
// the NAME=value words, which pick no target of a suite. A list of packages
// from go list is the packages of ./....
func suiteForm(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	prog := baseName(tokens[0])
	valued := suiteValued[prog]
	out := []string{prog}
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case strings.HasPrefix(t, "-"):
			if slices.Contains(valued, strings.TrimLeft(t, "-")) {
				i++
			}
		case assignment.MatchString(t):
		case (strings.HasPrefix(t, "$(go list") || strings.HasPrefix(t, "`go list")) && strings.Contains(t, "./..."):
			out = append(out, "./...")
		default:
			out = append(out, t)
		}
	}
	return out
}

// suiteRule (rule 4): the full suite is run by af gate run, which caches it.
func suiteRule(p Policy, s segment) (Decision, bool) {
	if s.prog == "" {
		return Decision{}, false
	}
	tokens := append([]string{s.prog}, s.args...)
	mine := suiteForm(tokens)
	for _, form := range p.Suite {
		if theirs := suiteForm(form); len(theirs) > 0 && slices.Equal(mine, theirs) {
			return deny(RuleSuiteCommand, s.text,
				"%q runs the full suite. Run af gate run instead: it runs the suite once, caches the result by tree hash, and the landing reuses that result. Only targeted runs are allowed here: run just the tests of what you changed, a package or a test by name.",
				strings.Join(tokens, " ")), true
		}
	}
	return Decision{}, false
}

// afAllowed are the commands of af that a brain may run.
var afAllowed = [][]string{
	{"lang", "detect"}, {"gate", "run"}, {"gate", "compare"}, {"revert-check"}, {"map"},
}

// afRule (rule 5): a brain may read the repository, measure it and report; the
// rest of af belongs to the loop.
func afRule(s segment) (Decision, bool) {
	if s.prog == "" || s.program() != "af" {
		return Decision{}, false
	}
	var words []string
	for _, a := range s.args {
		if strings.HasPrefix(a, "-") {
			break
		}
		words = append(words, a)
	}
	if len(words) == 0 {
		if slices.Contains(s.args, "--version") || slices.Contains(s.args, "--schema") {
			return Decision{}, false
		}
	}
	for _, allowed := range afAllowed {
		if len(words) >= len(allowed) && slices.Equal(words[:len(allowed)], allowed) {
			return Decision{}, false
		}
	}
	what := "af " + strings.Join(words, " ")
	if len(words) == 0 {
		what = "af with no command it may run"
	}
	return deny(RuleAfCommand, s.text,
		"%s belongs to the loop, which makes the commits, runs the stages and lands the change. A brain may run af lang detect, gate run, gate compare, revert-check, map, triage brief and submit, and fix brief and submit.", what), true
}

// programRule (rule 6): with an allowlist, only its programs, and af and git.
func programRule(p Policy, s segment) (Decision, bool) {
	if len(p.AllowPrograms) == 0 || s.prog == "" {
		return Decision{}, false
	}
	if isAfOrGit(s.prog) || slices.Contains(builtins, s.prog) || slices.Contains(p.AllowPrograms, s.prog) {
		return Decision{}, false
	}
	return deny(RuleProgramNotAllowed, s.text, "%s is not one of the programs this run may use (%s); af and git are always allowed.", s.prog, strings.Join(p.AllowPrograms, ", ")), true
}

// isAfOrGit reports whether a program word names af or git: by its name, or by
// the path of the system's own. A relative path is the file of the checkout
// with that name, which is not.
func isAfOrGit(prog string) bool {
	if strings.Contains(prog, "/") && !path.IsAbs(prog) {
		return false
	}
	name := baseName(prog)
	return name == "af" || name == "git"
}

// builtins are the words that begin a command and start no program: the
// builtins that only read or set the state of the shell, the compound commands
// whose own commands are judged in turn, and eval and trap, whose scripts are.
var builtins = []string{
	"cd", "pushd", "popd", "pwd", "true", "false", ":", "[", "[[", "test", "echo", "printf", "read", "export", "unset", "set", "shift", "return",
	"exit", "break", "continue", "wait", "local", "declare", "typeset", "readonly", "for", "case", "select", "eval", "trap",
}

// pathRule (rule 7): nothing writes into a protected path but af.
func pathRule(p Policy, dir string, bases []string, s segment) (Decision, bool) {
	if len(p.ProtectedPaths) == 0 {
		return Decision{}, false
	}
	for _, target := range writeTargets(s) {
		if protected(p.ProtectedPaths, dir, bases, target.path, target.removes, true) {
			return deny(RuleProtectedPath, s.text, "%s is a protected path: only af writes there (%s).", target.path, strings.Join(p.ProtectedPaths, ", ")), true
		}
	}
	return Decision{}, false
}

// target is a path a command writes.
type target struct {
	path    string
	removes bool // the command removes it, so it may be removed by removing a directory above it
}

// writeTargets are the paths a command writes: the targets of its output
// redirections, the files the options of any command name for its output, and
// what the arguments of the commands that write name.
func writeTargets(s segment) []target {
	var out []target
	for _, r := range s.redirects {
		switch r.op {
		case ">", ">>", ">|", "&>", "&>>", "<>":
			out = append(out, target{path: r.target})
		case ">&":
			if !isDigits(r.target) && r.target != "-" {
				out = append(out, target{path: r.target})
			}
		}
	}
	if s.prog == "" {
		return out
	}
	add := func(removes bool, paths ...string) {
		for _, p := range paths {
			out = append(out, target{path: p, removes: removes})
		}
	}
	prog := s.program()
	add(false, outputFiles(s.args)...)
	flags, operands, dirFlag := splitArgs(s.args, writerValued[prog])
	last := func() []string {
		if dirFlag != "" {
			return []string{dirFlag}
		}
		if len(operands) > 0 {
			return operands[len(operands)-1:]
		}
		return nil
	}
	switch prog {
	case "rm", "rmdir", "unlink", "shred":
		add(true, operands...)
	case "mv":
		add(true, operands...)
		if dirFlag != "" {
			add(false, dirFlag)
		}
	case "touch", "mkdir", "mkfifo", "tee", "truncate", "chmod", "chown", "chgrp", "patch":
		add(false, operands...)
	case "cp", "install", "rsync":
		add(false, last()...)
	case "ln":
		// Every operand: a link to a protected path is a way to write there.
		add(false, operands...)
		if dirFlag != "" {
			add(false, dirFlag)
		}
	case "sed", "perl":
		if inPlace(flags) {
			add(false, operands...)
		}
	case "dd":
		for _, a := range s.args {
			if file, ok := strings.CutPrefix(a, "of="); ok {
				add(false, file)
			}
		}
	case "find":
		out = append(out, findTargets(s.args)...)
	}
	return out
}

// writerValued are the options of the commands that write that take their
// value as the next word, which is not a path they write.
var writerValued = map[string][]string{
	"touch":    {"-r", "-d", "-t", "--reference", "--date", "--time"},
	"truncate": {"-s", "-r", "--size", "--reference"},
}

// outputFlags are the options, by their names without dashes, that name a file
// the command writes its output to, whatever the command is.
var outputFlags = []string{
	"o", "output", "out", "outfile", "output-file", "emit-events", "log-file", "coverprofile", "cpuprofile", "memprofile", "blockprofile", "mutexprofile", "trace",
}

// outputFiles are the files the arguments of a command name as its output.
func outputFiles(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !slices.Contains(outputFlags, name) {
			continue
		}
		switch {
		case hasValue:
			out = append(out, value)
		case i+1 < len(args):
			out = append(out, args[i+1])
		}
	}
	return out
}

// findTargets are what find writes: the paths it deletes with -delete, and the
// files its -fprint family writes to.
func findTargets(args []string) []target {
	var out []target
	var roots []string
	i := 0
	for i < len(args) && slices.Contains([]string{"-H", "-L", "-P"}, args[i]) {
		i++
	}
	for ; i < len(args) && !strings.HasPrefix(args[i], "-") && !slices.Contains([]string{"(", "!", ","}, args[i]); i++ {
		roots = append(roots, args[i])
	}
	if len(roots) == 0 {
		roots = []string{"."}
	}
	// A delete with a test in front of it removes some of the files under a
	// root, which a root above a protected path may well spare; one without
	// removes them all.
	unfiltered := true
	for j := i; j < len(args); j++ {
		switch a := args[j]; a {
		case "-delete", "-depth", "-print", "-print0", "-xdev", "-mount":
		case "-maxdepth", "-mindepth":
			j++
		default:
			unfiltered = unfiltered && !strings.HasPrefix(a, "-")
		}
	}
	for j, a := range args {
		switch {
		case a == "-delete":
			for _, root := range roots {
				out = append(out, target{path: root, removes: unfiltered})
			}
		case slices.Contains([]string{"-fprint", "-fprint0", "-fprintf", "-fls"}, a) && j+1 < len(args):
			out = append(out, target{path: args[j+1]})
		}
	}
	return out
}

// splitArgs splits the arguments of a command into its flags, its operands and
// the directory that -t or --target-directory names. The options in valued
// take the next word as their value, which is neither. The value of any other
// flag that takes one is read as an operand, which does no harm: it names a
// size, a mode or a script, and not a path in a protected place.
func splitArgs(args, valued []string) (flags, operands []string, targetDir string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			return flags, operands, targetDir
		case a == "-t" && !slices.Contains(valued, a), a == "--target-directory":
			if i+1 < len(args) {
				targetDir = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--target-directory="):
			targetDir = strings.TrimPrefix(a, "--target-directory=")
		case slices.Contains(valued, a):
			flags = append(flags, a)
			i++
		case strings.HasPrefix(a, "-") && a != "-":
			flags = append(flags, a)
		default:
			operands = append(operands, a)
		}
	}
	return flags, operands, targetDir
}

// inPlace reports whether the flags of a sed say that it edits files in place.
func inPlace(flags []string) bool {
	for _, f := range flags {
		switch {
		case f == "--in-place" || strings.HasPrefix(f, "--in-place="):
			return true
		case strings.HasPrefix(f, "--"):
		case strings.Contains(f[1:], "i"):
			return true
		}
	}
	return false
}

// resolve is a path resolved against a base directory.
func resolve(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p)
}

// protected reports whether a path, resolved against the checkout and against
// every directory a cd has gone to, is a protected path or below one. A path
// above one is too, when the command removes it. The path of a shell word
// (shell is set) may start with ~, $HOME or $PWD, which are what they are
// where the shell runs, and a component of it may be a glob; the case of a
// name does not matter, since the file system of a Mac does not tell
// .specs from .SPECS, and links are followed.
func protected(paths []string, dir string, bases []string, p string, removes, shell bool) bool {
	for _, base := range bases {
		word := p
		if shell {
			word = expandWord(p, base)
		}
		abs := resolve(base, word)
		for _, rel := range relativeTo(dir, abs) {
			for _, prot := range paths {
				if covers(rel, prot, removes) {
					return true
				}
			}
		}
	}
	return false
}

// relativeTo is where abs is in the checkout dir, as it is spelled and as the
// links resolve it, when it is in it.
func relativeTo(dir, abs string) []string {
	var out []string
	below := func(root, p string) {
		rel, err := filepath.Rel(filepath.Clean(root), p)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out = append(out, filepath.ToSlash(rel))
		}
	}
	below(dir, abs)
	if !strings.ContainsAny(abs, "*?[") {
		below(realPath(filepath.Clean(dir)), realPath(abs))
	}
	return out
}

// expandWord is the word with the ~, $HOME or $PWD it starts with replaced.
func expandWord(p, pwd string) string {
	home, err := os.UserHomeDir()
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		if err == nil {
			return home + p[1:]
		}
	}
	for _, v := range []struct{ name, value string }{{"PWD", pwd}, {"HOME", home}} {
		if v.value == "" {
			continue
		}
		for _, form := range []string{"$" + v.name, "${" + v.name + "}"} {
			if after, ok := strings.CutPrefix(p, form); ok && (after == "" || after[0] == '/') {
				return v.value + after
			}
		}
	}
	return p
}

// covers reports whether rel, a slash-separated path in the checkout whose
// components may be globs, is the protected path prot or below it, or above it
// when removes is set. Names are compared in lower case.
func covers(rel, prot string, removes bool) bool {
	prot = path.Clean(strings.TrimSuffix(filepath.ToSlash(prot), "/"))
	if prot == "." {
		return true
	}
	var relParts []string
	if rel != "." {
		relParts = strings.Split(strings.ToLower(rel), "/")
	}
	protParts := strings.Split(strings.ToLower(prot), "/")
	for i := range min(len(relParts), len(protParts)) {
		if !componentMatches(relParts[i], protParts[i]) {
			return false
		}
	}
	return len(relParts) >= len(protParts) || removes
}

// componentMatches reports whether a component of a path, which may be a
// glob, can be the name. A glob does not match a name that starts with a dot
// unless it starts with one itself.
func componentMatches(pattern, name string) bool {
	if !strings.ContainsAny(pattern, "*?[") {
		return pattern == name
	}
	if strings.HasPrefix(name, ".") && !strings.HasPrefix(pattern, ".") {
		return false
	}
	ok, err := path.Match(pattern, name)
	return ok && err == nil
}
