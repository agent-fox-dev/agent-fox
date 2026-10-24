package toolio

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"
)

// flagValues are what the flags the shell registers parsed to.
type flagValues struct {
	dir     string
	context stringList
	dryRun  bool
	repo    string
	timeout time.Duration
}

// stringList is a flag that may be given more than once.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// flagSet registers the flags of cmd on a new flag set: the flags the command
// opted into, and its own. The flag package prints nothing and never exits.
func flagSet(cmd *Command, v *flagValues) *flag.FlagSet {
	fs := flag.NewFlagSet(cmd.Path, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd.hasDir() {
		fs.StringVar(&v.dir, "dir", ".", "the checkout to work in")
	}
	if cmd.Context {
		fs.Var(&v.context, "context", "an answer to a question the command asked; may be repeated")
	}
	if cmd.DryRun {
		fs.BoolVar(&v.dryRun, "dry-run", false, "send no write request to the forge")
	}
	if cmd.Repo {
		fs.StringVar(&v.repo, "repo", "", "the repository, `owner/repo`")
	}
	if cmd.Timeout {
		fs.DurationVar(&v.timeout, "timeout", cmd.DefaultTimeout, "stop the command after this `duration`; 0 means no limit")
	}
	if cmd.Flags != nil {
		cmd.Flags(fs)
	}
	return fs
}

// hasDir reports whether cmd has --dir: it asked for it, or it uses a forge,
// whose detection depends on the directory, or a run directory, which lives
// under it.
func (c *Command) hasDir() bool {
	return c.Dir || c.UsesForge || c.UsesRunDir
}

// maxArgs is how many positional arguments cmd takes.
func (c *Command) maxArgs() int {
	switch c.MaxArgs {
	case 0:
		return 1
	case NoArgs:
		return 0
	}
	return c.MaxArgs
}

// parse parses the words after the command path into cmd's flags and
// positional arguments. A failure is a usage failure. The values are returned
// whatever happens, with the flags that were understood set, so that the
// --dir of an invocation that is refused is known.
func parse(cmd *Command, words []string) (*flagValues, []string, *Failure) {
	v := &flagValues{}
	fs := flagSet(cmd, v)
	args, err := parseInterspersed(fs, words)
	if err != nil {
		return v, nil, &Failure{Category: CategoryUsage, Message: err.Error()}
	}
	if max := cmd.maxArgs(); len(args) > max {
		msg := fmt.Sprintf("unexpected argument %q", args[max])
		if max == 0 {
			msg += ": the command takes no argument"
		}
		return v, nil, &Failure{Category: CategoryUsage, Message: msg}
	}
	for _, c := range v.context {
		if c == "" {
			return v, nil, &Failure{Category: CategoryUsage, Message: "a --context value is empty; give the answer after the flag"}
		}
	}
	if cmd.hasDir() {
		abs, err := filepath.Abs(v.dir)
		if err != nil {
			return v, nil, &Failure{Category: CategoryUsage, Message: fmt.Sprintf("--dir %q: %v", v.dir, err)}
		}
		v.dir = abs
	}
	return v, args, nil
}

// checkValues refuses the values of the flags that cannot be used: a --dir
// that is no directory for a command with a run directory, and a negative
// --timeout.
func checkValues(cmd *Command, v *flagValues) *Failure {
	if cmd.UsesRunDir {
		if failure := checkDir(v.dir); failure != nil {
			return failure
		}
	}
	if v.timeout < 0 {
		return &Failure{Category: CategoryUsage, Message: fmt.Sprintf("--timeout %s is negative; use 0 for no limit", v.timeout)}
	}
	return nil
}

// parseInterspersed parses words into fs the way the flag package would if
// it did not stop at the first positional argument: flags may come before or
// after the positional arguments, a flag that takes a value takes the next
// word whatever it looks like, and "--" ends the flags. It returns the
// positional arguments. The flags that are understood are set even when
// another is not; the error is the first one found.
func parseInterspersed(fs *flag.FlagSet, words []string) ([]string, error) {
	var flagWords, positional []string
	var first error
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			positional = append(positional, words[i+1:]...)
			break
		}
		name, hasValue, isFlag := splitFlag(w)
		if !isFlag {
			positional = append(positional, w)
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			if first == nil {
				first = fmt.Errorf("unknown flag --%s", name)
			}
			continue
		}
		flagWords = append(flagWords, w)
		if hasValue || isBoolFlag(f) {
			continue
		}
		if i+1 >= len(words) {
			if first == nil {
				first = fmt.Errorf("flag --%s needs a value", name)
			}
			flagWords = flagWords[:len(flagWords)-1]
			continue
		}
		i++
		flagWords = append(flagWords, words[i])
	}
	if err := fs.Parse(flagWords); err != nil && first == nil {
		first = err
	}
	return positional, first
}

// takesValue reports whether w is a flag of fs that takes the word after it
// as its value.
func takesValue(fs *flag.FlagSet, w string) bool {
	name, hasValue, isFlag := splitFlag(w)
	if !isFlag || hasValue {
		return false
	}
	f := fs.Lookup(name)
	return f != nil && !isBoolFlag(f)
}

// splitFlag reads w as a flag: its name, and whether it carries its value
// after an "=". A lone "-" is a positional argument, not a flag.
func splitFlag(w string) (name string, hasValue, isFlag bool) {
	if len(w) < 2 || w[0] != '-' {
		return "", false, false
	}
	name = strings.TrimPrefix(strings.TrimPrefix(w, "-"), "-")
	name, _, hasValue = strings.Cut(name, "=")
	return name, hasValue, name != ""
}

// isBoolFlag reports whether f takes no value word.
func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
