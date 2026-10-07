---
spec_id: "22"
spec_name: "polyglot_structural_checks"
title: "Polyglot structural checks: wire internal/conform to read from lang.Profile"
status: "active"
created_at: "2026-10-07T15:53:26.618774Z"
updated_at: "2026-10-07T15:53:26.618774Z"
intent_hash: "10b11fd80f483e474320491ef279adcd5e45c471261fd53e5eb27f25b966dcd9"
schema_version: 2
source: "docs/prds/13-rebuild-fix-on-a-shared-change-engine.md"
---
## Intent

Wire `internal/conform`'s structural checks — formatting, long functions, assertion-less tests, the revert check's file classification, the errata citation's test-reference pattern, the duplicate-detection classifier, and the gate-edit detectors — to read from `lang.Profile`'s assertion patterns, formatter commands, function-span logic, skip markers, gate-config classifier, and test-file classifier, so that every language gets the same mechanism with its own conventions and no language literal remains hardcoded in `internal/conform`.

## Goals

1. **Formatting checks read the profile.** The `formatters` table in `polyglot.go` and the `gofmt`/`vet` functions in `structural.go` are replaced by a single path that runs the profile's `Format` and `Lint` commands over the changed files, parsing findings by a per-profile output regex. A profile with no formatter produces no formatting finding.
2. **Assertion checks read the profile.** The hardcoded `pyAssertRe`, `jsAssertRe`, `rustAssertRe` patterns in `polyglot.go` and the `go/ast`-based `asserts` function in `structural.go` are replaced by a dispatch that uses the profile's `AssertPattern` for regex-based languages and `go/ast` for Go. The no-assertions check fires for every language that has an `AssertPattern` or a Go AST backend.
3. **Function-span detection is per-profile.** The `sourceSpans` dispatch in `polyglot.go` (which switches on file extension) is replaced by a dispatch on the detected profile. The existing span finders (`pythonSpans`, `jsSpans`, `rustSpans`, `braceSpans`) remain as backends; the profile selects which one runs. A language whose backend gives no end lines gets no long-function finding.
4. **The revert check uses the profile's test classifier.** `splitChange` in `revert.go` replaces `project.IsTestPath` with the `lang.Detection.TestFile` passed via `ScanInput` or a new parameter. The Rust inline-test handling (`keepRustTests`) is guarded by `Profile.InlineTests` rather than a hardcoded `.rs` suffix check.
5. **Every `project.IsTestPath`, `project.IsSourceFile` and `project.IsDocsFile` call in `internal/conform` is replaced.** `project.IsTestPath` → `lang.Detection.TestFile`; `project.IsSourceFile` → `lang.Detection.IsSourceFile`; `project.IsDocsFile` stays (it is not language-specific and remains in `internal/project`).
6. **The denylist test covers `internal/conform`.** The `TestNoLanguageLiterals` test in `internal/lang` is extended to walk `internal/conform`'s non-test `.go` files, so no language literal can hide in the structural checks.
7. **Every finding carries `Backend` and `Profile`.** The `Finding` struct gains `Backend string` (e.g. `"go/ast"`, `"regex"`, `"rustfmt"`, `"ruff"`) and `Profile string` (e.g. `"go"`, `"python"`), so a reviewer can see what judged each finding.

## Non-goals

- **Targeted revert check.** Running the profile's `Targeted` command instead of the full suite during the revert check is part of `codefix_on_engine`, not this scope. This scope makes `splitChange` and `keepRustTests` profile-aware; the targeted-run wiring is later.
- **Revert by declaration for inline tests.** Reverting by declaration line ranges (for Rust `#[cfg(test)]` modules) rather than by file is part of `codefix_on_engine`.
- **Moving `gateConfigFile`, `testFile`, `goldenFile`, `skipMarker` to the engine.** The `20_engine_core` spec moves these from `codeimpl/gateedits.go` to the engine's ledger. This scope does not duplicate that move; it makes the classifiers in `internal/conform` use the profile.
- **New flags, exit codes, or envelope fields.** The `Backend` and `Profile` fields on `Finding` are additive and do not change the schema version.
- **A language server or type checker per language.** Equality means the same mechanism runs for every language with that language's own conventions; Go keeps what `go/ast` gives it.
- **Changing the `Scan` function's signature in a breaking way.** Callers in `codefix` and `codeimpl` must continue to compile. The `ScanInput` struct gains a field; existing callers that do not set it get the current behaviour.

