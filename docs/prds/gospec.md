# Go Port of agentspec and spec CLI

## Intent

The `spec` CLI tool and its underlying `agentspec` library are currently implemented in Python, requiring users to install Python 3.12+, `uv`, and manage a virtual environment just to run a single command-line tool. This creates friction for adoption — especially for Go-centric teams who would prefer a single static binary with zero runtime dependencies.

This PRD defines the work to port the `agentspec` Python package and the `spec` CLI to Go, producing a self-contained `spec` binary that is feature-for-feature compatible with the Python version. The Go `afspec` library already exists with full I/O, rendering, lifecycle, and discovery support; this effort builds the AI agent layer and CLI on top of it, and fills remaining gaps in the afspec library to support the full validation and lint pipeline. Once the Go version is complete and validated, the Python implementation will be deprecated.

## Goals

- Produce a `spec` Go binary that is a drop-in replacement for the Python `spec` CLI — same commands, same flags, same JSON output shapes, same exit codes.
- Implement a Go `agentspec` package with feature parity to the Python `agentspec` package: session state machine, campaign management, LLM client with Anthropic API integration, prompt template system, and tool definitions.
- Complete the Go `afspec` library by filling validation, mutation, and lint gaps so the CLI can run `validate`, `lint`, and programmatic spec editing without falling back to Python.
- Single-binary distribution: the Go `spec` binary must be distributable as a single static binary with no runtime dependencies (all prompt templates embedded at compile time).
- Cross-platform builds for darwin/arm64, darwin/amd64, linux/arm64, linux/amd64.

## Non-goals

- Session interoperability between Go and Python — Go sessions are Go-only. Users migrating from Python re-run sessions with the Go CLI.
- Porting the Python test suite verbatim — Go tests will cover the same behaviors but use Go-idiomatic test patterns (table-driven tests, stdlib testing package).
- GUI or TUI beyond the existing ASCII banner and progress spinner.
- Supporting LLM providers other than Anthropic (direct API, Vertex AI, Bedrock) — matching the current Python scope.
- Changing any spec format behavior — the Go implementation must produce identical spec artifacts for identical inputs.
- Byte-identical validation error messages — structural parity (same JSON shape, equivalent meaning, same rule names) is sufficient.

## Functional Requirements

### afspec Validation Completion

- When validating a spec, the Go library must check all 10 cross-file integrity rules that the Python `afspec` library checks:
  - Every correctness property must have a corresponding property test.
  - Every execution path must have a corresponding smoke test.
  - Every `test_spec_id` referenced in traceability entries and subtask `test_spec_refs` must resolve to a known test spec entry.
  - Every backtick-wrapped term in criterion fields (`action`, `trigger`, `condition`, `error_condition`, `state`, `feature`) and property fields (`for_any`, `invariant`) must have a glossary entry. Terms that are numeric (including negatives and decimals), single characters, quoted strings, or longer than 80 characters are excluded from this check.
  - No duplicate `(requirement_id, test_spec_id)` pairs in traceability entries.
  - Every `requirement_refs` entry in subtasks must resolve to a known requirement or criterion ID.
  - Every criterion with `ears_pattern: unwanted` must have a non-null `return_contract`.
  - The first task group must have `kind: tests`, the last must have `kind: wiring_verification`.
  - The wiring verification group must contain at least one subtask with non-empty `test_spec_refs`, at least one smoke test reference matching `TS-*-SMOKE-*`, and at least one mention of stub or dead-code audit.
- When running cross-spec validation, the Go library must check all 5 cross-spec rules:
  - No two specs may declare the same external API symbol with different signatures.
  - No two interacting specs may have conflicting glossary definitions.
  - Every dependency declared in `tasks.json` must reference a known spec.
  - Interface contracts (return contracts on backtick-extracted symbols) must be consistent along dependency edges.
  - Every execution path actor that names an external spec boundary must have corresponding test coverage.
- The Go library must produce all 7 categories of validation warnings:
  - Task group with total `test_spec_refs` exceeding 15.
  - Task group with more than 6 non-verification subtasks.
  - Single subtask with more than 8 `test_spec_refs`.
  - Missing subtask `requirement_refs` or `test_spec_refs` (already implemented).
  - Error-indicating criteria or error_condition fields without a `return_contract`.
  - Vague language in criterion fields (words like "appropriate", "properly", "correctly").
  - Specs with more than 10 requirements (scope limit warning).
- `validate_structured` must produce structurally equivalent JSON output to the Python version: errors categorized as `schema` or `integrity`, warnings as a separate list.

### afspec Mutation Functions

