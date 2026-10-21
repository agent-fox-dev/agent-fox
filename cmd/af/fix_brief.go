package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The ledger record of a rendered brief.
const ledgerFixBrief = "fix.brief"

// baseUsage is the help of --base.
const baseUsage = "the `branch` the fix is made on and goes back to; the one that is checked out when it is not given"

// fixBriefResult is the result of af fix brief.
type fixBriefResult struct {
	BriefPath    string        `json:"brief_path"`
	PrefixSHA256 string        `json:"prefix_sha256"`
	Base         string        `json:"base"`
	BaseHead     string        `json:"base_head"`
	Baseline     *fix.GateRef  `json:"baseline"`
	Table        project.Table `json:"table"`
	SubmitArgv   []string      `json:"submit_argv"`
}

// registerFixBrief registers af fix brief.
func registerFixBrief(app *toolio.App) {
	var base string
	app.Register(toolio.Command{
		Path:    "fix brief",
		Summary: "Render the brief of a fix run, without running a brain",
		Usage:   "af fix brief <issue|file|text|-> [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name]",
		Flags: func(fs *flag.FlagSet) {
			base = ""
			fs.StringVar(&base, "base", "", baseUsage)
		},
		Input:          toolio.InputRequired,
		UsesForge:      true,
		UsesRunDir:     true,
		Context:        true,
		Repo:           true,
		Timeout:        true,
		DefaultTimeout: fixDefaultTimeout,
		ResultSchema:   resultSchema("fix_brief"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return fixBrief(r, base) },
	})
}

// fixBrief makes the preflight and the baseline, and writes the brief and the files
// the steps that follow read. It calls no model, writes nothing to the forge and
// nothing outside .agent-fox.
func fixBrief(r *toolio.Run, base string) (toolio.Outcome, error) {
	if err := refusePullRequest(r); err != nil {
		return toolio.Outcome{}, err
	}
	pre, err := fix.Preflight(r.Ctx, fix.PreflightParams{Dir: r.Dir, Base: base, Forge: r.Forge, Target: r.Target, Warn: r.Warn, Gate: fixGate})
	if err != nil {
		return toolio.Outcome{}, err
	}
	warnIssueClosed(r)
	start, err := startRun(r, pre, false)
	if err != nil {
		return toolio.Outcome{}, err
	}

	for _, step := range []struct{ command, reason string }{
		{"submit", "submit the fix report, as JSON on stdin"},
		{"land", "measure the change in the checkout and land it"},
	} {
		r.Next(toolio.Next{Argv: []string{"af", "fix", step.command, "--run-id", r.RunDir.ID, "--dir", r.Dir}, Reason: step.reason})
	}
	return toolio.Outcome{
		Status:  toolio.StatusDone,
		Summary: fmt.Sprintf("rendered the brief of run %s: %d bytes, about %d tokens", r.RunDir.ID, len(start.document), estimatedTokens(start.document)),
		Result: fixBriefResult{
			BriefPath: start.briefPath, PrefixSHA256: start.state.PrefixSHA256, Base: start.state.Base, BaseHead: start.state.BaseHead,
			Baseline: gateRef(start.state), Table: start.state.Table,
			SubmitArgv: []string{"af", "fix", "submit", "--run-id", r.RunDir.ID, "--dir", r.Dir},
		},
	}, nil
}

// gateRef is the baseline of a run as a result names it, or nil when the run has none.
func gateRef(s fix.State) *fix.GateRef {
	if s.BaselineGateID == "" {
		return nil
	}
	return &fix.GateRef{GateID: s.BaselineGateID, Passed: s.BaselinePassed}
}

// refusePullRequest is the usage failure for an input that is a pull or merge
// request: a fix takes an issue, a file or text.
func refusePullRequest(r *toolio.Run) error {
	if r.Input.Kind == toolio.KindIssue && r.Input.Issue != nil && r.Input.Issue.IsPullRequest {
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("%s is a pull or merge request: fix takes an issue, a file or text", r.Input.Issue.URL())}
	}
	return nil
}

// warnIssueClosed raises the warning of an input that is a closed issue; the run goes on.
func warnIssueClosed(r *toolio.Run) {
	if r.Input.Kind == toolio.KindIssue && r.Input.Thread != nil && r.Input.Issue != nil && strings.EqualFold(r.Input.Thread.Issue.State, "closed") {
		r.Warn(fix.WarnIssueClosed, fmt.Sprintf("%s is closed; the run goes on", r.Input.Issue.URL()))
	}
}

// startedRun is a run whose preflight has passed and whose brief is written: what
// fix.json holds, and the brief the first round reads.
type startedRun struct {
	state     fix.State
	prefix    fix.Prefix
	task      string
	briefPath string
	// document is the brief as the first round reads it: the prefix, then the task block.
	document string
}

