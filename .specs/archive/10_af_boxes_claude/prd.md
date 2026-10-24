---
spec_id: "10"
spec_name: "af_boxes_claude"
title: "af boxes and the claude brain: landlock, podman and none, the host-side commit under each, and headless Claude Code"
status: "active"
created_at: "2026-10-20T08:34:06.792553Z"
updated_at: "2026-10-20T08:34:06.792553Z"
intent_hash: "73f6d3f093ebb891097b8b2fd3c90f3aa800b47bf04d4c374c82b093d1339122"
schema_version: 2
source: "docs/prd/02-rebuild-triage-and-fix-on-the-toolbelt.md"
---
## Intent

Let `af triage` and `af fix` put the brain's tree in a real box on a developer's machine, or knowingly in no box, and let Claude Code run headless as a brain. `--sandbox landlock|podman|none` join `local` behind the box registry. Under every one of them the loop keeps the invariants the design rests on:

- `.git` goes in and never comes out;
- the commit that counts is made on the host, from facts `af` measured;
- nothing that holds the brain holds a credential other than the model vendor's key.

Under `none` there is no box, so the hook and `af`'s checks after the run are the boundary. `claude` is one more row behind the existing `Brain` interface. It gets the brief, an allowlist, and a settings file whose hook is `af guard check`. It submits through `af <agent> submit` and reports its cost from its own JSON output.

## Goals

1. **Selecting a box.**
   - `--sandbox` accepts `landlock`, `podman`, `local` and `none`. An unknown value is `usage`, and the message lists all four.
   - Without `--sandbox`, the provider comes from `AF_SANDBOX`. Without that, it is `landlock` on Linux and `podman` on any other OS.
   - The tests of `08_af_triage` and `09_af_fix` that relied on the old `local` default still pass, because they now pass `--sandbox local`.
2. **Three brains, one submission, offline.** `af triage <issue URL> --sandbox local` is run against an `httptest` GitHub three times, with `--brain scripted`, `--brain agentkit` (on `faux`) and `--brain claude` (a fake `claude` executable first on `PATH`). All three leave the same normalised diagnosis bytes in `<run_dir>/submission.json`. The claude run reports `usage.cost_source: reported`, with the cost and turns the fake wrote.
3. **Only the vendor key crosses, offline.** With `GITHUB_TOKEN`, `GH_TOKEN`, `GITLAB_TOKEN` and `ANTHROPIC_API_KEY` all set on the host:
   - `Spec.Env` for `claude` is exactly `{ANTHROPIC_API_KEY}`, and for `scripted` and for `agentkit --borrowed-hands` it is empty;
   - the fake `claude` process, under `local` and under `none`, sees `ANTHROPIC_API_KEY` and none of the three forge-token variables.
4. **The hook is wired, offline.** The `PreToolUse` command in the claude settings file is run with Claude Code payloads:
   - a `Bash` call of `go test ./...` gets a deny that names `af gate run`;
   - a `Write` to `.agent-fox/run/<id>/submission.json` gets a deny;
   - a `Bash` call of `git diff` gets `{}`.
5. **`none` lands from the checkout itself, offline.**
   - `af fix <issue URL> --brain scripted --sandbox none` lands `fix/<N>-<slug>` with one commit that carries the `Agent-Fox-Run:` trailer. `Export` is never called.
   - When a commit is made on the base branch during the brain's run, it is gone from the base branch afterwards, its change is inside `af`'s commit, and the warning `foreign_commits_reset` is raised.
6. **Real boxes and real Claude, opt-in.** Each of these runs only when its variable is set and its prerequisite is present. Otherwise it is skipped with a reason, never failed.
   - `AF_TEST_LANDLOCK=1` on Linux with Landlock ABI 4 or newer.
   - `AF_TEST_PODMAN=1` with a reachable engine and the image.
   - `AF_TEST_CLAUDE=1` with `ANTHROPIC_API_KEY` set and `claude` on `PATH`.

   They cover:
   - spec 07's box invariant tests under each provider;
   - `af fix` end to end under `landlock` and `podman`, with an edit, an addition, a deletion and an executable-bit change all arriving in `af`'s commit;
   - the contract row with the real `claude`.
7. **Checks and docs.** `make check` passes offline with no key, no engine and no network. The docs named in R10 describe what this scope ships.

## Non-goals

