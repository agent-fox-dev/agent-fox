# Agent Profiles

Profiles are markdown files that define the behavioral guidance for each agent
archetype — identity, rules, focus areas, constraints, and output format.
While [archetypes](architecture/03-execution-and-archetypes.md#agent-archetypes)
control the operational configuration of an agent (model tier, tool allowlist,
max turns, thinking mode), profiles control *what the agent does and how it
thinks*.

Every agent session receives a profile as part of its system prompt. The
profile tells the agent who it is, what rules to follow, and what output to
produce. Profiles are the primary mechanism for shaping agent behavior without
changing code.

## Built-in Profiles

agent-fox ships with profiles for all five archetypes and their modes:

| Profile file | Archetype | Mode | Purpose |
|---|---|---|---|
| `agent.md` | (all) | — | Base profile shared by every agent: pre-injected context acknowledgment, orientation guidance, external reference verification, git workflow, scope discipline, documentation conventions |
| `coder.md` | coder | — | Implementation agent: Quick Triage, task group routing, input triage, session summary |
| `coder_fix.md` | coder | fix | Fix-mode variant for the night-shift fix pipeline |
| `reviewer.md` | reviewer | — | Base reviewer (also used by pre-flight mode, which combines spec quality review and drift detection) |
| `reviewer_audit-review.md` | reviewer | audit-review | Test quality validation against test spec contracts |
| `reviewer_fix-review.md` | reviewer | fix-review | Fix patch review with extended tool access |
| `verifier.md` | verifier | — | Post-implementation verification against requirements |
| `gate.md` | gate | — | Lightweight checkpoint verification (runs subtask commands only) |
| `maintainer.md` | maintainer | — | Base maintainer: hunt mode (codebase scanning) and extraction mode (transcript analysis) |
| `maintainer_fix-triage.md` | maintainer | fix-triage | Issue triage and acceptance criteria generation |

These files live in `agentfox/_templates/profiles/` inside the package.

## Profile Structure

A profile is a plain markdown file with sections that define the agent's
behavior. There is no required schema — each profile is tailored to its
archetype — but most follow a common pattern:

- **Identity** — who the agent is and what its role is in the pipeline.
- **Rules** — hard constraints the agent must follow (e.g., "do not modify
  spec files", "read-only access only").
- **Focus Areas** or **Task Group Routing** — what to pay attention to and how
  to handle different task types.
- **Input Triage** — how to handle reports from other archetypes (review
  findings, verification verdicts) that appear in the context.
- **Constraints** — tool restrictions and operational boundaries.
- **Output Format** — the expected structure of the agent's output (e.g.,
  JSON findings for reviewers, session summaries for coders).

Profiles can include YAML frontmatter (between `---` delimiters) for metadata.
Frontmatter is stripped before injection into the prompt — it is never seen by
the agent.

### Ownership Split

`agent.md` (Layer 1) and archetype profiles (Layer 2) have a clear ownership
boundary. Universal rules live in `agent.md` and are never repeated in
archetype profiles:

**`agent.md` owns:**
- Pre-injected context acknowledgment (steering, spec artifacts, knowledge)
- Orientation guidance (git state check, codebase exploration, ADR reading)
- External reference verification (stale file paths and line numbers)
- Project structure and spec-driven workflow overview
- Quality commands (`make check` / `make test`)
- Git workflow (conventional commits, commit discipline, no Co-Authored-By,
  never push to remote)
- Scope discipline (one change per session, no unrelated fixes)
- Documentation conventions (ADRs, errata, when to update docs)

**Archetype profiles own only role-specific content:**
- Identity and purpose
- Role-specific rules (e.g., coder: never modify spec files)
- Role-specific workflow (Quick Triage, Task Group Routing, verification
  checklist, output schemas)
- Role-specific tool constraints where they differ from the generic set
- Role-specific input triage and output format

## Profile Resolution

When a session starts, the system resolves which profile to load using a
four-step priority chain. The first matching file wins:

1. **Project-level mode-specific:** `.agent-fox/profiles/{archetype}_{mode}.md`
2. **Package-embedded mode-specific:** `_templates/profiles/{archetype}_{mode}.md`
3. **Project-level base:** `.agent-fox/profiles/{archetype}.md`
4. **Package-embedded base:** `_templates/profiles/{archetype}.md`

Steps 1–2 are skipped when no mode is specified. Steps 1 and 3 are skipped
when no project directory is available.

This means:

- A project-level profile always overrides the package default.
- A mode-specific profile always overrides the base profile for that archetype.
- You can override just one mode without affecting others — if you customize
  `reviewer_pre-review.md` at the project level, the other reviewer modes
  still use the package defaults.

If no profile is found in any location, the system logs a warning and uses an
empty string (the agent runs without behavioral guidance beyond the task
context).

## Customizing Profiles

### Installing Defaults

Run `agent-fox init --profiles` to copy all built-in profiles into your
project's `.agent-fox/profiles/` directory. This is idempotent — existing
files are preserved, only missing profiles are created.

### Editing Profiles

Once installed, edit any profile in `.agent-fox/profiles/` to customize agent
behavior for your project. Common customizations:

- Adding project-specific rules (e.g., "always run `npm test` instead of
  `make test`")
- Adjusting focus areas for reviewers
- Adding domain-specific conventions to the coder profile
- Changing output format requirements

### Creating Mode-Specific Overrides

To customize behavior for a specific mode without affecting the base
archetype, create a file named `{archetype}_{mode}.md` in
`.agent-fox/profiles/`. For example, to customize only pre-review behavior:

```
.agent-fox/profiles/reviewer_pre-review.md   # your custom version
```

The base `reviewer.md` (package or project-level) still applies to other
reviewer modes unless they also have overrides.

### Custom Archetypes

You can define entirely new archetypes by placing a profile file in
`.agent-fox/profiles/{name}.md`. The system detects custom profiles via
`has_custom_profile()` and resolves them through the same priority chain.
Custom archetypes can be assigned to task groups using archetype tags in
`tasks.json` (e.g., `[archetype: my-custom-agent]`).

## How Profiles Fit Into Prompt Assembly

Profiles are one layer of the three-layer prompt assembly system. For a
detailed description of how profiles combine with task context and knowledge
facts to form the final prompt, see
[Prompt Generation](architecture/03-execution-and-archetypes.md#prompt-generation)
in the Architecture Guide.

The three layers are:

1. **Agent base profile** (`agent.md`) — universal rules for all agents (see
   Ownership Split above)
2. **Archetype profile** (e.g., `coder.md`) — role-specific behavioral guidance
3. **Task context** — spec documents (scoped to the active task group),
   knowledge facts, steering directives, prior findings

Each layer is loaded independently and concatenated with section separators.
The agent sees a single coherent system prompt composed of all three layers.
Because universal rules live exclusively in Layer 1, archetype profiles
(Layer 2) do not repeat them — each rule appears exactly once in the combined
prompt.
