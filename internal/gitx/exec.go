// Package gitx wraps the git and shell invocations the agent-fox tools make.
//
// There is no "run whatever the model says" path here. Every git mutation a
// tool performs is issued from this package, from Go, at a point in a
// pipeline that knows why — and the agents' shells are separately forbidden
// from running mutating git subcommands. That is what makes "committed as
// abc123" in a run summary mean exactly one thing.
package gitx

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/agentfox/agentkit-go/tools"
)

// Runner runs one external command and returns its combined output, its exit
// code, and an error for the failures that are NOT an exit code — the program
// is missing, the context was cancelled, the binary could not be executed.
//
// The two are separated because they need different handling: a non-zero exit
// from `git status` is information, while "git: executable file not found" is
// a broken environment, and one error return conflates them.
type Runner func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error)

// ExecRunner is the production Runner. Output is combined because these
// commands are read by a person in a log, and interleaved stderr is what puts
// a failing command's message next to the command that produced it.
func ExecRunner(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
	return run(ctx, dir, argv, nil, stdin...)
}

// ReducedEnvRunner is ExecRunner with the credential variables stripped: the
// model vendors' keys, and anything named *_TOKEN, *_SECRET, *_API_KEY or
// *_PASSWORD.
//
// It is what a project's own test suite runs under. A test suite is
// repository code, and a repository a tool is changing on a stranger's bug
// report is not something to hand an API key to. git keeps the full
// environment, because its credential helper lives there.
func ReducedEnvRunner(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
	return run(ctx, dir, argv, tools.ReducedEnv(nil), stdin...)
}

func run(ctx context.Context, dir string, argv, env []string, stdin ...string) (string, int, error) {
	if len(argv) == 0 {
		return "", -1, errors.New("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = env
	}
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(strings.Join(stdin, ""))
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return string(out), ee.ExitCode(), nil
		}
		return string(out), -1, fmt.Errorf("running %s: %w", argv[0], err)
	}
	return string(out), 0, nil
}
