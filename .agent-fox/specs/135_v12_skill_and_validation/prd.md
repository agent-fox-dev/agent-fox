# v1.2 Skill Template and Validation Migration

## Overview

Migrate the two user-facing surfaces of spec authoring to the v1.2 format:

1. **lint-specs CLI** -- switch from the custom markdown-based validators to
   `afspec.validate()` for v1.2 spec folders while preserving the existing
   validators for any remaining v1 markdown specs.
2. **af-spec skill template** -- update the Claude Code skill instructions so
   that the agent produces v1.2 format specs (JSON artifacts with YAML
   frontmatter PRD) instead of the old markdown format.

Together these changes close the loop on the v4 spec-format migration: specs
are authored in v1.2, and the linter validates them using the canonical
`afspec` schema and cross-file checks.

## Goals

1. Update `agent_fox/spec/lint.py` and `agent_fox/cli/lint_specs.py` to detect
   spec format and route v1.2 specs to `afspec.validate()` instead of the
   custom validators.
2. Keep the existing custom validators intact for v1 markdown specs -- the
   linter auto-detects format and routes accordingly.
3. Map `afspec.ValidationError` results to the existing `Finding` dataclass so
   the CLI output format is unchanged.
4. Update the af-spec skill template to instruct the agent to produce v1.2
   artifacts: `prd.md` (YAML frontmatter), `requirements.json`, `test_spec.json`,
   `tasks.json`, and optionally `architecture.md`.
5. Update ID format references in the skill template to v1.2 conventions
   (`{spec_id}-REQ-{N}`, `{spec_id}-PROP-{N}`).
6. Include a validation step in the skill template that runs `agent-fox
   lint-specs` on the generated spec.

## Non-Goals

- Deleting or deprecating the v1 markdown validators (they remain for legacy
  specs).
- Changing spec discovery or the graph builder (covered by specs 132-134).
- Migrating existing archived specs to v1.2 format.
- Changing the `--ai` flag behavior or AI validation pipeline.

## Design Decisions

1. **Format-based routing in lint.py:** The backing module (`lint.py`) detects
   each spec's format via `SpecInfo.format` (added by spec 132) and routes to
   either the custom `validate_specs()` or `afspec.validate()`. This is a
   branch at the per-spec level, not a global switch.

2. **Finding mapping:** `afspec.validate()` returns `list[ValidationError]`.
   Each `ValidationError` is mapped to a `Finding` with the same severity
   levels (error/warning/hint) and the same fields (spec_name, file, rule,
   message, line). No new output format is introduced.

3. **Skill template is a single file replacement:** The af-spec skill is one
   large markdown file. The update replaces artifact names, ID formats, EARS
   pattern format, and tasks format in-place. No new files are created.

4. **Backward compatibility:** The CLI interface (`agent-fox lint-specs`) is
   unchanged -- same flags, same output format. Users do not need to change
   their workflow.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 132_afspec_integration | 2 | 1 | Uses afspec dependency for validation and format reference; group 2 adds the dependency and SpecFormat enum |

## Source

Source: Input provided by user via interactive prompt.