- **The service's boxes.** The `gateway` provider, hub leases, `AF_SANDBOX_PROFILE` and `--workspace` belong to PRD 03 to 05.
- **Other harness brains.** `pi`, `codex` and `opencode` are not in this release.
- **Claude Code with borrowed hands.** That is `--tools ""` with tools served by `af serve` or `mcpserve`.
- **Streaming `Exec`.** There is no way to stop a running Claude Code process when it crosses the cost bound, and no progress events while it runs.
- **Copying transcripts out of the box.** Neither Claude Code's session transcript nor the in-box `af … run` transcript is copied to the host.
- **Narrower network rules.** Egress filtering down to the vendor is not done. Podman's network is all or nothing.
- **Per-run resource settings.** There are no CPU or memory flags and no `--image` flag.
- **Other ways to authenticate inside a box.** Claude subscription tokens, Bedrock and Vertex credentials, and `ANTHROPIC_BASE_URL` are not supported there.
- **The default brains.** They do not change. Triage stays on `agentkit`; fix keeps the default `09_af_fix` chose. `claude` is chosen with `--brain claude`.
- **Checking the tree after triage under `none`.** Triage commits nothing. The claude allowlist and the hook keep writers out.
- **Windows support for `none`.** A process brain under `none` runs through `sh`.
- **Publishing the tools image** from CI.
- **Changing `issuex`.**

## Background

PRD 02 §2, §3, §7 and §8 step 6 describe this scope. So do `docs/architecture.md` (*The box*, *Where the brain sits*, *What the box makes physical*, *The image*, *The brain interface*) and PRD 01 (D4, D8, D10, §7):

- **Placement.** The tree lives in the box, and git and the credentials stay on the host. A brain with its own hands runs inside the box with only the vendor key. A brain that borrows hands runs on the host over the box's tools.
- **`--local`.** PRD 01 §7 makes `--local` the one place the driver may borrow hands. PRD 02 §3 makes in-box the agentkit placement and `--borrowed-hands` the exception.
- **`none`.** "With `none`, the host's clone is the tree and the protected-path revert and the foreign-commit check are the invariant."

Spec `07_af_brains` explicitly hands this scope four things: the three providers, the `claude` brain with its settings file and hook, starting the driver inside a box with one `execute` of `af <agent> run` together with `--borrowed-hands`, and the env test that adds the vendor key to `Spec.Env`.

What exists, or is specified, to build on:

- **Spec 07 (`internal/box`, `internal/brain`, `internal/submission`; tasks pending):**
  - `box.Open`, `box.With` and `*box.Session` with `Provider`, `Spec`, `Tools`, `Sync`, `FS`, `WriteFile` (only under `.agent-fox/`), `Export` and `Close`;
  - a provider registry that holds `local` and raises `box_unconfined`;
  - `brain.Registry` with `Entry{Name, CostSource, BoxEnv, New}` and `Options{Model, Script, NewProvider, Getenv}`;
  - `brain.Flags` (`--brain`, `--sandbox`, `--budget`, `--max-turns`, `--script`);
  - `brain.Execute`, which places `brief.md`, `run.json {run_id, kind, attempt, nonce}` and `hook.json` and decides the submission only from the file;
  - the categories `sandbox`, `brain`, `no_result`, `budget` and `submission`;
  - a contract test table that "`af_boxes_claude` adds `claude` to".
- **Spec 06:**
  - `af guard check [--dir] [--policy] [--host claude]` reads a Claude Code `PreToolUse` payload. It answers `{}` for allow and a `permissionDecision: "deny"` document for deny, always exits 0, and fails closed.
  - The policy file format is `{"version": 1, "suite", "protected_paths", "allow_programs"}`, and a missing key takes its default.
  - Read-only git subcommands are allowlisted. `af <agent> brief|submit` are allowed to brains. `run`, `land` and the loop are denied.
  - Spec 06 leaves "the Claude Code settings file that installs `af guard check` as a `PreToolUse` hook" and the three boxes to this scope.
- **Spec 08:**
  - The triage loop runs `brain.Execute` with the selected `--brain` and `--sandbox`.
  - `af triage run --run-id <id>` is "the process a box executes". It runs the driver in-process over `--dir` and takes the prefix length from `triage.json`.
  - The brief renders the submission route `af triage submit --run-id <id>` for any brain that is not in-process.
- **`09_af_fix` (PRD written, package being generated; the directory is empty):**
  - applies the brain's exported tree to the host;
  - runs the protected-path revert, the gate after the change and the revert check;
  - commits with the `Agent-Fox-Run:` trailer, and parks work that does not land.
- **`internal/gitx` (read):**
  - `Runner` is `func(ctx, dir, argv, stdin...) (out string, code int, err error)`.
  - `ExecRunner` uses the full environment. `ReducedEnvRunner` uses `tools.ReducedEnv(nil)`, which strips the vendor keys and `*_TOKEN`, `*_SECRET`, `*_API_KEY` and `*_PASSWORD`.
  - The unexported `run` puts the command in its own process group, kills the group on cancel (`proc_unix.go`), and sets `WaitDelay` to 2 s.
  - `Head` returns the short hash. `CurrentBranch` returns `"HEAD"` when detached.
  - `ResetSoft(ctx, ref)`, `MergeBase(ctx, ref)` and `CommitsSince(ctx, since)` exist.
  - There is no full-hash helper and no ancestor check.
