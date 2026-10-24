# agent-fox

agent-fox is an autonomous spec-first coding agent (golang version).

This repository is being rebuilt from a clean state. The previous
implementation (the `triage`, `fix` and `impl` tools and the packages under
them) was removed; `1e6458d` is the last commit that has it.

It depends on

- [`agentkit-go`](https://github.com/agent-fox-dev/agentkit-go) — the AgentKit SDK
  (module `github.com/agent-fox-dev/agentkit-go`)
- [`aflib`](https://github.com/agent-fox-dev/aflib) — the packages shared by the
  agent-fox repositories: `issuex`, the forge client (one interface over GitHub
  and GitLab for issues, labels, and pull or merge requests), and `jobqueue`
  (module `github.com/agent-fox-dev/aflib`)

and works with [`hub`](https://github.com/agent-fox-dev/hub) as its backend
service. The spec format and the `spec` tool that writes specification
packages live in the [`spec`](https://github.com/agent-fox-dev/spec)
repository.

## What is here

| Path | Purpose |
|---|---|
| `cmd/af/` | The `af` binary: it makes the shell from `internal/afcmd`, adds the commands of the client of the agent-fox service and hands the process to it. [`internal/afcmd`](internal/afcmd) holds the commands of the loops and of ring 1, so that `agent-fox` can run them in its own process. Twenty-seven commands: `lang detect`, `gate run`, `gate compare`, `revert-check`, `guard check` and `map`, the five of `af triage` (`triage`, `triage brief`, `triage run`, `triage submit` and `triage land`) and the five of `af fix` (`fix`, `fix brief`, `fix run`, `fix submit` and `fix land`), and the eleven of the service client: `login`, `workspace subscribe`, `workspace list`, `workspace get` and `workspace unsubscribe`, and `jobs list`, `jobs get`, `jobs watch`, `jobs cancel`, `jobs answer` and `jobs retry`. `af triage` and `af fix` need `--local`, to run on this machine, or `--workspace`, to queue a job on the service. [`docs/cli.md`](docs/cli.md) describes them. |
| `internal/afclient/` | The library of the service client: the server URL, the credential file `~/.af/config.toml`, the requests with their bounds and the mapping of the answers, and the following of a job's event stream. |
| `cmd/agent-fox/` | The `agent-fox` binary, the long-running service: it starts from one `config.toml`, keeps its state in one SQLite file, asks hub who is calling, and lets a caller subscribe a hub workspace by handing over a workspace token, which it keeps sealed and never returns. It runs triage and fix attempts for the jobs it queues: the worker (`internal/worker`) runs the `af` commands in its own process against a hub workspace, with a sandbox that hub leases, and mirrors the outcome to the issue. [`docs/cli.md`](docs/cli.md#the-agent-fox-command-line) describes the command line, [`docs/api.md`](docs/api.md) the routes and [`docs/configuration.md`](docs/configuration.md#the-agent-fox-service) the file. |
| `internal/service/` | The packages of the service: `config` (the file), `secrets` (the key and the sealing of tokens), `store` (the SQLite file and its migrations), `ttl` (the cache of hub's answers) and `api` (the HTTP surface) and `jobs` (the jobs of triage and fix: the store, the queue, the runner seam, the event log and the retention) and `reconcile` (the pass that gives back the sandboxes and the sessions that attempts left held) and `poll` (the label poll: a subscribed workspace picks up the issues that carry its request labels). |
| `internal/hub/` | The client of hub's API (identity, and the calls of an attempt), the forge through hub, and `fakehub`, the `httptest` hub of the tests with its git server and gateway. |
| `internal/worker/` | The worker: the production `Runner` of the jobs layer. It resolves an attempt (the variables of the workspace, the refusals), opens a hub session, marks the issue, clones through hub's git server, leases a gateway box, runs the triage or the fix loop in process, mirrors the outcome as labels and comments, and reports to hub. Infrastructure failures are transient errors; every other ending is an envelope. An attempt that is handed over again after a crash or a restart is carried on from a journal it keeps in `recovery/` (`internal/worker/recovery`). |
| `internal/afcmd/` | The commands of `af`, importable: the loops and the ring-1 commands with the seams (forge, boxes, brains, git runner, earlier questions, measuring in the box) that `agent-fox` fills in. |
| `internal/toolio/` | The command shell behind `af`: the envelope and its exit codes, `needs_human` and `--context`, `--dry-run`, `--schema`, the positional input, and the run directory with its events and ledger. [`docs/cli.md`](docs/cli.md) describes it. |
| `internal/gitx/` | The git and shell invocations the tools make, hermetic and issued from Go. |
| `internal/agentdir/` | The git-ignored `.agent-fox/` directory and the atomic write of its files. |
| `internal/project/` | What a repository is written in and how it is checked: languages, the gate table, suite forms, programs, test paths. |
| `internal/checks/` | The file view and the tree hash, the gate engine with its cache by tree hash, labels, and the comparison of two gate results. |
| `internal/conform/` | The revert check: do the tests of a change fail without it, with the tree and the index given back. |
| `internal/policy/` | The guard policy: whether a brain may make a tool call. |
| `internal/repomap/` | The repository map at a token budget. |
| `internal/box/` | The box that holds a brain's tree: the checkout goes in with its `.git`, run files are placed under `.agent-fox/`, the tree comes out without `.git`. Four providers: `landlock` and `podman`, which isolate, `local`, which does not, and `none`, which is no box but the checkout itself. |
| `internal/submission/` | The submission protocol: a brain hands back one validated JSON object through the submit core, and the host checks the file again. |
| `internal/brain/` | The `Brain` interface, its registry and shared flags, `Execute` (run a brief and decide from the file), the tool gate, and three brains: `scripted`, the AgentKit driver, and headless Claude Code (`claude`), with `Process`, which runs a brain that is a process. |
| `internal/triage/` | `af triage` without the shell: the diagnosis and its validator, the issue body and its markers, the brief, the preflight, the landing on a forge, the submission kind and the tool allowlist, with `fakeforge`, the GitHub and GitLab server of its tests. |
| `internal/fix/` | `af fix` without the shell: the preflight and the baseline, the brief, the `fix_report` submission kind, apply (the box's tree onto the checkout), measure (protected paths, scope, the gate against the baseline, the revert check), land and park (branch, commit, push, pull request), the rounds, attempts and bounds of the loop, and the result. |
| `skills/` | The coding-agent skills (`af-prd`, `af-impl`, `af-fix`, `af-triage`, `af-bug-hunter`, `af-code-quality`, `af-code-simplifier`), and the Go package `skills` that embeds every `af-*` entry, so that the briefs of `af triage` and `af fix` carry the same `af-triage` and `af-fix` that a person installs. `make install-skills` copies each `af-*` file to `~/.claude/skills/<name>/SKILL.md`. |
| `internal/fakeclaude/`, `internal/optin/` | Test support: the fake `claude` and `af` that the tests run as, and the gates of the tests that need a real Landlock kernel, a container engine or the real Claude Code (`AF_TEST_LANDLOCK`, `AF_TEST_PODMAN`, `AF_TEST_CLAUDE`). |
| `containers/sandbox/` | The sandbox image. |
| `containers/tools/` | The tools image, `quay.io/agentfox/tools`: the sandbox image with `af` and Claude Code in it, which a `podman` box runs by default (`AF_SANDBOX_IMAGE` names another). `make build-tools-container` builds it. |
| `.specs/` | `steering.md` holds the directives every session follows; `archive/` holds the specs that are implemented (01 to 04 are `issuex`'s, which now lives in `aflib`; 05 to 19 are this repository's). The `TS-…` and `REQ-…` ids in the tests refer to them. |
| `docs/` | `cli.md`, `configuration.md` and `model-usage.md` describe `af` as it is, and [`docs/development.md`](docs/development.md) how to work on it; `architecture.md` describes how it is built, `prd/` holds the product requirements, `drafts/` holds design drafts and the archived target architecture, `errata/` holds the errata of the specs, `generated/` the Go package docs (`make doc`). |

[AGENTS.md](AGENTS.md) is the policy for every coding session in this
repository.

## Building

AgentKit is consumed through a `replace` to a sibling checkout (two of its
modules are used: the root, and `sandbox`, which holds the boxes, and both are
replaced to the same checkout):

```bash
git clone https://github.com/agent-fox-dev/agentkit-go ../agentkit-go
```

`aflib` is an ordinary Go module: `go.mod` requires it at a commit (it has no
tag yet), and `go` fetches it, so no checkout is needed. To change `aflib` and
agent-fox together, put both in a `go.work` (it is git-ignored):

```bash
git clone https://github.com/agent-fox-dev/aflib ../aflib
go work init . ../aflib
```

```bash
make check          # gofmt + go vet + golangci-lint + the short tests
make build          # go build ./..., and bin/af and bin/agent-fox
bin/af --version
bin/agent-fox --version
```

## Running the service

`agent-fox` needs a configuration file that sets where hub is and where its key is, and a key. A key file
is 32 random bytes in base64, and the service refuses one that group or others can read:

```bash
(umask 077; openssl rand -base64 32 > key)
cat > config.toml <<'EOF'
[hub]
url = "https://hub.example.com"

[secrets]
key_file = "key"
EOF
bin/agent-fox --config config.toml
```

It listens on `127.0.0.1:8090` unless `server.listen` says otherwise, answers `GET /healthz`,
`GET /readyz` and `GET /version` without a token, and stops on SIGINT or SIGTERM. Its log is one JSON
record per line on stderr. See [`docs/cli.md`](docs/cli.md#the-agent-fox-command-line),
[`docs/configuration.md`](docs/configuration.md#the-agent-fox-service) and [`docs/api.md`](docs/api.md).

Subscribing a workspace, or subscribing it again, creates on its repository the seven labels a team uses with agent-fox
(`af:triage` and `af:fix` to ask, and the five status labels the service sets), so a workspace subscribed before this release
gets them at its next subscribe.

A subscribed workspace picks up labelled issues by itself, so that a team asks for a triage or a fix just by labelling an
issue with the workspace's `af:triage` or `af:fix` label. Every `AF_POLL_INTERVAL` (5 minutes by default) the
service lists, through hub, the open issues that carry the labels; each new request is one job, and the label is removed.
`AF_POLL_INTERVAL=0` turns label-driven jobs off. See
[`docs/api.md`](docs/api.md#jobs-that-a-request-label-makes) and
[`docs/configuration.md`](docs/configuration.md#the-label-poll).

A team also carries a job forward from the issue. A person answers a blocked job, or asks again after a failed one, by
commenting and putting the request label back: the comments since the service's question or failure comment become the
answer, or the new attempt's context, and when there are none the service says so on the issue. See
[`docs/api.md`](docs/api.md#answering-and-retrying-from-the-forge).

The test suite needs no API key, no token, no container engine and no network. It does start
`httptest` servers on the loopback interface, so a sandbox that forbids
binding a local port fails it.

## Using the service from af

`af` is the client of the service. With `AF_URL` naming it and a hub credential in a file, a person logs in once, subscribes a
hub workspace with its token, and queues jobs:

```bash
export AF_URL=http://127.0.0.1:8090
bin/af login --token-file hub-token                       # checks the credential, writes ~/.af/config.toml (mode 0600)
bin/af workspace subscribe widgets --hub-token-file pat   # the workspace token, kept sealed by the service
bin/af fix 42 --workspace widgets --wait                  # queue a fix job and follow it until it is at rest
bin/af jobs list --workspace widgets --state blocked      # one page of jobs; jobs get, watch, answer, retry and cancel
bin/af fix https://github.com/o/r/issues/1 --local        # the loop on this machine, as before
```

`af triage` and `af fix` name where the run goes: `--workspace <slug>` queues a job, `--local` runs the loop here, and
neither is a usage error. A job that is blocked on a question ends `--wait` with exit 3 and the command that answers it
(`af jobs answer <id> --wait`, with `--context`). Every command prints the one envelope and uses the exit codes of every `af`
command. See [`docs/cli.md`](docs/cli.md#the-client-of-the-agent-fox-service) and
[`docs/configuration.md`](docs/configuration.md#the-client-of-the-agent-fox-service).
