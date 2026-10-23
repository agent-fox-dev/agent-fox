package toolio

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestTheModeHookChoosesTheCommandAndTheWordsBeforeAnythingIsBuilt_TS_16_1 verifies, for the
// shell, what TS-16-1 and TS-16-3 rely on: an App with a Mode hook asks it once the words have
// named a command, before the flags are parsed, the input is resolved, a forge client is built or
// a run directory is made. The hook may refuse, with the envelope of a failure that still honours
// --output and --emit-events, or choose another command and the words it parses, while the
// invocation keeps its own argv.
// Verifies: 16-REQ-1.1, 16-REQ-1.3
func TestTheModeHookChoosesTheCommandAndTheWordsBeforeAnythingIsBuilt_TS_16_1(t *testing.T) {
	var parsed []string
	other := &Command{Path: "work", MaxArgs: NoArgs, Flags: nil, Run: func(r *Run) (Outcome, error) {
		parsed = r.Argv
		return Outcome{Status: StatusDone, Summary: "the other command ran"}, nil
	}}
	local := Command{Path: "work", Input: InputRequired, UsesForge: true, UsesRunDir: true, Run: func(r *Run) (Outcome, error) {
		t.Error("the command ran although the hook refused it")
		return Outcome{}, nil
	}}

	forgeBuilt := false
	newApp := func(mode func(cmd *Command, words []string) (*Command, []string, *Failure)) *App {
		app := newTestApp()
		app.Mode = mode
		app.NewForge = nil
		app.Register(local)
		return app
	}
	dir := t.TempDir()

	t.Run("a refusal builds nothing and honours --output and --emit-events", func(t *testing.T) {
		app := newApp(func(cmd *Command, words []string) (*Command, []string, *Failure) {
			return nil, nil, &Failure{Category: CategoryUsage, Message: "choose a mode", FixHint: &FixHint{Flag: "--local"}}
		})
		out, events := filepath.Join(dir, "out.json"), filepath.Join(dir, "events.jsonl")
		code, stdout, _ := invoke(t, app, "work", "https://github.com/o/r/issues/1", "--dir", dir, "--output", out, "--emit-events", events)
		if code != 2 || at(decode(t, stdout), "error", "message") != "choose a mode" || at(decode(t, stdout), "tool") != "work" {
			t.Fatalf("exit %d, %s, want 2 with the message of the hook for the tool work", code, stdout)
		}
		if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); err == nil {
			t.Error("a run directory was made")
		}
		if got, err := os.ReadFile(out); err != nil || string(got) != stdout {
			t.Errorf("--output = %q (%v), want the envelope", got, err)
		}
		if got, err := os.ReadFile(events); err != nil || !strings.Contains(string(got), `"run_end"`) {
			t.Errorf("--emit-events = %q (%v), want the events of a refused invocation", got, err)
		}
		if forgeBuilt {
			t.Error("a forge client was built")
		}
	})

	t.Run("a choice replaces the command and the words", func(t *testing.T) {
		var seen []string
		app := newApp(func(cmd *Command, words []string) (*Command, []string, *Failure) {
			seen = slices.Clone(words)
			return other, slices.DeleteFunc(slices.Clone(words), func(w string) bool { return w == "--mode" }), nil
		})
		code, stdout, _ := invoke(t, app, "work", "--mode")
		if code != 0 || at(decode(t, stdout), "tool") != "work" {
			t.Fatalf("exit %d, %s, want the chosen command to run as the tool work", code, stdout)
		}
		if !slices.Equal(seen, []string{"--mode"}) {
			t.Errorf("the hook was given the words %v, want those after the command path", seen)
		}
		if !slices.Equal(parsed, []string{"af", "work", "--mode"}) {
			t.Errorf("the invocation's argv is %v, want it as it was received", parsed)
		}
	})

	t.Run("an app without a hook is as it was", func(t *testing.T) {
		app := newTestApp()
		app.Register(Command{Path: "plain", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) { return Outcome{Status: StatusDone}, nil }})
		if code, _, _ := invoke(t, app, "plain"); code != 0 {
			t.Errorf("exit %d, want 0", code)
		}
	})
}

