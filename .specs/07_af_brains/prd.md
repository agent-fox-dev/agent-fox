---
spec_id: "07"
spec_name: "af_brains"
title: "The Brain interface, the local box and the submission protocol"
status: "active"
created_at: "2026-10-20T07:51:23.542701Z"
updated_at: "2026-10-20T07:51:23.542701Z"
intent_hash: "67acc01f40f17bd896933b5ca66f1511ef209a44d9c82ba5594f343421699d49"
schema_version: 2
source: "docs/prd/02-rebuild-triage-and-fix-on-the-toolbelt.md"
---
## Intent

Give `af` one way to run a brief through a brain. Any brain implements a single `Brain` interface: today the in-process AgentKit driver and a scripted replay, later a headless harness. The loop holds a box around the brain's tree: the checkout goes in with `.git`, the brief and run files are placed under `.agent-fox/`, and the tree comes back out without `.git`. A brain hands back exactly one thing, a JSON submission. `af` validates it and writes it to a file, and never parses it from the brain's output. `af triage` and `af fix` are loops built on these pieces. A contract test runs one brief through every available brain and checks that the same submission arrives, and that test is what keeps the interface honest.

## Goals

1. **Same submission from every brain.** A test runs one brief over a `local` box through the `scripted` brain and through the `agentkit` brain on `provider/faux`. Both runs leave the same payload bytes in `<run_dir>/submission.json`. Both report `Submitted` true. The envelope's `usage.cost_source` is `none` for scripted and `measured` for agentkit.
2. **The box invariant holds, in tests.**
   - `git rev-parse --short HEAD` inside the box equals the host's HEAD at sync time.
   - A commit made inside the box is not on the host's branch.
   - The host's HEAD, index and working tree are unchanged after the brain wrote files inside.
   - The exported tree has no `.git` and contains the brain's edits.
   - With `GITHUB_TOKEN`, `GITLAB_TOKEN` and `ANTHROPIC_API_KEY` set on the host, `Spec.Env` is empty for the `agentkit` and `scripted` brains.
3. **A refused submission never exists.**
   - The submit path (the `submit` command factory and the in-process submit) rejects a bad payload naming every problem at once, writes nothing, and leaves an earlier valid file untouched.
   - If a brain writes the submission file itself and the file is invalid or stale, the host treats the run as having no submission.
4. **A denied tool call is never executed.** This covers a call outside `Brief.Tools` and a call the ring-1 guard denies. The denial reason is the brain's next turn, for both in-process brains.
5. **The box is always closed.** That holds after success, failure, a panic in the brain, and cancellation. A failed close is a warning, not a status change.
6. **Checks and docs.** `make check` passes offline with no key and no network. The docs listed in §10 describe what this scope ships.

## Non-goals

- **The other box providers, the `claude` brain and in-box agentkit** (scope `af_boxes_claude`):
  - the `landlock`, `podman` and `none` providers;
  - the `claude` brain, with its settings file and hook;
  - starting the agentkit driver *inside* a box with one `execute` of `af <agent> run`, with `--borrowed-hands` as the exception flag;
  - the env test that adds the vendor key to `Spec.Env`.

  In this scope the `agentkit` brain always runs on the host over the box's tools, which is the only placement that makes sense with `local`.
- **The agent commands and their submission kinds.** The triage brief, the `file_diagnosis` kind and its validation, `rejected_path_calls`, and the `af triage <input>|brief|run|submit|land` commands belong to `af_triage`. The fix brief, the implementation-report kind, and `af fix <input>|brief|run|submit|land` belong to `af_fix`. This scope supplies the `submission.Kind` type, the submit core, and the command factory those scopes register.
- **What happens after the brain.** Measuring, attempts, the host-side commit from the exported tree, landing and parking all belong to `af_fix`. This scope exports the tree and stops there.
- **The guard's rules.** What the ring-1 guard allows or denies is `af_ring1_facts`'s. This scope only feeds tool calls to it.
- **Other features.** Resuming a brain's transcript, `af serve` (MCP), code mode, and a streaming `Exec`.
- **Brief rendering.** The content of a brief is the agent scopes' job. This scope places what it is given.