- The Go library must provide collection mutation functions that return new copies (immutable pattern):
  - Add, get, and remove operations for requirements, glossary entries, correctness properties, execution paths, error handling entries, criteria, edge cases, test cases, property tests, edge case tests, smoke tests, task groups, subtasks, traceability entries, and dependencies.
  - All add operations must reject duplicate IDs with an error.
  - All mutation functions must return new struct copies, never mutate in place.
- Sequential ID generation: given a collection, return the next ID following the pattern (e.g., `{spec_id}-REQ-{N+1}`). Must handle empty collections (return 1), non-contiguous IDs (find max), and the correct ID format for each entity type.

### afspec Lint Module

- The Go library must provide a lint runner that discovers spec folders (requiring `requirements.json`), validates each, and returns sorted findings with severity levels (`error`, `warning`, `hint`).
- By default, fully-implemented specs (all subtasks `done` or `dropped`) are skipped. A lint-all option overrides this.
- Findings must be sorted by spec name, then file, then severity.
- Exit code is 1 if any finding has severity `error`, 0 otherwise.

### afspec Discovery: load_dependent_interfaces

- The Go library must provide a function that loads interface summaries (glossary, external APIs, interface symbols) from upstream dependency specs, returning an empty list on any failure for graceful degradation.

### agentspec Error Types

- The Go package must define an error hierarchy: a base `AgentSpecError`, plus `ConfigError`, `CampaignError`, `SessionError`, and `AgentError`.
- `AgentError` must carry structured metadata: `Category` (rate_limit, auth, transient, overloaded, input, internal, validation, refusal, context_window, pause_turn), `Retryable` (bool), `HTTPStatus` (optional int).
- All error types must support Go's `errors.As` and `errors.Is` for type-safe matching.

### agentspec Configuration

- Configuration is loaded from TOML files at `.specs/config.toml` (project-local) or `~/.specs/config.toml` (global). First file found wins — files are not merged.
- The `[model]` section contains a `model` key (tier name or model ID). The `[provider]` section contains `auth_method`, `vertex_project`, `vertex_region`.
- The `AF_SPEC_MODEL` environment variable always overrides the `model` field from the config file.
- Symlinked config files must be rejected.
- Invalid TOML must produce a `ConfigError`.

### agentspec Campaign Management

- A campaign is a directory containing `campaign.yaml` and numbered spec subdirectories.
- Creating a campaign writes `campaign.yaml` atomically (temp file + rename) with `name`, `description`, `created_at`, `updated_at` fields. Creating a campaign where `campaign.yaml` already exists is an error.
- Opening a campaign reads and parses `campaign.yaml`.
- Listing specs returns subdirectories matching the `{NN}_{snake_case}` pattern, sorted by numeric prefix, excluding `archive/`.
- Creating a new spec in a campaign validates the spec name against `[a-z][a-z0-9_]*`, computes the next numeric prefix by scanning both active specs and `archive/` (archive-aware numbering), creates the spec directory with `prd.md` (including YAML frontmatter), and initializes a session.

### agentspec Session State Machine

- Sessions track the lifecycle of authoring a single spec. The state machine has six states: `INIT`, `ASSESSING`, `REFINING`, `PRD_ACCEPTED`, `GENERATING`, `GENERATED`.
- Legal transitions: INIT→ASSESSING (assess), ASSESSING→REFINING (refine), ASSESSING→PRD_ACCEPTED (accept_prd), REFINING→ASSESSING (re-assess), REFINING→PRD_ACCEPTED (accept_prd), PRD_ACCEPTED→GENERATING→GENERATED (generate).
- Session state is persisted atomically to `_session.json` on every transition.
- Assessment history: each `assess()` and `refine()` call appends to the history. The `assessment` property returns the most recent entry.
- QA exchange recording: each successful `refine()` records `assessment_index`, `answers`, and `timestamp` (ISO 8601 UTC).
- Partial failure recovery: `generate()` writes artifacts incrementally. On resume in `GENERATING` state, existing artifact files are detected and only missing ones are regenerated.
- Errors are persisted as `last_error` in `_session.json` with message, category, retryable, and optional http_status.

### agentspec Session: Validation and Rendering

- `validate()` loads the spec via afspec and runs full validation, returning a result with `valid`, `schema_errors`, `integrity_errors`, and `repair_suggestions`.
- `render()` delegates to afspec's `render_combined` or `render_individual` functions.
- When `load_spec` fails (e.g., PRD lacks frontmatter), the session falls back to loading individual JSON artifacts.

### agentspec LLM Client