// TestARunMaySetTheInputItReportsInTheEnvelope_TS_16_49 verifies, for the shell, what TS-16-49
// relies on: a command that reads its own input says what it was, and the envelope reports it.
// Verifies: 16-REQ-7.5
func TestARunMaySetTheInputItReportsInTheEnvelope_TS_16_49(t *testing.T) {
	app := newTestApp()
	app.Register(Command{Path: "own", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) {
		r.SetInput(KindFile, "/abs/notes.txt", true)
		return Outcome{Status: StatusDone, Summary: "ok"}, nil
	}})
	app.Register(Command{Path: "none", MaxArgs: NoArgs, Run: func(r *Run) (Outcome, error) { return Outcome{Status: StatusDone, Summary: "ok"}, nil }})
	_, stdout, _ := invoke(t, app, "own")
	env := decode(t, stdout)
	if at(env, "input", "kind") != "file" || at(env, "input", "origin") != "/abs/notes.txt" || at(env, "input", "truncated") != true {
		t.Errorf("input = %v, want the file the command set", env["input"])
	}
	_, stdout, _ = invoke(t, app, "none")
	if at(decode(t, stdout), "input", "kind") != "none" {
		t.Errorf("a command that sets no input reports %v, want none", at(decode(t, stdout), "input"))
	}
}

// TestTheShellTellsWhichFlagsTakeAValueAndLetsAppHelpAddToTheHelp_TS_16_5 verifies, for the
// shell, what TS-16-5 and TS-16-8 rely on: a scan of the words can ask whether a word is a flag and
// whether the flag of a command takes the word after it, and the help of a command may end with text
// of the app's.
// Verifies: 16-REQ-1.5, 16-REQ-1.8
func TestTheShellTellsWhichFlagsTakeAValueAndLetsAppHelpAddToTheHelp_TS_16_5(t *testing.T) {
	for w, want := range map[string]struct {
		name             string
		hasValue, isFlag bool
	}{
		"--dir":    {"dir", false, true},
		"-dir":     {"dir", false, true},
		"--dir=x":  {"dir", true, true},
		"-local=1": {"local", true, true},
		"--":       {"", false, false},
		"-":        {"", false, false},
		"word":     {"", false, false},
		"--empty=": {"empty", true, true},
	} {
		name, hasValue, isFlag := SplitFlag(w)
		if isFlag != want.isFlag || (isFlag && (name != want.name || hasValue != want.hasValue)) {
			t.Errorf("SplitFlag(%q) = %q, %v, %v, want %q, %v, %v", w, name, hasValue, isFlag, want.name, want.hasValue, want.isFlag)
		}
	}

	cmd := &Command{Path: "work", Summary: "does work", Dir: true, Context: true, DryRun: true, Timeout: true}
	lookup := cmd.FlagLookup()
	for name, want := range map[string][2]bool{
		"dir": {true, true}, "context": {true, true}, "timeout": {true, true}, "output": {true, true}, "emit-events": {true, true},
		"dry-run": {true, false}, "unknown": {false, false}, "repo": {false, false},
	} {
		if known, takes := lookup(name); known != want[0] || takes != want[1] {
			t.Errorf("FlagLookup(%q) = %v, %v, want %v, %v", name, known, takes, want[0], want[1])
		}
	}

	app := newTestApp()
	app.Register(*cmd)
	app.Register(Command{Path: "other", MaxArgs: NoArgs})
	app.HelpExtra = func(c *Command) string {
		if c.Path == "work" {
			return "\nTwo modes.\n"
		}
		return ""
	}
	if _, stdout, _ := invoke(t, app, "work", "--help"); !strings.HasSuffix(stdout, "\nTwo modes.\n") {
		t.Errorf("the help of work is %q, want it to end with the text of the app", stdout)
	}
	if _, stdout, _ := invoke(t, app, "help", "work"); !strings.HasSuffix(stdout, "\nTwo modes.\n") {
		t.Errorf("help work is %q, want it to end with the text of the app", stdout)
	}
	if _, stdout, _ := invoke(t, app, "other", "--help"); strings.Contains(stdout, "Two modes") {
		t.Errorf("the help of other is %q, want nothing added", stdout)
	}
	if got := app.HelpFor(cmd); !strings.HasPrefix(got, "Usage: ") || !strings.HasSuffix(got, "\nTwo modes.\n") {
		t.Errorf("HelpFor = %q, want the help of the command with the text of the app", got)
	}
}
