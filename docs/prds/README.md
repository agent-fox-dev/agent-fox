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

Proposed for how a run chooses its model.

| Document | What it asks for |
|---|---|
| [07-replace-the-variant-flag-with-effort.md](07-replace-the-variant-flag-with-effort.md) | `--effort` and `$AF_MODEL_EFFORT` overriding a tier's reasoning effort, or setting one for a model named by id; `--repair-model-effort` on `impl`; clamping checked before the run with an `effort_clamped` warning; `--variant` and the tier table's variant level removed |

Proposed for a fifth tool without a model phase.

| Document | What it asks for |
|---|---|
| [08-add-the-issue-tool.md](08-add-the-issue-tool.md) | `issue`: every `issuex.Client` operation as `issue --op <name> <target>` on GitHub and GitLab, on the shared envelope and exit codes, with the forge, host and token resolved from the same environment variables the four tools read; no model is resolved. Not the tool that was renamed to `triage`. Needs the shell in `internal/toolio` to run a tool that has no model phase |
