---
spec_id: '03'
spec_name: unified_terminal_io
title: Unified Terminal Io
status: draft
created_at: '2026-06-23T07:04:55.356177+00:00'
updated_at: '2026-06-23T07:34:13.292876+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Unified Terminal IO Module

## Intent

Provide a single, shared terminal I/O library (`agentfox/io/`) that both the `af` and `spec` CLIs import, eliminating duplicated output logic and ensuring consistent JSON serialization, error envelopes, spinner display, and help behavior across both tools.

## Background

Both `af` and `spec` are designed to be driven by AI coding agents (Claude Code, Cursor, Codex). As agent integrations mature, inconsistent error envelopes, the absence of an agent-mode toggle, and duplicated I/O code have made agent integration brittle and new CLI feature development error-prone. This refactor establishes a stable, unified I/O foundation before two planned follow-up specs — **af CLI agentic optimization** (spec 04) and **spec CLI agentic optimization** (spec 05) — can build on it. Without this foundation, each follow-up spec would independently re-solve the same fragmentation problems, compounding the inconsistency.

## Problem

The `af` and `spec` CLIs share the same `agentfox` core library but each implement their own terminal I/O patterns: JSON output, error envelopes, progress spinners, theme/banner rendering, and format dispatch. This fragmentation causes five specific problems:

1. **Inconsistent error envelopes.** `af` emits `{"error": "<message>"}` (flat string). `spec` emits `{"ok": false, "error": {"type": ..., "message": ..., "retryable": ...}}` (rich envelope). Agents that call both CLIs must handle two schemas.

2. **Duplicated spinner implementations.** `PlanSpinner` (agentfox/ui/progress.py) and `StatusSpinner` (spec/ui.py) are 80% identical — both do TTY detection, quiet-mode, and Rich Live — but share no code.

3. **No agent-mode toggle.** Agents must pass `--json --quiet` on every invocation. There is no `AF_AGENT=1` environment variable to set once.

4. **No structured help output.** `--help` returns human-formatted text. Agents must parse Click's help output to discover commands and options.

5. **Scattered format dispatch.** Every `af` command reimplements the `if json_mode: emit(...) else: click.echo(...)` pattern.

> **Note:** Manual table formatting in `standup` and `findings` is a known pain point but is deferred to Spec 04, when `af` commands are migrated to use `agentfox/io/` directly. It is not addressed in this spec.

## Goals

The following outcomes define success for this spec. Each is verifiable at completion:

1. **All five pain points resolved.** Each of the five problems listed above has a corresponding implementation in `agentfox/io/` that eliminates it.
2. **Single spinner implementation.** `PlanSpinner` and `StatusSpinner` are replaced by exactly one `StatusSpinner` class in `agentfox/io/spinner.py`. No duplicated spinner logic remains.
3. **Unified error envelope schema.** Every JSON error output from both `af` and `spec` conforms to the spec-style envelope: `{"ok": false, "error": {"type": ..., "message": ..., "retryable": ...}}`. The flat `{"error": "<message>"}` format is retired.
4. **AF_AGENT=1 support.** Setting `AF_AGENT=1` in the environment causes both CLIs to default to `json_mode=True` and `quiet=True` without requiring per-invocation flags. Explicit CLI flags always override this default (see §AF_AGENT Precedence Rules).
5. **No regressions.** All existing `af` and `spec` CLI tests (unit and integration) pass unchanged after migration. The existing test suite is the contract; no tests may be deleted or skipped to satisfy this goal.

## Solution

Create a unified IO module at `agentfox/io/` that both CLIs import. Migrate all terminal output, JSON serialization, error handling, spinner display, banner rendering, format dispatch, and common CLI flags into this module.

### Target File Layout

```
agentfox/io/
├── __init__.py       # Package init and public API re-exports
├── output.py         # OutputManager class
├── json.py           # Unified JSON serialization and emit functions
├── spinner.py        # Unified StatusSpinner class
├── errors.py         # Unified error handling and error_envelope builder
├── cli.py            # common_options decorator and AgentFoxGroup class
└── help.py           # @exit_codes decorator and structured help scaffolding (rendering deferred to Spec 04)
```

Compatibility shims (temporary, removed in specs 04 and 05):
```
af/json_io.py         # Re-exports from agentfox/io/json.py (shim, removed in spec 04)
```

> **Note:** `spec/cli.py`'s inline JSON/error patterns are **not** shimmed in this spec. They remain entirely unchanged until spec 05 (spec CLI agentic optimization). This spec only creates the shared `agentfox/io/` module and the `af/json_io.py` shim. Implementors must not migrate `spec` during this spec.

### Public API — `agentfox/io/__init__.py`

`agentfox/io/__init__.py` re-exports the following curated set of symbols. Consumers may import any of these directly from `agentfox.io`:

```python
from agentfox.io import (
    OutputManager,       # output.py
    StatusSpinner,       # spinner.py
    get_output_manager,  # output.py
    emit,                # json.py
    emit_ok,             # json.py
    emit_line,           # json.py
    emit_error,          # json.py
    read_stdin,          # json.py
    error_envelope,      # errors.py
    AgentFoxGroup,       # cli.py
    common_options,      # cli.py
    exit_codes,          # help.py
)
```

All other symbols within submodules are considered internal. `handle_cli_errors` is intentionally excluded from this re-export surface — it is used internally by `AgentFoxGroup`, and command authors rely on `AgentFoxGroup`'s error routing rather than applying the decorator directly. Consumers requiring non-listed symbols must import directly from the relevant submodule (e.g. `from agentfox.io.errors import handle_cli_errors`). No stability guarantee applies to submodule-level imports until Spec 05 ships.

### API Stability

`agentfox/io/` is **internal only** through the completion of Spec 05. No stability guarantee is made until Spec 05 ships. Both `af` and `spec` are first-party consumers; there are no external plugin or notebook callers. Breaking changes in Specs 04 and 05 are permitted without semver concern.

### Package Dependency Graph

`agentspec` is a sibling package in the monorepo. The dependency graph is:

```
af       → agentfox → afspec
spec     → agentspec → afspec
```

`agentspec` does **not** import from `agentfox`, so importing agentspec exception types in `agentfox/io/errors.py` does not create a circular dependency. However, to keep `agentfox/io/` dependency-light and usable without agentspec installed, all agentspec imports (`AgentError`, `AgentSpecError`, `SessionError`) must use **lazy imports guarded by `try/except ImportError`**:

```python
try:
    from agentspec.errors import AgentError, AgentSpecError, SessionError
    _AGENTSPEC_AVAILABLE = True
except ImportError:
    _AGENTSPEC_AVAILABLE = False
```

Error type mapping for agentspec exceptions is skipped gracefully when the package is not installed — the exception falls through to the `internal_error` path.

### Rollback / Abort Policy

If the migration causes regressions that cannot be resolved within the scope of Spec 03, **partial merge is permitted**:

- `agentfox/io/` may be merged as a functional, tested module without wiring `af/app.py` to use `AgentFoxGroup`.
- CLI wiring (`af/app.py` → `cls=AgentFoxGroup`) is deferred to Spec 04 in that case.
- The new module must be functional and all new unit tests must pass before partial merge is accepted.
- Full revert (removing `agentfox/io/` entirely) is a last resort and requires explicit team sign-off.

### Components

#### 1. `agentfox/io/__init__.py` — Package Init and Public API Re-Exports

Re-exports the curated public API listed in §Public API above. Consumers can do:
```python
from agentfox.io import OutputManager, StatusSpinner, emit, emit_error
```

All twelve listed symbols are re-exported. No additional symbols are re-exported from `__init__.py`; any unlisted submodule-level names (including `handle_cli_errors`) are internal and available only via direct submodule import.

#### 2. `agentfox/io/output.py` — OutputManager

Central coordinator for all CLI output. Constructed once at CLI startup and threaded through `click.Context.obj`.

```python
class OutputManager:
    json_mode: bool
    quiet: bool
    verbose: bool
    trace: bool
    console: Console  # Rich console for stderr
```

Key behaviors:
- **Agent mode:** If `AF_AGENT=1` is set (exactly the string `"1"` — see §AF_AGENT Precedence Rules), default `json_mode=True` and `quiet=True` unless explicitly overridden by flags.
- **Format dispatch:** `OutputManager` provides three output methods:
  - `emit_json(data: dict) -> None` — writes JSON to stdout (indented with `indent=2`) when `json_mode=True`; silently no-ops otherwise.
  - `emit_human(text: str) -> None` — writes plain text to stdout when `json_mode=False`; silently no-ops otherwise.
  - `emit(data: dict, human_fn: Callable[[], None] | None = None) -> None` — convenience wrapper: calls `emit_json(data)` if `json_mode` is `True`; otherwise calls `human_fn()` if provided (silently no-ops if `human_fn` is `None`).
- **Banner:** `output.banner()` renders the themed banner on stderr (suppressed in json_mode or quiet).
- **Status lines:** `output.status(message)` writes to stderr (suppressed in quiet mode).
- **verbose / trace:** These fields are passed directly to `setup_logging()` to set the logging level. They do not alter any terminal output directly — no stack traces are added to error envelopes, and no structured debug lines are written to stderr. This matches current behavior in both `af` and `spec`.

##### ctx.obj Storage Key

`OutputManager` is stored in `ctx.obj` under the key `"output"`. Subcommands retrieve it via `ctx.obj["output"]` or via the `get_output_manager()` convenience accessor. CLI-specific data uses separate keys in the same dict. Example:

```python
# AgentFoxGroup.invoke() stores:
ctx.obj["output"] = OutputManager(...)

# Subcommands retrieve:
om = ctx.obj["output"]
# or equivalently:
om = get_output_manager()
```

This short, unambiguous key avoids collisions with CLI-specific keys (e.g. `"config"`, `"session"`) while being easy to remember and type.

##### Entry-Point Integration

Both CLI entry-point files integrate with `AgentFoxGroup` by replacing their root group decorator with `cls=AgentFoxGroup`:

- **`af/app.py`:** Replace the existing root group class (currently `BannerGroup` or equivalent) with `cls=AgentFoxGroup`. `AgentFoxGroup` subsumes all behavior previously provided by the existing group class — banner rendering, error handling, and `OutputManager` construction. If a regression blocks this wiring, defer to Spec 04 per the §Rollback / Abort Policy.

  > **`af/app.py` pre-existing behavior:** The current `BannerGroup` (or equivalent) performs banner rendering and error handling. `AgentFoxGroup` is a functional superset: it renders the banner (with suppression rules), routes errors through `cli_error_handler`, and additionally provides `OutputManager` construction and `AF_AGENT` support. No behavior from the existing group class is silently dropped; implementors must audit `BannerGroup` before replacement to confirm no additional behaviors (e.g. custom ctx.obj initialization, non-standard error codes) exist that `AgentFoxGroup` does not yet cover. Any such behaviors must either be incorporated into `AgentFoxGroup` or explicitly listed as deferred to Spec 04. **Audit artifact:** Implementors must leave an inline comment block in `af/app.py` at the replacement site documenting the result of the audit — listing any behaviors confirmed as covered by `AgentFoxGroup`, any behaviors explicitly deferred to Spec 04, and the date of the audit. This comment block serves as evidence that the audit occurred.

- **`spec/cli.py`:** Replace the existing root group definition with `cls=AgentFoxGroup` (**in spec 05 only** — see §Migration Strategy).

`AgentFoxGroup.invoke()` is responsible for constructing the `OutputManager` and storing it in `ctx.obj["output"]`. Each CLI can still define its own options and help text on the root group — `AgentFoxGroup` only handles the common behavior (banner, logging, error handling, `OutputManager` construction). No CLI-specific file is responsible for manually constructing or storing the `OutputManager`; this is handled entirely by `AgentFoxGroup`.

##### ctx.obj Initialization and Conflict Handling

`AgentFoxGroup.invoke()` calls `ctx.ensure_object(dict)` at its start before reading sentinels or constructing `OutputManager`. If `ctx.obj` is already set to a non-dict value by an upstream parent group, `ctx.ensure_object(dict)` will **not** overwrite it — this is Click's built-in behavior. In that case, `AgentFoxGroup` logs a debug-level warning and falls back to constructing `OutputManager` with defaults (honoring `AF_AGENT` env var but with no sentinel data). Implementors must not assume `ctx.obj` is always a fresh dict when `AgentFoxGroup` is nested inside a non-AgentFoxGroup parent.

##### Exit Code Contract

`AgentFoxGroup` uses a **uniform exit code contract**:

- **Exit 0:** Success — command completed without error.
- **Exit 1:** All handled exceptions — any caught `Exception` subclass (regardless of type or `retryable` field) causes `sys.exit(1)`.

`SystemExit` and `KeyboardInterrupt` are **re-raised immediately** without being routed through `cli_error_handler`. This preserves normal `sys.exit(0)` success paths, `--help`/`--version` exits (which raise `SystemExit`), and Ctrl-C behavior. Only `Exception` subclasses are caught and routed.

