package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// stagePublished is the stage of a resumed delivery whose change is on hub's git server already: the branch of
// the plan of the landing is there at the commit of the plan.
const stagePublished = "published"

// finishPublished finishes the landing of a change that is published: nothing is measured, committed or
// pushed again, no lease is taken and no brain runs. The steps of the plan that the journal does not
// record as done are made from the plan (hub's branch push route, the pull request), and the result is the one
// that the landing would have had: the plan's branch, commit and verdict, and the pull request that was
// opened, by the delivery before or by this one.
func (r *Runner) finishPublished(ctx context.Context, a *attempt) *outcome {
	a.step("run")
	j := a.resume.journal
	plan := j.Landing.Plan
	internal := func(format string, args ...any) *outcome {
		return r.envelopeOf(a, &refusal{category: toolio.CategoryInternal, message: fmt.Sprintf(format, args...)})
	}
	var result fix.Result
	if err := json.Unmarshal(plan.Result, &result); err != nil {
		return internal("the result in the plan of the landing cannot be read: %v", err)
	}
	var warnings []toolio.Warning
	for _, w := range plan.Warnings {
		warnings = append(warnings, toolio.Warning{Code: w.Code, Message: w.Message})
	}

	if !j.Landing.HubPush {
		if err := r.publishBranch(a)(ctx, plan.Branch); err != nil {
			return r.hubOutcome(ctx, a, "publish the branch", err)
		}
		a.note(stepHubPush, func(j *recovery.Journal) { j.Landing.HubPush = true })
		r.reached(a, "hub_push")
	}
	pull := j.Landing.PullRequest
	if pull == nil && plan.PullRequest {
		created, err := a.forge.CreatePullRequest(ctx, a.repo, issuex.CreatePullRequestRequest{
			Title: plan.Title, Body: plan.Body, Head: plan.Branch, Base: j.Run.Base, Draft: plan.Draft,
		})
		effect := toolio.SideEffect{Kind: fix.EffectCreatePullRequest, Target: a.repo.String(), State: toolio.StateDone}
		switch {
		case err == nil:
			pull = &recovery.Ref{Number: created.Number, URL: firstNonEmpty(created.HTMLURL, created.URL)}
			a.note(stepPullRequest, func(j *recovery.Journal) { j.Landing.PullRequest = pull })
			a.sideEffects = append(a.sideEffects, effect)
			r.reached(a, "pull_request")
		case ctx.Err() != nil:
			return &outcome{err: context.Cause(ctx)}
		default:
			effect.State, effect.Detail = toolio.StateFailed, err.Error()
			a.sideEffects = append(a.sideEffects, effect)
			warnings = append(warnings, toolio.Warning{Code: fix.WarnPullRequestFailed, Message: fmt.Sprintf("the branch is pushed, but the pull request was not opened: %v", err)})
		}
	}

	result.Pushed = true
	if pull != nil {
		result.PullRequest = &fix.PullRequestRef{Number: pull.Number, URL: pull.URL, Draft: plan.Draft}
	}
	env := toolio.NewEnvelope(string(a.req.Kind))
	env.RunID = &a.runID
	env.Input = toolio.InputInfo{Kind: toolio.KindIssue, Origin: a.input.url}
	env.Status = toolio.Status(plan.Status)
	env.Summary = fmt.Sprintf("%s the change on the branch %s", plan.Outcome, plan.Branch)
	if pull != nil {
		env.Summary += ", pull request " + pull.URL
	}
	env.Result = result
	env.Warnings = warnings
	env.Artifacts = []toolio.Artifact{{Kind: "branch", Name: plan.Branch}, {Kind: "commit", Name: plan.Commit}}
	if pull != nil {
		env.Artifacts = append(env.Artifacts, toolio.Artifact{Kind: "pull_request", URL: pull.URL, Number: pull.Number})
	}
	runDir := filepath.Join(a.cloneDir, ".agent-fox", "run", j.Run.ID)
	env.Artifacts = append(env.Artifacts, toolio.Artifact{Kind: "run_dir", Path: runDir})
	if report := filepath.Join(runDir, "report.md"); fileExists(report) {
		env.Artifacts = append(env.Artifacts, toolio.Artifact{Kind: "report", Path: report})
	}
	env.Usage = toolio.Usage{
		Brain: a.settings.Brain, CostUSD: a.spent.CostUSD, CostSource: a.spent.CostSource,
		InputTokens: a.spent.InputTokens, OutputTokens: a.spent.OutputTokens, Turns: a.spent.Turns,
	}
	env.Seal(a.started, r.cfg.Now())
	data, err := env.Marshal()
	if err != nil {
		return internal("the envelope of the landing cannot be encoded: %v", err)
	}
	return &outcome{result: jobs.Result{Envelope: data, Report: a.reportOf(*env), MaxAttempts: a.maxAttempts}}
}

// firstNonEmpty is the first of the strings that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// fileExists says whether the path is a file.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
