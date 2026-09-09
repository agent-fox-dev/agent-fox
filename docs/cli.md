# spec CLI Reference

The `spec` command is the CLI entry point for AI-powered spec creation. It manages the full lifecycle of creating, refining, generating, validating, and rendering specs. The binary is called `spec` and is installed via `install.sh` or built from `cmd/spec`.

## Global Options

| Option | Description |
|--------|-------------|
| `-d, --spec-dir PATH` | Spec directory (default: `.specs`). Can also be set via `SPEC_DIR` env var; CLI flag takes precedence. |
| `-s, --source PATH` | Source code directory for AI context during spec creation. Default: `.`. The path must exist on the filesystem. |
| `-q, --quiet` | Suppress progress output |
| `--version` | Show the version and exit |
| `--help` | Show help and exit |

```
spec [OPTIONS] [COMMAND] [ARGS]...
```

## Commands

### new

Create a new spec from a PRD file. Auto-initializes the spec root directory and a default `campaign.yaml` if they do not already exist.

```
spec new [OPTIONS] PRD_FILE
```

| Argument / Option | Description |
|-------------------|-------------|
| `PRD_FILE` | Path to an existing PRD file (required positional argument). The file must exist and must not be a directory. |
| `--name TEXT` | Snake-case spec name. When omitted, derived automatically from the PRD filename (CamelCase is converted to snake_case). Must match `[a-z][a-z0-9_]*`. |

**Example:**

```bash
spec new docs/my-feature.md
spec new docs/my-feature.md --name my_feature
```

### list

List all specs in the spec root with their session states. Outputs a JSON object containing `spec_dir` and a `specs` array. Each entry has the directory name and the session state (or `"no_session"` if `_session.json` is absent or malformed). Always exits with status 0.

```
spec list
```

No additional options.

**Example:**

```bash
spec list
spec --spec-dir /path/to/specs list
```

**Output format:**

```json
{
  "ok": true,
  "spec_dir": ".specs",
  "specs": [
    {"name": "01_my_feature", "state": "generated"},
    {"name": "02_auth_flow", "state": "assessing"}
  ]
}
```

### refine

Assess a PRD for quality and completeness, then iteratively refine it by answering questions.

Without `--answers`, runs the initial assessment and outputs pending questions as JSON. With `--answers`, submits answers, updates the PRD, and outputs the new assessment as JSON. Loop until the assessment quality reaches "ready", then run `generate`.

```
spec refine [OPTIONS] SPEC
```

| Option | Description |
|--------|-------------|
| `--answers TEXT` | Path to answers JSON file, or `-` to read from stdin. If the JSON has an `"answers"` key containing a map, the inner map is unwrapped automatically. |
| `--force` | Reset session to initial state, discarding all assessments, answers, and generated artifacts |

**Example:**

```bash
# Run initial assessment
spec refine 01_auth_redesign

# Submit answers from a file
spec refine --answers answers.json 01_auth_redesign

# Submit answers from stdin
echo '{"q1": "Yes, OAuth2 only"}' | spec refine --answers - 01_auth_redesign

# Force a fresh assessment
spec refine --force 01_auth_redesign
```

### generate

Generate JSON artifacts (`requirements.json`, `test_spec.json`, `tasks.json`) from an accepted PRD.

If the session state is `assessing` or `refining`, the PRD is auto-accepted before generation proceeds.

```
spec generate [OPTIONS] SPEC
```

| Option | Description |
|--------|-------------|
| `--force` | Delete existing artifacts and regenerate from scratch |

**Example:**

```bash
spec generate 01_auth_redesign
spec generate --force 01_auth_redesign
```

### validate

Run schema and cross-file checks on specs.

When `SPEC` is given, validates that single spec. When omitted, discovers and validates all specs in the spec directory. Validation checks required file existence (`prd.md`, `requirements.json`, `test_spec.json`, `tasks.json`), JSON well-formedness, then the four JSON Schemas and the cross-file rules C1 to C11.

```
spec validate [OPTIONS] [SPEC]
```

| Option | Description |
|--------|-------------|
| `--cross` | Run the cross-spec checks: declared dependencies exist, the dependency graph is acyclic, each dependency edge is reflected by a shared execution-path actor, and glossary terms two specs both define agree (a warning) |
| `--trace` | Print the derived traceability matrix for one spec instead of validating it |
| `--short` | Condensed output: emit only `valid`, `error_count`, and `warning_count` -- no `errors` array |

Warnings never fail the run: scope limits, vague language, glossary hints and cross-spec glossary conflicts are reported with `severity: "warning"` and leave the exit code at 0.

A spec created by `spec new` but not yet generated is *incomplete*, not invalid in the schema sense: validation reports one `completeness` error saying to run `spec generate`, and exits 1.

