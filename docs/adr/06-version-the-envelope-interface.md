# 06. Version the envelope interface: additive within a major version

**Status:** accepted
**Date:** 2026-09-12
**Supersedes:** nothing
**Related:** [ADR 03](03-rebuild-the-skills-as-tools.md)

## Context

`spec`, `issue`, `fix` and `impl` share one shell, `internal/toolio`, and so one
envelope: the single JSON object each writes to stdout. Each tool has its own
flags and its own `result` shape inside it, but the object around the result —
`tool`, `ok`, `status`, `needs_human`, `error`, `warnings`, the events on
stderr — is one interface that callers build against.

That interface has changed, and will again. Until now the only identity it
carried was `version`, the build, which says nothing about whether the shape
changed between two builds. A caller written against one shape had no way to
tell, before it acted on an envelope, whether it was looking at a shape it
understood. A field removed or retyped would break such a caller silently, at
the point it read the field, possibly after the tool had pushed a branch.

The tools can now also describe themselves (`--schema`), which makes the shape
machine-readable. A description is only worth building on if there is a
promise about how it may change.

## Decision

### 1. The interface has a semantic version, separate from the build

`schema_version` is carried by every envelope, by the `run_start` event and by
the `--schema` document. It is one constant, `toolio.SchemaVersion`, shared by
all four tools: they share the shell that defines the interface, so there is one
interface and one version. The cost is that a breaking change to one tool's
`result` moves the version every tool advertises, though nothing changed for the
other three. That is accepted: a caller that reads the major number is told to
look, and the description it then reads is exact.

### 2. Within a major version, a change is additive only

Any tool's change to the envelope is additive, and needs no version bump, when
it is one of:

- a new top-level field;
- a new field on an existing object — `result`, `needs_human`, `error`, a
  warning, an event;
- a new value in a field whose own description already documents it as an open
  set — a new warning `code`, a new error `category`;
- a new event type.

A caller is therefore required to ignore a field it does not know, and to treat
a value in an open set that it does not know as something to pass on rather than
something that cannot occur.

### 3. A change of any other kind is breaking, and bumps the major version

- removing a field;
- renaming a field;
- changing a field's type;
- narrowing a field documented as a closed enum, so that a value a caller was
  told to expect no longer occurs, or occurs under another name.

Each of these bumps the major version. A field is documented as a closed enum
only when a caller may switch on it exhaustively; every other enumerated field
is documented as an open set, and says so.

### 4. The versions so far

The envelope as it stood before the `05_envelope_decidable` and
`06_trim_and_chain_results` specs is `1.0.0`. Those two specs' changes —
`warnings` becoming structured objects, `ambiguity` and `blocker` folding into
`needs_human`, and the trimmed and added `result` fields — are the first
breaking change, and the reason `toolio.SchemaVersion` is `2.0.0` from the spec
that introduced the constant. [`docs/cli.md`](../cli.md#interface-versions)
lists each version and is where every future major bump appends its entry.

### 5. A golden file per tool keeps the description honest

`--schema` is generated from the flag set and from the Go result types, never
written by hand. Each tool's output is checked in as a golden file, and a test
fails when a field, a flag's type, default or enum, or the envelope's shape
changes without the golden file changing. The change then shows up in review as
a diff to a file that states the interface, next to the decision whether it
bumps the version.

## Consequences

**A future spec that touches the envelope states, in its own PRD, which kind of
change it is making.** Additive, or breaking and therefore a major bump. The
author consults the lists above rather than deciding case by case, and a
reviewer can check the claim against the golden-file diff.

**`toolio.SchemaVersion` is changed by hand.** Nothing computes it from the
diff. The golden-file test makes an unnoticed shape change impossible; it does
not make a forgotten bump impossible. The bump is part of the same change that
regenerates the golden files.

**Open sets must be named as open.** The description of a field such as a
warning `code` says it is an open set, because the rule depends on it. A field
described as a fixed list is a promise.

**The version covers the interface, not the build.** `version` still identifies
a build and still differs between builds; `schema_version` changes only when the
interface does, and the `--schema` document carries no build identity so its
bytes are stable until then.
