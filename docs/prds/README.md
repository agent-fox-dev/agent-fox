# PRDs

The product requirements documents this repository was built from. Both were
written before [ADR 03](../adr/03-rebuild-the-skills-as-tools.md) replaced the
`spec` CLI with the four tools, so they describe a program that no longer
exists; they are kept as the record of why `afspec` looks the way it does.

| Document | What it asked for | What became of it |
|---|---|---|
| [afspecgo.md](afspecgo.md) | A Go port of the Python `afspec` library with byte-for-byte round-trip fidelity | The [`afspec`](../../afspec/README.md) package, since moved to format version 2 ([ADR 01](../adr/01-adopt-spec-format-v2.md)) |
| [gospec.md](gospec.md) | A Go port of the `agentspec` package and the `spec` CLI, subcommand for subcommand | Built, then deleted: ADR 03 replaced the fifteen-subcommand CLI with the `spec` tool, and [ADR 02](../adr/02-build-the-spec-pipeline-on-agentkit.md) replaced its model layer with AgentKit |

New PRDs go here as `NN-imperative-verb-phrase.md`, numbered from `01`.

## Proposed

PRDs for making the four tools better tools for a model to call. The first
three change the interface in `internal/toolio` rather than any pipeline; the
fourth serves that interface over MCP. They are meant to be specified and
built in order.

| Document | What it asks for |
|---|---|
| [01-make-the-envelope-decidable.md](01-make-the-envelope-decidable.md) | JSON on every non-terminal invocation; `status` and `summary`; one `needs_human` shape and `--context` to answer it; `retryable`, `resumable` and `fix_hint` on errors; structured warnings |
| [02-trim-and-chain-the-results.md](02-trim-and-chain-the-results.md) | `--detail summary` by default with a full report file; `artifacts`, `side_effects` and `next`; a warning and `--input-kind` for a mistyped path; `--dry-run` and `--total-budget` shared |
| [03-make-the-tools-observable-and-self-describing.md](03-make-the-tools-observable-and-self-describing.md) | `--events jsonl` and `--output`; `--schema` and `schema_version`; trust labels on untrusted text; `--preflight` |
| [04-serve-the-tools-over-mcp.md](04-serve-the-tools-over-mcp.md) | `af mcp`: an MCP server over the four binaries, with jobs for long runs, an operator policy the model cannot override, progress, cancellation and elicitation. Builds on 01–03 |
| [05-write-events-to-the-state-directory.md](05-write-events-to-the-state-directory.md) | Events always written beside the report; `--emit-events` replaces `--events` and `--events-file`; a `session_id` joining events and reports; one overridable state directory |

Proposed for how the phases read the codebase. The split between this
repository and AgentKit, and the order of the work, is
[ADR 07](../adr/07-split-code-navigation-between-agentkit-and-agent-fox.md).

