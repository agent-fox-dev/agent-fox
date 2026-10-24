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
	"regexp"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
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
	// Nonce, when it is not empty, is the nonce of the run instead of one drawn from
	// Rand: the one a process that runs inside the box was given with the run.json
	// that an earlier Execute placed, since a submission with another nonce is not
	// this attempt's.
	Nonce string
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
//
// A run is one Execute at a time in a box: two that share a box and a run id
// would place the same files. The brain must honour its context; Execute waits
// for it to return. The brief's timeout counts from the start of Execute, so
// that placing the brief is part of it.
func Execute(ctx context.Context, req Request) (Result, error) {
	started := time.Now()
	r, err := newRun(req, started)
	if err != nil {
		return Result{}, err
	}
	req = r.req

	req.Report.Emit("step", stepEvent{Name: "brief"})
	r.clearHostSubmission() // one of an earlier attempt is not this attempt's
	if err := r.place(ctx); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err() // a box that could not be written to because the run was cancelled
		}
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
	outcome, panicked, runErr := r.runBrain(runCtx, b)
	stopBeats()
	runCtxErr := runCtx.Err() // whether the brief's own deadline passed, before it is cancelled
	cancel()

	r.record("brain_end", map[string]any{
		"brain": req.Name, "stop_reason": outcome.StopReason, "submitted_claim": outcome.Submitted, "cost_usd": outcome.CostUSD,
		"cost_source": outcome.CostSource, "turns": outcome.Turns, "duration_ms": time.Since(started).Milliseconds(),
		"input_tokens": outcome.InputTokens, "output_tokens": outcome.OutputTokens,
	})
	// The cost was spent, whether or not the brain finished.
	req.Tally.Spend(outcome, req.Name, req.Report.SetUsage)

	var panicErr error
	if panicked != nil {
		panicErr = r.panicFailure(panicked)
	}
	req.Report.Emit("step", stepEvent{Name: "submission"})
	return r.decide(ctx, outcome, runCtxErr, runErr, panicErr)
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
	if req.Nonce != "" {
		if !ValidNonce(req.Nonce) {
			return nil, fmt.Errorf("brain.Execute: the nonce to adopt, %q, is not 8 to 64 lower-case hex digits", req.Nonce)
		}
		return &run{req: req, brief: b, nonce: req.Nonce, started: started}, nil
	}
	var nonce [8]byte
	if _, err := io.ReadFull(req.Rand, nonce[:]); err != nil {
		return nil, fmt.Errorf("brain.Execute: cannot draw a nonce: %w", err)
	}
	return &run{req: req, brief: b, nonce: hex.EncodeToString(nonce[:]), started: started}, nil
}

// adoptable is what a nonce taken from a run.json that is already placed looks like.
var adoptable = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// ValidNonce says whether s can be the nonce of a run: the 16 hex digits that Execute
// draws, or another run.json's, which are 8 to 64 lower-case hex digits.
func ValidNonce(s string) bool { return adoptable.MatchString(s) }

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
	r.brief.Hook = hook // the brain is given the hook that was placed
	hookJSON, err := json.MarshalIndent(hook, "", "  ")
	if err != nil {
		return err
	}
	hookJSON = append(hookJSON, '\n')
	runJSON, err := json.MarshalIndent(submission.Run{RunID: b.RunID, Kind: b.Kind.Name, Attempt: b.Attempt, Nonce: r.nonce, PrefixBytes: len(b.Prefix)}, "", "  ")
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
	r.brief.Warn = req.Report.Warn
	r.brief.RunDir = req.RunDir.Path
	r.brief.Record = r.record
	r.brief.Artifact = req.Report.Artifact
	observe := b.Observe
	r.brief.Observe = func(c ToolCall) {
		req.Report.Emit("tool_call", toolCallEvent(c))
		if observe != nil {
			observe(c)
		}
	}
	return nil
}

// agentDir is where af keeps its files in the tree, and protectedAgentDir the
// way a protected path names it: as a directory.
const (
	agentDir          = agentdir.Name
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
				if !r.beat() {
					return
				}
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

// beat emits one heartbeat. A Report that panics ends the heartbeat and not the
// process: no one has a goroutine here to recover it for the caller.
func (r *run) beat() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	r.req.Report.Emit("heartbeat", map[string]any{"brain": r.req.Name, "elapsed_ms": time.Since(r.started).Milliseconds()})
	return true
}

// panicInfo is what a recovered panic leaves.
type panicInfo struct {
	value any
	stack []byte
}

// runBrain calls the brain, and recovers a panic of it.
func (r *run) runBrain(ctx context.Context, b Brief) (outcome Outcome, panicked *panicInfo, err error) {
	defer func() {
		if v := recover(); v != nil {
			panicked = &panicInfo{value: v, stack: debug.Stack()}
			outcome.StopReason = StopError
		}
	}()
	outcome, err = r.req.Brain.Run(ctx, b)
	return outcome, nil, err
}

// panicFailure writes the stack of a panic of the brain to the run directory and
// is the failure that says so, which a run that has no valid submission ends
// with.
func (r *run) panicFailure(p *panicInfo) error {
	name := fmt.Sprintf("brain-panic.%d.txt", r.brief.Attempt)
	text := fmt.Sprintf("the %s brain panicked: %v\n\n%s", r.req.Name, p.value, p.stack)
	where := name
	if err := agentdir.WriteFileAtomic(filepath.Join(r.req.RunDir.Path, name), []byte(text)); err != nil {
		where = fmt.Sprintf("%s (which could not be written: %v)", name, err)
	}
	return &toolio.Failure{Category: toolio.CategoryBrain, Message: fmt.Sprintf("the %s brain panicked: %v; the stack is in %s", r.req.Name, p.value, where)}
}
