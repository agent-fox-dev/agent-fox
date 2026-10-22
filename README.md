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
| `cmd/af/` | The `af` binary: it registers the commands on the shell and hands the process to it. Sixteen commands: `lang detect`, `gate run`, `gate compare`, `revert-check`, `guard check` and `map`, the five of `af triage` (`triage`, `triage brief`, `triage run`, `triage submit` and `triage land`) and the five of `af fix` (`fix`, `fix brief`, `fix run`, `fix submit` and `fix land`). [`docs/cli.md`](docs/cli.md) describes them. |
| `cmd/agent-fox/` | The `agent-fox` binary, the long-running service: it starts from one `config.toml`, keeps its state in one SQLite file, asks hub who is calling, and lets a caller subscribe a hub workspace by handing over a workspace token, which it keeps sealed and never returns. It runs no model in this release. [`docs/cli.md`](docs/cli.md#the-agent-fox-command-line) describes the command line, [`docs/api.md`](docs/api.md) the routes and [`docs/configuration.md`](docs/configuration.md#the-agent-fox-service) the file. |
| `internal/service/` | The packages of the service: `config` (the file), `secrets` (the key and the sealing of tokens), `store` (the SQLite file and its migrations), `ttl` (the cache of hub's answers) and `api` (the HTTP surface). |
| `internal/hub/` | The client of hub's API, and `fakehub`, the `httptest` hub of the tests. |
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
| `.specs/` | `steering.md` holds the directives every session follows; `archive/` holds the specs of `issuex`, which now lives in `aflib`. |
| `docs/` | `cli.md`, `configuration.md` and `model-usage.md` describe `af` as it is, and [`docs/development.md`](docs/development.md) how to work on it; `architecture.md` is the proposed target architecture, `prd/` holds the product requirements, `drafts/` holds design drafts, `errata/` holds the errata of the specs, `generated/` the Go package docs (`make doc`). |

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
make check          # gofmt + go vet + all tests
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

The test suite needs no API key, no token, no container engine and no network. It does start
`httptest` servers on the loopback interface, so a sandbox that forbids
binding a local port fails it.
