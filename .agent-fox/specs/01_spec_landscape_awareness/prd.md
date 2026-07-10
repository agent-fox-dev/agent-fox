---
spec_id: '01'
spec_name: spec_landscape_awareness
title: Spec Landscape Awareness
status: draft
created_at: '2026-07-10T07:52:10.008494+00:00'
updated_at: '2026-07-10T07:52:10.008494+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Spec Landscape Awareness

## Intent

Make the `spec` CLI's assessment and refinement pipeline aware of all existing specifications — both active and archived — so that new specs are authored with knowledge of the existing spec landscape, enabling overlap detection, dependency suggestion, and historical awareness.

## Background

The spec authoring pipeline (`spec assess`, `spec refine`, `spec generate`) has a gap in existing-spec awareness:

- **Assessment and refinement are blind.** The `assess()` and `refine()` methods in `SpecSession` evaluate a PRD in complete isolation. The LLM has no knowledge of sibling specs and cannot flag overlaps, suggest dependencies, or warn about re-implementing previously abandoned approaches.

- **Generation has partial awareness.** During `spec generate`, `load_dependent_interfaces()` in `afspec.discovery` loads upstream spec interfaces — but only for specs already declared as dependencies in `tasks.json`, which is itself a generated artifact. This creates a chicken-and-egg problem: the dependency information needed for context-aware generation doesn't exist until after generation.

- **Archives are invisible.** `discover_specs()` explicitly skips the `archive/` directory. The system has no memory of superseded specs, which can lead to re-specifying previously abandoned or replaced approaches without awareness of why they were superseded.

- **The af-spec skill compensates manually.** Step 2 of the skill instructs the agent to manually scan `.agent-fox/specs/` and identify cross-spec dependencies. This works but provides no programmatic support — the assessment and refinement LLM calls proceed without this context.

## Goals

1. **Landscape-aware assessment:** Inject a lightweight summary of all existing specs (active and archived) into the `spec assess` and `spec refine` LLM prompts so the model can flag overlaps, suggest dependencies, and note historical precedent.

2. **Overlap detection with teeth:** When the assessment LLM detects that a new PRD's scope overlaps with an existing spec, it generates specific clarification questions about the overlap AND flags it as a gap that blocks "ready" quality until the user resolves it.

3. **Efficient context injection:** Keep the landscape summary compact — title and intent for active specs, one-line entries for archived specs — so the context budget impact is minimal (~1000–3000 tokens for a typical project).

4. **Skill alignment:** Update the af-spec skill's Step 2 to reflect that landscape context is now automatically injected, reducing manual discovery overhead.

## Non-Goals

- **No new CLI subcommand.** The landscape context is injected silently into existing `spec assess` and `spec refine` operations. No `spec landscape` command is added.
- **Automatic dependency declaration.** The LLM suggests dependencies and flags overlaps, but the user decides whether to declare them in the PRD.
- **Changes to the dependency graph data model.** The `dependencies` array in `tasks.json` is unchanged.
- **Semantic similarity matching.** No embedding-based or full-text-search overlap detection — the LLM reasons about overlaps from titles and intents.
- **Changes to `spec generate`.** The existing `load_dependent_interfaces()` mechanism for generation is unchanged. This enhancement targets the earlier assessment/refinement stages only.

## Technical Specification

### Discovery Layer (`afspec/discovery.py`)

Add a new public function:

```python
def load_spec_landscape(
    spec_root: Path,
    *,
    include_archive: bool = True,
    current_spec_id: str | None = None,
) -> list[dict[str, Any]]:
```

This function:
1. Calls `discover_specs(spec_root)` for active specs.
2. If `include_archive` is true, scans `spec_root / "archive"` for spec directories using the same `_SPEC_DIR_RE` pattern matching.
3. For each **active** spec: returns `spec_id`, `spec_name`, `title`, `status`, `intent` (extracted from the `## Intent` section of `prd.md`), and `archived: false`.
4. For each **archived** spec: returns `spec_id`, `spec_name`, `title`, `status`, and `archived: true`. No intent section (lightweight).
5. Excludes `current_spec_id` from the results to avoid self-reference.
6. Returns `[]` on any failure (graceful degradation, matching the `load_dependent_interfaces()` pattern).