## Background

PRD 02 §3 and `docs/architecture.md` (*The brain interface*, *The box*) define the shape:

- The loop belongs to the brain. Every effect belongs to `af`.
- The box holds the tree. Git and the credentials stay on the host.
- The submission "arrives through `af … submit`, inside the box, as `.agent-fox/run/<id>/submission.json` … read by the host through `read_file` when the brain's process ends".

PRD 01 D8 names the two first-release brains: the AgentKit driver, testable offline with `provider/faux`, and a headless harness.

What exists in the repository today:

- **No brain, box or submission code, and no `cmd/af` or `internal/toolio`.** The last two come from spec `05_af_cli_envelope`, which is specified but whose tasks are still pending. The ring-1 commands and the guard come from `af_ring1_facts` (`.specs/06_af_ring1_facts/`, still being generated).
- **agentkit-go use.** It is consumed through `replace … => ../agentkit-go`, outside the repository. The only symbol used anywhere is `tools.ReducedEnv(nil)` (`internal/gitx/exec.go:46`, `internal/gitx/hermetic.go:37`). So every sandbox, driver, runner and faux API this scope needs is unverified (see *Verified External API*). `go.sum` lists `anthropic-sdk-go`, which suggests the driver's production provider is Anthropic's.
- **`internal/gitx` gives what the box checks need:**
  - `New(dir, Runner)`;
  - `(*Git).IsRepo`;
  - `(*Git).Head`, which returns the short hash;
  - real-git tests built on a `newRepo(t)` helper.

  It has no `--show-toplevel` method.
- **`issuex` is not used by this scope.**

## Requirements

### 1. The contract types (`internal/brain`)

`internal/brain` declares the interface and its values:

```go
type Brain interface {
	Run(ctx context.Context, b Brief) (Outcome, error)
}

type Brief struct {
	RunID          string
	Attempt        int               // 1-based
	Kind           submission.Kind   // what the brain submits and how it is validated
	Prefix         string            // the run-constant part, byte-identical across attempts and resumes
	Task           string            // the task block that follows it
	Tools          []string          // the allowlist of box tool names
	Hook           HookConfig
	Box            *box.Session      // never nil in this scope
	Env            map[string]string // for a brain process started inside the box; empty for in-process brains
	MaxCostUSD     float64
	MaxTurns       int
	Timeout        time.Duration     // 0: bounded by ctx only
	TranscriptPath string            // host path the brain may write a transcript to
	Observe        func(ToolCall)    // may be nil
}
func (b Brief) Document() string // Prefix + Task

type Outcome struct {
	Submitted    bool    // the brain's claim; af decides from the file
	StopReason   string  // submitted | ended | budget | turns | error
	CostUSD      float64
	CostSource   string  // measured | reported | none
	Turns        int
	InputTokens  int
	OutputTokens int
	Transcript   string  // a path; never read by af
}

type HookConfig struct {
	RunID          string   `json:"run_id"`
	ProtectedPaths []string `json:"protected_paths"` // always contains ".agent-fox/"
	AllowPrograms  []string `json:"allow_programs"`  // added to the programs the ring-1 guard allows
}

type ToolCall struct {
	Brain, Tool, Decision, Reason string // Decision: allow | deny
	OK                            bool
	DurationMS                    int64
}
```

`Outcome` is used for exactly two things:

- the envelope's `usage`;
- the `submission_claim_mismatch` warning (§5).

Whether a submission exists is decided only from the file (§4, §5).

### 2. Choosing a brain and a box

**The brain registry.**

