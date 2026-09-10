# 04. Write every scope of a split, and record the split until it is done

**Status:** accepted
**Date:** 2026-09-10
**Supersedes:** nothing
**Related:** [ADR 03](03-rebuild-the-skills-as-tools.md)

## Context

The PRD phase checks the size of the work. A spec is one cohesive feature —
at most 10 requirements and 8 tasks — and a PRD for a whole subsystem is
several. So the phase writes the PRD for the first, foundational scope and
reports the rest as `recommended_split`.

Until now that was where it ended. `spec` wrote one package, printed a
warning that the input was "N specs' worth of work; this package covers the
first scope only", and exited 0. The split lived in the JSON result and
nowhere else. A caller who wanted the remaining scopes had no way to ask for
them: running `spec` on the same input again would run the whole PRD phase
again and write a second copy of the first package, and there was no flag,
no file and no prompt that carried the plan forward.

The first real input showed the gap. `docs/drafts/forge_neutral_issue_pr_client.md`
was split four ways; `.specs/01_issuex_core` was written; its own
`## Non-goals` says the rest "lands in the follow-on specs listed under
'Recommended split'" — and there is no such list anywhere on disk.

## Decision

### 1. A split is work, not advice

The tool writes every scope of a split, one package per scope, in the order
the PRD phase gave. The first package comes from the PRD just written. Each
later scope is its own PRD phase, then the three generation phases, then the
write, validation and activation — the same pipeline as an undivided input,
with one addition to the PRD task: a block that states the decided split as
a fact. Which scopes exist, which this one is, and that its name is fixed.

The name is the plan's, not the model's. A follow-on PRD phase that renames
its scope is overruled with a warning, because the name is how the next run
tells that this scope was written. A follow-on phase that reports a further
split is likewise a warning: the split was decided once, before the first
package was written, and re-deciding it per scope is how a run never ends.

### 2. The split is recorded, until it is done

The plan — the input's identity, every scope, and which of them have a
package — is written to the spec root as `<first_scope>.split.json` the
moment the PRD phase reports a split, updated after each package, and removed
by the run that writes the last one.

This is the one piece of state the tool keeps between runs, and ADR 03
deleted a session state machine for good reason. The difference is what the
file means. `_session.json` described a conversation a person was in the
middle of. The plan describes work the tool owes: its presence means exactly
"`spec` stopped before it was done", and running `spec` on the same input
again is the whole resume protocol. No flag, no subcommand.

Matching a plan to an input is done in Go. A file or issue input matches by
origin, because an input edited between runs is the common reason a run is
repeated and a second copy of the first package is the wrong answer to it;
text and stdin match by content, because every text input's origin is
"argument". A plan for another input is reported and left alone.

### 3. An invalid package stops nothing

A package that does not validate is on disk with its errors named, and the
scopes after it are not made better by being skipped. The run writes them,
then exits 1 with `invalid_spec` naming every package to fix. Any other
failure — budget, the API, the disk — stops the run on the scope it happened
in, with the scope in the message and the plan recording what exists.

### 4. The result keeps its shape

The first package this run wrote is at the top level of `result`, as the only
package always was. `follow_on_specs` carries the rest, `split` every scope
with its state, and `split_plan` the file while it exists. `ok` is true only
when every scope has a valid package.

## Consequences

**A split costs what it costs.** Four scopes are four PRD phases and twelve
generation phases, under the same per-phase bounds. The alternative — one
package and a recommendation — was cheaper and did not do the job. A caller
who wants only the first scope can stop the run and delete the plan; the
package it wrote is complete on its own.

**`recommended_split` leaves the result.** Its meaning — "the package covers
the first scope only" — is no longer true of anything the tool produces.
`split` replaces it with the plan and each scope's state.

**The spec root gains a file type.** `*.split.json` is not part of the spec
format, and `afspec.DiscoverSpecs` ignores files, so nothing that reads
packages sees it. It is committed or not at the operator's discretion; an
unfinished split checked in is an honest record of unfinished work.

**The PRD prompt learns about the split.** The system prompt tells the model
that the scopes it names become packages, that the first must be the PRD it
submits, and that the later ones belong in `## Non-goals` by name, so a PRD
no longer refers to a list that exists nowhere.