// startRun records what the preflight found, renders the brief of the first round,
// and writes brief.md, run.json and, last, fix.json: a run directory that has
// fix.json is a run the steps that follow can carry on.
func startRun(r *toolio.Run, pre fix.Preflighted, noVerify bool) (startedRun, error) {
	in := fix.Input{Kind: string(r.Input.Kind), Origin: r.Input.Origin, Body: r.Input.Body}
	prefix, err := fix.BuildPrefix(r.Ctx, fix.PrefixParams{Dir: r.Dir, Input: in, Table: pre.Detection.Gate, Baseline: pre.Baseline, Files: pre.Files})
	if err != nil {
		if r.Ctx.Err() != nil {
			return startedRun{}, r.Ctx.Err()
		}
		return startedRun{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	for _, w := range prefix.Warnings {
		r.Warn(w.Code, w.Message)
	}

	state := fix.State{
		RunID: r.RunDir.ID, Input: fix.StateInput{Kind: in.Kind, Origin: in.Origin, Key: fix.InputKey(in.Kind, in.Origin, in.Body)},
		Base: pre.Base, BaseHead: pre.BaseHead, Table: pre.Detection.Gate, NoVerify: noVerify,
		PrefixSHA256: prefix.SHA256, PrefixBytes: len(prefix.Text), Suite: pre.Detection.Suite, Programs: pre.Detection.Programs,
		Context: append([]string{}, r.Context...),
	}
	if r.Input.Kind == toolio.KindIssue && r.Input.Issue != nil && r.Input.Thread != nil {
		state.Issue = &fix.StateIssue{URL: r.Input.Issue.URL(), Title: r.Input.Thread.Issue.Title, Number: r.Input.Issue.Number}
		state.Target = fix.StateTarget{Repo: r.Input.Issue.Repo.String(), Host: r.Input.Issue.Repo.Host}
	} else if r.Target.Valid() {
		state.Target = fix.StateTarget{Repo: r.Target.String(), Host: r.Target.Host}
	}
	if b := pre.Baseline; b != nil {
		state.BaselineGateID, state.BaselinePassed, state.BaselineMS = b.ID, b.Passed, fix.GateMS(b)
	}

	earlier := fix.FindEarlier(r.Dir, state.Input.Key, r.Context, r.RunDir.ID)
	for i, e := range earlier {
		if !e.Found {
			r.Warn(fix.WarnQuestionNotFound, fmt.Sprintf("no earlier run asked the question that --context %d answers, so the answer stands alone", i+1))
		}
	}
	task := fix.TaskBlock(fix.TaskParams{RunID: r.RunDir.ID, Attempt: 1, Round: 1, Earlier: earlier})

	if err := writeRunFile(filepath.Join(r.RunDir.Path, "original.md"), []byte(in.Body)); err != nil {
		return startedRun{}, err
	}
	started := startedRun{state: state, prefix: prefix, task: task, briefPath: filepath.Join(r.RunDir.Path, "brief.md"), document: prefix.Text + task}
	if err := writeRunFile(started.briefPath, []byte(started.document)); err != nil {
		return startedRun{}, err
	}
	if err := placeRunJSON(r.RunDir, fix.KindName); err != nil {
		return startedRun{}, err
	}
	if err := state.Write(r.RunDir.Path); err != nil {
		return startedRun{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	record(r, ledgerFixBrief, map[string]any{"brief": started.briefPath, "prefix_sha256": prefix.SHA256, "document_bytes": len(started.document)})
	r.Artifact(toolio.Artifact{Kind: "brief", Path: started.briefPath})
	return started, nil
}

// writeRunFile writes a file of the run directory, atomically.
func writeRunFile(path string, data []byte) error {
	if err := agentdir.WriteFileAtomic(path, data); err != nil {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	return nil
}

// registerFixSubmit registers af fix submit: the submit command of the submission
// protocol with the fix report kind.
func registerFixSubmit(app *toolio.App) {
	cmd := submission.CommandWith("fix submit", fix.FixReport, submission.Options{
		ResultSchema: resultSchema("fix_submit"),
		Prepare: func(r *toolio.Run, run *toolio.RunDir) error {
			if _, err := fix.LoadState(run.Path); err != nil {
				return notAFixRun(run.ID, err)
			}
			return placeRunJSON(run, fix.KindName)
		},
	})
	cmd.Summary = "Submit the fix report of a run, as JSON on stdin"
	cmd.Usage = "af fix submit --run-id ID [--dir DIR] < report.json"
	cmd.DefaultTimeout, cmd.Timeout = 30*time.Second, true
	app.Register(cmd)
}
