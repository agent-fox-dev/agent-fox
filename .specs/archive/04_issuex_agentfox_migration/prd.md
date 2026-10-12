---
spec_id: "04"
spec_name: "issuex_agentfox_migration"
title: "Issuex Migration and GHAPI Deprecation"
status: "active"
created_at: "2026-09-10T17:12:46.903801Z"
updated_at: "2026-09-10T17:12:46.903801Z"
intent_hash: "dc9195679695928ffa6a412d5956f2fe1b5db72ca06562bd7d30e1ab5112df07"
schema_version: 2
source: "docs/drafts/forge_neutral_issue_pr_client.md"
---
## Intent

Refactor agent-fox commands, pipeline runners, and internal input utilities to replace the GitHub-specific `internal/ghapi` package with the forge-neutral `issuex` package, deprecating `internal/ghapi` while enabling full issue and pull request operations across GitHub and GitLab.

## Goals

- Replace all usages of `internal/ghapi` across `agent-fox` (`internal/toolio`, `codefix`, `codeimpl`, `issuetriage`, `specgen`, and `cmd/{fix,impl,issue,spec}`) with `issuex`.
- Support GitLab issue and merge request URLs (including nested group paths like `group/subgroup/project`) as first-class inputs across all agent-fox tools alongside GitHub URLs.
- Update pipeline options and shared application dependencies to accept `issuex.Client` via a forge-neutral `Forge` field instead of a GitHub-specific client.
- Update repository target resolution in CLI tools to parse multi-segment project paths via `issuex.ParseRepo` and detect origin remotes via `issuex.DetectRepo`.
- Deprecate `internal/ghapi` with standard Go deprecation notices, aliasing core types to `issuex` to maintain backwards compatibility during the transition.
- Ensure preflight authentication checks and error messages cleanly guide users toward appropriate GitHub (`GITHUB_TOKEN`, `GH_TOKEN`) or GitLab (`GITLAB_TOKEN`) credentials.
- Update all existing test suites to use `issuex` client instances and mock HTTP test servers.

## Non-goals

- Implementing the core `issuex` interface, domain data models, or no-op client (handled in foundational spec `01_issuex_core`).
- Implementing the GitHub REST adapter in `issuex` (handled in spec `02_issuex_github`).
- Implementing the GitLab REST adapter in `issuex` (handled in spec `03_issuex_gitlab`).
- Adding support for forges other than GitHub and GitLab (such as Bitbucket or Azure DevOps).
- Modifying Git command line operations in `internal/gitx` (such as branch checkout, commit, push, or diff generation).
- Supporting automated forge token generation or OAuth browser flows.

## Background

Today, `agent-fox` tools (`fix`, `impl`, `issue`, and `spec`) interact with GitHub through `internal/ghapi`. Because `internal/ghapi` is GitHub-specific and hardcoded to GitHub's REST v3 schema, it cannot parse GitLab URLs, resolve GitLab nested project paths, or interact with GitLab merge requests. Furthermore, because it is located in `internal/`, sibling ecosystem projects (`hub` and `coder`) cannot import it.

The preceding specs in this split introduced the public `issuex` package:
- `01_issuex_core` defined the unified `Client` interface, domain types (`Repo`, `IssueRef`, `Issue`, `PullRequest`, etc.), URL parsing functions, standardized error classification, and `NoOpClient`.
- `02_issuex_github` implemented the GitHub REST adapter conforming to `issuex.Client`.
- `03_issuex_gitlab` implemented the GitLab REST v4 adapter conforming to `issuex.Client`.

With all client adapters in place, `agent-fox` must now be migrated away from `internal/ghapi` to use `issuex`, standardizing all CLI commands, pipeline runners, and input handling on the forge-neutral abstraction.

## Requirements

