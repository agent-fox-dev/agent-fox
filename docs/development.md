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
| `internal/agentdir` | The `.agent-fox/` directory and its `.gitignore`, and the atomic write the files the host keeps in it are made with (the submission core's `RootWriter` is the root-confined variant of it). |
| `internal/gitx` | The git and process invocations, hermetic and issued from Go: the runners, the NUL-separated listings of files and of changed paths. |
| `internal/project` | What a repository is written in and how it is checked: the languages, the gate table (`gate.json`, the Makefile, the manifests), the suite forms, the programs, and which paths are tests. |
| `internal/checks` | The file view and the tree hash; the gate engine and its cache (`.agent-fox/gate/`), the labels and the comparison of two results. |
| `internal/conform` | The revert check: whether the tests of a change fail without it. |
| `internal/policy` | The guard: the tokenizer, the rules, the policy files, and the decision for a tool call. |
| `internal/repomap` | The repository map at a token budget. |
| `internal/box` | The box that holds a brain's tree: a registry of providers (`local`), `Open` and `Sync` (the checkout goes in with its `.git`), a read-only `FS`, `WriteFile` under `.agent-fox/`, `Export` (without `.git`), `Close`, and `With`, which closes on every path. Every name of the hands' sandbox module that is not a type is in `adapter.go`. |
| `internal/submission` | The submission protocol: `Kind`, `Submit` (the path to a submission file for a brain that is af's own: it validates before it writes), `Check` (what the host asks of a file it did not write), `RootWriter`, and `Command`, the `af <agent> submit` command of a kind. |
| `internal/brain` | The `Brain` contract and its registry, the shared flags, `Execute` (place the brief, run the brain, decide from the file), the tool gate and the transcript, the `scripted` brain and the `agentkit` brain. |

The five packages from `project` to `repomap` (not the three after them, which are
the library of the agent commands and use `toolio`'s failures and run directory)
return plain Go results and
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
| `revert/<id>/` | `revert-check` | The backup that exists while the check is working: `manifest.json`, `index` (the index as it was) and `files/` (a copy of every file it changes). `<id>` is 12 random hex characters, and the directory, made exclusively, is the claim on the checkout. `changed/` holds what a path held when it was changed while the checks ran. It is removed when the tree is back. One that is left was left by a check that was killed or that could not put the tree back, and the next check refuses until someone has looked at it. |
| `run/<id>/` | the shell | The run directory of a command that has one; see [cli.md](cli.md#the-run-directory). None of the ring-1 commands makes one. A command that runs a brain adds its briefs, hook, accepted submission, transcripts and a panic's stack; the box has a `run/<id>/` of its own with `brief.md`, `run.json`, `hook.json` and `submission.json`. |

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

## The scripted brain

`--brain scripted --script <file>` replays a script on the host: no model, no cost,
and a step can say what it expects, so a script is also a test of what a run
does. The file is `{"steps": [...]}`, and a step is exactly one of two forms:

```json
{"steps": [
  {"tool": "write_file", "args": {"path": "notes/a.txt", "content": "hello\n"}, "expect": "ok"},
  {"tool": "write_file", "args": {"path": ".agent-fox/x", "content": "x"}, "expect": "denied"},
  {"submit": {"summary": "done", "files": ["notes/a.txt"]}, "expect": "accepted"}
]}
```

- `{"tool": name, "args": {...}}` calls a tool of the box through the gate, and
  `expect` is `ok`, `error` (the tool failed) or `denied` (the gate or the guard
  refused it, and it was not run).
- `{"submit": payload}` submits against the tree, and `expect` is `accepted` or
  `refused`.

A file that cannot be read, that holds a field that is not described, a step with
both forms or neither, `args` on a submit, an empty tool name or an `expect` that
does not fit its step is a `usage` failure that names the file and the step
(counted from 0), found when the brain is made, before any box opens. Each step is
a turn: an accepted submission ends the replay, with the steps after it listed as
skipped in the transcript; `--max-turns` ends it too (`turns`), and so does the
end of the steps (`ended`). A result that is not the one a step expects stops the
replay with an error that names the step, what was expected and what came.

## Writing a test of a brain

A test of a brain runs it through `brain.Execute` over a real `local` box on a
temporary git repository (with an identity of its own, which a commit in the box
needs, as the box has a private `HOME`), a real run directory, and a `Report`
that remembers. The `agentkit` brain is driven by the hands' faux provider, which
plays the turns of a model and records the requests it was sent:

```go
p := faux.New(
    faux.ToolCall("c1", "write_file", `{"path":"a.txt","content":"x"}`), // outside the allowlist: denied
    faux.ToolCall("c2", "submit_test", `{"summary":"done"}`),            // the terminating tool
)
b, _ := brain.Default.New("agentkit", brain.Options{
    Model:       "faux-1",
    Getenv:      func(string) (string, bool) { return "", false }, // no key is read
    NewProvider: func(string) (core.ProviderClient, error) { return p, nil },
})
res, err := brain.Execute(ctx, brain.Request{ /* Session, Name, Brain: b, Brief, RunDir, Report, Tally */ })
// p.Requests()[1] is what the model saw after its first turn: the denial.
```

Give a turn a cost with `faux.Turn.Usage` (`SetField` and `SetCost`) to see the
cost bound, and a `Brief.MaxTurns` for the turn bound. What a test checks is the
file and the ledger of the run, never what the brain printed: `res.Payload`, the
`submission.json` of the run directory, the warnings and artifacts of the
`Report`, and `RunDir.Ledger()`. A test of a kind gives `submission.Kind` a
`Validate` that reads the tree it is given. The whole of `internal/brain`,
`internal/box` and `internal/submission` runs with no key and no network
(`TestTheBrainsNeedNoKeyAndNoNetwork_TS_07_79` replaces the HTTP transport with
one that counts the requests).

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
