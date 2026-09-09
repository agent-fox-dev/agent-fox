// Package checks detects and runs the command that decides whether a change
// is correct.
//
// It is separate from the agent on purpose. The thing that decides whether
// the work succeeded must not be the thing that did the work: a prompt-only
// agent runs the tests and also writes the sentence "all tests pass", and
// nothing checks that those two are related. Here the run is made from Go and
// its exit status is the fact a report is rendered from.
package checks

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// Result is what one verification run produced.
type Result struct {
	// Command is what ran. Empty with Skipped means nothing did.
	Command string `json:"command"`
	// Skipped reports that no command was run — either none could be
	// detected, or the operator asked for none. It is a first-class outcome,
	// not a pass: a report renders it as "unverified".
	Skipped bool `json:"skipped,omitempty"`
	// OK is true only when the command ran and exited zero.
	OK bool `json:"ok"`
	// ExitCode is the command's exit status, or -1 when it could not run.
	ExitCode int `json:"exit_code"`
	// Output is the tail of the combined output, bounded.
	Output string `json:"output,omitempty"`
	// TimedOut reports that the run was killed by the timeout.
	TimedOut bool `json:"timed_out,omitempty"`
	// DurationMS is how long it took.
	DurationMS int64 `json:"duration_ms"`
}

// Ran reports whether a command actually executed.
func (r Result) Ran() bool { return !r.Skipped && r.Command != "" }

// outputTailLines bounds what a Result carries. A failing suite's last forty
// lines contain the failure; the four thousand before it are the passing
// tests, and they cost prompt tokens and log space for nothing.
const outputTailLines = 40

// DefaultTimeout bounds one verification run.
const DefaultTimeout = 10 * time.Minute

// Detect picks the command that decides whether a run succeeded. It reads the
// repository the way a new contributor would: the Makefile first, because a
// project that ships one has already answered the question, then the
// language's default.
//
// It returns "" when it cannot tell. That is a first-class outcome: the
// pipeline then refuses to claim the work is verified, and says so, instead
// of inventing a command and reporting its absence as success.
func Detect(dir string) string {
	read := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return string(b), err == nil
	}

	if mk, ok := read("Makefile"); ok {
		for _, target := range []string{"check", "test"} {
			if makeTargetRe(target).MatchString(mk) {
				return "make " + target
			}
		}
	}
	if _, ok := read("go.mod"); ok {
		return "go test ./... -count=1"
	}
	if pkg, ok := read("package.json"); ok && strings.Contains(pkg, `"test"`) {
		return "npm test"
	}
	if _, ok := read("pyproject.toml"); ok {
		if _, uv := read("uv.lock"); uv {
			return "uv run pytest -q"
		}
		return "pytest -q"
	}
	if _, ok := read("Cargo.toml"); ok {
		return "cargo test"
	}
	return ""
}

// makeTargetRe finds a target definition at the start of a line.
func makeTargetRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*:`)
}

// Run executes the command and reports what happened.
//
// The command is split on whitespace and executed directly — there is no
// shell between this process and the program. That rules out `make check &&
// lint` style compound commands, and the trade is deliberate: verification is
// the one place where "what exactly ran" must be unambiguous.
//
// The runner is expected to be gitx.ReducedEnvRunner: a project's test suite
// is repository code, and it does not need the model vendor's API key.
func Run(ctx context.Context, r gitx.Runner, dir, command string, timeout time.Duration) Result {
	if strings.TrimSpace(command) == "" {
		return Result{Skipped: true}
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if r == nil {
		r = gitx.ReducedEnvRunner
	}
	argv := strings.Fields(command)
	start := time.Now()

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out, code, err := r(runCtx, dir, argv)
	res := Result{
		Command:    command,
		ExitCode:   code,
		OK:         err == nil && code == 0,
		Output:     Tail(out, outputTailLines),
		DurationMS: time.Since(start).Milliseconds(),
	}
	if runCtx.Err() != nil && ctx.Err() == nil {
		res.TimedOut = true
		res.OK = false
		if res.ExitCode == 0 {
			res.ExitCode = -1
		}
	}
	if err != nil {
		res.OK = false
		res.ExitCode = -1
		res.Output = strings.TrimSpace(err.Error() + "\n" + res.Output)
	}
	return res
}

// Program is the first word of a command — the program a phase's shell
// allowlist has to include, or the agent cannot run the suite it is being
// judged by.
func Program(command string) string {
	if f := strings.Fields(command); len(f) > 0 {
		return filepath.Base(f[0])
	}
	return ""
}

// Tail returns the last n lines of s.
func Tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return strings.TrimSpace(strings.Join(lines, "\n"))
	}
	return "[…]\n" + strings.TrimSpace(strings.Join(lines[len(lines)-n:], "\n"))
}

// Verdict classifies a before/after pair of runs.
//
// It exists because "the tests must pass" and "note the pre-existing
// failures" cannot both be satisfied on a repository that was already red,
// and a program that does not name that case forces a model to pick one
// silently.
type Verdict string

const (
	// VerdictUnverified means nothing ran. It is not a pass.
	VerdictUnverified Verdict = "unverified"
	// VerdictPass means the checks pass after the change.
	VerdictPass Verdict = "pass"
	// VerdictRepaired means the checks pass now and were already failing
	// before the change. It is a pass, reported honestly.
	VerdictRepaired Verdict = "pass_was_already_failing"
	// VerdictRegressed means the checks passed before the change and fail
	// after it. The change caused this.
	VerdictRegressed Verdict = "regressed"
	// VerdictStillFailing means the checks failed before and after. The
	// change did not fix them, and it may not have caused them either.
	VerdictStillFailing Verdict = "still_failing"
)

// Compare classifies a baseline and a post-change run.
func Compare(baseline, after Result) Verdict {
	switch {
	case !after.Ran():
		return VerdictUnverified
	case after.OK && (!baseline.Ran() || baseline.OK):
		return VerdictPass
	case after.OK:
		return VerdictRepaired
	case baseline.Ran() && !baseline.OK:
		return VerdictStillFailing
	default:
		return VerdictRegressed
	}
}

// Landable reports whether a verdict is one a pipeline may commit and push.
// VerdictUnverified is landable only when the operator asked for no checks;
// that decision belongs to the caller, so it is not made here.
func (v Verdict) Landable() bool {
	return v == VerdictPass || v == VerdictRepaired
}
