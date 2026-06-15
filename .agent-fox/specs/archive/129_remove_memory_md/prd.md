# PRD: Remove docs/memory.md

## Problem Statement

`docs/memory.md` is a legacy manual knowledge-sharing file that agents are
instructed to read at session start and update before committing. In practice
agents frequently forget to update it, causing it to go stale. The knowledge
store (`session_summaries` + `FoxKnowledgeProvider`) has fully superseded this
mechanism for `code` sessions — the agent profile (`profiles/agent.md`) already
says "DO NOT READ `docs/memory.md`" because relevant knowledge is retrieved
automatically. The file adds no value and creates a false obligation in
CLAUDE.md/AGENTS.md.

## Goals

Completely remove `docs/memory.md` from the codebase: delete the file, remove
all references to it in templates, instructions, code, and tests.

## Affected Locations

1. **`docs/memory.md`** — the file itself (git rm)
2. **`agent_fox/workspace/init_project.py`** — `_ensure_seed_files()` creates
   it; `_DOCS_MEMORY_CONTENT` constant
3. **`agent_fox/_templates/agents_md.md`** — CLAUDE.md/AGENTS.md template
   references it in "Understand Before You Code" (step 2) and "Session
   Completion" (commit instruction)
4. **`CLAUDE.md`** — generated from template; references it in steps 2 and
   session completion
5. **`AGENTS.md`** — same as CLAUDE.md
6. **`agent_fox/_templates/skills/af-fix`** — tells fix agent to read it
7. **`.claude/skills/af-fix/SKILL.md`** — installed copy of above
8. **`agent_fox/_templates/profiles/agent.md`** — "DO NOT READ docs/memory.md"
   line (remove since file no longer exists)
9. **`tests/integration/test_init.py`** — two tests:
   `test_init_creates_docs_memory_md` and
   `test_reinit_preserves_existing_seed_files`

## Design Decisions

1. **CLAUDE.md and AGENTS.md updated directly.** Both the template
   (`_templates/agents_md.md`) and the project's current `CLAUDE.md` /
   `AGENTS.md` files are updated. Future `agent-fox init` runs will not
   re-introduce the references.

2. **`_ensure_seed_files()` simplified.** If `docs/memory.md` was the only
   seed file, the function becomes a no-op and can be removed entirely.
   If it creates other files too, only the memory.md logic is removed.

3. **Audit docs left as-is.** `docs/audits/audit2.md` mentions `memory.md`
   in historical context (describing past findings). These are historical
   records and should not be modified.

## Source

Source: Input provided by user via interactive prompt.
