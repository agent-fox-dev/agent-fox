# Development

## Prerequisites

- Go 1.26.5 or later
- A sibling checkout of [`coder`](https://github.com/agent-fox-dev/coder)

`coder` ships the AgentKit agent SDK that `agentspec` runs on. Its module path
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
agentspec/                # LLM-powered spec creation (package agentspec)
  templates/              # Prompt templates, embedded at compile time
cmd/
  af/                     # The af CLI
  nightshift/             # The nightshift CLI
  spec/                   # The spec CLI (cobra commands + main)
docs/                     # Documentation, ADRs and PRDs
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

- **agentspec** (`agentspec/`) — AI session layer, built on AgentKit. The
  SpecAgent pipeline (AssessPRD, RefinePRD, GenerateArtifacts), TOML
  configuration, tier aliases over the model catalog, campaign directory
  lifecycle, the session state machine with atomic persistence, prompt
  templates, and the submit tools each phase ends with.

  The files worth knowing: `runner.go` builds the `core.AgentConfig` a phase
  runs under and holds the read-only invariant; `tools.go` defines the submit
  tools whose handlers validate and terminate; `jsonschema.go` converts
  afspec's embedded JSON Schemas into the structured schema a tool declares;
  `models.go` is the tier table over `catalog.ResolveModel`; `credentials.go`
  is the preflight. See [ADR 02](adr/02-build-the-spec-pipeline-on-agentkit.md).

- **cmd/spec** (`cmd/spec/`) — the `spec` CLI binary, built with cobra. Wires
  afspec and agentspec together behind `new`, `list`, `refine`, `generate`,
  `validate`, `lint`, `render`, `status`, `campaign`, `activate`, `seal`,
  `archive`, `supersede`, `models` and `migrate`.

## Common tasks

All tasks are driven through `make`. Run from the repository root.

| Command             | Description                                            |
|---------------------|--------------------------------------------------------|
| `make check`        | Run lint and all tests (run this before committing)    |
| `make test`         | Run all Go tests: `go test ./... -count=1`             |
| `make lint`         | Lint Go source: `gofmt` + `go vet`                     |
| `make format`       | Auto-format Go source: `gofmt -w`                      |
| `make build`        | Build `af`, `nightshift` and `spec` into `bin/`        |
| `make build-all`    | Cross-compile the spec CLI into `dist/`                |
| `make clean`        | Remove build artifacts                                 |
| `make json-gen`     | Regenerate Go artifact types from the schemas          |

Tests need no API key and make no network calls: the phases that call a model
are driven by `provider/faux`, AgentKit's scripted provider, so a test asserts
on the requests that reached the wire rather than on arguments this repository
built for itself.

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
