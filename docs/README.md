# agent-fox documentation

| Document | What it covers |
|---|---|
| [Tool Reference](cli.md) | `spec`, `triage`, `fix` and `impl`: the shared interface, every flag, the JSON envelope and the exit codes |
| [Configuration](configuration.md) | Credentials, model selection, what the model is allowed to read, bounds |
| [Model Usage](model-usage.md) | What each phase sends, and what happens when the answer is wrong |
| [Development](development.md) | Setup, repository layout, the schema workflow, testing |
| [Go Library API](../afspec/README.md) | The `afspec` spec-format library |

## Architecture decisions

| ADR | Decision |
|---|---|
| [01](adr/01-adopt-spec-format-v2.md) | Adopt spec format version 2 |
| [02](adr/02-build-the-spec-pipeline-on-agentkit.md) | Build the spec pipeline on AgentKit |
| [03](adr/03-rebuild-the-skills-as-tools.md) | Rebuild the three skills as tools |
| [04](adr/04-implement-a-spec-as-a-tool.md) | Implement a spec as a tool: `impl` |
| [05](adr/05-write-every-scope-of-a-split.md) | Write every scope of a split, and record the split until it is done |
| [06](adr/06-version-the-envelope-interface.md) | Version the envelope interface: additive within a major version |

## Errata

Where the implementation diverges from a specification, or from what a reader
of the previous version would expect.

| Erratum | Subject |
|---|---|
| [04_ghapi_removed](errata/04_ghapi_removed.md) | Spec 04 asked for `internal/ghapi` to stay as a deprecated layer; it is deleted, and `issuex` is the only path to a forge |
| [11_preflight](errata/11_preflight.md) | Spec 11: `--dry-run` skips the forge checks under `--preflight`, `impl`'s estimate is zero phases with nothing pending, and the event stream has the baseline's `check` event |
| [06_summary_and_next](errata/06_summary_and_next.md) | Spec 06's summary view keeps the covered counts, `next[]` suggests `impl` on every package a split wrote, and the ambiguity entry equals `needs_human.resume` only when rendered |
| [04_issue_url_host](errata/04_issue_url_host.md) | Spec 04 asked the shell to build the forge client with `BaseURL` set to the issue URL's host; it passes the parsed repository, so the API host is derived (`api.github.com`, not `github.com`) |
| [unclassified_host_probe](errata/unclassified_host_probe.md) | `NewWithOptions` no longer probes a host whose name says neither `github` nor `gitlab` with the GitLab token; `03-REQ-1.4` is withdrawn |
| [issuex_shared_request_loop](errata/issuex_shared_request_loop.md) | The shared transport is one request loop both adapters run on, not an exported `Transport` they bypassed |
| [agentkit_model_resolution](errata/agentkit_model_resolution.md) | Bedrock refusal, Claude on Vertex, the extended-variant model, cache policy, forced tool calls |
| [google_function_response_references](errata/google_function_response_references.md) | Gemini resolves `$ref` inside a tool result; fixed in `agentkit-go`'s Google provider |
| [tool_schema_property_names](errata/tool_schema_property_names.md) | Why the generation tools do not declare the artifact's `$schema`, and who writes it |

## PRDs

[`docs/prds/`](prds/README.md) holds the product requirements documents this
repository was built from. Both predate ADR 03 and describe a CLI that no
longer exists; they are kept as history.

## Drafts

`docs/drafts/` holds PRD drafts that have been, or are about to be, handed to
`spec`. [`forge_neutral_issue_pr_client.md`](drafts/forge_neutral_issue_pr_client.md)
is the input the four `issuex` specs under `.specs/` were generated from.