- **Containers.**
  - `containers/sandbox/Containerfile` builds `quay.io/agentfox/sandbox` from `quay.io/agentfox/agentkit-sandbox:latest`, adding `specr`, `golang`, `gcc` and `git`, with `WORKDIR /workspace` and `USER 1001:0`.
  - There is no tools image in the repository. `docs/architecture.md` names `quay.io/agentfox/tools` (the sandbox image plus `af` and Claude Code) as `Spec.Image` for brains with their own hands.
  - The `Makefile` has `build-sandbox-container` and `build-containers`, and no tools target.
- **agentkit-go** is replaced by `../agentkit-go`, outside the repository. The only agentkit symbol used in the code is `tools.ReducedEnv`, so the sandbox API this scope needs is unverified, as it was for spec 07.

## Requirements

### R1. Choosing the box

- **The registry.** Spec 07's box registry gains `landlock`, `podman` and `none` beside `local`. An unknown `--sandbox` value is `usage`, and the message lists all four names.
- **The default.** The provider is the first of these that applies:
  1. `--sandbox`;
  2. `AF_SANDBOX`, validated the same way, where an unknown value is `usage` naming the variable;
  3. `landlock` when `runtime.GOOS` is `linux`, and `podman` otherwise.

  When the default provider cannot open, `af` never falls back to `local` or `none`. The provider's failure (R3, R4) carries a `fix_hint` naming `--sandbox` and `AF_SANDBOX`.
- **Provider properties.** Each provider declares two things:
  - whether it isolates: `landlock` and `podman` do, `local` and `none` do not;
  - whether it works in place: only `none` does.
- **Warnings.** Spec 07's `box_unconfined` warning is raised once per run for `none` as well as for `local`. For `none` the message says that the brain works on the checkout itself and that the hook is the only guard.
- **`--borrowed-hands`.** `brain.Flags` gains this flag.
  - It is `usage` with any brain other than `agentkit`.
  - It changes something only with an isolating provider (R9). Under `local` and `none`, agentkit always runs on the host.

### R2. The box spec for a run

One function computes the box request from four inputs: the brain entry, the provider, `--borrowed-hands`, and the agent's `Brief.Tools`. The triage and fix loops open their box with it.

- **Placement.**
  - `host`: `scripted`, and `agentkit` under `local`, under `none`, or with `--borrowed-hands`.
  - `box`: `claude` under `local`, `landlock` and `podman`, and `agentkit` under `landlock` and `podman` without `--borrowed-hands`.
  - `checkout`: `claude` under `none`.
- **`Spec.Env`.** It holds credentials and nothing else, as exactly the host values of the names listed:
  - empty for `host` placement;
  - `ANTHROPIC_API_KEY` for `box` and `checkout` placement.

  This placement-dependent list replaces spec 07's static `BoxEnv` and keeps its rule that no host variable reaches the box by any other path.
- **Network.**
  - `none` when the placement is `host` and `Brief.Tools` holds no shell tool (`execute`);
  - `open` otherwise.
  - Under `local` and `none` the value is recorded as `host`, meaning nothing is enforced.
- **Image.** `podman` only: `AF_SANDBOX_IMAGE`, else `quay.io/agentfox/tools:latest`.
- **TTL.** The time left on the command's deadline plus 10 minutes.
- **Landlock.** For `landlock`, `RequireLandlock` is set.
- **Labels.** As in spec 07.

The ledger entry `box_open` gains `image`, `network` and `placement`. The `af fix` result gains `box: {provider, image, network}`, and the schema golden is regenerated.

### R3. `landlock`

- **Checks before the sandbox is created:**
  - On an OS other than Linux, the status is `failed`/`sandbox`, `retryable: false`, with the message "landlock needs Linux" and `fix_hint {flag: "--sandbox", suggest: "podman"}`.
  - On Linux, `af` reads the Landlock ABI with the `landlock_create_ruleset` version query, behind a function tests can replace. An ABI below 4, or no Landlock at all, is `failed`/`sandbox`, `retryable: false`. The message names the ABI found and the ABI needed. The `fix_hint` is the same.
- **Opening.** The sandbox is created through agentkit's `landlock` provider with R2's spec. It follows spec 07's sync, `Export` and `Close` contract unchanged: synced in with `.git`, exported without it, and an export that carries `.git` is a failure.
- **Processes.** A process started inside runs from the host's filesystem, so its binaries are resolved on the host (R7).

### R4. `podman` and the tools image

- **Failures:**
  - An engine that cannot be reached is `failed`/`sandbox`, `retryable: false`. The message carries the provider's error. The `fix_hint` names `--sandbox`, and on an OS other than Linux the message mentions `podman machine start`.
  - An image that is missing and cannot be pulled is `failed`/`sandbox`, `retryable: true`. The message names the image and `AF_SANDBOX_IMAGE`.