| Document | What it asks for |
|---|---|
| [06-stop-re-reading-the-codebase-every-phase.md](06-stop-re-reading-the-codebase-every-phase.md) | Per-phase `tool_calls` and `tool_result_bytes` in the envelope; a repository map computed once per run and given to every `spec` phase; `relevant_files` on `submit_prd`, handed to the later phases |
| AgentKit [PRD 04](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/04-add-symbol-navigation-tools.md) | The `outline` package and an exported ignore-aware walk (which 06's map is built from); `file_outline` and `find_symbol` |
| AgentKit [PRD 05](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/05-add-an-indexed-code-search-module.md) | Gated on 06's numbers: a zoekt-backed `code_search` tool in a separate `codesearch` module |
| [09-add-a-find-references-tool.md](09-add-a-find-references-tool.md) | `find_references`: who calls or uses a declaration, each site attributed to its enclosing declaration and labelled `resolved` (Go, via `go/types`), `lexical` (outline-aware) or `text`; an exported seam for call edges; the tool added to every phase's read set. Mechanism in AgentKit, wiring here, per ADR 07 |

Proposed for how a run chooses its model.

| Document | What it asks for |
|---|---|
| [07-replace-the-variant-flag-with-effort.md](07-replace-the-variant-flag-with-effort.md) | `--effort` and `$AF_MODEL_EFFORT` overriding a tier's reasoning effort, or setting one for a model named by id; `--repair-model-effort` on `impl`; clamping checked before the run with an `effort_clamped` warning; `--variant` and the tier table's variant level removed |

Proposed for a fifth tool without a model phase.

| Document | What it asks for |
|---|---|
| [08-add-the-issue-tool.md](08-add-the-issue-tool.md) | `issue`: every `issuex.Client` operation as `issue --op <name> <target>` on GitHub and GitLab, on the shared envelope and exit codes, with the forge, host and token resolved from the same environment variables the four tools read; no model is resolved. Not the tool that was renamed to `triage`. Needs the shell in `internal/toolio` to run a tool that has no model phase |

Proposed for how `impl` and `fix` grade their own work, and built: see
[ADR 09](../adr/09-grade-the-work-independently.md).

| Document | What it asks for |
|---|---|
| [10-make-shipped-prs-match-their-specs.md](10-make-shipped-prs-match-their-specs.md) | An independent conformance review before the pull request; unmet requirements at its top, tracked; tests held to their observable contract; verification in a clean environment; survey decisions as checks; structural checks; docs copied from code; scope; `fix` closing an issue only on a proven fix |

Proposed for what happens after `impl` and `fix` have opened their pull
requests.

| Document | What it asks for |
|---|---|
| [11-review-a-landed-change-before-it-merges.md](11-review-a-landed-change-before-it-merges.md) | `review`: a fifth tool that takes the pull request `impl` opened or the issue `fix` worked on, establishes the merge facts in Go (the forge's state, the checks on the merge tree in a clean environment, the structural and scope checks) and runs an independent conformance review in its own process, then decides `merge` or `correct`: it merges, or files one issue per gap in the shape `fix` consumes, and says why on the pull request. Amended by 12 |

Proposed for how the tools record their work where the project already is.
It amends 08 and 11 in place.

| Document | What it asks for |
|---|---|
| [12-track-the-work-in-forge-issues.md](12-track-the-work-in-forge-issues.md) | One tracking issue per spec package, on GitHub or GitLab through `issuex`: `spec` creates it where the spec is created and records where it came from, writing its URL as `tracking` in the PRD frontmatter; `impl` posts start and end comments and ticks a task checklist in a program-owned region of the body as each task lands; `review` posts its verdict and the merge; `fix` posts its correction on the tracker a gap issue leads back to, and `--track off` lets any run leave no trail. Every write is rendered by Go from facts, through one shared `internal/tracking` package, and no model phase sees the issue |

Proposed for the machine behind `fix` and `impl`, after the analysis of
2026-10-06 (issues #215 to #224). The interface of both tools is untouched;
what changes is behind it. AgentKit
[PRD 06](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/06-support-multi-phase-coding-pipelines.md)
supplies the SDK side (a stable cache prefix, transcript pruning, honest
usage, a process runner, a required tool, language-neutral symbol
navigation), and the three are built in the order PRD 13 §9 gives.

| Document | What it asks for |
|---|---|
| [13-rebuild-fix-on-a-shared-change-engine.md](13-rebuild-fix-on-a-shared-change-engine.md) | `internal/lang`: every language as one declarative profile, polyglot detection, a denylist test that no language is spelled anywhere else. `internal/engine`: the ledger of what a run changed, one process runner that tells a timeout from a cancellation, a gate answered from a tree-hash cache, a targeted and crash-safe revert check, parking on every path, `aborted` everywhere. A run brief with a stable cache prefix, pruned transcripts, check logs read on demand, `run_checks`, rejections that keep what was right. `fix` rewritten over it; `unverified` parks unless `--no-verify` |
| [14-rebuild-impl-on-the-shared-change-engine.md](14-rebuild-impl-on-the-shared-change-engine.md) | `impl` over the same engine: the gate audited against every detected language; the run brief with the PRD, a digest of the spec and a map that does not churn; a task message that is the task; `run_checks revert`; a failed attempt continues the first agent instead of discarding it; declared deviations as commit trailers, so continuation sees them; the review reads contracts, not the package; resolve sees every finding and the stage is budgeted; `impl` runs the suite N+1 times and the model never does |
