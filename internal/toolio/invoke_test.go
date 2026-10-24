package toolio

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"
)

// echoOptions are the options of the command of the tests of Invoke.
type echoOptions struct{ Word string }

// echoApp is an app with one command that reports its input, its options and the values of the
// shell's flags it was given.
func echoApp() *App {
	app := newTestApp()
	var word string
	app.Register(Command{
		Path: "echo it", Input: InputRequired, Dir: true, Context: true, Timeout: true, DefaultTimeout: time.Hour,
		Flags: func(fs *flag.FlagSet) { fs.StringVar(&word, "word", "default", "a word") },
		Options: func(v any) error {
			o, ok := v.(echoOptions)
			if !ok {
				return fmt.Errorf("echo it takes echoOptions, not %T", v)
			}
			if o.Word != "" {
				word = o.Word
			}
			return nil
		},
		Run: func(r *Run) (Outcome, error) {
			deadline, _ := r.Ctx.Deadline()
			return Outcome{Status: StatusDone, Summary: "ok", Result: map[string]any{
				"body": r.Input.Body, "word": word, "dir": r.Dir, "context": r.Context, "deadline_set": !deadline.IsZero(),
			}}, nil
		},
	})
	app.Register(Command{Path: "plain", MaxArgs: NoArgs, Run: func(*Run) (Outcome, error) {
		return Outcome{Status: StatusDone, Summary: "plain"}, nil
	}})
	return app
}

// TestInvokeRunsACallAsMainRunsItsCommandLine pins that a call is the command line it stands
// for: the same envelope, but for the time and the argv, with the options of the call in place of
// the flags.
func TestInvokeRunsACallAsMainRunsItsCommandLine(t *testing.T) {
	dir := t.TempDir()
	app := echoApp()

	env, encoded := app.Invoke(context.Background(), Call{
		Path: "echo it", Args: []string{"-"}, Stdin: strings.NewReader("the text"), Dir: dir,
		Context: []string{"a", "b"}, Options: echoOptions{Word: "given"},
	})

	if env.Status != StatusDone || env.Tool != "echo it" {
		t.Fatalf("envelope %s", encoded)
	}
	result := env.Result.(map[string]any)
	if result["body"] != "the text" || result["word"] != "given" || result["dir"] != dir || fmt.Sprint(result["context"]) != "[a b]" || result["deadline_set"] != true {
		t.Errorf("result %+v, want the input, the option, the directory, the context and the default deadline", result)
	}
	var out, errb bytes.Buffer
	app.Main(context.Background(), []string{"af", "echo", "it", "-", "--dir", dir, "--word", "given", "--context", "a", "--context", "b"}, strings.NewReader("the text"), &out, &errb)
	if !bytes.Contains(out.Bytes(), []byte(`"given"`)) || !bytes.Contains(encoded, []byte(`"given"`)) {
		t.Errorf("Main wrote %s and Invoke %s", out.Bytes(), encoded)
	}

	if env, _ := app.Invoke(context.Background(), Call{Path: "echo it", Args: []string{"-"}, Stdin: strings.NewReader("x"), Dir: dir}); env.Result.(map[string]any)["word"] != "default" {
		t.Errorf("a call with no options: %+v, want the default of the flag", env.Result)
	}
}

// TestInvokeRefusesWhatMainRefuses pins the usage failures of a call.
func TestInvokeRefusesWhatMainRefuses(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]Call{
		"an unknown command":          {Path: "nothing"},
		"too many arguments":          {Path: "plain", Args: []string{"x"}},
		"no directory":                {Path: "echo it", Args: []string{"-"}},
		"an empty context value":      {Path: "echo it", Args: []string{"-"}, Dir: dir, Context: []string{""}},
		"options of another type":     {Path: "echo it", Args: []string{"-"}, Dir: dir, Options: 7},
		"options a command never had": {Path: "plain", Options: echoOptions{}},
		"a negative timeout":          {Path: "echo it", Args: []string{"-"}, Dir: dir, Timeout: -time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			env, encoded := echoApp().Invoke(context.Background(), c)

			if env.Status != StatusUsage || env.Error == nil || env.Error.Category != CategoryUsage || len(encoded) == 0 {
				t.Errorf("envelope %s, want a usage failure", encoded)
			}
		})
	}
}

// TestInvokeIsStoppedByItsContext pins that the context of a call is the context of the command.
func TestInvokeIsStoppedByItsContext(t *testing.T) {
	b := newBlocking()
	app := newTestApp()
	app.NoSignals = true
	app.Register(b.command())
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-b.started; cancel() }()

	app.Invoke(ctx, Call{Path: "X"})

	if cause := <-b.cause; !errors.Is(cause, context.Canceled) {
		t.Errorf("the command was stopped by %v", cause)
	}
}