- **The image's contents.** The image supplies `af` and `claude`. When the image's `af --version` differs from the running binary in `version` or `commit` (R7's probe), the warning `box_af_version_mismatch` is raised and the run continues. The host re-validates every submission.
- **`containers/tools/Containerfile`** is restored:
  - its base is this repository's sandbox image (`ARG BASE_IMAGE`, default `quay.io/agentfox/sandbox:latest`);
  - `af` is built from this repository's build context, with agentkit-go cloned at `ARG AGENTKIT_REF` into the path the `replace` directive expects;
  - Claude Code is installed at `ARG CLAUDE_CODE_VERSION`;
  - it keeps `WORKDIR /workspace` and `USER 1001:0`.
- **The Makefile.** `make build-tools-container` tags `$(CONTAINER_REGISTRY)/tools:$(VERSION)` and `:latest`. `build-containers` builds it after the sandbox image.

### R5. `none`: the checkout is the tree

`box.Open` with `none` keeps spec 07's top-level check. It returns a session that works in place:

| Member | Behaviour |
|---|---|
| `Tools()` | The hands' tool set over the checkout itself. |
| `FS(ctx)` | A read-only view of the checkout through `os.OpenRoot`. |
| `WriteFile` | Spec 07's rule: only paths under `.agent-fox/`. Writes atomically on the host. An existing `.agent-fox/.gitignore` is not overwritten. |
| `Sync` | Records `box_sync {head}` and copies nothing. |
| `Export` | Returns an internal error, "in place: nothing to export". |
| `InPlace()` | `true`. It is `false` for every other provider. |
| `Root()` | The checkout's path. For other providers, the workspace root as a process inside the box sees it. |
| `Spec()` | Network `host`, and `Env` as in R2. |
| `Close` | Records `box_close`. |

**Submission parity.** Under an in-place session, the box-side run directory and the host run directory are the same directory, so spec 07's `Execute` handles the submission file like this:

- Before the brain runs, it keeps the bytes of the `<run_dir>/submission.json` the host last accepted, if there is one.
- If this attempt's file is accepted, it is rewritten with the same bytes.
- If it is not accepted, it is moved to `submission.rejected.<attempt>.json` when it differs from the kept bytes. The kept bytes are restored, or no file is left.

So `<run_dir>/submission.json` only ever holds a submission the host accepted, exactly as under `local`.

### R6. The host-side commit under each provider

- **Isolating providers and `local`.** `09_af_fix`'s apply step reads the tree through `Session.Export` exactly as it does for `local`, unchanged. The opt-in tests of R10 prove the following for `landlock` and `podman`:
  - a modified file, a new file, a deleted file and an executable-bit change made inside the box all arrive in `af`'s single commit with the trailer;
  - a commit made inside the box is not on the branch;
  - files the box's user wrote are owned by the host user after apply.
- **`none`.** The apply step checks `Session.InPlace()` and skips export and apply. Before each brain run, the loop records HEAD as a full hash, together with the checked-out branch. After the brain ends, and before anything is measured, it compares them:
  - **Unchanged.** The run continues.
  - **Same branch, and the recorded HEAD is an ancestor of the current HEAD.** The loop runs `git reset --soft <recorded>`, raises the warning `foreign_commits_reset {count}`, and records it in the ledger. The change is then measured and committed by `af` as usual.
  - **Anything else** (another branch, a detached HEAD, rewritten history). The status is `failed`/`git`, not retryable. The message names the recorded and current branch and HEAD. The tree is left untouched, and nothing is landed or parked.
- **The protected-path revert.** Under `none`, `09_af_fix`'s revert runs directly on the checkout.
- **Triage** is unaffected.

### R7. Brains that run as a process

This section covers `claude`, and `agentkit` placed in the box.

- **`Session.Exec(ctx, command, timeout)`** returns the exit code and the output, capped. A non-zero exit is not an error.
  - Box providers run the command through the box's `execute` tool, at `Root()`.
  - `none` runs it with `sh -c` on the host at the checkout, through a new exported `gitx` runner that takes an explicit environment. That runner keeps the process-group kill. The base environment is `tools.ReducedEnv(nil)` plus `Spec.Env`.
- **The probe.** Once per session, before the first process-brain run:
  - `<af> --version` runs, and `<claude> --version` for `claude`;
  - `HOME` is checked to be set and writable.

  A failure is `failed`/`sandbox`, `retryable: false`. The message names the binary, the provider and the image. Each probe is recorded as `box_probe {binary, ok, version}`.
- **Which binaries.**
  - Under `local`, `landlock` and `none`:
    - `af` is the running executable, by absolute path; tests may override it through `Options.AFPath`, and the executable's directory goes first on `PATH`;
    - `claude` is `exec.LookPath("claude")` on the host.
  - Under `podman`, both are taken by name from the image's `PATH`.
- **Environment.** The command runs under `env -i` with only these:
  - `PATH`: the host `PATH` with the `af` directory first, under `local`, `landlock` and `none`; the box's own `PATH` under `podman`;
  - `HOME`;
  - those of `LANG`, `LC_ALL`, `TZ`, `TMPDIR`, `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` (in either case), `SSL_CERT_FILE`, `SSL_CERT_DIR` and `NODE_EXTRA_CA_CERTS` that are set;
  - the credential names from `Spec.Env`, referenced as shell parameters so that no secret appears in the command text.

  Non-secret `af` settings, such as `AF_MODEL`, go on the command line inline. Every argument is single-quoted.
- **Input and output.**
  - Stdin is the placed `brief.md`, or nothing.
  - Stdout and stderr are redirected by `af`'s own command line to `.agent-fox/run/<id>/<brain>.<attempt>.json` and `.stderr`.
  - After the process exits, both files are read through `Session.FS` and copied to the host run directory: the JSON up to 1 MiB, and the last 64 KiB of stderr. Each copy is added as the artifact `{kind: "brain_output", path}`.
  - Nothing in these files decides whether a submission exists.
- **Timeout.** The exec timeout is `Brief.Timeout`, or the time left on the context. When it expires, the process is killed.
- **The ledger.** Each run records `brain_process {brain, exit_code, duration_ms}`.

### R8. The `claude` brain

- **The registry entry** is `claude`, with cost source `reported` and the command submission route. The agent briefs render `af <agent> submit --run-id <id>` for it.
- **Construction**, before any box opens:
  - Unless the provider is `none`, `ANTHROPIC_API_KEY` is required. Without it the status is `failed`/`auth` with `fix_hint {env: "ANTHROPIC_API_KEY"}`.
  - Under `local`, `landlock` and `none`, `claude` must be on the host `PATH`. Otherwise the status is `failed`/`brain`, `retryable: false`, with a message naming `claude`.
- **The files placed for each attempt**, through `WriteFile`, with host copies in the run directory:
  - **`guard.json`**, in spec 06's format: version 1, `protected_paths` and `allow_programs` taken from `Brief.Hook`, and `suite` omitted so that it defaults from detection.
  - **`claude-settings.json`**: one `hooks.PreToolUse` entry that matches every tool. Its command is `<af> guard check --host claude --dir <root> --policy <root>/.agent-fox/run/<id>/guard.json`, with a timeout of 30 s.
- **The command line:**

  ```
  <claude> -p --output-format json --max-turns <MaxTurns> --permission-mode dontAsk
           --settings <settings> --strict-mcp-config --allowedTools <…> --disallowedTools <…>
           --append-system-prompt <note> [--model <AF_MODEL>]  < brief.md
  ```

  The note is a fixed text. It says:
  - the run is headless;
  - the only deliverable is one submission, through the command the brief names;
  - never commit, push or write to the forge;
  - a refusal is the next turn.
- **Tool mapping**, defined in one table:

  | Box tool | Claude Code tools |
  |---|---|
  | `read_file` | `Read` |
  | `list_files` | `LS`, `Glob` |
  | `find_files` | `Glob` |
  | `search_files` | `Grep` |
  | `write_file` | `Write` |
  | `edit_file` | `Edit`, `MultiEdit` |
  | `execute` | `Bash` |

  - Navigation tools with no equivalent are dropped, and the ledger records it.
  - `Bash(af <agent> submit:*)` is always allowed.
  - `WebFetch` and `WebSearch` are always disallowed. So are `Write`, `Edit`, `MultiEdit` and `NotebookEdit` when `Brief.Tools` has no writer.
- **The outcome**, read from the output JSON:

  | Outcome field | Source |
  |---|---|
  | `CostUSD` | `total_cost_usd`, else `cost_usd` |
  | `Turns` | `num_turns` |
  | `InputTokens` | the sum of `usage.input_tokens`, `cache_creation_input_tokens` and `cache_read_input_tokens` |
  | `OutputTokens` | `usage.output_tokens` |
  | `StopReason` | from `subtype`: `success` gives `ended`, `error_max_turns` gives `turns`, any subtype containing `budget` gives `budget`, anything else gives `error` |
  | `Submitted` | whether the box's `submission.json` carries this attempt's nonce |
  | `Transcript` | empty |

  The ledger records `claude_result {subtype, is_error, num_turns, total_cost_usd, usage, session_id}`.
- **Edge cases:**
  - A missing or unparseable output file means cost 0, `cost_source: none`, and the warning `brain_usage_unavailable`.
  - A non-zero exit with no valid submission makes `Run` return an error carrying the last 2 KiB of stderr.
  - A reported cost above `MaxCostUSD` raises the warning `budget_exceeded`, and sets `StopReason` to `budget` when nothing was submitted.

### R9. `agentkit` inside a box

- **Running inside.** Under `landlock` and `podman` without `--borrowed-hands`, the `agentkit` brain runs through R7 as:

  ```
  <af> <agent> run --run-id <id> --dir <root> --budget … --max-turns … --timeout …
  ```

  with `AF_MODEL` given inline and `Spec.Env` set to `{ANTHROPIC_API_KEY}`. Without the key, the status is `failed`/`auth` at construction, as spec 07 already requires for the host placement.
- **`run.json`.** Spec 07's `Execute` adds `prefix_bytes`, the byte length of `Brief.Prefix`, to `run.json`. The submit core keeps ignoring fields it does not use. `af triage run` and `af fix run` take `brief.md` and `prefix_bytes` from `run.json` when their run-state file (`triage.json` or `09_af_fix`'s equivalent) is absent. They need nothing else from the host's run directory.
- **The outcome**, read from the envelope written to `.agent-fox/run/<id>/agentkit.<attempt>.json`:
  - cost, tokens and turns come from `usage`, with `cost_source: measured`;
  - the claim comes from `result.submitted`;
  - a failure with category `budget` gives `budget` or `turns`, according to its `fix_hint` flag;
  - any other failure gives `error`;
  - an unparseable envelope is handled as in R8.
