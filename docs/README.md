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
| [09](adr/09-grade-the-work-independently.md) | Grade the work independently of the phase that did it: `impl`'s conformance stage and `fix`'s proof |

## Errata

Where the implementation diverges from a specification, or from what a reader
of the previous version would expect.

| Erratum | Subject |
|---|---|
| [04_ghapi_removed](errata/04_ghapi_removed.md) | Spec 04 asked for `internal/ghapi` to stay as a deprecated layer; it is deleted, and `issuex` is the only path to a forge |
| [03_detect_scope](errata/03_detect_scope.md) | `detect.go` was changed outside spec 03's task list; the Bitbucket forge type it added is withdrawn, and `ErrUnsupportedForge` is now a registry miss |
| [03_retry_after_buffer](errata/03_retry_after_buffer.md) | The rate-limit backoff is the `Retry-After` the forge sent plus one second, for both adapters; spec 03's pseudocode expects it to equal the header |
| [11_preflight](errata/11_preflight.md) | Spec 11: `--dry-run` skips the forge checks under `--preflight`, `impl`'s estimate is zero phases with nothing pending and counts the conformance review, and the event stream has the baseline's `check` event |
| [06_summary_and_next](errata/06_summary_and_next.md) | Spec 06's summary view keeps the covered counts, `next[]` suggests `impl` on every package a split wrote, and the ambiguity entry equals `needs_human.resume` only when rendered |
| [13_navigation_baseline](errata/13_navigation_baseline.md) | Spec 13's navigation baseline runs `triage` where the spec says `issue` (the old name), and its tables have every row but no counts until a run with a live model fills them |
| [15_navigation_baseline](errata/15_navigation_baseline.md) | Spec 15's regenerated baseline tables have the `file_outline` and `find_symbol` columns but no before-and-after counts until a run with a live model fills them |
| [14_repo_map](errata/14_repo_map.md) | Spec 14's `--verbose` lives in the runner's Observer, not a `Runner.Verbose` field, and no corrupt source file can make `repomap.Build` fail, so TS-14-39 fails it with a cancelled context |
| [13_schema_maxitems](errata/13_schema_maxitems.md) | Spec 13 sets the `relevant_files` limit with a chained `MaxItemsN(30)` that `agentkit-go` does not have; the limit is set on the schema's `MaxItems` field and the rendered schema carries `"maxItems":30` |
| [04_issue_url_host](errata/04_issue_url_host.md) | Spec 04 asked the shell to build the forge client with `BaseURL` set to the issue URL's host; it passes the parsed repository, so the API host is derived (`api.github.com`, not `github.com`) |
| [unclassified_host_probe](errata/unclassified_host_probe.md) | `NewWithOptions` no longer probes a host whose name says neither `github` nor `gitlab` with the GitLab token; `03-REQ-1.4` is withdrawn |
| [issuex_shared_request_loop](errata/issuex_shared_request_loop.md) | The shared transport is one request loop both adapters run on, not an exported `Transport` they bypassed |
| [agentkit_model_resolution](errata/agentkit_model_resolution.md) | Bedrock refusal, Claude on Vertex, the `[1m]` long-context model id (the variant layer was removed), cache policy, forced tool calls |
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
