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
| `--read-source` | Let the model read the source tree at `--source` while it works. Read-only: the mutating tools are excluded from the resolved set and the run refuses to start if one survives |
| `--trust-project` | Admit skills and context files authored in the source tree — `AGENTS.md`, `CLAUDE.md`, `.specs/steering.md` — into the system prompt |
| `--max-turns N` | Maximum turns for the phase (0: the default, 40). This is also the repair budget |
| `--max-budget USD` | Maximum spend for the phase (0: the default, 5.00) |
| `-v, --verbose` | Report what the model reads and submits, on stderr. Replaces the spinner |

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
| `--read-source` | Let the model read the source tree at `--source` while it works (read-only) |
| `--trust-project` | Admit skills and context files authored in the source tree into the system prompt |
| `--max-turns N` | Maximum turns per artifact (0: the default, 40). This is also the repair budget |
| `--max-budget USD` | Maximum spend per artifact (0: the default, 5.00) |
| `-v, --verbose` | Report what the model reads and submits, on stderr |

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

### models

Show the model each phase will run on, what it costs, and whether this shell
can authenticate to it.

```
spec models [OPTIONS]
```

| Option | Description |
|--------|-------------|
| `--all` | Also list every tier of every vendor |

The model surface is a catalog spanning several vendors, each with its own
credential variables, so "which model will `spec generate` use and can I reach
it" is worth being able to ask before running one. The command reports rather
than fails: an unconfigured vendor is exactly what it exists to tell you about,
so it always exits 0.

**Output format:**

```json
{
  "ok": true,
  "vendor": "anthropic",
  "models": [
    {
      "phase": "assess",
      "tier": "STANDARD",
      "vendor": "anthropic",
      "model": "claude-sonnet-5",
      "thinking_level": "high",
      "context_window": 1000000,
      "max_tokens": 128000,
      "input_cost_per_mtok": 3,
      "output_cost_per_mtok": 15,
      "credential": "ok"
    }
  ]
}
```

`credential` is `ok` or `missing`. A gateway that authenticates by URL, or an
instance role, reports `ok` with no key in the environment — that is the
`ambient` state, and refusing it would reject every such deployment for a key
it was never going to have.

A model spec that names nothing the catalog knows is reported as
`"unresolved: …"` in the row rather than failing the command.

**Example:**

```bash
spec models
spec models --all
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
| `0` | Success, and lint/validate found no errors |
| `1` | Anything else: an error, a usage mistake, or findings exist |

There is no third code. `Execute` exits 1 on every error, so a caller cannot
distinguish a usage mistake from a failed run by status alone — read the
`error` field of the JSON envelope.

## Output Format

All commands emit JSON to stdout. Successful operations include `"ok": true` in the output. In agent mode (`AF_AGENT=1`), errors are wrapped as `{"ok": false, "error": "..."}` on stdout. Outside agent mode, errors are printed to stderr.

The `render` command outputs raw markdown by default; use `--json` for JSON output.

## Environment Variables

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Credential for the AI-powered commands (`refine`, `generate`) when the model resolves to an Anthropic row. Other vendors read their own variables — run `spec models` to see which one the configured model needs. |
| `<VENDOR>_BASE_URL` | Gateway, proxy or local server for that vendor (`ANTHROPIC_BASE_URL`, `OPENAI_BASE_URL`, …). A base URL alone is a valid credential state. |
| `AF_SPEC_MODEL` | Override the model for every phase. Accepts a tier name (`SIMPLE`, `STANDARD`, `ADVANCED`) or any catalog spec (`anthropic/claude-opus-5`, `openai/gpt-6-astra`). Default: `STANDARD`. |
| `AF_AGENT` | Set to `1` to enable agent mode. Suppresses the banner, forces quiet output, disables the `--verbose` event trace, and auto-enables `--json` for `render`. |
| `SPEC_DIR` | Override the default spec root directory (`.specs`). The `--spec-dir` CLI flag takes precedence over this env var. |
| `CLAUDE_CODE_USE_VERTEX` | **Refused.** There is no wire for Claude on Vertex; the error names the alternative. See [errata](errata/agentkit_model_resolution.md). |
| `CLAUDE_CODE_USE_BEDROCK` | **Refused.** There is no wire for Bedrock; the error names the alternative. |

### Configuration File

The `spec` CLI loads configuration from `config.toml`, searching in order:

1. `.specs/config.toml` (project-local)
2. `~/.specs/config.toml` (user-global)

The first file found is used. Symlinked config files are rejected for security.

```toml
[model]
model = "STANDARD"     # tier name or catalog model spec
vendor = "anthropic"   # which tier table SIMPLE/STANDARD/ADVANCED resolve against
model_variant = ""     # "extended" for the long-context row of a tier

