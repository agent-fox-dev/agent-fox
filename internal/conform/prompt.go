package conform

import (
	"fmt"
	"strings"
	"time"
)

// ReviewSystemPrompt is the reviewer's mandate. It is written for a reader
// that did not write the change and has no stake in it passing.
const ReviewSystemPrompt = `You are an independent reviewer checking a finished change against the specification it claims to implement.

You did not write the change and you have not seen how it was written. That is
the point of this phase: the authors graded their own work already, and an
author grading its own work is how a pull request comes to claim a requirement
it knew it had not met. Read the code, not anyone's account of it.

This phase is read-only. You can read files, search, and run reporting
commands — git diff, git show, git log among them.

Method:

1. Read the specification. For each requirement id in scope, find what the
   code does about it. The question is the observable behaviour the
   requirement names — the exit code the process returns, the JSON a command
   prints, the rule a function applies — not whether a function with a
   plausible name exists. When a library between the code and the outcome
   changes it (a CLI framework that maps exit codes, a serializer that renames
   fields), the outcome is what the library produces. Answer implemented,
   partial, missing or different, with the file:line you read.
2. For each test id in scope, find the test that implements it and read it.
   Compare what it asserts with the contract the test spec names, and the
   real components the test spec says it drives:
   - a contract on a process exit code is asserted on the exit code of the
     process (or of the production entry point that sets it), not on an
     internal error value;
   - a contract on a classifier drives the classifier, not a sentinel compared
     with errors.Is;
   - a contract on wiring runs the production wiring function, not a copy of
     it built in the test.
   A test with no assertion, an assertion inside a branch that never runs, or
   a reference kept only to silence an unused import proves nothing.
   Answer asserts_contract, weaker, tautological, no_assertions or missing.
3. For each decision listed, check that the code does what it says. A note
   that reinterprets a decision into something else ("a parallel
   implementation, not a duplication") is not_followed.
4. Read the documentation the change wrote or edited — every quoted string,
   field name, enum value, exit code, endpoint path and sample payload — and
   check each against the code. Record every one the code contradicts.

Be exact rather than generous. A row you answer "implemented" on a hunch is
the failure this phase exists to catch; a row you answer "partial" with the
line that shows the gap is worth more than a clean table.

When the review is complete, call submit_review exactly once, with a row for
every id in scope.`

// ReviewPrompt renders what the reviewer is given.
func ReviewPrompt(in ReviewInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Review the change in %s against the specification below. The change is everything "+
		"between commit `%s` and the working tree: read it with `git diff %s` (add `--stat` for the "+
		"outline).\n\n", in.Root, in.Base, in.Base)
	if len(in.ChangedFiles) > 0 {
		b.WriteString("## The files the change touches, from git\n\n")
		for _, f := range in.ChangedFiles {
			fmt.Fprintf(&b, "- `%s`\n", f)
		}
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(in.DiffStat); s != "" {
		fmt.Fprintf(&b, "```\n%s\n```\n\n", s)
	}
	if len(in.TestCommands) > 0 {
		fmt.Fprintf(&b, "The project's checks are %s; they were run by the program and pass. Whether the "+
			"tests prove the contract is the question here, not whether they pass.\n\n",
			"`"+strings.Join(in.TestCommands, "`, `")+"`")
	}
	b.WriteString("## In scope\n\n")
	if len(in.Scope.Requirements) > 0 {
		fmt.Fprintf(&b, "Requirements: %s\n\n", strings.Join(in.Scope.Requirements, ", "))
	}
	if len(in.Scope.Tests) > 0 {
		fmt.Fprintf(&b, "Tests: %s\n\n", strings.Join(in.Scope.Tests, ", "))
	}
	if len(in.Scope.Decisions) > 0 {
		b.WriteString("Decisions recorded before the work started, which the change was bound to follow:\n\n")
		for _, d := range in.Scope.Decisions {
			fmt.Fprintf(&b, "- **%s:** %s\n", d.ID, strings.TrimSpace(d.Text))
		}
		b.WriteString("\n")
	}
	b.WriteString("## The specification\n\n")
	b.WriteString(strings.TrimSpace(in.Spec))
	b.WriteString("\n\n")
	if c := strings.TrimSpace(in.Context); c != "" {
		b.WriteString(c + "\n\n")
	}
	b.WriteString("Read the change, answer every id in scope, record every documentation statement the " +
		"code contradicts, and call " + ToolSubmitReview + ".")
	return b.String()
}

// DateLine is the sentence every writing phase is given, so that a date in
// a record is the date it was written rather than one a model chose.
func DateLine(now time.Time) string {
	return fmt.Sprintf("Today's date is %s, from the system clock. Any date you write — in an ADR, an "+
		"erratum, a changelog — is this one; never invent or estimate a date.\n\n", now.Format("2006-01-02"))
}
