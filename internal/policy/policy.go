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

// shellTools are the tools whose input is a line for a shell: Claude Code's
// Bash and the hands' execute.
var shellTools = []string{"Bash", "execute"}

// maxCommandBytes is the longest command line that is judged. The hands' guard
// takes time that grows with the square of the length of the line (a line of
// 150 KB takes it seconds), and a hook that does not answer in time answers
// nothing. A command this long is a file written through a shell: use the tool
// that writes files.
const maxCommandBytes = 64 << 10

// deny is a denial.
func deny(rule, segment, format string, args ...any) Decision {
	return Decision{Decision: Deny, Rule: rule, Segment: segment, Reason: fmt.Sprintf(format, args...)}
}

// writeTools are the tools that write a file, and the field of their input that
// names it.
var writeTools = map[string]string{
	"Write": "file_path", "Edit": "file_path", "MultiEdit": "file_path", "NotebookEdit": "notebook_path",
	"write_file": "path", "edit_file": "path",
}

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
	if protected(p.ProtectedPaths, dir, bases, file, false) {
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
	for _, s := range segments {
		if d, denied := judge(p, dir, bases, s); denied {
			return d
		}
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
}

// flatten lists every command a line runs, outer commands before the ones they
// contain, and the scripts of shells and eval read in turn.
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
		s := newSegment(c)
		out = append(out, s)
		if script, ok := s.script(); ok {
			nested, err := flatten(script, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		}
	}
	return out, nil
}