- Model resolution accepts a tier name (`SIMPLE`, `STANDARD`, `ADVANCED`) or a direct model ID string. Tier defaults: SIMPLE→claude-haiku-4-5, STANDARD→claude-sonnet-4-6, ADVANCED→claude-opus-4-6.
- The client creates a platform-aware Anthropic API client based on environment variables: `CLAUDE_CODE_USE_VERTEX=1` for Vertex AI, `CLAUDE_CODE_USE_BEDROCK=1` for Bedrock, otherwise direct API using `ANTHROPIC_API_KEY`.
- API calls use streaming. Retry logic retries on rate limit errors, 5xx server errors, and connection errors with exponential backoff (delays: 2s, 30s, 60s; 4 total attempts).
- Prompt caching: when the system prompt text exceeds a model-specific token threshold (2048 for sonnet, 4096 for opus/haiku, estimated via chars/4), inject `cache_control` headers. Fall back to non-cached request if the API rejects cache_control.

### agentspec AI Agent Pipeline

- **Assessment**: sends PRD text with assessment system prompt and `submit_assessment` tool. Forces tool use. Returns structured Assessment (quality enum: ready/needs_refinement/incomplete, summary, gaps, questions).
- **Refinement**: sends PRD + user answers + previous assessment with `submit_prd_update` and `submit_assessment` tools. Falls back to a second API call if the assessment tool call is missing from the response.
- **Generation**: generates three artifacts sequentially (requirements, test_spec, tasks). Each artifact uses a per-artifact tool whose schema embeds the afspec JSON schema with `$ref` resolved inline. Uses temperature=0.2. Each result is validated via afspec model construction. A repair loop (up to 2 attempts) sends validation errors back to the LLM for correction.
- Stop reasons `refusal`, `context_window_exceeded`, and `pause_turn` are checked before tool extraction and produce appropriate `AgentError` instances.
- Anthropic SDK errors are classified into categories (rate_limit, auth, transient, overloaded, input, internal, refusal, context_window, pause_turn) with retryable flag and HTTP status.

### agentspec Tool Definitions

- Three tool categories for structured output via Anthropic tool use:
  - `submit_assessment`: quality enum, summary, gaps array, questions array.
  - `submit_prd_update`: updated_prd string.
  - `submit_{artifact_name}`: content property embedding the afspec JSON Schema for that artifact type.
- Tool schemas must resolve `$ref`/`$defs` inline (Anthropic API does not support JSON Schema references). Metadata noise (title labels, defaults) must be stripped while preserving description fields.
- The afspec JSON Schema files (already embedded via `//go:embed`) serve as the source for tool schemas, rather than generating schemas from Go struct types.

### agentspec Prompt System

- 10 markdown prompt templates are embedded in the binary at compile time.
- Templates use `$variable` substitution syntax. Unmatched variables pass through unchanged.
- Template loading follows a two-tier fallback: check `<project_dir>/.spec/prompts/<name>.md` first, then fall back to bundled defaults.
- Prompt names are validated against `[a-zA-Z0-9_-]+` to prevent path traversal. Symlinked prompt files are skipped.
- YAML frontmatter in template files is stripped before use.
- Language detection scans the project directory for manifest files (go.mod, Cargo.toml, package.json, pyproject.toml, etc.) to determine project language and tooling hints for prompts.
- Spec landscape formatting produces tables of active and archived specs with columns for spec name, title, status, and intent summary.

### spec CLI: Root Command

- The CLI is invoked as `spec` with subcommands.
- Global flags: `--spec-dir/-d` (default `.specs`, overridable via `SPEC_DIR` env var), `--source/-s` (default `.`, must exist), `--quiet/-q` (suppress progress output), `--version`, `--help`.
- On startup, displays an ASCII banner on stderr with the version and current working directory, unless `--quiet` is set or the subcommand produces JSON output.
- Agent mode: when `AF_AGENT=1` is set, suppress the banner, force quiet mode, and wrap unhandled errors as JSON envelopes (`{"ok": false, "error": "..."}`) on stdout.

### spec CLI: `spec new`

- Creates a new spec from a PRD file. Positional argument: path to the PRD file (must exist). Optional `--name` flag for the spec name (must match `[a-z][a-z0-9_]*`); if omitted, derived from the filename.
- Auto-initializes the spec directory and a default `campaign.yaml` if they do not exist.
- Outputs JSON with spec directory name and session state.

### spec CLI: `spec list`

- Lists all specs with their session states. Always exits 0.
- Output: JSON with `spec_dir` and `specs` array. Each spec has `name` (directory name) and `state` (from `_session.json` or `no_session`).

### spec CLI: `spec refine`

- Assesses PRD quality and iteratively refines it. Positional argument: spec name or numeric prefix.
- Without `--answers`: runs initial assessment or returns pending questions.
- With `--answers` (JSON file path or `-` for stdin): submits answers and returns new assessment.
- `--force` flag: resets session to INIT, discards all assessments, answers, and artifacts.
- Shows a progress spinner on stderr during AI operations.

### spec CLI: `spec generate`

