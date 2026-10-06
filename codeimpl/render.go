package codeimpl

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
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
//
// The model's summary is stripped of any claim that the checks pass, and the
// body ends with the result of the gate as the tool measured it: nothing the
// model writes may assert a verification result.
func commitMessage(spec *afspec.Spec, task afspec.Task, sub Submission, repair *RepairSubmission, gate GateResult) string {
	subject := strings.TrimSuffix(strings.TrimSpace(sub.CommitSubject), ".")
	msg := "feat: " + subject
	if body := checks.StripClaims(strings.TrimSpace(sub.Summary)); body != "" {
		msg += "\n\n" + body
	}
	if gate.Ran() && gate.OK() {
		var cmds []string
		for _, c := range gate.Checks {
			cmds = append(cmds, "`"+c.Command+"`")
		}
		msg += "\n\nChecks (run by the tool): " + strings.Join(cmds, ", ") + " passed."
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

// resolveCommitMessage is the message of the commit the resolve phase's
// change lands as: a fix:, because it corrects the branch's own work, with
// the gate as the tool measured it.
func resolveCommitMessage(spec *afspec.Spec, sub ResolveSubmission, gate GateResult) string {
	msg := "fix: " + strings.TrimSuffix(strings.TrimSpace(sub.CommitSubject), ".")
	if body := checks.StripClaims(strings.TrimSpace(sub.Summary)); body != "" {
		msg += "\n\n" + body
	}
	if gate.Ran() && gate.OK() {
		var cmds []string
		for _, c := range gate.Checks {
			cmds = append(cmds, "`"+c.Command+"`")
		}
		msg += "\n\nChecks (run by the tool): " + strings.Join(cmds, ", ") + " passed."
	}
	return msg + "\n\n" + fmt.Sprintf("%s %s, %s", specTrailer, filepath.Base(spec.Dir), conformanceMarker) + "\n"
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
//
// It opens with what the change knowingly does not meet, and the summary line
// says the work is complete only when that list and the blocking findings are
// both empty: "N of N tasks landed" over an unmet requirement is the claim
// this body exists not to make.
func pullRequestBody(r *Result) string {
	lead, detail, tail := pullRequestParts(r)
	return lead + strings.Join(detail, "") + tail
}

// pullRequestParts is the body in the three parts fitPullRequest needs: the
// lead a reviewer must read first (what is not met, the summary, the tasks),
// the detail sections (each task's report and the conformance review), and
// the tail (the verification and the footer).
func pullRequestParts(r *Result) (lead string, detail []string, tail string) {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	b.WriteString(openingSections(r))

	p("## Summary\n\n")
	p("Implements specification `%s` (\"%s\"): %d of %d task(s) landed in this run", r.SpecDir,
		r.Title, r.TasksDone, r.TasksTotal)
	if r.TasksSkipped > 0 {
		p(", %d already done", r.TasksSkipped)
	}
	switch {
	case len(r.Blocking) > 0 || len(r.Unmet) > 0:
		p(". **The work is not complete:** %d unmet item(s) and %d blocking finding(s) are listed above",
			len(r.Unmet), len(r.Blocking))
	case r.Review != nil:
		p(", and an independent review found every requirement and test in scope met")
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
		p("**This is a baseline repair, not part of the specification.** The checks failed on the base "+
			"branch before any task ran, and `%s` restores them", r.Repair.Commit)
		if r.Repair.Model != "" {
			p(" (repaired on %s)", r.Repair.Model)
		}
		p(". **Cause:** %s\n\n%s\n\n", strings.TrimSpace(r.Repair.Submission.Cause),
			strings.TrimSpace(r.Repair.Submission.Summary))
		if strings.TrimSpace(r.Repair.Submission.Notes) != "" {
			p("**Notes:** %s\n\n", strings.TrimSpace(r.Repair.Submission.Notes))
		}
		if stat := strings.TrimSpace(r.Repair.DiffStat); stat != "" {
			p("What the repair changed, from git:\n\n```\n%s\n```\n\n", stat)
		} else if len(r.Repair.ChangedFiles) > 0 {
			p("Files the repair changed, from git: `%s`\n\n", strings.Join(r.Repair.ChangedFiles, "`, `"))
		}
	}

	if r.Survey != nil && len(r.Survey.Drift) > 0 {
		b.WriteString(driftSection(r.Survey.Drift))
	}

	lead = b.String()

	for _, t := range r.Tasks {
		if t.Submission == nil || t.Outcome != OutcomeDone {
			continue
		}
		b.Reset()
		p("### Task %d: %s\n\n%s\n\n", t.ID, t.Title, strings.TrimSpace(t.Submission.Summary))
		if t.Repair != nil && t.Repair.Outcome == OutcomeDone && t.Repair.Submission != nil {
			p("**The checks failed after this task and were repaired in its commit.** Cause: %s %s\n\n",
				strings.TrimSpace(t.Repair.Submission.Cause), strings.TrimSpace(t.Repair.Submission.Summary))
		}
		if dev := strings.TrimSpace(t.Submission.TestFirstDeviation); dev != "" {
			p("> ⚠️ **Test-first not followed.** %s\n\n", dev)
		}
		b.WriteString(verdictSection(t.Submission.TestVerdicts))
		if rc := t.RevertCheck; rc != nil {
			b.WriteString(revertCheckLine(*rc))
		}
		if strings.TrimSpace(t.Submission.Notes) != "" {
			p("**Notes:** %s\n\n", strings.TrimSpace(t.Submission.Notes))
		}
		detail = append(detail, b.String())
	}
	if c := conformanceSections(r); c != "" {
		detail = append(detail, c)
	}

	b.Reset()
	p("## Verification\n\n%s\n\n", verificationLines(r.Gate, r.Baseline, r.Verification))
	if r.FinalVerification != nil {
		b.WriteString(cleanEnvironmentLines(r.Gate, *r.FinalVerification, r.Environment))
	}
	p("---\n%s\n", footer)
	return lead, detail, b.String()
}

// fitPullRequest is the body to open the pull request with and the comments
// that follow it, each within limit bytes.
//
// A body that fits is sent whole. One that does not keeps the lead and the
// tail — what is not met, the tasks, the verification — and moves the detail
// sections to comments, packed at section boundaries: a forge refuses a body
// over its limit outright, and a pull request that is never opened is worse
// than one whose detail is a scroll away. The JSON report holds everything.
func fitPullRequest(r *Result, limit int) (body string, comments []string) {
	lead, detail, tail := pullRequestParts(r)
	if full := lead + strings.Join(detail, "") + tail; len(full) <= limit {
		return full, nil
	}
	// Room for the "(n of m)" heading each comment carries.
	const commentHeading = 64
	comments = packSections(detail, limit-commentHeading)
	for i, c := range comments {
		comments[i] = fmt.Sprintf("**The full account, continued (%d of %d)**\n\n%s", i+1, len(comments), c)
	}
	note := fmt.Sprintf("## The full account\n\nThe per-task reports and the conformance review do not fit in "+
		"this description (the forge accepts at most %d characters). They follow as %d comment(s) on this pull "+
		"request, and the run's JSON report holds them in full.\n\n", limit, len(comments))
	if room := limit - len(note) - len(tail); len(lead) > room {
		const cut = "\n\n*…cut to fit the forge's limit; the JSON report holds the rest.*\n\n"
		lead = truncateUTF8(lead, room-len(cut)) + cut
	}
	return lead + note + tail, comments
}

// packSections joins sections into as few pieces of at most max bytes as
// their order allows. A section larger than max is split at line breaks, and
// a line larger than max is cut.
func packSections(sections []string, max int) []string {
	var pieces []string
	for _, s := range sections {
		for len(s) > max {
			i := strings.LastIndex(s[:max], "\n")
			if i <= 0 {
				i = len(truncateUTF8(s, max))
			} else {
				i++
			}
			pieces = append(pieces, s[:i])
			s = s[i:]
		}
		if s != "" {
			pieces = append(pieces, s)
		}
	}
	var out []string
	var cur strings.Builder
	for _, p := range pieces {
		if cur.Len() > 0 && cur.Len()+len(p) > max {
			out = append(out, cur.String())
			cur.Reset()
		}
		cur.WriteString(p)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// truncateUTF8 is s cut to at most n bytes without splitting a character.
func truncateUTF8(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// openingSections is what a pull request opens with when the work is not
// done: the blocking findings, then the unmet requirements.
func openingSections(r *Result) string {
	var b strings.Builder
	if len(r.Blocking) > 0 {
		fmt.Fprintf(&b, "## ❌ Not ready: blocking findings\n\nThe conformance stage found %d problem(s) "+
			"that were neither fixed nor declared. This pull request is a draft until they are:\n\n", len(r.Blocking))
		for _, x := range r.Blocking {
			fmt.Fprintf(&b, "- **%s**: %s\n", x.Key, strings.TrimSpace(x.What))
		}
		b.WriteString("\n")
	}
	b.WriteString(conform.RenderUnmet(r.Unmet))
	return b.String()
}

// conformanceSections is the conformance stage's account: the review, what
// the resolve phase did, the structural findings left, and the files out of
// scope.
func conformanceSections(r *Result) string {
	var b strings.Builder
	if r.Review != nil {
		b.WriteString(conform.RenderReview(*r.Review, nil))
	}
	if res := r.Resolve; res != nil && res.Submission != nil {
		fmt.Fprintf(&b, "## Findings answered after the last task\n\n%s", strings.TrimSpace(res.Submission.Summary))
		if res.Commit != "" {
			fmt.Fprintf(&b, " (`%s`)", res.Commit)
		} else if res.Outcome == "discarded" {
			fmt.Fprintf(&b, " The change was discarded: %s.", res.Error)
		}
		b.WriteString("\n\n")
	}
	b.WriteString(conform.RenderFindings(r.Structural))
	if len(r.OutOfScope) > 0 {
		fmt.Fprintf(&b, "## Outside the spec's scope\n\n`%s`\n\n", strings.Join(r.OutOfScope, "`, `"))
	}
	return b.String()
}

// driftGroups orders the kinds of drift for a reader: what needs a
// reviewer's attention first.
var driftGroups = []struct{ kind, heading string }{
	{DriftBehaviorChange, "Behavior changes (review these)"},
	{DriftOpen, "Open items (not resolved by this change)"},
	{DriftInconsistency, "Inconsistencies in the spec"},
	{DriftSpecGap, "Spec gaps the tasks adapted to"},
}

// driftSection is the pull request's account of where the spec and the code
// disagreed. The survey found these before any task ran, so the section says
// so, and says what the section is not: a list of defects in the merged code.
func driftSection(drift []Drift) string {
	var b strings.Builder
	b.WriteString("## Spec deviations and how the tasks handled them\n\n")
	b.WriteString("These are mismatches between the spec and the code, found by a survey before any task " +
		"ran; they are not defects in the merged code. Each shows what the spec assumed, what the code did, " +
		"and the decision the tasks were given to follow.\n\n")
	byKind := map[string][]Drift{}
	for _, d := range drift {
		k := d.Kind
		if !slices.Contains(DriftKinds, k) {
			k = DriftSpecGap
		}
		byKind[k] = append(byKind[k], d)
	}
	for _, g := range driftGroups {
		items := byKind[g.kind]
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(&b, "### %s\n\n", g.heading)
		for _, d := range items {
			fmt.Fprintf(&b, "- **%s:** %s\n  - Decision: %s\n", d.SpecRef,
				strings.TrimSpace(d.Finding), strings.TrimSpace(d.Resolution))
		}
		b.WriteString("\n")
	}
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
		if red := strings.TrimSpace(v.RedEvidence); red != "" {
			fmt.Fprintf(&b, "  - red first: %s\n", red)
		}
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

// cleanEnvironmentLines is the final verification, run with an empty HOME and
// no global git configuration, and the environment it ran in.
func cleanEnvironmentLines(cmds []string, g GateResult, env *checks.Environment) string {
	var b strings.Builder
	b.WriteString("In a clean environment (an empty HOME, no global or system git configuration):\n\n")
	for i, cmd := range cmds {
		if i >= len(g.Checks) || !g.Checks[i].Ran() {
			fmt.Fprintf(&b, "⚠️ `%s` did not run.\n", cmd)
			continue
		}
		c := g.Checks[i]
		switch {
		case c.ExitCode == -1 || c.TimedOut:
			fmt.Fprintf(&b, "❌ `%s` could not run (%s).\n", cmd, runFailure(c))
		case c.OK:
			fmt.Fprintf(&b, "✅ `%s` passes (exit 0, %s).\n", cmd, ms(c.DurationMS))
		default:
			fmt.Fprintf(&b, "❌ `%s` fails (exit %d).\n", cmd, c.ExitCode)
		}
	}
	if env != nil {
		b.WriteString("\n| Environment | |\n|---|---|\n")
		fmt.Fprintf(&b, "| git | %s |\n| init.defaultBranch | %s |\n", orDash(env.GitVersion), orDash(env.InitDefaultBranch))
		if env.GoVersion != "" {
			fmt.Fprintf(&b, "| go | %s |\n", env.GoVersion)
		}
	}
	b.WriteString("\n")
	return b.String()
}

// revertCheckLine says what the tests did with the implementation taken out.
func revertCheckLine(rc conform.RevertResult) string {
	switch {
	case rc.Proves:
		return fmt.Sprintf("**Revert check:** with %s taken out, the checks fail (exit %d), so the tests "+
			"depend on the work.\n\n", "`"+strings.Join(rc.Reverted, "`, `")+"`", rc.Check.ExitCode)
	case rc.Ran:
		return "**Revert check:** ❌ " + rc.Reason + ".\n\n"
	default:
		return "**Revert check:** not run — " + rc.Reason + ".\n\n"
	}
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