- **With `--borrowed-hands`**, the brain keeps spec 07's host placement over `Session.Tools()`, with `Spec.Env` empty.

### R10. Tests and documentation

**Offline tests, in `make check`:**

- **A fake `claude`.** A test executable placed first on `PATH`, driven by a JSON script. It:
  - records its argv, environment, stdin and settings file;
  - runs the settings file's hook command with given payloads and records each answer;
  - runs `af <agent> submit` (a binary built in `TestMain`) with given payloads;
  - writes an output JSON.

  It is used for:
  - the three-brain contract at the `af triage` level;
  - the environment invariant under `local` and `none`;
  - the hook wiring;
  - the outcome mapping: success, maximum turns, missing output, over budget;
  - the construction and probe failures.
- **A recording test provider.** It asserts R2's spec for every combination of brain, provider and `--borrowed-hands`. It also exercises R9's command and envelope parsing with a fake `af`.
- **Selection and provider failures.** The default selection for each OS and `AF_SANDBOX`. R3's refusals through an injected ABI function. R4's failure mapping through a stub provider.
- **`none`:**
  - the in-place session;
  - submission parity;
  - a foreign-commit reset and a foreign-commit refusal, made through a stub brain that commits or switches branches;
  - `af fix` end to end with `scripted`.
- **Existing tests.** Tests in `cmd/af` and `internal/*` that relied on the `local` default pass `--sandbox local`.

