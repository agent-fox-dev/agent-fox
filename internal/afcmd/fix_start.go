package afcmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The ledger record of the brief of a fix run.
const ledgerFixBrief = "fix.brief"

// baseUsage is the help of --base.
const baseUsage = "the `branch` the fix is made on and goes back to; the one that is checked out when it is not given"

// refuseNonIssue is the usage failure for an input that is no issue: a text, a file,
// stdin, or a pull or merge request. A fix takes an issue, which names its branch and its
// pull request; a report that is not one is triaged first, which makes the issue.
func refuseNonIssue(r *toolio.Run) error {
	switch {
	case r.Input.Kind != toolio.KindIssue:
		return &toolio.Failure{
			Category: toolio.CategoryUsage,
			Message:  "fix takes an issue, by its URL; a text, a file or stdin is no issue: triage it first with af triage <report> --repo owner/name, which creates the issue",
		}
	case r.Input.Issue != nil && r.Input.Issue.IsPullRequest:
		return &toolio.Failure{Category: toolio.CategoryUsage, Message: fmt.Sprintf("%s is a pull or merge request: fix takes an issue", r.Input.Issue.URL())}
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
// and writes brief.md and, last, fix.json, whose question and answers a later run
// that resumes it reads.
func startRun(c *Config, r *toolio.Run, pre fix.Preflighted, noVerify bool) (startedRun, error) {
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

	earlier := c.earlierOf(r.Dir, state.Input.Key, r.Context, r.RunDir.ID)
	for i, e := range earlier {
		if !e.Found && !e.Material {
			r.Warn(fix.WarnQuestionNotFound, fmt.Sprintf("no earlier run asked the question that --context %d answers, so the answer stands alone", i+1))
		}
	}
	task := fix.TaskBlock(fix.TaskParams{RunID: r.RunDir.ID, Round: 1, Earlier: earlier})

	if err := writeRunFile(filepath.Join(r.RunDir.Path, "original.md"), []byte(in.Body)); err != nil {
		return startedRun{}, err
	}
	started := startedRun{state: state, prefix: prefix, task: task, briefPath: filepath.Join(r.RunDir.Path, "brief.md"), document: prefix.Text + task}
	if err := writeRunFile(started.briefPath, []byte(started.document)); err != nil {
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
