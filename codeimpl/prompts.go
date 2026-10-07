package codeimpl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/repomap"
)

// surveySystemPrompt is the read-only mandate.
//
// It is the legacy reviewer's pre-flight mode with the two things that made
// its findings informational removed: the brief goes into every task prompt
// rather than into a store, and the one verdict it can give — a blocker —
// stops the run.
const surveySystemPrompt = `You are a senior engineer reading a specification against the repository it will be implemented in, before any code is written.

This phase is read-only. You can read files, search, and run reporting
commands; you cannot write, and you do not need to — the tasks are
implemented afterwards, one phase each, and every one of them opens with
your brief.

Method:

1. Read the spec: the PRD, the requirements, the tests, the tasks. Note every
   module, type, command, file and library it names.
2. Locate each of them in the code. Read the file it lives in, use find_references for its callers,
   and read its tests. Where the spec expects something that does not exist, say
   where it should be created and what it should look like to fit.
3. Learn the conventions a coder here must follow: the test framework and
   where tests live, how errors are handled, how packages are wired, what
   the linter enforces, what the Makefile does. Read a neighbouring test
   file rather than guessing a style.
4. Compare the spec's assumptions with the code as it stands. Every place
   they disagree is drift: record what the spec assumes, what the code does,
   the decision (written in the past tense, as it will read in the pull
   request: "Followed the code: ..."), and its kind: spec_gap for a detail
   the tasks adapt to, behavior_change for work that alters what existing
   code does, inconsistency for a spec that contradicts itself, open for an
   edge case nothing settles. Prefer the code's reality over the
   spec's assumption when the spec is merely wrong about a detail, and the
   spec's intent over the code when the code is what the spec changes.
5. Look at every task's touches and steps and say what a coder will need to
   know that the spec does not tell them.

On a spec that cannot be implemented as written — it names a module the PRD
does not say to create and nothing in the code plays that role, or it
contradicts an invariant the code enforces — set blocker. That stops the run
and asks a person, which is expensive and is the right answer perhaps one
time in twenty. Everything smaller is drift with a resolution.

When the survey is complete, call submit_survey exactly once.`

// implementSystemPrompt is the implementation mandate.
//
// It is the legacy coder profile rewritten for format v2 — every task is
// test-first for the tests it owns, there is no "group 1 writes the tests"
// — and for a program that enforces what the profile could only ask for.
// The sentences about git, the spec package and the commit are the ones that
// save a turn: each is a refusal the model would otherwise discover.
const implementSystemPrompt = `You are a senior engineer implementing one task of a specification in an existing repository.

You have the specification scoped to your task, a survey of how it maps onto
this code, and the reports of the tasks landed before yours. Implement this
task and no other.

Method:

1. Orient yourself: read the files the task touches and their tests, and the
   survey's notes on them. Paths in the spec are what the author expected;
   confirm them against the code before acting.
2. Work test-first. Write every test the task owns from its entry in the test
   spec — given, when, then, pseudocode — in the project's own framework and
   layout, and name each test so its id (for example TS-05-3) appears in the
   function name or a comment. Write and run them RED before any implementation
   file changes: run them and see them fail, and keep the command and the
   failure, because you report them.
   If the tests cannot compile until the task introduces a new function
   signature, type or interface method, do not implement it yet. First add the
   minimal stub that lets them compile (an empty body, a zero value, or a panic
   or "not implemented" error), then run the tests and confirm they fail on
   behaviour, not on a compile error. Only then implement. If a compile failure
   is the only red there can be, say so in the evidence.
3. Implement the task's steps, in order, until those tests pass. Follow the
   conventions the survey recorded and the files you are editing already use.
4. Introduce nothing unrelated. A "while I was here" cleanup makes the change
   harder to review and harder to revert. Do not touch other tasks' work.
5. Check your work with targeted runs: the tests you wrote, then the package
   you changed — the suite's command narrowed to them (when the project's
   language is known, its targeted form is named below). The program runs the
   task's full check command itself after you submit, and a task is judged by
   that run alone: running the whole suite here only repeats it, at a minute
   or more each, so the shell refuses it. The linter is allowed. Run the
   targeted tests again after your last edit.
6. Update the documentation the change makes wrong, from the code: every
   quoted string, field name, enum value, exit code, endpoint path and sample
   payload you write is copied from the code or a test, not from the PRD, and
   you report the line it came from in doc_sources. When the code does not do
   what the spec asks, the documentation says what the code does.

Anything you know the work does not meet — a requirement, a test's contract,
a decision from the survey — goes in deviations, with the reason and the
erratum you wrote for it. An independent reviewer reads the finished change
against the spec after the last task; a gap you declare is reported as one, a
gap you bury in notes is reported as a defect.

Do not write in your summary that tests or checks pass. The program runs them
and states the result itself; a sentence of yours that asserts one is removed
from the commit message.

Constraints that are mechanical, not advisory:

- git is limited to its read-only subcommands. The branch already exists and
  you are on it; the task's state, the commit and the push are made by the
  program after this phase, from what you submit. Do not try to commit.
- The spec package is read-only to you: writes under it are refused. If the
  implementation has to diverge from the spec, do what is right for the code
  and say exactly how and why in your report's notes.
- gh is not available. Your file tools cannot reach outside the repository.

When the work is done and the checks pass, call submit_task exactly once
with an honest report: a verdict and evidence for every test the task owns,
and for every done_when entry. Each test verdict carries red_evidence: the
command you ran and the failure you saw before you implemented. If running
the tests red first was impossible (a pure refactor, a docs-only change),
set test_first_deviation with the reason instead; it is shown to the
reviewer as a deviation, so do not bury it in notes. Do not claim a test you did not write or a
check you did not run — both are verified afterwards. Report a failing test
as fail with why: a criterion recorded as failed with its reason is worth
more than one claimed passed on a run that never happened.

If the task cannot be implemented as specified and reading the code cannot
settle how to proceed, set blocker instead of guessing; that stops the run
and asks a person. Small doubts are resolved and recorded in notes.`

