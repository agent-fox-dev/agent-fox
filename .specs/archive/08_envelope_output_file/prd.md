---
spec_id: "08"
spec_name: "envelope_output_file"
title: "Persist the envelope to a file too (--output)"
status: "active"
created_at: "2026-09-30T10:12:08.611829Z"
updated_at: "2026-09-30T10:12:08.611829Z"
intent_hash: "82d1c7f4c71372429126ef14245c0c2b7aab573f69c2c08b3a3913c0b3b959f3"
schema_version: 2
source: "docs/prds/03-make-the-tools-observable-and-self-describing.md"
---
## Intent

A caller that drives `spec`, `issue`, `fix` or `impl` as a subprocess and reads its result from stdout has exactly one copy of that result: if its own stdout pipe is closed, discarded, or never re-attached to before the tool finishes — the ordinary way a supervisor loses a long-running child's output — the envelope is gone even though the tool did the work and knows exactly what happened. This spec gives every tool a second, durable copy: a shared `--output <path>` flag that writes the same final envelope to a file, atomically, before anything is written to stdout, on every path that produces an envelope at all, including the tool's internal fallback for a result that cannot be marshalled. It reconciles this with the report file `06_trim_and_chain_results` already writes: the two flags answer different questions, and when an operator points both at the same destination, the complete report — not whichever `--detail` view `--output` would otherwise have produced alone — is what ends up there.

## Goals

- A run given `--output <path>` leaves one complete, valid JSON envelope at that path, written before the run writes anything to stdout, so a caller that has already lost its own stdout by the time the run finishes still finds the result on disk.
- The file at `--output` is never observed partially written: a reader either finds nothing yet, or the complete document.
- `--output` behaves identically on all four tools and on every exit path — success, failure, a usage error, `needs_human`, `unverified` — and on the one internal-error fallback `Emit` already has for a result it cannot encode.
- A failure to write `--output` (a missing parent directory that cannot be created, a permission error, a full disk) never fails the run or changes its exit code; it is recorded as a low-severity warning naming the path and the cause.
- When `--output` and `06_trim_and_chain_results`'s report file resolve to the same path, the file that lands there is the complete report, and a warning says the two flags collided, rather than one silently overwriting the other with a smaller view.

## Non-goals

- The machine-readable progress stream, `--events`/`--events-file` and the event types — `07_progress_event_stream`, already written; this flag is orthogonal to it (one persists the final result, the other streams progress while the run is live) and the two compose without interaction.
- `--schema`, `schema_version` and the additive-only compatibility rule — `tool_self_schema`, not yet written.
- Classifying which envelope strings are untrusted text — `untrusted_text_labels`, not yet written.
- `--preflight` — `preflight_checks`, not yet written.
- Anything about `--detail`, `--report-file`, its default location, or the shape of `result` — that is `06_trim_and_chain_results`'s own scope; this spec only adds the two warning codes needed to describe what happens when `--output` and the report file collide.
- Recovering the envelope for a run whose own process was killed before it finished. Nothing can persist a result for work that never reached one; a caller that needs to tell a live run from a hung one has `07_progress_event_stream`'s heartbeat for that. This spec only protects the finished envelope against the caller's own stdout being lost.
- Sanitising or redacting anything before it is written to `--output`. The file gets exactly what stdout would have gotten (or, on a collision, what the report file holds) — deciding what to do with any of it is unchanged, and stays the caller's.

## Background

Every tool shares one shell, `internal/toolio` (`App`, `Run`, `Envelope`, `Emit`), per [ADR 03](../adr/03-rebuild-the-skills-as-tools.md). `App.Main` (`internal/toolio/app.go`) has exactly two call sites that produce the one JSON object a run ever writes: one for a flag-parsing failure, and one at the very end of `Main`, after `a.execute` returns `(code, result, failure)`, via `Emit(stdout, run.Envelope(code, result, failure))`. `Emit` (`internal/toolio/envelope.go`) marshals the envelope and writes it to `stdout`; if marshalling itself fails — the one case today's tests exercise deliberately, an unencodable field in `result` — it falls back to a small, guaranteed-encodable envelope carrying just the failure, so a caller is never left with nothing on stdout. Nothing today writes the envelope anywhere else: a caller's own stdout is the only copy, and if that pipe closes before the process holding the other end reads it, the result is gone even though the tool finished cleanly.

