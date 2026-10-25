# agent-fox

agent-fox is an AI coding agent that triages and fixes issues in GitHub and
GitLab repositories. Point it at an issue and it diagnoses the problem,
writes a fix, proves the fix with the project's own tests, and opens a pull
request. You can run it from your terminal, or run it as a service that
picks up issues your team labels `af:triage` or `af:fix`.

## Why agent-fox

Letting a language model change a codebase raises two questions: can you
trust what it did, and can you trust what it can reach? agent-fox answers
both by splitting the work in two.

- **The model does the judgment.** It reads the code, works out the root
  cause, and edits files — inside a sandbox that holds a copy of the
  repository and no forge or git credentials.
- **The program does everything else.** `af`, the agent-fox command-line
  tool, runs the project's checks before and after the change, reverts the
  fix to confirm the new tests fail without it, and makes every commit, push,
  comment and pull request itself.

A change lands only when every check passes after it. The pull request says `Closes #N` only when the tests are proven to fail without the
fix; otherwise it says `Refs #N` and asks a person to look. When the model
needs a decision, it stops and asks a question instead of guessing.

## What it does

| | |
|---|---|
| **Triage** | Reads an issue (or any bug report), investigates the code, and rewrites the issue with a structured diagnosis: root cause, affected files, suggested fix and acceptance criteria. The reporter's original text is kept. |
| **Fix** | Takes an issue, makes the smallest change that fixes it, and measures it. A change that passes the checks becomes a branch and a pull request; one that does not is kept on a `wip:` branch for a person to review. |
| **Service** | `agent-fox` runs triage and fix jobs unattended for a team. It queues requests from its API or from issue labels, runs each in a sandbox, and reports progress back on the issue with status labels and comments. |

## Example

Triage an issue, then fix it, from a checkout of the repository:

```bash
export ANTHROPIC_API_KEY=...   # the model
export GITHUB_TOKEN=...        # to read the issue and open the pull request

cd ~/src/shop
af triage https://github.com/acme/shop/issues/42
af fix    https://github.com/acme/shop/issues/42
```

Every `af` command prints one JSON document — the *envelope* — with the
status, the result, any warnings, and suggested next commands. If the agent
needs an answer, the command exits with code 3 and prints the question;
re-run it with `--context "<answer>"` to continue.

With the service running, a team member only needs to label an issue
`af:fix`. From the terminal, with `AF_URL` pointing at the service, the same
request is:

```bash
af jobs submit fix 42 --workspace shop --wait
```

## How it fits together

- **`af`** runs triage and fix on your machine, in a local
  [Podman](https://podman.io) container or, for quick trials, directly on the
  host. It is also the command-line client of the service.
- **`agent-fox`** is the long-running service. It does not talk to the forge
  or run sandboxes itself:
  [hub](https://github.com/agent-fox-dev/hub) provides identity, workspaces,
  the git server, sandboxes and the forge connection.
- The agent is built on [agentkit-go](https://github.com/agent-fox-dev/agentkit-go),
  and the forge client and job queue come from
  [aflib](https://github.com/agent-fox-dev/aflib).

## Documentation

| Document | What it covers |
|---|---|
| [docs/cli.md](docs/cli.md) | Every `af` command and the `agent-fox` command line: flags, output, statuses and exit codes |
| [docs/api.md](docs/api.md) | The service's HTTP API, workspaces, jobs, and the issue label protocol |
| [docs/configuration.md](docs/configuration.md) | Environment variables, the service's `config.toml`, per-workspace settings, and repository files |
| [docs/model-usage.md](docs/model-usage.md) | Which commands call a model, what the model is sent, and what it costs |
| [docs/architecture.md](docs/architecture.md) | How agent-fox is built and why |
| [docs/development.md](docs/development.md) | Building, testing and changing the code |

Product requirements are in [docs/prd/](docs/prd/), and the decisions that
shaped the code, where it differs from its specifications among them, are in
[docs/adr/](docs/adr/).

## Installation

agent-fox is built from source. You need Go 1.27 and git, plus Podman to run
the default sandbox.

```bash
git clone https://github.com/agent-fox-dev/agentkit-go
git clone https://github.com/agent-fox-dev/agent-fox
cd agent-fox
make build          # writes bin/af and bin/agent-fox
bin/af --version
```

agentkit-go must sit next to this repository: `go.mod` points at
`../agentkit-go`.

`af triage` and `af fix` run the model in a Podman container by default.
Build its image, `quay.io/agentfox/tools` (the sandbox image with `af` in it),
with `make build-containers`, or pass `--sandbox local` to run on the host
without isolation. To run the service, write a `config.toml` and a key file —
see [the agent-fox service](docs/configuration.md#the-agent-fox-service).

## Development

```bash
make check    # format, vet, lint and the short tests; run before every commit
make help     # all targets
```

The tests need no network, API key or container engine. See
[docs/development.md](docs/development.md) for the layout, the test fakes and
how to add a command. Coding agents working on this repository follow
[AGENTS.md](AGENTS.md).

## License

[Apache License 2.0](LICENSE)
