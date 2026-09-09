package codefix

import (
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/checks"
)

// The footer every comment this tool posts carries. It says what wrote the
// comment, because a reader deciding how much to trust a diagnosis should not
// have to work that out from the prose style.
const footer = "*Written by [`fix`](https://github.com/agent-fox-dev/agent-fox). Trust, but verify!*"

// commitMessage is the message of the one commit a successful run makes.
//
// The subject comes from the model; the type prefix and the issue trailer do
// not, because those are facts about the run rather than judgements about the
// change.
func commitMessage(class Classification, impl Implementation, issue issueRef, body string) string {
	subject := strings.TrimSpace(impl.CommitSubject)
	subject = strings.TrimSuffix(subject, ".")
	msg := fmt.Sprintf("%s: %s", class.CommitType(), subject)
	if issue != nil {
		msg += fmt.Sprintf(" (#%d)", issue.Number)
	}
	if b := strings.TrimSpace(body); b != "" {
		msg += "\n\n" + b
	}
	if issue != nil {
		msg += fmt.Sprintf("\n\nCloses #%d", issue.Number)
	}
	return msg + "\n"
}

// wipCommitMessage is what an unverified change is parked as.
//
// The work is committed rather than left loose, because the implementation
// phase edited real files and a branch you can delete is safer than a dirty
// tree you have to untangle. The subject says `wip:` so that nothing
// downstream mistakes it for a finished change.
func wipCommitMessage(impl Implementation, issue issueRef, verdict checks.Verdict) string {
	subject := strings.TrimSpace(impl.CommitSubject)
	ref := ""
	if issue != nil {
		ref = fmt.Sprintf(" for #%d", issue.Number)
	}
	return fmt.Sprintf("wip: unverified change%s — %s\n\nThe project's checks did not pass "+
		"after this change (%s), so it was not landed.\n", ref, subject, verdict)
}

// analysisComment is what is posted to the issue before any code is written.
//
// It is posted after the pre-flight checks rather than before them. The skill
// posts it in step 5 and checks for a dirty working tree in step 6.1, so its
// most common failure leaves a public comment describing work that never
// began.
func analysisComment(a Analysis, branch, verifyCommand string, baseline checks.Result) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("## Analysis\n\n")
	p("**Classification:** %s\n\n", a.Classification)
	p("### Diagnosis\n\n%s\n\n", strings.TrimSpace(a.RootCause))
	p("### Planned fix\n\n%s\n\n", strings.TrimSpace(a.Approach))

	if len(a.Files) > 0 {
		p("**Files to change:**\n\n")
		for _, f := range a.Files {
			p("- `%s` — %s\n", f.Path, strings.TrimSpace(f.Change))
		}
		p("\n")
	}
	if len(a.Assumptions) > 0 {
		p("**Assumptions:**\n\n")
		for _, s := range a.Assumptions {
			p("- %s\n", strings.TrimSpace(s))
		}
		p("\n")
	}

	p("### Before the change\n\n")
	p("%s\n\n", baselineLine(verifyCommand, baseline))
	p("Work is on `%s`. Implementation follows.\n\n", branch)
	p("---\n%s\n", footer)
	return b.String()
}

// ambiguityComment is posted when the run stops to ask.
func ambiguityComment(a Ambiguity) string {
	return fmt.Sprintf(`## Clarification needed

I started on this and stopped: the report reads two ways, and the two lead to
different changes. Nothing was branched and no code was written.

**Question:** %s

**Interpretation A:** %s

**Interpretation B:** %s

Answer in a comment and re-run, and I will implement the one you name.

---
%s
`, strings.TrimSpace(a.Question), strings.TrimSpace(a.InterpretationA),
		strings.TrimSpace(a.InterpretationB), footer)
}

// summaryComment is posted after the work is done and landed.
//
// It is posted LAST, not before the commit as the skill has it, so it can
// carry the pull-request link and quote the exit status of the verification
// run that actually happened. Every line of the verification section is
// derived from a checks.Result, which is what makes it impossible for this
// function to print a green tick for a run that did not occur.
func summaryComment(r *Result) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("## Fix implemented\n\n%s\n\n", strings.TrimSpace(r.Summary))

	if len(r.ChangedFiles) > 0 {
		p("### Changes\n\n")
		described := map[string]string{}
		if r.Implementation != nil {
			for _, c := range r.Implementation.Changes {
				described[c.Path] = strings.TrimSpace(c.Change)
			}
		}
		p("| File | Change |\n|---|---|\n")
		for _, path := range r.ChangedFiles {
			p("| `%s` | %s |\n", path, orDash(described[path]))
		}
		p("\n")
	}
	if r.Implementation != nil && len(r.Implementation.Tests) > 0 {
		p("### Tests\n\n")
		for _, t := range r.Implementation.Tests {
			p("- %s\n", strings.TrimSpace(t))
		}
		p("\n")
	}

	p("### Verification\n\n%s\n\n", verificationLines(r.Baseline, r.Verification))

	p("### Where it is\n\n")
	p("- Branch: `%s`", r.Branch)
	if r.Commit != "" {
		p(" at `%s`", r.Commit)
	}
	p("\n")
	if r.PullRequestURL != "" {
		p("- Pull request: %s\n", r.PullRequestURL)
	} else if r.Pushed {
		p("- Pushed to `origin/%s`; no pull request was opened.\n", r.Branch)
	} else {
		p("- Committed locally; nothing was pushed.\n")
	}
	if r.Implementation != nil && strings.TrimSpace(r.Implementation.Notes) != "" {
		p("\n### Notes\n\n%s\n", strings.TrimSpace(r.Implementation.Notes))
	}
	p("\n---\n%s\n", footer)
	return b.String()
}