`--trace` reports, for every criterion and execution path, the tests that verify it and the tasks that own those tests, together with counts of how many are `uncovered` and `unowned`. The matrix is computed from `test.verifies` and `task.tests`; nothing of the kind is stored on disk, so it cannot drift from the artifacts.

**Example:**

```bash
# Validate all specs
spec validate

# Validate a single spec
spec validate 01_auth_redesign

# Include cross-spec checks
spec validate --cross

# Show what verifies and owns each criterion
spec validate --trace 01_auth_redesign

# Condensed output
spec validate --short 01_auth_redesign
```

### lint

Lint specs for validation errors and quality issues.

Discovers all specs in the spec directory and runs structural validation on each. By default, skips fully-implemented specs (every task in `done` or `dropped` state). Also checks for empty JSON artifacts and missing `_session.json`.

```
spec lint [OPTIONS]
```

| Option | Description |
|--------|-------------|
| `--all` | Include fully-implemented specs in the lint run |

**Example:**

```bash
spec lint
spec lint --all
```

### render

Render a spec as Markdown, or as a JSON envelope carrying that Markdown.

```
spec render [OPTIONS] SPEC
```

| Option | Description |
|--------|-------------|
| `--combined` | One document: PRD body, architecture if present, requirements, tests, tasks |
| `--task N` | Scope the render to task N — the render a coder receives |
| `--max-tokens N` | Cap the estimated size, dropping architecture first and then slimming the tests |
| `--json` | Output as a JSON envelope (auto-enabled when `AF_AGENT=1`) |

Every criterion renders as its EARS sentence followed by its contract, and every test renders all of its fields whatever its kind.

The scoped render (`--task N`) shows the requirements owning that task's criteria in full and every other requirement as one line, the task's own tests in full, the execution paths those tests verify, and the task in full with every other task as one line.

A spec too incomplete for the library to load still renders: the artifact files that exist are printed as they are.

**Example:**

```bash
spec render 01_auth_redesign
spec render --combined 01_auth_redesign
spec render --task 3 01_auth_redesign
spec render --json 01_auth_redesign
```

### migrate

Convert a format version 1.3 spec package to version 2, in place.

```
spec migrate [OPTIONS] SPEC
```

| Option | Description |
|--------|-------------|
| `--dry-run` | Report what the conversion would do without writing anything |

Everything in the version 1 to 2 mapping is mechanical except task ownership of tests: version 1 had no rule that a task owns a test, and in practice no version 1 spec owned its edge-case or property tests. The converter attaches each orphaned test to the task owning its parent requirement and names those tests in `attached_tests`, so the result is a starting point for review rather than a finished plan.

A spec that already declares `schema_version: 2` is refused.

**Example:**

```bash
spec migrate --dry-run 01_auth_redesign
spec migrate 01_auth_redesign
```

### status

Query session state for a spec (read-only). Reports the current lifecycle state, whether an assessment exists, and which artifacts have been generated.

```
spec status SPEC
```

No additional options.

**Output fields:** `ok`, `state`, `has_assessment`, `generated_artifacts`. Optional fields `last_error` and `quality` are included when present in the session.

**Example:**

```bash
spec status 01_auth_redesign
```

### activate

Transition a draft spec to the active state. Activation computes and stores the `intent_hash` from the `## Intent` section of the PRD, and captures immutable fields (`spec_id`, `spec_name`, `created_at`).

```
spec activate SPEC
```

No additional options.

**Output format:**

```json
{
  "ok": true,
  "spec": "my_feature",
  "status": "active"
}
```

**Example:**

```bash
spec activate 01_auth_redesign
spec --spec-dir /path/to/specs activate 01
```

**Errors:** Returns exit code 1 if the transition is invalid (e.g., the spec is already active, sealed, archived, or superseded). Returns exit code 1 with an `IntentError` if the PRD body does not contain a `## Intent` section — the spec remains in `draft` state. In agent mode (`AF_AGENT=1`), errors are emitted as `{"ok": false, "error": "..."}` to stdout.

### seal

Transition an active spec to the sealed state. Sealing marks a spec as finalized — no further edits are permitted.

```
spec seal SPEC
```

No additional options.

**Output format:**

```json
{
  "ok": true,
  "spec": "my_feature",
  "status": "sealed"
}
```

**Example:**

```bash
spec seal 01_auth_redesign
spec --spec-dir /path/to/specs seal 01
```

**Errors:** Returns exit code 1 if the transition is invalid (e.g., sealing a draft spec). In agent mode (`AF_AGENT=1`), errors are emitted as `{"ok": false, "error": "..."}`.

### archive

Move a spec directory to the `archive/` subdirectory inside the spec root. The original directory is removed and replaced by `<spec-dir>/archive/<name>/`. Creates the archive directory if it does not exist.

