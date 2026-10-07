package codeimpl

import (
	"context"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// The conformance stage runs the gate once more in a clean environment. When
// the gate that lands the run's last commit has just run on the same tree,
// that is the suite twice in a row, minutes each, with only the environment
// different. So the gate of the last task, and of the resolve phase's change,
// runs in the clean environment first: a pass there is the landing verdict
// and, once the commit is made, the stage's clean-environment verification.
// A failure there is the stage's finding, and the landing is decided by the
// normal gate, as it always was.

// cleanRun is a clean-environment gate the stage may reuse: at is the commit
// whose tree it measured, "" until that commit is made.
type cleanRun struct {
	gate GateResult
	env  *checks.Environment
	at   string
}

// landingGate is the gate that decides whether a change lands. For the run's
// last change after a green gate, it runs in the clean environment first and
// keeps that run for the conformance stage; otherwise, and whenever the
// clean run fails, it is the normal gate.
func (st *RunState) landingGate(ctx context.Context, o Options, label string, last bool) GateResult {
	st.clean = nil
	if !last || len(st.gate) == 0 || !st.baseline.OK() {
		return st.runGate(ctx, o, label)
	}
	g, env := hermeticGate(ctx, o, st)
	if g == nil {
		return st.runGate(ctx, o, label)
	}
	if g.aborted() {
		return *g
	}
	st.clean = &cleanRun{gate: *g, env: env}
	if g.OK() {
		return *g
	}
	return st.runGate(ctx, o, label)
}

// landedClean ties the kept clean run to the commit that carries the tree it
// measured. Anything else — a repair on top of the task's work — leaves it
// tied to nothing, and the stage runs its own.
func (st *RunState) landedClean(commit string, sameTree bool) {
	if st.clean == nil {
		return
	}
	if !sameTree {
		st.clean = nil
		return
	}
	st.clean.at = commit
}

// reusableClean is the kept clean run when the tree is still the one it
// measured: HEAD is its commit and nothing has changed since.
func (st *RunState) reusableClean(ctx context.Context) (*GateResult, *checks.Environment, bool) {
	if st.clean == nil || st.clean.at == "" {
		return nil, nil, false
	}
	head, err := st.git.Head(ctx)
	if err != nil || head != st.clean.at {
		return nil, nil, false
	}
	if dirty, err := st.dirtyAfterCommit(ctx); err != nil || len(dirty) > 0 {
		return nil, nil, false
	}
	g := st.clean.gate
	return &g, st.clean.env, true
}