// repairSystemPrompt is the mandate of the phase that runs only when the
// operator asked for it and the checks are red: before any task, or after
// the integration task.
//
// It is narrower than the implementation mandate on purpose: the phase
// exists to make the spec's own checks pass, and nothing it could do toward
// the spec is worth the confusion of a commit that does both. The one
// temptation it names — a failing test made to pass by deleting it — is
// named because it is the shortest route to green and the wrong one.
const repairSystemPrompt = `You are a senior engineer restoring a repository's own checks to green, at one of two moments: before a specification is implemented in it, or after the specification's last task, when the whole suite is what decides whether the work lands.

The checks failed. Your job is to find out why and fix the cause, so that
the checks pass. You are not implementing the specification: what it says
and what is not yet done are not yours to do, and it is given to you so you
know what the code is for.

Method:

1. Run the failing command yourself and read the whole output, not the last
   line. Find the first real error; later ones are often its consequences.
2. Find the cause. A stale fixture, a test that depends on the environment,
   a dependency that moved, a lint rule that tightened, a real bug the tests
   are right about. Read the code the failure points at and its history.
3. Fix the cause, not the symptom. A test that fails because the code is
   wrong is fixed in the code. A test that fails because it is wrong about
   the code is fixed in the test, and your report says why the code is
   right. Never delete, skip or weaken a test to make the run green unless
   the test is genuinely obsolete — and then say so, with the reason, in
   your report.
4. Keep the change to what the checks need. This commit is reviewed on its
   own, before the spec's work, and a reader has to be able to see that it
   only repairs.
5. Run the checks again — every command, in the order listed — and see them
   pass. You are judged by them afterwards, so there is no benefit in
   submitting before they do.

Constraints that are mechanical, not advisory:

- git is limited to its read-only subcommands. The branch already exists and
  you are on it; the commit is made by the program after this phase, from
  what you submit. Do not try to commit.
- The spec package is read-only to you: writes under it are refused.
- gh is not available. Your file tools cannot reach outside the repository.

When the checks pass, call submit_repair exactly once with an honest report:
what was wrong, what you changed, and anything a reviewer should know.

If the failure is not in the code — the checks need a credential, a service,
or a tool this machine does not have, and no change to the repository could
make them pass — set blocker instead of working around it; that stops the
run and asks a person.`