This simple, uniform contract is intentional. Agents use the `type` and `retryable` fields in the error envelope to branch retry logic — they do not need different exit codes per error type. Exit 2 is not used by `AgentFoxGroup`. The exit code does **not** appear as a field in the JSON envelope.

Implementation: `AgentFoxGroup.invoke()` catches `Exception` (not `BaseException`), routes output through `cli_error_handler`, then calls `sys.exit(1)`. It does not re-raise after routing. `SystemExit` and `KeyboardInterrupt` (both `BaseException` subclasses but not `Exception` subclasses) propagate normally.

##### Eager Option Handling

Click processes some options eagerly before `AgentFoxGroup.invoke()` completes — specifically options declared with `is_eager=True`, such as `--version`. For eager options, the `OutputManager` may not yet be constructed when the option's callback fires. Eager option callbacks must not call `get_output_manager()` or any function that depends on `ctx.obj` being populated. They should use `click.echo()` directly.

**`af/app.py` `--version` option:** The existing `--version` eager option in `af/app.py` already uses `click.echo()` directly and requires no changes. Implementors must verify during the `BannerGroup` → `AgentFoxGroup` migration that `--version` (and any other eager options) continue to use `click.echo()` directly and do not reference `get_output_manager()`. No new eager options should be introduced that violate this constraint.

##### OutputManager Access in Nested Contexts

`OutputManager` is stored in `ctx.obj["output"]` and is always accessible via `click.get_current_context()`. A module-level convenience accessor is provided:

```python
def get_output_manager() -> OutputManager:
    """Return the active OutputManager.

    Reads ctx.obj["output"] from the current Click context via
    click.get_current_context(). Falls back to a default OutputManager
    (json_mode=False, quiet=False, verbose=False, trace=False) if no
    Click context is active — e.g. in tests or library usage.

    AF_AGENT is NOT consulted in the fallback path. The fallback is
    strictly for non-CLI contexts (tests, library usage) where agent
    mode is irrelevant. AF_AGENT is a CLI-layer concern handled by
    AgentFoxGroup during OutputManager construction.
    """
```

The fallback `OutputManager` (used when no Click context is active) has the following defaults:

| Field | Fallback Default |
|-------|-----------------|
| `json_mode` | `False` |
| `quiet` | `False` |
| `verbose` | `False` |
| `trace` | `False` |

All four fields default to their most conservative/human-friendly values. `AF_AGENT` env var is **not** consulted for the fallback — the fallback is strictly for non-CLI contexts (tests, library usage) where agent mode is irrelevant. This means tests or library callers that set `AF_AGENT=1` globally in their environment will still receive `json_mode=False` from the fallback — this is the intended and confirmed behavior. Tests that need to exercise agent-mode logic should construct an `OutputManager` directly with `json_mode=True` rather than relying on `AF_AGENT`.

This avoids global mutable state while providing convenient access without requiring explicit context propagation. Nested subgroups within `AgentFoxGroup` inherit the same `ctx.obj` automatically via Click's context inheritance; they do not need to re-create or re-fetch the `OutputManager`.

Subcommands access the `OutputManager` exclusively via `ctx.obj["output"]` (populated by `AgentFoxGroup`) or `get_output_manager()`. They do not declare `--json`, `--quiet`, or other common flags — these are declared on the root group only (see §Common CLI Flags Scope).

##### AF_AGENT Precedence Rules

`AF_AGENT=1` sets **defaults only**. Only the exact string value `"1"` activates agent mode — other values such as `"true"`, `"yes"`, `"on"`, `"0"`, or empty string have no effect and agent mode is not activated. This matches common Unix conventions (e.g. `CI=1`, `DEBUG=1`) and is the simplest rule to implement and document.

It is equivalent to the user not having passed any format flags. Explicit CLI flags always take precedence.

**Implementation mechanism:** `--json` / `--no-json` are implemented as a Click option pair. Precedence detection uses a Click callback that calls `ctx.ensure_object(dict)` before setting a sentinel key, so `ctx.obj` is always a dict when sentinels are written. Specifically, the callback sets `ctx.obj["_json_explicit"] = True` whenever the user explicitly passes either `--json` or `--no-json`. `AgentFoxGroup` checks this sentinel during `OutputManager` construction: if the sentinel is set, the explicit flag value wins; if not set and `AF_AGENT=1`, the env default (`True`) is used.

The same sentinel mechanism applies to `--quiet` / `--verbose` — the callback calls `ctx.ensure_object(dict)` then sets `ctx.obj["_quiet_explicit"] = True` when either flag is explicitly passed, preventing `AF_AGENT=1`'s quiet default from overriding an explicit `--verbose`.

The `common_options` decorator wraps each option callback to call `ctx.ensure_object(dict)` before writing any sentinel. This ensures `ctx.obj` is always initialized as a dict before `AgentFoxGroup.invoke()` completes, regardless of decorator ordering or whether a parent group has already set `ctx.obj`.

| Environment | Flags | Effective `json_mode` | Effective `quiet` |
|-------------|-------|-----------------------|-------------------|
| `AF_AGENT=1` | (none) | `True` | `True` |
| `AF_AGENT=1` | `--no-json` | `False` | `True` |
| `AF_AGENT=1` | `--verbose` | `True` | `False` |
| `AF_AGENT=1` | `--no-json --verbose` | `False` | `False` |
| (unset) | `--json --quiet` | `True` | `True` |

This follows the standard convention: environment variables set defaults, CLI flags override.

##### Banner Suppression Rules

`output.banner()` is suppressed whenever any of the following conditions is true:
- `json_mode=True`
- `quiet=True`

Because `AF_AGENT=1` defaults both `json_mode` and `quiet` to `True`, banner is suppressed in agent mode. If a user passes `--no-json` alongside `AF_AGENT=1`, `quiet` remains `True` (from the env default) and the banner is still suppressed. The banner only reappears if quiet is explicitly overridden to `False` (e.g. via `--verbose`) in addition to `--no-json`.

##### Common CLI Flags Scope

`common_options` is applied to the **root group only**. Subcommands do not re-declare `--json`, `--quiet`, `--verbose`, or `--trace`. Instead, subcommands inherit the resolved `OutputManager` via `ctx.obj["output"]` (set by `AgentFoxGroup.invoke()`). This follows the standard Click pattern and eliminates the risk of duplicate flag registration on subcommands.

