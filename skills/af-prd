---
name: af-prd
description: Write a Product Requirements Document with the user, through conversation. The PRD says what the system does and how it behaves, not how it is built, and is the input the spec tool turns into a specification package.
argument-hint: "[idea, file path, or nothing]"
---

# af-prd

You help the user write a PRD. A PRD states **what** a system should do and
**how it behaves**, from the point of view of the people who use it. It is not
a design or an implementation plan: the `spec` tool reads the PRD and generates
the requirements, tests, tasks and, with `--architecture`, the design.

The output is one markdown file, no YAML frontmatter, that `spec <path>` can
consume as it stands.

If `.specs/steering.md` exists, read it first and let it answer any question it
answers.

## What goes in, what stays out

In: behaviours and their error paths, who uses the system and why, what counts
as done, boundaries and edge cases, what the product will *not* do, and the
constraints that shape the solution space (a required protocol, a target
platform, a library the project already uses). Constraints are product
decisions.

Out: package layout, internal types and signatures, database schemas that no
user sees, concurrency design, optimisation strategy. A performance
*requirement* ("answers within 200ms") is in; the plan to meet it is out.

When the user describes how to build it, capture the behaviour they want and
say the architecture phase will handle the how. When they label something a
constraint, take it as one.

## The shape of the PRD

The same sections, in the same order, that the `spec` tool's own PRD phase
writes, so a PRD authored here and one generated from a sentence look alike:

```markdown
# {Title, as an imperative verb phrase}

## Intent
One short paragraph: what this is for and why it matters. `spec` hashes this
section to detect drift, so write it as a stable statement of purpose, not a
summary of the current plan.

## Goals
Concrete, verifiable outcomes. "p99 under 200ms", not "faster".

## Non-goals
What is deliberately out of scope. This is the cheapest section to write and
the most expensive to omit: it is what stops a generator inventing
requirements for scope you excluded.

## Background
What exists today and why this is worth doing. Name real files, commands and
modules from the codebase.

## Requirements
The behaviour, in prose, grouped by area. For each: the happy path, the error
paths, the boundaries, and what happens when a dependency is unavailable.
Each statement should be checkable.

## Design Decisions
Every question the input left open, the answer chosen, and one line of why.
Numbered. Omit when there were none.

## Dependencies
Existing specs or external systems this depends on or changes, each with a
reason. Omit when there are none.

## Open Questions
Only what neither the user nor you could settle. Omit when empty.
```

A PRD may be more than one spec's worth of work. Write it whole; `spec`
detects that and splits it into one package per scope.

## How to work

1. **Read the input.** `$ARGUMENTS` is a file path, an idea in prose, or
   absent. If absent, ask what they want to build. Separate what is being
   asked for from the author's guess at a solution.

2. **Read the codebase**, if there is one: `README.md`, the docs, the layout,
   and the existing packages under `.specs/` and PRDs under `docs/prds/` for
   vocabulary, numbering and overlap. A PRD written against the code names
   real modules and does not propose a component that already exists under
   another name. Read only files `git ls-files` lists.

3. **Draft.** Write the whole PRD in the shape above. Where you lack
   information, write `**[GAP]** the question` in place, so every hole is
   visible. Show the draft; do not save it yet.

4. **Close the gaps.** Ask about them in small batches, the ones that most
   change the shape of the work first: intent and scope, then the core
   behaviours and their failures, then edges and constraints. Fold each
   answer into the PRD and show what changed. Stop when the gaps are closed
   or the user wants to stop; a good PRD that ships beats a complete one
   that does not.

   If the user says "you decide", decide every open point, record each in
   `## Design Decisions`, and carry on. A question nobody can answer goes
   to `## Open Questions`.

5. **Check**, before presenting the final version: no `[GAP]` left; goals
   verifiable; non-goals specific; every requirement has its error path; no
   internal structure leaked (package layout, types, signatures); constraints
   labelled as constraints. It should read as a product
   manager's document, not an architect's.

6. **Save.** The repository convention is `docs/prds/NN-imperative-verb-phrase.md`,
   numbered with the next free two-digit prefix, with a row added to
   `docs/prds/README.md`. Offer that path; take the user's if they give one.
   Then show the next step:

   ```sh
   spec docs/prds/NN-name.md                 # generate the spec package
   spec docs/prds/NN-name.md --architecture  # and the design document
   ```
