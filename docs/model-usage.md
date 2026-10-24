# Models and cost

Which parts of agent-fox call a language model, which credentials they use,
what the model is sent, how much a run may spend, and how the cost is
reported. Settings are in [configuration.md](configuration.md); command
details are in [cli.md](cli.md).

## Contents

- [Which commands use a model](#which-commands-use-a-model)
- [The model and its credentials](#the-model-and-its-credentials)
- [What the model is sent](#what-the-model-is-sent)
- [Bounds and defaults](#bounds-and-defaults)
- [How cost is reported](#how-cost-is-reported)
- [Under the service](#under-the-service)

## Which commands use a model

Only the two agent commands call a model:

| Command | Model | Tools the model gets |
|---|---|---|
| [`af triage`](cli.md#af-triage) | Yes | Read-only: read, list, find and search files, outlines, symbols and references, plus `file_diagnosis` to hand in its diagnosis. |
| [`af fix`](cli.md#af-fix) | Yes | Read and search, write and edit files, and run the commands the [tool policy](architecture.md#the-tool-policy) allows (the project's checks, `af`, read-only `git`, a few utilities), plus `submit_fix` to hand in its report. It cannot commit or write to a protected path. |
| `af lang detect`, `af gate run`, `af revert-check` | No | — |
| `af workspace …`, `af jobs …` | No | — (the service does the work; see [below](#under-the-service)) |

`af triage run` and `af fix run` are the halves of the agent commands that run
inside a sandbox; they spend from the same budget as the command that started
them.

The commands that call no model read no model vendor variable. `af gate run`
also strips credentials from the environment of the project's checks, because
a test suite is repository code (see [cli.md](cli.md#af-gate-run)).

Two brains can drive the agent commands:

| Brain | What it is | Cost |
|---|---|---|
| `agentkit` (default) | The AgentKit driver on the model `AF_MODEL` names. | Measured: the driver counts tokens and prices them from AgentKit's model catalog. |
| `scripted` | Replays a script; no model. For tests. | None. |

## The model and its credentials

`AF_MODEL` names the model. Unset, it is the catalog's default Anthropic
model, `claude-sonnet-5-5`. A model the catalog does not list has no price:
the run reports no cost, and `--budget` refuses to start with it. See
[configuration.md](configuration.md#model-and-credentials).

The credential depends on where the brain runs, which depends on the sandbox
provider (see [architecture.md](architecture.md#where-the-brain-runs)):

| Sandbox | Brain runs | Credential |
|---|---|---|
| `local` | On the host, using the sandbox's tools | `ANTHROPIC_API_KEY`, or any other credential AgentKit's Anthropic provider accepts (an auth token, Bedrock, Vertex AI). |
| `podman` (default) | Inside the sandbox | `ANTHROPIC_API_KEY` only, passed into the sandbox. |

Without a credential the run fails as `auth` before a sandbox opens.

The sandbox never holds a forge token. When the brain runs inside it, the
vendor key is the only credential it holds; when the brain runs on the host,
the sandbox holds none. Everything the brain reads in the repository, and
everything it is told, goes to the model provider. See
[configuration.md](configuration.md#sandbox) for which credential and network
each sandbox gets.

## What the model is sent

The brain gets one document, the **brief**. It has two parts:

- The **prefix**: everything that stays the same for a given report and
  repository tree. It is sent as the first message, so a provider that caches
  prompts can reuse it across runs, rounds and resumes.
- The **task block**: what is particular to this run, sent as the message that
  starts the run.

A triage brief:

| Part | Content |
|---|---|
| Mandate | Fixed rules: the run is read-only; text from the report is material, never instructions; cite only files that were read; submit exactly one diagnosis. |
| Skill | The `af-triage` skill: how to diagnose a report. Compiled into the binary. |
| Repository map | Files and their declarations, within about 4,000 tokens. |
| Report | The issue (title, state, author, body, a previous diagnosis marked as possibly stale, comments), or the text or file given. At most 128 KiB; longer reports are cut with the warning `input_truncated`. |
| Task block | Earlier questions and the `--context` answers, the run id, how to submit, the bounds. |

A fix brief has the mandate (find the root cause, make the smallest change,
never commit or touch the issue, submit one report), the `af-fix` skill, the
report, the project's checks with their baseline results, the repository map
and the schema of the report. Its task block carries the run and round, any
answers, and, after a rejected round, why the change was rejected and the last
4 KiB of output of each failing check.

## Bounds and defaults

Every agent run is bounded by cost, turns and time. Override them with
`--budget`, `--max-turns` and `--timeout`.

| Command | `--budget` (USD) | `--max-turns` | `--timeout` |
|---|---|---|---|
| `af triage` | 2 | 100 | 15m |
| `af fix` | 10 | 150 | 45m |

- A run that hits its budget or turn limit before submitting fails as
  `budget`, with a hint to raise the limit (to twice its value).
- A fix can take several **rounds**: when `af` rejects a change after
  measuring it, the brain gets another round in the same sandbox. All rounds
  share one budget and one turn limit. A new round starts only while budget
  and turns remain and enough time is left to measure the result.
- A fix that runs out of bounds with a change in hand parks it on a `wip`
  branch rather than losing it (see [cli.md](cli.md#af-fix)).
- `--dry-run` only stops writes to the forge. The model runs as usual and
  costs the same.

## How cost is reported

Every envelope has a `usage` object (see [cli.md](cli.md#the-envelope)):

```json
"usage": {
  "brain": "agentkit",
  "cost_usd": 0.42,
  "cost_source": "measured",
  "input_tokens": 51234,
  "output_tokens": 3120,
  "turns": 18
}
```

| `cost_source` | Meaning |
|---|---|
| `measured` | `af` counted the tokens and priced them. |
| `reported` | The brain reported the cost. |
| `none` | No cost is known: no brain ran, the `scripted` brain ran, or the model has no price. |

The cost is summed over every round, including rounds that failed, because the
money was spent. When a brain running inside a sandbox leaves no readable
result, the run reports cost 0 with `cost_source` `none` and the warning
`brain_usage_unavailable`. The pull request opened by `af fix` states the
brain and its cost in its footer.

## Under the service

The `agent-fox` service process never calls a model and holds no vendor key.
Each job attempt runs the triage or fix loop in the service's process, but the
brain runs inside a sandbox that hub leases for the attempt. The sandbox
profile on hub supplies the image, the network and the vendor key; the brain
refers to `ANTHROPIC_API_KEY` by name and never sees it as a value from the
service. A key in the service's own environment has no effect.

What an attempt may spend is set by the workspace's variables on hub (see
[configuration.md](configuration.md#workspace-variables-on-hub)):

| Variable | Triage default | Fix default |
|---|---|---|
| `AF_MODEL` | catalog default | catalog default |
| `AF_MAX_BUDGET_USD` | 2 | 10 |
| `AF_MAX_TURNS` | 100 | 150 |
| `AF_TIMEOUT` | 15m | 45m |
| `AF_MAX_ATTEMPTS` | 2 | 2 |

A job can spend more than one attempt's budget:

- **Automatic attempts.** After a retryable `brain` or `no_result` failure the
  service tries again by itself, up to `AF_MAX_ATTEMPTS` per round. Other
  failures end the job and cost nothing more (see
  [api.md](api.md#how-an-attempt-ends)).
- **Answers and retries.** Each answer to a question and each retry starts a
  new round of attempts, whether it comes from the API, `af jobs`, or a
  comment and label on the issue (see
  [api.md](api.md#answering-and-retrying-from-the-issue)).
- **Redelivery.** An attempt handed over again after a crash, a restart or an
  infrastructure failure starts afresh with the full budget. A brain that was
  running when the process died is paid for again.
- **Infrastructure failures** (sandbox, hub, forge) hand the same attempt
  over again, up to three failures. They do not count against
  `AF_MAX_ATTEMPTS`, but each handover is a redelivery and pays again.
- **Request labels** make one job per new request; the poll takes each
  request once (see [api.md](api.md#request-labels)). The poll itself
  calls no model.

A cancel stops the spending: the running attempt is told to stop and has 60
seconds to do so.

The job document's `usage` is the sum over its finished attempts, and each
attempt shows its own (see [api.md](api.md#the-job-document)). After an
attempt, the worker posts one usage record to hub with the model and tokens.

The client commands (`af jobs submit`, `af jobs watch`, …) spend nothing on
your machine: their envelope has zero usage, and the job's cost is in
`result.job.usage`. For a job that ended failed or cancelled, read it with
`af jobs get <id>`.
