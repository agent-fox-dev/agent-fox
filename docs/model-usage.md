# Model usage

`af` runs no model in this release. The six commands it has (`lang detect`, `gate run`, `gate compare`,
`revert-check`, `guard check` and `map`) call no model: they read files, run
git and the checks of the project, and answer from what they measured. They
read no variable of a model vendor, and `gate run` strips from the environment
of the checks the variables that name a credential, which are the model
vendors' keys, the variables of the clouds and anything that ends in `_TOKEN`,
`_SECRET`, `_KEY`, `_PASSWORD` and the like (the list is in
[cli.md](cli.md#gate-run)), because a test suite is the repository's code. The shell they
are built on makes no call to a model provider either.

There are two brains in the library (`internal/brain`), for the agent commands
that later specs add; no command of this release runs one.

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