If `common_options` is accidentally applied to a subcommand (rather than the root group), it raises a `TypeError` at decoration time with a descriptive message (e.g. `"common_options must be applied to the root Click group, not to a subcommand"`). It does not silently warn or proceed, because accidentally applying `common_options` to a subcommand would register duplicate flags that conflict with root-group inheritance.

If a name collision is detected between a common flag and an existing flag on the root group itself, the root group's existing flag takes precedence and `common_options` skips registering the conflicting flag, emitting a debug-level warning to the log.

#### 3. `agentfox/io/json.py` — Unified JSON Serialization

Migrate from `af/json_io.py` and `spec/cli.py`'s inline patterns. Adopt the richer error envelope format from `spec`:

```python
def emit(data: dict) -> None: ...          # Pretty-printed JSON (indent=2), stdout
def emit_line(data: dict) -> None: ...     # Compact JSONL, stdout
def emit_ok(data: dict) -> None: ...       # {"ok": true, ...data} — always overwrites "ok" key
def emit_error(exc: Exception, *, state: str | None = None) -> None: ...
    # Always writes JSON to stdout regardless of json_mode.
    # {"ok": false, "error": {"type": ..., "message": ..., "retryable": ..., ...}, "state": ...}
    # Calls error_envelope(exc, state=state) internally to build the dict, then serializes and prints.
    # See §emit_error Behavior for routing details.
def read_stdin() -> dict: ...              # JSON from piped stdin
```

The error envelope uses the spec-style format everywhere:
```json
{
  "ok": false,
  "error": {
    "type": "config_error",
    "message": "Config file not found",
    "retryable": false
  }
}
```

##### JSON Indentation

`emit()` and `emit_ok()` write pretty-printed JSON using `indent=2`. This matches the current `af/json_io.py` behavior and is the standard convention for CLI tools. `emit_line()` writes compact JSON (no indentation) for JSONL output.

##### JSON Serialization Strategy

`emit()`, `emit_ok()`, and `emit_line()` call `json.dumps()` with `default=str` as a catch-all serializer. Any value that is not natively JSON-serializable (e.g. `datetime`, `UUID`, `Path`, custom objects) is coerced to its string representation via `str()`. This is the current behavior in `af/json_io.py` and is the most pragmatic approach for CLI tools. No `TypeError` is raised for non-serializable values; the string coercion is always applied.

##### BrokenPipeError Handling

All emit functions (`emit()`, `emit_line()`, `emit_ok()`, `emit_error()`) silently suppress `BrokenPipeError`. This occurs when stdout is closed by a downstream pipe consumer (e.g. `af list | head`). Without suppression, a `BrokenPipeError` propagates up and produces an ugly traceback or a spurious error envelope. Suppression is applied by wrapping the `sys.stdout.write()` call in a `try/except BrokenPipeError: pass` block. This is the standard convention for CLI tools.

##### `emit_ok` Key Conflict Behavior

`emit_ok(data)` always overwrites the `"ok"` key with `True`, regardless of whether the caller-supplied `data` dict already contains an `"ok"` key. The caller's value is silently ignored. This is the simplest and safest behavior — it prevents subtle bugs where callers accidentally pass `ok=False` inside the dict.

```python
emit_ok({"ok": False, "result": "done"})
# Emits: {"ok": true, "result": "done"}
```

##### `emit_error` Behavior

`emit_error()` in `json.py` **always writes JSON to stdout**, regardless of the current `json_mode` setting. It is a low-level serialization primitive. Internally, `emit_error()` calls `error_envelope(exc, state=state)` to build the structured dict, then serializes and prints it. This ensures a single source of truth for error type mapping — no duplication of mapping logic exists between `emit_error()` and `error_envelope()`.

Callers responsible for routing (human mode vs. JSON mode) should use `cli_error_handler` in `errors.py`, which checks `json_mode` and either calls `emit_error()` (for JSON output to stdout) or writes plain text to stderr via `click.echo(err=True)` (for human-readable output). This separation keeps `json.py` concern-free with respect to output mode.

Concretely:
- **`json_mode=True`:** `cli_error_handler` calls `emit_error()` → structured JSON envelope to stdout.
- **`json_mode=False`:** `cli_error_handler` calls `click.echo(str(exc), err=True)` → plain text to stderr.

The `handle_cli_errors` decorator in `errors.py` wraps command functions and delegates to `cli_error_handler` with the `json_mode` resolved dynamically from the active `OutputManager` at call time (see §`handle_cli_errors` Decorator).

##### `state` Parameter in `emit_error` and `error_envelope`

The `state` parameter represents the workflow/execution phase the CLI was in when the error occurred. It is a free-form string drawn from a defined set of phase names (e.g. `"planning"`, `"executing"`, `"assessing"`, `"refining"`, `"generating"`). When non-`None`, it is added as a top-level `"state"` field in the error envelope:

```json
{
  "ok": false,
  "state": "executing",
  "error": {
    "type": "agent_error",
    "message": "Model rate limit exceeded",
    "retryable": true
  }
}
```

When `state` is `None`, the `"state"` field is omitted entirely from the output. This carries forward the existing behavior from the `spec` CLI and allows agents to know what phase the CLI was in when the error occurred. Both `emit_error()` and `error_envelope()` accept `state` for consistency; callers that do not track workflow phase simply omit the argument.

##### `read_stdin()` Behavior

`read_stdin()` reads from stdin assuming **UTF-8 encoding only**. If the piped input contains non-UTF-8 bytes, Python will raise a `UnicodeDecodeError` — this is the expected behavior and no special handling is performed. CLI tools communicate via text, not binary, and non-UTF-8 input is treated as a caller error.

| Condition | Behavior |
|-----------|----------|
| stdin is not piped (interactive TTY) | Returns `{}` immediately without blocking |
| stdin is piped but empty | Returns `{}` |
| stdin contains valid UTF-8 JSON | Returns parsed `dict` |
| stdin contains malformed JSON (valid UTF-8) | Raises `json.JSONDecodeError` |
| stdin contains non-UTF-8 bytes | Raises `UnicodeDecodeError` |
| stdin is piped and slow/streaming | Blocks until EOF; no timeout is applied |

`read_stdin()` blocks until EOF for piped stdin — no timeout mechanism is implemented. Callers that require timeout behavior must implement it externally (e.g. using `select` or a threading wrapper). This matches current behavior in `af/json_io.py` and is the least-surprising contract for CLI tools. Callers that need to distinguish "no input provided" from "empty object input" should check whether stdin is a TTY before calling `read_stdin()`.

