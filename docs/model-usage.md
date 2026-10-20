# Model usage

`af` runs no model and reads no vendor key in this release. It has no brain:
the binary answers `--version`, `--schema` and `help`, and the shell that
every later command is built on makes no call to a model provider.

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
