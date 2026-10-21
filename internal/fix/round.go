package fix

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
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
	// Placed notes the files that the round puts on a checkout that is the box; nil notes none.
	Placed *Placed
	// DriverInBox says the brain is the driver of AgentKit, run as a process in the box. The
	// run directory of the box must have no fix.json then, which would make the af fix run
	// that the box executes the round of the host, with a box of its own.
	DriverInBox bool
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

// Placed are the files that af put on a checkout that is the box, for a run: the ones to take
// off it when the run ends, if they are as af wrote them. The checkout of a person is not to
// keep, as the person's own, a file that af made for the run.
type Placed struct{ files map[string][]byte }

func (p *Placed) note(rel string, data []byte) {
	if p == nil {
		return
	}
	if p.files == nil {
		p.files = map[string][]byte{}
	}
	p.files[rel] = data
}

// Remove takes the files off the checkout at dir, those of them that are as af wrote them: a
// file that someone has changed since is theirs, and stays.
func (p *Placed) Remove(dir string) {
	if p == nil {
		return
	}
	for rel, data := range p.files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
			_ = os.Remove(path) // a file that stays is a stale table at the next run, and nothing to stop this one for
		}
	}
	p.files = nil
}

// WriteBoxFiles places gate.json and guard.json in the box, so that a brain's own gate
// run and the hook use the host's table and rules, and not what the box would detect, and
// fix.json in the run directory of the box, so that a brain with hands of its own can run
// af fix submit there, which refuses a run directory that is no fix run.
//
// A box that is the checkout (the none provider) has gate.json and guard.json of the
// person's own, perhaps: the run's are placed only where there are none, and those that
// are there are left as they are. The driver of AgentKit that runs in the box has no
// fix.json (see RoundParams.DriverInBox).
func WriteBoxFiles(ctx context.Context, sess *box.Session, s State, driverInBox bool, placed *Placed) error {
	for _, f := range []struct {
		rel   string
		make  func(State) ([]byte, error)
		state bool
	}{
		{boxGatePath, GateFile, false}, {boxGuardPath, GuardFile, false},
		{path.Join(".agent-fox", "run", s.RunID, StateFile), State.Marshal, true},
	} {
		if f.state && driverInBox {
			continue
		}
		if sess.InPlace() && !f.state {
			if _, err := fs.Stat(sess.FS(ctx), f.rel); err == nil {
				continue
			}
		}
		data, err := f.make(s)
		if err != nil {
			return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("cannot make %s: %v", f.rel, err)}
		}
		if err := sess.WriteFile(ctx, f.rel, data); err != nil {
			return err
		}
		if sess.InPlace() && !f.state {
			placed.note(f.rel, data)
		}
	}
	return nil
}

// RunRound runs one round: the files of the box are placed, the brain runs on the brief of
// the run, and the tree of an accepted report is exported and applied to the checkout. A
// round that ends without an accepted submission applies nothing, and neither does one
// that asks a question. Where the box is the checkout, nothing is exported or applied:
// the brain has worked on the tree that is measured next, and the commits it made on the
// branch are taken off it first (see Checkout.Check), or the run is stopped.
func RunRound(ctx context.Context, p RoundParams) (RoundResult, error) {
	if err := WriteBoxFiles(ctx, p.Session, p.State, p.DriverInBox, p.Placed); err != nil {
		return RoundResult{}, err
	}
	var tools []string
	for _, tool := range p.Session.Tools() {
		tools = append(tools, tool.Name)
	}
	// The brain of a box that is the checkout works on the checkout itself, so what it
	// commits is on the branch; where the checkout stands is recorded before it runs.
	inPlace := p.Session.InPlace()
	var before Checkout
	if inPlace {
		var err error
		if before, err = RecordCheckout(ctx, p.Git); err != nil {
			return RoundResult{}, err
		}
	}
	res, err := brain.Execute(ctx, brain.Request{
		Session: p.Session, Name: p.Name, Brain: p.Brain, RunDir: p.RunDir, Report: p.Report, Tally: p.Tally,
		Brief: NewBrief(BriefParams{
			RunID: p.RunDir.ID, Prefix: p.Prefix, Task: p.Task, Round: p.Round, ToolNames: tools, Programs: p.State.Programs,
			MaxCostUSD: p.MaxCostUSD, MaxTurns: p.MaxTurns, Timeout: p.Timeout,
		}),
	})
	if inPlace {
		// Before anything is measured, and whether or not the brain came to a report. A run
		// that is stopped still gets its checkout back: what the brain committed is not for the
		// branch of the person, so this does not wait on the context that the stop has reached.
		checkCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ParkTimeout)
		_, cerr := before.Check(checkCtx, ForeignParams{Git: p.Git, Warn: warnOf(p.Report), Record: p.RunDir.Record})
		cancel()
		if cerr != nil {
			return RoundResult{}, cerr
		}
	}
	if err != nil {
		return RoundResult{}, err
	}
	out := RoundResult{Result: res}
	if out.Submission, err = Decode(res.Payload); err != nil {
		return out, &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	if out.Submission.Question != nil || inPlace {
		return out, nil // the tree of a box that is the checkout is the checkout: nothing to export or apply
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