// newSegment finds the program of a command: the first word that is not an
// assignment, a wrapper (and its options) or a keyword that starts a command.
func newSegment(c command) segment {
	argv := skipPrefix(c.words)
	s := segment{text: c.text, redirects: c.redirects}
	if len(argv) > 0 {
		s.prog, s.args = argv[0], argv[1:]
	}
	return s
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*\+?=`)

// keywords are the words that start a command in a compound command, and
// are not a program.
var keywords = []string{"if", "then", "else", "elif", "while", "until", "do", "{", "!", "}", "fi", "done", "esac"}

// skipPrefix drops what comes before the program of a command.
func skipPrefix(words []string) []string {
	for len(words) > 0 {
		switch {
		case assignment.MatchString(words[0]):
			words = words[1:]
		case slices.Contains(keywords, words[0]):
			words = words[1:]
		case isWrapper(words[0]):
			words = skipWrapper(words)
		default:
			return words
		}
	}
	return words
}

var wrappers = []string{"env", "time", "timeout", "nice", "nohup", "command", "exec"}

func isWrapper(word string) bool {
	return slices.Contains(wrappers, path.Base(word))
}

// skipWrapper drops a wrapper and its options, and what the wrapper takes
// before the command: the duration of timeout, the assignments of env.
func skipWrapper(words []string) []string {
	// valued are the options that take the next word as their value.
	var valued []string
	positional := 0 // words the wrapper takes before the command
	switch path.Base(words[0]) {
	case "env":
		valued = []string{"-u", "-C", "-S", "--unset", "--chdir", "--split-string"}
	case "time":
		valued = []string{"-f", "-o", "--format", "--output"}
	case "timeout":
		valued, positional = []string{"-s", "-k", "--signal", "--kill-after"}, 1
	case "nice":
		valued = []string{"-n", "--adjustment"}
	case "exec":
		valued = []string{"-a"}
	case "command":
		for _, a := range words[1:] {
			if a == "-v" || a == "-V" {
				return nil // it describes a command and runs none
			}
		}
	}

	i := 1
	for i < len(words) {
		a := words[i]
		switch {
		case a == "--":
			return rest(words, i+1, positional)
		case slices.Contains(valued, a):
			i += 2
		case strings.HasPrefix(a, "-") && a != "-":
			i++
		default:
			return rest(words, i, positional)
		}
	}
	return nil
}

// rest is the words from i on, less the positional words a wrapper takes, and
// the assignments an env puts in front.
func rest(words []string, i, positional int) []string {
	if i+positional > len(words) {
		return nil
	}
	return words[i+positional:]
}

// shells are the programs whose -c option takes a script.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh", "ash"}

// script is the script a command runs through a shell or eval, if it does.
func (s segment) script() (string, bool) {
	prog := path.Base(s.prog)
	if prog == "eval" {
		return strings.Join(s.args, " "), len(s.args) > 0
	}
	if !slices.Contains(shells, prog) {
		return "", false
	}
	for i := 0; i < len(s.args); i++ {
		a := s.args[i]
		switch {
		case a == "-o" || a == "+o" || a == "-O" || a == "+O":
			i++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && strings.Contains(a[1:], "c"):
			if i+1 < len(s.args) {
				return s.args[i+1], true
			}
			return "", false
		case strings.HasPrefix(a, "-") || strings.HasPrefix(a, "+"):
		default:
			return "", false // a script file
		}
	}
	return "", false
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
// suite: as it is spelled, less the directory it is run from.
func (s segment) program() string {
	return path.Base(s.prog)
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
	switch {
	case global != "":
		return deny(RuleGitMutation, s.text, "git %s is refused: af makes every commit, branch and push, and sets the configuration it needs itself. Read the repository with git status, log, diff or show.", global), true
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
		case slices.Contains([]string{"-C", "--git-dir", "--work-tree", "--namespace", "--super-prefix", "--attr-source"}, a):
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:], ""
		}
	}
	return "", nil, ""
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

// stripDashes drops the tokens that start with a dash.
func stripDashes(tokens []string) []string {
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if !strings.HasPrefix(t, "-") {
			out = append(out, t)
		}
	}
	return out
}

// suiteForm is the tokens of a command, less the directory of its program and
// the tokens that start with a dash.
func suiteForm(tokens []string) []string {
	if len(tokens) == 0 {
		return nil
	}
	tokens = slices.Clone(tokens)
	tokens[0] = path.Base(tokens[0])
	return stripDashes(tokens)
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
	{"lang", "detect"}, {"gate", "run"}, {"gate", "compare"}, {"revert-check"}, {"map"}, {"guard", "check"},
	{"triage", "brief"}, {"triage", "submit"}, {"fix", "brief"}, {"fix", "submit"},
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
		"%s belongs to the loop, which makes the commits, runs the stages and lands the change. A brain may run af lang detect, gate run, gate compare, revert-check, map, guard check, triage brief and submit, and fix brief and submit.", what), true
}

// programRule (rule 6): with an allowlist, only its programs, and af and git.
func programRule(p Policy, s segment) (Decision, bool) {
	if len(p.AllowPrograms) == 0 || s.prog == "" {
		return Decision{}, false
	}
	if s.prog == "af" || s.prog == "git" || slices.Contains(p.AllowPrograms, s.prog) {
		return Decision{}, false
	}
	return deny(RuleProgramNotAllowed, s.text, "%s is not one of the programs this run may use (%s); af and git are always allowed.", s.prog, strings.Join(p.AllowPrograms, ", ")), true
}

// pathRule (rule 7): nothing writes into a protected path but af.
func pathRule(p Policy, dir string, bases []string, s segment) (Decision, bool) {
	if len(p.ProtectedPaths) == 0 {
		return Decision{}, false
	}
	for _, target := range writeTargets(s) {
		if protected(p.ProtectedPaths, dir, bases, target.path, target.removes) {
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
// redirections, and what the arguments of the commands that write name.
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
	flags, operands, dirFlag := splitArgs(s.args)
	last := func() []string {
		if dirFlag != "" {
			return []string{dirFlag}
		}
		if len(operands) > 0 {
			return operands[len(operands)-1:]
		}
		return nil
	}
	add := func(removes bool, paths ...string) {
		for _, p := range paths {
			out = append(out, target{path: p, removes: removes})
		}
	}
	switch s.program() {
	case "rm":
		add(true, operands...)
	case "mv":
		add(true, operands...)
		if dirFlag != "" {
			add(false, dirFlag)
		}
	case "touch", "mkdir", "tee", "truncate", "chmod":
		add(false, operands...)
	case "cp", "ln":
		add(false, last()...)
	case "sed":
		if inPlace(flags) {
			add(false, operands...)
		}
	}
	return out
}

// splitArgs splits the arguments of a command into its flags, its operands and
// the directory that -t or --target-directory names. The value of any other
// flag that takes one is read as an operand, which does no harm: it names a
// size, a mode or a script, and not a path in a protected place.
func splitArgs(args []string) (flags, operands []string, targetDir string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			return flags, operands, targetDir
		case a == "-t" || a == "--target-directory":
			if i+1 < len(args) {
				targetDir = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--target-directory="):
			targetDir = strings.TrimPrefix(a, "--target-directory=")
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
// above one is too, when the command removes it.
func protected(paths []string, dir string, bases []string, p string, removes bool) bool {
	for _, base := range bases {
		rel, err := filepath.Rel(filepath.Clean(dir), resolve(base, p))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		for _, prot := range paths {
			prot = strings.TrimSuffix(prot, "/")
			if rel == prot || strings.HasPrefix(rel, prot+"/") {
				return true
			}
			if removes && (rel == "." || strings.HasPrefix(prot, rel+"/")) {
				return true
			}
		}
	}
	return false
}
