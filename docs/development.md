# Development

## Prerequisites

- Go 1.26.5 or later
- A sibling checkout of [`coder`](https://github.com/agent-fox-dev/coder)

`coder` ships the AgentKit agent SDK the three tools run on. Its module path
is `github.com/agentfox/agentkit-go` while its repository is
`agent-fox-dev/coder`, so the module proxy cannot serve it and it is consumed
through a `replace` to a checkout beside this one:

```bash
git clone https://github.com/agent-fox-dev/coder ../coder
```

Point the replace elsewhere if your layout differs:

```bash
go mod edit -replace github.com/agentfox/agentkit-go=/path/to/coder
```

This is the same arrangement `coder`'s own `examples/flatline` uses for the
spec library, and it goes away when the module path is fixed upstream.

## Repository layout

```
afspec/                   # Spec format library (package afspec)
  schemas/                # Bundled JSON Schemas, embedded at compile time
specgen/                  # The spec pipeline
  templates/              # Prompt templates, embedded at compile time
issuetriage/              # The triage pipeline
codefix/                  # The fix pipeline
internal/
  toolio/                 # Input classification, the JSON envelope, the shared CLI shell
  agentrun/               # Model resolution, the phase runner, the read-only invariant, the shell guard
  ghapi/                  # GitHub REST client
  gitx/                   # git, and the process runner
  checks/                 # Detecting and running a project's own quality command
cmd/
  spec/  issue/  fix/     # The three tools
  af/  nightshift/        # Stubs
docs/                     # Documentation, ADRs and PRDs
skills/                   # The markdown skills the tools replaced, kept for reference
testdata/                 # Shared test fixtures
bin/, dist/               # Build output
```

The Go module is rooted at the repository root under module path
`github.com/agent-fox-dev/agentfox`. The root package (`agentfox`) holds only
build metadata (`version.go`).

## Setup

```bash
git clone https://github.com/agent-fox-dev/agent-fox.git
git clone https://github.com/agent-fox-dev/coder.git
cd agent-fox
go mod download
```

## Go package structure

- **agentfox** (repository root) — build-time metadata (`Version`, `Commit`,
  `BuildTime`), injected via `-ldflags`.

- **afspec** (`afspec/`) — core library. Spec loading, validation (schema and
  cross-file rules C1–C11), lifecycle state machines, EARS criterion builders,
  dependency graphs, coverage and traceability derivation, rendering, and
  discovery. Types are value-oriented — mutation methods return new copies. No
  goroutine-safety guarantees; callers must synchronize externally.

- **internal/toolio** — the shared interface. `input.go` classifies the one
  positional argument; `envelope.go` is the JSON object every tool writes and
  the exit-code table; `app.go` is the shell each command is a value of;
  `cli.go` holds the shared flags and model resolution; `progress.go` writes
  stderr.

- **internal/agentrun** — the AgentKit layer. `phase.go` builds the
  `core.AgentConfig` one phase runs under, installs compaction and drives the
  stream; `policy.go` holds the read-only invariant; `guard.go` is the shell
  authorization boundary; `models.go` is the tier table over
  `catalog.ResolveModel`; `credentials.go` is the preflight. See
  [ADR 02](adr/02-build-the-spec-pipeline-on-agentkit.md).

- **internal/ghapi** — a dependency-free GitHub REST client. It lives outside
  every agent on purpose: no model is given a tool that reaches it.

- **internal/gitx**, **internal/checks** — every git command a tool runs, and
  the detection and execution of the command that decides whether a change is
  correct.

- **specgen**, **issuetriage**, **codefix** — one package per tool. Each holds
  its schemas, its prompts, its terminating tools and its pipeline. In `specgen`
  and `codefix` the model half is an interface (`author`, `brain`) so a test can
  drive the real pipeline with a scripted one; `issuetriage` has a single phase
  and is tested through the real agent loop against a scripted provider instead.

- **cmd/spec**, **cmd/issue**, **cmd/fix** — each is a `toolio.App` value and
  an `os.Exit`. See [ADR 03](adr/03-rebuild-the-skills-as-tools.md).

## Common tasks

All tasks are driven through `make`. Run from the repository root.

| Command             | Description                                            |
|---------------------|--------------------------------------------------------|
| `make check`        | Run lint and all tests (run this before committing)    |
| `make test`         | Run all Go tests: `go test ./... -count=1`             |
| `make lint`         | Lint Go source: `gofmt` + `go vet`                     |
| `make format`       | Auto-format Go source: `gofmt -w`                      |
| `make build`        | Build every CLI into `bin/`                            |
| `make build-all`    | Cross-compile `spec`, `issue` and `fix` into `dist/`   |
| `make clean`        | Remove build artifacts                                 |
| `make json-gen`     | Regenerate Go artifact types from the schemas          |

Tests need no API key, no GitHub token and no network:

- the phases that call a model run against `provider/faux`, AgentKit's scripted
  provider, so a test asserts on the `core.Request` values that reached the wire
  rather than on arguments this repository built for itself;
- GitHub runs against an `httptest` server;
- git runs against real temporary repositories, because the wrapper's whole job
  is to get git's own behaviour right and a fake git would only confirm the
  wrapper's assumptions about it.

The pipelines are tested through their real `Run`, with only the model half
replaced. That split — judgment in the model, everything else in Go — is the
design claim the tools make, and having it be an interface is what makes it
checkable.

## Schema workflow

The canonical JSON Schemas for the spec format live in the
[`spec`](https://github.com/agent-fox-dev/spec) repository under
`specification/schemas/`. The copies in `afspec/schemas/` are what this library
compiles and embeds:

- `prd-frontmatter.v2.json`
- `requirements.v2.json`
- `tasks.v2.json`
- `test_spec.v2.json`

After syncing a changed schema, regenerate the Go struct types with
[go-jsonschema](https://github.com/atombender/go-jsonschema):

```bash
make json-gen
```

The generated files are `afspec/*.v2.go`. Always run `make check` afterwards.

## Git workflow

- Branch from `main` using `feature/<descriptive-name>`.
- Never commit directly to `main`.
- Use conventional commit messages: `feat:`, `fix:`, `refactor:`, `docs:`,
  `test:`, `chore:`.
- Run `make check` before every commit.
