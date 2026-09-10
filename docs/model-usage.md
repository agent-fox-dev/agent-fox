# Model Usage

How the three tools invoke a model, what each phase sends, and what happens
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
| `issue` | `triage` | `read_file`, `list_files`, `find_files`, `search_files` | provider default | `file_issue` |
| `fix` | `analyse` | the four read tools, plus `execute` under a read-only allowlist | provider default | `submit_analysis` |
| `fix` | `implement` | the read tools, `write_file`, `edit_file`, `execute` under a build allowlist | provider default | `submit_implementation` |
| `spec` | `prd` | the four read tools | 32 768 | `submit_prd` |
| `spec` | `generate:{artifact}` | the four read tools | 65 536 | `submit_requirements`, `submit_test_spec`, `submit_tasks` |
| `spec` | `architecture` (opt-in) | the four read tools | 32 768 | `submit_architecture` |

`max_tokens` is an upper bound, clamped to the resolved model's own ceiling.
Temperature is 0.2 everywhere, because every phase produces a structured
artifact that is then validated — creativity here shows up as a schema
violation. A model whose catalog row rejects sampling parameters has them
dropped by its provider rather than by a branch in this code.

Every phase's turns, stop reason, tokens and cost appear in the envelope's
`usage.phases[]`, so a surprising bill is a field rather than an investigation.

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
| `submit_prd` | a spec name the format cannot use, an empty title, a body with no `## Intent` | each of the three would otherwise fail later and more expensively |
| `submit_{artifact}` | the artifact's v2 schema, plus every cross-file rule decidable at that point | the rule that failed, by name (`C1`…`C11`) |
| `submit_tasks` | test commands from another ecosystem than the project's | the detected language, and the project's real commands |

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

The category in the envelope is `no_result` for the first and `max_turns` or
`budget` for the others.

There is no way to *force* a tool call: AgentKit's `ToolChoice` is
unset/auto/none, the tri-state expressible on every wire it speaks.

## Reading the codebase

Every phase reads the source. The turns it spends reading come out of the same
budget, and `--verbose` reports each tool call on stderr.

Nothing that writes is reachable in a read-only phase: the mutating tools are
excluded from the resolved set, an invariant checks that set before the first
request, and AgentKit's unguarded-shell guard fails any run where one survived.
`fetch_url` is never registered by any tool, so no phase has outbound network.

`fix`'s implementation phase is the one that writes, and it runs under a guard
that narrows AgentKit's own restricted policy. See the
[tool reference](cli.md#what-the-model-may-and-may-not-do).

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

`issue`'s and `fix`'s prompts are Go string constants, because they are short
and assembled with the run's own facts — the baseline verification result, the
branch name, the diagnosis.

Per-project prompt overrides are not supported. The previous CLI read them from
`<project>/.spec/prompts/`; a repository that can rewrite the system prompt of
the agent reading it is a trust boundary, and `--trust-project` is the narrow,
labelled version of that grant.

## Untrusted input

A GitHub issue is text a stranger wrote. Every prompt that carries one fences
it, labels it with its provenance, and says to treat it as evidence to be
verified against the code rather than as instructions to follow.

That is a mitigation, not a guarantee, and it is the cheap half of one. The
expensive half is that no phase has a tool that reaches GitHub, git or the
network, so an instruction hidden in an issue body has nothing to reach for.
