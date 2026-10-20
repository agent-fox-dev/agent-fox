package brain

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The failure categories of a run that has no submission.
const (
	CategoryBrain    = "brain"
	CategoryNoResult = "no_result"
	CategoryBudget   = "budget"
)

// defaultHeartbeat is how often a run that is still going says so.
const defaultHeartbeat = 30 * time.Second

// Request is what Execute runs: one brief, through one brain, in a held box.
type Request struct {
	// Session is the held box, and Name the brain's name in the registry.
	Session *box.Session
	Name    string
	Brain   Brain
	Brief   Brief
	// RunDir is the run's directory on the host, which holds what the run
	// leaves; Report gets its events, warnings and artifacts, and its usage.
	RunDir *toolio.RunDir
	Report Report
	// Tally accumulates the cost of the attempts of the run.
	Tally *Tally
	// Heartbeat is how often the run says it is alive; 30 seconds when zero.
	Heartbeat time.Duration
	// Rand is where the nonce comes from; crypto/rand when nil.
	Rand io.Reader
}

// Result is what a run that submitted leaves.
type Result struct {
	Outcome Outcome
	// Payload is the payload of the submission, as the host validated it, and
	// Path the host file that holds the submission.
	Payload json.RawMessage
	Path    string
}

// run is what the steps of one Execute share.
type run struct {
	req     Request
	brief   Brief
	nonce   string
	started time.Time
	warned  sync.Once
}

// Execute runs one brief through one brain in a held box, and returns what the
// brain submitted. Before the brain runs it places the brief and the run's files
// in the box, and copies them to the run directory; while it runs it says so,
// and records its beginning and its end; whatever the brain does, a panic
// included, its cost is added to the tally and reported. Then the host decides,
// from the submission file alone, whether there is a submission (see decide).
func Execute(ctx context.Context, req Request) (Result, error) {
	started := time.Now()
	r, err := newRun(req, started)
	if err != nil {
		return Result{}, err
	}
	req = r.req

	req.Report.Emit("step", stepEvent{Name: "brief"})
	if err := r.place(ctx); err != nil {
		return Result{}, err
	}

	b := r.brief
	req.Report.Emit("step", stepEvent{Name: "brain", Brain: req.Name})
	r.record("brain_start", map[string]any{"brain": req.Name, "attempt": b.Attempt})
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if b.Timeout > 0 {
		runCtx, cancel = context.WithDeadline(ctx, started.Add(b.Timeout))
	}
	stopBeats := r.heartbeat()
	outcome, runErr, panicked := r.runBrain(runCtx, b)
	stopBeats()
	cancel()

	r.record("brain_end", map[string]any{
		"brain": req.Name, "stop_reason": outcome.StopReason, "submitted_claim": outcome.Submitted, "cost_usd": outcome.CostUSD,
		"cost_source": outcome.CostSource, "turns": outcome.Turns, "duration_ms": time.Since(started).Milliseconds(),
	})
	// The cost was spent, whether or not the brain finished.
	req.Tally.Add(outcome)
	req.Report.SetUsage(req.Tally.Usage(req.Name))

	if panicked != nil {
		return Result{Outcome: outcome}, r.panicFailure(panicked)
	}
	req.Report.Emit("step", stepEvent{Name: "submission"})
	return decide(ctx, r, outcome, runErr)
}

// newRun checks the request and fills in what it leaves out.
func newRun(req Request, started time.Time) (*run, error) {
	switch {
	case req.Session == nil:
		return nil, errors.New("brain.Execute: there is no box session")
	case req.Brain == nil:
		return nil, errors.New("brain.Execute: there is no brain")
	case req.RunDir == nil:
		return nil, errors.New("brain.Execute: there is no run directory")
	}
	if req.Report == nil {
		req.Report = discardReport{}
	}
	if req.Tally == nil {
		req.Tally = &Tally{}
	}
	if req.Heartbeat <= 0 {
		req.Heartbeat = defaultHeartbeat
	}
	if req.Rand == nil {
		req.Rand = rand.Reader
	}
	b := req.Brief
	if b.RunID == "" {
		b.RunID = req.RunDir.ID
	}
	if b.RunID != req.RunDir.ID {
		return nil, fmt.Errorf("brain.Execute: the brief is for run %s, and the run directory is %s", b.RunID, req.RunDir.ID)
	}
	if b.Attempt < 1 {
		return nil, fmt.Errorf("brain.Execute: attempt %d, and attempts count from 1", b.Attempt)
	}
	b.Box = req.Session
	var nonce [8]byte
	if _, err := io.ReadFull(req.Rand, nonce[:]); err != nil {
		return nil, fmt.Errorf("brain.Execute: cannot draw a nonce: %w", err)
	}
	return &run{req: req, brief: b, nonce: hex.EncodeToString(nonce[:]), started: started}, nil
}

// discardReport is a Report for a caller that has none.
type discardReport struct{}

func (discardReport) Emit(string, any)         {}
func (discardReport) Warn(string, string)      {}
func (discardReport) Artifact(toolio.Artifact) {}
func (discardReport) SetUsage(toolio.Usage)    {}

// stepEvent is the data of a step event: the stage the run is in.
type stepEvent struct {
	Name  string `json:"name"`
	Brain string `json:"brain,omitempty"`
}