**Opt-in tests**, gated by `AF_TEST_LANDLOCK`, `AF_TEST_PODMAN` and `AF_TEST_CLAUDE` as in Goal 6, and calling `t.Skip` with a reason otherwise:

- spec 07's box invariant tests for each provider;
- `af fix` end to end with `scripted` under `landlock` and `podman`, with R6's set of changes;
- the contract row with the real `claude` under `local`, and under `podman` when both variables are set.

**Docs.** Run `make doc` first. Then update:

- **`docs/cli.md`:**
  - the `--sandbox` values and the default;
  - `--borrowed-hands` and `--brain claude`;
  - the warnings `foreign_commits_reset`, `box_af_version_mismatch`, `brain_usage_unavailable` and `budget_exceeded`, and `box_unconfined` under `none`;
  - the `fix` result's `box`.
- **`docs/configuration.md` and `docs/model-usage.md`:**
  - `AF_SANDBOX` and `AF_SANDBOX_IMAGE`;
  - which credential enters which box;
  - `AF_MODEL` mapped to `--model`;
  - claude's cost, which is reported and checked against the budget only after the run;
  - the network for each placement.
- **`docs/architecture.md`.** Fill in the *Sections to be written* that this scope settles: the host settings for each brain, the `Spec` for each brain, and the tools image. Also record the placement table as built.
- **`docs/development.md`:** the opt-in variables and the fake-`claude` script format.
- **`README.md`:** `containers/tools/`.

## Design Decisions

