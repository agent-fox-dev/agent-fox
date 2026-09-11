# Erratum: `internal/ghapi` is removed rather than kept deprecated

Spec 04 (`issuex_agentfox_migration`), Requirement 9, asked for
`internal/ghapi` to be kept as a deprecated compatibility layer: type aliases
onto `issuex`, forwarding functions for the parsers, and the old `Client`
methods retained "to avoid breaking unmigrated branches during review". Test
TS-04-34 verified that layer. This erratum records that the layer is gone, and
why.

## What the spec left behind

**Was:** the package stayed, and so did a live path through it. The shared
shell in `internal/toolio` built a `ghapi.Client` on every run beside the
`issuex.Client` it had just built, and handed both to the tool as
`Deps.GitHub` and `Deps.Forge`. Every pipeline's `Options` carried a
deprecated `GitHub *ghapi.Client` next to `Forge`, and `codeimpl` still fell
back to `GitHub.CreatePullRequest` and counted `GitHub.Authenticated()` as a
credential when `Forge` was nil. `internal/ghapi/client.go` was a complete
second GitHub REST client — its own transport, its own pagination, its own
rate-limit retry, its own error type — and `ref.go` kept a private copy of the
issue-URL and remote parsers that the forwarding functions no longer called.

**Is:** `internal/ghapi` is deleted. `toolio.Deps` has one client, `Forge`;
each pipeline's `Options` has one client, `Forge`; `codeimpl` opens its pull
request through `Forge` alone. The only way an agent-fox tool reaches GitHub
or GitLab is `issuex`.

**Why:** the reason the spec gave for keeping the package — branches under
review that had not migrated — no longer holds: every branch of the split is
merged. What remained was a duplicate GitHub implementation that no tool was
supposed to use and that the shell nonetheless instantiated on every run, and
a fallback in `codeimpl` that would have silently opened a pull request on
GitHub for a repository the forge client had identified as GitLab. A
deprecated package that is still constructed is not a compatibility layer; it
is a second way of doing the thing the migration existed to make singular.

## What changes for a caller

- `toolio.Deps.GitHub`, `codefix.Options.GitHub`, `codeimpl.Options.GitHub`,
  `issuetriage.Options.GitHub` and `specgen.Options.GitHub` are gone. Set
  `Forge` instead; it takes any `issuex.Client`, including `issuex.NewNoOp()`
  for a run that must not touch a forge.
- `CategoryGitHub` stays in `codefix`, `codeimpl` and `issuetriage` as an
  alias of `CategoryForge` (`"forge"`), as spec 04's design decision 7 asks.
  The envelope's category for a refused forge call is `forge`, never
  `github`.
- TS-04-34 is removed with the package it tested. Nothing else in spec 04's
  test list depended on `internal/ghapi`.
- `cmd/fix`'s command-level test still refuses any `cmd/*` main that imports
  `internal/ghapi`; with the package gone that is a guard against the import
  path reappearing rather than a check on a live package.
