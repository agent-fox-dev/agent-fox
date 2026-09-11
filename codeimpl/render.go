package codeimpl

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// The footer every pull request this tool opens carries. It says what wrote
// it, because a reader deciding how much to trust a change should not have
// to work that out from the prose style.
const footer = "*Written by [`impl`](https://github.com/agent-fox-dev/agent-fox). Trust, but verify!*"

// specRef is how a commit names the package and the task: the trailer a
// later run reads to recognize its own work.
func specRef(spec *afspec.Spec, task afspec.Task) string {
	return fmt.Sprintf("%s %s, task %d", specTrailer, filepath.Base(spec.Dir), task.Id)
}

// repairRef is the trailer of the repair commit: the same shape as a
// task's, with "repair" where the task number goes, so that a later run
// recognizes a parked repair the way it recognizes a parked task.
func repairRef(spec *afspec.Spec) string {
	return fmt.Sprintf("%s %s, %s", specTrailer, filepath.Base(spec.Dir), repairMarker)
}

// repairCommitMessage is the message of the commit a landed repair makes.
// It is a fix:, not a feat:, and its body opens with the cause, because the
// commit is reviewed on its own and "why" is the review.
func repairCommitMessage(spec *afspec.Spec, sub RepairSubmission) string {
	subject := strings.TrimSuffix(strings.TrimSpace(sub.CommitSubject), ".")
	msg := "fix: " + subject
	var body []string
	if c := strings.TrimSpace(sub.Cause); c != "" {
		body = append(body, c)
	}
	if s := strings.TrimSpace(sub.Summary); s != "" {
		body = append(body, s)
	}
	if len(body) > 0 {
		msg += "\n\n" + strings.Join(body, "\n\n")
	}
	return msg + "\n\n" + repairRef(spec) + "\n"
}

// wipRepairMessage is what a repair that did not make the checks pass is
// parked as.
func wipRepairMessage(spec *afspec.Spec, reason string) string {
	return fmt.Sprintf("%s the repair of the checks before %s did not land\n\n%s\n\n%s\n",
		wipPrefix, filepath.Base(spec.Dir), strings.TrimSpace(reason), repairRef(spec))
}

// commitMessage is the message of the one commit a landed task makes.
//
// The subject comes from the model; the type prefix and the trailer do not,
// because those are facts about the run rather than judgements about the
// change.
//
// repair, when set, is the repair of the checks after the task, which the
// commit carries too: the body says so, with the cause, because a reader of
// the one commit has to be able to tell the task's change from the fix.
func commitMessage(spec *afspec.Spec, task afspec.Task, sub Submission, repair *RepairSubmission) string {
	subject := strings.TrimSuffix(strings.TrimSpace(sub.CommitSubject), ".")
	msg := "feat: " + subject
	if body := strings.TrimSpace(sub.Summary); body != "" {
		msg += "\n\n" + body
	}
	if repair != nil {
		msg += "\n\nThe checks failed after the task and were repaired in the same commit. " +
			strings.TrimSpace(repair.Cause)
		if s := strings.TrimSpace(repair.Summary); s != "" {
			msg += " " + s
		}
	}
	return msg + "\n\n" + specRef(spec, task) + "\n"
}

// holdCommitMessage is the commit that holds a task's work while its
// checks are repaired: every repair attempt starts from it, and it is
// undone — soft, keeping the work — before the task is committed for real.
// It carries the parked shape so that a run that dies mid-repair leaves a
// tip the next run knows to discard.
func holdCommitMessage(spec *afspec.Spec, task afspec.Task) string {
	return fmt.Sprintf("%s task %d of %s awaits the repair of its checks\n\n%s\n",
		wipPrefix, task.Id, filepath.Base(spec.Dir), specRef(spec, task))
}

// wipCommitMessage is what a task that did not land is parked as.
//
// The work is committed rather than left loose, because the phase edited
// real files and a branch you can reset is safer than a tree you have to
// untangle. The subject says `wip:` so that nothing downstream mistakes it
// for a finished task, and the trailer is what lets the next run discard
// it and start the task again.
func wipCommitMessage(spec *afspec.Spec, task afspec.Task, reason string) string {
	return fmt.Sprintf("%s task %d of %s did not land\n\n%s\n\n%s\n",
		wipPrefix, task.Id, filepath.Base(spec.Dir), strings.TrimSpace(reason), specRef(spec, task))
}

// pullRequestTitle names the spec, not a task: the pull request is the
// whole branch.
func pullRequestTitle(spec *afspec.Spec) string {
	return fmt.Sprintf("feat: %s (spec %s)", strings.TrimSpace(spec.Title), spec.SpecID)
}