- `brain.Registry` holds `Entry{Name, CostSource, BoxEnv []string, New func(Options) (Brain, error)}`.
- `Options` carries:
  - `Model` (from `AF_MODEL`; empty means the driver's default);
  - `Script` (the `--script` path);
  - `NewProvider`, an injectable factory for the driver's model provider (faux in tests);
  - `Getenv`.
- The default registry has `agentkit` and `scripted`.
- Looking up an unknown name is a `usage` failure that lists the registered names.

**The flags helper.** `brain.Flags` registers `--brain`, `--sandbox`, `--budget <usd>`, `--max-turns <n>` and `--script <path>` on a command's `flag.FlagSet`, using defaults the calling command supplies. These are `usage` errors:

- a budget that is not a finite number greater than 0;
- `--max-turns` below 1;
- `--brain scripted` without `--script`;
- `--script` given with any other brain.

**The box registry.**

- `box` keeps a registry of providers by name, with `local` registered.
- An unknown `--sandbox` value is a `usage` failure that lists the registered providers.
- The helper's documented default is `local` until `af_boxes_claude` registers more providers.
- Every run that uses `local` raises the warning `box_unconfined` once, saying the box has no process or network isolation.

**The box environment.** The box's `Spec.Env` (and `Brief.Env`) holds exactly the host values of the names in the selected entry's `BoxEnv`, and nothing else. `BoxEnv` is empty for `agentkit` and `scripted`. No host variable reaches the box by any other path this scope controls.

### 3. Holding the box (`internal/box`)

**Opening.** `box.Open(ctx, Request{Provider, HostDir, RunID, Tool, Env, TTL, Record, Warn})` returns a `*Session`.

- `HostDir` must be the top level of a git work tree. If it is not a work tree, or is not its top level, that is a `usage` failure. The message names the top level when there is one. Paths are compared after `filepath.EvalSymlinks`.
- The session creates the sandbox with `Spec{Env, Labels: {af.run_id, af.tool}, TTL}`.
- It syncs `HostDir` in with `.git`, so `git log` and `git diff` inside are true.
- It records the ledger entries `box_open {provider}` and `box_sync {head}`.
- A provider that cannot open is `failed`/`sandbox` with `retryable: false`.
- A sync failure is `failed`/`sandbox` with `retryable: true`, and the box is closed before returning.

**Session methods:**

| Method | What it does |
|---|---|
| `Provider()` | The provider name. |
| `Spec()` | The spec the box was created with. Tests use it to check `Env`. |
| `Tools()` | The box's tool set, for brains. |
| `Sync(ctx)` | Re-syncs the host directory and records `box_sync`. |
| `FS(ctx) fs.FS` | A read-only view of the box's tree, built on the box's read tools. A missing path gives `fs.ErrNotExist`. A directory stats as a directory. |
| `WriteFile(ctx, rel, data)` | Writes only paths that pass `fs.ValidPath` and lie under `.agent-fox/`. Anything else is an internal error, because the session writes run files and nothing else. |
| `Export(ctx) (dir, error)` | See below. |
| `Close()` | Idempotent. Removes the session's export directories and records `box_close`. |

**Exporting.** `Export(ctx)` exports the box's tree into a fresh directory under the OS temporary directory (`af-export-<run_id>-*`), never inside the checkout.

- If `<dir>/.git` exists afterwards, that is `failed`/`sandbox` ("export carried .git"), and the directory is removed.
- An export records `box_export {path}`.

**Closing on every path.** `box.With(ctx, req, fn func(*Session) error) error` opens a session, calls `fn`, and closes the session on every path: a normal return, an error, a panic (re-raised after the close), and a cancelled context.

- The close runs on a context detached from cancellation and bounded to 30 seconds.
- A close error raises the warning `box_close_failed` through `req.Warn` and does not change the result.

**No host writes.** The session never writes the host checkout. The host copies of run files are written by §5 into the run directory.

### 4. The submission protocol (`internal/submission`)

**Kinds.** A kind is:

```go
type Kind struct {
	Name, ToolName, Description string
	Schema   json.RawMessage // JSON Schema of the payload
	Validate func(ctx context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []Problem)
}
type Problem struct{ Path, Message string }
```

`Validate` returns the normalised payload, or every problem it found. The protocol sorts problems by `Path`, then by `Message`.

**The file.**

- Inside the tree, the submission is `.agent-fox/run/<id>/submission.json`. It holds `{kind, run_id, attempt, nonce, submitted_at, payload_sha256, payload}`, where `payload` is the normalised payload compacted.
- The `attempt` and `nonce` values come from `.agent-fox/run/<id>/run.json` (`{run_id, kind, attempt, nonce}`), which §5 places before each brain run.
- Writes are atomic: a temporary file in the same directory, then a rename. Through the box's write tool, a write is as atomic as that tool makes it.

**The submit core.** `submission.Submit(ctx, Target{Tree fs.FS, Write, RunID}, kind, raw, now)` is the one path to a submission file. It refuses with `*Refused{Problems}`, writes nothing, and leaves an existing file untouched when:

- the payload is larger than 1 MiB;
- it is empty;
- it is not a single JSON object;
- `run.json` is missing or names another kind;
- `Validate` reports problems.

Otherwise it writes the file and returns `Receipt{Path, SHA256}`. Each accepted submission replaces the one before it: the last valid submission wins.

**The command factory.** `submission.Command(path string, kind Kind) toolio.Command` builds the `af <agent> submit` command that `af_triage` and `af_fix` register.

- It reads the payload as raw JSON from stdin.
- It takes `--dir`, the tree root (default `.`), and `--run-id`, which is required and opened with `toolio.OpenRunDir`. A missing or invalid id is a `usage` failure.
- It validates against the tree at `--dir`, opened with `os.OpenRoot` so that symlinks cannot escape the root.
- On success its status is `done`, with result `{run_id, kind, path, sha256}` and an artifact `{kind: "submission", path}`.
- A refusal, including malformed JSON, is `failed` with category `submission` and `retryable: true`. `error.message` names every problem.
- It neither creates a run directory nor opts into `--dry-run`.
- Its result schema is declared so that it appears in `af --schema`.

### 5. Running a brief

`brain.Execute(ctx, Request{Session, Name, Brain, Brief, RunDir, Report, Tally, Heartbeat, Rand})` runs one brief through one brain in a held box and returns `Result{Outcome, Payload, Path}`. `Report` is an interface that `*toolio.Run` satisfies: `Emit`, `Warn`, `Artifact`, `SetUsage`.

**Before the brain runs:**

1. Draw a 16-hex nonce.
2. Write these into the box with `Session.WriteFile`:
   - `.agent-fox/.gitignore` (`*`);
   - `.agent-fox/run/<id>/brief.md` (`Document()`);
   - `run.json`;
   - `hook.json` (`HookConfig`, with `.agent-fox/` added to `ProtectedPaths` if it is missing).
3. Write host copies to `<run_dir>/brief.<attempt>.md` and `<run_dir>/hook.json`.
4. Record `brief_placed {attempt, bytes, sha256, prefix_sha256}`.
5. Set `TranscriptPath` to `<run_dir>/transcript.<attempt>.jsonl`, and `Observe` so that each call emits a `tool_call` event.

**While the brain runs:**

- Apply `Brief.Timeout` when it is greater than 0.
- Emit `step` events for `brief`, `brain` and `submission`, and a `heartbeat {brain, elapsed_ms}` every `Heartbeat` (default 30 s).
- Record `brain_start`, call `Brain.Run`, then record `brain_end {brain, stop_reason, submitted_claim, cost_usd, cost_source, turns, duration_ms}`.
- A panic inside `Brain.Run` is recovered as `failed`/`brain`. The stack goes to `<run_dir>/brain-panic.<attempt>.txt`.
- The returned `Outcome` is added to `Tally`, which accumulates across attempts, and `SetUsage` is called with the total (`brain` is the entry name). This happens even when `Run` returned an error, because the cost was spent.

**After the brain:**

1. Read the box's `submission.json` through `Session.FS`. If the context expired, use a fresh 30-second context.
2. The file counts only if all of these hold:
   - it parses;
   - `kind`, `run_id`, `attempt` and `nonce` match this run;
   - `payload_sha256` matches the payload;
   - `Kind.Validate` accepts the payload against the box's tree. The re-validated payload is the authoritative one.
3. A file that passes is copied atomically to `<run_dir>/submission.json`. The artifact `{kind: "submission", path}` is added, and `submission_accepted {sha256}` is recorded.
4. A file that fails raises the warning `submission_invalid` (with its problems), records `submission_invalid`, and counts as absent.
5. A stale nonce records `submission_stale` and counts as absent.
6. A missing file records `submission_absent`.
7. When the brain's `Submitted` claim differs from the host's decision, the warning `submission_claim_mismatch` is raised.
8. A brain transcript, if one exists, is added as the artifact `{kind: "transcript", path}`.

**The result.**

- A valid submission wins. A brain error that came with one becomes the warning `brain_error`.
- Without a valid submission, Execute fails with the first of these that applies:

| Situation | Status / category | Retryable | `fix_hint` |
|---|---|---|---|
| `Brief.Timeout` expired | `failed`/`timeout` | — | `--timeout`, twice the value |
| The context was cancelled, or the shell's own deadline passed | the context error is returned to the shell | — | — |
| `Run` returned an error | `failed`/`brain` | true | — |
| `StopReason` is `budget` | `failed`/`budget` | false | `--budget`, twice the value |
| `StopReason` is `turns` | `failed`/`budget` | false | `--max-turns`, twice the value |
| Anything else | `failed`/`no_result` | true | — |

Nothing is ever parsed from the brain's output.

### 6. Tool calls from in-process brains

Both in-process brains send every tool call through one gate:

- **The allowlist.** A tool not in `Brief.Tools` is denied with "tool X is not in this run's allowlist". The terminating submit tool is always available.
- **Unknown names.** A name in `Brief.Tools` that the box does not provide is an internal failure at the start of `Run`, before any model call or step.
- **The guard.** Each allowed call goes to the ring-1 guard's Go decision function (from `af_ring1_facts`) together with `Brief.Hook`.
- **Run files.** Any write whose path resolves under `.agent-fox/` is denied, by the guard through `ProtectedPaths` or by the gate itself.
- **Denials.** A denied call is not executed. Its reason is returned to the brain as the tool's error result, which is the brain's next turn.
- **Reporting.** Each call produces one `Observe(ToolCall)` and one transcript line, `{seq, tool, args, decision, reason, ok, result}`, with `args` and `result` each truncated to 4096 bytes. `tool_call` events carry no arguments and no results.

### 7. The scripted brain

- **The script.** `--script` names a JSON file `{"steps": [...]}`. Each step is exactly one of `{"tool": name, "args": {...}}` or `{"submit": <payload>}`, with an optional `"expect"`:
  - `ok`, `error` or `denied` for a tool step;
  - `accepted` or `refused` for a submit step.

  A file that cannot be read, that has unknown fields, or that has a step with both forms or neither is a `usage` failure when the brain is constructed, before any box opens.
- **The replay.** `Run` replays the steps in order on the host:
  - tool steps go over `Brief.Box.Tools()` through the §6 gate;
  - submit steps go through `submission.Submit` against the session (`Tree = Session.FS`, `Write = Session.WriteFile`).
- **Expectations.** A step whose actual result differs from its `expect` makes `Run` return an error that names the step index, the expected result and the actual one.
- **Stopping.**
  - An accepted submission ends the replay (`StopReason` `submitted`), and the remaining steps are listed as skipped in the transcript.
  - Each step is one turn. Reaching `MaxTurns` first gives `turns`.
  - Running out of steps gives `ended`.
- **Usage.** Cost is 0 with `cost_source: none`. `Turns` is the number of steps run, and the transcript is written.

### 8. The agentkit brain

**Construction.** The `agentkit` brain builds the driver with `agentkit.New` on the host (borrowed hands), over the session's tools:

- the provider comes from `Options.NewProvider` (faux in tests). In production it is agentkit's provider for `AF_MODEL`, with the vendor's credentials from the environment;
- `Config.Prefix` is `Brief.Prefix`;
- the tools are the allowlisted box tools plus a terminating tool named `Kind.ToolName`, with `Kind.Description` and `Kind.Schema` as its input schema;
- the cost bound is `MaxCostUSD` and the turn bound is `MaxTurns`;
- the before-tool-call interceptor is the §6 gate.

**Running.** The first user message is `Brief.Task`.

- The terminating tool calls `submission.Submit` against the session.
- A refusal returns every problem as the tool's error result, and the loop continues.
- An acceptance ends the loop: through agentkit's terminating-tool mechanism, or by cancelling the driver's context when there is no such mechanism. The result is `StopReason: submitted`.

**The outcome.** Cost, turns and tokens come from the driver, with `cost_source: measured`. The driver's stop is mapped to `budget`, `turns` or `ended`.

**Credentials.** A provider that cannot be built because a credential is missing is `failed`/`auth` with `fix_hint {env: "ANTHROPIC_API_KEY"}`. This is raised when the brain is constructed, before any box opens.

### 9. The contract and invariant tests

**The Brain contract test.** `internal/brain/contract_test.go` is a table with one row per available brain: `scripted` with a script file, and `agentkit` with a faux script. `af_boxes_claude` adds `claude`, skipped when there is no key. Each row runs the same brief, with a test kind whose validator checks that a cited path exists in the tree, over a `local` box on a temporary repository. Each row asserts:

1. the same normalised payload in `<run_dir>/submission.json`;
2. an invalid first submission whose problems reach the brain as its next turn, while no file exists until the valid one;
3. a call outside the allowlist, and a write under `.agent-fox/`, both denied and not executed;
4. `Outcome.Submitted` equal to the host's decision.

**The box invariant tests**, using `Session.Tools()` directly to stand in for a brain with its own hands and no hook:

- `git rev-parse --short HEAD` inside equals `gitx.Head` on the host;
- a commit made inside leaves the host's HEAD and `git status` unchanged;
- a file written inside is absent from the host checkout, present in `Export`, and the export has no `.git`;
- `Spec().Env` is empty with host tokens set;
- `box.With` closes the box after a returned error, after a panic and after cancellation, observed through a counting provider wrapper;
- a stale `submission.json` from attempt 1 is not accepted in attempt 2.

**Submission and command tests.** Unit tests cover:

- the submit core's refusals (each one, all problems at once, an earlier file kept);
- the command factory's envelope;
- the scripted brain's `expect` mismatches;
- the flag validation;
- the `budget`, `no_result` and `timeout` mappings.

### 10. Documentation

Run `make doc` first. It produces nothing for these internal packages, but the steering requires the run. Then:

- **`docs/architecture.md`:**
  - fill in *The brain interface* as built: the fields that differ from the sketch, the registry, and the submission file with its nonce;
  - fill in the run-directory layout additions: `brief.<attempt>.md`, `hook.json`, `submission.json`, `transcript.<attempt>.jsonl`, the box-side `run.json`, and the ledger kinds of §3 and §5;
  - mark the agentkit placement as host-side in this release.
- **`docs/cli.md`:**
  - the error categories `sandbox`, `brain`, `no_result`, `budget` and `submission`;
  - the warnings `box_unconfined`, `box_close_failed`, `submission_invalid`, `submission_claim_mismatch` and `brain_error`;
  - the shared brain and box flags and the `submit` command contract, both used by `af triage` and `af fix`.
- **`docs/configuration.md` and `docs/model-usage.md`:** `AF_MODEL`, the vendor key the agentkit brain reads (`ANTHROPIC_API_KEY`), how cost is measured or reported per brain, and the fact that `local` is unconfined.
- **`docs/development.md`** (create it if it does not exist): the scripted brain's script format, and how to write a faux-driven test.
- **`README.md`:** add `internal/brain/`, `internal/box/` and `internal/submission/` to *What is here*.

## Design Decisions

1. **This scope builds the agentkit brain on the host over `Box.Tools` only.** With `local` the two placements are the same thing. PRD 01 §7 allows borrowed hands for `--local`, and the in-box placement needs a real box and an `af <agent> run` command, neither of which exists in this scope. It lands with `af_boxes_claude`.
2. **`Brief.Box` is `*box.Session`, not agentkit's `sandbox.Sandbox`.** Brains need the run-file writes, the read view and the tools together. Keeping the unverified sandbox API behind one adapter limits how far a signature mismatch can spread.
3. **`Brief` splits `Prefix` and `Task`, with `Document()` joining them.** The driver needs the run-constant part separately for `Config.Prefix`, and later scopes must be able to assert it byte for byte (`prefix_sha256` in the ledger).
4. **The host reads the submission through the box's read tools and re-validates it against the box's tree.** `docs/architecture.md` says the host reads it with `read_file`. A brain with its own hands could write the file directly, and an exported tree may or may not carry the ignored `.agent-fox/`.
5. **A per-brief nonce in `run.json` makes a stale submission count as absent.** A re-sync between attempts, or a file left by a previous attempt, must never look like this attempt's result, whatever the sync does with `.agent-fox/`.
6. **The last valid submission wins, and a refusal never overwrites.** A harness may submit several times. The agentkit and scripted brains stop at the first acceptance.
7. **A refused submission is `failed`/`submission` and retryable, including malformed JSON.** The brain's remedy is the same in every case, and a failure carries no `result` in the 05 envelope, so the problems go in `error.message`.
8. **The submission payload is capped at 1 MiB.** That is far above any diagnosis or report, and it bounds what a brain can make `af` hold.
9. **Exports go to a temporary directory outside the checkout, removed by `Close`.** An export inside `.agent-fox/` would be carried back into the box by the next sync if the sync does not exclude it, and that cannot be verified.
10. **An export that carries `.git` is a hard `sandbox` failure.** It breaks the invariant the design rests on, so the run stops rather than repairing it silently.
11. **`--dir` must be the work tree's top level.** `.git` must be at the root of what is synced for `git` inside to be true, and the run directory lives there.
12. **The box's environment is an allowlist per brain entry (`BoxEnv`), empty for `agentkit` and `scripted`.** This gives the "only the vendor key" invariant one place to be enforced and tested before `claude` adds its key.
13. **`local` is the documented default with a `box_unconfined` warning until `af_boxes_claude` makes `landlock` the default on Linux.** Without it no default would work in this split, and the warning keeps the missing isolation visible in every envelope.
14. **In-process brains call the ring-1 guard's Go function, and `hook.json` mirrors `HookConfig` in the box.** One rule set decides for the hook and for the interceptors, as `docs/architecture.md` requires (*The policy hook*). The file is what a hook running inside will read.
15. **Writes under `.agent-fox/` are denied to brains.** Only `af` writes the brief, `run.json` and the submission. Denying the brain those paths keeps "validated before it exists" true for in-process brains.
16. **`tool_call` events carry no arguments. The transcript does, truncated to 4 KiB.** Events go to hub audit later and arguments can hold file contents. The transcript is a local file that `af` never reads.
17. **New failure categories: `sandbox`, `brain`, `no_result`, `budget`, `submission`.** `no_result` and `sandbox` are PRD 01 §6.4's names. `budget` covers both the cost bound and the turn bound, and the `fix_hint` says which flag to raise.
18. **A valid submission wins over a brain error or a stop reason.** The submission is the fact. An error after it changes nothing that `af` measured, so it becomes a warning.
19. **Usage accumulates across attempts in a `Tally` and is recorded even when the brain failed.** The cost was spent, and the envelope reports the run, not the last attempt.
20. **The scripted brain is available in the binary, not only in tests.** PRD 02's first acceptance criterion runs `af fix … --brain scripted` from the command line. `expect` turns a script into a test oracle.
21. **A missing vendor credential is `auth`, detected when the brain is constructed.** It fails before a box is opened or a tree synced, with the variable named in `fix_hint`.

## Dependencies

| Spec | Relationship | Why |
|---|---|---|
| `05_af_cli_envelope` | depends on | The things it consumes: `toolio.Command` and `toolio.Run` (`Emit`, `Warn`, `Artifact`, `SetUsage`); `toolio.RunDir` (`ID`, `Path`, `Record`) and `OpenRunDir`; `*toolio.Failure` with `FixHint` and its category mapping; the envelope's `usage` fields `brain`, `cost_source` and `turns`; the `run_dir` artifact. The submit factory builds a `toolio.Command`. |
| `06_af_ring1_facts` | depends on | Two things: the ring-1 guard's Go decision function, which the §6 gate calls with `HookConfig`, and `Run.Stdin`, which that scope adds for `af guard check` and which the submit factory reads. |

## Verified External API

Only the import path `github.com/agent-fox-dev/agentkit-go/tools` and the call `tools.ReducedEnv(nil) []string` are seen in the repository (`internal/gitx/exec.go:46`). The module itself is replaced by `../agentkit-go`, outside the repository, so every agentkit symbol below is **unverified**: it is taken from `docs/architecture.md`, `docs/prd/01`, `docs/prd/02` and `docs/prd/03`.

| Symbol | Assumed shape | Status |
|---|---|---|
| `sandbox.Open` | opens a provider by name (`"local"`) | unverified |
| `Provider.Create` | `Create(ctx, sandbox.Spec) (sandbox.Sandbox, error)` (PRD 03 §6: `Create(sandbox.Spec{Labels})`) | unverified |
| `sandbox.Spec` | `Image`, `Env map[string]string`, `Network`, resources, `TTL`, `Labels map[string]string`, `RequireLandlock` | unverified |
| `sandbox.Sandbox` | `Sync(ctx, src)`, `Export(ctx, dst)` that never exports `.git`, `Tools()` returning `tools.All` as proxies, `Close() error` | unverified |
| `sandbox.LocalDir` | `struct{ Path string }`, used as the sync source and the export destination | unverified |
| `local` provider | in-process, no isolation, with its own workspace directory filled by `Sync` | unverified. If it works in place on the source directory, the box invariant tests fail and an erratum is due |
| `agentkit.New` / `agentkit.Config` | the driver, with `Config.Prefix`, `MaxCostUSD`, a model provider and model, tools, a turn bound, and a `BeforeToolCall` interceptor; its run takes the first user message and reports cost, turns and tokens | unverified |
| `agentkit.Toolset`, `agentkit.NewRunner`, `Runner.Call` | calling tools without a model: `Call(ctx, name, args)` | unverified |
| `provider/faux` | a scripted provider that returns a sequence of model turns (text or tool calls) | unverified |
| box tool names | `read_file`, `write_file`, a listing tool, `execute` | unverified |
| `internal/gitx` | `func New(dir string, r Runner) *Git`; `(*Git).IsRepo(ctx) bool`; `(*Git).Head(ctx) (string, error)` (short hash) | read in `internal/gitx/git.go` |