`Common` (`internal/toolio/cli.go`) is the one flag set every tool's `Register` call shares (`--dir`, `--model`, `--budget`, `--verbose`, `--quiet`, …); a shared flag like this one is added there once, per ADR 03's rule that every addition to the tools is a flag, never a new subcommand.

This spec is scope 2 of a five-scope split of `docs/prds/03-make-the-tools-observable-and-self-describing.md`. Scope 1 (`07_progress_event_stream`, written) gives a caller a live signal while a run is in flight. This scope gives the same caller a durable copy of the run's outcome once it is done. `06_trim_and_chain_results` (active, not yet implemented in the tree) is depended on directly: its requirement 1 adds `--report-file <path>` (explicit or a computed default under `$XDG_STATE_HOME/agent-fox/runs/…`), a `--detail summary|full` flag that trims what stdout carries, and a `report_file` envelope field naming where the *complete* view — everything `--detail full` would have printed, regardless of what `--detail` stdout actually used — was written; a run that cannot write it records a low-severity warning and omits the field, rather than failing. `--output` is a second, independent destination for *whatever `--detail` produced for stdout*, not a second full view — the two exist for different reasons (one is "the caller asked to see less"; the other is "the caller might lose stdout entirely") and this spec has to say what happens when an operator points both at the same file.

The codebase already has an atomic-write idiom for exactly this shape of problem — write to a temp file in the target's own directory, `Sync`, `Close`, then `os.Rename` over the destination — used by `afspec.Save` (`afspec/afspec.go`) and `codeimpl`'s own state file (`codeimpl/state.go`'s `saveTasks`). This spec reuses that idiom rather than inventing a second one.

## Requirements

### 1. `--output <path>`, a new flag on `Common`

Registered once, alongside the rest of `Common`'s flags (`internal/toolio/cli.go`), so every tool accepts it with identical meaning. Its default is empty, meaning nothing is written beyond stdout — today's behaviour, unchanged.

- The path is resolved to an absolute path against the process's current working directory, **not** against `--dir`'s workspace root: `--output` names a destination on the machine running the tool, for whatever process is watching it, and is not one of the file tools `tools.Workspace` sandboxes inside the repository. A relative `--output` behaves the same as a relative `--report-file` or any other ordinary CLI path flag.
- `--output -` is a usage error (exit 2): `-` already means "read stdin" for the tool's one positional input, and accepting it here too would make the same character mean two different things depending on which flag it followed.
- A path that already exists and is a directory is a usage error (exit 2), caught before anything is fetched — the same point and the same reasoning as `--dir`'s own directory check in `Common.Workspace()`. A path whose parent directory does not yet exist is not an error: the parent is created (`os.MkdirAll`, matching `06_trim_and_chain_results`'s own report-file requirement) when the envelope is actually written.
- This validation happens once, as part of parsing the shared flags, before any tool's own `PreCheck` runs and before `Workspace()`, `Resolve`, or model resolution — nothing is fetched, read, or spent before a malformed `--output` is refused.

### 2. What gets written, and when

`--output` receives exactly the envelope the run is about to write to stdout — the same `--detail` view, the same `warnings`, the same `error` — except in the one collision case requirement 4 describes.