## Background

### What exists today

`internal/conform/structural.go` splits changed files into `goFiles` and `otherFiles` with a hardcoded `strings.HasSuffix(p, ".go")` check (L101), then calls `scanGoFile` (using `go/ast`) for Go files and `scanSourceFile` (using regex heuristics from `polyglot.go`) for everything else. The file-type classification uses `project.IsSourceFile` (L103), which is a hardcoded extension map in `internal/project/docs.go`.

`internal/conform/polyglot.go` has:
- `sourceSpans` (L85–102): a switch on file extension (`.py`, `.js`/`.ts`, `.rs`, brace-based) that selects the span finder and assertion pattern. Each branch returns a hardcoded `*regexp.Regexp` for assertions.
- `formatters` (L264–274): a table of `{exts, tools}` that maps file extensions to formatter commands (`rustfmt`, `ruff`/`black`, `prettier`). Each tool has a hardcoded argv, output-line regex, and fix command.
- `formatChecks` (L285–326): runs the first installed formatter per extension group.

`internal/conform/structural.go` has:
- `gofmt` (L557–573): runs `gofmt -l` over Go files.
- `vet` (L580–617): runs `go vet` over Go files, gated by `go.mod` existence.
- `asserts` (L383–421): uses `go/ast` to check whether a Go test function can fail.
- `scanGoFile` (L315–352): uses `go/ast` for long functions, assertion-less tests, and unused declarations.
- `testRefRe` (L495–496): a regex that recognises test references in errata, with hardcoded Go (`Test[A-Z]`), Python (`test_`), and JS (`it()/test()/describe()`) patterns.
- `suppressorRe` (L148): checks for `var _ = pkg.Symbol` but only in `.go` test files (L195).

`internal/conform/revert.go` has:
- `splitChange` (L106–122): uses `project.IsTestPath` and `project.IsDocsFile` to classify files.
- `keepRustTests` (L180–189): hardcodes `.rs` suffix check (L168 in `putBack`).
- `rustTestModule` (L197–231): finds `#[cfg(test)]` modules.

`internal/conform/duplicate.go` uses `project.IsSourceFile` and `project.IsTestPath` (L82, L106).

`internal/conform/docsource.go` uses `project.IsDocsFile` (L59).

The `19_language_profiles` spec defines `Profile.AssertPattern`, `Profile.GateConfig`, `Profile.SkipMarkers`, `Profile.TestFile`, `Profile.Format`, and `Profile.Lint` on the profile struct. The `20_engine_core` spec moves `gateConfigFile` and `testFile` to the engine's ledger. Neither scope wires these into `internal/conform`'s structural checks — that is this scope.

### Why this matters

Today, adding a language to the structural checks requires editing `polyglot.go`'s `sourceSpans` switch, the `formatters` table, and potentially `structural.go`'s `gofmt`/`vet` functions. The assertion patterns, span finders, and formatter commands are scattered across two files with no connection to the profile table. After this scope, adding a language's structural-check support is setting fields on its `Profile` row.

## Requirements

### `ScanInput` gains a `Detection` field

`ScanInput` gains a field `Lang lang.Detection`. When set, the scan uses it for all language-specific decisions. When nil (zero value), the scan falls back to the current behaviour (hardcoded extension checks and `project.IsTestPath`/`project.IsSourceFile`) so that existing callers that do not set it continue to work without change. Both `codefix/pipeline.go` and `codeimpl/conformance.go` are updated to pass the detection they already hold.

### File classification uses the detection

When `ScanInput.Lang` is set:
- The `goFiles`/`otherFiles` split in `Scan` uses `lang.Detection.IsSourceFile` instead of `project.IsSourceFile`, and identifies Go files by checking whether the primary or any detected profile is `"go"` and the file ends in `.go`, rather than hardcoding the suffix.
- `scanAddedText`'s `code` and `production` checks use `lang.Detection.IsSourceFile` and `lang.Detection.TestFile` instead of `project.IsSourceFile` and `project.IsTestPath`.
- The `suppressorRe` check (import suppressor) fires for Go test files identified by the Go profile's `TestFile`, not by a hardcoded `.go` suffix.
- `checkErrataCitation`'s `project.IsTestPath(ref)` call uses `lang.Detection.TestFile`.
- `testRefRe` is extended with each detected profile's `TestID` pattern (from `Profile.TestID`), so errata citations recognise test references in any detected language.

