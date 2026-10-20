package main

import (
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
func registerTriage(app *toolio.App) {
	registerTriageBrief(app)
	registerTriageSubmit(app)
	registerTriageRun(app)
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

// renderBrief renders the brief of the run, writes brief.md, original.md and
// triage.json, records triage.input and triage.brief, and lists the brief as an
// artifact. It is the part of a triage run that the loop and `af triage brief`
// share.
func renderBrief(r *toolio.Run, inProcess bool, bounds triage.Bounds) (renderedBrief, error) {
	report := triageReport(r.Input)
	facts := triageFacts(r, report)
	record(r, ledgerInput, facts.Input)

	var previous *triage.Question
	if len(r.Context) > 0 {
		previous = triage.FindPreviousQuestion(r.Dir, facts.Input.Fingerprint, r.RunDir.ID)
	}
	brief, err := triage.BuildBrief(r.Ctx, triage.BriefParams{
		Dir: r.Dir, Report: report, RunID: r.RunDir.ID, Context: r.Context, PreviousQuestion: previous,
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
func registerTriageBrief(app *toolio.App) {
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
		Run:            triageBrief,
	})
}

// triageBrief renders the brief for a person to read, and for the steps that
// follow. It calls no model and writes nothing to the forge.
func triageBrief(r *toolio.Run) (toolio.Outcome, error) {
	rendered, err := renderBrief(r, false, triage.DefaultBounds)
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
		Prepare:      placeRunJSON,
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
func placeRunJSON(_ *toolio.Run, run *toolio.RunDir) error {
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
	data, err := json.MarshalIndent(submission.Run{RunID: run.ID, Kind: triage.KindName, Attempt: 1, Nonce: hex.EncodeToString(nonce[:])}, "", "  ")
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

// triageBrainOptions are what the brain of a run is made of: the model from
// AF_MODEL, and the provider and the credentials the brain finds for itself. A
// test replaces it with a brain on the faux provider.
var triageBrainOptions = func() brain.Options { return brain.Options{Model: os.Getenv("AF_MODEL")} }

// triageRunResult is the result of af triage run.
type triageRunResult struct {
	RunID             string `json:"run_id"`
	Submitted         bool   `json:"submitted"`
	RejectedPathCalls int    `json:"rejected_path_calls"`
}

// registerTriageRun registers af triage run, which a box executes: the agentkit
// brain, in this process, on the brief of a run and the directory it is in.
func registerTriageRun(app *toolio.App) {
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
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return triageRun(r, runID, budget, maxTurns) },
	})
}

// triageRun runs the agentkit brain over the directory, with no box: the tools
// work on --dir itself, as they do in the box that executes this process, and
// file_diagnosis submits into the run directory under it.
func triageRun(r *toolio.Run, runID string, budget float64, maxTurns int) (toolio.Outcome, error) {
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
	prefix, task, err := briefOf(run, budget, maxTurns, r)
	if err != nil {
		return toolio.Outcome{}, err
	}

	sess, err := box.Here(r.Ctx, box.Request{HostDir: r.Dir, RunID: runID, Tool: "triage", Record: run.Record, Warn: r.Warn})
	if err != nil {
		return toolio.Outcome{}, err
	}
	defer func() { _ = sess.Close() }()
	b, err := brain.Default.New(brain.AgentkitName, triageBrainOptions())
	if err != nil {
		return toolio.Outcome{}, err
	}
	var offered []string
	for _, tool := range sess.Tools() {
		offered = append(offered, tool.Name)
	}

	res, err := brain.Execute(r.Ctx, brain.Request{
		Session: sess, Name: brain.AgentkitName, Brain: b, RunDir: run, Report: r, Tally: &brain.Tally{},
		Brief: brain.Brief{
			RunID: runID, Attempt: 1, Kind: triage.Kind(r.Dir), Prefix: prefix, Task: task,
			Tools: triage.BoxTools(offered), Hook: brain.HookConfig{RunID: runID},
			MaxCostUSD: budget, MaxTurns: maxTurns, Timeout: timeLeft(r),
		},
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	pathCalls := countRejections(r, run)
	recordIn(r, run, ledgerSubmission, map[string]any{"submission": res.Path})
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("the agentkit brain submitted a diagnosis for run %s", runID),
		Result:  triageRunResult{RunID: runID, Submitted: true, RejectedPathCalls: pathCalls},
	}, nil
}

// briefOf reads the brief of a run, and splits it into the prefix, which is kept
// as it was rendered, and the task block, adapted for a brain that submits
// through the tool.
func briefOf(run *toolio.RunDir, budget float64, maxTurns int, r *toolio.Run) (prefix, task string, err error) {
	data, err := os.ReadFile(filepath.Join(run.Path, "brief.md"))
	if err != nil {
		return "", "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no brief (%v): run af triage brief first", run.ID, err)}
	}
	var facts triage.Facts
	factsData, err := os.ReadFile(filepath.Join(run.Path, "triage.json"))
	if err == nil {
		err = json.Unmarshal(factsData, &facts)
	}
	if err != nil || facts.Prefix.Bytes <= 0 || facts.Prefix.Bytes > len(data) {
		return "", "", &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("run %s has no triage.json that goes with its brief: run af triage brief first", run.ID)}
	}
	prefix = string(data[:facts.Prefix.Bytes])
	bounds := triage.Bounds{MaxTurns: maxTurns, MaxCostUSD: budget, Timeout: timeLeft(r)}
	return prefix, triage.AdaptTask(string(data[facts.Prefix.Bytes:]), run.ID, bounds), nil
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
	return triage.Tree{Roots: roots, Files: files, Lines: func(p string) (int, error) {
		data, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(p)))
		if err != nil {
			return 0, err
		}
		n := strings.Count(string(data), "\n")
		if len(data) > 0 && data[len(data)-1] != '\n' {
			n++
		}
		return n, nil
	}}, nil
}

