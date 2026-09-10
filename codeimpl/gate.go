package codeimpl

import (
	"context"
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// The gate is the format's implicit definition of done (§8.3) made into a
// measurement: the spec's own linter and all_tests, run before any change
// and after every task, and compared pair by pair.

// VerdictGateFailed is the verdict when a check could not run at all after
// a task — the program was missing or the run timed out. It is neither a
// pass nor the task's fault, so it is its own outcome rather than a
// regression, and a task that ends on it is parked without a retry: the
// model's work was never measured.
const VerdictGateFailed = "gate_failed"

// gateCommands picks the commands one run is judged by.
func gateCommands(tc afspec.TestCommands, override string, none bool) []string {
	if none {
		return nil
	}
	if strings.TrimSpace(override) != "" {
		return []string{strings.TrimSpace(override)}
	}
	var out []string
	for _, c := range []string{tc.Linter, tc.AllTests} {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// shellOnly are the characters checks.Run cannot honour: it runs a program,
// not a shell, so a command that needs one would run with them as literal
// arguments and report a failure that is nobody's.
const shellOnly = "|;&<>$()`\"'\n"

// checkCommandShape refuses a command that only a shell could run.
func checkCommandShape(field, cmd string) error {
	what := ""
	if i := strings.IndexAny(cmd, shellOnly); i >= 0 {
		what = fmt.Sprintf("%q", string(cmd[i]))
	} else if f := strings.Fields(cmd); len(f) > 0 {
		// A leading NAME=value is an environment assignment a shell would
		// apply and a program runner would try to execute.
		if i := strings.IndexByte(f[0], '='); i > 0 && !strings.ContainsAny(f[0][:i], "/.") {
			what = "an environment assignment"
		}
	}
	if what == "" {
		return nil
	}
	return fmt.Errorf("test_commands.%s is %q, which needs a shell to run (%s); the checks "+
		"run one program with its arguments, so put the compound command in a Makefile "+
		"target or a script and name that", field, cmd, what)
}

// runGate runs the commands in order and reports each.
func runGate(ctx context.Context, o Options, root string, cmds []string, label string) GateResult {
	var g GateResult
	for _, cmd := range cmds {
		done := o.Progress.Begin("%s: %s", label, cmd)
		res := checks.Run(ctx, o.CheckRunner, root, cmd, o.VerifyTimeout)
		status := "passed"
		switch {
		case res.TimedOut:
			status = "timed out"
		case res.ExitCode == -1:
			status = "could not run"
		case !res.OK:
			status = fmt.Sprintf("failed (exit %d)", res.ExitCode)
		}
		done(status)
		g.Checks = append(g.Checks, res)
	}
	return g
}

// Ran reports whether anything executed.
func (g GateResult) Ran() bool {
	for _, c := range g.Checks {
		if c.Ran() {
			return true
		}
	}
	return false
}

// OK reports whether every check ran and passed.
func (g GateResult) OK() bool {
	if len(g.Checks) == 0 {
		return false
	}
	for _, c := range g.Checks {
		if !c.OK {
			return false
		}
	}
	return true
}

// couldNotRun names the first check that never produced an exit status.
func (g GateResult) couldNotRun() (checks.Result, bool) {
	for _, c := range g.Checks {
		if c.Ran() && (c.ExitCode == -1 || c.TimedOut) {
			return c, true
		}
	}
	return checks.Result{}, false
}

// failing lists the checks that did not pass, for a prompt or a report.
func (g GateResult) failing() []checks.Result {
	var res []checks.Result
	for _, c := range g.Checks {
		if c.Ran() && !c.OK {
			res = append(res, c)
		}
	}
	return res
}

// verdictRank orders verdicts from best to worst, so a gate's verdict is
// the worst of its checks'.
var verdictRank = map[string]int{
	string(checks.VerdictPass):         0,
	string(checks.VerdictRepaired):     1,
	string(checks.VerdictUnverified):   2,
	string(checks.VerdictStillFailing): 3,
	string(checks.VerdictRegressed):    4,
	VerdictGateFailed:                  5,
}

// compareGate classifies a before/after pair of gates: the worst verdict
// over the commands they share, with a check that could not run after the
// task as its own, worst outcome.
func compareGate(baseline, after GateResult) string {
	if len(after.Checks) == 0 {
		return string(checks.VerdictUnverified)
	}
	worst := string(checks.VerdictPass)
	for i, a := range after.Checks {
		var b checks.Result
		if i < len(baseline.Checks) {
			b = baseline.Checks[i]
		}
		v := string(checks.Compare(b, a))
		if a.Ran() && (a.ExitCode == -1 || a.TimedOut) {
			v = VerdictGateFailed
		}
		if verdictRank[v] > verdictRank[worst] {
			worst = v
		}
	}
	return worst
}

// landable reports whether a verdict lets a task be committed as done. An
// unverified verdict is landable only when nothing was asked to run; that
// decision is the caller's and it is passed in.
func landable(verdict string, nothingToRun bool) bool {
	switch verdict {
	case string(checks.VerdictPass), string(checks.VerdictRepaired):
		return true
	case string(checks.VerdictUnverified):
		return nothingToRun
	}
	return false
}
