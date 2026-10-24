# Development guide

How to build agent-fox, run its checks, find your way around the code, and
make a change. For what the binaries do, see the [CLI reference](cli.md) and
the [HTTP API](api.md); for how they are built inside, see
[architecture.md](architecture.md). Coding agents also follow the policy in
[AGENTS.md](../AGENTS.md).

## Contents

- [Prerequisites](#prerequisites)
- [Building](#building)
- [Checks and tests](#checks-and-tests)
- [Repository layout](#repository-layout)
- [Test infrastructure](#test-infrastructure)
  - [The fake hub](#the-fake-hub)
  - [The fake forge](#the-fake-forge)
  - [The fake af](#the-fake-af)
  - [The scripted brain](#the-scripted-brain)
  - [Testing a brain](#testing-a-brain)
  - [Tests and the sandbox](#tests-and-the-sandbox)
  - [Property and fuzz tests](#property-and-fuzz-tests)
- [Opt-in tests](#opt-in-tests)
- [Adding a command](#adding-a-command)
- [Container images](#container-images)
- [Skills](#skills)
- [Documentation](#documentation)

## Prerequisites

| Tool | Why |
|---|---|
| Go 1.27 | Every `go.mod` says `go 1.27`. |
| git | The tests build their own repositories, and the worker clones through hub. The worker needs git 2.31 or later. |
| A checkout of [agentkit-go](https://github.com/agent-fox-dev/agentkit-go) next to this one | `go.mod` replaces the AgentKit modules with `../agentkit-go`. |
| golangci-lint (optional) | `make lint` uses it, and skips with a message when it is missing. |
| podman (optional) | Only for the [opt-in tests](#opt-in-tests) and for [building the images](#container-images). |

Clone AgentKit beside this repository:

```bash
git clone https://github.com/agent-fox-dev/agentkit-go ../agentkit-go
```

[aflib](https://github.com/agent-fox-dev/aflib) is an ordinary module that
`go` fetches by its pinned commit. To change aflib and agent-fox together, put
both in a `go.work` (it is git-ignored):

```bash
git clone https://github.com/agent-fox-dev/aflib ../aflib
go work init . ../aflib
```

## Building

```bash
make build          # go build ./..., then bin/af and bin/agent-fox
bin/af --version
bin/agent-fox --version
```

`make build` stamps both binaries with `VERSION` (from `git describe`),
`COMMIT` and `BUILD_TIME` through `-ldflags`. A binary built another way, such
as `go build ./cmd/af`, reports what the Go toolchain recorded, or `dev`.

`make help` lists every target.

## Checks and tests

`make check` is the gate. Run it before every commit:

```bash
make check          # gofmt, go vet, golangci-lint and go test -short ./...
```

`make test` and `make check-release` run the full suite, including the slow
tests that `-short` skips. They are for a person to run before a release or a
merge. Coding agents never run them (see [.specs/steering.md](../.specs/steering.md#quality-gate));
narrow `go test` runs on the packages you change are fine.

The tests need no network, no API key, no forge token and no container
engine. Keep it that way:

- Tests start `httptest` servers on the loopback interface. A sandbox that
  forbids binding a local port makes them fail.
- A test that needs git creates its own repository in a temporary directory,
  with its own identity and no global git configuration, and skips when git is
  missing. A test that needs `sh` skips when there is none.
  `TestSuitesPassWithoutGit_TS_06_75` runs the suites of these packages with
  nothing on `PATH` to prove it.
- Tests that do need a key or a container engine are [opt-in](#opt-in-tests).

## Repository layout

| Path | What it holds |
|---|---|
| `cmd/af/` | The `af` binary, including the service client commands (`af workspace`, `af jobs`). |
| `cmd/agent-fox/` | The `agent-fox` service binary. |
| `internal/afcmd/` | The local `af` commands (`triage`, `fix`, `lang detect`, `gate run`, `revert-check`), importable so the service can run them in process. |
| `internal/toolio/` | The command shell every `af` command runs on: the envelope, exit codes, warnings and the run directory. |
| `internal/triage/`, `internal/fix/` | The triage and fix loops, without the shell. |
| `internal/brain/`, `internal/box/`, `internal/submission/` | Brains, sandboxes and the submission protocol. |
| `internal/project/`, `internal/checks/`, `internal/conform/`, `internal/policy/`, `internal/repomap/` | Language detection, the gate, the revert check, the tool policy and the repository map. |
| `internal/service/` | The service: configuration, secrets, the state file, the HTTP API, jobs, the label poll. |
| `internal/worker/` | Runs one job attempt against a hub workspace. |
| `internal/hub/` | The hub API client and `fakehub`. |
| `internal/afclient/` | The library behind the service client commands. |
| `skills/` | The coding-agent [skills](#skills). |
| `containers/` | Containerfiles for the [images](#container-images). |
| `docs/` | The [documentation](#documentation). |
| `.specs/` | `steering.md` and the archived specs. Test names such as `TS_10_63` and `REQ-…` ids refer to them. |

[architecture.md](architecture.md#package-map) has the full package map.

## Test infrastructure

Everything the tests talk to is faked in-process.

### The fake hub

`internal/hub/fakehub` is an `httptest` hub. A test sets up tokens, users and
workspaces, and the state an attempt works on: issues with comments and
labels, pull requests, sessions, sandbox leases. It also serves:

- a git smart-HTTP server that accepts only the workspace's token as Basic
  auth;
- an in-process sandbox gateway (AgentKit's `gatewayd` over the local
  provider) for leases;
- hub's label route, keeping each workspace's labels.

It records every request, and can fail, hang up, delay, redirect or send an
oversized answer on any route. Helpers a test uses to set up a scenario:

| Helper | Effect |
|---|---|
| `SeedLease`, `Deleted`, `KillSandbox` | Seed a held lease, see which leases were released, destroy a leased box. |
| `RefuseBoxes`, `KillOn`, `KillAfter` | Make the gateway refuse boxes or go away. |
| `AddLabel`, `RemoveLabel`, `Labels`, `PutLabel` | Change and read issue and workspace labels as a person would. |
| `SetRemovalLag`, `SetAdditionLag`, `SetListOrder`, `SetPageLinks`, `ListRequests` | Imitate a forge whose label index lags, set list order and paging, and read the list requests. |
| `AddPersonComment`, `SetClock` | Add a person's comment; set the clock that stamps comments. |
| `Inject` | Replace an answer, for example an issue read with `truncated: true` or `comments_err`. |
| `OnRequest` | Call a function with each request as it arrives. |

The worker is tested end to end against the fake hub, its gateway and the
[scripted brain](#the-scripted-brain), with no hub, no key and no container
engine (`internal/worker/offline_test.go` guards this). The poll's tests drive
`Supervisor.Cycle` with the real jobs layer, the fake runner
(`internal/service/jobs/fakerunner`) and a fake clock
(`internal/service/jobs/fakeclock`).

### The fake forge

`internal/triage/fakeforge` serves the GitHub or GitLab API of one repository
over `httptest`, including pull and merge requests. It records requests
(`Requests`, `Writes`, `Count`), can fail an operation (`Fail`), and hands the
shell its client (`NewForge(token)`; an empty token is an unauthenticated
client). The `internal/afcmd` tests set up a git repository, an app and a
forge in one helper (`newTriageEnv`, and `fixEnv` for fix).

### The fake af

A sandbox runs `af` by name, but a test binary is not `af`. So the `TestMain`
of packages that run `af fix` (`internal/afcmd`, `cmd/af`) links the test
binary as `af` (`fakeaf.LinkSelf`), points `brain.DefaultAFPath` at the link,
and acts as the real `af` when run under that name. `internal/worker` does the
same.

`internal/brain` instead uses a scripted fake: when its test binary runs as
`af`, `fakeaf.Run` takes over. `fakeaf.Install(t)` creates the link directory
to put first on `PATH`. Because a process started with `env -i` sees none of
the test's environment, the fake reads its instructions from
`.agent-fox/fake.json` in its working directory. Every field is optional:

| Field | Effect |
|---|---|
| `af_version`, `af_commit`, `version_exit` | What `--version` prints, and its exit code. |
| `sleep_ms`, `spawn_child` | Wait, and start a `sleep 30` child (pid in `.agent-fox/fake-child.pid`), to test timeouts and process groups. |
| `stderr`, `stderr_bytes`, `exit` | What to print on stderr, and the exit code. |
| `output_json` | What to print on stdout: the envelope of `af <agent> run`. |

Each run is recorded (argv, environment, working directory, pid) in
`.agent-fox/fake-record.jsonl`.

### The scripted brain

`--brain scripted --script <file>` replays a script instead of calling a
model: no key, no cost. Each step can state what it expects, so a script is
also a test of what a run does.

```json
{"steps": [
  {"tool": "write_file", "args": {"path": "notes/a.txt", "content": "hello\n"}, "expect": "ok"},
  {"tool": "write_file", "args": {"path": ".agent-fox/x", "content": "x"}, "expect": "denied"},
  {"submit": {"summary": "done", "files": ["notes/a.txt"]}, "expect": "accepted"}
]}
```

A step has exactly one of two forms:

| Step | `expect` |
|---|---|
| `{"tool": name, "args": {...}}` calls a sandbox tool through the tool gate. | `ok`, `error` (the tool failed) or `denied` (the gate refused it). |
| `{"submit": payload}` submits a payload. | `accepted` or `refused`. |

Each step is one turn. An accepted submission ends the replay; so does
`--max-turns` or the end of the steps. A result that does not match `expect`
stops the replay with an error naming the step. A script that cannot be read,
has an unknown field, a step with both forms or neither, or an `expect` that
does not fit is a `usage` failure naming the file and the step (counted from
0). It is reported before any sandbox opens.

### Testing a brain

Run a brain through `brain.Execute` over a real `local` box on a temporary git
repository and a real run directory. Drive the `agentkit` brain with AgentKit's
`faux` provider, which plays a model's turns and records what it was sent:

```go
p := faux.New(
    faux.ToolCall("c1", "write_file", `{"path":"a.txt","content":"x"}`), // not in the allowlist: denied
    faux.ToolCall("c2", "submit_test", `{"summary":"done"}`),            // the terminating tool
)
b, _ := brain.Default.New("agentkit", brain.Options{
    Model:       "faux-1",
    Getenv:      func(string) (string, bool) { return "", false }, // read no key
    NewProvider: func(string) (core.ProviderClient, error) { return p, nil },
})
res, err := brain.Execute(ctx, brain.Request{ /* Session, Name, Brain: b, Brief, RunDir, Report, Tally */ })
// p.Requests()[1] is what the model saw after its first turn: the denial.
```

Give a turn a cost with `faux.Turn.Usage` to test the cost bound, or set
`Brief.MaxTurns` for the turn bound. Assert on the run's files and ledger, not
on what the brain printed: `res.Payload`, the run directory's
`submission.json`, the report's warnings and artifacts, and
`RunDir.Ledger()`. `internal/brain`, `internal/box` and `internal/submission`
run with no key and no network; `TestTheBrainsNeedNoKeyAndNoNetwork_TS_07_79`
counts HTTP requests to prove it.

A triage test can use the scripted brain, the `agentkit` brain on `faux`, or a
spy brain of its own. `TestOnlyTheAllowedWarningCodesAndCategories_TS_08_64`
checks that every envelope uses only documented warning codes and failure
categories.

### Tests and the sandbox

The default sandbox is `podman`, and no test may need it. Every test in
`internal/*` that runs `af triage` or `af fix` passes `--sandbox local`. The
helpers `triageEnv.loop`, `fixEnv.fix` and `fixEnv.fixRun` add it for you; a
later `--sandbox` in a test's arguments wins. A test of the default selection
sets or clears `AF_SANDBOX` instead.
`TestEveryTestThatRunsTriageOrFixSaysWhichBox_TS_10_62` parses the test
sources and fails on any invocation that does neither. It cannot see a call
whose command word is held in a variable.

### Property and fuzz tests

Property tests use a generator seeded by the test, so a failure repeats.
Parsers and decisions that take text have fuzz targets:

```bash
go test ./internal/policy -run xxx -fuzz FuzzDecide
go test ./internal/service/jobs -run xxx -fuzz FuzzParseIssue
```

## Opt-in tests

Tests that need something `make check` must not depend on are gated by
`internal/optin`. Each skips, never fails, and says what is missing.

| Variable | Set to `1` to run | Needs |
|---|---|---|
| `AF_TEST_PODMAN` | The sandbox invariants and `af fix` end to end under `podman` | `podman info` answering, and the image (`AF_SANDBOX_IMAGE`, else `quay.io/agentfox/tools:latest`; `make build-tools-container` builds it). |

```bash
AF_TEST_PODMAN=1 go test ./internal/brain ./internal/afcmd -run TS_10_63 -v
```

The same scenarios also run offline under `local`, so `make check` covers them.

## Adding a command

1. Write the logic as a package under `internal/` that returns plain results
   and typed errors, with its tests. It must not import `internal/toolio`,
   call `os.Exit` or panic on bad input.
2. Add `internal/afcmd/<name>.go` with a `registerX(app)` that calls
   `app.Register` with the command path, summary, usage, flags and default
   timeout, and call it from `register` in `internal/afcmd/afcmd.go`. Map the
   package's errors onto `toolio.Failure` categories. (Service client commands
   live in `cmd/af/` instead.)
3. If it is a fact command (like `lang detect`), add it to the lists in
   `internal/afcmd/ring1_test.go`.
4. Document it: [cli.md](cli.md); [configuration.md](configuration.md) if it
   reads a file or a variable; [model-usage.md](model-usage.md) if it runs a
   model; [architecture.md](architecture.md) if it changes the layout.
5. Run `make check`.

## Container images

| Target | Builds |
|---|---|
| `make build-sandbox-container` | The sandbox image from `containers/sandbox/Containerfile`. |
| `make build-tools-container` | The tools image from `containers/tools/Containerfile`: the sandbox image with `af` built in. It is the default image of a `podman` sandbox. |
| `make build-containers` | Both, in that order. |

Images are tagged `$(CONTAINER_REGISTRY)/<name>:$(VERSION)` and `:latest`;
`CONTAINER_REGISTRY` defaults to `quay.io/agentfox`. The tools image clones
agentkit-go at `AGENTKIT_REF` (default `main`). A push to `main` builds the
sandbox image in CI (`.github/workflows/build-containers.yaml`).

The `af` in an image should match the `af` that uses it. A mismatch raises the
warning `box_af_version_mismatch`; see [cli.md](cli.md#warning-codes).

## Skills

`skills/af-*` are skills for coding agents: `af-triage`, `af-fix`,
`af-bug-hunter`, `af-code-quality` and `af-code-simplifier`.

```bash
make install-skills     # copy each to ~/.claude/skills/<name>/SKILL.md
make uninstall-skills   # remove them again
```

The `skills` Go package embeds every `af-*` file. The briefs of `af triage`
and `af fix` carry the bodies of `af-triage` and `af-fix`, so the brain reads
exactly the skill a person installs.

## Documentation

Update the documentation in the same change as the code. Run `make doc` first
to refresh the generated package docs. The style rules are in
[.specs/steering.md](../.specs/steering.md#documentation-style).

| Document | Owns |
|---|---|
| [README.md](../README.md) | What agent-fox is, a short example, where to go next. |
| [cli.md](cli.md) | The `af` and `agent-fox` command lines. |
| [api.md](api.md) | The HTTP API of the service. |
| [configuration.md](configuration.md) | Environment variables, repository files, the service's `config.toml` and hub workspace variables. |
| [model-usage.md](model-usage.md) | Which commands call a model, what they send and what it costs. |
| [architecture.md](architecture.md) | How it is built, and why. |
| development.md | This guide. |
| [prd/](prd/) | Product requirements. |
| [errata/](errata/) | Where the code diverges from its spec. |
| [drafts/](drafts/) | Design drafts, including the archived target architecture. |
| [generated/](generated/) | Go package docs, written by `make doc`. Never edit by hand. |

[AGENTS.md](../AGENTS.md) is the policy every coding-agent session follows.