### Requirement 1: toolio Input Resolution and Forge-Neutral Issue Parsing
The `internal/toolio` package shall use `issuex` for URL parsing and issue thread representations:
- In `internal/toolio/input.go`, replace `KindIssue SourceKind = "github"` with `KindIssue SourceKind = "issue"`, representing an issue or pull/merge request on any supported forge.
- Update `Input` struct fields:
  - `Issue *issuex.IssueRef` (replacing `*ghapi.IssueRef`).
  - `Thread *issuex.IssueThread` (replacing `*ghapi.Thread`).
- Update `Resolve(ctx context.Context, arg string, stdin io.Reader, forge issuex.Client) (Input, error)` to accept `issuex.Client` rather than `*ghapi.Client`.
- In `Resolve`, use `issuex.ParseIssueURL(arg)` to recognize web issue and pull/merge request URLs for both GitHub (`https://{host}/{owner}/{repo}/issues/{id}`, `/pull/{id}`) and GitLab (`https://{host}/{group}/{project}/-/issues/{id}`, `/-/merge_requests/{id}`).
- If `issuex.ParseIssueURL` recognizes the input, call `resolveIssue`:
  - If the provided `forge` client is `nil`, return an error indicating that no forge client is configured.
  - Call `forge.ReadIssue(ctx, ref)` to fetch the issue thread.
  - If reading the issue fails with an HTTP 404 and the client is unauthenticated (`forge.Authenticated() == false`), propagate the error annotating that the issue or project may be private and require credentials.
  - Set `ref.IsPullRequest = ref.IsPullRequest || thread.Issue.IsPR`.
  - Truncate the rendered thread using `Truncate` and return an `Input` with `Kind: KindIssue`, `Origin: ref.URL()`, `Body: body`, `Truncated: cut || thread.Truncated`, `Issue: &ref`, and `Thread: &thread`.

### Requirement 2: toolio Thread Rendering and Prompt Generation
The `RenderThread` function in `internal/toolio/input.go` shall produce deterministic prompt text tailored to the forge platform:
- Update signature to `RenderThread(ref issuex.IssueRef, t issuex.IssueThread) string`.
- Determine forge branding from `ref.Repo.Host`:
  - If the host contains `gitlab`, format the opening line as `GitLab <kind> <ref> (state: <state>)`.
  - If the host contains `github` or is empty, format as `GitHub <kind> <ref> (state: <state>)`.
  - For other hosts, format as `Forge <kind> <ref> (state: <state>)`.
- The `<kind>` string shall be `"pull request"` if `ref.IsPullRequest` is true and `"issue"` otherwise.
- Output the thread metadata in order:
  - Title: `Title: <t.Issue.Title>`
  - Author: `Author: <t.Issue.Author.Login>`
  - Labels: `Labels: <comma-separated list>` (omitted if `t.Issue.Labels` is empty)
  - Issue Body: separated by blank lines and trimmed.
  - Comments: for each comment in `t.Comments`, append `\n--- comment by <comment.User.Login> ---\n<comment.Body>\n`.
  - Truncation marker: if `t.Truncated` is true, append `\n[... further comments not read ...]\n`.

### Requirement 3: toolio App Shell and Client Factory Integration
The `internal/toolio` application shell shall manage forge client construction and lifecycle:
- In `internal/toolio/app.go`, update `Deps`:
  - Replace `GitHub *ghapi.Client` with `Forge issuex.Client`.
- In `App.execute`:
  - First check if the input argument matches an issue URL via `issuex.ParseIssueURL(e.argument)`. If recognized and `ref.Repo.Host` is known, attempt to construct an `issuex.Client` targeted to that host via `issuex.NewWithOptions(issuex.Options{BaseURL: "https://" + ref.Repo.Host, UserAgent: a.Name + "/" + a.Version})`.
  - If the argument is not an issue URL or host-specific creation returns an error, attempt to construct the forge client using `issuex.New(a.Name + "/" + a.Version)`.
  - If `issuex.New` returns an error (such as `issuex.ErrAmbiguousForge` or unconfigured remote), fall back to `issuex.NewNoOp()`. This ensures that tools executing against local files or raw text succeed without requiring forge credentials or remotes.
  - Pass the instantiated client into `Resolve(ctx, e.argument, e.stdin, forge)`.
  - If `in.Thread != nil && in.Thread.CommentsErr != nil`, log a warning on `e.run`: `"the issue's comments could not be read: %v"`.
  - Pass `Forge: forge` in `Deps` when invoking `a.Exec`.