### Assertion checks use the profile's `AssertPattern`

`scanSourceFile` in `polyglot.go` currently receives the assertion regex from `sourceSpans`'s hardcoded switch. After this change:
- `sourceSpans` receives the `lang.Detection` (or the matched profile for the file's extension) and returns the profile's `AssertPattern` instead of the hardcoded `pyAssertRe`/`jsAssertRe`/`rustAssertRe`.
- For Go files, `scanGoFile`'s `asserts` function continues to use `go/ast` (it is strictly more precise than a regex). The `AssertPattern` on the Go profile is set but used only as a fallback or for documentation; the `go/ast` backend takes precedence.
- A profile whose `AssertPattern` is nil produces no no-assertions finding for non-Go files. The finding's message is unchanged.

### Function-span detection is dispatched by profile

`sourceSpans` currently switches on file extension. After this change:
- The function receives the file's matched profile (determined by extension from the detection's profiles).
- The profile selects the span finder: Go uses `go/ast` (via `scanGoFile`), Python uses `pythonSpans`, JS/TS uses `jsSpans`, Rust uses `rustSpans`, brace-based languages (Java, Kotlin, C#, C, C++, Swift, PHP, Dart, Scala) use `braceSpans` with the appropriate regex.
- The mapping from profile name to span finder is a table in `polyglot.go`, not a switch on extension. Adding a new profile's span finder is adding a row.
- A profile with no span finder (e.g. `generic`, `elixir`, `ruby` if no brace-based finder applies) gets no long-function finding, and the scan does not report an error.

### Formatting checks use the profile's `Format` command

The `formatters` table and `formatChecks` function in `polyglot.go`, and the `gofmt` and `vet` functions in `structural.go`, are replaced by a unified path:

1. For each changed file, determine its profile from the detection.
2. Group files by profile.
3. For each profile that has a `Format` command, resolve the command (via `Command.Resolve(root)`) and run it over the profile's files. The command's output is parsed by a per-profile output regex (a new field or a table alongside the profile) to extract `path:line:col: message` or the tool-specific format.
4. For Go files, `gofmt -l` and `go vet` continue to run via the Go profile's `Format` and `Lint` commands. The Go profile's `Format` command resolves to `gofmt -l` and its `Lint` to `go vet`.
5. A profile with no `Format` command produces no formatting finding. The finding carries `Backend` set to the formatter's name (e.g. `"gofmt"`, `"rustfmt"`, `"ruff"`, `"prettier"`) and `Profile` set to the profile's `Name`.

The per-profile output regex is stored alongside the profile's `Format` command. The existing `formatter` struct's `line *regexp.Regexp` field serves this purpose. The profile either carries this regex directly or the `polyglot.go` code maintains a small lookup table keyed by profile name. The latter is chosen to keep the `lang.Profile` struct from growing a field that only `internal/conform` uses.

### The revert check uses the detection

`splitChange` in `revert.go` receives a `lang.Detection` (or a test-file classifier function) and uses it instead of `project.IsTestPath`. `project.IsDocsFile` stays because it is not language-specific.

`putBack` in `revert.go` currently hardcodes `strings.HasSuffix(s.rel, ".rs")` to decide whether to call `keepRustTests`. After this change, it checks `profile.InlineTests` for the file's matched profile. The `keepRustTests` function itself is unchanged (it operates on Rust source text), but it is called only when the profile says the language has inline tests.

The `Revert` function's signature gains an optional `lang.Detection` parameter (or `splitChange` is refactored to accept a classifier function). Existing callers that pass nil get the current `project.IsTestPath` behaviour.

### The duplicate check uses the detection

`duplicates` in `duplicate.go` replaces `project.IsSourceFile` and `project.IsTestPath` with `lang.Detection.IsSourceFile` and `lang.Detection.TestFile` when the detection is available (passed through `ScanInput.Lang`).

### `Finding` gains `Backend` and `Profile` fields

The `Finding` struct gains two optional string fields:

```go
type Finding struct {
    Check   string `json:"check"`
    Path    string `json:"path"`
    Line    int    `json:"line,omitempty"`
    Message string `json:"message"`
    Backend string `json:"backend,omitempty"`
    Profile string `json:"profile,omitempty"`
}
```

- `Backend` is set by the check that produced the finding: `"go/ast"` for `scanGoFile`, `"regex"` for `scanSourceFile`, the formatter's name for formatting checks (e.g. `"gofmt"`, `"rustfmt"`, `"ruff"`, `"prettier"`), `"go vet"` for vet findings.
- `Profile` is the `lang.Profile.Name` of the language the finding was judged under (e.g. `"go"`, `"python"`, `"rust"`). Empty when the check is language-neutral (e.g. `discarded_error`, `later_task`, `git_init_branch`, `future_date`, `errata_citation`, `duplicate`).
- These fields are additive and `omitempty`; existing JSON consumers are unaffected.

### The denylist test covers `internal/conform`

The `TestNoLanguageLiterals` test in `internal/lang` (defined by `19_language_profiles`) is extended to walk `internal/conform`'s non-test `.go` files. After this scope lands, no line in `internal/conform` (outside test files) contains a string from the denylist (`.go"`, `"go.mod"`, `gofmt`, `"go test"`, `"go vet"`, `_test.go"`, `panic("not`, `"pytest"`, `"cargo "`, `"npm "`, `.py"`, `.ts"`, `.rs"`). Exceptions are allowed for import paths.

### Formatter output regex table

A table in `internal/conform/polyglot.go` (or a new file `internal/conform/formatline.go`) maps profile names to the regex that parses the formatter's output into `(path, line, col, message)`. The table covers:

| Profile | Formatter | Output regex |
|---|---|---|
| `go` | `gofmt -l` | bare filename per line (no line/col) |
| `go` | `go vet` | `path:line:col: message` |
| `rust` | `rustfmt --check` | `Diff in path:line:` or `Diff in path at line N:` |
| `python` | `ruff format --check` / `black --check` | `Would reformat: path` / `would reformat path` |
| `node` | `prettier --check` | `[warn] path.ext` |

A profile not in this table gets no formatting finding. The table is the only place a formatter's output shape is spelled.

### Error handling

- When `ScanInput.Lang` is nil, every check falls back to the current hardcoded behaviour. No caller breaks.
- A profile whose `Format` command is not installed (the version probe fails) produces no finding, as today.
- A profile whose `AssertPattern` is nil produces no no-assertions finding for non-Go files.
- A profile with no span finder produces no long-function finding.
- Parsing errors in formatter output are silently skipped (as today).

### Backward compatibility

- The `Scan` function's signature does not change; `ScanInput` gains a field.
- The `Revert` function gains an optional parameter or `splitChange` is refactored internally. Existing callers that do not pass a detection get the current behaviour.
- The `Finding` struct gains two `omitempty` fields. JSON output changes only by these additive fields.
- No flag, exit code, envelope field or schema version changes.
- The `--schema` golden files do not change (the `Finding` struct is not part of the top-level schema; it is nested in `result.structural`).

## Design Decisions

1. **The formatter output regex is a table in `internal/conform`, not a field on `lang.Profile`.** The `lang.Profile` struct is the single source of language facts, but the regex that parses a formatter's stdout is a concern of the structural-check package, not of the profile. Putting it on the profile would couple `internal/lang` to `internal/conform`'s parsing logic. A small table in `polyglot.go` keyed by profile name keeps the concern local.

2. **`ScanInput.Lang` is optional (nil means fallback).** This avoids a breaking change to callers. Both `codefix` and `codeimpl` already hold a detection and will pass it; any other caller (tests, future tools) that does not set it gets the current behaviour. The fallback is removed in a later scope when all callers are migrated.

3. **Go keeps its `go/ast` backend for assertions and unused declarations.** The input PRD says "equality means the same mechanism runs for every language with that language's own conventions; it does not mean Go loses what `go/ast` gives it." The `go/ast`-based `asserts` function is strictly more precise than a regex (it tracks `*testing.T` through helpers), so it stays as the Go backend. Other languages use the profile's `AssertPattern` regex.

4. **`sourceSpans` dispatches by profile, not by extension.** Today it switches on `path.Ext(p)`. After this change, the file's profile is determined from the detection (by matching the file's extension against the profiles' conventions), and the profile selects the span finder. This means a `.kt` file in a JVM project uses the Kotlin span finder because the JVM profile is detected, not because `.kt` is in a hardcoded map. The existing `braceExts`, `jsExts`, `kwFuncExts` maps become part of the profile-to-finder mapping.

5. **`Revert`'s signature change is minimal.** Rather than adding `lang.Detection` as a parameter to `Revert` (which would change its public signature), `splitChange` is refactored to accept a `testFile func(string) bool` parameter. `Revert` passes `project.IsTestPath` when no detection is available, and the caller (the pipeline) can wrap `lang.Detection.TestFile`. This keeps the change internal.

6. **`keepRustTests` is guarded by `Profile.InlineTests`, not by file extension.** Today `putBack` checks `strings.HasSuffix(s.rel, ".rs")`. After this change, it checks whether the file's matched profile has `InlineTests == true`. This means if Elixir or another language with inline tests is added, the same mechanism fires. The `keepRustTests` function itself is Rust-specific (it finds `#[cfg(test)]` modules); a future scope may generalise it per profile.

7. **`testRefRe` is extended with profile `TestID` patterns.** The errata citation check uses `testRefRe` to recognise test references. Today it hardcodes Go, Python and JS patterns. After this change, it builds the regex from the union of every detected profile's `TestID` pattern (when set) plus the existing `TS-` pattern for spec test ids. This is done once per `Scan` call, not per file.

8. **The `Backend` and `Profile` fields on `Finding` are strings, not enums.** The set of backends and profiles is open (new languages, new formatters), so a string is more appropriate than a closed enum. The fields are `omitempty` so existing JSON output is unchanged for findings that do not set them.

9. **`go vet` is treated as the Go profile's `Lint` command.** Today `vet` is a separate function that runs `go vet` over Go files. After this change, it is the Go profile's `Lint` command, run through the same path as other profiles' linters. The output parsing uses the existing `vetLineRe` regex, stored in the formatter output regex table.

10. **The `import_suppressor` check stays Go-only.** The `suppressorRe` check (`var _ = pkg.Symbol`) is a Go idiom. It fires only when the file's profile is Go and the file is a test. No other language has this pattern, so it is not generalised.

## Dependencies

| Spec | Reason |
|---|---|
| `19_language_profiles` | This scope reads `Profile.AssertPattern`, `Profile.Format`, `Profile.Lint`, `Profile.TestFile`, `Profile.InlineTests`, `Profile.TestID`, `Profile.GateConfig`, `Profile.SkipMarkers`, and `Detection.TestFile` / `Detection.IsSourceFile`, all defined by this spec. The denylist test defined by this spec is extended to cover `internal/conform`. |
| `20_engine_core` | The engine's `Ledger.Changes` classifies paths using `lang.Detection.TestFile` and `Profile.GateConfig`. This scope makes `internal/conform` use the same classifiers, so the two packages agree on what is a test and what is gate config. The `gateConfigFile` function moves to the engine in that spec; this scope replaces `internal/conform`'s own hardcoded classifiers with the profile's. |

## Verified External API

This scope uses only the Go standard library and types already defined in the repository. No new external packages are introduced.

| Symbol | Package | Signature | Source |
|---|---|---|---|
| `lang.Detection.TestFile` | `internal/lang` | `func (d Detection) TestFile(path string) bool` | Defined by `19_language_profiles` — **NOT FOUND** (package does not exist yet); signature assumed from the spec's PRD |
| `lang.Detection.IsSourceFile` | `internal/lang` | `func (d Detection) IsSourceFile(path string) bool` | Defined by `19_language_profiles` — **NOT FOUND**; signature assumed from the spec's PRD |
| `lang.Profile.AssertPattern` | `internal/lang` | `*regexp.Regexp` field on `Profile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.1 |
| `lang.Profile.Format` | `internal/lang` | `Command` field on `Profile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.1 |
| `lang.Profile.Lint` | `internal/lang` | `Command` field on `Profile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.1 |
| `lang.Profile.InlineTests` | `internal/lang` | `bool` field on `Profile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.1 |
| `lang.Profile.TestID` | `internal/lang` | `*regexp.Regexp` field on `Profile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.1 |
| `lang.Profile.TestFile` | `internal/lang` | `func(path string) bool` field on `Profile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.1 |
| `lang.Command.Resolve` | `internal/lang` | `func (c Command) Resolve(root string) []string` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from 19-REQ-1.2 |
| `project.IsDocsFile` | `internal/project` | `func IsDocsFile(p string) bool` | `internal/project/docs.go` L55 |
| `go/ast` | stdlib | standard AST types and `Inspect` | stdlib |
| `go/parser` | stdlib | `func ParseFile(...)` | stdlib |
| `go/token` | stdlib | `func NewFileSet()` | stdlib |
| `regexp.MustCompile` | stdlib | `func MustCompile(str string) *Regexp` | stdlib |
