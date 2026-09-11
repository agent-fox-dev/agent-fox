# 03. Rebuild the three skills as tools

**Status:** accepted
**Date:** 2026-09-09
**Supersedes:** nothing
**Related:** [ADR 02](02-build-the-spec-pipeline-on-agentkit.md),
[ADR 04](04-implement-a-spec-as-a-tool.md) (the fourth tool, `impl`),
[erratum 04](../errata/04_ghapi_removed.md) (the `ghapi` package named below
is now `issuex`)

## Context

Three markdown skills carried agent-fox's most valuable workflows:
`af-spec` (~750 lines), `af-issue` (~450) and `af-fix` (~640). Each is a
document handed to a coding CLI, and each demonstrates by its own shape what a
prompt cannot do.

Read `af-issue` and count the categories. There is a table for classifying the
argument. A step for detecting the repository, with two shell commands and a
regex. An issue-body template as a fenced markdown block. A labels menu with
three numbered choices. A `gh issue create` invocation. And a Guardrails
section that ends with the sentence every prompt-shaped program ends with:

> **Read-only.** Do not modify, create, or delete any source files. […] Use
> only: `cat`, `head`, `tail`, `ls`, `git log`, …

That sentence is a request. The CLI reading it has `write_file` and a shell,
and nothing between the request and the tool.

Writing the skills out as programs also surfaced logic errors that reading them
does not. `af-fix` opens a pull request whose body says `Closes #N` and then
squash-merges the same branch locally and pushes the default branch — leaving an
open PR whose changes are already merged, under a hash GitHub cannot recognise.
It posts its analysis to the issue in step 5 and checks for a dirty working
tree in step 6.1, so its most common failure leaves a public comment describing
work that never began. It declares a "single-pass mandate: complete all steps
without halting" and then halts in step 6.3 to ask whether to force-push. It
requires in step 7.5 that "all existing tests still pass" having recorded in
step 3.3 that they do not. And its final banner prints "✅ fixed and merged"
on paths where the push failed.

Meanwhile the `spec` CLI had grown the opposite problem. Fifteen subcommands, a
session state machine persisted to `_session.json`, and a refinement loop that
stops to ask a person questions are a reasonable shape for a CLI a human drives
and the wrong shape for one a program drives.

## Decision

Three tools — `spec`, `issue`, `fix` — with one interface: one positional
input (text, a file path, a GitHub URL, or `-`), one JSON object on stdout,
progress on stderr, one exit-code table. The old `spec` CLI and the
`agentspec` package are deleted rather than adapted.

The design rule is the one ADR 02 established and this ADR generalizes: **the
model does the part that needs judgment and Go does everything else, so the
parts that cannot be wrong stop being prompt.**

| The skill says, in prose | The tool does, in code |
|---|---|
| "classify the argument" (a table) | `toolio.Resolve`, before anything else happens |
| "detect the repository" (two shell commands) | `ghapi.DetectRepo`, parsing the three remote spellings |
| the issue-body template (a fenced block) | a `schema.Schema`; Go renders the markdown |
| "ask the user how to label the issue (1/2/3)" | `--label`, parsed before the run starts |
| `gh issue create --repo …` | a `net/http` call after the run, suppressed by `--dry-run` |
| "the codebase is read-only to you" | the mutating tools are not in the resolved set |
| "cite real files; do not guess" | every cited path resolved against the workspace |
| "run `make check`" and then "✅ all tests pass" | a `checks.Result`, and a renderer that takes one |
| "read tasks.json and check the test commands" | the project's language detected, a mismatch refused |
| "halt until input is received" | exit 2, before a token is spent |

### 1. The interface is the product decision

A tool a program drives has different constraints from a CLI a person drives.
One input, because the caller has one thing to hand over. JSON on stdout on
*every* path, because a tool that prints JSON on success and a sentence on
failure forces its caller to parse two formats and guess which one it got. One
exit-code table across all three, because a caller that can drive one can drive
all of them.

The shared shell that enforces this lives in `internal/toolio`. Each command is
a `toolio.App` value: its flags, its bounds, its pre-checks and one `Exec`.

### 2. The refinement loop is gone

`spec refine` asked a person clarifying questions and waited. Nobody is waiting
in an unattended run, so the PRD phase resolves every open question itself,
records each in a `## Design Decisions` section, and reports the ones a
different answer would have changed the work for as `open_questions`.

That is a real trade and it is made deliberately: a caller who wants a human in
the loop reads the array and re-runs with more context, and a caller who does
not gets a finished spec instead of a session id.

### 3. Nothing the model emits reaches GitHub or git

There is no `create_issue` tool, no `fetch_url`, and no MCP server. `file_issue`
and the two `submit_*` tools write a validated struct into the process and vote
to end the run; the REST calls happen in Go, before and after, at points that
know why. `fix`'s implementation phase has a shell, and that shell's `git` is
limited to read-only subcommands with `gh` refused outright — the branch, the
commit and the push are the pipeline's.

### 4. Verification is measured, not asserted

`fix` runs the project's own checks once before any change and once after, and
compares. `pass_was_already_failing` is a distinct verdict from `pass`, and
`regressed` from `still_failing`, because a repository that was red before the
run is a real case that the skill's two contradictory instructions could not
express. Every line of every comment is rendered from a `checks.Result` by a
pure function, so a verification section cannot claim a run that did not happen.

### 5. Two ordering corrections, kept as invariants

Every check that can refuse a run happens before the model is called and before
anything is posted. The summary comment is posted last, so it can carry the
pull request's URL and quote the exit status of the run that actually happened.

### 6. `--land` is a flag, not a contradiction

Opening a pull request and squash-merging the branch are alternatives.
`--land=pr` (default) · `branch` · `none`. There is no `merge`: a tool that
merges its own work removes the review that is the reason to open a PR at all.

### 7. A branch name is never reused

A second attempt at the same issue is normal — the first was rejected in
review — so `UniqueBranchName` picks the next free name rather than asking
whether to force-push over an existing branch, which is what the skill does
inside a workflow that promised not to stop.

## Consequences

**About 14 000 lines leave the repository** (`agentspec` and `cmd/spec`), and
what replaces them is smaller and covered by tests that make no network call.
`afspec` is untouched: it is the format library and it was never the problem.

**Four things survive from `agentspec`**, because they were the parts that were
right: the tier table over the AgentKit catalog, the credential resolution with
its three-state answer, the position-aware JSON-Schema-to-tool-schema converter
(and the bug it fixed), and the prompt templates.

**A real bug was found by a test written for this change.** The phase runner's
stop policy initially carried `StopWhenToolCalled(terminator)`, copied from a
prototype whose submit tools never reject. It ends a run when the terminating
tool is *called* — and a call the handler rejects is the repair loop's first
step, not its last. With it in place, the first malformed artifact would have
ended the phase instead of being corrected. The policy is now turns and budget
only; `ToolResult.Terminate`, set on acceptance and not on rejection, is the
intended ending.

**Lifecycle management is library API with no CLI surface.** `activate`,
`seal`, `archive`, `supersede`, `migrate`, `list`, `lint`, `status`, `render`
and `campaign` are gone as commands. `spec` activates a package it has just
validated, and everything else is `afspec` for an embedder to call. Restoring
any of them means adding a program, not a subcommand — the tools' interface
does not have room for a verb.

**Project trust stays opt-in.** `--trust-project` is off by default for the
same reason ADR 02 gave: a repository that is merely the current directory
would otherwise author part of the system prompt that reads it. `fix` renders
`AGENTS.md` into the *task* prompt of the phase that writes code, labelled as
repository-authored material, which is the weaker and more appropriate
placement.