##### Error Message Extraction

For all exception types — `AgentFoxError` subclasses, `AgentError`, `AgentSpecError`, `SessionError`, `click.ClickException`, and all generic exceptions — the `message` field in the error envelope is derived by calling `str(exc)`. This is simple, consistent, and matches the current behavior in both `af` and `spec` CLIs. No `.message` attribute or `exc.args[0]` is used.

##### Error Type Mapping

Error type derivation uses the exception class hierarchy. For `AgentFoxError` subclasses, the `type` field is always derived from the class name via snake_case conversion (e.g. `ConfigError` → `config_error`). This applies to **all** `AgentFoxError` subclasses, including custom or unknown subclasses — no subclass falls through to `internal_error` solely because its name-to-snake-case mapping is not in a predefined list. The snake_case conversion is applied programmatically to the class name at runtime.

| Exception | `type` field | `retryable` |
|-----------|-------------|-------------|
| `AgentFoxError` subclasses | Derived from class name via snake_case (e.g. `ConfigError` → `config_error`) | Per-class attribute |
| `AgentError` with `.category` | Value of `.category` (e.g. `auth_error`, `rate_limit_error`) | Per-instance attribute |
| `AgentError` without `.category` | `agent_error` | `False` |
| `AgentSpecError` with `.category` | Value of `.category` (same mapping logic as `AgentError`) | Per-instance attribute |
| `AgentSpecError` without `.category` | Derived from class name via snake_case | Per-class attribute (if defined), else `False` |
| `SessionError` | `session_error` | `False` |
| `click.ClickException` | `input_error` | `False` |
| `OSError` / `PermissionError` / `FileNotFoundError` | `internal_error` | `False` |
| `ValueError` / `KeyError` / `TypeError` | `internal_error` | `False` |
| Any other unknown exception | `internal_error` | `False` |

> **Note:** `AgentSpecError` subclasses `AgentError` and already carries a `.category` attribute, so the same `.category`-first mapping logic applies. `SessionError` always maps to `session_error` with `retryable: false` regardless of any attributes. When agentspec is not installed, all agentspec exception types fall through to `internal_error` (see §Package Dependency Graph).

For unknown/unmapped exceptions, the envelope includes a `detail` field with the exception class name to aid debugging:

```json
{
  "ok": false,
  "error": {
    "type": "internal_error",
    "message": "Unexpected error occurred",
    "retryable": false,
    "detail": "ValueError"
  }
}
```

The `detail` field is **only** present for exceptions that fall through to `internal_error`; it is omitted for well-typed `AgentFoxError`, `AgentError`, `AgentSpecError`, and `SessionError` envelopes.

#### 4. `agentfox/io/spinner.py` — Unified Spinner

Merge `PlanSpinner` and `StatusSpinner` into a single `StatusSpinner` class that supports both use patterns:

```python
class StatusSpinner:
    """Context manager for animated progress on stderr.

    Quiet mode: all methods are no-ops.
    Non-TTY: prints plain text lines to stderr instead of animating.
    """
    def __init__(self, message: str, *, quiet: bool = False, theme: AppTheme | None = None): ...
    def __enter__(self) -> StatusSpinner: ...
    def __exit__(self, *exc) -> None: ...
    def update(self, message: str) -> None: ...
    def log(self, message: str) -> None: ...
```

Key behaviors:
- **Non-TTY fallback:** When stdout/stderr is not a TTY, `StatusSpinner` prints plain text lines to stderr instead of animating. Both `update()` and `log()` print a plain text line to stderr in non-TTY mode — this matches the current `StatusSpinner` behavior and ensures all status output is visible in non-interactive environments (e.g. CI, piped output). This applies equally to `log()` as to `update()`: in non-TTY mode, `log()` prints its message as a plain text line to stderr (via `theme.console.print(message)` if a theme is provided, or the fallback `Console(stderr=True).print(message)` otherwise).
- **`theme` parameter:** `AppTheme` already exists in `agentfox/ui/display.py`. `StatusSpinner` uses `theme.console` only for printing `log()` messages with styling. No other `AppTheme` methods are called by `StatusSpinner`. If no `theme` is passed (i.e. `theme=None`), `StatusSpinner` falls back to a `Rich Console(stderr=True)` with no styling for all output. The `log()` method prints its message via `theme.console.print(message)` when a theme is provided, or via the fallback `Console(stderr=True).print(message)` otherwise — no additional Rich markup or style is applied by `StatusSpinner` itself beyond what `theme.console` already defines. In non-TTY mode, `update()` also uses `theme.console.print(message)` (or the fallback console) to print the plain text line.
- **Spinner style:** `StatusSpinner` uses the Rich `"dots"` spinner style in TTY/animated mode. This matches the current `StatusSpinner` implementation in `spec/ui.py` and provides a familiar, broadly-supported animation. The frame rate is governed by Rich's default for the `"dots"` spinner and is not overridden.
- **Single implementation** replaces both `PlanSpinner` and `StatusSpinner`.
- **Thread safety:** `StatusSpinner` uses Rich `Live` internally (matching the current `StatusSpinner` implementation). Concurrent `update()` and `log()` calls are serialized by Rich `Live`'s internal lock. Calling `__exit__` from a different thread than `__enter__` is **not supported** — `__exit__` must be called from the same thread that entered the context manager. This matches Rich's own threading contract.
- Integrates with `LiveAwareHandler` for log routing.

The existing `ProgressDisplay` class (for `af code` orchestration) is **not** merged — it has fundamentally different concerns (task tracking, activity events, multi-line display). It stays in `agentfox/ui/progress.py` but should import `StatusSpinner` from `agentfox/io/spinner.py` if it needs a simple spinner internally.

#### 5. `agentfox/io/errors.py` — Unified Error Handling

Consolidate error-handling patterns:

```python
def handle_cli_errors(fn: Callable) -> Callable:
    """Decorator (no arguments) that catches exceptions and routes to JSON or stderr.

    Wraps the decorated function. When the decorated function raises an Exception
    (not SystemExit or KeyboardInterrupt), resolves json_mode dynamically by calling
    get_output_manager() at call time, then delegates to cli_error_handler().
    Usage: @handle_cli_errors (no parentheses, no arguments).
    """

def error_envelope(exc: Exception, *, state: str | None = None) -> dict:
    """Build structured error dict from any exception.

    Returns the full envelope dict including 'ok': False, 'error': {...},
    and optionally 'state': ... if state is non-None. Does not serialize or print.
    """

def cli_error_handler(ctx: click.Context, exc: Exception) -> None:
    """Top-level error handler for Click groups.

    Checks json_mode on the OutputManager (from ctx.obj["output"] or get_output_manager()).
    - json_mode=True: calls emit_error(exc) → structured JSON to stdout.
    - json_mode=False: calls click.echo(str(exc), err=True) → plain text to stderr.
    In both cases, AgentFoxGroup calls sys.exit(1) after cli_error_handler returns.
    """
```