// createdIssue is the issue an earlier landing of the run created, from its
// ledger: the last landing that wrote to an issue of a report that was no issue.
func createdIssue(run *toolio.RunDir) func() (issuex.IssueRef, bool) {
	return func() (issuex.IssueRef, bool) {
		entries, err := run.Ledger()
		if err != nil {
			return issuex.IssueRef{}, false
		}
		for i := len(entries) - 1; i >= 0; i-- {
			if entries[i].Kind != ledgerLand {
				continue
			}
			var land struct {
				Issue *triage.IssueInfo `json:"issue"`
			}
			if json.Unmarshal(entries[i].Data, &land) == nil && land.Issue != nil && land.Issue.Number > 0 {
				return issuex.IssueRef{Repo: repoOfFacts(triage.RepoFact{Repo: land.Issue.Repo, Host: land.Issue.Host}), Number: land.Issue.Number}, true
			}
		}
		return issuex.IssueRef{}, false
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
	tree, err := hostTree(r)
	if err != nil {
		return toolio.Outcome{}, err
	}

	var forge issuex.Client
	if land != triage.LandNone {
		if forge, err = r.ForgeFor(target); err != nil {
			return toolio.Outcome{}, err
		}
		if refusal := triage.CheckLandTarget(r.Ctx, triage.LandCheck{Forge: forge, Target: target, DryRun: r.DryRun}); refusal != nil {
			return toolio.Outcome{}, refusalFailure(refusal)
		}
	}
	listRunArtifacts(r, run)

	res, err := triage.Land(r.Ctx, triage.LandParams{
		RunDir: run.Path, RunID: runID, Facts: facts, Forge: forge, Mode: land, Labels: labels,
		IsDryRun: func(err error) bool { return errors.Is(err, toolio.ErrDryRun) },
		Tree:     tree, Created: createdIssue(run),
		Record: run.Record,
		Warn:   r.Warn,
		Artifact: func(a triage.Artifact) {
			r.Artifact(toolio.Artifact{Kind: a.Kind, Path: a.Path, URL: a.URL, Number: a.Number})
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
	return landOutcome(r, run, facts, res), nil
}

// listRunArtifacts lists the files of the run that exist and a failed run still
// has: the brief and the submission.
func listRunArtifacts(r *toolio.Run, run *toolio.RunDir) {
	for _, a := range []struct{ kind, name string }{{"brief", "brief.md"}, {"submission", "submission.json"}} {
		path := filepath.Join(run.Path, a.name)
		if _, err := os.Stat(path); err == nil {
			r.Artifact(toolio.Artifact{Kind: a.kind, Path: path})
		}
	}
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