**Intent extraction heuristic:** Read `prd.md` body (after frontmatter), find the `## Intent` section, and return its content up to the next heading or end of section. If no `## Intent` section exists, return the first non-empty paragraph of the body. Truncate to 300 characters to keep context compact.

### Prompt Layer (`agentspec/prompts.py`)

Add a new helper function:

```python
def _format_spec_landscape(
    landscape: list[dict[str, Any]] | None,
) -> str:
```

Formats the landscape into a markdown section:

```markdown
## Existing Spec Landscape

The following specifications already exist in this project. Check for overlaps,
potential dependencies, and historical precedent before assessing this PRD.

### Active Specs
| Spec | Title | Status | Intent |
|------|-------|--------|--------|
| 01 | Core Foundation | implemented | Establish the base... |
| 02 | Backend Protocol | draft | Define the protocol... |

### Archived Specs
| Spec | Title | Status |
|------|-------|--------|
| 08 | Spec Generation Improvement | archived |
| 09 | Worktree Path Collision | archived |
```

Returns empty string if landscape is `None` or empty.

Update the following prompt functions to accept and format the landscape:

1. `assessment_user_prompt(prd_text, spec_name, *, spec_landscape=None, project_dir=None)` — add `spec_landscape` keyword parameter. Substitutes into the template via new `$spec_landscape_block` template variable.

2. `refinement_user_prompt(prd_text, answers, previous_assessment, *, spec_landscape=None, project_dir=None)` — add `spec_landscape` keyword parameter. Substitutes into the template via new `$spec_landscape_block` template variable.

### Template Updates

**`assessment_system.md`** — Append cross-spec awareness instructions:

```markdown
## Cross-spec awareness

When a `## Existing Spec Landscape` section is present in the user message,
you MUST check the new PRD against all listed specs:

1. **Overlap detection:** If the new PRD's scope overlaps with an existing
   active spec (similar intent, overlapping functional areas, or touching
   the same modules), flag it as a gap and generate a clarification question
   asking whether the new spec should depend on, extend, or supersede the
   existing one.

2. **Historical precedent:** If the new PRD's scope overlaps with an archived
   spec, note this in the summary and ask whether the user is aware of the
   prior work and what has changed.

3. **Dependency suggestion:** If the new PRD references capabilities that an
   existing spec already provides, suggest declaring a dependency.

An overlap with an active spec that is not acknowledged in the PRD is a gap
that prevents "ready" quality — the user must explicitly address it.
```

**`assessment_user.md`** — Add `$spec_landscape_block` before the PRD:

```
Please assess the following PRD for the spec named "$spec_name".

$spec_landscape_block
---

$prd_text

---

Provide your structured assessment using the submit_assessment tool.
```

**`refinement_user.md`** — Add `$spec_landscape_block` after the QA block:

```
...existing template content...

$spec_landscape_block

Incorporate the answers into the PRD and re-assess.
...
```

### Session Layer (`agentspec/session.py`)

**Update `assess()`** (lines 250–286):
1. After reading `prd_text` and parsing `spec_id`, call `load_spec_landscape(self._spec_dir.parent, current_spec_id=spec_id)`.
2. Pass the result to `agent.assess_prd()` as a new `spec_landscape` keyword argument.
3. Wrap the landscape call in try/except — if it fails, proceed with `spec_landscape=None`.

**Update `refine()`** (lines 288–346):
1. After reading `prd_text` and before calling `agent.refine_prd()`, call `load_spec_landscape(self._spec_dir.parent, current_spec_id=spec_id)`.
2. Pass the result to `agent.refine_prd()` as a new `spec_landscape` keyword argument.
3. Same graceful degradation on failure.

### Agent Layer (`agentspec/agent.py`)

**Update `assess_prd()` signature:**
```python
async def assess_prd(
    self,
    prd_text: str,
    spec_name: str,
    *,
    spec_landscape: list[dict[str, Any]] | None = None,
) -> Assessment:
```
Pass `spec_landscape` to `assessment_user_prompt()`.

**Update `refine_prd()` signature:**
```python
async def refine_prd(
    self,
    prd_text: str,
    answers: dict[str, str],
    previous_assessment: Assessment,
    *,
    spec_landscape: list[dict[str, Any]] | None = None,
) -> tuple[str, Assessment]:
```
Pass `spec_landscape` to `refinement_user_prompt()`.

### Skill Update (`.claude/skills/af-spec/SKILL.md`)

Update Step 2 ("Learn the Context") to note the automated landscape injection. Replace the paragraph about looking for existing specifications with:

> Look for existing specifications in `.agent-fox/specs/`. Specification folders
> use a **numbered prefix** indicating creation sequence.
>
> **Note:** The `spec refine` command automatically injects a summary of all
> existing specs (active and archived) into the assessment and refinement
> prompts. The LLM will flag overlaps and suggest dependencies during
> refinement. However, you should still review existing specs manually when
> detailed interface understanding is needed for the `## Dependencies` section.

