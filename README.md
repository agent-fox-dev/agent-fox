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
| `internal/toolio/` | The command shell behind `af`: the envelope and its exit codes, `needs_human` and `--context`, `--dry-run`, `--schema`, the positional input, and the run directory with its events and ledger. [`docs/cli.md`](docs/cli.md) describes it. |
| `internal/gitx/` | The git and shell invocations the tools make, hermetic and issued from Go. |
| `internal/agentdir/` | The git-ignored `.agent-fox/` directory and the atomic write of its files. |
| `internal/project/` | What a repository is written in and how it is checked: languages, the gate table, suite forms, programs, test paths. |
| `internal/checks/` | The file view and the tree hash, the gate engine with its cache by tree hash, labels, and the comparison of two gate results. |
| `internal/conform/` | The revert check: do the tests of a change fail without it, with the tree and the index given back. |
| `internal/policy/` | The guard policy: whether a brain may make a tool call. |
| `internal/repomap/` | The repository map at a token budget. |
| `internal/box/` | The box that holds a brain's tree: the checkout goes in with its `.git`, run files are placed under `.agent-fox/`, the tree comes out without `.git`. One provider, `local`, which has no isolation. |
| `internal/submission/` | The submission protocol: a brain hands back one validated JSON object through the submit core, and the host checks the file again. |
| `internal/brain/` | The `Brain` interface, its registry and shared flags, `Execute` (run a brief and decide from the file), the tool gate, and two brains: `scripted` and the AgentKit driver on the host. |
| `internal/triage/` | `af triage` without the shell: the diagnosis and its validator, the issue body and its markers, the brief, the preflight, the landing on a forge, the submission kind and the tool allowlist, with `fakeforge`, the GitHub and GitLab server of its tests. |
| `internal/fix/` | `af fix` without the shell: the preflight and the baseline, the brief, the `fix_report` submission kind, apply (the box's tree onto the checkout), measure (protected paths, scope, the gate against the baseline, the revert check), land and park (branch, commit, push, pull request), the rounds, attempts and bounds of the loop, and the result. |
| `skills/` | The coding-agent skills (`af-prd`, `af-impl`, `af-fix`, `af-triage`, `af-bug-hunter`, `af-code-quality`, `af-code-simplifier`), and the Go package `skills` that embeds every `af-*` entry, so that the briefs of `af triage` and `af fix` carry the same `af-triage` and `af-fix` that a person installs. `make install-skills` copies each `af-*` file to `~/.claude/skills/<name>/SKILL.md`. |
| `containers/sandbox/` | The sandbox image. |
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
make build          # go build ./..., and bin/af
bin/af --version
```

The test suite needs no API key, no token and no network. It does start
`httptest` servers on the loopback interface, so a sandbox that forbids
binding a local port fails it.