```
spec archive SPEC
```

No additional options.

**Output format:**

```json
{
  "ok": true,
  "archived": "01_auth_redesign"
}
```

**Example:**

```bash
spec archive 01_auth_redesign
spec --spec-dir /path/to/specs archive 01
```

**Errors:** Returns exit code 1 if the spec cannot be found or if an archive conflict exists (a directory with the same name already exists in `archive/`).

### supersede

Transition a sealed spec to the superseded state, prepending a deprecation banner to the PRD body that references the superseding spec.

```
spec supersede [OPTIONS] SPEC
```

| Option | Description |
|--------|-------------|
| `--by TEXT` | ID of the superseding spec (required) |

**Output format:**

```json
{
  "ok": true,
  "spec": "my_feature",
  "status": "superseded",
  "superseded_by": "02_auth_v2"
}
```

**Example:**

```bash
spec supersede 01_auth_redesign --by 02_auth_v2
spec --spec-dir /path/to/specs supersede 01 --by 02_auth_v2
```

**Errors:** Returns exit code 1 if `--by` is omitted, if the spec does not exist, or if the spec is not in the sealed state (only sealed specs can be superseded).

### campaign

Create a new campaign directory at the specified path, independent of the global `--spec-dir` option. Fails if `campaign.yaml` already exists at the target path.

```
spec campaign [OPTIONS]
```

| Option | Description |
|--------|-------------|
| `-p, --path PATH` | Campaign directory path (required) |
| `-n, --name TEXT` | Campaign name (required) |
| `--description TEXT` | Campaign description |

**Example:**

```bash
spec campaign --path campaigns/q3-auth --name "Q3 Auth Overhaul" --description "Auth system redesign"
spec campaign -p campaigns/q3-auth -n "Q3 Auth Overhaul"
```

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success (or lint/validate found no errors) |
| `1` | Error or findings exist (e.g., lint/validate found problems) |
| `2` | Usage error (invalid arguments or missing required options) |

## Output Format

All commands emit JSON to stdout. Successful operations include `"ok": true` in the output. In agent mode (`AF_AGENT=1`), errors are wrapped as `{"ok": false, "error": "..."}` on stdout. Outside agent mode, errors are printed to stderr.

The `render` command outputs raw markdown by default; use `--json` for JSON output.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | **Required** for AI-powered commands (`refine`, `generate`). Anthropic API key used to call Claude for PRD assessment, refinement, and artifact generation. |
| `AF_SPEC_MODEL` | Override the default model tier or specify a model ID directly. Accepts tier names (`SIMPLE`, `STANDARD`, `ADVANCED`) or model IDs (e.g. `claude-sonnet-4-6`). Default: `STANDARD`. Also configurable via `config.toml` (see below). |
| `AF_AGENT` | Set to `1` to enable agent mode. Suppresses the banner, forces quiet output, and auto-enables `--json` for `render`. |
| `SPEC_DIR` | Override the default spec root directory (`.specs`). The `--spec-dir` CLI flag takes precedence over this env var. |
| `CLAUDE_CODE_USE_VERTEX` | Set to `1` to use Google Vertex AI as the Claude provider. |
| `CLAUDE_CODE_USE_BEDROCK` | Set to `1` to use AWS Bedrock as the Claude provider. |

### Configuration File

The `spec` CLI loads configuration from `config.toml`, searching in order:

1. `.specs/config.toml` (project-local)
2. `~/.specs/config.toml` (user-global)

The first file found is used. Symlinked config files are rejected for security.

```toml
[model]
model = "STANDARD"     # tier name or model ID

[provider]
auth_method = ""       # optional provider auth method
vertex_project = ""    # Google Vertex project ID
vertex_region = ""     # Google Vertex region
```

The `AF_SPEC_MODEL` environment variable overrides the `[model].model` value.

## Agent and Skill Workflow

The `spec` CLI is designed to be driven by AI agents. Two Claude Code skills orchestrate the spec creation workflow:

### PRD Authoring: the af-prd Skill

The `af-prd` skill (`/af-prd`) guides the user through creating a well-structured Product Requirements Document via an iterative interview. It operates as a Product Manager collaborator:

1. **Assess input maturity** -- classifies the starting material as Seed (vague idea), Sketch (rough bullets), or Draft (mostly complete).
2. **Analyze the codebase** -- reads project structure, README, existing specs, and steering directives to understand context.
3. **Draft an initial PRD** -- produces a first pass following the standard PRD structure (Intent, Goals, Non-goals, Functional Requirements, etc.), marking gaps with `[GAP]` placeholders.
4. **Interview loop** -- asks 3-5 questions per round across categories (intent, user stories, core behaviors, edge cases, error handling, technical boundaries). Maximum 5 rounds.
5. **Save the PRD** -- writes the finalized markdown file, ready to be passed to `spec new` or `/af-spec`.