// maxInstructionBytes bounds what a project's own instruction file may
// contribute to a prompt.
const maxInstructionBytes = 24 << 10

// projectInstructions reads the repository's own agent instructions, if it
// has one that is small enough to inline. It is rendered into the prompt of
// the phase that writes code, labelled as repository-authored material — the
// weaker and more appropriate placement than the system prompt.
func projectInstructions(root string) string {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if b := readSmall(filepath.Join(root, name)); b != "" {
			return b
		}
	}
	return ""
}

// steering reads .specs/steering.md, the project-level directives this
// repository's own instructions say every agent must read.
func steering(specsDir string) string { return project.Steering(specsDir) }

func readSmall(path string) string {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 || len(b) > maxInstructionBytes {
		return ""
	}
	return string(b)
}

// gateBlock tells a phase what it will be judged by, and what the checks
// did before anything changed.
func gateBlock(cmds []string, baseline GateResult) string {
	var b strings.Builder
	if len(cmds) == 0 {
		b.WriteString("No verification command runs in this run, so the change cannot be verified " +
			"automatically. Be correspondingly careful, and say in your report how you convinced " +
			"yourself the work is correct.\n")
		return b.String()
	}
	b.WriteString("The task lands only if these commands pass afterwards, run by the program in this order:\n\n")
	for i, cmd := range cmds {
		fmt.Fprintf(&b, "%d. `%s`", i+1, cmd)
		if i < len(baseline.Checks) {
			r := baseline.Checks[i]
			switch {
			case !r.Ran():
				b.WriteString(" — not run before the change")
			case r.OK:
				b.WriteString(" — PASSED before the change, so anything it reports afterwards is yours")
			default:
				fmt.Fprintf(&b, " — was ALREADY FAILING before the change (exit %d); the run compares "+
					"before and after rather than requiring green, so do not chase pre-existing failures "+
					"unless they are this task's", r.ExitCode)
			}
		}
		b.WriteString("\n")
	}
	for i, r := range baseline.Checks {
		if r.Ran() && !r.OK && strings.TrimSpace(r.Output) != "" && i < len(cmds) {
			fmt.Fprintf(&b, "\nThe output of `%s` before the change ended:\n\n```\n%s\n```\n", cmds[i], r.Output)
		}
	}
	return b.String()
}

// repoMapBlock renders the '## Repository map' section with the blank line
// that separates it from what follows, or nothing for an empty map, so a
// prompt built without a map is byte-identical to one built before the map
// existed (14-REQ-5.3). It sits after the language block and ahead of the
// specification and of every prior phase's conclusions (14-REQ-5.1).
func repoMapBlock(m string) string {
	if block := repomap.Block(m); block != "" {
		return block + "\n"
	}
	return ""
}

func surveyPrompt(in surveyInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Survey the specification below against the code in %s. ", in.Root)
	fmt.Fprintf(&b, "This run will implement %d task(s) of it, in order, one phase each.\n\n", len(in.Pending))
	b.WriteString(languageBlock(in.Profile))
	b.WriteString(repoMapBlock(in.RepoMap))
	b.WriteString("## The specification\n\n")
	b.WriteString(in.Spec.RenderCombined())
	b.WriteString("\n")
	b.WriteString(externalAPIsBlock(in.Spec))
	b.WriteString("## Tasks this run will implement\n\n")
	for _, t := range in.Pending {
		fmt.Fprintf(&b, "- %d. %s (%s)\n", t.Id, t.Title, t.Kind)
	}
	b.WriteString("\n## Verification\n\n")
	b.WriteString(gateBlock(in.Gate, in.Baseline))
	if in.Repair {
		b.WriteString("\nThe run will repair the failing checks in a phase of its own before the first " +
			"task. If you see the cause while reading, record it in the summary; do not resolve it " +
			"as drift.\n")
	}
	if in.Context != "" {
		b.WriteString("\n" + strings.TrimSpace(in.Context) + "\n")
	}
	b.WriteString("\nRead the code, locate what the spec names, record the conventions and the drift, " +
		"and call " + ToolSubmitSurvey + ".")
	return b.String()
}