// pullRequestBody is the PR description. Every line of every verification
// cell is rendered from a measured gate, so the body cannot claim a run
// that did not happen.
func pullRequestBody(r *Result) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("## Summary\n\n")
	p("Implements specification `%s` (\"%s\"): %d of %d task(s) landed in this run", r.SpecDir,
		r.Title, r.TasksDone, r.TasksTotal)
	if r.TasksSkipped > 0 {
		p(", %d already done", r.TasksSkipped)
	}
	p(".\n\n")

	p("## Tasks\n\n| # | Task | Outcome | Commit | Verification | Tests |\n|---|---|---|---|---|---|\n")
	for _, t := range r.Tasks {
		commit := orDash(t.Commit)
		if t.Commit != "" {
			commit = "`" + t.Commit + "`"
		}
		p("| %d | %s | %s | %s | %s | %s |\n", t.ID, t.Title, t.Outcome, commit,
			orDash(t.Verdict), orDash(t.TestsOutcome))
	}
	p("\n")

	if r.Repair != nil && r.Repair.Outcome == OutcomeDone && r.Repair.Submission != nil {
		p("## The checks were repaired first\n\n")
		p("They failed before any change, and `%s` restores them", r.Repair.Commit)
		if r.Repair.Model != "" {
			p(" (repaired on %s)", r.Repair.Model)
		}
		p(". **Cause:** %s\n\n%s\n\n", strings.TrimSpace(r.Repair.Submission.Cause),
			strings.TrimSpace(r.Repair.Submission.Summary))
		if strings.TrimSpace(r.Repair.Submission.Notes) != "" {
			p("**Notes:** %s\n\n", strings.TrimSpace(r.Repair.Submission.Notes))
		}
	}

	if r.Survey != nil && len(r.Survey.Drift) > 0 {
		p("## Where the spec and the code disagreed\n\n")
		for _, d := range r.Survey.Drift {
			p("- **%s:** %s → %s\n", d.SpecRef, strings.TrimSpace(d.Finding), strings.TrimSpace(d.Resolution))
		}
		p("\n")
	}

	for _, t := range r.Tasks {
		if t.Submission == nil || t.Outcome != OutcomeDone {
			continue
		}
		p("### Task %d: %s\n\n%s\n\n", t.ID, t.Title, strings.TrimSpace(t.Submission.Summary))
		if t.Repair != nil && t.Repair.Outcome == OutcomeDone && t.Repair.Submission != nil {
			p("**The checks failed after this task and were repaired in its commit.** Cause: %s %s\n\n",
				strings.TrimSpace(t.Repair.Submission.Cause), strings.TrimSpace(t.Repair.Submission.Summary))
		}
		b.WriteString(verdictSection(t.Submission.TestVerdicts))
		if strings.TrimSpace(t.Submission.Notes) != "" {
			p("**Notes:** %s\n\n", strings.TrimSpace(t.Submission.Notes))
		}
	}

	p("## Verification\n\n%s\n\n", verificationLines(r.Gate, r.Baseline, r.Verification))
	p("---\n%s\n", footer)
	return b.String()
}

// verdictSection answers a task's tests one by one. The verdict and the
// evidence are the model's; the list of ids and the pairing are not, so a
// test the task owns appears here whatever the run did about it.
func verdictSection(verdicts []Verdict) string {
	if len(verdicts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Per-test verdicts:**\n\n")
	for _, v := range verdicts {
		mark, label := "✅", "PASS"
		if !v.Passed() {
			mark, label = "❌", "FAIL"
		}
		fmt.Fprintf(&b, "- %s **%s**: %s — %s\n", mark, v.ID, label, strings.TrimSpace(v.Evidence))
	}
	b.WriteString("\n")
	return b.String()
}

// verificationLines renders the gate's commands with their first and last
// results. It takes GateResults rather than booleans, which is the whole
// point: there is no argument that produces a green tick out of nothing.
func verificationLines(cmds []string, baseline, last GateResult) string {
	if len(cmds) == 0 || !last.Ran() {
		return "⚠️ **Unverified.** No verification command ran, so nothing here has been checked automatically."
	}
	var lines []string
	for i, cmd := range cmds {
		var b, a checks.Result
		if i < len(baseline.Checks) {
			b = baseline.Checks[i]
		}
		if i < len(last.Checks) {
			a = last.Checks[i]
		}
		switch {
		case !a.Ran():
			lines = append(lines, fmt.Sprintf("⚠️ `%s` did not run after the last task.", cmd))
		case a.ExitCode == -1 || a.TimedOut:
			lines = append(lines, fmt.Sprintf("❌ `%s` could not run (%s).", cmd, runFailure(a)))
		case a.OK && b.Ran() && !b.OK:
			lines = append(lines, fmt.Sprintf("✅ `%s` passes (exit 0, %s). It was **already failing "+
				"before this branch** (exit %d), so the run did not start from green.", cmd, ms(a.DurationMS), b.ExitCode))
		case a.OK:
			lines = append(lines, fmt.Sprintf("✅ `%s` passes (exit 0, %s).", cmd, ms(a.DurationMS)))
		case b.Ran() && !b.OK:
			lines = append(lines, fmt.Sprintf("❌ `%s` fails (exit %d). It was also failing before this "+
				"branch (exit %d).", cmd, a.ExitCode, b.ExitCode))
		default:
			lines = append(lines, fmt.Sprintf("❌ `%s` fails (exit %d). It passed before this branch.", cmd, a.ExitCode))
		}
	}
	return strings.Join(lines, "\n")
}

func runFailure(r checks.Result) string {
	if r.TimedOut {
		return "timed out"
	}
	return "the program could not be started"
}

func ms(v int64) string {
	if v < 1000 {
		return fmt.Sprintf("%dms", v)
	}
	return fmt.Sprintf("%.1fs", float64(v)/1000)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
