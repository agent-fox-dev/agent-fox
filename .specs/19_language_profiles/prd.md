---
spec_id: "19"
spec_name: "language_profiles"
title: "Language profiles: replace hardcoded language detection with a declarative profile table"
status: "active"
created_at: "2026-10-07T15:00:28.879856Z"
updated_at: "2026-10-07T15:00:28.879856Z"
intent_hash: "4d5dcb3505decc06a9010726cf8efe8311d44583f585a98949d2d712df2cc225"
schema_version: 2
source: "docs/prds/13-rebuild-fix-on-a-shared-change-engine.md"
---
## Intent

Extract every language-specific literal, command, convention and classifier scattered across `internal/project`, `internal/checks`, `internal/conform`, `codefix` and `codeimpl` into a single declarative profile table in a new `internal/lang` package, so that adding a language is adding a row and every mechanism in the tool reads the profile rather than hardcoding Go, Python, Rust or any other ecosystem.

## Goals

1. **One table, one truth.** A single exported `Profile` struct in `internal/lang` carries every language-specific fact: manifests, test/lint/format/build commands, targeted-test form, test-file classifier, inline-test flag, stub marker, skip markers, assertion patterns, read-root extractor, shell programs, caches, and version probe. No language literal exists outside this table and the tests that exercise it.
2. **Detection returns a set.** `Detect(root string) Detection` returns every profile whose manifest is present, with `Primary` as the first, and a `generic` fallback when no manifest matches. A `go.mod` beside a `package.json` is a Go-and-Node repository.
3. **Verification detection is unified.** One function on `Detection` resolves the verification command for both `fix` and `impl`: Makefile targets first, then the primary profile's commands, then nothing. Nothing-detected is a first-class outcome.
4. **The denylist test passes.** A test in `internal/lang` walks every non-test `.go` file under `internal/engine` (when it exists), `codefix` and `codeimpl` and fails on any language literal from a denylist (`.go"`, `go.mod`, `gofmt`, `go test`, `go vet`, `_test.go`, `panic("not`, `pytest`, `cargo`, `npm`, `.py"`, `.ts"`, `.rs"`). This test is the acceptance gate for goal 1.
5. **Existing callers migrate.** `internal/project.DetectProfile` is replaced by `lang.Detect`, and every call site in `codefix`, `codeimpl`, `internal/checks`, and `internal/conform` that reads `project.Profile` fields or calls `project.IsTestPath` / `project.IsSourceFile` / `project.ReadRoots` uses the `lang` package instead. `internal/project` retains only what `specgen` and `afspec` need (docs helpers, steering) until a later spec retires it.

## Non-goals

- **Shared change engine** (`engine_core`): the `internal/engine` package with the ledger, gate cache, parking and branching is a separate scope.
- **Prompt documents** (`prompt_documents`): moving prompts from Go string constants to embedded Markdown templates is a separate scope.
- **Polyglot structural checks** (`polyglot_structural_checks`): making `internal/conform` read its formatters, assertion patterns and function-span logic from the profile is a separate scope that builds on this one.
- **Rewriting codefix on the engine** (`codefix_on_engine`): the pipeline rewrite is a separate scope.
- **`run_checks` tool** (`run_checks_tool`): the model-facing verification tool is a separate scope.
- A language server, a type checker per language, or exact parity between languages in what the structural checks can see.
- Changing the spec format, `afspec`, `triage` or `spec`.

## Background

### What exists today

Language detection lives in `internal/project/project.go` as `DetectProfile`. It returns a single `Profile` struct with fields `Language`, `AllTests`, `Linter`, `Manifest`, `StubMarker`, and `Also` (other ecosystems present). The function is a large switch statement over manifest files (`go.mod`, `Cargo.toml`, `pyproject.toml`, `package.json`, etc.) that hardcodes every command.

Test-path classification is `project.IsTestPath` in `internal/project/docs.go` — a function that checks file suffixes and directory names for all languages in one place, with no per-language customisation.

Read roots are in `internal/project/readroots.go` as `ReadRoots`, which handles `go.mod` replace directives, `package.json` file/link dependencies, `Cargo.toml` path dependencies, and `requirements.txt` local paths.

Verification command detection is in `internal/checks/checks.go` as `Detect`, which reads the Makefile and then falls back to `project.DetectProfile(dir).AllTests`. This is separate from `codeimpl`'s `gateCommands` which reads from the spec's `TestCommands`.

The structural checks in `internal/conform/structural.go` split files into `goFiles` and `otherFiles` with a hardcoded `.go` suffix check, then call `scanGoFile` (using `go/ast`) or `scanSourceFile` (using regex-based heuristics from `polyglot.go`). The assertion patterns, function-span finders, and formatter lists are all hardcoded in `polyglot.go`.

The shell guard in `internal/agentrun/guard.go` has `ReadOnlyPrograms` and `BuildPrograms` as hardcoded slices. The runner families map in `project.go` maps program names to ecosystems.

### Why this matters

The input PRD's analysis (§1) documents that Go is the only language for which every mechanism fires correctly. The profile table is the foundational piece: every later scope (the engine, the prompt documents, the structural checks, the `codefix` rewrite) depends on having a single, testable source of language facts.