### Requirement 4: Codefix Pipeline Runner Migration
The `codefix` package shall migrate all forge interactions to `issuex`:
- In `codefix/types.go`, update `issueRef`: `type issueRef = *issuex.IssueRef`.
- In `codefix/pipeline.go`, update `Options`:
  - Replace `Repo ghapi.Repo` with `Repo issuex.Repo`.
  - Replace `GitHub *ghapi.Client` with `Forge issuex.Client`.
- Define `const CategoryForge = "forge"` and alias `const CategoryGitHub = CategoryForge` for backwards compatibility.
- In `preflight`:
  - Detect repository via `issuex.DetectRepo(o.Workspace.Root)` when `target` is not valid.
  - If `o.Input.Issue != nil` and `target` is not valid, default `target` to `o.Input.Issue.Repo`.
  - Check credentials: if `!o.DryRun && (o.Input.Issue != nil || o.Land == LandPR)` and `!o.Forge.Authenticated()`, return an auth failure naming `GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN`.
  - If `o.Land == LandPR && !o.DryRun && !target.Valid()`, return a usage failure: `"--land=pr needs a target repository: <root> has no origin remote on a recognized forge and the input is not an issue URL — pass --repo owner/repo, or --land=branch"`.
- In `openPullRequest`:
  - Call `o.Forge.CreatePullRequest(ctx, target, issuex.CreatePullRequestRequest{ Title: pullRequestTitle(...), Body: pullRequestBody(...), Head: branch, Base: base, Draft: o.Draft })`.
  - On success, populate `result.PullRequestURL = pr.URL` and `result.PullRequestNumber = pr.Number`.
- In `postComment`:
  - Call `url, err := o.Forge.AddComment(ctx, *o.Input.Issue, body)`.
  - On success, append `url` to `result.Comments`.

### Requirement 5: Codeimpl Pipeline Runner Migration
The `codeimpl` package shall migrate all forge interactions to `issuex`:
- In `codeimpl/types.go`:
  - Replace `Repo ghapi.Repo` with `Repo issuex.Repo`.
  - Replace `GitHub *ghapi.Client` with `Forge issuex.Client`.
  - Define `const CategoryForge = "forge"` and alias `const CategoryGitHub = CategoryForge`.
- In `codeimpl/pipeline.go`:
  - In `preflight`: detect repository via `issuex.DetectRepo(st.root)`.
  - Check credentials: if `o.Land == LandPR && !o.DryRun && !o.Forge.Authenticated()`, return an auth failure naming `GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN`.
  - Check target: if `o.Land == LandPR && !o.DryRun && !st.target.Valid()`, return a usage failure indicating no origin remote on a recognized forge.
  - In `Run`: call `o.Forge.CreatePullRequest(ctx, st.target, issuex.CreatePullRequestRequest{ Title: pullRequestTitle(st.spec), Body: pullRequestBody(result), Head: st.branch, Base: st.base, Draft: o.Draft })`.
  - On success, populate `result.PullRequestURL = pr.URL` and `result.PullRequestNumber = pr.Number`.

### Requirement 6: Issuetriage Pipeline Runner Migration
The `issuetriage` package shall migrate all issue reading and filing operations to `issuex`:
- In `issuetriage/pipeline.go`, update `Options`:
  - Replace `Repo ghapi.Repo` with `Repo issuex.Repo`.
  - Replace `GitHub *ghapi.Client` with `Forge issuex.Client`.
