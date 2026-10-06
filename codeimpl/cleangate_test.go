package codeimpl

import (
	"context"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// suiteCounter counts the runs of `make test` through the normal runner and
// through the clean-environment one. extra, when set, is prepended to the
// normal runner's command line: an environment the clean run does not have.
type suiteCounter struct {
	normal, clean int
}

func (c *suiteCounter) install(o *Options, extra ...string) {
	o.CheckRunner = func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if strings.Join(argv, " ") == "make test" {
			c.normal++
			argv = append(append([]string(nil), extra...), argv...)
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	}
	o.HermeticRunner = func(home string) gitx.Runner {
		inner := gitx.HermeticRunner(home)
		return func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
			if strings.Join(argv, " ") == "make test" {
				c.clean++
			}
			return inner(ctx, dir, argv, stdin...)
		}
	}
}

// Issue #197: the last task's checks run in the clean environment, and the
// conformance stage takes that run as its clean-environment verification
// instead of running the suite again on the same tree.
func TestTheLastTasksCleanRunIsTheFinalVerification(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	b := &scriptedBrain{}
	o := newOptions(ws, g, b)
	var c suiteCounter
	c.install(&o)
	got, err := Run(context.Background(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if c.clean != 1 {
		t.Errorf("the suite ran %d time(s) in the clean environment, want once", c.clean)
	}
	// The baseline and tasks 1 and 2 run it as before; task 3's landing gate
	// is the clean run.
	if c.normal != 3 {
		t.Errorf("the suite ran %d time(s) in the normal environment, want 3 (baseline, tasks 1 and 2)", c.normal)
	}
	last := got.Tasks[len(got.Tasks)-1]
	if got.FinalVerification == nil || !got.FinalVerification.OK() || last.Verification == nil ||
		got.FinalVerification.Checks[1].DurationMS != last.Verification.Checks[1].DurationMS {
		t.Errorf("FinalVerification = %+v, want task 3's clean run %+v", got.FinalVerification, last.Verification)
	}
	if env := got.Environment; env == nil || !env.Hermetic {
		t.Errorf("Environment = %+v", got.Environment)
	}
}

// A clean run that fails where the normal one passes is not the task's
// failure: the task lands on the normal run, and the failing clean run is the
// conformance stage's finding, without running the suite a third time.
func TestACleanRunThatFailsFallsBackToTheNormalGate(t *testing.T) {
	ws, g, _ := newSpecRepo(t)
	write(t, ws.Root, "Makefile", "test:\n\t@test -n \"$$AF_NORMAL_ENV\"\nlint:\n\t@exit 0\n")
	if _, err := g.CommitAll(context.Background(), "chore: a suite that needs the machine's environment\n"); err != nil {
		t.Fatal(err)
	}
	b := &scriptedBrain{resolve: func(string, resolveInput) (ResolveSubmission, error) {
		return ResolveSubmission{Summary: "The clean-environment failure is the machine's; nothing to change."}, nil
	}}
	o := newOptions(ws, g, b)
	var c suiteCounter
	c.install(&o, "env", "AF_NORMAL_ENV=1")
	got, _ := Run(context.Background(), o)
	if got.TasksDone != 3 {
		t.Fatalf("TasksDone = %d: the last task must land on the normal run", got.TasksDone)
	}
	if c.clean != 1 {
		t.Errorf("the suite ran %d time(s) in the clean environment, want once", c.clean)
	}
	if got.FinalVerification == nil || got.FinalVerification.OK() {
		t.Errorf("FinalVerification = %+v, want the failing clean run", got.FinalVerification)
	}
}