## Requirements

### Profile structure

The `internal/lang` package exports a `Profile` struct with these fields:

- `Name` (string): the ecosystem identifier (`"go"`, `"python"`, `"node"`, `"rust"`, `"jvm"`, `"dotnet"`, `"ruby"`, `"php"`, `"elixir"`, `"swift"`, `"generic"`).
- `Manifests` ([]string): files whose presence selects this profile.
- `Test`, `Lint`, `Format`, `Build` (each a `Command`): the ecosystem's default commands, each an argv split without a shell. Each `Command` also carries a variant to prefer when a lockfile or wrapper is present (e.g. `uv.lock` → `uv run pytest`, `gradlew` → `./gradlew test`).
- `Targeted` (Command): how to run one test file or one test name. Empty when the ecosystem has none.
- `Programs` ([]string): the toolchain binaries the implementing shell may run.
- `TestFile` (func(path string) bool): whether a path is a test by this language's conventions.
- `InlineTests` (bool): whether the language keeps tests inside implementation files (Rust `#[cfg(test)]`, Elixir doctests).
- `TestID` (*regexp.Regexp): matches a spec test id in a test's source.
- `Stub` (string): the idiom for "not implemented".
- `SkipMarkers` (*regexp.Regexp): the ways a test is disabled.
- `AssertPattern` (*regexp.Regexp): the assertion pattern for the no-assertions check.
- `GateConfig` (func(path string) bool): whether a path is a gate configuration file.
- `ReadRoots` (func(root string) []ReadRoot): the local directories this ecosystem's manifest points dependencies at.
- `Caches` ([]string): environment variable names for download caches.
- `Version` (Command): the probe for the environment fingerprint.

The `Command` type holds an argv slice, a variant selector (lockfile or wrapper presence), and a method to resolve the actual argv given a root directory.

### Detection

`Detect(root string) Detection` scans the root for every profile whose manifest is present, in a fixed precedence order matching today's `manifestFamilies`. `Detection` is a set:

- `Primary` is the first matched profile.
- `All` is every matched profile, in precedence order.
- A repository with no manifest gets the `generic` profile: no toolchain, Makefile targets only.
- `Programs()` returns the union of every detected profile's `Programs`.
- `TestFile(path)` returns true if any detected profile's `TestFile` returns true.
- `IsSourceFile(path)` returns true if the path has a source-code extension recognised by any detected profile.

### Verification detection

`Detection` has a method `VerifyCommand(root string) (test string, lint string)` that resolves the verification commands:

1. A `Makefile` target `check`, else `test` (for the test command); a `Makefile` target `lint` (for the linter).
2. Else, for the primary profile, its `Test` command in the lockfile variant, and its `Lint` command when its configuration file is present.
3. Else empty strings.

This replaces both `checks.Detect` and the command-resolution logic in `project.DetectProfile`. `checks.Detect` is updated to delegate to `lang.Detect(dir).VerifyCommand(dir)` and return the test command, preserving its existing signature for callers that have not migrated.

### Read roots

`Detection.ReadRoots(root string) []ReadRoot` returns the union of every detected profile's read roots, deduplicated by absolute path, matching the current `project.ReadRoots` behaviour. The `ReadRoot` type is moved from `internal/project` to `internal/lang`.

### Test classification

`Detection.TestFile(path string) bool` replaces `project.IsTestPath`. Each profile's `TestFile` function encodes that language's conventions (e.g. Go: `_test.go`; Python: `test_` prefix or `_test.py` suffix; JS/TS: `.test.` or `.spec.`; Rust: `_test.rs`; etc.). The detection's classifier is the union: a path is a test if any detected profile says so. Common directory conventions (`testdata/`, `test/`, `tests/`, `__tests__/`, `spec/`) are checked by every profile.

### Runner families and audit

The `runnerFamilies` map and `AuditTasks` / `AuditTestCommands` methods move from `internal/project` to `internal/lang`, operating on `Profile` and `Detection` instead. The `LanguageBlock()` method moves as well.

### Migration of callers

Every import of `internal/project` for `DetectProfile`, `IsTestPath`, `IsSourceFile`, `ReadRoots`, `TargetedRun`, `LanguageBlock`, `AuditTasks`, `AuditTestCommands`, or `runnerFamilies` is updated to use `internal/lang`. The `internal/project` package retains:

- `Steering(specsDir string) string`
- `MissingDocs(root string, changed []string) string`
- `IsDocsFile(p string) bool`
- `ProjectInstructions(root string) string` (the AGENTS.md / CLAUDE.md reader, currently `projectInstructions` in `codefix/prompts.go` — it stays where it is until the engine scope moves it)

Functions in `internal/project` that are only used by the migrated detection (`nodeRunner`, `nodeCommands`, `makeTarget`, `manifestFamilies`, `hasGlob`, `flutterRe`) move to `internal/lang`.

### The denylist test

