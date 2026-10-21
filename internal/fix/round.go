package fix

import (
	"context"
	"fmt"
	"path"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/policy"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// RoundParams is one round of a brain over a held box.
type RoundParams struct {
	// Dir and Git are the checkout, and Session the box that holds a copy of it.
	Dir     string
	Git     *gitx.Git
	Session *box.Session
	// Name and Brain are the brain, and Tally what the rounds of the run cost.
	Name  string
	Brain brain.Brain
	Tally *brain.Tally
	// RunDir is the run's directory, and Report gets its events and warnings.
	RunDir *toolio.RunDir
	Report brain.Report
	// State is fix.json, and Prefix and Task the brief of the round.
	State        State
	Prefix, Task string
	// Round is the number of the round in the run, from 1.
	Round      int
	MaxCostUSD float64
	MaxTurns   int
	Timeout    time.Duration
}

// RoundResult is what a round left.
type RoundResult struct {
	// Result is the submission the host accepted, and Submission it decoded.
	Result     brain.Result
	Submission Submission
	// Apply is what was applied to the checkout: nothing for a round that asked a question.
	Apply *ApplyResult
}

// The files of the box that the host writes before a round.
const (
	boxGatePath  = ".agent-fox/gate.json"
	boxGuardPath = ".agent-fox/guard.json"
)

// GateFile is the content of the gate.json that a brain's own `af gate run` reads in the
// box: the table the run is measured by, as it was pinned.
func GateFile(s State) ([]byte, error) { return project.MarshalGateFile(s.Table.Checks) }

// GuardFile is the content of the guard.json that the hook in the box reads: the suite
// of the gate, the default protected paths, and the programs a brain may run.
func GuardFile(s State) ([]byte, error) {
	return policy.MarshalGuardFile(policy.Policy{Suite: s.Suite, ProtectedPaths: policy.DefaultProtectedPaths(), AllowPrograms: AllowPrograms(s.Programs)})
}

// WriteBoxFiles places gate.json and guard.json in the box, so that a brain's own gate
// run and the hook use the host's table and rules, and not what the box would detect, and
// fix.json in the run directory of the box, so that a brain with hands of its own can run
// af fix submit there, which refuses a run directory that is no fix run.
func WriteBoxFiles(ctx context.Context, sess *box.Session, s State) error {
	for _, f := range []struct {
		rel  string
		make func(State) ([]byte, error)
	}{
		{boxGatePath, GateFile}, {boxGuardPath, GuardFile},
		{path.Join(".agent-fox", "run", s.RunID, StateFile), State.Marshal},
	} {
		data, err := f.make(s)
		if err != nil {
			return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("cannot make %s: %v", f.rel, err)}
		}
		if err := sess.WriteFile(ctx, f.rel, data); err != nil {
			return err
		}
	}
	return nil
}

// RunRound runs one round: the files of the box are placed, the brain runs on the brief of
// the run, and the tree of an accepted report is exported and applied to the checkout. A
// round that ends without an accepted submission applies nothing, and neither does one
// that asks a question.
func RunRound(ctx context.Context, p RoundParams) (RoundResult, error) {
	if err := WriteBoxFiles(ctx, p.Session, p.State); err != nil {
		return RoundResult{}, err
	}
	var tools []string
	for _, tool := range p.Session.Tools() {
		tools = append(tools, tool.Name)
	}
	res, err := brain.Execute(ctx, brain.Request{
		Session: p.Session, Name: p.Name, Brain: p.Brain, RunDir: p.RunDir, Report: p.Report, Tally: p.Tally,
		Brief: NewBrief(BriefParams{
			RunID: p.RunDir.ID, Prefix: p.Prefix, Task: p.Task, Round: p.Round, ToolNames: tools, Programs: p.State.Programs,
			MaxCostUSD: p.MaxCostUSD, MaxTurns: p.MaxTurns, Timeout: p.Timeout,
		}),
	})
	if err != nil {
		return RoundResult{}, err
	}
	out := RoundResult{Result: res}
	if out.Submission, err = Decode(res.Payload); err != nil {
		return out, &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	if out.Submission.Question != nil {
		return out, nil
	}
	export, err := p.Session.Export(ctx)
	if err != nil {
		return out, err
	}
	applied, err := Apply(ctx, ApplyParams{Dir: p.Dir, Export: export, Git: p.Git, Record: p.RunDir.Record, Warn: warnOf(p.Report)})
	if err != nil {
		return out, err
	}
	out.Apply = &applied
	return out, nil
}

// warnOf is the Warn of a report that may be nil.
func warnOf(r brain.Report) func(code, message string) {
	if r == nil {
		return nil
	}
	return r.Warn
}
