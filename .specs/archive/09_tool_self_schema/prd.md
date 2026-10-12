---
spec_id: "09"
spec_name: "tool_self_schema"
title: "`--schema`: the tool describes itself"
status: "active"
created_at: "2026-09-30T10:35:37.532094Z"
updated_at: "2026-09-30T10:35:37.532094Z"
intent_hash: "bde36756feaa020ed5957395353bac4c3acd91268270ef1d9ec5c3a4308d0706"
schema_version: 2
source: "docs/prds/03-make-the-tools-observable-and-self-describing.md"
---
## Intent

`spec`, `issue`, `fix` and `impl` share one shell (`internal/toolio`) but each has its own flags and its own `result` shape, and today the only way to learn either is to read `docs/cli.md` or the Go source. This spec gives each tool a way to print its own interface — the flags it accepts and the JSON Schema of what it returns — on demand and without doing any work, and gives the envelope a version number for that interface, separate from the build identity, so a caller can tell a shape it understands from one it does not before it acts on either.

## Goals

- Each of the four tools, given `--schema`, exits `0` printing one JSON document, with no network call, no requirement that `--dir` be a real repository, and no model resolved — verifiable by running it with no credentials configured and `--dir` pointing at an empty directory.
- That document's `flags` and `result` values are each, independently, a document that compiles without error against the JSON Schema 2020-12 meta-schema.
- Every ordinary envelope (not just `--schema`'s own output) carries a `schema_version` field alongside `version`, currently `"2.0.0"`.
- A change to any field reachable from a tool's `Result` type, to a flag's type, default or declared enum, or to the envelope's own shape, that is not reflected in that tool's checked-in `--schema` golden file fails `make test`.
- `docs/cli.md` and a new ADR each state, in one place, exactly which kind of change to the envelope is additive (no version bump needed) and which is breaking (a major bump), with the bump this split's own earlier scopes cause given as the worked example.

## Non-goals

- Labelling which result fields carry untrusted text (`x-trust`, the envelope's `untrusted_fields` array) — `untrusted_text_labels`, the next scope of this split, not yet written.
- `--preflight` and its `result.preflight`/`result.estimate` — `preflight_checks`, the split's last scope, not yet written.
- Any change to `--events`/`--events-file` (`07_progress_event_stream`, written) or `--output` (`08_envelope_output_file`, written) beyond the one additive field this scope adds to the `run_start` event (`schema_version`, alongside the fields those two scopes already gave it).
- A new subcommand. `--schema` is a flag on the existing four tools, per [ADR 03](../adr/03-rebuild-the-skills-as-tools.md).
- Serving this document over a socket, an MCP server, or any channel other than the stdout a one-shot process already writes to.
- Validating, at runtime, that a tool's actual output on a real run matches what `--schema` advertised. The golden-file test (Requirement 6) is a build-time check on the Go source; nothing here re-checks a live envelope against its own schema.
- Hand-writing any part of `flags` or `result`. Both are derived from the `flag.FlagSet` and the Go `Result` types respectively, so the document cannot silently drift from what the tool actually accepts or returns.

## Background

The four tools share one shell, `internal/toolio` (`App`, `Common`, `Run`, `Envelope`), built per ADR 03. `App.Main` (`internal/toolio/app.go`) already special-cases two flags before any input is classified or model resolved: `-h`/`--help` (via `flag.ErrHelp`) and `--version`, both handled and returned on before `Resolve`, `ResolveModel`, or `a.Exec` run. `--schema` is a third flag of the same shape.

Each tool's own package defines its `Result` struct returned through `Deps.Exec`: `codefix.Result` (`codefix/types.go`), `codeimpl.Result` (`codeimpl/types.go`), `issuetriage.Result` (`issuetriage/pipeline.go`), `specgen.Result` (`specgen/pipeline.go`, embedding `Package` anonymously). None of their fields carry a machine-readable description today, only Go doc comments, which are not available by reflection at runtime. `toolio.Envelope` (`internal/toolio/envelope.go`) is the outer object every tool actually writes to stdout; its own `Result any` field holds whichever of the four concrete types the running tool produced.

Two schema-shaped mechanisms already exist in the tree and neither does this job. `specgen/jsonschema.go`'s `ToolSchema` converts an *afspec-authored* JSON Schema document into `agentkit-go/schema.Schema` — the model-facing tool-call schema shape, with vendor-specific keywords dropped (`$schema`, `title`, `allOf`, …) and property names outside `^[a-zA-Z0-9_.-]{1,64}$` rejected, because its audience is an LLM provider's tool-calling API. `afspec/validate.go` compiles and validates the format's own bundled schemas via `github.com/santhosh-tekuri/jsonschema/v6`, a direct module dependency already used this way. Nothing today derives a JSON Schema from an arbitrary Go struct type, and nothing serves as an audience-appropriate document for a caller's own tooling rather than a model's.

This scope depends on the two scopes of this split that change what the envelope looks like: `05_envelope_decidable` (not yet implemented in the tree, per its own PRD) folds `ambiguity`/`blocker` into `needs_human`, adds `status`/`summary`, and turns `warnings` from `[]string` into a structured object; `06_trim_and_chain_results` adds `--detail`, `artifacts[]`, `side_effects[]`, `next[]`, and trims or restructures several `result` fields per tool. Both are breaking changes to the shape callers parse today. `07_progress_event_stream` (written) defines the `run_start` event and explicitly defers adding a `schema_version` field to it to this scope (its own Design Decision 8).

## Requirements

### 1. `--schema`, a new shared flag

A new boolean flag on `Common` (`internal/toolio/cli.go`), default `false`.

- Checked in `App.Main` immediately after the existing `--version` check and before the bare-input check, `PreCheck`, `Common.Workspace()`, `Resolve`, and model resolution — the same place `--version` already returns before any of them run. `--version` keeps priority when both are given, matching today's check order.
- When true, the tool builds the document (Requirement 2), writes it to stdout as indented JSON followed by a newline (the same rendering convention `Emit` already uses for the envelope, for the same reason: a person is at least as likely to read this as a program is), and the process exits `0`.
- No `Run`, `Progress`, events sink, or `--output` file is constructed on this path: `--output` and `--events`/`--events-file`, even when given alongside `--schema`, have no effect, because no run happens for them to describe.
- A malformed *other* flag is still reported by `flag.Parse` exactly as it is today (parsing happens before any of these flags are read); a positional argument given alongside `--schema` is accepted and ignored, so `fix --schema "some text"` behaves exactly like `fix --schema`.

### 2. The document's shape

```jsonc
{
  "tool": "fix",
  "schema_version": "2.0.0",
  "description": "Diagnoses a problem, writes the change on a branch, verifies it, and lands it.",
  "input":  { "description": "…", "kinds": ["text", "file", "stdin", "issue"] },
  "flags":  { "$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": { "land": { "enum": ["pr", "branch", "none"], "type": "string", "default": "pr", "description": "…" } } },
  "result": { "$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "properties": { "tool": {...}, "schema_version": {...}, "ok": {...}, "result": { "type": "object", "properties": { /* codefix.Result, in field order */ } }, … } },
  "exit_codes": { "0": "done", "1": "failed", "2": "usage", "3": "needs_human", "4": "unverified" }
}
```

- `tool` and `schema_version` are the same values `Envelope.Tool`/`Envelope.SchemaVersion` carry on an ordinary run.
- `description` is one sentence, hand-authored per tool (a new `App.Description` field, set once in each `cmd/<tool>/main.go`).
- `input.description` is hand-authored per tool (a new `App.InputDescription` field) — the prose already in each tool's `usage` constant under "The input is exactly one of:", stated once as a value rather than parsed out of help text. `input.kinds` is always the same four-element closed list, `["text", "file", "stdin", "issue"]` — the values `toolio.Input.Kind` always produces — regardless of whether a given tool's `CheckInput` then refuses one of them (`impl` refuses `issue`); the refusal is business logic, not a different classifier.
- `exit_codes` lists only the codes this specific tool can actually return: `issue` and `spec` never produce `3` or `4`, so their `exit_codes` has three entries, not five. Hand-authored once per tool (a new `App.ExitCodes map[int]string`), mirroring the "Exit codes:" block each `usage` constant already documents in prose.
- `flags` and `result` are Requirements 3 and 4.

### 3. `flags`, generated from the tool's `flag.FlagSet`

Built by walking `fs.VisitAll` over the fully-populated `FlagSet` `App.Main` already constructs (`Common`'s registration, then the tool's own `Flags` closure) — Go's `flag` package visits in name order, which gives a stable, deterministic property order for free, without a second bookkeeping mechanism.

- `--schema` and `--version` themselves are excluded: neither takes an argument a caller would ever pass as part of a tool call, and both make the call return before doing anything else.
- For every other flag: `description` is `f.Usage` verbatim; `default` is the flag's own default value; `type` is inferred from the concrete `flag.Value` — when it implements the standard library's `flag.Getter` (every flag registered through `*Var`, i.e. `StringVar`, `BoolVar`, `IntVar`, `Float64Var`, `DurationVar`, does), the JSON type follows the dynamic type `Get()` returns (`string`, `boolean`, `integer`, `number`; `time.Duration` renders as `{"type": "string", "format": "duration"}`, its default as the duration's own string form, e.g. `"10m0s"`). A flag whose `Value` does not implement `Getter` (`fix`'s `--pull`, a hand-written `flag.Value`) is reported as `{"type": "string"}`, using its own `String()` for the default.
- `enum` is present only for a flag registered with a new declaration alongside its `*Var` call — every flag that already has a closed set of legal values today (`--land` on `fix` and `impl`) and every one added by an earlier scope of this split that has one (`--events`, `--detail`, once those flags exist in the tree) is declared this way as part of this scope's implementation. A flag that takes free text (`--repo`, `--model`, `--dir`) carries no `enum` key.
- The whole object is `{"$schema": "…/2020-12/schema", "type": "object", "properties": {…}}`; nothing is marked `required` (every registered flag has a default), and no `additionalProperties` restriction is asserted — a caller's framework decides how strict its own use of the document should be.

### 4. `result`, generated from Go types by reflection

A new, small reflection-based generator (this scope's principal new code) that turns a Go struct type into a JSON Schema node:

- A struct becomes `{"type": "object", "properties": {…}, "required": […]}`. Property order follows the struct's declaration order (via `reflect.VisibleFields`, which also promotes an anonymously embedded struct's fields in place — exactly how `encoding/json` already flattens `specgen.Result`'s embedded `Package` — so the schema's property order matches the order the same value actually marshals in). Because Go sorts `map[string]any` keys unconditionally on marshal, the generator's own schema type carries its properties as an ordered slice with a custom `MarshalJSON`, not a plain map — the same problem, and the same fix, `specgen.ToolSchema`'s own doc comment already describes for the model-facing case.
- A field's JSON name and optionality come from its existing `json:"…"` tag, read exactly as `encoding/json` reads it: `omitempty` means the field is absent from `required`; its absence means the field always appears and is required. A field tagged `json:"-"` is skipped.
- A slice becomes `{"type": "array", "items": …}`; a pointer's schema is its pointee's (never separately marked nullable — `omitempty` already says whether it can be absent); a scalar becomes the matching JSON Schema primitive.
- A field's `description` comes from a new struct tag read alongside `json`, added to every field reachable from `toolio.Envelope` and from each of the four tools' `Result` types — including the shared `internal/checks.Result`, embedded by both `codefix.Result` (`Baseline`, `Verification`) and `codeimpl.GateResult`, and every other nested type (`Ambiguity`, `Implementation`, `TaskReport`, `Survey`, `ValidationReport`, `Criterion`, `FileRef`, and the rest). A field missing the tag is caught by Requirement 6's exhaustiveness test, not silently left blank.
- Applied to `toolio.Envelope` itself to build `result`'s value: the envelope's own `Result any` field is special-cased — for a given tool, its schema is substituted with the schema of that tool's own concrete `Result` type (a new `App.ResultSample any` field, set once per `cmd/<tool>/main.go` to a zero-value `codefix.Result{}` and so on, used purely for its type). `flags` and `result` therefore both carry their own `$schema` line and are each independently valid, standalone JSON Schema documents.
- This generator is new, standalone code, not a reuse of `agentkit-go/schema.Schema` via `specgen.ToolSchema`: that type and its converter exist to satisfy what a model's tool-calling API will accept (dropped `$schema`/`title`/`allOf`, property names restricted to `^[a-zA-Z0-9_.-]{1,64}$`), constraints that do not apply to a document a caller's own tooling reads directly.

### 5. `schema_version`, on the envelope and the compatibility rule it names

- `toolio.Envelope` gains `SchemaVersion string` (`json:"schema_version"`), always present (never `omitempty`), immediately after `Version` in field order, set from one package-level constant (`toolio.SchemaVersion`) shared by all four tools — one interface, one version, since the four share the shell that defines it.
- `07_progress_event_stream`'s `run_start` event gains the same value under the same key: an additive field on an already-closed event type, exactly the kind of change the rule below classifies as not needing a version bump of its own.
- The rule, stated here and repeated in `docs/cli.md` and the new ADR: within a major version, a change to the envelope — any tool's, since the shell is shared — is additive only: a new top-level field; a new field on an existing object (`result`, `needs_human`, `error`, a `warning`, an event); a new value in a field whose own description already documents it as an open set (a new `WarnCode`, a new error `category`); a new event type. Removing or renaming a field, changing a field's type, or narrowing a field documented as a closed enum, is a breaking change and bumps the major version.
- The envelope as it stood before `05_envelope_decidable` and `06_trim_and_chain_results` is `1.0.0`. Those two specs' changes — `warnings` becoming a structured object, `ambiguity`/`blocker` folding into `needs_human`, and `06`'s trimmed/added `result` fields — are the first breaking change, and are the reason `toolio.SchemaVersion` is `"2.0.0"` as this scope introduces the constant.

### 6. A golden-file test per tool, and an exhaustiveness check on descriptions

- `cmd/<tool>/testdata/schema.golden.json` (new directories, one per tool) holds the exact bytes `<tool> --schema` produces. A test in each `cmd/<tool>/main_test.go` (the `getTestBin` helper `cmd/fix/main_test.go` already has is reused for `impl`, `issue` and `spec`, which do not yet have a `main_test.go`) builds the tool, runs it with only `--schema`, and compares byte-for-byte against the golden file; a mismatch fails with a diff, and an explicit update path (an environment variable or test flag, the common Go convention) regenerates the file deliberately rather than the test silently accepting a new shape.
- The same test additionally compiles both `flags` and `result` with `github.com/santhosh-tekuri/jsonschema/v6` (already a direct dependency, used the same way by `afspec.getCompiledSchemas`) against the 2020-12 meta-schema, so a document that is byte-stable but not actually valid JSON Schema still fails.
- A separate test walks, by reflection, every field reachable from `toolio.Envelope` and from each of the four `Result` types and fails naming any field that lacks the description tag Requirement 4 requires — the same style of exhaustiveness check `05_envelope_decidable` already uses for its `WarnCode`→stage table.

### 7. `--schema` touches nothing else

- Any other flag given alongside `--schema` is parsed (an unparseable one still reports `flag.Parse`'s own error, unchanged) but never acted on: no `PreCheck`, no `Common.Workspace()`, no `Resolve`, no model resolution, no credential check, and (Requirement 1) no `Run`, `Progress`, events sink, or `--output` file.
- `-h`/`--help` and a bare invocation on a terminal are unaffected; both already return before flags like `--schema` would be read for their value in `App.execute`, since they return out of `App.Main` earlier still.

### 8. Documentation

- `docs/cli.md`: a `--schema` row in the shared-flags table; a new "Self-description (`--schema`)" section with one abbreviated worked example and an explanation of `flags`, `result` and `exit_codes`; a new "Interface versions" section listing `1.0.0` and `2.0.0`, one paragraph each, stating the additive/breaking rule from Requirement 5 and serving as the place every future major bump appends its own entry.
- A new ADR, `docs/adr/06-version-the-envelope-interface.md`, records the compatibility rule as an accepted decision: the context (the envelope is a shared interface across four tools and needs to evolve without breaking a caller silently), the decision (semver, additive-only within a major version, the closed list of what counts as additive vs. breaking from Requirement 5), and the consequence (a future spec that touches the envelope states, in its own PRD, which kind of change it is making).

## Design Decisions

1. **`schema_version` is one constant shared by all four tools, not versioned independently per tool.** The four tools share one shell and one envelope shape by construction (ADR 03); a single version names that one shared interface. This is flagged below as worth checking: a breaking change to only one tool's `Result` type still forces every tool's advertised version to move, even though nothing changed for the other three.
2. **`--schema`'s `result` key holds the JSON Schema of the whole envelope, with the tool's own `Result` type substituted for the envelope's `result` field — not a bare schema of just that `Result` type.** This mirrors why `flags` has to be a complete, standalone document ("so a framework can use it as a tool's input_schema"): a caller wanting a tool's full output_schema gets one document, not one it has to merge by hand with a separately-documented envelope shape. Flagged below, since the source material's own inline comment ("JSON Schema of the envelope with this tool's result") is genuinely readable the other way too.
3. **`flags`'s property order is `fs.VisitAll`'s alphabetical order, not source-declaration order.** Getting declaration order would need a second, parallel bookkeeping mechanism at every flag registration call site across four `cmd/<tool>/main.go` files; alphabetical order is what the standard library already gives for free, and it is exactly as deterministic for the golden-file test and for any provider-side caching benefit, even though it does not match the order flags are listed in each tool's own `usage` text.
4. **Field descriptions live in a new struct tag beside `json`, not in Go doc comments or a side table.** A doc comment is unreadable by reflection at runtime; a side table (field name → description) drifts from the field it describes the moment one is renamed and the other is not. The tag lives beside the field it describes and Requirement 6's exhaustiveness test is what keeps it from being forgotten, the same enforcement style `05_envelope_decidable` already uses for its warning-code table.
5. **The generator is new, standalone code, not built on `agentkit-go/schema.Schema`.** That type (and `specgen.ToolSchema`, which produces it) exists to satisfy a model provider's tool-calling constraints — dropped `$schema`/`title`/`allOf`, property names restricted to `^[a-zA-Z0-9_.-]{1,64}$` — none of which apply to a document a caller's own tooling reads directly and which would otherwise silently strip metadata (`$schema`, descriptions on some keywords) this feature exists to provide.
6. **`flag.Getter` (`type Getter interface { Value; Get() any }`, standard library) is how a flag's JSON type is inferred**, rather than string-matching the unexported `flag.stringValue`-style type names — every built-in `*Var` registration already implements it, so this only needs a fallback (report `string`) for a hand-written `flag.Value` like `fix`'s `--pull`.
7. **A flag's closed set of legal values must be declared explicitly** (a new small function pairing a flag already registered on a `FlagSet` with its enum), because `flag.FlagSet` carries nothing today about whether a flag's legal values are closed.
8. **Each tool's `exit_codes` lists only the codes it can actually produce**, hand-authored once per tool rather than always printing the shared five-entry table: `issue` and `spec` never stop with `needs_human` or `unverified`, and a caller building a framework's tool description from this document should not have to know that separately.
9. **`--version` keeps priority over `--schema` when both are given**, matching the existing check order in `App.Main`; no new precedence rule was introduced for this.

## Dependencies

| Spec | Reason |
|---|---|
| `05_envelope_decidable` | Defines `status`, `summary`, `needs_human`, and the structured `Warning{code, severity, stage}` object; together with `06`'s changes this is the breaking change `schema_version` 2.0.0 names, and this scope's `result` document reflects whatever `toolio.Envelope` looks like once this spec lands. |
| `06_trim_and_chain_results` | Defines `--detail`, and the `detail`/`artifacts`/`side_effects`/`next` fields added to the envelope and to each tool's `Result`, plus the removal of the duplicated diagnosis fields; the other half of the 2.0.0 bump, and further fields this scope's reflection generator and golden files must cover. |
| `07_progress_event_stream` | Defines the `run_start` event this scope adds an additive `schema_version` field to, and the `--events`/`--events-file` flags this scope's `--schema` path must leave inert. |