A test `TestNoLanguageLiterals` in `internal/lang` walks every `.go` file (excluding `_test.go` files) under `internal/lang` itself (to verify the table is the only place), and under `codefix` and `codeimpl`, and fails if any line contains a string from the denylist: `.go"`, `"go.mod"`, `gofmt`, `"go test"`, `"go vet"`, `_test.go"`, `panic("not implemented")`, `"pytest"`, `"cargo "`, `"npm "`, `.py"`, `.ts"`, `.rs"`. The denylist also runs over any `internal/engine` directory when it exists. Exceptions are allowed only for the `internal/lang` package's own profile definitions and for import paths.

When this spec lands, the denylist test will initially fail for existing hardcoded literals in `codefix` and `codeimpl`. Those callers are migrated as part of this spec: every reference to a language-specific literal in `codefix` and `codeimpl` is replaced by a reference to the profile. The `internal/conform` package is not migrated in this scope (that is `polyglot_structural_checks`), so the denylist test does not walk `internal/conform`.

### Error handling

- `Detect` never fails: a directory that cannot be read returns the `generic` profile.
- `ReadRoots` returns an empty slice when the manifest cannot be parsed.
- `Command.Resolve(root string)` returns the base command when the lockfile/wrapper check fails.

### Backward compatibility

- `project.DetectProfile` is kept as a thin wrapper over `lang.Detect` that returns the old `project.Profile` shape, so callers outside the migrated set (if any) continue to compile. It is marked deprecated.
- `checks.Detect` delegates to `lang` internally but keeps its signature.
- No flag, exit code, envelope field or schema version changes.

## Design Decisions

1. **`Command` is a struct, not a string.** The input PRD specifies commands as `Command` types with lockfile variants. Today they are plain strings split on whitespace. A struct with `Base []string`, `LockfileVariant` and `Resolve(root) []string` lets the profile carry the variant logic (e.g. prefer `uv run pytest` when `uv.lock` exists) without the caller knowing the lockfile names. This is cleaner than the current `for _, l := range []struct{ lock, run string }{...}` pattern in `DetectProfile`.

2. **The denylist test walks `codefix` and `codeimpl` but not `internal/conform`.** The input PRD says the denylist covers `internal/engine`, `codefix` and `codeimpl`. Since `internal/conform` has extensive language-specific logic (assertion patterns, formatter lists, function-span finders) that is migrated in a separate scope (`polyglot_structural_checks`), including it in the denylist now would force that scope into this one. The denylist is extended to cover `internal/conform` when that scope lands.

3. **`internal/project` is not deleted.** It retains `Steering`, `MissingDocs`, `IsDocsFile`, and the AGENTS.md reader. These are not language-specific and are used by `specgen` and other packages. The input PRD says `internal/project` is retired when PRD 14 lands; this scope does not retire it.

4. **Detection is a set, not a single profile.** The input PRD specifies this. Today `DetectProfile` returns one profile with an `Also []string` field for secondary ecosystems. The new `Detection` type carries full `Profile` values for every detected ecosystem, which lets callers get the union of programs, test classifiers, and read roots without re-detecting.

5. **`generic` profile for unknown repositories.** A repository with no recognised manifest gets a `generic` profile with empty commands and no toolchain programs. This matches the current behaviour where `DetectProfile` returns an empty `Profile{}` with `Known() == false`.

6. **`text/template` is not used in this scope.** The input PRD specifies `text/template` for the prompt documents. This scope is about the data table, not the rendering. The prompt-document scope will use the profile data through views.

7. **The `TestFile` function replaces `IsTestPath` but keeps the same conventions.** The per-profile `TestFile` functions encode the same rules currently in `project.IsTestPath` (suffix checks, directory conventions), split by language. The detection's union classifier produces the same results as the current single function for any repository the current code handles.

8. **`InlineTests` is a boolean on the profile.** The input PRD describes inline tests for Rust (`#[cfg(test)]`), Python doctests, and Elixir doctests. For this scope, the field is set on the profile; the revert-check logic that uses it (reverting by declaration rather than by file) is part of the engine scope.

9. **Profiles cover the same languages as today plus `generic`.** The current `DetectProfile` handles: go, rust, python, node, jvm (Maven and Gradle), dotnet, ruby, elixir, php, dart, swift, cpp. The new table covers all of these. The input PRD's list omits dart and cpp but includes them in the manifest list; they are kept for backward compatibility.

10. **`AssertPattern` and `GateConfig` are on the profile but consumed by `internal/conform` in a later scope.** This scope defines them; the `polyglot_structural_checks` scope wires them into the structural scan.

## Dependencies

| Spec | Reason |
|---|---|
| None | This is a foundational scope with no dependencies on existing specs. |

## Verified External API

This scope uses only the Go standard library (`os`, `path/filepath`, `regexp`, `strings`, `encoding/json`, `slices`, `sort`) and types already defined in the repository (`internal/gitx`, `afspec`). No external packages are introduced.

| Symbol | Package | Signature | Source |
|---|---|---|---|
| `embed.FS` | `embed` (stdlib) | N/A — not used in this scope | N/A |
| `regexp.MustCompile` | `regexp` (stdlib) | `func MustCompile(str string) *Regexp` | stdlib |
