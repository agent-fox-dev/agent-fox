package toolio

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// echoOptions are the options of the command echo it.
type echoOptions struct{ Word string }

// echoApp is an app whose command "echo it" answers with its input, its
// option and the values of the shell's flags it was given, and whose command
// "plain" takes nothing.
func echoApp() *App {
	app := newApp()
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
				"body": r.Input.Body, "word": word, "dir": r.Dir, "context": r.Context, "deadline": time.Until(deadline).Round(time.Hour).String(),
			}}, nil
		},
	})
	app.Register(Command{Path: "plain", MaxArgs: NoArgs, Run: func(*Run) (Outcome, error) { return Outcome{Status: StatusDone, Summary: "plain"}, nil }})
	return app
}

func TestInvoke_RunsACallAsMainRunsTheCommandLineItStandsFor(t *testing.T) {
	dir := t.TempDir()
	app := echoApp()

	env, encoded := app.Invoke(context.Background(), Call{
		Path: "echo it", Args: []string{"-"}, Stdin: strings.NewReader("the text"), Dir: dir,
		Context: []string{"a", "b"}, Options: echoOptions{Word: "given"},
	})
	r := runWith(t, context.Background(), app, strings.NewReader("the text"), "echo", "it", "-", "--dir", dir, "--word", "given", "--context", "a", "--context", "b")

	want := map[string]any{"body": "the text", "word": "given", "dir": dir, "context": []any{"a", "b"}, "deadline": "1h0m0s"}
	if env.Status != StatusDone || env.Tool != "echo it" || fmt.Sprint(at(testutil.Decode[map[string]any](t, encoded), "result")) != fmt.Sprint(want) {
		t.Errorf("Invoke: %s, want the result %v", encoded, want)
	}
	if fmt.Sprint(at(r.env(t), "result")) != fmt.Sprint(want) {
		t.Errorf("Main: %s, want the result %v", r.stdout, want)
	}
	env, _ = app.Invoke(context.Background(), Call{Path: "echo it", Args: []string{"x"}, Dir: dir, Timeout: 3 * time.Hour})
	if result := env.Result.(map[string]any); result["word"] != "default" || result["deadline"] != "3h0m0s" {
		t.Errorf("a call with no options and a timeout: %v, want the default word and a deadline in 3h", result)
	}
}

func TestInvoke_RefusesWhatMainRefuses(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]Call{
		"an unknown command":          {Path: "nothing"},
		"too many arguments":          {Path: "plain", Args: []string{"x"}},
		"no directory":                {Path: "echo it", Args: []string{"-"}},
		"an empty context value":      {Path: "echo it", Args: []string{"-"}, Dir: dir, Context: []string{""}},
		"options of another type":     {Path: "echo it", Args: []string{"-"}, Dir: dir, Options: 7},
		"options a command never had": {Path: "plain", Options: echoOptions{}},
		"a negative timeout":          {Path: "echo it", Args: []string{"-"}, Dir: dir, Timeout: -time.Second},
		"no input":                    {Path: "echo it", Dir: dir},
	} {
		env, encoded := echoApp().Invoke(context.Background(), c)

		if env.Status != StatusUsage || env.Error == nil || env.Error.Category != CategoryUsage || testutil.Decode[Envelope](t, encoded).Status != StatusUsage {
			t.Errorf("%s: %s, want a usage failure", name, encoded)
		}
	}
}

func TestInvoke_IsStoppedByItsContext(t *testing.T) {
	cause := make(chan error, 1)
	app := newApp()
	app.NoSignals = true
	app.Register(Command{Path: "X", Run: func(r *Run) (Outcome, error) {
		<-r.Ctx.Done()
		cause <- context.Cause(r.Ctx)
		return Outcome{Status: StatusDone, Summary: "stopped"}, nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	env, _ := app.Invoke(ctx, Call{Path: "X"})

	if err := <-cause; !errors.Is(err, context.Canceled) || env.Status != StatusDone {
		t.Errorf("the command was stopped by %v and ended %s", err, env.Status)
	}
}