The output is a raw markdown file (no YAML frontmatter) suitable as input to `spec new <path>`.

### Full Spec Workflow: the af-spec Skill

The `af-spec` skill (`/af-spec`) orchestrates the complete spec creation pipeline using the `spec` CLI:

1. **Understand the PRD** (Step 1) -- reads the PRD from a file path, GitHub issue URL, or user prompt. Identifies ambiguities, inconsistencies, and gaps, then resolves them with the user. Verifies external API assumptions against installed libraries.
2. **Learn the context** (Step 2) -- analyzes the codebase, existing specs, and cross-spec dependencies.
3. **Create the spec** (Step 3) -- runs `spec new <prd_path> --name <name>` to create the spec directory with a numbered prefix and initial `_session.json`.
4. **Refine the PRD** (Step 4) -- runs `spec refine <spec>` to get AI assessment, then iterates with `spec refine --answers <file> <spec>` until quality reaches "ready" (max 5 iterations).
5. **Generate artifacts** (Step 5) -- runs `spec generate <spec>` to produce `requirements.json`, `test_spec.json`, and `tasks.json`. Performs a post-generation language audit to ensure artifacts match the project's tooling.
6. **Architecture document** (Step 6) -- optionally creates `architecture.md` for complex designs.
7. **Validate and finish** (Step 7) -- runs `spec validate <spec>` (and `--cross` for multi-spec projects), fixes any issues, and reviews generated artifacts for quality.

### Session State Machine

Each spec maintains a `_session.json` file that tracks its lifecycle state. The state machine transitions are:

```
init --> assessing --> refining --> prd_accepted --> generating --> generated
```

| State | Description |
|-------|-------------|
| `init` | Spec created, PRD copied, no assessment yet |
| `assessing` | AI assessment of PRD quality in progress |
| `refining` | PRD is being refined through Q&A exchanges |
| `prd_accepted` | PRD quality accepted, ready for artifact generation |
| `generating` | Artifact generation in progress |
| `generated` | All artifacts generated and written to disk |

The session persists assessment history, QA exchanges, generated artifact names, and any last error. State transitions are written atomically using temp-file-and-rename.

Use `spec status <spec>` to query the current state at any time.

### AI Pipeline: SpecAgent

The `SpecAgent` (in `agentspec/agent.go`) implements the three AI-powered pipeline stages that the session orchestrates:

**Model tiers.** The agent uses a tiered model system to select the appropriate Claude model:

| Tier | Default Model | Use Case |
|------|--------------|----------|
| `SIMPLE` | `claude-haiku-4-5` | Lightweight, cost-effective tasks |
| `STANDARD` | `claude-sonnet-4-6` | Balanced capability (default) |
| `ADVANCED` | `claude-opus-4-6` | Most capable, complex reasoning |

The tier is configured via `AF_SPEC_MODEL`, `config.toml`, or defaults to `STANDARD`.

**Stage 1: AssessPRD.** Sends the PRD text to Claude with a structured assessment prompt and a `submit_assessment` tool. The model evaluates PRD quality and returns an assessment containing a quality rating, summary, identified gaps, and clarifying questions. The spec landscape (sibling specs) is included as context to avoid duplication.

**Stage 2: RefinePRD.** Sends the PRD text along with user answers and the previous assessment to Claude with a `submit_prd_update` tool. The model rewrites the PRD incorporating the answers, then produces a new assessment. If the assessment is not included in the initial response, a fallback call retrieves it separately.

**Stage 3: GenerateArtifacts.** Sequentially generates three artifacts in order, each building on the prior ones:

1. `requirements.json` -- EARS criteria and end-to-end execution paths
2. `test_spec.json` -- one flat list of tests, each verifying named criteria or a path
3. `tasks.json` -- one flat list of tasks, each owning named criteria and tests

The order is mandatory and the steps are sequential: the tests step receives the complete requirements artifact, and the tasks step receives that plus a table of every test with its id, kind and verifies list. A generator that cannot see a test ID cannot own it, which is how the previous format ended up with specs whose edge-case and property tests belonged to no task at all.

Each artifact is generated via a dedicated `submit_{artifact}` tool call. After each step the pipeline runs the artifact's JSON Schema plus every cross-file rule decidable so far, and sends any violation back to Claude as a `tool_result` naming the rule that failed — up to three repair attempts per artifact. Temperature is 0.2.

A generation that ends with an invalid spec is a failed generation: `spec generate` exits non-zero, removes the artifacts that run wrote, and reports the violations as errors. Artifacts that already existed on disk are left alone, so a re-run still resumes from the point of failure.