1. **The default box is `AF_SANDBOX`, else `landlock` on Linux and `podman` elsewhere, and there is never a silent fallback to `local` or `none`.** PRD 02 makes `landlock` the Linux default. An unconfined default would quietly remove the boundary that goal 4 promises.
2. **`landlock` requires ABI 4 and fails closed.** ABI 4 is the first with network rules, and PRD 02 §7 names it. A box that cannot confine the network when asked for `none` would break the borrowed-hands guarantee.
3. **`none` is an in-place `Session`, not a nil `Brief.Box`.** Every brain and both loops keep one code path. `InPlace()` is the single question the apply step asks.
4. **`Export` under `none` is an internal error rather than returning the checkout.** A caller that forgets to branch then fails loudly instead of copying the tree onto itself.
5. **Under `none`, `<run_dir>/submission.json` only ever holds an accepted submission.** The box-side and host paths are the same file. Moving a rejected file aside and restoring the earlier bytes keeps the behaviour identical to `local` for `land --run-id` and for the next attempt.
6. **Foreign commits under `none` are soft-reset when they sit linearly on the same branch, and anything else is refused without touching the tree.** A soft reset keeps the work and makes `af`'s commit the one that counts. Undoing a branch switch or a history rewrite on a person's own checkout is not something to guess at.
7. **`agentkit` runs inside `landlock` and `podman` by default, with `--borrowed-hands` as the exception.** This is what PRD 02 §3 specifies, and it exercises locally the placement the service (PRD 03) requires.
8. **`Spec.Env` holds credentials only, chosen by placement.** It is empty for brains on the host and `ANTHROPIC_API_KEY` for process brains. Non-secret settings (`PATH`, `AF_MODEL`) go on the command line. "Only the vendor key" stays one testable rule.
9. **Process brains start under `env -i` with an explicit allowlist.** `local` and `none` do not filter the environment themselves. The allowlist gives every provider the same invariant, and references credentials by name so that their values never reach the ledger.
10. **The network is `none` only for host-placed brains without a shell tool, and `open` otherwise.** A brain inside needs its vendor, and a brain that runs tests needs module downloads. Podman offers only all or nothing, which the architecture accepts.
11. **The image comes from `AF_SANDBOX_IMAGE`, defaulting to `quay.io/agentfox/tools:latest`, with no flag, and `containers/tools/Containerfile` is restored.** The image is machine configuration, not a per-run choice. Without a recipe in the repository, `podman` with `claude` has no image.
12. **Inside `local`, `landlock` and `none`, `af` is the running executable; inside `podman` it is the image's, with a version-mismatch warning.** A same-host box can run the same binary. Container images can drift, but the host re-validates every submission, so drift is a warning, not a failure.
13. **The probe runs once per session, before the first process brain.** A missing `claude` or `af`, or an unwritable `HOME`, fails before any money is spent, and with a clear category.
14. **The whole brief goes to Claude Code on stdin, and the skill is not appended again; a fixed headless note is the appended system prompt.** Linux limits a single argument to 128 KiB, and the brief can exceed that. The brief's prefix (spec 08 R3, and `09_af_fix`'s) already carries the skill. This departs from PRD 02 §3's "the skill as the appended system prompt".
15. **The Claude allowlist is a single mapping table, with `dontAsk`, `WebFetch` and `WebSearch` always disallowed, and writers disallowed for read-only agents.** One table can be tested. Read tools need no permission in Claude Code, and web tools are a vector for prompt injection.
16. **The hook is `af guard check --host claude --policy <run>/guard.json`, with `guard.json` generated from `Brief.Hook` in spec 06's format.** Spec 07's `hook.json` is not in spec 06's policy format. Generating the file makes the hook and the in-process gate decide from the same rules.
17. **Claude's cost is `reported`, and input tokens include cache tokens.** PRD 02 §10 says a harness reports its cost. Cache reads are tokens the run consumed.
18. **For `claude`, the USD budget is checked after the run. `--max-turns` and the timeout are the bounds while it runs.** There is no streaming `Exec` to stop the process mid-run, and `budget_exceeded` says so plainly.
19. **`ANTHROPIC_API_KEY` is required for `claude` except under `none`.** A box has a private `HOME` with no login. A person at their own checkout may be logged in to Claude Code.
20. **Output and stderr are redirected by `af`'s own command line into the run directory, then copied to the host.** The brain's hook denies writes under `.agent-fox/`. The host reads the files through `Session.FS`, and they never decide a submission.
21. **The tests that need real boxes or real Claude are opt-in through `AF_TEST_*` variables.** `make check` must stay offline and free even on a machine that has a key, an engine or a recent kernel.
22. **The `claude` brain's `Submitted` claim is "a `submission.json` with this nonce exists".** Claude Code makes no claim of its own, and a constant `false` would raise `submission_claim_mismatch` on every success.
23. **`--strict-mcp-config` is passed.** A repository's own MCP configuration should not start servers inside an unattended run.
24. **`run.json` gains `prefix_bytes`, and `af <agent> run` falls back to it.** A box receives only what `Execute` places, and the driver needs the prefix boundary for `Config.Prefix`.
25. **The default brains do not change.** A default that needs a vendor CLI installed would break `af fix` out of the box. PRD 01's `AF_BRAIN` is the service's setting.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `05_af_cli_envelope` | depends on | Uses the coded warnings, the `usage` block (`cost_source: reported`), `*toolio.Failure` with `fix_hint`, the `--schema` golden that the `fix` result's `box` changes, and `Run.Warn` and `Artifact`. |
| `06_af_ring1_facts` | depends on | Uses `af guard check --host claude --policy`, the `guard.json` format, and the policy that allows `af <agent> submit` and denies mutating git. The settings file invokes them. |
| `07_af_brains` | depends on; modifies | Extends the box registry (three providers, `InPlace`, `Root`, `Exec`), the brain registry (`claude`; placement-dependent credential lists replace `BoxEnv`), `brain.Flags` (`--borrowed-hands`, the new default), the `box_open` ledger fields, `Execute` (`prefix_bytes` in `run.json`, in-place submission parity), and the contract test (the `claude` row). |
| `08_af_triage` | depends on; modifies | The triage loop opens its box with R2's spec. `af triage run` falls back to `run.json` inside a box. The claude brain needs the command submission route. Its tests pass `--sandbox local`. |
| `09_af_fix` | depends on; modifies | The apply step branches on `InPlace()` and gains the foreign-commit check. The `fix` result gains `box`. `af fix run` falls back to `run.json`. The fix loop opens its box with R2's spec. Its tests pass `--sandbox local`. |

