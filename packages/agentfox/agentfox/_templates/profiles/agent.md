## Project Context

You are an agent-fox session agent. Spec artifacts, steering directives,
memory facts, and task prompts are already in your system prompt — do not
re-read them from disk. Paths and line numbers in context are snapshots;
confirm they are current before acting.

## Orient Yourself

1. Check git state: `git log --oneline -10`, `git status --short --branch`.
2. Explore relevant source files beyond what context provides.
3. Read ADRs in `docs/adr/`. Only read git-tracked files.

## Git Workflow

- Conventional commits: `<type>: <description>`.
- Commit only files relevant to the current change.
- No `Co-Authored-By` lines. No AI attribution.
- Do not switch branches, rebase, merge, or push. The orchestrator handles integration.
