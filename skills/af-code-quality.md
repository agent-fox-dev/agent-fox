---
name: af-code-quality
description: Review a codebase's design and quality against the rules it has set for itself, then improve it. Finds boundaries that leak, concepts that exist twice, conventions that drifted, and tests that prove nothing, and turns the agreed findings into verified changes and recorded decisions.
argument-hint: "[package, directory, or nothing for the whole repository]"
---

# af-code-quality

You review how a codebase is built and make it better built. Where
`af-code-simplifier` removes weight without changing design, this skill is
allowed to change the design: move a responsibility, redraw a boundary,
replace a mechanism. Because it is allowed to, it has to earn each change.

If `.specs/steering.md` exists, read it first. Follow the repository's own
agent instructions (`CLAUDE.md`, `AGENTS.md`) for branching, commits and
checks.

The standard is the project's own, not a textbook's. A repository that has
written down how it wants to be built (ADRs, a steering file, a README that
explains its shape) is reviewed against those documents. Where it has not
written anything down, the standard is consistency: the way most of the code
already does it. Generic principles come last, and only where they decide a
question the project has left open.

## 1. Learn the intended design

- `$ARGUMENTS` is a package or directory: that is the target, read in the
  context of what imports it and what it imports. No argument: the whole
  repository.
- Read `README.md`, every ADR, the errata and the steering file. Write down,
  for yourself, the three to five rules the project has committed to. Every
  finding below is measured against this list.
- Read the target in full, and the tests beside it. Read only files
  `git ls-files` lists. The code is the truth about what *is*; the documents
  are the truth about what was *meant*. A gap between them is a finding
  either way: the code drifted, or the document is stale.
- Run the project's checks and record the result, so you know which failures
  are already there.

## 2. Map the structure

Before judging anything, draw the shape: the packages or modules, which
depends on which, where the entry points are, where state lives, where the
boundaries with the outside world are (network, filesystem, shell, a model, a
user). A short dependency sketch in text is enough. Most design findings are
visible in this sketch before a line of logic is read.

## 3. Find the findings

Each finding cites the file and line, names the rule or convention it
violates, and says what it costs: a change that has to be made in two places,
a test that cannot be written, a reader who has to know something the code
does not say.

**Boundaries**
- A dependency pointing the wrong way: a core package importing a leaf, a
  library importing its own command, a low-level module reaching into
  policy.
- A concern leaking across a boundary: parsing in the transport layer,
  rendering in the domain, a filesystem path in a type that should not know
  there is a filesystem.
- A boundary the project says it has that the code does not enforce. If a
  document says "X cannot happen" and nothing in code prevents X, that is the
  first finding.

**Concepts**
- The same idea with two names or two implementations, in two packages.
- A type that carries fields for every caller's use, or a function with a
  mode flag that makes it two functions.
- Primitives standing in for a domain concept everywhere it appears.

**Conventions**
- Error handling that differs from one package to the next: wrapped here,
  swallowed there, logged and returned both.
- Naming that follows one scheme in half the code and another in the rest.
- A pattern most of the code uses that a few places do not.

**Tests**
- A test that passes if the code is deleted. A test that asserts the
  implementation, not the behaviour. A behaviour the documents promise that
  no test checks. A test suite that needs the network, a credential or a
  particular machine.

Say what is good, in one short paragraph. A review that names only faults
teaches nothing about what to preserve.

## 4. Propose

One numbered list, ranked by what it would cost to leave alone. Each item:
the finding, the change, the files, the risk, and whether it is

- **convention**: brings code in line with what the project already does;
  no decision needed, you may proceed; or
- **design**: changes a boundary, a mechanism or a public surface; needs the
  user's decision, and when accepted needs a record (an ADR in the project's
  own format, or the project's equivalent) stating the context, the decision
  and the consequences.

Wait for the user on every design item. Do not bundle a design item into a
convention one to avoid asking.

## 5. Change

Each agreed item is its own commit, with the checks run before it is kept.
For a design item, the record is part of the same change. When a change
touches a documented behaviour, the document changes in the same commit.
Checks going red means revert that item and report it, not adjust the test.

## Never

- Rewrite for taste. Every change traces to a finding, and every finding to a
  rule the project has or a cost it is paying.
- Introduce a layer, a framework or a pattern the project does not already
  use without a design item the user accepted.
- Change a public surface without the user's explicit yes.
- Delete a test to make a change pass. Weaken a test only by replacing it with
  one that proves more.
- Treat the absence of a document as licence. Where nothing is written,
  consistency rules.

## 6. Report

- The rules you reviewed against, in one line each.
- The findings, with their disposition: changed, accepted and waiting,
  declined by the user, or left as a known cost.
- The check results, from the run.
- Documents written or updated.
- What is good and should stay that way.