func repairPrompt(in repairInput) string {
	var b strings.Builder
	spec := in.Spec
	if in.Task == nil {
		fmt.Fprintf(&b, "Repair the checks of the repository in %s, so that specification %s (\"%s\") "+
			"can be implemented on a green baseline. You are on branch `%s`, created for that work.",
			in.Root, filepath.Base(spec.Dir), spec.Title, in.Branch)
	} else {
		fmt.Fprintf(&b, "Repair the checks of the repository in %s: they failed after task %d (\"%s\"), "+
			"the integration task of specification %s (\"%s\"), was implemented. You are on branch `%s`.",
			in.Root, in.Task.Id, in.Task.Title, filepath.Base(spec.Dir), spec.Title, in.Branch)
	}
	if in.Attempts > 1 {
		fmt.Fprintf(&b, " This is attempt %d of %d.", in.Attempt, in.Attempts)
	}
	b.WriteString("\n\n")
	if !in.Now.IsZero() {
		b.WriteString(conform.DateLine(in.Now))
	}
	b.WriteString(languageBlock(in.Profile))
	b.WriteString(repoMapBlock(in.RepoMap))

	if in.Task != nil {
		b.WriteString("## Where the work stands\n\n")
		b.WriteString("The task's change is the commit at HEAD, made provisionally so that you can read it " +
			"(`git show HEAD`); the tasks before it are the commits below it on this branch, each " +
			"landed with the checks green. The integration task is the one that runs the spec's " +
			"smoke tests against the real components, so a failure here is often a wiring gap " +
			"between earlier tasks, not a fault of this one. Fix the cause wherever it is. Do not " +
			"undo the task's work and do not remove or weaken the tests it wrote: they are what " +
			"found the gap.\n\n")
		if len(in.Task.Tests) > 0 {
			fmt.Fprintf(&b, "The task owns these tests: %s.\n\n", strings.Join(in.Task.Tests, ", "))
		}
	}

	b.WriteString("## What failed\n\n")
	if in.Task == nil {
		b.WriteString("The program ran these commands, in this order, before any change:\n\n")
	} else {
		b.WriteString("The program ran these commands, in this order, after the task:\n\n")
	}
	for i, cmd := range in.Gate {
		fmt.Fprintf(&b, "%d. `%s`", i+1, cmd)
		if i < len(in.Failing.Checks) {
			r := in.Failing.Checks[i]
			switch {
			case !r.Ran():
				b.WriteString(" — not run")
			case r.OK:
				b.WriteString(" — passed")
			case r.TimedOut:
				b.WriteString(" — TIMED OUT")
			default:
				fmt.Fprintf(&b, " — FAILED (exit %d)", r.ExitCode)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	for _, r := range in.Failing.failing() {
		if strings.TrimSpace(r.Output) == "" {
			continue
		}
		fmt.Fprintf(&b, "The output of `%s` ended:\n\n```\n%s\n```\n\n", r.Command, checks.Tail(r.Output, 80))
	}
	b.WriteString("The repair lands only if every command above passes afterwards, run by the program " +
		"in the same order. Every command has to pass, including any that passed before.\n\n")

	if in.Survey != nil {
		b.WriteString(surveyBlock(*in.Survey))
	}
	if len(in.Prior) > 0 {
		b.WriteString("## Tasks landed in this run\n\n")
		for _, p := range in.Prior {
			fmt.Fprintf(&b, "- Task %d: %s — %s", p.ID, p.Title, strings.TrimSpace(p.Summary))
			if len(p.Files) > 0 {
				fmt.Fprintf(&b, " (%s)", strings.Join(p.Files, ", "))
			}
			b.WriteString("\n")
			for _, g := range p.Gotchas {
				fmt.Fprintf(&b, "  - gotcha: %s\n", strings.TrimSpace(g))
			}
		}
		b.WriteString("\n")
	}
	if in.Previous != nil {
		b.WriteString(previousAttemptBlock(*in.Previous, "the repair"))
	}

	if in.Task == nil {
		b.WriteString("## The specification that will be implemented afterwards\n\n")
	} else {
		b.WriteString("## The specification the work implements\n\n")
	}
	b.WriteString("For orientation only: do not implement any of it.\n\n")
	b.WriteString(strings.TrimSpace(spec.RenderCombined()))
	b.WriteString("\n\n")

	if in.Instructions != "" {
		b.WriteString("## Project instructions\n\n")
		b.WriteString("The repository ships these. Follow them where they apply.\n\n")
		b.WriteString("--- BEGIN PROJECT INSTRUCTIONS ---\n")
		b.WriteString(strings.TrimSpace(in.Instructions))
		b.WriteString("\n--- END PROJECT INSTRUCTIONS ---\n\n")
	}
	if in.Steering != "" {
		b.WriteString("## Steering\n\n")
		b.WriteString("The project's own directives to every agent working on it.\n\n")
		b.WriteString("--- BEGIN STEERING ---\n")
		b.WriteString(strings.TrimSpace(in.Steering))
		b.WriteString("\n--- END STEERING ---\n\n")
	}
	b.WriteString("Run the failing command, find the cause, fix it, run every check and see it pass, " +
		"then call " + ToolSubmitRepair + ".")
	return b.String()
}

func taskPrompt(in taskInput) string {
	var b strings.Builder
	spec := in.Spec
	task := in.Task
	fmt.Fprintf(&b, "Implement task %d of specification %s (\"%s\") in %s. You are on branch `%s`, "+
		"created for this work.", task.Id, filepath.Base(spec.Dir), spec.Title, in.Root, in.Branch)
	if in.Attempts > 1 {
		fmt.Fprintf(&b, " This is attempt %d of %d.", in.Attempt, in.Attempts)
	}
	b.WriteString("\n\n")
	if !in.Now.IsZero() {
		b.WriteString(conform.DateLine(in.Now))
	}
	if len(in.Scope) > 0 {
		fmt.Fprintf(&b, "The spec restricts the files a change may touch to what its tasks list and the "+
			"survey's decisions name: %s. Documentation is exempt, and so is a test file that names one of "+
			"the spec's test ids. A file outside that list is reported in the pull request as a change the "+
			"spec did not ask for: a \"while I was here\" fix belongs in a separate change.\n\n",
			"`"+strings.Join(in.Scope, "`, `")+"`")
	}
	if task.Kind == afspec.TaskKindIntegration {
		b.WriteString("This is the spec's integration task: it exists to catch the wiring gaps that " +
			"component tests cannot see. Its smoke tests run against real components, and an execution " +
			"path that is not live in production code fails it — an erratum or a deferral does not " +
			"satisfy it.\n\n")
	}
	b.WriteString(languageBlock(in.Profile))
	b.WriteString(repoMapBlock(in.RepoMap))

	b.WriteString("## The specification, scoped to this task\n\n")
	b.WriteString("Requirements the task does not own are listed by id only; other tasks are one line each. " +
		"Everything the task owns is rendered in full.\n\n")
	rendered := spec.RenderIndividualScoped(task.Id)
	for _, key := range []string{"prd", "architecture", "requirements", "test_spec", "tasks"} {
		if s := strings.TrimSpace(rendered[key]); s != "" {
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", sectionTitle(key), s)
		}
	}
	b.WriteString(externalAPIsBlock(spec))

	b.WriteString("## Definition of done\n\n")
	b.WriteString("The format defines it, and the program checks what it can:\n\n")
	fmt.Fprintf(&b, "- the tests this task owns (%s) exist, run and pass — you answer for each, by id;\n",
		strings.Join(task.Tests, ", "))
	b.WriteString("- the project's checks below pass afterwards — the program runs them;\n")
	if len(task.DoneWhen) > 0 {
		b.WriteString("- every done_when entry holds — you answer for each, as DW-n:\n")
		for i, d := range task.DoneWhen {
			fmt.Fprintf(&b, "  - DW-%d: %s\n", i+1, d)
		}
	}
	b.WriteString("\n")

	b.WriteString("## Verification\n\n")
	b.WriteString(gateBlock(in.Gate, in.Baseline))
	b.WriteString("\n")

	if in.Survey != nil {
		b.WriteString(surveyBlock(*in.Survey))
	}
	if len(in.Prior) > 0 {
		b.WriteString("## Tasks landed before this one, in this run\n\n")
		for _, p := range in.Prior {
			fmt.Fprintf(&b, "### Task %d: %s\n\n%s\n", p.ID, p.Title, strings.TrimSpace(p.Summary))
			if len(p.Files) > 0 {
				fmt.Fprintf(&b, "\nFiles: %s\n", strings.Join(p.Files, ", "))
			}
			if len(p.Gotchas) > 0 {
				b.WriteString("\nGotchas:\n")
				for _, g := range p.Gotchas {
					fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(g))
				}
			}
			b.WriteString("\n")
		}
	}
	if in.Previous != nil {
		b.WriteString(previousAttemptBlock(*in.Previous, "this task"))
	}
	if in.Instructions != "" {
		b.WriteString("## Project instructions\n\n")
		b.WriteString("The repository ships these. Follow them where they apply to the task.\n\n")
		b.WriteString("--- BEGIN PROJECT INSTRUCTIONS ---\n")
		b.WriteString(strings.TrimSpace(in.Instructions))
		b.WriteString("\n--- END PROJECT INSTRUCTIONS ---\n\n")
	}
	if in.Steering != "" {
		b.WriteString("## Steering\n\n")
		b.WriteString("The project's own directives to every agent working on it.\n\n")
		b.WriteString("--- BEGIN STEERING ---\n")
		b.WriteString(strings.TrimSpace(in.Steering))
		b.WriteString("\n--- END STEERING ---\n\n")
	}
	if in.Context != "" {
		b.WriteString(strings.TrimSpace(in.Context) + "\n\n")
	}
	b.WriteString("Write the tests, stub any new API they need to compile, see them fail, implement " +
		"the steps, run the targeted tests, then call " + ToolSubmitTask + ".")
	return b.String()
}

func sectionTitle(key string) string {
	switch key {
	case "prd":
		return "PRD"
	case "architecture":
		return "Architecture"
	case "requirements":
		return "Requirements"
	case "test_spec":
		return "Test specification"
	default:
		return "Tasks"
	}
}

// externalAPIsBlock renders the requirements' external_apis, which the
// scoped render omits and which a coder is the one to need: the format says
// an unverified signature is an assumption to confirm before use.
func externalAPIsBlock(spec *afspec.Spec) string {
	if spec.Requirements == nil || len(spec.Requirements.ExternalApis) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### External APIs\n\n")
	b.WriteString("Signatures the spec relies on. One marked UNVERIFIED is the PRD's assumption: " +
		"confirm it against the installed package before using it.\n\n")
	for _, api := range spec.Requirements.ExternalApis {
		status := "verified"
		if !api.Verified {
			status = "UNVERIFIED"
		}
		fmt.Fprintf(&b, "- `%s` %s (%s)\n", api.Package, api.Version, status)
		for _, sym := range api.Symbols {
			fmt.Fprintf(&b, "  - `%s` from `%s`: `%s`\n", sym.Name, sym.ImportPath, sym.Signature)
		}
	}
	b.WriteString("\n")
	return b.String()
}

func surveyBlock(s Survey) string {
	var b strings.Builder
	b.WriteString("## Survey of this repository against the spec\n\n")
	b.WriteString(strings.TrimSpace(s.Summary) + "\n\n")
	if len(s.Conventions) > 0 {
		b.WriteString("**Conventions:**\n\n")
		for _, c := range s.Conventions {
			fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(c))
		}
		b.WriteString("\n")
	}
	if len(s.Locations) > 0 {
		b.WriteString("**Where things live:**\n\n")
		for _, l := range s.Locations {
			fmt.Fprintf(&b, "- %s: `%s`", l.Name, l.Path)
			if strings.TrimSpace(l.Note) != "" {
				fmt.Fprintf(&b, " — %s", strings.TrimSpace(l.Note))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(s.Drift) > 0 {
		b.WriteString("**Where the spec and the code disagree, and what to do:**\n\n")
		for _, d := range s.Drift {
			fmt.Fprintf(&b, "- **%s:** %s → %s\n", d.SpecRef, strings.TrimSpace(d.Finding), strings.TrimSpace(d.Resolution))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// previousAttemptBlock tells an attempt what the one before it did and why
// it was thrown away. subject is "this task" or "the repair".
func previousAttemptBlock(f attemptFailure, subject string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## The previous attempt at %s\n\n", subject)
	if f.Kept {
		fmt.Fprintf(&b, "Its work is still in the tree: the checks passed with it, so the code is not what "+
			"is wrong. With `%s` put back as they were before it, the checks still passed, so the tests the "+
			"task owns do not depend on that code. Strengthen those tests until they fail without it — drive "+
			"the component the code wires in and assert its observable outcome — and do not rewrite the "+
			"implementation or redo the documentation; change them only where a test shows them wrong.\n\n",
			strings.Join(f.Reverted, "`, `"))
		if strings.TrimSpace(f.DiffStat) != "" {
			fmt.Fprintf(&b, "It changed:\n\n```\n%s\n```\n\n", strings.TrimSpace(f.DiffStat))
		}
		return b.String()
	}
	b.WriteString("It was discarded: the branch is back at the commit it started from and nothing of it " +
		"remains in the tree. ")
	fmt.Fprintf(&b, "It did not land because %s\n\n", strings.TrimSpace(f.Reason))
	if f.Gate != nil {
		for _, c := range f.Gate.failing() {
			fmt.Fprintf(&b, "`%s` exited %d", c.Command, c.ExitCode)
			if c.TimedOut {
				b.WriteString(" (timed out)")
			}
			b.WriteString(". Its output ended:\n\n```\n")
			b.WriteString(checks.Tail(c.Output, 40))
			b.WriteString("\n```\n\n")
		}
	}
	if len(f.Verdicts) > 0 {
		b.WriteString("It reported these as not passing:\n\n")
		for _, v := range f.Verdicts {
			fmt.Fprintf(&b, "- **%s:** %s\n", v.ID, strings.TrimSpace(v.Evidence))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(f.DiffStat) != "" {
		fmt.Fprintf(&b, "It had changed:\n\n```\n%s\n```\n\n", strings.TrimSpace(f.DiffStat))
	}
	b.WriteString("Do not repeat it. Read what failed, understand why, and take a different route " +
		"where the first one was wrong.\n\n")
	return b.String()
}

// languageBlock says what the project is, so the phase does not have to
// find out.
func languageBlock(p project.Profile) string {
	if !p.Known() {
		return ""
	}
	block := fmt.Sprintf("This project is **%s**, detected from `%s`. A stub marker in this "+
		"language is `%s`.", p.Language, p.Manifest, p.StubMarker)
	if t := p.TargetedRun(); t != "" {
		block += fmt.Sprintf(" A targeted test run — one test or one file — looks like `%s`.", t)
	}
	return block + "\n\n"
}

// resolveSystemPrompt is the mandate of the phase that answers the
// conformance stage. It is a repair with a different bar: not "the checks
// pass" but "the change does what the spec says, or says plainly where it
// does not".
const resolveSystemPrompt = `You are a senior engineer answering an independent review of a finished change, before it is presented as done.

Every task of the specification has landed and the project's checks pass.
After that, the program checked the change: an independent reviewer read it
against the spec, and the program ran structural checks over the files it
touches and ran the checks again in a clean environment. What it found is
below.

For each finding, do one of two things:

1. Fix it. A requirement missing or implemented differently is implemented
   as the spec says. A test that does not assert its contract is rewritten to
   drive the real component and assert the observable outcome. A document
   that contradicts the code is corrected to what the code does. A test
   that fails in a clean environment is fixed — usually a fixture that
   relies on the machine's git configuration (git init without -b, a commit
   without a configured identity).
2. Declare it, when it cannot be fixed in this change: a limitation of a
   library the code depends on, a requirement that contradicts another. The
   declaration names the finding's key, says what the code does instead and
   why, and is recorded in an erratum you write in this change citing the
   code line and the test of the delivered behaviour. The pull request opens
   with every declaration. Findings marked fix-only cannot be declared.

Structural findings (formatting, vet, duplication, discarded errors,
comments deferring to a later task, unused declarations, long functions,
dates) are fixed where the fix is local and safe; what you leave is listed
in the pull request.

Do not weaken or delete a test to make a finding go away, and do not touch
anything the findings do not name.

Constraints that are mechanical, not advisory:

- git is limited to its read-only subcommands; the commit is made by the
  program after this phase, from what you submit.
- The spec package is read-only to you: writes under it are refused.

When you are done and the checks pass when you run them, call submit_resolve
exactly once.`

func resolvePrompt(in resolveInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Answer the conformance findings on branch `%s` in %s, where specification %s (\"%s\") "+
		"has been implemented.\n\n", in.Branch, in.Root, filepath.Base(in.Spec.Dir), in.Spec.Title)
	if !in.Now.IsZero() {
		b.WriteString(conform.DateLine(in.Now))
	}
	b.WriteString(languageBlock(in.Profile))
	if len(in.Blockers) > 0 {
		b.WriteString("## Findings to fix or declare\n\n")
		for _, x := range in.Blockers {
			tag := ""
			if !x.Declarable {
				tag = " (fix-only)"
			}
			fmt.Fprintf(&b, "- **%s**%s: %s\n", x.Key, tag, strings.TrimSpace(x.What))
		}
		b.WriteString("\n")
	}
	if in.Hermetic != nil {
		for _, c := range in.Hermetic.failing() {
			fmt.Fprintf(&b, "In the clean environment `%s` exited %d. Its output ended:\n\n```\n%s\n```\n\n",
				c.Command, c.ExitCode, checks.Tail(c.Output, 60))
		}
	}
	if len(in.Findings) > 0 {
		b.WriteString("## Structural findings\n\n")
		for _, f := range in.Findings {
			fmt.Fprintf(&b, "- `%s` (%s): %s\n", f.Ref(), f.Check, f.Message)
		}
		b.WriteString("\n")
	}
	if len(in.Shortfalls) > 0 {
		b.WriteString("## Partial requirements and weaker tests (fix or declare)\n\n" +
			"The review found these met only in part. Complete the requirement, or rewrite the test to " +
			"assert its contract; declare it by its id when it cannot be done in this change.\n\n")
		for _, u := range in.Shortfalls {
			fmt.Fprintf(&b, "- **%s**: %s\n", firstNonEmpty(u.Requirement, u.Test), strings.TrimSpace(u.What))
		}
		b.WriteString("\n")
	}
	if len(in.Outside) > 0 {
		b.WriteString("## Files outside the spec's scope (fix-only)\n\n" +
			"No task of the spec lists these files. A change the spec did not ask for belongs in a pull " +
			"request of its own: take each out of this change — restore it, or delete it if this change " +
			"created it — unless the spec's work cannot do without it. What stays is reported in the pull " +
			"request; it cannot be declared.\n\n")
		for _, p := range in.Outside {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Verification\n\n")
	b.WriteString(gateBlock(in.Gate, in.Baseline))
	b.WriteString("\n")
	if in.Survey != nil {
		b.WriteString(surveyBlock(*in.Survey))
	}
	b.WriteString("## The specification\n\n")
	b.WriteString(strings.TrimSpace(in.Spec.RenderCombined()))
	b.WriteString("\n\n")
	if in.Instructions != "" {
		b.WriteString("## Project instructions\n\n--- BEGIN PROJECT INSTRUCTIONS ---\n")
		b.WriteString(strings.TrimSpace(in.Instructions))
		b.WriteString("\n--- END PROJECT INSTRUCTIONS ---\n\n")
	}
	if in.Steering != "" {
		b.WriteString("## Steering\n\n--- BEGIN STEERING ---\n")
		b.WriteString(strings.TrimSpace(in.Steering))
		b.WriteString("\n--- END STEERING ---\n\n")
	}
	b.WriteString("Fix what can be fixed, declare what cannot, run the targeted tests, then call " +
		ToolSubmitResolve + ".")
	return b.String()
}
