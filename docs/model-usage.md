# Model usage

`af` runs no model and reads no vendor key in this release. It has no brain.
The six commands it has (`lang detect`, `gate run`, `gate compare`,
`revert-check`, `guard check` and `map`) call no model: they read files, run
git and the checks of the project, and answer from what they measured. They
read no variable of a model vendor, and `gate run` strips from the environment
of the checks the variables that name a credential, which are the model
vendors' keys and anything called `*_TOKEN`, `*_SECRET`, `*_API_KEY` or
`*_PASSWORD`, because a test suite is the repository's code. The shell they
are built on makes no call to a model provider either.

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

`cost_source` is `none` here; a command that runs a brain sets it to
`measured` (the program counted) or `reported` (the brain said). The
environment variables `af` does read are those of the forge clients, in
[configuration.md](configuration.md).
