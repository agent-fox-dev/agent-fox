# Model usage

`af` runs no model except in two commands, `af triage` and `af triage run` (see
[Triage](#triage)). The other commands it has (`lang detect`, `gate run`, `gate compare`,
`revert-check`, `guard check`, `map`, and `triage brief`, `triage submit` and
`triage land`) call no model: they read files, run
git and the checks of the project, and answer from what they measured. They
read no variable of a model vendor, and `gate run` strips from the environment
of the checks the variables that name a credential, which are the model
vendors' keys, the variables of the clouds and anything that ends in `_TOKEN`,
`_SECRET`, `_KEY`, `_PASSWORD` and the like (the list is in
[cli.md](cli.md#gate-run)), because a test suite is the repository's code. The shell they
are built on makes no call to a model provider either.

There are two brains in the library (`internal/brain`), for the agent commands;
`af triage` is the only one that runs one so far.

| Brain | Model | Where it runs | Cost source |
|---|---|---|---|
| `agentkit` | the AgentKit driver, on the model `AF_MODEL` names (`claude-sonnet-5-5` by default), through the hands' Anthropic provider | on the host, over the tools of the box | `measured`: the driver counts the tokens and prices them from the catalog; a model the catalog does not list has no price, so the run reports `none` and `--budget` is refused |
| `scripted` | none: it replays a script | on the host | `none`: cost 0 |

The cost of a run is the sum over its attempts, counted also for an attempt that
failed or panicked, because it was spent; a bound (`--budget`, `--max-turns`)
stops the driver, and a run that stopped on one and left no submission fails as
`budget` with the flag to raise. A brain that runs inside a box with its own
hands (a later scope) is `reported`: it says what it cost. The `agentkit` brain
needs `ANTHROPIC_API_KEY` or another credential the hands' provider accepts (see
[configuration.md](configuration.md#the-brain)); the tests run it on a scripted
provider and need none.

The box that holds the tree has no model credential, and the `local` provider,
the only one in this release, is not isolated: its tools run in the `af` process
with its privileges, on this machine's files and network. That is why every run
that opens it carries the warning `box_unconfined`.

## Triage

`af triage` runs the `agentkit` brain by default (`--brain`), on the model
`AF_MODEL` names, over a box that holds a copy of the checkout. The brain is
read-only: its tools read and search the repository (`read_file`, `list_files`,
`find_files`, `search_files`, `file_outline`, `find_symbol`, `find_references`
and `code_search` where the box has it), and the one tool that ends the run,
`file_diagnosis`. Everything the brain reads goes to the model provider, and so
does what it is told; the box holds no token and no key.

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
