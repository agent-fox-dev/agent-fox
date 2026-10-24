package afcmd

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// registerTriage registers af triage and its subcommands.
func registerTriage(app *toolio.App, c *Config) {
	registerTriageLoop(app, c)
	registerTriageRun(app, c)
}

// The ledger kinds of a triage run.
const (
	ledgerInput      = "triage.input"
	ledgerBrief      = "triage.brief"
	ledgerSubmission = "triage.submission"
	ledgerQuestion   = triage.LedgerQuestion
	ledgerLand       = "triage.land"
)

// triageReport is the input of a run as the triage package takes it.
func triageReport(in toolio.Input) triage.Report {
	r := triage.Report{Kind: string(in.Kind), Origin: in.Origin, Body: in.Body}
	if in.Kind == toolio.KindIssue && in.Issue != nil && in.Thread != nil {
		r.Ref, r.Thread = *in.Issue, *in.Thread
		r.Body = ""
	}
	return r
}

// triageFacts are the facts of a run that triage.json keeps, before the brief is
// written: what was triaged, and the repository and the issue it concerns.
func triageFacts(r *toolio.Run, report triage.Report) triage.Facts {
	facts := triage.Facts{Input: triage.InputFact{Kind: report.Kind, Origin: report.Origin, Fingerprint: triage.Fingerprint(report)}}
	switch {
	case report.Kind == triage.KindIssue:
		ref := report.Ref
		facts.Target = triage.RepoFact{Repo: ref.Repo.String(), Host: ref.Repo.Host}
		facts.Issue = &triage.IssueFact{Repo: ref.Repo.String(), Host: ref.Repo.Host, Number: ref.Number, URL: ref.URL()}
	case r.Target.Valid():
		facts.Target = triage.RepoFact{Repo: r.Target.String(), Host: r.Target.Host}
	}
	return facts
}

// record notes an entry in the ledger of the run. A ledger that cannot be written
// is a warning and does not stop the run.
func record(r *toolio.Run, kind string, data any) { recordIn(r, r.RunDir, kind, data) }

// recordIn notes an entry in the ledger of a run directory that the command
// opened, as the run directory of its own is not one.
func recordIn(r *toolio.Run, run *toolio.RunDir, kind string, data any) {
	if err := run.Record(kind, data); err != nil {
		r.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", kind, err))
	}
}

// refusalFailure is the shell's failure for a refusal of the triage package.
func refusalFailure(ref *triage.Refusal) *toolio.Failure {
	f := &toolio.Failure{Category: ref.Category, Message: ref.Message, Retryable: ref.Retryable}
	if ref.FixHintEnv != "" {
		f.FixHint = &toolio.FixHint{Env: ref.FixHintEnv}
	}
	return f
}

// renderedBrief is a brief that was rendered and written, and what it was
// rendered from.
type renderedBrief struct {
	brief    triage.Brief
	report   triage.Report
	facts    triage.Facts
	previous bool
}

// resolveInput is the report of the run and the facts known of it, and records
// triage.input: the input is resolved, whatever becomes of the run.
func resolveInput(r *toolio.Run) (triage.Report, triage.Facts) {
	report := triageReport(r.Input)
	facts := triageFacts(r, report)
	record(r, ledgerInput, facts.Input)
	return report, facts
}