## Verified External API

| Symbol | Package | Signature assumed | Status |
|---|---|---|---|
| `New` | `internal/gitx` | `func New(dir string, r Runner) *Git` | verified, `internal/gitx/git.go:18` |
| `Runner` | `internal/gitx` | `type Runner func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error)` | verified, `internal/gitx/exec.go:28` |
| `ReducedEnvRunner`, `ExecRunner` | `internal/gitx` | `Runner` values. The process-group helpers `setProcessGroup` and `killGroup` are unexported (`proc_unix.go`, `proc_windows.go`) | verified |
| `(*Git).Head` / `CurrentBranch` | `internal/gitx` | `(ctx) (string, error)`. `Head` is the short hash. `CurrentBranch` returns `"HEAD"` when detached | verified, `git.go:71`, `git.go:76` |
| `(*Git).ResetSoft` | `internal/gitx` | `(ctx, ref string) error` | verified, `git.go:348` |
| `(*Git).MergeBase`, `CommitsSince` | `internal/gitx` | `MergeBase(ctx, ref) (string, error)`; `CommitsSince(ctx, since) ([]Commit, error)` | verified |
| `tools.ReducedEnv` | agentkit-go `tools` | `ReducedEnv(extra []string) []string`. Strips the vendor keys and `*_TOKEN`, `*_SECRET`, `*_API_KEY` and `*_PASSWORD` (doc comment at `exec.go:37`) | **unverified**: only call sites are readable |
| `sandbox.Open` | agentkit-go `sandbox` | `Open(name string) (Provider, error)` for `"landlock"` and `"podman"` | **unverified** |
| `sandbox.Spec` | agentkit-go `sandbox` | `Image`, `Env map[string]string`, `Network` (none or all), `TTL`, `Labels`, `RequireLandlock` | **unverified** |
| `Sandbox` | agentkit-go `sandbox` | `Sync`, `Export` (never `.git`), `Tools()`, `Close()`. The `execute` tool takes a shell `command` and a timeout, and runs at the workspace root | **unverified** |
| `tools.For`, `workspace.Open` | agentkit-go | the hands' tool set over a directory, used by the `none` session | **unverified** |
| `landlock_create_ruleset` | Linux UAPI, through stdlib `syscall.Syscall` | syscall 444 with `attr=NULL, size=0, flags=LANDLOCK_CREATE_RULESET_VERSION (1)`, which returns the ABI | **unverified** here (kernel UAPI) |
| Claude Code CLI | external binary `claude` | `-p`, `--output-format json`, `--max-turns`, `--model`, `--allowedTools`, `--disallowedTools`, `--permission-mode dontAsk`, `--settings <file>`, `--append-system-prompt`, `--strict-mcp-config`; prompt on stdin | **unverified**: an external contract |
| Claude Code JSON result | external | `{type, subtype, is_error, num_turns, total_cost_usd \| cost_usd, usage{input_tokens, output_tokens, cache_creation_input_tokens, cache_read_input_tokens}, session_id, result}` | **unverified** |
| Claude Code settings | external | `{"hooks": {"PreToolUse": [{"matcher": "*", "hooks": [{"type": "command", "command": "…", "timeout": 30}]}]}}` | **unverified** |
| Podman engine | external, through agentkit's provider | tests detect it with `podman info` exiting 0 | **unverified** |