##### `handle_cli_errors` Decorator

`handle_cli_errors` takes **no arguments** and is applied as `@handle_cli_errors` (no parentheses). It wraps the decorated function and, when an `Exception` is raised (not `SystemExit` or `KeyboardInterrupt`), resolves `json_mode` dynamically by calling `get_output_manager()` at call time, then delegates to `cli_error_handler()`. This ensures that `json_mode` always reflects the runtime-resolved value from the active `OutputManager`, rather than a value baked in at decoration time.

`handle_cli_errors` is intentionally excluded from `agentfox/io/__init__.py` re-exports. It is used internally by `AgentFoxGroup`. Command authors should allow `AgentFoxGroup` to handle errors at the group level rather than applying `handle_cli_errors` directly to subcommands.

This replaces:
- `af/__init__.py::handle_agent_fox_errors()`
- `af/app.py::BannerGroup.invoke()` error handling
- `spec/cli.py::_json_error_exit()`

> **`af` internal symbols being replaced:** The following `af`-internal symbols are superseded by `agentfox/io/` components and must be audited during migration:
> - `af/__init__.py::handle_agent_fox_errors()` → replaced by `handle_cli_errors` / `cli_error_handler` in `agentfox/io/errors.py`
> - `af/json_io.py::emit`, `emit_line`, `emit_error`, `read_stdin` → shimmed via `af/json_io.py` re-exporting from `agentfox/io/json.py`
> - `af/app.py::BannerGroup` (or equivalent) → replaced by `AgentFoxGroup` in `agentfox/io/cli.py`
>
> Any additional `af`-internal symbols that import from or depend on these must be updated or shimmed in Spec 03. Implementors must audit `af/` for all transitive imports before completing the migration.

Error type mapping consolidates all exception hierarchies as described in §Error Type Mapping above:
- `AgentFoxError` subclasses (from agentfox/core/errors.py) — always snake_case class name
- `AgentError` / `AgentSpecError` / `SessionError` (from agentspec) — lazy-imported, graceful fallback if not installed
- `click.ClickException`
- All other exceptions → `internal_error` with `detail` field

`error_envelope()` is part of the public re-export surface of `agentfox/io/__init__.py` (see §Public API). It may be called directly by command implementations that need to construct an error dict without immediately emitting it (e.g. for embedding in a larger response payload).

#### 6. `agentfox/io/cli.py` — Common CLI Flags and Group

Shared Click decorators and group class:

```python
def common_options(fn):
    """Decorator adding --verbose, --quiet, --trace, --json/--no-json to the root Click group.

    Applied to the root group only. Raises TypeError if applied to a subcommand
    (i.e. any Click Command that is not a Group). Subcommands inherit resolved
    settings via ctx.obj["output"]. Each option callback calls ctx.ensure_object(dict)
    before writing sentinel keys, ensuring ctx.obj is always a dict before
    AgentFoxGroup.invoke() completes.
    """

class AgentFoxGroup(click.Group):
    """Click group with banner display, error handling, and OutputManager setup."""
```

Both CLIs use `AgentFoxGroup` instead of reimplementing banner + error handling. The group:
- Creates `OutputManager` and stores it in `ctx.obj["output"]` (via `AgentFoxGroup.invoke()`)
- Renders banner (suppressed per §Banner Suppression Rules)
- Catches `Exception` subclasses and routes through `cli_error_handler`, then calls `sys.exit(1)`; re-raises `SystemExit` and `KeyboardInterrupt` immediately without routing
- Calls `setup_logging()` with the resolved verbosity

`setup_logging(verbose: bool, quiet: bool, trace: bool) -> None` already exists in `agentfox/core/logging.py`. It configures the root logger with the following log level mapping: `trace=True` → TRACE (5), `verbose=True` → DEBUG (10), default → WARNING (30), `quiet=True` → ERROR (40). No changes to `setup_logging()` are required by this spec.

**`ctx.obj` initialization:** Each option callback registered by `common_options` calls `ctx.ensure_object(dict)` before writing any sentinel key. This guarantees `ctx.obj` is a dict when sentinels are written, regardless of decorator ordering or whether a parent group has initialized `ctx.obj`. `AgentFoxGroup.invoke()` also calls `ctx.ensure_object(dict)` at its start before reading sentinels or constructing `OutputManager`. If `ctx.obj` is already a non-dict value set by an upstream parent, `ensure_object(dict)` will not overwrite it — see §ctx.obj Initialization and Conflict Handling.

**Scope:** `common_options` is applied to the root group only. If accidentally applied to a subcommand, it raises a `TypeError` at decoration time. Subcommands do not use `common_options` and do not redeclare `--json`, `--quiet`, `--verbose`, or `--trace`. Name-collision detection and the debug-level warning apply only at the root group level.

#### 7. `agentfox/io/help.py` — `@exit_codes` Decorator and Help Scaffolding

This module contains the `@exit_codes` decorator for metadata storage, and scaffolding for future structured JSON help rendering. **Actual JSON help rendering is deferred to Spec 04**, avoiding the eager-option timing complexity that would arise from detecting `json_mode` before `AgentFoxGroup.invoke()` completes.

In Spec 03, `--help` behavior is **unchanged from the current behavior** — Click's standard formatted text help is shown in all cases, regardless of `--json`. No custom `HelpFormatter` is introduced in this spec.

##### `@exit_codes` Decorator

**Import path:** `from agentfox.io.help import exit_codes` (or `from agentfox.io import exit_codes` via the re-export).

The `@exit_codes` decorator is written **above** `@click.command` — in Python's decorator evaluation order, this means it is applied **last** and receives the already-constructed Click `Command` object as its argument. It sets the exit code mapping as an attribute on that `Command` object:

```python
@exit_codes(**{
    "0": "completed",
    "1": "error",
    "2": "stalled"
})
@click.command()
def code(...): ...
```

To be explicit: `@exit_codes` (outermost/written above) is applied after `@click.command` (innermost/written below) has already constructed the `Command`. Therefore `@exit_codes` receives a `Command` instance — not the raw Python function — and sets `command.exit_codes` directly on it.