// failureComment is posted when code was written and the checks did not pass.
//
// It is the honest version of a run the skill would have reported as a
// success: the work exists, it is on a branch, and it is not landed.
func failureComment(r *Result) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("## Change written, not landed\n\n")
	p("%s\n\n", strings.TrimSpace(r.Summary))
	p("The project's own checks do not pass after the change, so nothing was landed. ")
	p("The work is parked on `%s` as a `wip:` commit", r.Branch)
	if r.Commit != "" {
		p(" (`%s`)", r.Commit)
	}
	p(", and the checkout is back on `%s`.\n\n", r.BaseBranch)

	p("### Verification\n\n%s\n\n", verificationLines(r.Baseline, r.Verification))
	if out := strings.TrimSpace(r.Verification.Output); out != "" {
		p("<details><summary>Output</summary>\n\n```\n%s\n```\n\n</details>\n\n", out)
	}
	p("---\n%s\n", footer)
	return b.String()
}

// pullRequestBody is the PR description.
func pullRequestBody(r *Result) string {
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	p("## Summary\n\n%s\n\n", strings.TrimSpace(r.Summary))
	if r.IssueNumber > 0 {
		p("Closes #%d\n\n", r.IssueNumber)
	}
	if strings.TrimSpace(r.RootCause) != "" {
		p("## Root cause\n\n%s\n\n", strings.TrimSpace(r.RootCause))
	}
	if len(r.ChangedFiles) > 0 {
		p("## Changes\n\n")
		described := map[string]string{}
		if r.Implementation != nil {
			for _, c := range r.Implementation.Changes {
				described[c.Path] = strings.TrimSpace(c.Change)
			}
		}
		p("| File | Change |\n|---|---|\n")
		for _, path := range r.ChangedFiles {
			p("| `%s` | %s |\n", path, orDash(described[path]))
		}
		p("\n")
	}
	if r.Implementation != nil && len(r.Implementation.Tests) > 0 {
		p("## Tests\n\n")
		for _, t := range r.Implementation.Tests {
			p("- %s\n", strings.TrimSpace(t))
		}
		p("\n")
	}
	if len(r.Assumptions) > 0 {
		p("## Assumptions\n\n")
		for _, a := range r.Assumptions {
			p("- %s\n", strings.TrimSpace(a))
		}
		p("\n")
	}
	p("## Verification\n\n%s\n\n", verificationLines(r.Baseline, r.Verification))
	if r.Implementation != nil && strings.TrimSpace(r.Implementation.Notes) != "" {
		p("## Notes\n\n%s\n\n", strings.TrimSpace(r.Implementation.Notes))
	}
	p("---\n%s\n", footer)
	return b.String()
}

// pullRequestTitle is the commit subject, which is the analysis title as the
// model wrote it.
func pullRequestTitle(class Classification, impl Implementation, issue issueRef) string {
	title := fmt.Sprintf("%s: %s", class.CommitType(), strings.TrimSpace(impl.CommitSubject))
	if issue != nil {
		title += fmt.Sprintf(" (#%d)", issue.Number)
	}
	return title
}

// verificationLines renders the two runs and their verdict.
//
// It takes checks.Results rather than booleans, which is the whole point: a
// caller cannot pass "true" for a run that never happened, and there is no
// argument to this function that produces a green tick out of nothing.
func verificationLines(baseline, after checks.Result) string {
	verdict := checks.Compare(baseline, after)
	switch verdict {
	case checks.VerdictUnverified:
		return "⚠️ **Unverified.** No verification command ran, so nothing here has been checked " +
			"automatically."
	case checks.VerdictPass:
		return fmt.Sprintf("✅ `%s` passes (exit 0, %s).", after.Command, ms(after.DurationMS))
	case checks.VerdictRepaired:
		return fmt.Sprintf("✅ `%s` passes (exit 0, %s). It was **already failing before this "+
			"change** (exit %d), so this run did not start from green.",
			after.Command, ms(after.DurationMS), baseline.ExitCode)
	case checks.VerdictStillFailing:
		return fmt.Sprintf("❌ `%s` fails (exit %d). It was also failing before this change "+
			"(exit %d), so the failure may not be this change's.",
			after.Command, after.ExitCode, baseline.ExitCode)
	default:
		return fmt.Sprintf("❌ `%s` fails (exit %d). It passed before this change, so this "+
			"change caused it.", after.Command, after.ExitCode)
	}
}

// baselineLine describes the state before any change.
func baselineLine(command string, baseline checks.Result) string {
	switch {
	case strings.TrimSpace(command) == "":
		return "No verification command could be detected for this project, so the change " +
			"will be reported as **unverified**."
	case !baseline.Ran():
		return fmt.Sprintf("`%s` was not run before the change.", command)
	case baseline.OK:
		return fmt.Sprintf("`%s` passed before the change (exit 0).", command)
	default:
		return fmt.Sprintf("`%s` was **already failing** before the change (exit %d). The result "+
			"will be reported by comparing before and after, not by requiring green.",
			command, baseline.ExitCode)
	}
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
