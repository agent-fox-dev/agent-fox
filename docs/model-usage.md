# Model Usage

How the spec pipeline invokes a model across its three stages, what each stage
sends, and what happens when the answer is wrong.

For credentials, model selection and the config file, see
[Configuration](configuration.md).

## A phase is an agent

Each stage builds its own agent — its own model, tool set, turn budget and cost
cap — and runs it to a result. They are separate agents rather than one
conversation because sharing a transcript would carry the PRD critique into the
requirements the critique was supposed to improve.

| Phase | `max_tokens` | Temperature | Configurable via | Submits through |
|---|---|---|---|---|
| `assess` | 4 096 | 0.2 | `assess_model` | `submit_assessment` |
| `refine` | 16 384 | 0.2 | `refine_model` | `submit_prd_update` |
| `generate` (per artifact) | 65 536 | 0.2 | `generate_model` | `submit_requirements`, `submit_test_spec`, `submit_tasks` |

`max_tokens` is an upper bound and is clamped to the resolved model's own
ceiling. Temperature is low because every phase produces a structured artifact
that is then validated — creativity here shows up as a schema violation. A
model whose catalog row rejects sampling parameters has them dropped by its
provider rather than by a branch in this code.

## The result is a tool call

Every phase ends by calling one tool whose arguments *are* the result. The
handler decodes them into a typed Go value, validates, and votes to end the
run. Nothing parses a response afterwards.

- **`submit_assessment`** — quality (one of `ready`, `needs_refinement`,
  `incomplete`), summary, gaps, questions.
- **`submit_prd_update`** — the rewritten PRD *and* a fresh assessment of it,
  as two fields of one call. They used to have to appear in one response with a
  hand-written check enforcing it; making them one call means the schema says
  so, and a submission missing half is refused by its own tool call.
- **`submit_{artifact}`** — the whole artifact, against the v2 JSON Schema
  `afspec` embeds. The schema is converted from those bytes rather than
  re-authored, so the declaration the model fills in and the rules the artifact
  is validated against cannot disagree.

## Generation is sequential, and each step sees everything before it

Format v2 §12.1 fixes the order: `requirements`, then `test_spec`, then
`tasks`. Each step receives the complete artifacts produced before it —
requirements through the library's own renderer, tests as a table carrying
every id, kind and `verifies` list.

The order is the rule and the reason for it is concrete: the previous format
ran `test_spec` and `tasks` concurrently off a summary of requirement ids, so
the task generator never saw a test id and could not own the tests it was
supposed to own. In all eight archived v1 specs, every edge-case and property
test ended up owned by no task.

## Repair is the loop

`afspec.ValidateGenerationStep` runs **inside** the submit handler: the
artifact's schema plus every cross-file rule decidable at that point. A
violation is returned as a tool error whose text names the rule that failed,
and the loop appends it to the transcript the model is already holding.

There is no second conversation assembled anywhere. Two things follow:

- The system prompt and the tool schemas are byte-identical across an attempt
  and its correction, so the provider's cache prefix survives the repair
  instead of being rebuilt each time.
- "How many attempts does the model get" and "how many turns may this phase
  take" are the same number, bounded by `--max-turns` (default 40).

A step that never produces a valid artifact within that budget is a failed
generation: `spec generate` exits non-zero, removes the artifacts that run
wrote, and reports the violations (§12.2). Artifacts that already existed on
disk are left alone, so a re-run resumes from the point of failure.

## A run that ends without submitting

The error names the `RunStopReason`, because the three ways this happens want
three different responses:

| Reason | What happened | What to do |
|---|---|---|
| `end_turn` | the model answered in prose instead of calling the tool | the error quotes what it said |
| `max_turns` | it kept failing validation | raise `--max-turns`, or read the rejections with `--verbose` |
| `budget_exceeded` | the phase hit its cost cap | raise `--max-budget`, or use a cheaper tier for that phase |

There is no way to *force* a tool call: AgentKit's `ToolChoice` is
unset/auto/none, the tri-state expressible on every wire it speaks. A phase run
without `--read-source` declares exactly one tool and its prompt guideline says
to call it.

## Reading the codebase

With `--read-source`, the phase additionally gets AgentKit's non-mutating
built-in tools rooted at `--source`, and the turns it spends reading come out
of the same budget. `--verbose` reports each tool call on stderr.

Nothing that writes is reachable: the mutating tools are excluded from the
resolved set, an invariant checks that set before the first request, and
AgentKit's unguarded-shell guard fails any run where one survived.

## Retry and caching

A transient provider failure is retried by middleware (3 attempts by default,
exponential with jitter). Prompt caching is the provider's: AgentKit stamps
breakpoints over the tool schemas and the assembled system prompt and keeps a
late-added tool after the prefix, so a tool arriving mid-session does not
invalidate the whole transcript.

## Prompt templates

Nine templates are embedded at compile time and each can be overridden per
project at `<project>/.spec/prompts/<name>.md`. A symlinked override is
ignored.

```
assessment_system     assessment_user
refinement_system     refinement_user
generation_system     generation_user_base
generation_user_requirements  generation_user_test_spec  generation_user_tasks
```

With `--trust-project`, repository-authored skills and context files
(`AGENTS.md`, `CLAUDE.md`, `.specs/steering.md`) are appended to the assembled
system prompt as well.
