package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// registerTriage registers af triage and its subcommands.
func registerTriage(app *toolio.App) {
	registerTriageBrief(app)
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
func record(r *toolio.Run, kind string, data any) {
	if err := r.RunDir.Record(kind, data); err != nil {
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
