# Model Usage

How the tools invoke a model, what each phase sends, and what happens
when the answer is wrong.

For credentials, model selection and bounds, see
[Configuration](configuration.md).

## A phase is an agent

Each phase builds its own agent — its own system prompt, tool set, turn budget
and cost cap — and runs it to a result. Phases are separate agents rather than
one conversation because sharing a transcript carries each phase's reasoning
into the next, and the next phase is usually meant to start from the previous
one's *conclusion* rather than from how it got there.

| Tool | Phase | Tools it may call | `max_tokens` | Terminating tool |
|---|---|---|---|---|
| `triage` | `triage` | `read_file`, `list_files`, `find_files`, `search_files`, `file_outline`, `find_symbol`, `code_search` (if indexed) | provider default | `file_issue` |
| `fix` | `analyse` | the six read tools, `code_search` (if indexed), plus `execute` under a read-only allowlist | provider default | `submit_analysis` |
| `fix` | `implement` | the six read tools, `code_search` (if indexed), `write_file`, `edit_file`, `execute` under a build allowlist | provider default | `submit_implementation` |
| `fix` | `review` (when the report cites spec ids) | the six read tools, `code_search` (if indexed), plus `execute` under a read-only allowlist; no repository map | provider default | `submit_review` |
| `spec` | `prd` | the six read tools, `code_search` (if indexed) | 32 768 | `submit_prd` |
| `spec` | `generate:{artifact}` | the six read tools, `code_search` (if indexed) | 65 536 | `submit_requirements`, `submit_test_spec`, `submit_tasks` |
| `spec` | `architecture` (opt-in) | the six read tools, `code_search` (if indexed) | 32 768 | `submit_architecture` |
| `impl` | `survey` | the six read tools, `code_search` (if indexed), plus `execute` under a read-only allowlist | provider default | `submit_survey` |
| `impl` | `repair` (with `--repair`, on a red baseline or after the integration task) | the same as `implement`; on `--repair-model`, a model of its own | provider default | `submit_repair` |
| `impl` | `implement` (once per task) | the six read tools, `code_search` (if indexed), `write_file`, `edit_file`, `execute` under a build allowlist; writes under the spec package refused | provider default | `submit_task` |
| `impl` | `review` (after the last task) | the six read tools, `code_search` (if indexed), plus `execute` under a read-only allowlist; no repository map | provider default | `submit_review` |
| `impl` | `resolve` (when the review found something) | the same as `implement`; no repository map | provider default | `submit_resolve` |

