package fix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// BoxMeasurer measures a fix in the box that holds its tree, with the ring-1 commands of the af
// of the box (its image's, or the running one under local), in place of the host's gate and
// revert check: the tests of a change are written by a brain, and they run where its tools
// did, never on the host that holds the credentials. For each of detection, gate and revert check it syncs the host's tree into the box,
// places the table of the run there, runs the command and reads the answer from the envelope
// the command wrote. Detect, Gate and RevertCheck are the seams of Preflight, LoopParams and
// MeasureParams.
//
// The first command of a box is preceded by af --version, once for the box whichever runner of
// processes asks: an af that is missing or fails is a failure that cannot be tried again, naming
// the profile or the image that has to provide it; an af of another version raises
// box_af_version_mismatch and the run goes on. A box that cannot be reached is a failure that
// can. An envelope of another major version of the schema, one that is not an envelope, and a
// result that does not decode are the failure of an af that is not the one this run needs.
type BoxMeasurer struct {
	// Session is the box that serves the whole attempt.
	Session *box.Session
	// Process runs the commands; the version and commit of its config are those of the af
	// that is running, for the comparison with the box's.
	Process *brain.Process
	// Profile names the sandbox profile whose image provides af in a leased box; empty is
	// hub's default. Another box is named by its provider and its image.
	Profile string
	// RunID names the run directory of the box that the answers of the commands are written in.
	RunID string
	// Record notes what happened in the run's ledger, Warn raises a warning of the run and
	// Artifact lists one.
	Record   func(kind string, data any) error
	Warn     func(code, message string)
	Artifact func(toolio.Artifact)

	mu    sync.Mutex
	calls int
}

// gateFilePath is where the table of the run goes in the box.
const gateFilePath = ".agent-fox/gate.json"

// maxMirrored bounds what is copied from the box of the results of a gate: the files of one
// result and their size.
const (
	maxMirroredFiles = 64
	maxMirroredBytes = 4 << 20
)

// Detect is the detection of the checks of the tree, made in the box by af lang detect.
func (m *BoxMeasurer) Detect(ctx context.Context, dir string, files []string) (project.Detection, error) {
	// The gate.json of the checkout is in .agent-fox/, which the sync leaves out: it is placed
	// as it is, so that the box detects what the host would.
	own, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(gateFilePath)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		own = nil
	case err != nil:
		return project.Detection{}, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read %s: %v", gateFilePath, err)}
	case !utf8.Valid(own):
		return project.Detection{}, &toolio.Failure{Category: toolio.CategoryConfig, Message: fmt.Sprintf("%s is not text (UTF-8), and cannot be the table of the gate", gateFilePath)}
	}
	var detection project.Detection
	if _, err := m.run(ctx, own, []string{"lang", "detect"}, &detection); err != nil {
		return project.Detection{}, err
	}
	return detection, nil
}

// Gate runs the checks of the table in the box with af gate run, and mirrors the result onto
// the host, where the comparison with the baseline reads it.
func (m *BoxMeasurer) Gate(ctx context.Context, dir string, o checks.Options) (checks.Result, error) {
	args := []string{"gate", "run"}
	if o.Hermetic {
		args = append(args, "--hermetic")
	}
	if o.NoCache {
		args = append(args, "--no-cache")
	}
	if o.Label != "" {
		args = append(args, "--label", o.Label)
	}
	var res checks.Result
	gate, err := tableFile(o.Table)
	if err != nil {
		return checks.Result{}, err
	}
	env, err := m.run(ctx, gate, args, &res)
	if err != nil {
		return checks.Result{}, err
	}
	for _, w := range env.Warnings {
		if w.Code == WarnTreeChangedDuringGate {
			res.TreeChanged = true
		}
	}
	if res.Checks == nil {
		res.Checks = []checks.CheckResult{}
	}
	if res.Skipped == nil {
		res.Skipped = []checks.Skipped{}
	}
	if err := m.mirror(ctx, dir, res.ID); err != nil {
		return checks.Result{}, err
	}
	return res, nil
}

// RevertCheck asks the box whether the tests of the change fail without the rest of it, with
// af revert-check.
func (m *BoxMeasurer) RevertCheck(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error) {
	var res conform.Result
	gate, err := tableFile(o.Table)
	if err != nil {
		return conform.Result{}, err
	}
	if _, err := m.run(ctx, gate, []string{"revert-check", "--since", since}, &res); err != nil {
		return conform.Result{}, err
	}
	return res, nil
}

