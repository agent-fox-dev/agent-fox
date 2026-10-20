# Development

How to build `af`, how the code is laid out, where it keeps its state, and
what a change to it has to leave true. The rules for every coding session are in
[AGENTS.md](../AGENTS.md); the reference of what `af` does is in
[cli.md](cli.md).

## Building and checking

```bash
make check      # gofmt + go vet + all tests: the command to run before a commit
make build      # go build ./..., and bin/af
make doc        # Go package docs of the public packages, under docs/generated
```

`make check` needs no network, no token and no API key. The tests build their
own git repositories in temporary directories, with an identity and a
configuration of their own, and skip when `git` is missing; the checks they
run are small shell scripts, and skip when there is no `sh`. A few start a
server on the loopback interface, so a sandbox that forbids binding a local
port fails them. AgentKit is a sibling checkout (see the [README](../README.md)).

Run `make doc` before you update the documentation, and update it in the same
change as the code: a document that is out of date is a regression.

## Layout

| Package | What it is |
|---|---|
| `cmd/af` | The binary. `main.go` hands the process to the shell; each command is a file here (`lang.go`, `gate.go`, `gate_compare.go`, `revert_check.go`, `guard.go`, `map.go`) that registers it, with its flags, its result and the JSON Schema of the result in `schemas/`. |
| `internal/toolio` | The command shell: the envelope, exit codes, coded warnings, `--dir` and `--timeout`, `--schema`, the positional input, the run directory. |
| `internal/agentdir` | The `.agent-fox/` directory and its `.gitignore`, and the atomic write every file in it is made with. |
| `internal/gitx` | The git and process invocations, hermetic and issued from Go: the runners, the NUL-separated listings of files and of changed paths. |
| `internal/project` | What a repository is written in and how it is checked: the languages, the gate table (`gate.json`, the Makefile, the manifests), the suite forms, the programs, and which paths are tests. |
| `internal/checks` | The file view and the tree hash; the gate engine and its cache (`.agent-fox/gate/`), the labels and the comparison of two results. |
| `internal/conform` | The revert check: whether the tests of a change fail without it. |
| `internal/policy` | The guard: the tokenizer, the rules, the policy files, and the decision for a tool call. |
| `internal/repomap` | The repository map at a token budget. |

The five packages from `project` to `repomap` return plain Go results and
errors. They never import `internal/toolio`, never call `os.Exit` or
`log.Fatal`, and never panic on bad input; a test reads their sources to see
that. The commands in `cmd/af` map their errors onto the failure categories of
the envelope. The loops of later specs call the packages in process, without
the shell.

Two files bind to AgentKit's hands, each in one place so that a change in them
has one place to follow: `internal/policy/handsguard.go` (the guard, a floor
under the rules, as a variable a test can replace) and
`internal/repomap/handsoutline.go` (the outline behind the `Outliner`
interface).

## What is kept in `.agent-fox/`

Everything `af` keeps for a checkout is under `<dir>/.agent-fox/`, which has a
`.gitignore` of its own that ignores it all. `.agent-fox/gate/` and
`.agent-fox/revert/` are where the gate and the revert check keep theirs. It is never part of the file view,
so nothing in it changes a tree hash.

| Path | Written by | Content |
|---|---|---|
| `gate.json`, `guard.json` | `lang detect --write`, a person or a loop | The gate table and the guard policy; see [configuration.md](configuration.md). |
| `gate/<id>/NN-<slug>.log` | `gate run` | The full output of the NNth check that ran. |
| `gate/<id>/result.json` | `gate run` | The result for the key `<id>`: the SHA-256 of the tree hash, the checks and the runner mode, 12 hex characters. Written after the logs, through a temporary file and a rename. |
| `gate/labels.json` | `gate run --label` | `{label: id}`; the latest result for a label wins. |
| `revert/<id>/` | `revert-check` | The backup that exists while the check is working: `manifest.json`, `index` (the index as it was) and `files/` (a copy of every file it changes). `<id>` is 12 characters of the tree hash. It is removed when the tree is back. One that is left was left by a check that was killed, and the next check refuses until someone has looked at it. |
| `run/<id>/` | the shell | The run directory of a command that has one; see [cli.md](cli.md#the-run-directory). None of the ring-1 commands makes one. |

## Tests

- A test of a command goes through `newApp().Main` with streams of its own, or
  through the built binary when it is about signals or the process (`buildBinary`
  runs `make build` into a temporary directory).
- A test that needs git makes its repository with a helper that skips when git
  is not there, and gives it an identity on the command line and an environment
  with no global git configuration. A test that needs a shell skips when there
  is none. `TestSuitesPassWithoutGit_TS_06_75` runs the suites of these packages
  with nothing on `PATH`.
- A property is tested with a generator seeded by the test (so a failure
  repeats), and a parser or a decision that takes text has a fuzz target:
  `go test ./internal/policy -run xxx -fuzz FuzzDecide`.
- `af --schema` is compared with a golden file per binary
  (`cmd/af/testdata/schema.golden.json`, and the shell's own in
  `internal/toolio/testdata`). After a change to a result schema, or to the
  schema of the envelope, rewrite them with
  `go test ./internal/toolio ./cmd/af -update`. A test also compares the
  top-level properties of each command's schema with the JSON fields of its Go
  result type, so the two cannot drift apart.

## Adding a command

1. Write its package under `internal/`, returning a plain result and typed
   errors, with its tests.
2. Add `cmd/af/<name>.go`: a `registerX(app)` that calls `app.Register` with the
   path, the summary, the usage, the flags, the default timeout and the result
   schema (`resultSchema("<name>")`), and add the call to `register` in
   `main.go`. Map the errors of the package onto `toolio.Failure` categories.
3. Add `cmd/af/schemas/<name>.json`, rewrite the golden file, and add the
   command to the lists in `ring1_test.go` if it is a ring-1 command.
4. Document it in [cli.md](cli.md), and in [configuration.md](configuration.md)
   and [model-usage.md](model-usage.md) if it reads a file or a variable or runs
   a model, and here and in [architecture.md](architecture.md) if it changes the
   layout.
5. Run `make check`.
