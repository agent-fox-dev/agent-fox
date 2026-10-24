# Development

How to build `af`, how the code is laid out, where it keeps its state, and
what a change to it has to leave true. The rules for every coding session are in
[AGENTS.md](../AGENTS.md); the reference of what `af` does is in
[cli.md](cli.md).

## Building and checking

```bash
make help       # every target, with a line on each
make check      # gofmt + go vet + golangci-lint + the short tests: the command to run before a commit
make build      # go build ./..., and bin/af and bin/agent-fox
make doc        # Go package docs of the public packages, under docs/generated
```

`make check-release` is `make check` with the full tests (`go test` without `-short`). It is
for a person to run before a release or a merge, and is not part of an agent session.
`make lint` runs golangci-lint with the configuration in `.golangci.yml`, and is skipped,
with a message, when golangci-lint is not installed.

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
| `cmd/agent-fox` | The service binary. `main.go` parses the flags (`--config`, `--version`, `--help`) and `run.go` starts the service: configuration, key, state file, server, listener, and the stop on a signal; the exit codes are 0 (stopped), 1 (the listener) and 2 (configuration, key or database). It is wiring, as `cmd/af` is. |
| `internal/service/config` | The `config.toml` of the service: lookup, the seven keys, `${NAME}`, defaults, validation; plain errors that name the dotted key. |
| `internal/service/secrets` | The key file and the sealing of workspace tokens (AES-256-GCM, the slug as additional data). |
| `internal/service/store` | The SQLite state file: `af_`-prefixed tables, append-only migrations in `af_schema_migrations`, and the subscriptions (`Upsert`, `Get`, `List`, `Delete`, and `Token`, the opened token for code in this process). |
| `internal/service/ttl` | The bounded cache with expiry that holds hub's answers; it takes its clock as a function. |
| `internal/service/api` | The HTTP surface: probes, the error envelope, the guards that authenticate a caller and check the workspace against hub, the subscription routes, the jobs routes (make, cancel, answer, retry, list, read, the stored envelope and report, the event stream), and the request log. |
| `internal/service/jobs` | The jobs layer: the documents and the store (`af_jobs`, `af_attempts`, `af_events`), creating and validating jobs, the adapter over `aflib/jobqueue` (per-workspace order, `[workers].count` slots, transient retries, shutdown and restart, the sweep), the `Runner` seam and the placeholder runner (the binary wires the worker), ending attempts, cancel, answer, retry and unsubscribe, the event log with its broker and stream, and the retention purge. |
| `internal/service/jobs/fakerunner`, `internal/service/jobs/fakeclock` | The fake runner that a test scripts (return, error, transient failure, panic, block) and records the inputs of, and the fake clock whose waits and tickers a test advances; exported for the tests of the specs that follow. |
| `internal/hub` | The client of hub's API, the one place that knows hub's paths, field names and answers: who a token belongs to and whether it sees a workspace, and the calls of an attempt (the record and variables of a workspace, sessions, events, sandbox leases, the push of a branch). |
| `internal/worker/recovery` | The files the worker keeps in `recovery/` of an attempt directory, to carry the attempt on after the process died: the journal (`journal.json`) and the outcome (`outcome.json`), written mode 0600 by an atomic synced replacement, and the status a session is completed with for an outcome. |
| `internal/service/labels` | The one table of the label protocol: the seven labels (suffix, kind, colour, description) in order, and their names under a prefix. Standard library only, so that the subscribe step, the worker, the cancel cleanup and the poll all import it. |
| `internal/service/labelcleanup` | The hub-backed cancel cleanup, the `jobs.Cleaner` that `cmd/agent-fox` wires: one try reads the token, the workspace record and the issue through hub, removes the status labels the cancelled job accounts for and puts back its request label, and classes every failure into its outcome. The layer's side (the mark, migration 7, the background tries, the sweep's retries, the unsubscribe's wait) is in `internal/service/jobs/cleanup.go`. |
| `internal/service/poll` | The label poll: the `Supervisor` that keeps one loop for each subscribed workspace (`supervisor.go`, `loop.go`), the cycle (`cycle.go`, `interval.go`), the rules for a listed issue (`handler.go`), the reconciliation of the poll's records with their jobs (`reconcile.go`), the classes of failure and the log-once rule (`failure.go`), and the store of the records, the table `af_poll_requests` (`store.go`). |
| `internal/service/reconcile` | The reconciliation pass: it gives back the sandboxes and completes the sessions that attempts left held, when the service starts and every 15 minutes. |
| `internal/hub/fakehub` | The `httptest` hub of the tests: a table of tokens, users and workspaces, the state an attempt works on (workspaces, issues, pull requests, sessions, leases), a git smart-HTTP server that takes only the Basic PAT, an in-process gateway for the leases, a record of every request, and failures, hang-ups, delays, redirects and oversize answers by route on demand. It serves hub's label route, `POST /api/v1/workspaces/{slug}/labels`, for any slug a token is granted (added or not), keeps each slug's labels (`Labels`, `PutLabel`), and calls a test's `OnRequest` with each request as it arrives. |
| `cmd/af` | The binary. `main.go` keeps the identity of the build (the ldflags variables), makes the shell with `afcmd.New`, registers the commands of the client of the agent-fox service on it and sets its mode hook and its help, and hands the process to it. The client is here and in no other package, so that a caller of `afcmd.New` never has it: `client_common.go` (the server, the credential and the failures of the client), `login.go`, `workspace.go`, `jobs.go`, `client_follow.go` (the following of a job and what it ends with), `serve_queue.go` (`af triage|fix --workspace`) and `mode.go` (the choice between `--local` and `--workspace`), with the JSON Schemas of their results in `schemas/`. The tests that build the binary and run it are here, with the golden file of `af --schema`. |
| `internal/afclient` | The library of the client: `ParseServer` (the server URL), `LoadCredential` and the editing of the credential file (`ReadExisting`, `ExistingFile.With`, `StoreCredential` behind an `FS`), `ReadSecretFile`, the `Client` (`Do`, which bounds the request, reads at most 8 MiB, refuses redirects and maps every answer to an `*Error`), the event stream (`Follow`, in tries, with an idle cut and a backoff on an injectable `Clock`) and `Envelope`. It returns plain and typed errors, never imports `internal/toolio` and never exits; the commands of `cmd/af` map its errors to the envelope. `fakeclock` is its clock for tests: the pauses of a follower cost nothing and are recorded. |
| `internal/worker` | The worker: the production `Runner` of the jobs layer, which carries out one attempt of a job against a subscribed hub workspace. It resolves the attempt (the token and label prefix of the subscription, the record and the variables of the workspace from hub, the refusals that need no box and no model, the input as an issue URL on the workspace's repository), opens the hub session, and clones the workspace into `clone/` with a git that holds the PAT only as a scoped header for the commands that reach hub, publishes a pushed branch through hub, leases the sandbox (a gateway box registry of the attempt, the timeout cut to the lease, the release on every path), runs the loop in process with the seams of `internal/afcmd`, and mirrors the outcome to the issue as labels and comments, with the cleanup of a cancel and of a shutdown, and reports the usage, the events and the end of the session to hub. Infrastructure failures are transient errors; everything else is an envelope. |
| `internal/afcmd` | The commands of `af`, importable so that `agent-fox` can run them in its own process. Each command is a file here (`lang.go`, `gate.go`, `gate_compare.go`, `revert_check.go`, `guard.go`, `map.go`, `triage.go` for the five of `af triage`, and `fix.go`, `fix_brief.go`, `fix_run.go` and `fix_land.go` for the five of `af fix`) that registers it, with its flags, its result and the JSON Schema of the result in `schemas/`. `afcmd.New(&afcmd.Config{…})` makes the shell. The zero `Config` is `af`; a caller that runs the commands in process supplies, per shell, the forge factory, the box and brain registries, the options of the brains with their lookup of the environment, the git runner, the questions that the `--context` values answer and the gate, and the commands use them in place of the package variables and the process environment. |
| `internal/toolio` | The command shell: the envelope, exit codes, coded warnings, `--dir` and `--timeout`, `--schema`, the positional input, the run directory. |
| `internal/agentdir` | The `.agent-fox/` directory and its `.gitignore`, and the atomic write the files the host keeps in it are made with (the submission core's `RootWriter` is the root-confined variant of it). |
| `internal/gitx` | The git and process invocations, hermetic and issued from Go: the runners, the NUL-separated listings of files and of changed paths. |
| `internal/project` | What a repository is written in and how it is checked: the languages, the gate table (`gate.json`, the Makefile, the manifests), the suite forms, the programs, and which paths are tests. |
| `internal/checks` | The file view and the tree hash; the gate engine and its cache (`.agent-fox/gate/`), the labels and the comparison of two results. |
| `internal/conform` | The revert check: whether the tests of a change fail without it. |
| `internal/policy` | The guard: the tokenizer, the rules, the policy files, and the decision for a tool call. |
| `internal/repomap` | The repository map at a token budget. |
| `internal/box` | The box that holds a brain's tree: a registry of providers (`landlock`, `podman`, `local` and `none`; the `gateway` of a lease is made for one attempt and held only in the registry of that attempt), `Open` and `Sync` (the checkout goes in with its `.git`), a read-only `FS`, `WriteFile` under `.agent-fox/`, `Export` (without `.git`), `Exec` (a command in the box, at its root), `Close`, and `With`, which closes on every path. `none` is a session in place over the checkout. Every name of the hands' sandbox module that is not a type is in `adapter.go`. |
| `internal/submission` | The submission protocol: `Kind`, `Submit` (the path to a submission file for a brain that is af's own: it validates before it writes), `Check` (what the host asks of a file it did not write), `RootWriter`, and `Command`, the `af <agent> submit` command of a kind. |
| `internal/brain` | The `Brain` contract and its registry, the shared flags, `BoxRequest` (the request for a box from the brain, the provider and the tools), `Execute` (place the brief, run the brain, decide from the file), the tool gate and the transcript, the `scripted` brain, the `agentkit` brain and the `claude` brain, and `Process`, which runs a brain that is a process (probe, environment, output). |
| `internal/fakeclaude` | The fake Claude Code and the fake `af` of the tests: the test binary run under those names (see [the fake claude](#the-fake-claude)). |
| `internal/optin` | The gates of the opt-in tests (see [Opt-in tests](#opt-in-tests)). |
| `containers` | The Containerfiles of the sandbox image (`containers/sandbox/`) and of the tools image (`containers/tools/`: the sandbox image with `af` and Claude Code), built by `make build-containers`. |
| `internal/triage` | `af triage` without the shell: the diagnosis, its validator and its rejections, the issue body (`Render`, `Extract`), the brief, the preflight, the landing on a forge, the submission kind and the tool allowlist. `fakeforge` is the GitHub and GitLab server (`httptest`) that its tests and those of `internal/afcmd` run against. |
| `skills` | The skills of the repository (`skills/af-*`) and the Go package that embeds them (`//go:embed af-*`); `skills.Triage()` is the body of `af-triage` that the brief carries. `make install-skills` and `make uninstall-skills` loop over `skills/af-*` only. |

The five packages from `project` to `repomap` (not the three after them, which are
the library of the agent commands and use `toolio`'s failures and run directory;
`internal/triage` imports none of the shell, the box or the brains itself)
return plain Go results and
errors. They never import `internal/toolio`, never call `os.Exit` or
`log.Fatal`, and never panic on bad input; a test reads their sources to see
that. The commands in `internal/afcmd` map their errors onto the failure categories of
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

### Testing recovery

The recovery of an attempt is tested offline, against the fake hub, its in-process gateway over the local provider and the
scripted brain: no hub, no key of a model vendor and no container engine
(`TestTheRecoveryTestsRunOfflineWithLoopbackListenersTheScriptedBrainAndNoContainerEngine_TS_14_60` guards it).

- **The stage-abort seam.** A process that dies is simulated by the stage-abort seam, `Runner.abortAfter`, which only the tests of
  `internal/worker` can set: the delivery stops right after the named stage (`applied`, `pushed`, `mirrored`, `usage_posted`
  and the others, `applied#2` for the second time), with no cleanup, no release and no report, as a process that
  died is stopped. The test then runs the next delivery with the handover count in the request, and reads
  `recovery/journal.json` and `recovery/outcome.json` of the attempt directory. `Runner.reachedHook` is called with every stage a
  delivery reaches, which is how a test makes a sandbox go away at an exact point, and `Runner.journalFault` makes a write of the
  journal fail. A graceful shutdown is tested through the real jobs layer, restarted on the same database file.
- **The fake hub.** `GET /api/v1/workspaces/{slug}/sandboxes` lists the leases of the workspace that are held, with their
  id, labels, purpose and end; `SeedLease` puts a labelled lease in the hub as if an attempt had taken it, `Deleted` is
  what was released, a session keeps its status, its usage and its events, and `KillSandbox` destroys the box that a lease
  holds and leaves the lease held, as a sandbox that is gone is. The gateway can be made to refuse boxes
  (`RefuseBoxes`) or to go away on a command (`KillOn`, `KillAfter`). `GET /api/v1/workspaces/{slug}/issues` lists the issues
  of the workspace with the `state`, `labels`, `limit` and `page` of a query, pull requests with their marker, and a `Link`
  header when a further page remains (`SetPageLinks` with false makes it say nothing, as the real hub does);
  `SetRemovalLag` and `SetAdditionLag` make an issue stay listed under a label for the next lists after the label was removed,
  or be left out of the lists of a label after it was added, as a forge whose index lags does; `SetListOrder` sets the
  order of a list, `AddLabel` and `RemoveLabel` change the labels as a person would, and `ListRequests` are the requests for
  the list. The tests of `internal/service/poll` drive `Supervisor.Cycle` with the real jobs layer, the fake runner and a fake
  clock, and put a recording handler or reconciler in the unexported seams of the supervisor where the listing, and not the
  rules, is what they test.
- **The fake hub's event stream.** `GET /api/v1/events?workspace=<slug>` checks the token's access to the workspace as the
  other routes do and then holds the connection open as `text/event-stream`, in hub's own frames (`event: audit_event` with
  the audit event as JSON). `SendEvent` sends a `HubEvent` (an id, a type, the issue and the label) to every open
  connection of the slug, `SendFrame` sends any text as it is (a frame in another encoding, or a malformed one),
  `Heartbeat` and `Comment` send hub's heartbeat frame and a comment line, `EndEvents` ends the slug's connections as hub
  does when it goes away, `EventConns` counts the open ones and `EventsRequests` lists the requests of the route, failed
  ones included; `Fail`, `Delay` and the other faults of a path apply to it as to any route. `EmitLabeledOnAdd` makes every
  label added through the forge API send `hub.forge.issue.labeled`, as hub relays the forge's webhook, and `SetIssueState`
  closes or reopens an issue. The stream's tests in `internal/service/poll` run the supervisor with `Start`, its stream
  and a fake clock: a clock that only moves when the test moves it, with first cycles 1000 hours away when a test wants the
  stream alone, and the supervisor's `beforeEvent` seam to hold the handling of an event.

## Opt-in tests

`make check` needs no key, no container engine and no recent kernel, so the tests that
do are opt-in. Each is gated by a function of `internal/optin` that skips it, with a
reason that names what is missing, and never fails it:

| Variable | Set to 1 to run | Needs |
|---|---|---|
| `AF_TEST_LANDLOCK` | spec 07's box invariants and `af fix` end to end under `landlock` | Linux with Landlock ABI 4 or newer |
| `AF_TEST_PODMAN` | the same under `podman` | `podman info` answering, and the image (`AF_SANDBOX_IMAGE`, else `quay.io/agentfox/tools:latest`; `make build-tools-container` builds it) |
| `AF_TEST_CLAUDE` | the contract row with the real Claude Code, under `local`, and under `podman` when `AF_TEST_PODMAN` is set too | `ANTHROPIC_API_KEY`, and `claude` on `PATH` |

```bash
AF_TEST_LANDLOCK=1 go test ./internal/brain ./internal/afcmd -run TS_10_63 -v
AF_TEST_CLAUDE=1 ANTHROPIC_API_KEY=… go test ./internal/brain -run RealClaude -v
```

The tests are `TestSpec07sBoxInvariantsHoldUnderTheRealBoxes_TS_10_63` and
`TestTheContractRowOfTheRealClaude_TS_10_63` in `internal/brain/optin_test.go`, and
`TestFixUnderTheRealBoxesLandsAllFourChanges_TS_10_63` in `internal/afcmd/fix_optin_test.go`.
What they run is also run offline, so that `make check` proves it: the invariants
under `local`, the real-claude test with a fake `claude`, and the scripted `af fix`
under `local`.

## The tests and the box

The default box is the isolating provider of the platform (`landlock` on Linux, `podman`
elsewhere), which a test must not need. Every test of `internal/*` (`internal/afcmd` among them) that
runs `af triage` or `af fix` passes `--sandbox local`: the helpers `triageEnv.loop`,
`fixEnv.fix` and `fixEnv.fixRun` add it themselves (a later `--sandbox` in the arguments
of a test, such as `--sandbox none`, is the one that counts), and a test that calls
`run` with the command words passes it. `TestEveryTestThatRunsTriageOrFixSaysWhichBox_TS_10_62`
reads the sources of the tests with `go/parser` and finds the invocations that do not.
A test of the default selection names `AF_SANDBOX` (sets it, or clears it to get the default of
the platform), which the scan accepts. The scan finds the calls of `run`, `runCtx`, `Main`, the
three helpers and the helpers that take the test and a command line (`afJSON(t, bin, "fix", url,
"--dir", dir)`), and the `[]string` lists of arguments written to be run later; it does not find a
call whose command word is held in a variable.

## The fake claude

A fake `claude` (and a fake `af`) is the test binary linked under that name. The
`TestMain` of a package that wants them calls `fakeclaude.Name()` and, when the binary
was run as `af` or `claude`, hands the process to `fakeclaude.Run` instead of running
the tests (`internal/brain` does it for both; `internal/afcmd` makes `af` the real app, so that
`af triage submit` is the real thing, and `claude` the fake). `fakeclaude.Install(t)`
makes the directory with the two links, which a test puts first on `PATH`.

What a fake does is told by a JSON script, because a process started with `env -i`
has none of the test's environment. It reads `.agent-fox/fake.json` in the tree it runs
in (the cwd, which a test of the box places with `Session.WriteFile`) and, if there is
none, `fake.json` beside the program (`fakeclaude.WriteScript`). Every field is
optional:

| Field | What the fake does |
|---|---|
| `af_version`, `af_commit`, `claude_version`, `version_exit` | What `--version` prints (the JSON document of `af`, a line for `claude`), and the exit code of it for each program. |
| `sleep_ms`, `spawn_child` | Waits, and starts a `sleep 30` child (its pid is noted), for the timeout and the process group. |
| `stdout`, `stdout_bytes`, `stderr`, `stderr_bytes`, `exit` | What it prints (a text, and that many bytes) and how it ends. |
| `env_to_stderr` | Prints its environment to stderr, a line for each variable. |
| `hook_payloads` | The `PreToolUse` payloads it runs through the hook command of the file named by `--settings`, one after the other (`{run_id}` and `{root}` in a payload are the id of the run and the tree); the answers are `{stdout, exit}` in `.agent-fox/fake-hooks.json`. |
| `submits` | Payloads it submits to the contract kind of the package, through the submit core; `{accepted, problems}` of each in `.agent-fox/fake-submits.json`. |
| `show_files` | Prints files of the tree to its stderr, a line `file: ` and `{path, content, missing}` for each (`{run_id}` and `{root}` are as in a hook payload). |
| `notes_to_stderr` | Prints the answers of the hooks to its stderr, as `hooks: ` and their JSON, for a test that cannot read the tree it ran in. |
| `commands` | Shell commands it runs at the tree (`{run_id}` and `{root}` are as in a hook payload), such as `af triage submit --run-id {run_id} <<'EOF' … EOF`; `{command, exit, output}` of each in `.agent-fox/fake-commands.json`. |
| `output_json` | What it prints last: the JSON that Claude Code prints, with `total_cost_usd`, `num_turns` and `usage`. |

It records each run (argv, environment, cwd, the size of its standard input, its pid and
the settings file it was given) in `.agent-fox/fake-record.jsonl`.

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

### Testing `af triage`

The triage tests run offline. `internal/triage/fakeforge` serves the GitHub or the
GitLab API of one repository over `httptest`, keeps every request (`Requests`,
`Writes`, `Count`), can make an operation fail (`Fail`), and hands the shell its
client (`NewForge(token)`, which a test gives to `App.NewForge`; an empty token is
an unauthenticated client). The tests of `internal/afcmd` make a git repository, an app
and a forge in one helper (`newTriageEnv`) and run commands through `app.Main`.
A brain is one of four things: the `scripted` brain with a script (its steps
call the tools of the box, and `submit` steps hand a payload over, with the
outcome they expect), the `agentkit` brain on the `faux` provider (put its options in
`testConfig.Brain`), the `claude` brain with a fake `claude` first on `PATH`
(`useFakeClaude`, which also points `testConfig.Brain` at the test binary as `af`),
or a spy of the test's own (put it in `testConfig.Brains`).
`TestOnlyTheAllowedWarningCodesAndCategories_TS_08_64` checks the warning codes
and failure categories of every envelope that the helper saw.

## Adding a command

1. Write its package under `internal/`, returning a plain result and typed
   errors, with its tests.
2. Add `internal/afcmd/<name>.go`: a `registerX(app)` that calls `app.Register` with the
   path, the summary, the usage, the flags, the default timeout and the result
   schema (`resultSchema("<name>")`), and add the call to `register` in
   `afcmd.go`. Map the errors of the package onto `toolio.Failure` categories.
3. Add `internal/afcmd/schemas/<name>.json`, rewrite the golden file (`cmd/af/testdata`), and add the
   command to the lists in `ring1_test.go` if it is a ring-1 command.
4. Document it in [cli.md](cli.md), and in [configuration.md](configuration.md)
   and [model-usage.md](model-usage.md) if it reads a file or a variable or runs
   a model, and here and in [architecture.md](architecture.md) if it changes the
   layout.
5. Run `make check`.