// renderBrief renders the brief of the run for the report, writes brief.md,
// original.md and triage.json, records triage.brief, and lists the brief as an
// artifact. It refuses what triage does not take (the loop has refused it in its
// preflight already).
func renderBrief(c *Config, r *toolio.Run, report triage.Report, facts triage.Facts, bounds triage.Bounds) (renderedBrief, error) {
	warnings, refusal := triage.CheckInput(report)
	if refusal != nil {
		return renderedBrief{}, refusalFailure(refusal)
	}
	for _, w := range warnings {
		r.Warn(w.Code, w.Message)
	}

	var previous *triage.Question
	if len(r.Context) > 0 {
		previous = c.previousQuestion(r.Dir, facts.Input.Fingerprint, r.RunDir.ID, len(r.Context))
	}
	brief, err := triage.BuildBrief(r.Ctx, triage.BriefParams{
		Dir: r.Dir, Report: report, RunID: r.RunDir.ID, Context: r.Context, Material: c.materialFlags(len(r.Context)), PreviousQuestion: previous,
		Bounds: bounds, TruncationMarker: toolio.TruncationMarker,
	})
	if err != nil {
		if r.Ctx.Err() != nil {
			return renderedBrief{}, r.Ctx.Err()
		}
		return renderedBrief{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	for _, w := range brief.Warnings {
		r.Warn(w.Code, w.Message)
	}
	facts, err = triage.WriteRunFiles(r.RunDir.Path, brief, facts)
	if err != nil {
		return renderedBrief{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	path := filepath.Join(r.RunDir.Path, "brief.md")
	record(r, ledgerBrief, map[string]any{"brief": path, "prefix_bytes": facts.Prefix.Bytes, "prefix_sha256": facts.Prefix.SHA256, "document_bytes": len(brief.Document)})
	r.Artifact(toolio.Artifact{Kind: "brief", Path: path})
	return renderedBrief{brief: brief, report: report, facts: facts, previous: previous != nil}, nil
}

// triageRunResult is the result of af triage run.
type triageRunResult struct {
	RunID     string `json:"run_id"`
	Submitted bool   `json:"submitted"`
}

// registerTriageRun registers af triage run, which a box executes: the agentkit
// brain, in this process, on the brief that brain.Execute placed in the run directory
// of the box, over the directory it is in. It is no command for a person, and help
// does not list it.
func registerTriageRun(app *toolio.App, c *Config) {
	var runID string
	var budget float64
	var maxTurns int
	app.Register(toolio.Command{
		Path:    "triage run",
		Summary: "Run the agentkit brain in this process on the brief placed in a box",
		Hidden:  true,
		Usage:   "af triage run --run-id ID [--dir DIR] [--budget USD] [--max-turns N] [--timeout 15m]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&runID, "run-id", "", "the `id` of the run that was placed in the box")
			fs.Float64Var(&budget, "budget", triage.DefaultBudget, "stop the brain after this many `dollars` of model cost")
			fs.IntVar(&maxTurns, "max-turns", triage.DefaultMaxTurns, "stop the brain after this many `turns`")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: triage.DefaultTimeout,
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return triageRun(c, r, runID, budget, maxTurns) },
	})
}

// triageRun runs the agentkit brain over the directory, with no box: the tools
// work on --dir itself, as they do in the box that executes this process, and
// file_diagnosis submits into the run directory under it.
func triageRun(c *Config, r *toolio.Run, runID string, budget float64, maxTurns int) (toolio.Outcome, error) {
	if _, refusal := triage.CheckFlags(triage.Flags{Budget: budget, MaxTurns: maxTurns}); refusal != nil {
		return toolio.Outcome{}, refusalFailure(refusal)
	}
	if runID == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--run-id is required: the id of the run that was placed in the box"}
	}
	run, err := toolio.OpenRunDir(r.Dir, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	placed, ok, err := readPlaced(run, triage.KindName)
	if err != nil {
		return toolio.Outcome{}, err
	}
	if !ok {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no brief placed in it: af triage run runs only in the box of af triage", runID)}
	}
	prefix := placed.Prefix
	task := triage.AdaptTask(placed.Task, triage.Bounds{MaxTurns: maxTurns, MaxCostUSD: budget, Timeout: timeLeft(r)})
	// The run is run under the attempt and the nonce it was placed with, since the process
	// that placed it accepts a submission made under no other.
	attempt, nonce := placed.Run.Attempt, placed.Run.Nonce

	sess, err := box.Here(r.Ctx, box.Request{HostDir: r.Dir, RunID: runID, Tool: "triage", Record: run.Record, Warn: r.Warn})
	if err != nil {
		return toolio.Outcome{}, err
	}
	defer func() { _ = sess.Close() }()
	b, err := c.brains().New(brain.AgentkitName, c.brainOptions())
	if err != nil {
		return toolio.Outcome{}, err
	}
	var offered []string
	for _, tool := range sess.Tools() {
		offered = append(offered, tool.Name)
	}

	res, err := brain.Execute(r.Ctx, brain.Request{
		Session: sess, Name: brain.AgentkitName, Brain: b, RunDir: run, Report: r, Tally: &brain.Tally{}, Nonce: nonce,
		Brief: brain.Brief{
			RunID: runID, Attempt: attempt, Kind: triage.Kind(r.Dir), Prefix: prefix, Task: task,
			Tools: triage.BoxTools(offered), Hook: brain.HookConfig{RunID: runID},
			MaxCostUSD: budget, MaxTurns: maxTurns, Timeout: timeLeft(r),
		},
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	recordSubmission(r, run, res)
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("the agentkit brain submitted a diagnosis for run %s", runID),
		Result:  triageRunResult{RunID: runID, Submitted: true},
	}, nil
}

// timeLeft is the time until the deadline of the command, or 0 when it has none.
func timeLeft(r *toolio.Run) time.Duration {
	if deadline, ok := r.Ctx.Deadline(); ok {
		return max(time.Until(deadline), time.Second)
	}
	return 0
}

// triageResult is the result of af triage and of af triage land.
type triageResult struct {
	RunID                string            `json:"run_id"`
	Issue                *triage.IssueInfo `json:"issue"`
	Action               string            `json:"action"`
	Diagnosis            triage.Diagnosis  `json:"diagnosis"`
	Labels               []string          `json:"labels"`
	OriginalReportSHA256 string            `json:"original_report_sha256"`
}

// labelList is a flag that may be given more than once.
type labelList []string

func (l *labelList) String() string { return strings.Join(*l, ",") }

func (l *labelList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// hostTree is what a diagnosis is checked against on the host: the file view of
// the checkout, and the lines of its files.
func hostTree(r *toolio.Run) (triage.Tree, error) {
	files, err := checks.FileView(r.Ctx, r.Dir)
	if err != nil {
		return triage.Tree{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	roots := []string{r.Dir}
	if resolved, err := filepath.EvalSymlinks(r.Dir); err == nil && resolved != r.Dir {
		roots = append(roots, resolved)
	}
	return triage.Tree{Roots: roots, Files: files, Regular: func(p string) bool { return triage.RegularFile(r.Dir, p) }, Lines: triage.HostLines(r.Dir)}, nil
}

// landing is what a landing of a run is made of.
type landing struct {
	run    *toolio.RunDir
	facts  triage.Facts
	tree   triage.Tree
	forge  issuex.Client
	mode   string
	labels []string
	// artifact lists a file of the run; the files are listed once each.
	artifact func(toolio.Artifact)
}

// landRun writes the submission of a run to the forge, or to diagnosis.md alone,
// and says how that came out. The loop has checked the target and the credential by now.
func landRun(r *toolio.Run, l landing) (toolio.Outcome, error) {
	res, err := triage.Land(r.Ctx, triage.LandParams{
		RunDir: l.run.Path, RunID: l.run.ID, Facts: l.facts, Forge: l.forge, Mode: l.mode, Labels: l.labels,
		IsDryRun: func(err error) bool { return errors.Is(err, toolio.ErrDryRun) },
		Tree:     l.tree,
		Record:   func(kind string, data any) error { recordIn(r, l.run, kind, data); return nil },
		Warn:     r.Warn,
		Artifact: func(a triage.Artifact) {
			l.artifact(toolio.Artifact{Kind: a.Kind, Path: a.Path, URL: a.URL, Number: a.Number})
		},
		SideEffect: func(s triage.SideEffect) {
			r.SideEffect(toolio.SideEffect{Kind: s.Kind, Target: s.Target, State: s.State})
		},
	})
	if err != nil {
		var refusal *triage.Refusal
		if errors.As(err, &refusal) {
			return toolio.Outcome{}, refusalFailure(refusal)
		}
		return toolio.Outcome{}, err
	}
	return landOutcome(r, l.run, l.facts, res), nil
}

// landOutcome is the outcome of a landing: done, or needs_human when the
// submission carries a question and nothing was landed.
func landOutcome(r *toolio.Run, run *toolio.RunDir, facts triage.Facts, res triage.LandResult) toolio.Outcome {
	labels := res.Labels
	if labels == nil {
		labels = []string{}
	}
	sha := res.OriginalSHA256
	if sha == "" {
		sha = facts.OriginalReportSHA256
	}
	result := triageResult{
		RunID: run.ID, Issue: res.Issue, Action: res.Action, Diagnosis: res.Diagnosis,
		Labels: labels, OriginalReportSHA256: sha,
	}
	if q := res.Question; q != nil {
		recordIn(r, run, ledgerQuestion, q)
		return toolio.Outcome{
			Status: toolio.StatusNeedsHuman, Summary: "the diagnosis of run " + run.ID + " asks a question, and nothing was written",
			Result: result, NeedsHuman: &toolio.Question{Text: q.Text, Options: q.Options},
		}
	}
	if triage.FixSuggested(res.Diagnosis, res.Issue) {
		r.Next(toolio.Next{Argv: []string{"af", "fix", res.Issue.URL, "--dir", r.Dir}, Reason: "fix the problem the diagnosis describes"})
	}
	return toolio.Outcome{Status: toolio.StatusDone, Summary: landSummary(res), Result: result}
}

// landSummary says what a landing did in a line.
func landSummary(res triage.LandResult) string {
	where := "nowhere"
	if res.Issue != nil {
		where = res.Issue.URL
	}
	switch res.Action {
	case triage.ActionReplacedBody:
		return "replaced the body of " + where + " with the diagnosis, keeping the original report"
	case triage.ActionCommented:
		return "posted the diagnosis as a comment on " + where + ", since the body would be too long"
	case triage.ActionCreatedIssue:
		return "created " + where + " with the diagnosis"
	case triage.ActionPlanned:
		return "planned the writes of the diagnosis; a dry run sent none"
	}
	return "wrote the diagnosis to diagnosis.md only"
}

// recordSubmission notes the submission a brain left in the ledger of its run.
func recordSubmission(r *toolio.Run, run *toolio.RunDir, res brain.Result) {
	recordIn(r, run, ledgerSubmission, map[string]any{"submission": res.Path, "sha256": submission.PayloadSHA256(res.Payload)})
}

// triageStep is the data of a step event of the loop.
type triageStep struct {
	Name string `json:"name"`
}

// The steps of the loop, in the order its events name them.
const (
	stepPreflight = "preflight"
	stepBrief     = "brief"
	stepBrain     = "brain"
	stepLand      = "land"
)

// registerTriageLoop registers af triage: the input in, the diagnosis on the
// forge, in one process.
func registerTriageLoop(app *toolio.App, c *Config) {
	var settings *brain.Settings
	var land string
	var labels labelList
	app.Register(toolio.Command{
		Path:    "triage",
		Summary: "Diagnose a report and write the diagnosis back to the forge",
		Usage:   "af triage <issue|file|text|-> [--dir DIR] [--context ANSWER]... [--repo owner/name] [--land issue|none] [--label L]... [--dry-run] [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 15m]",
		Flags: func(fs *flag.FlagSet) {
			settings = brain.Flags(fs, brain.Defaults{Budget: triage.DefaultBudget, MaxTurns: triage.DefaultMaxTurns})
			labels = nil
			fs.StringVar(&land, "land", triage.LandIssue, "where the diagnosis goes: `issue` (write it to the forge) or none (write diagnosis.md only)")
			fs.Var(&labels, "label", "add this `label` to the issue; may be repeated")
		},
		Input:          toolio.InputRequired,
		UsesForge:      true,
		UsesRunDir:     true,
		Context:        true,
		Repo:           true,
		DryRun:         true,
		Timeout:        true,
		DefaultTimeout: triage.DefaultTimeout,
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return triageLoop(c, r, settings, land, labels) },
	})
}

// triageLoop is a triage run from the report to the forge: it refuses what it can
// before it spends, renders the brief, runs the brain over a box that holds the
// tree, takes the submission and the refusals back from the box, checks the
// submission against the host's tree once more, and lands it.
func triageLoop(c *Config, r *toolio.Run, s *brain.Settings, land string, labelFlags []string) (toolio.Outcome, error) {
	r.Emit("step", triageStep{stepPreflight})
	report, facts := resolveInput(r)
	labels, entry, boxEntry, err := loopPreflight(c, r, report, s, land, labelFlags)
	if err != nil {
		return toolio.Outcome{}, err
	}
	opts := c.brainOptions()
	opts.Script, opts.Provider = s.Script, s.Sandbox
	b, err := entry.New(opts)
	if err != nil {
		return toolio.Outcome{}, err
	}

	r.Emit("step", triageStep{stepBrief})
	// A brain that submits with the command alone is told the command; every other brain
	// submits through the tool, in this process or in the box that runs its driver.
	rendered, err := renderBrief(c, r, report, facts, triage.Bounds{MaxTurns: s.MaxTurns, MaxCostUSD: s.Budget, Timeout: timeLeft(r)})
	if err != nil {
		return toolio.Outcome{}, err
	}

	r.Emit("step", triageStep{stepBrain})
	res, err := runInBox(c, r, s, entry, boxEntry, b, opts, rendered.brief, &brain.Tally{})
	if err != nil {
		return toolio.Outcome{}, err
	}

	tree, err := hostTree(r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	if reason := triage.StaleReason(res.Payload, tree); reason != "" {
		return toolio.Outcome{}, staleSubmission(r, res, reason)
	}
	r.Artifact(toolio.Artifact{Kind: "submission", Path: res.Path})
	recordSubmission(r, r.RunDir, res)

	r.Emit("step", triageStep{stepLand})
	return landRun(r, landing{run: r.RunDir, facts: rendered.facts, tree: tree, forge: r.Forge, mode: land, labels: labels, artifact: r.Artifact})
}

// loopPreflight is every refusal that needs no model, in the order flags, input,
// landing: nothing is rendered and no brain is made before it passes. It returns
// the labels, the entry of the brain the run is for and the entry of its box provider.
func loopPreflight(c *Config, r *toolio.Run, report triage.Report, s *brain.Settings, land string, labelFlags []string) ([]string, brain.Entry, box.Entry, error) {
	if err := s.ValidateIn(c.getenv(), c.boxes()); err != nil {
		return nil, brain.Entry{}, box.Entry{}, err
	}
	entry, err := c.brains().Lookup(s.Brain)
	if err != nil {
		return nil, brain.Entry{}, box.Entry{}, err
	}
	boxEntry, err := c.boxes().Lookup(s.Sandbox)
	if err != nil {
		return nil, brain.Entry{}, box.Entry{}, err
	}
	pre, refusal := triage.Preflight(r.Ctx, triage.PreflightParams{
		Flags:     triage.Flags{Budget: s.Budget, MaxTurns: s.MaxTurns, Labels: labelFlags, Land: land},
		Report:    report,
		Truncated: r.Input.Truncated,
		LandCheck: triage.LandCheck{Forge: r.Forge, Target: r.Target, DryRun: r.DryRun},
	})
	if refusal != nil {
		return nil, brain.Entry{}, box.Entry{}, refusalFailure(refusal)
	}
	for _, w := range pre.Warnings {
		r.Warn(w.Code, w.Message)
	}
	return pre.Labels, entry, boxEntry, nil
}

// loopReport is the report that brain.Execute gets: the run's own, less what the
// loop says itself. The stages of Execute are not the steps of a triage run, and
// a submission is listed once the host has checked it again.
type loopReport struct{ *toolio.Run }

func (l loopReport) Emit(typ string, data any) {
	if typ != "step" {
		l.Run.Emit(typ, data)
	}
}

func (l loopReport) Artifact(a toolio.Artifact) {
	if a.Kind != "submission" {
		l.Run.Artifact(a)
	}
}

// runInBox opens the box over the checkout and runs the brain in it, with the
// tools of the triage allowlist. The request for the box is the one the brain, the
// provider and those tools come to (brain.BoxRequest). The
// submission and the refused calls of the box are in the run directory when it
// returns, whether the run came to a submission or not.
func runInBox(c *Config, r *toolio.Run, s *brain.Settings, entry brain.Entry, boxEntry box.Entry, b brain.Brain, opts brain.Options, brief triage.Brief, tally *brain.Tally) (brain.Result, error) {
	var res brain.Result
	deadline, _ := r.Ctx.Deadline()
	err := box.With(r.Ctx, brain.BoxRequest(brain.BoxParams{
		Brain: entry, Provider: boxEntry, Tools: triage.BriefTools(), Getenv: opts.Getenv,
		Deadline: deadline, Defaulted: s.SandboxSource != brain.SandboxFlag,
		HostDir: r.Dir, RunID: r.RunDir.ID, Tool: "triage", Record: r.RunDir.Record, Warn: r.Warn, Registry: c.boxes(), Labels: c.BoxLabels,
	}), func(sess *box.Session) error {
		var offered []string
		for _, tool := range sess.Tools() {
			offered = append(offered, tool.Name)
		}
		prefix, task := brief.Document[:brief.PrefixBytes], brief.Document[brief.PrefixBytes:]
		var err error
		res, err = brain.Execute(r.Ctx, brain.Request{
			Session: sess, Name: s.Brain, Brain: b, RunDir: r.RunDir, Report: loopReport{r}, Tally: tally,
			Brief: brain.Brief{
				RunID: r.RunDir.ID, Attempt: 1, Kind: triage.Kind(r.Dir, sess.Spec().Workdir), Prefix: prefix, Task: task,
				Tools: triage.BoxTools(offered), Hook: brain.HookConfig{RunID: r.RunDir.ID},
				MaxCostUSD: s.Budget, MaxTurns: s.MaxTurns, Timeout: timeLeft(r),
			},
		})
		return err
	})
	return res, err
}

// staleSubmission is the failure of a submission that the box accepted and the
// host's tree no longer bears out: it is taken out of the run directory, as a
// run without a valid submission has none.
func staleSubmission(r *toolio.Run, res brain.Result, reason string) error {
	message := "the submission no longer holds against the files of the checkout, so the run has no submission: " + reason
	if err := os.Remove(res.Path); err != nil {
		r.Warn("submission_not_removed", fmt.Sprintf("%s is not a submission af accepted, and could not be removed: %v", res.Path, err))
	}
	r.Warn("submission_invalid", message)
	return &toolio.Failure{Category: toolio.CategoryNoResult, Retryable: true, Message: message}
}