- In `resolveTarget`:
  - Detect repository via `issuex.DetectRepo(o.Workspace.Root)` if `o.Repo` and `o.Input.Issue` are unset.
  - If resolution fails and `!o.DryRun`, return usage failure: `"no target repository: <root> has no origin remote on a recognized forge and the input is not an issue URL — pass --repo owner/repo, or --dry-run to print the diagnosis"`.
- In `checkWriteCredential`:
  - If `o.Forge == nil`, return an internal failure `"no forge client configured"`.
  - If `!o.Forge.Authenticated()`, return an auth failure: `"<action> in <target> needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run"`.
- In `write`:
  - If `o.Overwrite` is true, call `o.Forge.UpdateIssue(ctx, *o.Input.Issue, issuex.UpdateIssueRequest{ Title: out.Title, Body: out.Body })`.
  - If `o.Overwrite` is false, call `o.Forge.CreateIssue(ctx, target, issuex.CreateIssueRequest{ Title: out.Title, Body: out.Body, Labels: o.Labels })`.
  - If `err != nil`, inspect error via `issuex.IsNoToken(err)`: return auth failure if true, or forge failure `fail("write", CategoryForge, err)` otherwise.
  - Populate `out.URL = issue.URL` and `out.Number = issue.Number`.

### Requirement 7: Specgen Pipeline Runner Migration
The `specgen` package shall migrate all issue commenting operations to `issuex`:
- In `specgen/pipeline.go`, update `Options`:
  - Replace `GitHub *ghapi.Client` with `Forge issuex.Client`.
- In preflight validation:
  - If `o.Comment && !o.DryRun` and `(o.Forge == nil || !o.Forge.Authenticated())`, return an auth failure: `"--comment needs a forge credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or run without --comment"`.
- In PRD phase comment posting:
  - When `o.Comment && o.Input.Issue != nil && !o.DryRun`, call `url, err := o.Forge.AddComment(ctx, *o.Input.Issue, body)`.
  - If comment posting fails, record a warning on `o.Run` without failing the pipeline run.

### Requirement 8: CLI Commands Migration (cmd/fix, cmd/impl, cmd/issue, cmd/spec)
All CLI entry points shall parse repository arguments and dispatch dependencies through `issuex`:
- In `cmd/fix/main.go`:
  - Parse `--repo` with `issuex.ParseRepo(repo)`.
  - Pass `Forge: d.Forge` into `codefix.Options`.
  - Update usage text to describe the argument as a GitHub or GitLab issue or pull/merge-request URL.
- In `cmd/impl/main.go`:
  - Parse `--repo` with `issuex.ParseRepo(repo)`.
  - Pass `Forge: d.Forge` into `codeimpl.Options`.
  - Update usage text.
- In `cmd/issue/main.go`:
  - Parse `--repo` with `issuex.ParseRepo(repo)`.
  - Pass `Forge: d.Forge` into `issuetriage.Options`.
  - Update usage and preflight text to refer to forge issue URLs.
- In `cmd/spec/main.go`:
  - Pass `Forge: d.Forge` into `specgen.Options`.
  - Update usage text to refer to GitHub or GitLab issue URLs.

### Requirement 9: Deprecation and Compatibility Layer for internal/ghapi
The `internal/ghapi` package shall be marked deprecated and backed by `issuex`:
- Add `// Deprecated: use github.com/agent-fox-dev/agentfox/issuex instead.` to `internal/ghapi/doc.go` and package-level documentation.
- Update `internal/ghapi/ref.go` to provide type aliases and forwarding functions:
  - `type Repo = issuex.Repo`
  - `type IssueRef = issuex.IssueRef`
  - `func ParseRepo(s string) (Repo, bool) { return issuex.ParseRepo(s) }`
  - `func ParseRemote(remote string) (Repo, bool) { return issuex.ParseRemote(remote) }`
  - `func DetectRepo(dir string) (Repo, bool) { return issuex.DetectRepo(dir) }`
  - `func ParseIssueURL(s string) (IssueRef, bool) { return issuex.ParseIssueURL(s) }`