[agent]
read_source = false    # let the model read the source tree
trust_project = false  # admit project skills and context files
max_turns = 40         # per phase; also the repair budget
max_budget_usd = 5.0   # per phase
max_attempts = 3       # retries of one model call

[provider]
auth_method = ""       # read, not used
vertex_project = ""    # read, not used
vertex_region = ""     # read, not used
```

The `AF_SPEC_MODEL` environment variable overrides the `[model].model` value.
Command-line flags win over the file for the fields they set; a flag that was
not passed leaves the file's value alone.

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

The `SpecAgent` (in `agentspec/agent.go`) implements the three AI-powered
stages the session orchestrates. It runs on
[AgentKit](https://github.com/agent-fox-dev/coder): each stage builds its own
agent — its own model, tool set, turn budget and cost cap — and drives it to a
result.

**Model tiers.** `SIMPLE`, `STANDARD` and `ADVANCED` are aliases over the
AgentKit catalog, per vendor, so a tier still means what a spec author chooses
between while any catalog model spec also works. See
[Configuration](configuration.md#model-selection) for the table and
[Model Usage](model-usage.md) for what each stage sends.

**Stage 1: AssessPRD.** Sends the PRD with the assessment system prompt and a
`submit_assessment` tool. The spec landscape (sibling specs) is included so the
model can see what already exists.

**Stage 2: RefinePRD.** Sends the PRD, the author's answers and the previous
assessment, with a `submit_prd_update` tool whose arguments carry the rewritten
PRD *and* a fresh assessment of it. Both used to have to appear in one response
with a hand-written check enforcing it; making them two fields of one call
means the schema says so.

**Stage 3: GenerateArtifacts.** Generates three artifacts in the order format
v2 §12.1 mandates, each step receiving the complete artifacts before it:

1. `requirements.json` — EARS criteria and end-to-end execution paths
2. `test_spec.json` — one flat list of tests, each verifying named criteria or a path
3. `tasks.json` — one flat list of tasks, each owning named criteria and tests

The order is the rule and the reason is concrete: a generator that cannot see a
test ID cannot own it, which is how the previous format ended up with specs
whose edge-case and property tests belonged to no task at all.

**Repair.** The artifact's schema and every cross-file rule decidable so far
run *inside* the submit tool's handler. A violation is returned as a tool error
naming the rule that failed, and the loop appends it to the transcript the
model is already holding — so the system prompt and the tool schemas stay
byte-identical across the repair and the provider's cache prefix survives it.
The turn budget (`--max-turns`, default 40) is the repair budget.

A generation that ends with an invalid spec is a failed generation: `spec
generate` exits non-zero, removes the artifacts that run wrote, and reports the
violations. Artifacts that already existed on disk are left alone, so a re-run
resumes from the point of failure.

**Reading the codebase.** With `--read-source` a stage additionally gets
AgentKit's non-mutating built-in tools rooted at `--source`, so a spec can be
written against the code it describes. The read-only mandate is the tool list
plus an invariant checked before the first request, not a sentence in a prompt.