**Misuse handling:**
- If `@exit_codes` receives a non-Command object (e.g. it is placed below `@click.command` in the source file, receiving the raw Python function instead), it **raises `TypeError`** with a descriptive message (e.g. `"@exit_codes must be applied above @click.command; received a plain function, not a Click Command"`). This catches the common mistake of reversing decorator order.
- If `@exit_codes` is applied twice to the same command, the **second application overwrites** the first. The second `@exit_codes` call receives the `Command` object (already decorated once) and simply replaces `command.exit_codes` with the new mapping. No merge is performed; no warning is emitted.

Commands that do not use `@exit_codes` simply omit the `exit_codes` field from their JSON help — the field is optional.

##### Structured JSON Help Output (Deferred to Spec 04)

Structured JSON help rendering is deferred to Spec 04. The decision to defer is driven by Click's eager option processing: `--help` fires before `AgentFoxGroup.invoke()` completes, making it impossible to reliably detect `json_mode` via `OutputManager` at help-rendering time without introducing complexity that is out of scope for this spec.

In Spec 04, the mechanism will be specified. The `@exit_codes` decorator introduced in this spec stores metadata on `Command` objects now, so it is ready to be consumed by the Spec 04 help renderer without requiring changes to decorated commands.

The intended future output format (for implementor reference — not a deliverable of this spec):

```json
{
  "name": "code",
  "description": "Execute the task plan",
  "options": [
    {"name": "--dry-run", "type": "bool", "default": false, "help": "..."},
    {"name": "--specs-dir", "type": "path", "default": ".agent-fox/specs", "help": "..."}
  ],
  "exit_codes": {
    "0": "completed",
    "1": "error",
    "2": "stalled"
  }
}
```

### Migration Strategy

The migration is **internal** — no public API changes to `af` or `spec` command-line interfaces. The user-facing commands, flags, and exit codes remain identical. Only the internal implementation moves to the shared module.

1. **Spec 03 (this spec):** Create `agentfox/io/` with all components. Add thin compatibility shims in `af/json_io.py` that re-export from `agentfox/io/json.py` so existing internal imports keep working during migration. Wire `af/app.py` to use `cls=AgentFoxGroup`. If CLI wiring causes blocking regressions, defer wiring to Spec 04 per §Rollback / Abort Policy. `spec/cli.py`'s inline patterns remain entirely unchanged — no shim is introduced for `spec` in this spec.
2. **Spec 04 (af CLI agentic optimization):** Update `af` to import directly from `agentfox/io/`. Delete `af/json_io.py` shim. Apply `af/app.py` wiring if deferred from Spec 03. Address manual table formatting in `standup` and `findings`. Implement structured JSON help rendering (deferred from Spec 03).
3. **Spec 05 (spec CLI agentic optimization):** Update `spec` to import directly from `agentfox/io/`, replacing all inline JSON/error patterns. Wire `spec/cli.py` to use `cls=AgentFoxGroup`. Delete any remaining `spec`-side shims. After Spec 05 ships, `agentfox/io/` is considered stable.

Both CLIs continue to work identically from the user's perspective throughout all three specs. Shims are explicitly temporary scaffolding and must not persist beyond their respective migration spec.

#### `af/json_io.py` Compatibility Shim

The shim re-exports exactly the four symbols currently defined and exported by `af/json_io.py`:

```python
# af/json_io.py — compatibility shim, removed in Spec 04
from agentfox.io.json import emit, emit_line, emit_error, read_stdin

__all__ = ["emit", "emit_line", "emit_error", "read_stdin"]
```

`emit_ok` and `error_envelope` are **not** re-exported from the shim because they are not currently part of `af/json_io.py`'s public surface. Adding them would exceed the shim's purpose of maintaining backward compatibility.

### Testing Strategy

Regression safety is enforced at three levels:

1. **Existing test suites must pass unchanged.** All current `af` and `spec` CLI unit tests and integration tests are run against the migrated code without modification. Any failure is a blocking regression. No tests may be deleted, skipped, or marked `xfail` to satisfy this requirement — the existing test suite is the contract.

2. **New unit tests for each `agentfox/io/` component.** Each of the seven new modules (`output.py`, `json.py`, `spinner.py`, `errors.py`, `cli.py`, `help.py`, `__init__.py`) ships with dedicated unit tests covering:
   - Happy-path behavior
   - Error and exception handling
   - Quiet-mode no-ops
   - Non-TTY fallback: both `update()` and `log()` print plain text lines to stderr (no animation); verified for `StatusSpinner` with `theme=None` and with a theme provided; in non-TTY mode both methods use `theme.console.print(message)` or fallback `Console(stderr=True).print(message)` with no additional markup
   - `AF_AGENT=1` precedence (env default vs. flag override); also verify that `AF_AGENT=true`, `AF_AGENT=yes`, `AF_AGENT=0`, and `AF_AGENT=` (empty string) do **not** activate agent mode
   - `read_stdin()` edge cases (interactive TTY, empty stdin, malformed JSON, non-UTF-8 bytes raising `UnicodeDecodeError`, slow/blocking stdin)
   - `get_output_manager()` fallback when no Click context is active — verifies all four fields: `json_mode=False`, `quiet=False`, `verbose=False`, `trace=False`; also verifies that setting `AF_AGENT=1` in the environment does **not** change the fallback (json_mode remains False)
   - `--help` behavior is **unchanged** in Spec 03 — standard Click text help is shown regardless of `--json`; no JSON help rendering tests are required in this spec (deferred to Spec 04)
   - `state` parameter inclusion/omission in error envelopes
   - Banner suppression under all combinations of `json_mode`, `quiet`, and `AF_AGENT`
   - `emit()` / `emit_line()` with non-JSON-serializable values (e.g. `datetime`, `UUID`, `Path`) — verifying `default=str` coercion
   - `emit()` uses `indent=2`; `emit_line()` uses no indentation
   - `emit_json()` is a no-op when `json_mode=False`; `emit_human()` is a no-op when `json_mode=True`; `emit()` calls `human_fn` when `json_mode=False` and `human_fn` is provided; `emit()` silently no-ops when `json_mode=False` and `human_fn=None`
   - `emit_error()` always writes JSON to stdout (regardless of json_mode); `emit_error()` calls `error_envelope()` internally; `cli_error_handler` routes to `emit_error()` or `click.echo(err=True)` based on json_mode
   - `BrokenPipeError` is silently suppressed in all four emit functions (`emit`, `emit_line`, `emit_ok`, `emit_error`)
   - `emit_ok()` always overwrites the `"ok"` key with `True`, including when caller-supplied data contains `"ok": False`
   - Exit code: `AgentFoxGroup` calls `sys.exit(1)` for all caught `Exception` subclasses; `sys.exit(0)` (implicit) on success; `SystemExit` and `KeyboardInterrupt` are re-raised immediately without routing through `cli_error_handler`
   - `AgentFoxGroup.invoke()` correctly constructs `OutputManager` and stores it in `ctx.obj["output"]`
   - `ctx.ensure_object(dict)` is called by each `common_options` callback before writing sentinel keys; sentinels are set correctly when flags are explicitly passed vs. left at default
   - `ctx.obj` conflict handling: when `ctx.obj` is already a non-dict, `AgentFoxGroup` logs a debug warning and falls back to defaults
   - `setup_logging()` called with correct `verbose`, `quiet`, `trace` arguments derived from resolved flags
   - Eager option callbacks (e.g. `--version`) do not call `get_output_manager()` before `OutputManager` is constructed; specifically verify `af --version` uses `click.echo()` directly
   - `StatusSpinner` fallback to `Console(stderr=True)` when `theme=None`; uses `theme.console` when theme is provided; `log()` and `update()` (in non-TTY mode) call `console.print(message)` with no additional markup; uses `"dots"` Rich spinner style in TTY mode
   - `@exit_codes` decorator sets attribute on the Click `Command` object (not on the underlying Python function); raises `TypeError` if applied to a non-Command object; second application overwrites first (no merge)
   - `handle_cli_errors` resolves `json_mode` dynamically via `get_output_manager()` at call time, not at decoration time
   - `agentspec` lazy import: `agentfox/io/errors.py` functions correctly when agentspec is not installed (agentspec exceptions fall through to `internal_error`)
   - `_json_explicit` and `_quiet_explicit` sentinel values correctly set when flags are explicitly passed vs. left at default
   - `AgentFoxError` subclasses (including unknown custom subclasses) always produce snake_case `type` field from class name
   - `common_options` raises `TypeError` when applied to a subcommand (non-Group Click object); subcommands receive `OutputManager` via `ctx.obj["output"]` only
   - `agentfox/io/__init__.py` re-exports all twelve listed public symbols and no others; `handle_cli_errors` is not in the public surface and requires direct submodule import