- Mark all forwarded functions and types with `// Deprecated: ...` annotations.
- Retain existing `Client` methods in `internal/ghapi/client.go` with deprecation notices to avoid breaking unmigrated branches during review.

### Requirement 10: Test Suite Migration and Verification
All package test suites shall be updated to test against `issuex`:
- Update `internal/toolio/input_test.go`:
  - Use `issuex.NewWithOptions` or `issuex.NewNoOp()` for test inputs.
  - Verify that both GitHub and GitLab issue URLs resolve to `KindIssue` with correct `IssueRef` and parsed fields.
  - Verify deterministic thread rendering for GitHub and GitLab formats.
- Update `codefix/pipeline_test.go`:
  - Inject an `issuex.Client` pointing at the mock `httptest.Server`.
  - Verify pull request creation and comment posting against the forge client.
- Update `issuetriage/triage_test.go`:
  - Inject an `issuex.Client` pointing at the test server.
  - Verify issue creation and issue updating mutations.
- Ensure all tests across `make test` and linter checks across `make lint` pass without errors or unhandled warnings.

## Design Decisions

1. **Rename `GitHub` field to `Forge` in pipeline options and `toolio.Deps`**: `Forge` is neutral across GitHub and GitLab, aligns with `issuex.ForgeClient`, and pairs cleanly with `Git *gitx.Git` in pipeline options.
2. **Standardize `KindIssue` string value to `"issue"`**: Setting `KindIssue SourceKind = "issue"` ensures that JSON envelopes for GitLab issue inputs do not misleadingly report `"kind": "github"`.
3. **Fallback to `NoOpClient` in `toolio.App.execute`**: When forge detection is ambiguous or no remote is configured, the application falls back to `issuex.NewNoOp()` rather than failing immediately, allowing local file and text operations to run without requiring forge credentials.
4. **Issue URL priority in client construction**: If the CLI input argument is recognized by `issuex.ParseIssueURL`, `execute` targets the specific forge host extracted from the URL, allowing cross-forge triage without relying on local git configuration.
5. **Dynamic thread rendering in `RenderThread`**: Prompt generation examines `ref.Repo.Host` to label the thread as `GitHub`, `GitLab`, or `Forge`, preventing language models from being confused by mismatched platform conventions.
6. **Support multi-segment GitLab group paths in `--repo`**: Adopting `issuex.ParseRepo` allows all CLI commands (`fix`, `impl`, `issue`) to parse nested GitLab paths (e.g. `gitlab-org/subgroup/project`) seamlessly.
7. **Unified `CategoryForge` failure category with `CategoryGitHub` alias**: Pipeline failures communicating with the forge return category `"forge"`, while preserving `CategoryGitHub = "forge"` to maintain backwards compatibility with existing error checks.
8. **Credential guidance listing both GitHub and GitLab tokens**: Preflight auth error messages explicitly mention `GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN` to give clear troubleshooting guidance across platforms.
9. **Retain deprecated type aliases in `internal/ghapi`**: Aliasing `ghapi.Repo` and `ghapi.IssueRef` to `issuex` minimizes churn and ensures existing tests and external branches compile cleanly.
10. **Single unified migration spec**: Refactoring all consumer packages and CLI tools in one cohesive migration step prevents broken intermediate states where half the codebase uses `ghapi` and half uses `issuex`.

## Dependencies

| Spec | Reason |
|---|---|
| `01_issuex_core` | Foundational forge client interface, domain data models, URL and git remote parsing, error taxonomy, and NoOpClient. |
| `02_issuex_github` | GitHub REST client adapter implementing `issuex.Client` for GitHub and GitHub Enterprise instances. |
| `03_issuex_gitlab` | GitLab REST client adapter implementing `issuex.Client` for GitLab and self-hosted GitLab instances. |
