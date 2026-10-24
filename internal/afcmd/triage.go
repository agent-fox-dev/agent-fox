package afcmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
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
	registerTriageBrief(app, c)
	registerTriageSubmit(app)
	registerTriageRun(app, c)
	registerTriageLand(app)
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
// artifact. It is the part of a triage run that the loop and `af triage brief`
// share, and it refuses what triage does not take (the loop has refused it in its
// preflight already).
func renderBrief(c *Config, r *toolio.Run, report triage.Report, facts triage.Facts, inProcess bool, bounds triage.Bounds) (renderedBrief, error) {
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
		InProcess: inProcess, Bounds: bounds, TruncationMarker: toolio.TruncationMarker,
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

// triageBriefResult is the result of af triage brief.
type triageBriefResult struct {
	Brief            string    `json:"brief"`
	PrefixBytes      int       `json:"prefix_bytes"`
	PrefixSHA256     string    `json:"prefix_sha256"`
	DocumentBytes    int       `json:"document_bytes"`
	EstimatedTokens  int       `json:"estimated_tokens"`
	Map              triageMap `json:"map"`
	ReportTruncated  bool      `json:"report_truncated"`
	PreviousQuestion bool      `json:"previous_question"`
}

// triageMap says how much of the repository map a brief holds.
type triageMap struct {
	Level     int  `json:"level"`
	Truncated bool `json:"truncated"`
}

// registerTriageBrief registers af triage brief.
func registerTriageBrief(app *toolio.App, c *Config) {
	app.Register(toolio.Command{
		Path:           "triage brief",
		Summary:        "Render the brief of a triage run, without running a brain",
		Usage:          "af triage brief <issue|file|text|-> [--dir DIR] [--context ANSWER]... [--repo owner/name]",
		Input:          toolio.InputRequired,
		UsesForge:      true,
		UsesRunDir:     true,
		Context:        true,
		Repo:           true,
		Timeout:        true,
		DefaultTimeout: 2 * time.Minute,
		ResultSchema:   resultSchema("triage_brief"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return triageBrief(c, r) },
	})
}

// triageBrief renders the brief for a person to read, and for the steps that
// follow. It calls no model and writes nothing to the forge.
func triageBrief(c *Config, r *toolio.Run) (toolio.Outcome, error) {
	report, facts := resolveInput(r)
	rendered, err := renderBrief(c, r, report, facts, false, triage.DefaultBounds)
	if err != nil {
		return toolio.Outcome{}, err
	}
	b := rendered.brief
	path := filepath.Join(r.RunDir.Path, "brief.md")
	for _, step := range []struct{ command, reason string }{
		{"submit", "submit the diagnosis, as JSON on stdin"},
		{"land", "write the diagnosis to the forge"},
	} {
		r.Next(toolio.Next{Argv: []string{"af", "triage", step.command, "--run-id", r.RunDir.ID, "--dir", r.Dir}, Reason: step.reason})
	}
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("rendered the brief of run %s: %d bytes, about %d tokens", r.RunDir.ID, len(b.Document), estimatedTokens(b.Document)),
		Result: triageBriefResult{
			Brief: path, PrefixBytes: b.PrefixBytes, PrefixSHA256: rendered.facts.Prefix.SHA256, DocumentBytes: len(b.Document),
			EstimatedTokens: estimatedTokens(b.Document), Map: triageMap{Level: b.MapLevel, Truncated: b.MapTruncated},
			ReportTruncated: b.ReportTruncated, PreviousQuestion: rendered.previous,
		},
	}, nil
}

// estimatedTokens is the size of a text in tokens, estimated as its bytes divided
// by 4, rounded up, the way the repository map counts.
func estimatedTokens(text string) int { return (len(text) + 3) / 4 }

// triageSubmitResult is the result of af triage submit.
type triageSubmitResult struct {
	RunID             string `json:"run_id"`
	Accepted          bool   `json:"accepted"`
	Submission        string `json:"submission"`
	RejectedPathCalls int    `json:"rejected_path_calls"`
}

// registerTriageSubmit registers af triage submit: the submit command of the
// submission protocol, with the triage kind made for the directory it works in.
func registerTriageSubmit(app *toolio.App) {
	cmd := submission.CommandWith("triage submit", triage.Kind(""), submission.Options{
		Kind:         func(dir string) submission.Kind { return triage.Kind(dir) },
		ResultSchema: resultSchema("triage_submit"),
		Prepare:      func(_ *toolio.Run, run *toolio.RunDir) error { return placeRunJSON(run, triage.KindName) },
		Result:       submitResult,
	})
	cmd.Summary = "Submit the diagnosis of a triage run, as JSON on stdin"
	cmd.Usage = "af triage submit --run-id ID [--dir DIR] < diagnosis.json"
	cmd.DefaultTimeout, cmd.Timeout = 30*time.Second, true
	app.Register(cmd)
}

