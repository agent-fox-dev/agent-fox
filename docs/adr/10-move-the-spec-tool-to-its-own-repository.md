# 10. Move the spec tool to its own repository

**Status:** accepted
**Date:** 2026-10-14
**Supersedes:** nothing
**Related:** [ADR 02](02-build-the-spec-pipeline-on-agentkit.md),
[ADR 05](05-write-every-scope-of-a-split.md),
[ADR 06](06-version-the-envelope-interface.md)

## Context

This repository built four tools on one shell: `spec`, `triage`, `fix` and
`impl`. `spec` turns a product idea into a specification package; the other
three read a problem or a package and act on a repository. `spec` is now
built and released from [`agent-fox-dev/spec`](https://github.com/agent-fox-dev/spec),
next to the format it writes. Two copies of the same tool cannot both be the
real one.

## Decision

`cmd/spec` and the `specgen` pipeline are deleted from this repository, along
with their tests, their fixtures and their documentation. `make build`,
`make build-all`, `install.sh` and the tools container ship `triage`, `fix`
and `impl`.

`afspec` stays. `impl` loads, validates and updates the packages `spec`
writes, and the format's JSON Schemas stay copied into `afspec/schemas/`.

What only `spec` produced leaves the shared envelope: the `spec_package`
artifact kind, with the `id` and `valid` fields only it carried, and the
thirteen warning codes only it raised (`specs_dir_unreadable`,
`project_language_unknown`, `name_flag_ignored`, `scope_renamed`,
`scope_count_mismatch`, `relevant_files_unavailable`, the five `split_plan_*`
codes, `architecture_not_written` and `activation_failed`). The artifact
kind is a closed set, so removing a value is breaking under ADR 06, and
`toolio.SchemaVersion` moves from 3.1.0 to 4.0.0. Nothing else changes for
`triage`, `fix` or `impl`.

## Consequences

**ADRs 02 and 05 are kept as history.** ADR 05's split, and the part of ADR 02
about the spec pipeline, now describe code in `agent-fox-dev/spec`. The
AgentKit layer ADR 02 introduced, `internal/agentrun`, stays here: the other
three tools run on it.

**The envelope is no longer one interface across all agent-fox tools.** The
four tools used to advertise one `schema_version` from one shell. `spec` now
versions its envelope in its own repository, and a caller that drives it as
well as these three reads that tool's version separately.

**A package is written in one repository and implemented in another.** The
spec format is the contract between them, as it already was between `spec`
and `impl`. A format change lands in `agent-fox-dev/spec` first; its schemas
are then copied into `afspec/schemas/` and `make json-gen` regenerates the Go
types.