- The write happens **before** anything is written to stdout, on every path that produces an envelope: both existing `Emit` call sites in `App.Main` (the flag-parsing-failure path and the end of `Main`), so a caller whose stdout pipe is already gone, or who is killed the instant after the tool exits, still finds the file. On the flag-parsing-failure path specifically, this only happens when `--output` itself was already parsed successfully before the flag that caused the failure — an inherent, one-directional consequence of how `flag.Parse` reports the first bad token and leaves everything parsed before it in place, not a gap this spec has to close.
- It also happens on `Emit`'s own internal fallback — the rare case where `result` cannot be marshalled at all. The safe, minimal envelope `Emit` falls back to for stdout in that case is the same one written to `--output`, so even this defensive corner case leaves something usable at the path rather than nothing.
- The write is atomic: a temp file created in the same directory as the target, written, `Sync`d, closed, then renamed over the destination — the same three-step idiom `afspec.Save` and `codeimpl`'s `saveTasks` already use. A reader polling the path never sees a partially written document.
- `--output` is written under `--dry-run` exactly as it is otherwise: it is local state on the machine running the tool, not a remote change, the same reasoning `06_trim_and_chain_results` gives for still writing its report file under `--dry-run`.
- `--output` writes nothing, and none of this validation runs, on the two purely human-driven paths that already write nothing to stdout: `-h`/`--help`, and a bare invocation with no positional argument on a terminal. Neither produces an envelope, so there is nothing to persist. (A bare invocation whose stdout is *not* a terminal does produce one, per `05_envelope_decidable`'s requirement 1, and `--output` applies to it the same as to any other envelope-producing path.)
- `--output` never changes `ok`, `status`, or `exit_code`: whether the file was requested, and whether writing it succeeded, is invisible to the run's own outcome.

### 3. A failed write is a warning, not a failure

Writing `--output` can fail for reasons that have nothing to do with whether the tool did its job: a read-only filesystem, a permission error, a full disk, a parent directory that cannot be created. None of these are allowed to change the run's exit code.

- On failure, the run records a new low-severity warning, `output_not_written` (stage `emit`), naming the path and the underlying error — added to the central `WarnCode`→stage table `05_envelope_decidable` establishes, next to that spec's own table and `06_trim_and_chain_results`'s `report_file_not_written`, since neither existing code fits a failure to write a second, caller-requested copy of the envelope.
- This warning is recorded before the single envelope that goes to stdout for that run is assembled, so it is visible there too — a caller that still has its own stdout learns immediately that the second copy did not land, rather than discovering it only by later checking for a file that never appeared.
- The one exception is `Emit`'s own internal marshal-failure fallback (requirement 2): if the fallback envelope itself then also fails to write to `--output`, there is no further envelope left to carry a warning about it, so this one case is surfaced only in the underlying error already implicit in that fallback's own message on stdout, not as a second, separate `warnings` entry.

### 4. Reconciling with `06_trim_and_chain_results`'s report file

`--report-file` (explicit or the computed default) and `--output` answer different questions — "give me the full view regardless of what stdout showed" versus "put a copy of whatever stdout showed somewhere durable" — and normally write independent files. When an operator points both at the same destination, only one document can exist there, and it is the full one:

- The two paths are compared as resolved, absolute, cleaned paths (`filepath.Abs` + `filepath.Clean`), not by resolving symlinks — both files are typically about to be created and may not exist yet, so a symlink cannot reliably be resolved beforehand, and this spec does not need it to be: an operator who names the same string twice, however it later resolves, is the case worth calling out.
- The comparison is between the two *configured* destinations, independent of whether either write actually succeeds. A configured collision produces the warning below regardless of outcome; it is not conditioned on `06_trim_and_chain_results`'s own write having landed.
- When they collide, `--output`'s own, independent write (requirement 2) is skipped entirely — no second write is attempted at that path, successful or not — and a new low-severity warning, `output_matches_report_file` (stage `emit`), records that both flags named the same destination and that the file there (when the report-file write itself succeeds) holds the complete report, not necessarily the `--detail`-selected view `--output` alone would have produced. If the report-file write itself then fails, nothing lands at the shared path; the resulting envelope carries both `output_matches_report_file` and `06_trim_and_chain_results`'s own `report_file_not_written`, which together explain why, rather than this spec silently retrying with a smaller view instead.
- `output_matches_report_file` is added to the same central `WarnCode` table as `output_not_written`.

### 5. Documentation

- `docs/cli.md`'s shared-flags table gains a `--output` row, stating that it writes a second copy of the same envelope stdout gets, atomically, before stdout, and what happens on a collision with `--report-file`.
- The warning-codes table (added by `05_envelope_decidable`) gains `output_not_written` and `output_matches_report_file`, alongside `06_trim_and_chain_results`'s `report_file_not_written`.

## Design Decisions

1. **No new envelope field confirms that `--output` was written.** `06_trim_and_chain_results`'s `report_file` field exists because the report's path is often the *default*, computed one — the caller does not already know it and needs it echoed back. `--output`'s path is always the caller's own literal argument; echoing it back would be pure noise on success, and would need the write to have already happened before the envelope destined for that same file could truthfully include a field describing it — a circularity `report_file` does not have, since that field's presence is decided once, before the single final envelope for the whole run is assembled, not self-referentially inside the file it names. A failure is still visible, as a warning (requirement 3); a success is visible by the file simply existing.
2. **`--output` resolves against the process's working directory, not `--dir`'s workspace root.** Every existing path the tools sandbox (`tools.Workspace`) is a path the *model* touches inside the repository being worked on; `--output` is an operator/supervisor concern about where the tool's own result lives, matching how `--report-file` is described (a location on the machine running the tool, not inside the repository). Nothing in the codebase already establishes this convention for an operator-facing path flag, since none exists yet — flagged below as worth checking once `--report-file` actually lands.
3. **The write reuses the existing atomic-write idiom (temp file in the target directory, `Sync`, `Close`, `Rename`) rather than a new one.** `afspec.Save` and `codeimpl`'s `saveTasks` already establish it for exactly this failure mode (a reader must never see a half-written file); inventing a second convention for the same problem would be needless.
4. **`--output -` and an existing-directory `--output` are both usage errors, caught before anything is fetched.** `-` already means "read stdin" for the tool's one positional argument; accepting it silently as a literal filename here would make the same token mean two different things depending on position. An existing directory is refused the same way `--dir` already refuses one, rather than surfacing as a cryptic rename failure deep inside a finished run.
5. **A failed `--output` write downgrades to a `low` warning and never changes the exit code**, mirroring exactly how `06_trim_and_chain_results` already treats a failed report-file write — the two mechanisms fail the same way, for the same reason: persistence to a second location is not part of what decides whether the tool did its job.
6. **Collision detection compares the two flags' configured, resolved paths, not their write outcomes.** An operator who names the same path twice has made a single decision about where the result should live; whether either individual write later succeeds is a separate fact, already covered by each mechanism's own failure warning, and should not change whether the collision itself is reported.
7. **`--output` is written under `--dry-run`, exactly like the report file.** Both are local state describing a run that already happened by the time either is written; neither is the kind of remote effect `--dry-run` exists to suppress.
8. **The ordering guarantee ("before stdout, including `Emit`'s internal fallback") is implemented at the two existing choke points that already produce every envelope — `App.Main`'s two `Emit` calls, and `Emit`'s own marshal-failure branch — rather than a third mechanism.** No pipeline (`codefix`, `codeimpl`, `issuetriage`, `specgen`) changes: they already only ever return `(code, result, failure)` to `toolio`, which is the one place that decides what gets written and where.

## Dependencies

| Spec | Reason |
|---|---|
| `05_envelope_decidable` | Supplies the structured `Warning{code, severity, stage}` shape and the central `WarnCode`→stage table this spec's two new codes (`output_not_written`, `output_matches_report_file`) are added to; `ok`/`status`/`exit_code` semantics this spec must not disturb are also defined there. |
| `06_trim_and_chain_results` | Supplies `--report-file`, its default location, the `--detail summary\|full` view `--output` mirrors, and the `report_file` envelope field this spec's requirement 4 reconciles `--output` against when the two name the same path. |

## Open Questions