// toolCallEvent is the data of a tool_call event. It carries what was decided,
// and no argument and no result: events go to audit, and arguments can hold the
// content of files.
type toolCallEvent struct {
	Brain      string `json:"brain"`
	Tool       string `json:"tool"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
}

// record notes an entry in the ledger. A ledger that cannot be written is a
// warning, once, and does not stop the run.
func (r *run) record(kind string, data any) {
	if err := r.req.RunDir.Record(kind, data); err != nil {
		r.warned.Do(func() {
			r.req.Report.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", kind, err))
		})
	}
}

// place puts the brief and the run's files in the box, and copies of them in
// the run directory, before the brain runs.
func (r *run) place(ctx context.Context) error {
	b, req := r.brief, r.req
	hook := b.Hook
	if !slices.Contains(hook.ProtectedPaths, protectedAgentDir) {
		hook.ProtectedPaths = append(slices.Clone(hook.ProtectedPaths), protectedAgentDir)
	}
	if hook.AllowPrograms == nil {
		hook.AllowPrograms = []string{}
	}
	hookJSON, err := json.MarshalIndent(hook, "", "  ")
	if err != nil {
		return err
	}
	hookJSON = append(hookJSON, '\n')
	runJSON, err := json.MarshalIndent(submission.Run{RunID: b.RunID, Kind: b.Kind.Name, Attempt: b.Attempt, Nonce: r.nonce}, "", "  ")
	if err != nil {
		return err
	}
	runJSON = append(runJSON, '\n')

	dir := path.Join(agentDir, "run", b.RunID)
	for _, f := range []struct {
		rel  string
		data []byte
	}{
		{path.Join(agentDir, ".gitignore"), []byte("*")},
		{path.Join(dir, "brief.md"), []byte(b.Document())},
		{submission.RunPath(b.RunID), runJSON},
		{path.Join(dir, "hook.json"), hookJSON},
	} {
		if err := req.Session.WriteFile(ctx, f.rel, f.data); err != nil {
			return err
		}
	}

	for name, data := range map[string][]byte{fmt.Sprintf("brief.%d.md", b.Attempt): []byte(b.Document()), "hook.json": hookJSON} {
		if err := agentdir.WriteFileAtomic(filepath.Join(req.RunDir.Path, name), data); err != nil {
			return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s in the run directory: %v", name, err)}
		}
	}
	r.record("brief_placed", map[string]any{
		"attempt": b.Attempt, "bytes": len(b.Document()), "sha256": sum(b.Document()), "prefix_sha256": sum(b.Prefix),
	})

	r.brief.TranscriptPath = filepath.Join(req.RunDir.Path, fmt.Sprintf("transcript.%d.jsonl", b.Attempt))
	observe := b.Observe
	r.brief.Observe = func(c ToolCall) {
		req.Report.Emit("tool_call", toolCallEvent{Brain: c.Brain, Tool: c.Tool, Decision: c.Decision, Reason: c.Reason, OK: c.OK, DurationMS: c.DurationMS})
		if observe != nil {
			observe(c)
		}
	}
	return nil
}

// agentDir is where af keeps its files in the tree, and protectedAgentDir the
// way a protected path names it: as a directory.
const (
	agentDir          = ".agent-fox"
	protectedAgentDir = agentDir + "/"
)

// sum is the hex SHA-256 of a string.
func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// heartbeat says, every Request.Heartbeat, that the brain is still running, until
// the function it returns is called; no event comes after that.
func (r *run) heartbeat() (stop func()) {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(r.req.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				r.req.Report.Emit("heartbeat", map[string]any{"brain": r.req.Name, "elapsed_ms": time.Since(r.started).Milliseconds()})
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// panicInfo is what a recovered panic leaves.
type panicInfo struct {
	value any
	stack []byte
}

// runBrain calls the brain, and recovers a panic of it.
func (r *run) runBrain(ctx context.Context, b Brief) (outcome Outcome, err error, panicked *panicInfo) {
	defer func() {
		if v := recover(); v != nil {
			panicked = &panicInfo{value: v, stack: debug.Stack()}
			outcome.StopReason = StopError
		}
	}()
	outcome, err = r.req.Brain.Run(ctx, b)
	return outcome, err, nil
}

// panicFailure writes the stack of a panic of the brain to the run directory and
// is the failure that says so.
func (r *run) panicFailure(p *panicInfo) error {
	name := fmt.Sprintf("brain-panic.%d.txt", r.brief.Attempt)
	text := fmt.Sprintf("the %s brain panicked: %v\n\n%s", r.req.Name, p.value, p.stack)
	where := name
	if err := agentdir.WriteFileAtomic(filepath.Join(r.req.RunDir.Path, name), []byte(text)); err != nil {
		where = fmt.Sprintf("%s (which could not be written: %v)", name, err)
	}
	return &toolio.Failure{Category: CategoryBrain, Message: fmt.Sprintf("the %s brain panicked: %v; the stack is in %s", r.req.Name, p.value, where)}
}

// decide is the host's decision, after the brain, whether a submission exists
// and what the run came to. Until it reads the submission file, a run has none.
func decide(ctx context.Context, r *run, outcome Outcome, runErr error) (Result, error) {
	return Result{Outcome: outcome}, &toolio.Failure{Category: CategoryNoResult, Retryable: true, Message: "the run has no submission"}
}