The "six read tools" are `read_file`, `list_files`, `find_files`,
`search_files`, `file_outline` and `find_symbol`. `code_search` is conditional
on the index: a phase gets it only when the run built a code-search index (see
[Reading the codebase](#reading-the-codebase)), and without one the phase has
the six read tools alone. The build allowlist is the read-only one plus the toolchains
(`go`, `make`, `npm`, `python`, `cargo`, …), the verification command's own
program, and whatever `--allow` adds. The exact lists, the heredoc and `cd`
handling and the refusal format are in the
[tool reference](cli.md#what-the-model-may-and-may-not-do).

`max_tokens` is an upper bound, clamped to the resolved model's own ceiling.
Temperature is 0.2 everywhere, because every phase produces a structured
artifact that is then validated — creativity here shows up as a schema
violation. A model whose catalog row rejects sampling parameters has them
dropped by its provider rather than by a branch in this code.

Every phase's turns, stop reason, tokens and cost appear in the envelope's
`usage.phases[]`, so a surprising bill is a field rather than an investigation.

That is the bill after the run. To watch spend while it accumulates, run with
`--emit-events` or read the events file: a `turn` event carries each model
turn's cost and tokens as it happens, and a `phase_end` event carries the
phase's total turns, cost and duration the moment it finishes — the same
figures `usage.phases[]` will report. A `heartbeat` event carries the run's
spend so far when nothing else has been emitted for 15 seconds. See
[event types](cli.md#machine-readable-progress-the-event-types).

## The result is a tool call

Every phase ends by calling one tool whose arguments *are* the result. The
handler decodes them into a typed Go value, validates, and votes to end the
run. Nothing parses a response afterwards.

The alternative — asking for JSON in the final message and unmarshalling it —
fails in a way this cannot. A model that wraps the object in a code fence, adds
a sentence before it, or renames a field produces text that parses into
nothing, and the failure surfaces as an empty result rather than as a
correction the model can act on.

`ConstrainedSampling` is set to `StrictPrefer` on every one of these tools. It
is honoured on the OpenAI wires and ignored on Anthropic's, so it is a helpful
narrowing and never the thing keeping the arguments well formed — the handler
validates either way. `StrictRequire` would fail the whole request on an
endpoint that cannot emit strict schemas, which is worse than an unconstrained
call.

## Repair is the loop

A handler that rejects a submission returns a tool **error**, not a Go error.
The loop appends it to the transcript the model is already holding, and the
model corrects itself on the next turn. There is no second conversation
assembled anywhere. Two things follow:

- The system prompt and the tool schemas are byte-identical across an attempt
  and its correction, so the provider's cache prefix survives the repair
  instead of being rebuilt each time.
- "How many attempts does the model get" and "how many turns may this phase
  take" are the same number.

The stop policy is turns and budget only, and that is deliberate: a policy that
ended the run when the terminating tool was *called* would end it on the first
rejected submission, which is the repair loop's first step rather than its
last. `ToolResult.Terminate`, set on acceptance and not on rejection, is the
intended ending.

What each handler rejects, and what the rejection says:

| Tool | Rejects | The message says |
|---|---|---|
| `file_issue` | an `affected_files` path that is not a regular file in the workspace | which paths, and to use `find_files`/`search_files` to get the real one |
| `file_issue` | a `suggested_fix.files` path outside the workspace | a proposed *new* file is fine; an escape is not |
| `submit_analysis` | an empty title, an unknown classification, an ambiguity with no question | which field, and what it is for |
| `submit_implementation` | an empty commit subject | that it is the subject of the commit this run makes |
| `submit_implementation` | `criteria_verdicts` that skips an acceptance criterion the report defined, names one it did not, answers twice, uses a verdict outside `pass`/`fail`, or offers a one-word evidence | which ids are missing or unknown, and what a piece of evidence has to name |
| `submit_prd` | a spec name the format cannot use (the directory rule's: no doubled or trailing underscore), an empty title, a body with no `## Intent` spelled exactly so (the heading the intent hash is computed from) or one that starts with YAML frontmatter; a `recommended_split` of one scope, with a name the format cannot use or used twice, or whose first scope is not the PRD being submitted | each would otherwise fail later and more expensively — the split's names become directory names, three phases on |
| `submit_{artifact}` | the artifact's v2 schema, including a key it does not allow (`additionalProperties: false`), plus every cross-file rule decidable at that point | the rule that failed, by name (`C1`…`C11`), or the disallowed keys by path |
| `submit_tasks` | test commands from another ecosystem than the project's | the detected language, and the project's real commands |
| `submit_survey` | an empty summary, a blocker with no reason | which field, and what it is for |
| `submit_repair` | an empty cause, an empty commit subject, an empty change list, a blocker with no reason | which field, and what a reviewer needs it for |
| `submit_task` | an empty commit subject; `test_verdicts` that skips a test the task owns, names one it does not, answers twice, uses a verdict outside `pass`/`fail`, or offers a one-word evidence; the same for `done_when_verdicts`; a blocker with no reason | which ids are missing or unknown, and what a piece of evidence has to name |

A `blocker` is accepted as soon as it has a reason: it is the one submission
that is not asked to be complete, because its whole content is "a person has
to decide". A `submit_task` or `submit_repair` that carries one skips every
other check.

The three `submit_{artifact}` schemas are converted from the format's own JSON
Schemas rather than re-authored, with one omission: the artifact's `$schema`
field. A tool schema may not declare a property by that name — the vendors
restrict property names to `^[a-zA-Z0-9_.-]{1,64}$` — so the model is not
shown the field and the submit handler writes its value from the schema
document's own `$id`. See
[the erratum](errata/tool_schema_property_names.md). Any other name a tool
schema cannot carry fails the run in pre-flight, naming the property.

## A split is decided once, and every scope gets its own PRD phase

An input the PRD phase reports as several specs' worth of work is written as
several packages. The first comes from the PRD just written. Each later scope
is a fresh `prd` phase with the same system prompt and the same tool, plus a
block in the task stating the decided split as a fact: every scope in order,
which are written, which this one is, and that its `spec_name` is fixed. The
model's job narrows to writing one PRD well against the packages that exist —
which it can read under the spec root — rather than re-deciding the shape of
the work. A `recommended_split` it reports anyway is a warning, not a second
split: the plan was recorded before the first package was written, and the
plan is what a later run resumes from.

## Generation is sequential, and each step sees everything before it

Format v2 §12.1 fixes the order: `requirements`, then `test_spec`, then
`tasks`. Each step receives the complete artifacts produced before it, in full
JSON rather than as a summary.

The order is the rule and the reason for it is concrete: the previous format
ran `test_spec` and `tasks` concurrently off a summary of requirement ids, so
the task generator never saw a test id and could not own the tests it was
supposed to own. In all eight archived v1 specs, every edge-case and property
test ended up owned by no task.

A rejected artifact leaves the accumulated state untouched, so the next attempt
validates against the same upstream artifacts as the one before it.

## A run that ends without submitting

The error names the `RunStopReason`, because the three ways this happens want
three different responses:

| Reason | What happened | What to do |
|---|---|---|
| `end_turn` | the model answered in prose instead of calling the tool | narrow the input; the phase declares its tool and a prompt guideline saying to call it |
| `max_turns` | it kept failing validation | raise `--max-turns`, or read the rejections with `--verbose` |
| `budget_exceeded` | the phase hit its cost cap | raise `--budget`, or choose a cheaper tier |

The category in the envelope is `no_result` in all three cases — the phase
ran to its end and produced nothing — and the message carries the stop reason
and the matching hint. The `max_turns` and `budget` categories are reserved
for a phase that ends in an error while at its ceiling; a phase that merely
stops there reports `no_result`.

There is no way to *force* a tool call: AgentKit's `ToolChoice` is
unset/auto/none, the tri-state expressible on every wire it speaks.

## Reading the codebase

Every phase reads the source. The turns it spends reading come out of the same
budget, and `--verbose` reports each tool call on stderr.

Six tools read the tree. `read_file`, `list_files`, `find_files` and
`search_files` read and search by path and by text. `file_outline` returns one
file's declarations (kind, name and line) without reading its body, and
`find_symbol` locates a name across the workspace in one call, where a
`search_files` and a `read_file` would otherwise be needed. Both are
AgentKit's. Universal-ctags is the accelerator: where it is installed they use
it, and where it is not they fall back to heuristics, the same pattern as
`search_files` with `rg`. Each result names the backend that produced it, and
`--preflight` reports which one a run will use (the `symbol_backend` check).
Each phase holds its own symbol table: `find_symbol` builds it on its first
call and it is bounded by AgentKit's defaults, so `fix` and `impl` do not carry
a table across the checkouts, resets, gate runs and commits between phases.

A seventh tool, `code_search`, searches an index instead of walking the tree on
every call, and ranks its results by relevance, so a phase that searches
repeatedly makes fewer `search_files` calls and reads fewer bytes. It is
AgentKit's, from the separate `codesearch` module. The index is
built once per run, before the first phase, and shared by every phase of the
run; it is closed when the run ends, on success, failure and cancellation
alike. It is
conditional on the index: when the index cannot be created (the platform does
not support it), the run continues without `code_search`, falls back to
`search_files`, and records a `low` `code_search_unavailable` warning
(`--preflight` reports which as the `code_search_index` check). The index reads
the tree only when `code_search` is first called, under that call's deadline; a
build that fails there fails that call, and the phase still has `search_files`. `triage` and `spec` never change the
tree, so their index is never stale. `fix` and `impl` invalidate it after
every change the program itself makes to the tree, so the next phase's
`code_search` results reflect the tree it is working on. `fix` invalidates it
after it creates the work branch, after each run of the verification command
and after the revert check. `impl` invalidates it after it creates or checks
out the work branch, after a parked attempt is reset, after a spec-directory
revert or a scratch-file drop, after a discarded attempt, after a repair's
reset, after each run of the checks, after each commit, and before the
conformance review and the resolve phase. The program only marks the index for
revalidation; it never builds a second one.

Nothing that writes is reachable in a read-only phase: the mutating tools are
excluded from the resolved set, an invariant checks that set before the first
request, and AgentKit's unguarded-shell guard fails any run where one survived.
`fetch_url` is never registered by any tool, so no phase has outbound network.

`fix`'s and `impl`'s implementation phases are the ones that write, and they
run under a guard that narrows AgentKit's own restricted policy. See the
[tool reference](cli.md#what-the-model-may-and-may-not-do).

The system prompt tells the model what its tools are for. The phase's own text
closes with the registered tools by name, and AgentKit follows it, under
`Guidelines:`, with the Tool guidelines every registered tool carries,
AgentKit's and the phase's own (`submit_analysis`'s "Report the diagnosis by
calling submit_analysis; do not write it as prose."), in registration order
with blanks and repeats removed. A phase with a shell and `search_files` also
gets AgentKit's "Prefer search_files over execute+grep" line, once. A phase with a
shell and the file tools is told which to use for what — read files with
`read_file`, search with `search_files`, find files with `find_files`, list
directories with `list_files` — and to use `execute` only for git, and in a
phase that writes, only for git and for building, formatting and testing. The
`execute` description says not to read or search files with `cat`, `head`,
`tail`, `grep` or `rg`; the allowlist it states is unchanged, so those
programs still run when the model has a reason. A phase without a shell is told
it has none, and gets no guideline that mentions `execute`.

## Compaction

A phase that reads a dozen large files fills the context window before it
reaches its terminating tool, and the run then ends on a provider error rather
than on a result. Every phase installs a context transform that summarizes its
own transcript in place once it passes 60% of the model's context window. A
compaction failure is reported as a detail line, not as a failed run.

## Retry and caching

A transient provider failure is retried by middleware (3 attempts by default,
exponential with jitter). Prompt caching is the provider's: AgentKit stamps
breakpoints over the tool schemas and the assembled system prompt.

Each phase reports a `SessionID` of `<tool>/<phase>`, which becomes
`prompt_cache_key` on the OpenAI wires. It is the tool's name and not a
per-run identifier on purpose: every `spec/generate:tasks` call in the world
shares this repository's system prompt and tool schemas, and making the key
unique per run would fragment exactly the cache it exists to help.

## Prompt templates

`spec`'s prompts are Markdown files embedded at compile time, because they are
prose a requirements engineer maintains and a diff against a document reads
better than a diff against escaped string concatenation:

```
prd_system   prd_user
generation_system   generation_user_base
generation_user_requirements   generation_user_test_spec   generation_user_tasks
architecture_user
```

`triage`'s, `fix`'s and `impl`'s prompts are Go string constants, because they
are short and assembled with the run's own facts — the baseline verification
result, the branch name, the diagnosis, the spec rendered scoped to one task.

`impl`'s three phase names are the same for every task and every run: the
task is in the user prompt, so `impl/implement` is one cache key across a
whole spec rather than one per task. The `repair` phase — `--repair`, on a
red baseline or after the integration task — is the one phase any tool runs
on a model of its own:
`--repair-model` resolves a second tier or catalog spec by the same rules
and the same `--vendor` as the run's model, and the rest of the run stays on
`--model`. The repair phase's effort is the first of these that is set:
(1) `--repair-model-effort`, (2) the repair model's tier effort when
`--repair-model` names a tier, (3) the run's effort when no separate repair
model is given. `--repair-model-effort` implies `--repair`.

Per-project prompt overrides are not supported. The previous CLI read them from
`<project>/.spec/prompts/`; a repository that can rewrite the system prompt of
the agent reading it is a trust boundary, and `--trust-project` is the narrow,
labelled version of that grant.

## Untrusted input

A GitHub issue is text a stranger wrote. Every prompt that carries one fences
it, labels it with its provenance, and says to treat it as evidence to be
verified against the code rather than as instructions to follow.

That is a mitigation, not a guarantee, and it is the cheap half of one. The
expensive half is that no phase has a tool that reaches GitHub or GitLab, the
network, or a mutating `git` subcommand, so an instruction hidden in an issue
body has nothing to reach for: the branch, the commit, the push, the pull
request and every comment are made by the program.