// answer is the part of an envelope that the measuring reads.
type answer struct {
	SchemaVersion string          `json:"schema_version"`
	Status        string          `json:"status"`
	Result        json.RawMessage `json:"result"`
	Warnings      []struct {
		Code string `json:"code"`
	} `json:"warnings"`
	Error *struct {
		Category  string `json:"category"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	} `json:"error"`
}

// tableFile is the gate.json of a table, or nil for a table with no check.
func tableFile(table project.Table) ([]byte, error) {
	if len(table.Checks) == 0 {
		return nil, nil
	}
	data, err := project.MarshalGateFile(table.Checks)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("cannot make the table of the gate: %v", err)}
	}
	return data, nil
}

// run runs one ring-1 command in the box, on the host's tree and with gate, when it is not
// nil, placed as the gate.json of the box, and decodes the result of its envelope into into.
func (m *BoxMeasurer) run(ctx context.Context, gate []byte, args []string, into any) (answer, error) {
	m.mu.Lock()
	m.calls++
	n := m.calls
	m.mu.Unlock()

	if err := m.Session.Sync(ctx); err != nil {
		return answer{}, err
	}
	if gate != nil {
		if err := m.Session.WriteFile(ctx, gateFilePath, gate); err != nil {
			return answer{}, err
		}
	}
	// The file the answer is written to is made first, which makes the directory it is in.
	spec := brain.ProcessSpec{Brain: ring1Brain, Args: args}
	if err := m.Session.WriteFile(ctx, path.Join(".agent-fox", "run", m.RunID, fmt.Sprintf("%s.%d.json", ring1Brain, n)), nil); err != nil {
		return answer{}, err
	}
	brief := brain.Brief{RunID: m.RunID, Attempt: n, Box: m.Session, Warn: m.Warn, Artifact: m.Artifact}
	if m.Record != nil {
		brief.Record = func(kind string, data any) { _ = m.Record(kind, data) } // the ledger warns of itself when it cannot be written
	}
	res, err := m.Process.Run(ctx, brief, spec)
	if err != nil {
		return answer{}, m.explain(err)
	}
	if _, ok := ctx.Deadline(); ok && res.TimedOut {
		// A measuring has no time of its own: the process was stopped a little before the
		// deadline of the run, which is what stops the run. The rest of the time is waited
		// out, so that the run is told of its stop as it is anywhere else.
		<-ctx.Done()
		return answer{}, ctx.Err()
	}
	env, err := m.decode(res, args, into)
	if err != nil {
		// An answer that cannot be read is an af that cannot be used, unless the box is gone: the
		// command may have run, and the box been lost before its envelope was read.
		if lost := m.lost(ctx); lost != nil {
			return answer{}, lost
		}
	}
	return env, err
}

// boxProbeTimeout bounds the question whether the box is still there.
const boxProbeTimeout = 10 * time.Second

// lost is the failure of a box that can no longer be asked anything, which may be tried again, or
// nil while it answers or when the run was stopped.
func (m *BoxMeasurer) lost(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	probe, cancel := context.WithTimeout(context.WithoutCancel(ctx), boxProbeTimeout)
	defer cancel()
	// The root of the view is not asked of the box; a path under it is, and a path that is not
	// there is an answer.
	if _, err := fs.Stat(m.Session.FS(probe), agentdir.Name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("%s was lost while af ran in it: %v", m.where(), err)}
	}
	return nil
}

// ring1Brain names the files of the answers of the ring-1 commands: the runner of processes
// names them after the brain it runs for.
const ring1Brain = "ring1"

// explain is the failure for an error of running a command: a box that could not be asked is
// as it was (it can be tried again); an af that cannot run in it is a failure of the image of
// the profile; and the error of a context is as it was.
func (m *BoxMeasurer) explain(err error) error {
	var failure *toolio.Failure
	if !errors.As(err, &failure) || failure.Category != toolio.CategorySandbox || failure.Retryable {
		return err
	}
	return &toolio.Failure{
		Category: toolio.CategorySandbox,
		Message:  fmt.Sprintf("af cannot run in %s: its image must provide af (%s)", m.where(), failure.Message),
		FixHint:  m.hint(),
	}
}

// where names the box in a message: a leased box by the profile its image is of, and any
// other by its provider and its image.
func (m *BoxMeasurer) where() string {
	switch provider, image := m.Session.Provider(), m.Session.Image(); {
	case provider == box.GatewayName && m.Profile == "":
		return "the box of hub's default profile"
	case provider == box.GatewayName:
		return "the box of the profile " + m.Profile
	case image != "":
		return fmt.Sprintf("the %s box of the image %s", provider, image)
	default:
		return fmt.Sprintf("the %s box", provider)
	}
}

// hint is what a failure of the af of the box says to change: the profile of a leased box,
// the image of another one that has an image, and nothing for a box with none.
func (m *BoxMeasurer) hint() *toolio.FixHint {
	switch {
	case m.Session.Provider() == box.GatewayName:
		return &toolio.FixHint{Env: "AF_SANDBOX_PROFILE"}
	case m.Session.Image() != "":
		return &toolio.FixHint{Env: "AF_SANDBOX_IMAGE"}
	}
	return nil
}

// unusable is the failure of an af whose answer is none this run can use.
func (m *BoxMeasurer) unusable(args []string, why string) error {
	return &toolio.Failure{
		Category: toolio.CategorySandbox,
		Message:  fmt.Sprintf("the af of %s answered af %s with something this af cannot use (%s): its image must provide an af of this version", m.where(), strings.Join(args, " "), why),
		FixHint:  m.hint(),
	}
}

// decode reads the envelope a command left and its result.
func (m *BoxMeasurer) decode(res brain.ProcessResult, args []string, into any) (answer, error) {
	var env answer
	if len(res.Output) == 0 || json.Unmarshal(res.Output, &env) != nil || env.Status == "" {
		why := "it left no envelope that can be read"
		if tail := strings.TrimSpace(string(res.Stderr)); tail != "" {
			why += ": " + tail
		}
		return answer{}, m.unusable(args, why)
	}
	if major(env.SchemaVersion) != major(toolio.SchemaVersion) {
		return answer{}, m.unusable(args, fmt.Sprintf("its envelope is of schema %s, and this af speaks %s", env.SchemaVersion, toolio.SchemaVersion))
	}
	if toolio.Status(env.Status) != toolio.StatusDone {
		if env.Error == nil {
			return answer{}, m.unusable(args, "its envelope says "+env.Status+" and no error")
		}
		return answer{}, &toolio.Failure{Category: env.Error.Category, Message: env.Error.Message, Retryable: env.Error.Retryable}
	}
	if len(env.Result) == 0 || string(env.Result) == "null" {
		return answer{}, m.unusable(args, "its envelope has no result")
	}
	if err := json.Unmarshal(env.Result, into); err != nil {
		return answer{}, m.unusable(args, "its result does not decode: "+err.Error())
	}
	return env, nil
}

// major is the major version of a schema version, and -1 for one that has none.
func major(version string) int {
	first, _, _ := strings.Cut(version, ".")
	n, err := strconv.Atoi(first)
	if err != nil {
		return -1
	}
	return n
}

// mirror copies the stored result of a gate run, and its logs, from the box to the same
// place in dir, so that the comparison of two results finds them on the host.
func (m *BoxMeasurer) mirror(ctx context.Context, dir, id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return m.unusable([]string{"gate", "run"}, fmt.Sprintf("its result has the id %q", id))
	}
	rel := path.Join(agentdir.Name, "gate", id)
	fsys := m.Session.FS(ctx)
	entries, err := fs.ReadDir(fsys, rel)
	if err != nil {
		return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("the result %s of the gate cannot be read out of the box: %v", id, err)}
	}
	keep := filepath.Join(dir, filepath.FromSlash(rel))
	if _, err := agentdir.Ensure(dir); err != nil {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	if err := os.MkdirAll(keep, 0o755); err != nil {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot keep the result %s of the gate: %v", id, err)}
	}
	copied := 0
	for _, e := range entries {
		// A file that is still being written, under a temporary name, is not the result's.
		if e.IsDir() || copied >= maxMirroredFiles || !fs.ValidPath(e.Name()) || strings.HasPrefix(e.Name(), ".tmp") {
			continue
		}
		f, err := fsys.Open(path.Join(rel, e.Name()))
		if err != nil {
			return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("%s cannot be read out of the box: %v", path.Join(rel, e.Name()), err)}
		}
		data, err := io.ReadAll(io.LimitReader(f, maxMirroredBytes+1))
		_ = f.Close() // read only
		if err != nil {
			return &toolio.Failure{Category: toolio.CategorySandbox, Retryable: true, Message: fmt.Sprintf("%s cannot be read out of the box: %v", path.Join(rel, e.Name()), err)}
		}
		if len(data) > maxMirroredBytes {
			data = data[:maxMirroredBytes]
		}
		if err := agentdir.WriteFileAtomic(filepath.Join(keep, e.Name()), data); err != nil {
			return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot keep the result %s of the gate: %v", id, err)}
		}
		copied++
	}
	return nil
}
