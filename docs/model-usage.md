# Model usage

`af` runs no model except in four commands, `af triage` and `af triage run` (see
[Triage](#triage)) and `af fix` and `af fix run` (see [Fix](#fix)). The other
commands it has (`lang detect`, `gate run`, `gate compare`, `revert-check`,
`guard check`, `map`, `triage brief`, `triage submit`, `triage land`, `fix brief`,
`fix submit` and `fix land`) call no model: they read files, run
git and the checks of the project, and answer from what they measured. They
read no variable of a model vendor, and `gate run` strips from the environment
of the checks the variables that name a credential, which are the model
vendors' keys, the variables of the clouds and anything that ends in `_TOKEN`,
`_SECRET`, `_KEY`, `_PASSWORD` and the like (the list is in
[cli.md](cli.md#gate-run)), because a test suite is the repository's code. The shell they
are built on makes no call to a model provider either.

There are three brains in the library (`internal/brain`), for the agent commands;
`af triage` and `af fix` run them.

| Brain | Model | Where it runs | Cost source |
|---|---|---|---|
| `agentkit` | the AgentKit driver, on the model `AF_MODEL` names (`claude-sonnet-5-5` by default), through the hands' Anthropic provider | inside the box under `landlock` and `podman`, as `af <agent> run`; on the host, over the tools of the box, under `local` and `none` and with `--borrowed-hands` | `measured`: the driver counts the tokens and prices them from the catalog; a model the catalog does not list has no price, so the run reports `none` and `--budget` is refused |
| `scripted` | none: it replays a script | on the host | `none`: cost 0 |
| `claude` | Claude Code, headless, on its own default model, or the one `AF_MODEL` names, passed as `--model` | in the box, or at the checkout under `none` | `reported`: the cost is the `total_cost_usd` of Claude Code's own output, and its tokens are those of its `usage` (the cache tokens count as input) |

The cost of a run is the sum over its attempts, counted also for an attempt that
failed or panicked, because it was spent; a bound (`--budget`, `--max-turns`)
stops the driver, and a run that stopped on one and left no submission fails as
`budget` with the flag to raise. A brain that runs inside a box with its own
hands is `reported`: it says what it cost, and only when it has stopped. So for
`claude` the dollars of `--budget` are checked **after the run**: a cost above it
raises the warning `budget_exceeded` (and, when nothing was submitted, the run
fails as `budget`), and while Claude Code runs only `--max-turns` and the timeout
bound it, since `af` cannot stop the process mid-run. A run whose output cannot be
read has cost 0, `cost_source` `none` and the warning `brain_usage_unavailable`. The
`agentkit` brain on the host needs `ANTHROPIC_API_KEY` or another credential the hands'
provider accepts; in a box, and for the `claude` brain, it is `ANTHROPIC_API_KEY` and nothing
else (see
[configuration.md](configuration.md#the-brain)); the tests run them on a scripted
provider and a fake `claude`, and need none.

**Who holds the vendor's key.** The box that holds the tree has no model
credential when the brain is on the host: the key stays in the process that runs the
driver. A brain that runs as a process (`claude`; `agentkit` inside `landlock` or
`podman`) has the host's `ANTHROPIC_API_KEY` in its box, and no other credential:
no forge token, which is the rule that makes the box safe to give a model. Under
`landlock` and `podman` such a brain has the whole network (it needs its vendor, and
Podman filters nothing narrower), and a brain on the host whose brief has no tool that
runs a command (`af triage`) has none. The `local` and `none` providers are not
isolated: their tools run with the privileges of `af`, on this machine's files and
network, which is why every run that opens either carries the warning
`box_unconfined`. `AF_SANDBOX` chooses the provider when `--sandbox` does not, and a
`podman` box runs the image `AF_SANDBOX_IMAGE` names, else the tools image with `af`
and Claude Code in it. See [configuration.md](configuration.md#the-box) for which
credential enters which box.

## The agent-fox service

agent-fox runs no model in this release. The service (`agent-fox`, see [cli.md](cli.md#the-agent-fox-command-line))
serves probes and the subscription of workspaces: it makes no call to a model provider, holds no vendor key
and no forge token, and reads no variable of a model vendor. The only credentials it holds are the tokens
that callers send it, which it asks hub about and keeps only as a hash in memory, and the workspace tokens
that callers hand over to subscribe a workspace, which it keeps sealed (see [api.md](api.md) and
[configuration.md](configuration.md#the-agent-fox-service)). The jobs that run `af triage` and `af fix` for a
subscribed workspace come with later specs, and this document describes their cost when they do.

## Triage

`af triage` runs the `agentkit` brain by default (`--brain`), on the model
`AF_MODEL` names, over a box that holds a copy of the checkout. The brain is
read-only: its tools read and search the repository (`read_file`, `list_files`,
`find_files`, `search_files`, `file_outline`, `find_symbol`, `find_references`
and `code_search` where the box has it), and the one tool that ends the run,
`file_diagnosis`. Everything the brain reads goes to the model provider, and so
does what it is told; the box holds no token and no key (a brain that runs in it, such
as `claude`, has the key of the model vendor and nothing else).

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
holds no token and no key (a brain that runs in it, such as `claude`, has the key of
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