// placeRunJSON places the file that the protocol reads the attempt and the nonce
// of a submission from, when the run has none: a person who drives the steps at
// their own checkout has no loop to place it. A box that runs a brain has it
// placed already, and it is left as it is.
func placeRunJSON(run *toolio.RunDir, kind string) error {
	path := filepath.Join(run.Path, "run.json")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: err.Error()}
	}
	data, err := json.MarshalIndent(submission.Run{RunID: run.ID, Kind: kind, Attempt: 1, Nonce: hex.EncodeToString(nonce[:])}, "", "  ")
	if err == nil {
		err = agentdir.WriteFileAtomic(path, append(data, '\n'))
	}
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	return nil
}

// submitResult is the result of an accepted `af triage submit`: it records
// triage.submission and counts the refusals of the run.
func submitResult(r *toolio.Run, run *toolio.RunDir, receipt submission.Receipt) (any, error) {
	path := filepath.Join(r.Dir, filepath.FromSlash(receipt.Path))
	pathCalls := countRejections(r, run)
	recordIn(r, run, ledgerSubmission, map[string]any{"submission": path, "sha256": receipt.SHA256})
	return triageSubmitResult{RunID: run.ID, Accepted: true, Submission: path, RejectedPathCalls: pathCalls}, nil
}

// countRejections is the number of refused calls of the run that cited a path
// that is no file, from rejections.jsonl. A file that cannot be read counts 0 and
// is a warning. The count is reported and decides nothing.
func countRejections(r *toolio.Run, run *toolio.RunDir) int {
	pathCalls, _, warning := triage.CountRejections(filepath.Join(run.Path, triage.RejectionsFile))
	if warning != "" {
		r.Warn(warning, "rejections.jsonl of run "+run.ID+" cannot be read, so the refused calls are counted as 0")
	}
	return pathCalls
}

// triageRunResult is the result of af triage run.
type triageRunResult struct {
	RunID             string `json:"run_id"`
	Submitted         bool   `json:"submitted"`
	RejectedPathCalls int    `json:"rejected_path_calls"`
}

// registerTriageRun registers af triage run, which a box executes: the agentkit
// brain, in this process, on the brief of a run and the directory it is in.
func registerTriageRun(app *toolio.App, c *Config) {
	var runID string
	var budget float64
	var maxTurns int
	app.Register(toolio.Command{
		Path:    "triage run",
		Summary: "Run the agentkit brain in this process on the brief of a triage run",
		Usage:   "af triage run --run-id ID [--dir DIR] [--budget USD] [--max-turns N] [--timeout 15m]",
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&runID, "run-id", "", "the `id` of the run, made by af triage brief")
			fs.Float64Var(&budget, "budget", triage.DefaultBudget, "stop the brain after this many `dollars` of model cost")
			fs.IntVar(&maxTurns, "max-turns", triage.DefaultMaxTurns, "stop the brain after this many `turns`")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		Timeout:        true,
		DefaultTimeout: triage.DefaultTimeout,
		ResultSchema:   resultSchema("triage_run"),
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
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--run-id is required: the id of the run that af triage brief made"}
	}
	run, err := toolio.OpenRunDir(r.Dir, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	prefix, task, placed, err := briefOf(run, budget, maxTurns, r)
	if err != nil {
		return toolio.Outcome{}, err
	}
	// A run placed in a box is run under the attempt and the nonce it was placed with,
	// since the process that placed it accepts a submission made under no other.
	attempt, nonce := 1, ""
	if placed != nil {
		attempt, nonce = placed.Run.Attempt, placed.Run.Nonce
	}

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
	pathCalls := countRejections(r, run)
	recordSubmission(r, run, res)
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("the agentkit brain submitted a diagnosis for run %s", runID),
		Result:  triageRunResult{RunID: runID, Submitted: true, RejectedPathCalls: pathCalls},
	}, nil
}