- Generates JSON artifacts from an accepted PRD. Positional argument: spec name or numeric prefix.
- Auto-accepts PRD if session state is ASSESSING or REFINING.
- `--force` flag: deletes existing artifacts and regenerates.
- Outputs JSON with list of generated artifacts.

### spec CLI: `spec render`

- Renders spec artifacts as markdown. Positional argument: spec name or numeric prefix.
- `--combined` flag: single concatenated document. Default: per-artifact with `---` separators.
- `--json` flag: JSON envelope output. Auto-enabled in agent mode.
- Falls back to rendering available artifacts individually when some are missing.

### spec CLI: `spec validate`

- Runs validation checks. Optional positional argument: spec name (single-spec mode); omit for multi-spec discovery.
- `--cross` flag: run cross-spec interface consistency checks.
- `--short` flag: condensed output with only counts, no error/warning arrays.
- Exit code 1 if any validation errors.

### spec CLI: `spec lint`

- Discovers and validates all specs. `--all` flag includes fully-implemented specs.
- Reports findings as JSON array with spec, file, rule, severity, message fields.
- Exit code 1 if any findings have severity `error`.

### spec CLI: `spec status`

- Queries session state for a spec (read-only). Positional argument: spec name or numeric prefix.
- Output: JSON with state, has_assessment, generated_artifacts, optionally last_error and quality.

### spec CLI: `spec campaign`

- Creates a new campaign directory. `--path/-p` (required), `--name/-n` (required), `--description` (optional).
- Errors when the path already contains `campaign.yaml`.

### spec CLI: JSON Output Contract

- All commands except `render` (without `--json`) produce JSON on stdout.
- Success responses are wrapped in `{"ok": true, ...}`.
- Error responses in agent mode are wrapped in `{"ok": false, "error": "..."}`.
- Progress, spinners, and banners always go to stderr, never stdout.

## Technical Boundaries

- Go 1.26.5 (per current go.mod). The binary must compile as a static binary with CGO_ENABLED=0.
- The Anthropic Go SDK (`github.com/anthropics/anthropic-sdk-go`) is the only supported LLM SDK. It must support streaming, tool use, and the three deployment targets (direct API, Vertex AI, Bedrock).
- cobra is the CLI framework (already in go.mod as an indirect dependency).
- Prompt templates are embedded using Go's `//go:embed` directive.
- TOML parsing uses a Go TOML library (e.g., `github.com/BurntSushi/toml`).
- JSON output uses Go's `encoding/json` with `json.MarshalIndent` for pretty-printing.
- Terminal UI (banner, spinner) should use lightweight libraries or stdlib — no heavy TUI frameworks required.

## Dependencies

- **afspec (Go, in-repo)**: the Go format library, now at `afspec/` in the agent-fox repository. Provides spec loading, saving, validation, rendering, lifecycle, discovery. This PRD includes completing its gaps.
- **Anthropic Go SDK**: provides API client with streaming, tool use, retry. Must support Vertex AI and Bedrock auth via environment variables.
- **cobra**: CLI framework for subcommands, flags, and help generation.
- **go-yaml (goccy)**: already in go.mod for YAML parsing.
- **jsonschema (santhosh-tekuri)**: already in go.mod for JSON Schema validation.
- **TOML parser**: for config file reading.

## Design Decisions

1. **Single Go module, multiple packages**: The Go implementation stays in the existing `github.com/agent-fox-dev/spec` module with `agentspec/` and `cmd/spec/` as sub-packages. This simplifies dependency management within the monorepo.
2. **Tool schemas from embedded JSON Schema files**: Rather than generating JSON schemas from Go struct types at runtime, the Go implementation bundles the canonical JSON Schema files and resolves `$ref` at initialization time. This guarantees schema parity with Python.
3. **Synchronous AI calls with context.Context**: AI calls are synchronous from the caller's perspective, with `context.Context` for cancellation and timeout. Streaming happens internally within the client.
4. **$variable substitution via strings.Replacer**: Prompt templates use simple `$variable` replacement rather than Go's `text/template`, matching the Python `string.Template.safe_substitute` behavior.
5. **Functional options for optional parameters**: Following the existing `WithMaxTokens` pattern in afspec, use Go functional options (e.g., `WithModel`, `WithCachePolicy`) for API functions with optional configuration.
6. **No session interoperability**: Go sessions use their own `_session.json` format. Users migrating from Python re-initialize sessions with the Go CLI.
7. **Structural validation parity**: Error messages may differ in wording but must convey the same rule name, affected entity, and category. JSON output shapes must be structurally equivalent for tool consumers.
8. **Python deprecation on completion**: The Python `spec` CLI and `agentspec` package will be deprecated once the Go version achieves feature parity and passes validation in production use.
