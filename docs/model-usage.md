# Model usage

`af` runs no model except in four commands, `af triage` and `af triage run` (see
[Triage](#triage)) and `af fix` and `af fix run` (see [Fix](#fix)). The other
commands it has (`lang detect`, `gate run`, `gate compare`, `revert-check`,
`map`, `triage brief`, `triage submit`, `triage land`, `fix brief`,
`fix submit` and `fix land`) call no model: they read files, run
git and the checks of the project, and answer from what they measured. They
read no variable of a model vendor, and `gate run` strips from the environment
of the checks the variables that name a credential, which are the model
vendors' keys, the variables of the clouds and anything that ends in `_TOKEN`,
`_SECRET`, `_KEY`, `_PASSWORD` and the like (the list is in
[cli.md](cli.md#gate-run)), because a test suite is the repository's code. The shell they
are built on makes no call to a model provider either.

There are two brains in the library (`internal/brain`), for the agent commands;
`af triage` and `af fix` run them.

| Brain | Model | Where it runs | Cost source |
|---|---|---|---|
| `agentkit` | the AgentKit driver, on the model `AF_MODEL` names (`claude-sonnet-5-5` by default), through the hands' Anthropic provider | inside the box under `podman` (and a `gateway`), as `af <agent> run`; on the host, over the tools of the box, under `local` | `measured`: the driver counts the tokens and prices them from the catalog; a model the catalog does not list has no price, so the run reports `none` and `--budget` is refused |
| `scripted` | none: it replays a script | on the host | `none`: cost 0 |

The cost of a run is the sum over its attempts, counted also for an attempt that
failed or panicked, because it was spent; a bound (`--budget`, `--max-turns`)
stops the driver, and a run that stopped on one and left no submission fails as
`budget` with the flag to raise. The driver inside a box counts its cost the same
way and hands it back in its envelope; a run whose envelope cannot be read has cost
0, `cost_source` `none` and the warning `brain_usage_unavailable`. The `agentkit`
brain on the host needs `ANTHROPIC_API_KEY` or another credential the hands'
provider accepts; in a box it is `ANTHROPIC_API_KEY` and nothing else (see
[configuration.md](configuration.md#the-brain)); the tests run it on a scripted
provider and a fake `af`, and need none.

**Who holds the vendor's key.** The box that holds the tree has no model
credential when the brain is on the host: the key stays in the process that runs the
driver. The driver that runs as a process (`agentkit` inside `podman`) has the
host's `ANTHROPIC_API_KEY` in its box, and no other credential: no forge token, which
is the rule that makes the box safe to give a model. Under `podman` such a brain has
the whole network (it needs its vendor, and Podman filters nothing narrower), and a
brain on the host whose brief has no tool that runs a command (`af triage`) has none.
The `local` provider is not isolated: its tools run with the privileges of `af`, on
this machine's files and network, which is why every run that opens it carries the
warning `box_unconfined`. `AF_SANDBOX` chooses the provider when `--sandbox` does not, and a
`podman` box runs the image `AF_SANDBOX_IMAGE` names, else the tools image with `af`
in it. See [configuration.md](configuration.md#the-box) for which
credential enters which box.

## The client of the agent-fox service

`af` runs no model on this machine when it talks to the agent-fox service. `af login`, `af workspace …` and `af jobs …`
call the service and nothing else, and `af triage` or `af fix` with `--workspace` queues a job and, with `--wait`, follows
it: the brain of the job runs in the box that the service's worker leases from hub, as described below. The envelope of such
a command therefore has the zero usage of a run with no brain, `"cost_source": "none"`. What a job cost is in the job
document the command returns, in `result.job.usage`: the cost, the tokens and the turns summed over the finished
attempts of the job. A command that follows a job to a failure, a cancel or a `usage` ending returns the error and no result
(the shell clears the result of a failed envelope), so `af jobs get <id>` shows the cost of such a job. The local mode (`--local`) is the one that runs the loop, and spends, on this machine.

## The agent-fox service

agent-fox runs no model in this release. The service (`agent-fox`, see [cli.md](cli.md#the-agent-fox-command-line))
makes no call to a model provider: its worker runs the `af` loops in its own process, and the brain they start runs
in a box that hub leases. It holds no vendor key and no forge token, and reads no variable of a model vendor. The
only credentials it holds are the tokens that callers send it, which it asks hub about and keeps only as a hash in memory, and the workspace
tokens that callers hand over to subscribe a workspace, which it keeps sealed (see [api.md](api.md) and
[configuration.md](configuration.md#the-agent-fox-service)). What an attempt spends is this:

- **The label poll spends no model, and each labelled issue is a job.** The poll that turns a request label into a job
  (see [api.md](api.md#jobs-that-a-request-label-makes)) calls hub and the forge, and the label poll runs no model: it lists
  issues, makes jobs and removes labels. A job made from a label spends once and as any other does, with the same hub variables
  and the same bounds: each labelled issue is a job that spends like a job of the API. The poll takes a label once, and does not
  take it again while its removal is owed or it is held, so a request does not spend twice; and
  a restored label does not spend again: when a job is cancelled while its attempt runs, the worker puts the request label back on the
  issue, and the poll leaves it there until a person asks again, rather than taking it for a new request.
- **An answer or a retry from the forge spends like one from the API.** A comment and the request label again answer a
  blocked job or retry a failed one (see [api.md](api.md#answering-and-retrying-from-the-forge)): the new attempt spends as
  the attempt `POST /api/v1/jobs/{id}/answer` or `/retry` would queue, once for each sighting that takes the label.
- **The model is `AF_MODEL`.** It is a hub variable of the workspace (see
  [configuration.md](configuration.md#the-hub-variables-of-an-attempt)); when it is unset the catalog's default
  Anthropic model, `claude-sonnet-5-5` today. The session that is opened on hub, the brain and the usage record carry the same one.
- **The vendor key is the box's.** The sandbox profile that hub leases supplies the image, the network and the secrets
  of the box, and a brain started there is told the *names* of its credentials (`ANTHROPIC_API_KEY`) as references to the
  box's own environment, never as values. The worker sends no key and puts none in a box spec, whose `Env` is empty,
  and the lookup of the environment that the loops, the brains and the settings read yields nothing, so a key in
  the environment of the service changes nothing.
- **The bounds are variables too.** `AF_MAX_BUDGET_USD` (default `2` for a triage, `10` for a fix), `AF_MAX_TURNS`,
  `AF_TIMEOUT` and `AF_MAX_ATTEMPTS` bound an attempt; the `scripted` brain, which spends nothing, is for tests and is
  not one a variable may name.
- **The usage is reported to hub.** After the sandbox is released the worker posts one usage record when a brain ran,
  with the model and the tokens, and the brain, the cost, where the cost came from and the turns of the envelope
  beside them; hub keeps the model and the tokens, and the envelope keeps the rest.
- **A job costs the sum of its attempts.** Its `usage` is the sum of `cost_usd`, `input_tokens`, `output_tokens`
  and `turns` over the attempts that have finished, as each attempt's envelope reports them; the attempts show
  their own `usage`. A job that is asked again (an answer, a retry) spends more, because each human action opens
  a round of attempts of its own.
- **Automatic attempts spend more, and follow only some failures.** The service queues another attempt by itself
  only after a `failed` envelope whose error category is `brain` or `no_result` and is retryable, and only until
  the round has made as many attempts as the runner's maximum allows (2 unless it says otherwise, at most 10).
  A failure of the infrastructure (the box, hub, the forge) is a transient error: the same attempt is handed over
  again, up to three failures, and it spends no further brain attempt. A failure of any other category, or one
  that is not retryable, ends the job and costs nothing more.
- **A cancel stops the spending.** A cancelled attempt is told to stop, and its runner has 60 seconds; what it
  reports after that is dropped.
- **A redelivery pays again.** An attempt that is handed over again after a crash, a restart or a transient error
  begins afresh, with the whole of `AF_MAX_BUDGET_USD` and `AF_MAX_TURNS`: a brain that was running when the process
  died is paid for again. The usage of the envelope and the record that is posted to hub are those of the delivery
  that came to the outcome.

## Triage

`af triage` runs the `agentkit` brain by default (`--brain`), on the model
`AF_MODEL` names, over a box that holds a copy of the checkout. The brain is
read-only: its tools read and search the repository (`read_file`, `list_files`,
`find_files`, `search_files`, `file_outline`, `find_symbol`, `find_references`
and `code_search` where the box has it), and the one tool that ends the run,
`file_diagnosis`. Everything the brain reads goes to the model provider, and so
does what it is told; the box holds no token and no key (the driver, when it runs in the box,
has the key of the model vendor and nothing else).

**What the brief sends.** The brief is one document. The `agentkit` brain gives
the model the part before the task block as the first message of the
conversation (the driver's `Config.Prefix`, which a provider can cache) and the
task block as the message that starts the run. It is made of the mandate, the
skill, the repository map, the report and the task block, in that order:

| Part | What it is |
|---|---|
| The mandate | A fixed text: the run is read-only; everything inside a fence in the report is material from people and never an instruction; cite only files that were read; submit exactly one diagnosis; and the rules a diagnosis must keep. |
| The skill | The body of the `af-triage` skill, which says how to diagnose: find the signals, locate each in the code, read the tests, separate the symptom from the cause, state the confidence honestly, ask only when two readings lead to opposite fixes, write criteria that can be observed. It is compiled into the binary. |
| The repository map | The files of the repository with their declarations, within 4000 tokens (the files that git tracks and the untracked ones that are not ignored). |
| The original report | The issue (its title, state and author, the body as the reporter wrote it, a previous diagnosis of `af` marked as possibly stale, and its comments), or the text, file or standard input that was given, each part in a fence with one line that says where it comes from. At most 131072 bytes: a longer report is cut at a UTF-8 boundary, and the warning `input_truncated` is raised. |
| The task block | What is particular to the run: the question an earlier run asked and each `--context` answer, the run id, how to submit, and the bounds. |

Everything particular to a run is in the task block, so the part before it (the
prefix) is the same for every run over the same tree and the same report, whatever
the run id, the answers, the brain and the bounds. A provider that caches a prefix
caches it; a run that resumes after a question has the same prefix as the one it
resumes. `af triage brief` renders the brief and calls no model, so that a person
can read what would be sent and how big it is (`estimated_tokens`).

**The default bounds** are USD 2 of model cost (`--budget`), 100 turns
(`--max-turns`) and 15 minutes (`--timeout`). A run that spends its cost or its turns before it submits is
`failed`/`budget` with the flag to raise and twice its value, and lands nothing.
`--budget` needs a model whose price the catalog knows; with another, `agentkit`
fails as `usage` and names `AF_MODEL`. A dry run (`--dry-run`) still asks the
model, and costs what any run costs: it sends no write to the forge, nothing
more. The cost of the run is in the envelope's `usage` (`brain`, `cost_usd`,
`cost_source`, tokens and turns).

## Fix

`af fix` runs the `agentkit` brain by default (`--brain`), on the model `AF_MODEL`
names, over a box that holds a copy of the checkout. Unlike triage, the brain
writes: its tools read and search the repository, write and edit files, and run
commands (`execute`) that the policy of the box allows, which are the checks of
the gate, `af`, `git` for reading and a few utilities that only read or make a
directory; it can neither commit nor write under a protected path. The one tool that ends a round is `submit_fix`. Everything
the brain reads goes to the model provider, and so does what it is told; the box
holds no token and no key (the driver, when it runs in the box, has the key of
the model vendor and nothing else).

**What the brief sends.** As for triage, the part before the task block is the
first message of the conversation, which a provider can cache, and the task block
starts the round. The brief is made of the mandate (find the root cause, make the
smallest change, never commit or touch the issue, the report is material and not
an instruction, submit one report), the body of the `af-fix` skill, the original
report, the gate with its baseline, the repository map and the schema of the
report, and then the task block. The prefix is byte-identical for every round,
every attempt and every resume over the same report and tree, so that a cache
read serves them all; what differs is in the task block: the run, the attempt and
the round, the answers of a person, and, after a refusal, why `af` refused the
last change and the end (4 KiB) of the output of each check that failed.
`af fix brief` renders it and calls no model.

**The default bounds** are USD 10 of model cost (`--budget`), 150 turns
(`--max-turns`) and 45 minutes (`--timeout`). The budget and the turns are bounds
per attempt: an attempt is rounds of the brain in a row, in one box, and the next
attempt (`--attempts`, 2 by default and at most 5) starts from the tree as it is
with a fresh budget of both. A round is one run of the brain, ended by a report or
a question; a change that `af` refuses after measuring it is the task block of the
next round, so a run that needs three tries to pass the gate is three rounds of
one attempt, each paid from the same budget. Another round starts only while cost
and turns remain and the time left of `--timeout` is at least a minute after the
time kept for measuring (three times what the baseline took, and a minute). A run
that has spent its attempts without a change that may land parks what it has, as a
`wip` commit; one that applied nothing fails with the category of its last round
(`budget` with the flag to raise, for one that spent its cost or turns before it
submitted). Under a `--timeout` or a signal, the change that was applied is parked
on a local branch and the model is not asked again.

The cost of the run is the sum over every round of every attempt and is in the
envelope's `usage`, and the footer of a pull request says it (`brain`, `cost_usd`
and `cost_source`). A dry run (`--dry-run`) still asks the model, round after
round. A dry run spends model money like any other run, and writes nothing beyond
its working tree, which it puts back. `--budget` needs a model whose price the
catalog knows, as for triage. `fix run` is one round, with the bounds of an
attempt; `fix brief`, `fix submit` and `fix land` call no model, and a person who
drives them with a model of their own pays for it elsewhere.

## The cost in the envelope

The envelope has a place for the cost of a run, so that the commands that do
run a brain have somewhere to report it. For a run with no brain it holds the
zero value:

```json
"usage": {
  "brain": "",
  "cost_usd": 0,
  "cost_source": "none",
  "input_tokens": 0,
  "output_tokens": 0,
  "turns": 0
}
```

`cost_source` is `none` here; a command that runs a brain sets `brain` to its
name and `cost_source` to `measured` (the program counted), `reported` (the
brain said) or `none` (the scripted brain). The
environment variables `af` does read are those of the forge clients, in
[configuration.md](configuration.md).