// briefOf reads the brief of a run, and splits it into the prefix, which is kept
// as it was rendered, and the task block, adapted for a brain that submits
// through the tool. The prefix ends where triage.json says; a run directory that
// has none is one that brain.Execute placed in a box, and run.json says it (placed is
// then its content).
func briefOf(run *toolio.RunDir, budget float64, maxTurns int, r *toolio.Run) (prefix, task string, placed *placedRun, err error) {
	data, err := os.ReadFile(filepath.Join(run.Path, "brief.md"))
	if err != nil {
		return "", "", nil, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no brief (%v): run af triage brief first", run.ID, err)}
	}
	noFacts := func() error {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no triage.json that goes with its brief: run af triage brief first", run.ID)}
	}
	var facts triage.Facts
	factsData, err := os.ReadFile(filepath.Join(run.Path, "triage.json"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p, ok, perr := readPlaced(run, triage.KindName)
		if perr != nil {
			return "", "", nil, perr
		}
		if !ok {
			return "", "", nil, noFacts()
		}
		placed, prefix, task = &p, p.Prefix, p.Task
	default:
		if err == nil {
			err = json.Unmarshal(factsData, &facts)
		}
		if err != nil || facts.Prefix.Bytes <= 0 || facts.Prefix.Bytes > len(data) {
			return "", "", nil, noFacts()
		}
		prefix, task = string(data[:facts.Prefix.Bytes]), string(data[facts.Prefix.Bytes:])
	}
	bounds := triage.Bounds{MaxTurns: maxTurns, MaxCostUSD: budget, Timeout: timeLeft(r)}
	return prefix, triage.AdaptTask(task, run.ID, bounds), placed, nil
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
	RejectedPathCalls    int               `json:"rejected_path_calls"`
	RejectedSubmissions  int               `json:"rejected_submissions"`
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

// registerTriageLand registers af triage land: the landing of a run that has a
// submission, which can be repeated.
func registerTriageLand(app *toolio.App) {
	var runID, land, repo string
	var labels labelList
	app.Register(toolio.Command{
		Path:    "triage land",
		Summary: "Write the diagnosis of a triage run to the forge",
		Usage:   "af triage land --run-id ID [--dir DIR] [--land issue|none] [--label L]... [--repo owner/name] [--dry-run]",
		Flags: func(fs *flag.FlagSet) {
			labels = nil
			fs.StringVar(&runID, "run-id", "", "the `id` of the run, with a submission")
			fs.StringVar(&land, "land", triage.LandIssue, "where the diagnosis goes: `issue` (write it to the forge) or none (write diagnosis.md only)")
			fs.StringVar(&repo, "repo", "", "the repository, `owner/name`, which must be the one the run recorded")
			fs.Var(&labels, "label", "add this `label` to the issue; may be repeated")
		},
		MaxArgs:        toolio.NoArgs,
		Dir:            true,
		DryRun:         true,
		Timeout:        true,
		DefaultTimeout: 2 * time.Minute,
		ResultSchema:   resultSchema("triage_land"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return triageLand(r, runID, land, repo, labels) },
	})
}

// readFacts reads triage.json of a run.
func readFacts(run *toolio.RunDir) (triage.Facts, error) {
	var facts triage.Facts
	data, err := os.ReadFile(filepath.Join(run.Path, "triage.json"))
	if err == nil {
		err = json.Unmarshal(data, &facts)
	}
	if err != nil {
		return facts, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no triage.json (%v): run af triage brief first", run.ID, err)}
	}
	return facts, nil
}

// repoOfFacts is the repository of a fact, with its host.
func repoOfFacts(f triage.RepoFact) issuex.Repo {
	repo, _ := issuex.ParseRepo(f.Repo)
	repo.Host = f.Host
	return repo
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

// createdIssue is the issue an earlier landing of the run created, from its
// ledger, as that landing reported it: the last landing that wrote to an issue of
// a report that was no issue.
func createdIssue(run *toolio.RunDir) func() (triage.IssueInfo, bool) {
	return func() (triage.IssueInfo, bool) {
		entries, err := run.Ledger()
		if err != nil {
			return triage.IssueInfo{}, false
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Kind != ledgerLand {
				continue
			}
			var land struct {
				Issue *triage.IssueInfo `json:"issue"`
			}
			if json.Unmarshal(entries[i].Data, &land) == nil && land.Issue != nil && land.Issue.Number > 0 {
				return *land.Issue, true
			}
		}
		return triage.IssueInfo{}, false
	}
}

// triageLand lands the submission of a run.
func triageLand(r *toolio.Run, runID, land, repoFlag string, labelFlags []string) (toolio.Outcome, error) {
	labels, refusal := triage.CheckLandFlags(land, labelFlags)
	if refusal != nil {
		return toolio.Outcome{}, refusalFailure(refusal)
	}
	if runID == "" {
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: "--run-id is required: the id of a run with a submission"}
	}
	run, err := toolio.OpenRunDir(r.Dir, runID)
	if err != nil {
		return toolio.Outcome{}, err
	}
	listed := listRunArtifacts(r, run)
	facts, err := readFacts(run)
	if err != nil {
		return toolio.Outcome{}, err
	}
	target := repoOfFacts(facts.Target)
	if repoFlag != "" {
		given, ok := issuex.ParseRepo(repoFlag)
		switch {
		case !ok:
			return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--repo %q is not of the form owner/repo", repoFlag)}
		case facts.Target.Repo != "" && !strings.EqualFold(given.String(), target.String()):
			return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("--repo %s is not the repository of run %s, which is %s", given, runID, target)}
		case facts.Target.Repo == "":
			target = given
			facts.Target.Repo = given.String()
		}
	}
	if !target.Valid() {
		// A text or a file report whose run was given no repository: the issue an earlier landing created is in one.
		if info, ok := createdIssue(run)(); ok {
			if created, ok := issuex.ParseRepo(info.Repo); ok {
				target, facts.Target.Repo = created, created.String()
			}
		}
	}
	tree, err := hostTree(r)
	if err != nil {
		return toolio.Outcome{}, err
	}

	var forge issuex.Client
	var check func(context.Context) *triage.Refusal
	if land != triage.LandNone {
		// A run that has no repository is told so before a client is made for none.
		if refusal := triage.CheckLandTarget(r.Ctx, triage.LandCheck{Target: target, DryRun: true}); refusal != nil {
			return toolio.Outcome{}, refusalFailure(refusal)
		}
		if forge, err = r.ForgeFor(target); err != nil {
			return toolio.Outcome{}, err
		}
		check = func(ctx context.Context) *triage.Refusal {
			return triage.CheckLandTarget(ctx, triage.LandCheck{Forge: forge, Target: target, DryRun: r.DryRun})
		}
	}
	return landRun(r, landing{run: run, facts: facts, tree: tree, forge: forge, mode: land, labels: labels, check: check, artifact: listed})
}

// landing is what a landing of a run is made of.
type landing struct {
	run    *toolio.RunDir
	facts  triage.Facts
	tree   triage.Tree
	forge  issuex.Client
	mode   string
	labels []string
	// check is the target and the credential check of a landing whose caller has
	// not made it; the loop made it in its preflight.
	check func(ctx context.Context) *triage.Refusal
	// artifact lists a file of the run; the files are listed once each.
	artifact func(toolio.Artifact)
	// retry is the command that lands the run again; it is suggested when the
	// landing fails after the run had a valid submission.
	retry []string
}

// landRun writes the submission of a run to the forge, or to diagnosis.md alone,
// and says how that came out. It is the landing that `af triage land` and the loop
// share: both have checked the target and the credential by now.
func landRun(r *toolio.Run, l landing) (toolio.Outcome, error) {
	res, err := triage.Land(r.Ctx, triage.LandParams{
		RunDir: l.run.Path, RunID: l.run.ID, Facts: l.facts, Forge: l.forge, Mode: l.mode, Labels: l.labels,
		IsDryRun: func(err error) bool { return errors.Is(err, toolio.ErrDryRun) },
		Tree:     l.tree, Created: createdIssue(l.run), Check: l.check,
		Record: func(kind string, data any) error { recordIn(r, l.run, kind, data); return nil },
		Warn:   r.Warn,
		Artifact: func(a triage.Artifact) {
			l.artifact(toolio.Artifact{Kind: a.Kind, Path: a.Path, URL: a.URL, Number: a.Number})
		},
		SideEffect: func(s triage.SideEffect) {
			r.SideEffect(toolio.SideEffect{Kind: s.Kind, Target: s.Target, State: s.State})
		},
	})
	if err != nil {
		if res.Submitted && l.retry != nil {
			r.Next(toolio.Next{Argv: l.retry, Reason: "write the diagnosis to the forge again"})
		}
		var refusal *triage.Refusal
		if errors.As(err, &refusal) {
			return toolio.Outcome{}, refusalFailure(refusal)
		}
		return toolio.Outcome{}, err
	}
	return landOutcome(r, l.run, l.facts, res), nil
}

// listRunArtifacts lists the files of the run that exist and a failed run still
// has: the brief, the submission and the diagnosis. It returns the function that
// lists an artifact of the run, which lists none a second time.
func listRunArtifacts(r *toolio.Run, run *toolio.RunDir) func(toolio.Artifact) {
	seen := map[toolio.Artifact]bool{}
	list := func(a toolio.Artifact) {
		if !seen[a] {
			seen[a] = true
			r.Artifact(a)
		}
	}
	for _, a := range []struct{ kind, name string }{{"brief", "brief.md"}, {"submission", "submission.json"}, {"diagnosis", "diagnosis.md"}} {
		path := filepath.Join(run.Path, a.name)
		if _, err := os.Stat(path); err == nil {
			list(toolio.Artifact{Kind: a.kind, Path: path})
		}
	}
	return list
}

// landOutcome is the outcome of a landing: done, or needs_human when the
// submission carries a question and nothing was landed.
func landOutcome(r *toolio.Run, run *toolio.RunDir, facts triage.Facts, res triage.LandResult) toolio.Outcome {
	pathCalls, total, warning := triage.CountRejections(filepath.Join(run.Path, triage.RejectionsFile))
	if warning != "" {
		r.Warn(warning, "rejections.jsonl of run "+run.ID+" cannot be read, so the refused calls are counted as 0")
	}
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
		RejectedPathCalls: pathCalls, RejectedSubmissions: total, Labels: labels, OriginalReportSHA256: sha,
	}
	if q := res.Question; q != nil {
		recordIn(r, run, ledgerQuestion, q)
		return toolio.Outcome{
			Status: toolio.StatusNeedsHuman, Summary: "the diagnosis of run " + run.ID + " asks a question, and nothing was written",
			Result: result, NeedsHuman: &toolio.Question{Text: q.Text, Options: q.Options},
		}
	}
	if triage.FixSuggested(res.Diagnosis, res.Issue) {
		r.Next(toolio.Next{Argv: []string{"af", "fix", res.Issue.URL, "--dir", r.Dir, "--local"}, Reason: "fix the problem the diagnosis describes"})
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
		Usage:   "af triage <issue|file|text|-> [--dir DIR] [--context ANSWER]... [--repo owner/name] [--land issue|none] [--label L]... [--dry-run] [--brain NAME] [--sandbox NAME] [--borrowed-hands] [--budget USD] [--max-turns N] [--timeout 15m]",
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
		ResultSchema:   resultSchema("triage"),
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
	opts.Script, opts.Provider, opts.BorrowedHands = s.Script, s.Sandbox, s.BorrowedHands
	b, err := entry.New(opts)
	if err != nil {
		return toolio.Outcome{}, err
	}

	r.Emit("step", triageStep{stepBrief})
	// A brain that submits with the command alone is told the command; every other brain
	// submits through the tool, in this process or in the box that runs its driver.
	rendered, err := renderBrief(c, r, report, facts, true, triage.Bounds{MaxTurns: s.MaxTurns, MaxCostUSD: s.Budget, Timeout: timeLeft(r)})
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
	retry := triage.LandAgain(r.RunDir.ID, r.Dir, "")
	if rendered.report.Kind == triage.KindIssue {
		// The client was built from the URL of the input, so landing again is told the repository.
		retry = triage.LandAgain(r.RunDir.ID, r.Dir, rendered.facts.Target.Repo)
	}
	return landRun(r, landing{run: r.RunDir, facts: rendered.facts, tree: tree, forge: r.Forge, mode: land, labels: labels, artifact: r.Artifact, retry: retry})
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
// provider, borrowed hands and those tools come to (brain.BoxRequest). The
// submission and the refused calls of the box are in the run directory when it
// returns, whether the run came to a submission or not.
func runInBox(c *Config, r *toolio.Run, s *brain.Settings, entry brain.Entry, boxEntry box.Entry, b brain.Brain, opts brain.Options, brief triage.Brief, tally *brain.Tally) (brain.Result, error) {
	var res brain.Result
	deadline, _ := r.Ctx.Deadline()
	err := box.With(r.Ctx, brain.BoxRequest(brain.BoxParams{
		Brain: entry, Provider: boxEntry, BorrowedHands: s.BorrowedHands, Tools: triage.BriefTools(), Getenv: opts.Getenv,
		Deadline: deadline, Defaulted: s.SandboxSource != brain.SandboxFlag, AFPath: opts.AFPath,
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
		readBackRejections(r, sess)
		return err
	})
	return res, err
}

// readBackRejections copies the refused calls of the box into the run directory.
// A file that cannot be had is a warning: the count of refused calls is a report
// and decides nothing.
func readBackRejections(r *toolio.Run, sess *box.Session) {
	data, err := triage.ReadBackRejections(sess.FS(r.Ctx), r.RunDir.ID)
	if err == nil && len(data) > 0 {
		err = agentdir.WriteFileAtomic(filepath.Join(r.RunDir.Path, triage.RejectionsFile), data)
	}
	if err != nil {
		r.Warn(triage.WarnRejectionsUnreadable, fmt.Sprintf("the refused calls of run %s could not be brought out of the box, so they are counted as 0: %v", r.RunDir.ID, err))
	}
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