3. **Property tests for error envelope serialization (using Hypothesis).** The project already uses Hypothesis for property-based testing. Property tests verify that any `AgentFoxError`, `AgentError`, `AgentSpecError`, `SessionError`, or `click.ClickException` can be serialized to a valid unified error envelope. "Valid" means:
   - `type` is always a non-empty string
   - `message` is always a non-empty string (derived from `str(exc)`)
   - `retryable` is always a boolean
   - `detail` is present if and only if the exception falls through to `internal_error`
   - `state` is present in the top-level envelope if and only if a non-`None` value was passed
   - `emit_error()` output is always valid JSON (parseable by `json.loads()`) and matches the dict returned by `error_envelope()` for the same inputs

   **Hypothesis strategy:** Property tests generate exception instances using Hypothesis `@given` strategies. For `AgentFoxError` subclasses, the strategy generates instances of known subclasses with arbitrary string messages. For unknown/custom subclasses, a dynamic subclass is created at test time (e.g. `type("CustomFoxError", (AgentFoxError,), {})`) with Hypothesis-generated messages to verify the snake_case class name derivation. For `AgentError` and `AgentSpecError`, the strategy generates instances with and without `.category` attributes set.

   **agentspec dependency in CI:** Property tests covering `AgentError`, `AgentSpecError`, and `SessionError` are guarded with `pytest.importorskip("agentspec")` at the top of the relevant test module or test function. This causes those tests to be automatically skipped in environments where agentspec is not installed, matching the lazy-import behavior of `agentfox/io/` itself. No separate CI matrix entry is required; the skip guard is sufficient.

   Deserialization back to an exception object is **not** a requirement.

All tests must pass in CI before this spec is considered complete.

### Non-Goals

- **Table formatting utility.** Manual table formatting in `standup` and `findings` is deferred to Spec 04 and is not addressed in this spec.
- **ProgressDisplay refactoring.** The orchestrator's multi-task progress display (`agentfox/ui/progress.py::ProgressDisplay`) is not part of this spec. It is specific to `af code` orchestration.
- **Report content changes.** Standup/findings report content and aggregation logic are unchanged. Only the output formatting layer is unified.
- **agentspec error hierarchy.** The `agentspec` package's exception classes (`AgentError`, `AgentSpecError`, `SessionError`) are not modified. The unified error handler knows how to serialize them.
- **Shim removal.** Compatibility shims introduced in this spec are removed in specs 04 and 05, not here.
- **Coverage thresholds.** Minimum code coverage percentages are not specified by this spec. Passing all existing tests is sufficient.
- **`read_stdin()` timeout.** No timeout mechanism is implemented for slow/streaming piped stdin. Callers requiring timeouts must implement them externally.
- **`spec/cli.py` migration.** `spec`'s inline JSON/error patterns are not touched in this spec. Migration of `spec` is deferred entirely to spec 05.
- **`setup_logging()` changes.** The existing `setup_logging(verbose, quiet, trace)` in `agentfox/core/logging.py` requires no modifications.
- **CI/CD pipeline definition.** The spec does not define CI environment, runner configuration, or Hypothesis test gating. These are infrastructure concerns managed outside the spec.
- **Performance/latency constraints.** No import-time, startup overhead, or spinner frame rate requirements are specified. The module should be no slower than the code it replaces, but no formal benchmarks are required.
- **`StatusSpinner` cross-thread `__exit__`.** Calling `__exit__` from a different thread than `__enter__` is not supported and not tested. Callers must ensure context manager entry and exit occur on the same thread.
- **`agentfox/io/` external stability.** The public API of `agentfox/io/` carries no stability guarantee until Spec 05 ships.
- **Structured JSON help rendering.** Deferred to Spec 04. `--help` behavior is unchanged in this spec; Click's standard text help is emitted in all cases.

## Tech Stack

- Python 3.12+
- Click >=8.1 (CLI framework)
- Rich >=13.0 (terminal formatting)
- Hypothesis (property-based testing, already used in project)
- Existing `agentfox`, `af`, `spec`, `agentspec`, `afspec` packages

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| (none) | — | — | No upstream spec dependencies |

## Source

Source: Input provided by user via interactive prompt