## Verified External API

### `afspec` (Python)

| Symbol | Module / path | Signature | Notes |
|--------|---------------|-----------|-------|
| `discover_specs` | `afspec.discovery` | `(root: str \| Path) -> list[SpecMeta]` | Skips `archive/` |
| `load_dependent_interfaces` | `afspec.discovery` | `(spec_id: str, spec_root: Path) -> list[dict[str, Any]]` | Returns `[]` on failure |
| `_SPEC_DIR_RE` | `afspec.discovery` | `re.compile(r"^\d+_[a-z][a-z0-9_]*$")` | Pattern for spec dirs |
| `_load_frontmatter_only` | `afspec.discovery` | `(prd_path: Path) -> PRDFrontmatter` | Private; loads YAML only |
| `SpecMeta` | `afspec.models` | dataclass: `spec_id, spec_name, status, dir` | |
| `PRDFrontmatter` | `afspec.models` | Pydantic model: `spec_id, spec_name, status, title, ...` | |

### `agentspec` (Python)

| Symbol | Module / path | Signature | Notes |
|--------|---------------|-----------|-------|
| `SpecSession.assess` | `agentspec.session` | `async (self) -> Assessment` | State: init → assessing |
| `SpecSession.refine` | `agentspec.session` | `async (self, answers: dict[str, str]) -> Assessment` | State: assessing → refining |
| `SpecAgent.assess_prd` | `agentspec.agent` | `async (self, prd_text: str, spec_name: str) -> Assessment` | No landscape param yet |
| `SpecAgent.refine_prd` | `agentspec.agent` | `async (self, prd_text, answers, prev_assessment) -> tuple[str, Assessment]` | No landscape param yet |
| `assessment_user_prompt` | `agentspec.prompts` | `(prd_text, spec_name, *, project_dir=None) -> str` | Template: `$spec_name`, `$prd_text` |
| `refinement_user_prompt` | `agentspec.prompts` | `(prd_text, answers, prev_assessment, *, project_dir=None) -> str` | Template: `$prd_text`, `$assessment_block`, `$qa_block` |
| `_format_dependent_interfaces` | `agentspec.prompts` | `(dependent_interfaces: list[dict] \| None) -> str` | Analogous pattern for new function |
| `load_prompt_template` | `agentspec.prompt_loader` | `(name, *, project_dir=None, **kwargs) -> str` | `$variable` substitution |

## Design Decisions

1. **Prompt injection over CLI command**: The landscape context is injected automatically during `spec assess` and `spec refine`. No new CLI subcommand is needed because the consumers are the LLM prompts, not external tooling. The skill is updated to note the automated awareness.

2. **Archives as lightweight entries**: Archived specs appear as one-line table rows (spec_id, title, status) without intent sections. This keeps context compact while providing historical awareness. Active specs get richer summaries including their intent section (truncated to 300 chars).

3. **Overlap blocks "ready"**: When the assessment LLM detects overlap with an active spec, it flags it as a gap AND generates a clarification question. This prevents specs from reaching "ready" quality without addressing the overlap. The user resolves it by explaining the relationship (dependency, extension, supersession, or "intentionally separate").

4. **Graceful degradation**: All new discovery calls are wrapped in try/except with fallback to `None` or empty results. If landscape loading fails, assessment and refinement proceed exactly as they do today — no regression risk.

5. **Intent extraction heuristic**: For active specs, the intent is extracted from the `## Intent` section of `prd.md`. If no such section exists, the first non-empty paragraph of the body is used, truncated to 300 characters. This handles both structured PRDs (with Intent sections) and simpler ones.

6. **No changes to `spec generate`**: The existing `load_dependent_interfaces()` mechanism for generation-time dependency context is unchanged. This enhancement targets the earlier assessment/refinement stages where dependency awareness has the most impact on PRD quality.

