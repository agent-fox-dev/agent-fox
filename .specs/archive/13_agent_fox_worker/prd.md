---
spec_id: "13"
spec_name: "agent_fox_worker"
title: "agent-fox worker: run one triage or fix attempt over hub — lease, gateway box, in-process loops, forge through hub, sessions and audit"
status: "active"
created_at: "2026-10-21T21:25:19.129062Z"
updated_at: "2026-10-21T21:25:19.129062Z"
intent_hash: "f1932716868d3695f2c93cf02b27bf300f3d0b33767c1d00fdb57cc32cbef129"
schema_version: 2
source: "docs/prd/03-run-the-agent-fox-service.md"
---
## Intent

Give `agent-fox` the worker that carries out one attempt of a job against a subscribed hub workspace. It runs PRD 02's triage and fix loops in-process: the same code `af` runs, moved where both binaries can call it. Hub supplies everything around them: the workspace record and variables, a clone from hub's git server, a sandbox lease reached through the gateway, every forge read and write, and the session, usage and audit records the attempt is billed and traced by. The worker holds two credentials, the subscription's PAT and the lease token, and nothing else. Nothing from the repository or the brain runs on the worker's host.

## Goals

1. **A fix lands end to end.** The setup is offline, against `fakehub`, with its sandbox lease pointing at an in-process gateway over the `local` provider, and the scripted brain. A fix attempt on issue 42 of a `standard` workspace must do the following, in this order (R8's):
   - open a hub session, and report its session id and run id to the jobs recorder;
   - set `af:in-progress` on issue 42;
   - clone from the fake hub's git server;
   - lease a box, report its sandbox id to the recorder, and sync the clone into it;
   - run the fix loop, with every gate run inside the box;
   - push `fix/42-…` to the fake hub's git server, call hub's branch push route, then `POST …/pulls`;
   - post one `fix_summary` comment and set `af:landed`, removing `af:in-progress`;
   - release the lease;
   - post the session's usage and an events batch, then complete the session.

   The attempt then returns a result. Its envelope is `done`, and its artifacts include the pull request, the hub session and the sandbox. Its labels say that `af:landed` was set and `af:in-progress` was removed.
2. **A triage lands end to end.** In the same setup, a triage attempt on an issue replaces the body through hub, sets `af:ready` and posts no comment. The `## Original report` section of the new body is byte-identical to the original body. A triage on an issue whose submission carries a question instead:
   - returns a `needs_human` envelope;
   - sets `af:needs-human`;
   - posts one comment with the question, its lettered options and how to answer.

   A triage on text input whose submission carries a question returns `needs_human` and writes nothing to the forge.
3. **A fix on a `carry_patch` workspace is refused.** It returns a `failed` envelope with `error.category` `unsupported`. `fakehub` records no clone, no lease, no session request and no forge write (no label, no comment), and no brain runs. A triage on the same workspace runs normally.
4. **The worker holds no vendor key and no forge token.** The test process sets sentinel values for `ANTHROPIC_API_KEY`, `GITHUB_TOKEN`, `GH_TOKEN` and `GITLAB_TOKEN`. During a full fix attempt:
   - no request to hub, the git server or the gateway carries any sentinel;
   - no box spec the worker builds carries any sentinel, and every box spec the worker builds has an empty `Env`;
   - every credential observed is either the subscription PAT (hub and git) or the lease token (gateway);
   - neither the PAT nor the lease token appears in the envelope, the events, the comments, any file left on disk, or any `Spec.Env`.
5. **The repository's checks never run on the worker's host.** In a service fix attempt, the host-side gate and revert-check functions are never called. Detection, the baseline, the gate after the change and the revert check are recorded as executions inside the box. A box whose image has no `af` fails the attempt with a `failed` envelope of category `sandbox`, not retryable, and the message names the profile.
6. **Attempts can run side by side.** Two attempts on two workspaces run concurrently in one process under `go test -race`. Each finishes, and every request each one sends carries only its own workspace's PAT.
7. **`af` is unchanged by the move.** After the commands move to an importable package, `make check` passes, the existing `af` tests pass, and the `--schema` goldens are byte-identical.
8. **Infrastructure failures come back as transient errors.** Each of these makes the worker return a transient error and no envelope: a lease that hub refuses with 503, hub answering 503 when the session opens, and a gateway that cannot be reached. The error carries a `toolio.Failure` of category `sandbox` or `hub`, with `retryable` true. With the jobs layer's fake retry, the same attempt is handed over again.
9. **A cancel cleans up.** Take a job created with the request label `af:fix`, on an issue that carries `af:ready`, and cancel its running fix attempt. Within the 60 s grace the worker:
   - removes `af:in-progress`;
   - puts `af:fix` back and leaves `af:ready`;
   - releases the lease;
   - posts no comment;
   - reports `af:fix` as set and `af:in-progress` as removed.

## Non-goals

- **Surviving restarts and lost boxes.** These belong to `agent_fox_worker_recovery`:
  - re-running an interrupted attempt;
  - reusing the clone's branch;
  - skipping the brain when a valid `submission.json` exists;
  - one retry with a new lease after `sandbox_unavailable`;
  - releasing orphaned leases at start.

  This spec only keeps the clone when the service is stopping or the attempt returns a transient error (R5), so that spec can find it.
- **The poll.** Polling hub's issues API for request labels, and the `ListIssues` path through hub, belong to `agent_fox_poll`.
- **The `af` client.** `af login`, `af workspace`, `af jobs`, `--workspace`, `--wait`, `--server` and `AF_URL` belong to `af_service_client`.
- **Job and attempt records.** The queue, the retry policy, the jobs API, SSE, retention, and the cancel/answer/retry endpoints belong to `agent_fox_jobs`. This spec implements the `Runner` that the jobs spec calls. Its one change to that spec is one more field the runner is given: the attempt's position in its round (R8, Design Decision 41).
- **The label protocol beyond the status mirror and the cancel restore (PRD 04).** That covers:
  - creating the labels on subscribe;
  - consuming request labels;
  - applying `labels.restore` for a job cancelled while queued or blocked, when no runner is involved;
  - `AF_AUTO_FIX`;
  - hub's event stream;
  - answering by re-labelling.
- **Mirroring endings that no runner sees.** Sometimes the jobs layer ends a job without a runner returning a result. That happens when an attempt's transient retries are spent, and when a runner overruns the cancel grace. The issue then keeps whatever labels the last runner left. The job record is the truth (PRD 01 §12, "labels can lie for a moment").
- **Carry patches (PRD 05).** Patch branches, `POST /patches`, rebuilds and upstream pull requests are out. Here a fix on a `carry_patch` workspace, or with `AF_FIX_LAND=patch`, is refused.
- **Finer hub reporting.** Usage per model turn and streaming events to hub during the attempt are left out. The worker reports one usage record and batches the events after the loop (Design Decisions 26 and 27).
- **`--sandbox gateway` for `af`.** `af --local` and the `af` toolbelt keep their providers, and the gateway provider is the worker's alone.
- **More than one attempt per workspace at a time.** That is the jobs spec's serialization.
- **Hub's own concerns.** Checking a PAT's scopes at subscribe belongs to the core (its Design Decision 22). Choosing images or profiles is hub's.

## Background

### What exists

- **The loops live in `package main`.**
  - `cmd/af/triage.go` (`triageLoop`, `loopPreflight`, `runInBox`) and `cmd/af/fix.go` (`fixLoop`) cannot be imported by `cmd/agent-fox`.
  - They read their dependencies from package-level variables: `fixRegistry`, `triageRegistry`, `fixBrainOptions`, `triageBrainOptions` and `fixGate` (`cmd/af/fix.go:19-27`, `cmd/af/triage.go:289,720`), plus `box.Default`.
  - They also read the process environment: `baseOptions()` reads `AF_MODEL` (`cmd/af/main.go`), `brain.Settings.Validate` reads `AF_SANDBOX`, and the brains read `ANTHROPIC_API_KEY`.
  - Command flags are captured in closures per `Register` call (`registerFix`'s `var o fixOptions`), so one `toolio.App` cannot serve two invocations at once.
- **`internal/toolio` is the shell.**
  - `App.Main` does the following:
    - builds the forge client through `App.NewForge`, an injection point that already exists;
    - resolves the input;
    - makes the run directory;
    - writes the events, to files only (`events.go`);
    - applies `--timeout` and the grace period;
    - assembles the envelope.
  - It installs `signal.NotifyContext(SIGINT, SIGTERM, SIGHUP)` on every call (`app.go`).
  - Run ids are `YYYYMMDD_HHMMSS_<6 hex>` (`rundir.go`). That is the format of hub's session `run_id`.
  - `forgeFor` (`forge.go`) takes the target repository from the issue URL. Without one it uses `--repo` (owner/name, with no host), and without that the origin of `--dir`.
  - `Resolve` marks an issue a pull request when the forge says it is one (`ref.IsPullRequest || thread.Issue.IsPR`, `input.go:170`). Both loops refuse a pull request (`internal/triage/preflight.go:114`, `cmd/af/fix_brief.go:101`).
  - `toolio.Artifact` paths are absolute (`envelope.go:142`). Triage and fix name the files of their run directory under `<dir>/.agent-fox/run/<id>/` as artifacts: `submission`, `brief`, `run_dir` and `report`. `toolio.Usage` has no model field.
- **Earlier questions are found in run directories.**
  - `fix.FindEarlier` (`internal/fix/questions.go`) and `triage.FindPreviousQuestion` (`internal/triage/previous.go`) search `<dir>/.agent-fox/run`.
  - A fresh clone has no run directories, so both briefs would carry bare answers.
  - `af fix` would also warn `question_not_found` for each answer (`cmd/af/fix_brief.go:158-161`).
- **The forge seam is `issuex.Client`.**
  - Triage and fix call `Authenticated`, `GetRepository`, `ReadIssue`, `UpdateIssue`, `CreateIssue`, `AddComment`, `AddLabels` and `CreatePullRequest` (`internal/triage/land.go`, `internal/fix/land.go`, `internal/fix/preflight.go`, `internal/toolio/input.go`).
  - They classify failures with `*issuex.HTTPError`, `issuex.IsRateLimited`, `IsNotFound`, `ErrConflict` and `IsNoToken`.
- **The box.**
  - `internal/box` has the providers `landlock`, `podman`, `local` and `none`, and no `gateway` (`registry.go`, `adapter.go`).
  - `box.Open` creates the sandbox from a `sandbox.Spec` whose `Env` is `brain.BuildBoxEnv`: the host values of the brain's `Credentials`, which is `ANTHROPIC_API_KEY` for `agentkit` and `claude` (`internal/brain/registry.go`, `agentkit.go:19`, `claude.go:24`).
  - `newAgentkitInBox` and `newClaude` refuse to start without the key on the host (`agentkit_process.go:37`, `claude.go:48`).
  - `Process.Run` names the credentials from `Session.Spec().Env` (`process.go`).
  - `Process` probes each program in the box once per session. A program that is missing or fails is a `sandbox` failure, not retryable. An `af` of another version or commit raises the warning `box_af_version_mismatch` (`process.go` `probe`, `compare`).
  - `driverInBox` consults `box.Default`.
  - When no model is given, the agentkit brain uses the catalog's default Anthropic model (`agentkit_adapter.go` `defaultModel`). The in-box brains pass no model.
- **Measuring runs on the host.**
  - `fix.Preflight` runs `project.Detect`, whose availability check is `exec.LookPath` on the host. It runs the baseline through `checks.Run` on the host checkout (`internal/fix/preflight.go`, `internal/project/gate.go:185`).
  - `fix.Measure` runs the gate after the change and `conform.Check` on the host too (`internal/fix/measure.go`, `internal/fix/loop.go:201`).
  - That is right for `af --local`, where the machine is the person's. In the service, the brain writes the tests, and the worker host holds every workspace's sealed PAT and the key that opens them. PRD 01 §4.2 already says the gate runs "inside the box over the proxy `execute`".
- **Git.**
  - `fix.Preflight` builds `gitx.New(dir, gitx.ExecRunner)`, which runs git under the process environment.
  - `landing.push` (`internal/fix/land.go:426`) is the single place a branch is pushed, to `origin`. It tries 3 times (`maxPushAttempts`). A final failure is category `git`, not retryable.
  - With `--land none`, `fix.Land` returns outcome `landed` and status `done` without pushing.
  - `box.Open` and the session's sync read the checkout with `gitx.New(dir, nil)` (`internal/box/open.go:184`, `session.go:129,186,201`), and `checks.FileView` reads it with `gitx.ExecRunner` (`internal/checks/view.go:42`). All of these are local reads under the process environment.
  - `gitx.HermeticEnv` already strips global and system git config and prompts.
- **The service so far.**
  - `11_agent_fox_service_core` delivers:
    - the `internal/hub` client (`User`, `Workspace`, `ErrRefused`, `ErrNotFound`, `*UnavailableError`). For its identity and visibility calls, a workspace 403 means not found (core Design Decision 17);
    - `internal/hub/fakehub`;
    - the store, with `Token(ctx, slug)` (`ErrNotSubscribed`, `ErrUnreadable`) and the subscription's `label_prefix`.
  - `12_agent_fox_jobs` delivers the job and attempt state machine over `aflib/jobqueue`, the event log and SSE. It also delivers the `Runner` contract (its R4):
    - **What the runner is given:**
      - the attempt's context, whose cause is a cancel, an unsubscribe or a shutdown;
      - the job id, the attempt number and the handover count;
      - the workspace, the kind and the input;
      - the request's context strings;
      - every answered question, with its options and the answer text;
      - the source and the request label;
      - the attempt directory `attempts/<job id>/<n>/`, created before the call.
    - **What it reports while it runs**, through a recorder: events, and the attempt's hub references (session id, run id, sandbox id).
    - **What it returns:** either a result or an error that it may mark transient. A result holds the envelope, the report text, the labels the runner set and removed, and the most attempts a round may make.
    - **Its four obligations:**
      - one brain-level attempt per attempt;
      - infrastructure failures as transient errors, not failed envelopes;
      - the PAT read at the start;
      - cleanup by cause, with the request label restored on a cancel.
    - **What the jobs layer does with the result.** After the runner returns, it stores `envelope.json` and `report.md` in the attempt directory. It ends a job at once on a retryable failed envelope whose category is not `brain` or `no_result`.
    - **The placeholder runner** answers `unsupported`.

### Why

PRD 03 §6 is the worker pipeline. Without it the service records jobs and runs none.

## Requirements

### R1. The agent commands can be called in-process by both binaries

**The move.** Every `af` command implementation moves from `cmd/af` into one importable internal package. The package's name is left to design. The move covers:
- the triage and fix loops;
- the ring-1 commands of `06_af_ring1_facts`;
- the submit and land commands;
- their schemas.

What stays and what moves:
- `cmd/af/main.go` keeps `main`, the ldflags variables and building the `toolio.App` from that package.
- Unit tests move with the code. Tests that build and run the binary stay in `cmd/af`.

**`af` behaves exactly as before.** That covers the commands, flags, help text, envelopes, exit codes, events and `--schema` output, with the goldens unchanged.

**What an in-process caller can supply.** It can give the following per invocation, in place of the package-level variables and the process environment. Each one defaults to what `af` uses today.
- The forge client factory.
- The box provider registry, which may hold providers `af` does not offer.
- The brain registry and the brain options: the model, and the environment the brains and the settings read. The worker's environment yields nothing.
- The git runner the fix pipeline uses for every git command, including the push.
- **The earlier questions.** For each `--context` value, the caller gives either the question it answers, with its options, or no question.
  - The loops use this in place of searching run directories.
  - A value given with a question appears in the brief as the question followed by its answer.
  - A value given with no question appears as material from a person, not as an answer to a question that could not be found, and raises no `question_not_found` warning.
  - Triage's brief holds one earlier question. It takes the question of the last value that has one.
- A step run after a successful push and before any pull request is asked for (R8).
- The measuring seams (R7).

**What `toolio` gains for an in-process invocation.**
- It installs no signal handler. The caller's context is the only stop, and `--timeout` and the grace period still apply.
- It delivers every event line to a caller-supplied sink as the line is written, in addition to the run directory's `events.jsonl`.
- It accepts a run id chosen by the caller. That id becomes the run directory's name and the envelope's `run_id`, and it must match the run-id format.
- It accepts a target repository chosen by the caller, host included. The command builds its forge client for that repository and uses it as its repository whatever the input, in place of `--repo` and the origin of `--dir`. An issue URL on another repository is a usage error.

**Concurrency.** Two invocations in one process may run at the same time, each with its own `App`. No invocation:
- sets or reads process environment variables on behalf of an attempt;
- changes the working directory;
- writes a package-level variable.

### R2. The hub client covers every call an attempt makes

The `internal/hub` client gains these calls. Every one of them:
- is made with the subscription's PAT;
- never touches the core's identity or visibility caches;
- has the path-escaped slug in its path.

| Operation | Request |
|---|---|
| read the workspace record: the clone URL on hub's git server, the base branch, the mode (`standard` or `carry_patch`), and the URL of the forge repository | `GET /api/v1/workspaces/{slug}` |
| read the resolved variables | `GET /api/v1/workspaces/{slug}/vars/resolved` |
| open a session | `POST /api/v1/sessions` with `{workspace_slug, archetype, model, run_id}` |
| post usage | `POST /api/v1/sessions/{id}/usage` |
| complete a session | `POST /api/v1/sessions/{id}/complete` |
| post an events batch | `POST /api/v1/workspaces/{slug}/runs/{run_id}/events/batch` |
| lease a sandbox | `POST /api/v1/workspaces/{slug}/sandboxes` |
| release a sandbox | `DELETE /api/v1/workspaces/{slug}/sandboxes/{id}` |
| publish a branch to the forge | `POST /api/v1/workspaces/{slug}/branches/{branch}/push` |
| the forge calls of R3 | — |

**Every request** follows the core's rules:
- a 10-second bound, unless the context is shorter;
- at most 1 MiB read;
- no redirect followed;
- no token in any error value or message.

Two calls make hub do slow work, so they have a longer bound instead of the 10 seconds:
- **The lease request:** 5 minutes, or the attempt's context when that is shorter. hub creates the box from the profile's image before it answers. A timeout counts as unavailable, so the lease fails as a transient error (R6).
- **The branch push route:** 5 minutes, or the attempt's context when that is shorter. hub pushes the branch to the forge before it answers. A timeout counts as unavailable, and R8 says what follows.

**How an answer is classified.** Callers tell these apart without parsing text:
- **refused**: hub's 401;
- **forbidden**: hub's 403. Its message names the hub operation, says that the subscription's PAT lacks the scope for it, and points to `PUT /api/v1/workspaces/{slug}` to replace the token (core Design Decision 22). This holds for the workspace record too, which departs from the core's identity and visibility calls, where a workspace 403 means not found (Design Decision 51);
- **not found**: 404;
- **rejected**: another 4xx. It carries hub's message from its `{error: {code, message}}` envelope when there is one;
- **unavailable**: 429, 5xx, a transport error, a timeout, or a body that is not the expected JSON.

A release answered 404 counts as released.

**Field names.** Only the hub client knows hub's field names, as in the core (its Design Decision 35).

### R3. Every forge read and write goes through hub

**The client.** `internal/hub` provides an `issuex.Client` bound to one workspace slug and its PAT, and a factory with the signature of `toolio.App.NewForge`.

**Which repository.** The client accepts only the workspace's forge repository. It compares host, owner and name, ignoring case:
- A factory call, ref or repo for another repository fails before any request is sent, and the failure says the issue is not on the workspace's repository.
- A repo or ref with an empty host is taken to be on the workspace's host, and is compared on owner and name alone.

**Operations triage and fix need**, mapped to hub's forge API under `/api/v1/workspaces/{slug}`:

| `issuex.Client` method | Hub call |
|---|---|
| `Authenticated` | true |
| `GetRepository` | from the workspace record |
| `ReadIssue` | the issue and its comments. The issue is marked a pull or merge request when hub says it is one, so the loops' existing refusal applies |
| `ListComments` | the issue's comments |
| `UpdateIssue` | the body and title |
| `CreateIssue` | creates an issue |
| `AddComment` | adds a comment |
| `AddLabels` | adds labels |
| `RemoveLabel` | removes a label |
| `CreatePullRequest` | `POST …/pulls` with `{title, body, head, base, draft}` |

The client also reports the forge's body limit through `issuex.BodyLimiter`: GitLab's when the repository host is GitLab, and GitHub's otherwise.

**Every other method** returns an error that says the operation is not available through hub in this release, and sends nothing.

**Errors keep the types the loops already classify**, so that `auth`, `forge` and `retryable` come out as they do with a direct forge:

| Hub answer | Error returned |
|---|---|
| refused or forbidden | `*issuex.HTTPError` with status 401 or 403, carrying the R2 message |
| not found | issuex's not-found error |
| 409 | issuex's conflict error |
| 429 | issuex's rate-limited error |
| other unavailable | a retryable error |

### R4. An attempt's settings come from hub variables, read at the start of every attempt

**The variables.**

| Variable | Default (triage / fix) | Accepted |
|---|---|---|
| `AF_BRAIN_TRIAGE`, `AF_BRAIN_FIX`, then `AF_BRAIN` | `agentkit` / `claude` | `agentkit`, `claude` |
| `AF_MODEL` | the catalog's default Anthropic model (R9) | any non-empty string |
| `AF_MAX_BUDGET_USD` | `2` / `10` | a finite number above 0 |
| `AF_MAX_TURNS` | `100` / `150` | a whole number of at least 1 |
| `AF_TIMEOUT` | `15m` / `45m` | a Go duration from `1m` to `24h` |
| `AF_MAX_ATTEMPTS` | `2` | a whole number from 1 to 10 |
| `AF_SANDBOX_PROFILE` | hub's default (omitted) | any non-empty string |
| `AF_FIX_LAND` | `pr` | `pr`, `branch`, `none`; `patch` is refused below |

**How they are read.**
- A variable that is unset or empty takes its default.
- Unknown variables are ignored.
- A value outside its range fails the attempt before a session opens. The envelope is `failed` with `error.category` `config`, not retryable. Its `fix_hint.var` names the variable, and its message names the variable and its value.

**The label prefix.** The subscription's `label_prefix` sets the prefix, and `AF_LABEL_PREFIX` is not read. When `AF_LABEL_PREFIX` is set and differs from the subscription's prefix, the envelope carries the warning `label_prefix_ignored`.

**Attempt limits.**
- `AF_MAX_ATTEMPTS` goes back to the jobs layer with the attempt's result, as the bound on further brain-level attempts of the job.
- The fix loop runs with `--attempts 1`, so that one attempt row is one brain-level attempt.

**Refusals of a fix.** A fix is refused before any clone, lease, session or brain when either holds:
- the workspace's mode is `carry_patch`;
- `AF_FIX_LAND` is `patch`.

The envelope is `failed` with `error.category` `unsupported`, not retryable, and its message says that carry-patch landing comes in a later release.

A triage on a `carry_patch` workspace runs normally.

### R5. The clone, and the only git credential

**The working directory.** The jobs layer gives the worker the attempt directory `attempts/<job id>/<n>/`. The worker works only in named subdirectories of it, each with mode `0700`:
- `clone/` for the clone;
- `input/` for a file input.

The worker never writes, moves or removes `envelope.json` or `report.md` in the attempt directory, and never removes the directory itself. The jobs layer writes those files after the worker returns.

**Cloning.**
- The worker clones the workspace's hub clone URL into `clone/`, with the workspace's base branch checked out.
- It sets a fixed committer identity in the clone's local configuration.

**How git authenticates.** The PAT reaches git only as the header `Authorization: Basic <base64 of "agent-fox:<PAT>">`: the PAT is the HTTP Basic password, under a fixed user name. The header is set as git configuration scoped to hub's URL, and it is passed through the command's environment. The PAT is therefore never:
- on a command line;
- in `.git/config` or any other file, so it is never synced into the box;
- in a log, the envelope, the events or a comment.

**Which git commands are hermetic.** Every git command that carries the PAT or can reach a remote runs under a hermetic runner: no global or system git configuration, no prompt, no askpass. That covers the clone, every git command of the fix pipeline (through its git runner, R1), and the push.

Two kinds of git command only read the local clone: the box's sync, and `checks.FileView`. They keep the default runner. They never see the PAT, and the clone's own configuration holds no credential.

**Clone failures.**
- Hub's git server refuses the credential (401 or 403): a `failed` envelope, category `auth`, not retryable.
- Any other clone failure (a transport error, hub's 5xx, a timeout): a transient error of category `git` (R8).

**Removing the directories.** `clone/` and `input/` are removed when the attempt returns. `clone/` is kept, for `agent_fox_worker_recovery`, in two cases:
- the attempt ended because the service is stopping, as `agent_fox_jobs`'s cancellation cause tells;
- the attempt returns a transient error.

Until that spec lands, a delivery that finds a kept `clone/` removes it and clones afresh.

### R6. The lease and the gateway box

**When the lease is requested.** Only after every refusal that needs neither a box nor a model has passed: settings, mode, input and session.

**The lease request** is `{purpose: "triage"|"fix", labels: {job, attempt}}`, with `profile` set to `AF_SANDBOX_PROFILE` when that is set. Hub answers with `{id, gateway_url, token, expires_at}`.

**How a refused lease fails.**
- Hub answers 401 or 403: a `failed` envelope, category `auth`, not retryable.
- Any other refusal, unavailable answer or timeout (capacity, an unknown profile, a 5xx): a transient error of category `sandbox`, with hub's reason in the message.

**The `gateway` provider.**
- It is built from the lease's URL and token, in the attempt's own box registry. It is not one of `af`'s `--sandbox` providers.
- It counts as isolating.
- Processes in it are called by name from the image, as under `podman`.

**Creating the box.**
- The box is created with the labels `af.run_id`, `af.tool`, `af.job` and `af.attempt`, and with no `Env` from the worker. The lease's profile supplies the image, the network and the secrets.
- A gateway that cannot be reached, or that refuses to create the box, makes the attempt return a transient error of category `sandbox`.

**Brains that need the vendor key accept that the box supplies it.**
- The worker checks for no key and sends none.
- A process started in the box gets the names of the brain's credentials as references to the box's own environment, never as values.

**The timeout.**
- The attempt's timeout is `AF_TIMEOUT`, cut so that it ends 2 minutes before `expires_at`. A cut adds the warning `lease_shortened_timeout`.
- A lease with less than 2 minutes left makes the attempt return a transient error of category `sandbox`.

**Releasing the lease.** The lease is released after the box is closed, on every path: success, failure, transient error, cancellation, shutdown and panic.
- The release runs on a context the attempt's cancellation does not reach, bounded to 30 seconds.
- A release that fails adds the warning `lease_release_failed` and does not change the status.

### R7. In the service, measuring runs in the box

**Nothing from the repository runs on the worker's host.** For a service fix attempt, each of these runs inside the leased box through the image's `af` ring-1 commands:
- deciding which checks can run (`af lang detect`);
- the baseline (`af gate run`);
- the gate after the change (`af gate run`);
- the revert check (`af revert-check`).

Before each run, the host's tree is synced into the box and the run's gate table is placed there. Each result is read from the command's envelope.

**The `af` in the image.** The profile's image must provide `af` at a compatible version. Before the first ring-1 command in a box, the worker runs `af --version` there once:
- **`af` is missing or fails:** a `failed` envelope, category `sandbox`, not retryable. The message names the profile (`AF_SANDBOX_PROFILE`, or hub's default) and says that its image must provide `af`.
- **`af` is another version or commit than the worker's:** the warning `box_af_version_mismatch`, as brain processes raise it, and the attempt goes on.

A ring-1 envelope whose `schema_version` has another major version than the worker's, or whose result does not decode, gives a `failed` envelope, category `sandbox`, not retryable, naming the profile.

**Ordering.** In this mode the fix pipeline opens the box before its own preflight's detection, and keeps that one box for the whole attempt.

**What stays on the host.** Steps that only read files or run git stay where they are: the file view, the repository map, path validation, applying the export, and the commit and push.

**What does not change.** The verdict table, the brief's gate section and the result fields keep their shape. `af --local` keeps measuring on the host.

**A lost box.** A ring-1 command that cannot run because the box or the gateway was lost ends the loop with a `sandbox` failure marked retryable. The worker returns that failure as a transient error (R8).

### R8. The attempt pipeline and what it writes to the forge

**The worker is the production `Runner`** of `agent_fox_jobs`. It takes what that contract gives a runner (see Background), plus one field this spec adds to the contract: the attempt's position in its round. That is 1 for an attempt whose trigger is `create`, `answer` or `retry`, and one more for each `auto` attempt after it. The worker returns either a result or a transient error.

**The order of an attempt:**
1. *Resolve.* Read the PAT from the store, the workspace record and the variables (R4). For an issue input, also read the issue through hub (see "The input" below). Refusals happen here.
2. *Session.* Open the hub session (R9), and report its session id and run id to the jobs recorder.
3. *Mark.* Set `<prefix>in-progress` on the issue, when there is one.
4. *Clone* (R5).
5. *Lease and box* (R6), and report the sandbox id to the recorder.
6. *Run.* Run the loop in-process (R1) with:
   - the hub forge factory (R3);
   - the workspace's forge repository as the target;
   - the hermetic git runner (R5);
   - the gateway registry (R6);
   - the box-backed measuring (R7);
   - the earlier questions (see "The input" below);
   - the run id of the session;
   - the settings of R4 as flags.
7. *Mirror.* Write the outcome to the issue.
8. *Release* the lease.
9. *Report* to hub (R9).

**Transient errors and envelopes.** The worker returns a transient error for every infrastructure failure. The error carries a `*toolio.Failure` with the category, the message and `retryable` true, and the worker returns no envelope. The infrastructure failures are:
- **Hub unavailable** (429, 5xx, a transport error, a timeout) when reading the workspace record, the variables or the issue, or when opening the session: category `hub`.
- **A clone** that fails for any reason other than a refused credential: category `git`.
- **A lease or box problem:** a lease hub refuses with anything but 401 or 403, a gateway that cannot be reached or refuses the box, or a lease too short to use. Category `sandbox`.
- **An infrastructure failure inside the loop:** a loop envelope that is `failed` with `retryable` true and category `sandbox`, `forge` or `hub` is returned as that failure. These are:
  - the box's prepare, sync and export failures;
  - a box lost during measuring;
  - a forge rate limit;
  - a lost forge connection.

Every other ending returns a result whose envelope says what the attempt came to:
- brain-level outcomes, including `brain`, `no_result` and every other loop ending;
- permanent refusals: `config`, `unsupported`, `usage`, `auth`, and `sandbox` when not retryable.

A push or publish failure after the commit stays the loop's `git` failure, not retryable.

**Envelopes the worker builds.** A refusal before the loop returns an envelope the worker builds itself:
- its `tool` is `triage` or `fix`;
- its status is `failed`, or `usage` for an input refusal;
- its `error` carries the category, the message and `retryable` false;
- it has empty arrays and usage source `none`, and is encoded as `toolio` encodes envelopes.

A refused PAT, a forbidden operation, and a subscription token that is unreadable or gone are all category `auth`. Their messages point to `PUT /api/v1/workspaces/{slug}`.

**What runs after an attempt stops early.**

| Where it stops | Returns | Forge writes | Lease | Hub report |
|---|---|---|---|---|
| Resolve, any refusal | an envelope | none | none | none: no session |
| Resolve or session, hub unavailable | a transient error | none | none | none |
| Session, hub 401 or 403 | an `auth` envelope | none | none | none |
| Clone, lease or box, transient | a transient error | `in-progress` removed | released if granted | events posted, session completed `failed` |
| Clone or lease, refused credential | an `auth` envelope | the mirror: `failed` and one comment | released if granted | events posted, session completed `failed` |
| Run | the loop's envelope, or a transient error | the mirror | released | R9 |

**The input.**
- An issue given as a number becomes the forge URL of that issue in the workspace's repository.
- An issue given as `<path>#<N>` is checked against the workspace's repository. The path is compared without case, and on GitLab the full namespace path is compared. A match becomes that issue's URL. Any other path is a `usage` refusal that says the issue is not on the workspace's repository.
- An issue given as a URL must be on that repository. Otherwise it is the same `usage` refusal.
- At resolve the worker reads the issue through hub. An issue that hub reports is a pull or merge request is refused with a `usage` envelope, before anything is written.
- Text goes to the loop on stdin. A title, when given, is the text's first line as a Markdown heading.
- A file is written as `input/<base name>` and passed by path.
- The `--context` values are the request's context strings, in order, followed by the answers, in order. The earlier questions (R1) give no question for each context string, and for each answer the question it answers and its options. The brief then carries each question with its answer, and no `question_not_found` warning is raised.

**The branch.** After the push to hub's git server succeeds, and before any pull request is asked for, the worker asks hub to push that branch to the forge.
- While hub is unavailable, the worker tries up to 3 times in all, as the git push does.
- A final failure is the push's failure: the loop ends `failed`, category `git`, not retryable. The branch is kept on hub's git server, and the envelope's `next` names it.
- This departs from PRD 03 §6, which makes a forge write that fails after the push a warning on a landed run (Design Decision 12).

**Events.**
- Every event line of the loop goes to the jobs recorder as it is emitted.
- The worker adds its own `step` events, `{"name": …, "source": "worker"}`, for `resolve`, `session`, `clone`, `lease`, `run`, `mirror`, `release` and `report`.
- The hub references go to the recorder as soon as they are known: the session id and run id once the session opens, and the sandbox id once the lease is granted.

**The mirror.** It uses the same hub forge client.

*Which issue.* The mirror writes to one of two issues:
- the issue the input named;
- for a triage of text or a file that ended `done`, the issue the loop created.

When there is neither, the worker writes nothing to the forge. A question then lives only in the job record.

*Status labels.* The status labels are `in-progress`, `ready`, `needs-human`, `landed` and `failed`. Setting one also removes any of the other four that the issue carries, with one exception: in a fix attempt, `ready` is not removed by `in-progress`. It is removed only when the fix's outcome sets `landed`, `needs-human` or `failed` (PRD 01 §3).

| Outcome | Status label | Comment |
|---|---|---|
| triage `done` with confidence `confirmed` or `probable` | `ready` | none |
| triage `done` with confidence `suspected` or `not_reproducible` | `needs-human` | the reason |
| any `needs_human` | `needs-human` | the question |
| fix `done`, or `unverified` with outcome `landed`, with `AF_FIX_LAND` `pr` or `branch` | `landed` | `fix_summary` |
| fix `done` with outcome `landed` and `AF_FIX_LAND` `none` | none; `in-progress` removed | `fix_summary` saying nothing was kept |
| fix `unverified` with outcome `parked` | `needs-human` | `fix_summary`, with what failed and how to ask again |
| `failed` that an automatic attempt follows | none; `in-progress` removed | none |
| any other `failed`, or `usage` | `failed` | the failure |
| a transient error | none; `in-progress` removed | none |

*When an automatic attempt follows.* All of these hold, which is exactly the jobs layer's rule 5:
- the status is `failed`;
- the category is `brain` or `no_result`;
- `retryable` is true;
- the attempt's position in its round is below `AF_MAX_ATTEMPTS`.

*Comments.*
- **The question.** A `needs_human` gets one comment. It holds the question, the lettered options, and how to answer: `af jobs answer <job id>`, or `POST /api/v1/jobs/{id}/answer`.
- **The reason.** A triage `done` with confidence `suspected` or `not_reproducible` gets one comment. It holds:
  - the confidence;
  - where the diagnosis is: the body, or triage's own comment when the body would be too long;
  - how to ask again: a new triage of the issue with `context`, through `POST /api/v1/workspaces/{slug}/triage`.
- **The fix summary.** A landed or parked fix gets one `fix_summary` comment. It holds the outcome, the branch, the pull request or its absence, the verdict, `Closes` or `Refs`, and what was not proven.
  - With `AF_FIX_LAND` `none`, it says that the change was measured and that nothing was pushed or kept.
  - For a parked fix, it adds what failed (the verdict and the refusal) and how to ask again: `af jobs retry <job id>`, or `POST /api/v1/jobs/{id}/retry`.
- **The failure.** A `failed` or `usage` attempt that ends the job gets one comment with the category and the message, and says that `POST /api/v1/jobs/{id}/retry` asks again.
- **No comment.** A triage that ends `ready` gets none.

Every comment carries a marker naming the job, the attempt and the kind. A comment whose marker is already on the issue is not posted again. Comments contain no absolute path of the worker.

**Cancelled attempts.** This applies when the attempt's context is cancelled with a cancel cause, which an unsubscribe also reads as. Within the jobs layer's 60 s grace, and after the loop has stopped, the worker:
- removes `<prefix>in-progress`;
- puts the job's request label back on the issue, when the job has one;
- sets no status label and posts no comment;
- releases the lease and reports to hub.

It returns a result that carries the loop's envelope and the labels, so that the jobs layer clears `labels.restore`. When the loop had not started, the worker builds the envelope itself, with category `cancelled`. The PAT read at resolve serves this cleanup even after an unsubscribe has removed the subscription.

**A shutdown.** When the cause is a shutdown, the worker:
- writes nothing to the forge, so the labels stay for the resumed attempt;
- keeps `clone/`;
- releases the lease;
- completes the session as far as the jobs layer's shutdown wait allows.

The jobs layer discards whatever the worker returns.

**Write failures.** A forge write in steps 3 and 7, or in cancel cleanup, that fails is a warning on the envelope (`status_label_failed`, `comment_failed`, `request_label_failed`), with a matching side effect. It never changes the status. Under a transient error there is no envelope, so the failure is logged.

**Hub references.** Besides reaching the recorder (see Events), they go into the envelope as the artifacts `{kind: "hub_session", name: <session id>}` and `{kind: "sandbox", name: <sandbox id>}`.

**What the worker returns.** A result holds:
- the envelope, amended only by adding warnings, side effects and artifacts, and encoded as `toolio` encodes envelopes;
- the run's report text, or nothing when there is none;
- the labels it set and removed, in steps 3 and 7 and in cancel cleanup. Set means the status label and any restored request label;
- `AF_MAX_ATTEMPTS`, as the most attempts a round may make. It is the default 2 when the attempt stopped before the variables were read.

The envelope's path artifacts name files in the attempt's `clone/`, which is removed when the attempt returns. They stay in the envelope as facts of the run. They are paths on the worker and cannot be retrieved, and `docs/api.md` says so. The envelope and the report are what the jobs layer keeps.

### R9. Every attempt that reaches a session is reported to hub

**Opening the session.** It opens after resolve and before the mark, with:
- `archetype`: `triage` or `fix`;
- `model`: the resolved model. That is `AF_MODEL`, or, when it is unset, the catalog's default Anthropic model id. The worker also passes that model to the brain, so the brain runs the model the session names;
- a `run_id` the worker draws in the run-id format, which the loop then uses as its run id (R1).

**When the session cannot be opened**, nothing has been spent yet:
- hub unavailable: the attempt returns a transient error of category `hub`;
- hub answers 401 or 403: an `auth` envelope.

An attempt refused before this point has no session.

**After the release:**
1. When a brain ran, one usage record is posted. It holds the resolved model, and from the envelope's `usage` the brain, tokens, cost, cost source and turns.
2. The loop's events are posted in sequence order, in batches of at most 500.
3. The session is completed with the envelope's status, or with `failed` when the worker returns a transient error.

Each of these that fails adds a warning (`hub_usage_failed`, `hub_events_failed`, `hub_session_failed`) and changes nothing else. Under a transient error the failure is logged instead.

### R10. The credential invariant, the test hub, and the documentation

**The invariant.** Across an attempt, the worker holds and sends two credentials:
- the subscription's PAT, to hub and to hub's git server;
- the lease token, to the gateway alone.

It never sends, places in a box spec, or writes a vendor key or a forge token, even when the service's own process environment holds one. A test asserts this over every request and every box spec of a full attempt (Goal 4).

**The test hub.** `internal/hub/fakehub` grows to serve the endpoints of R2 and R3 from state that tests set and read. It records and counts every request, and injects failures by route. It covers:
- workspaces with a mode and a base branch;
- resolved variables;
- issues with comments and labels, including issues that are pull requests;
- pull requests;
- sessions with usage, events and completion;
- leases;
- branch pushes.

It also provides:
- a git smart-HTTP server on loopback for the clone URL. It accepts only `Authorization: Basic` with the fixed user name and the workspace's PAT, and refuses a bearer token and any other credential;
- leases that point at an in-process gateway over the `local` provider. The gateway's backend records every spec it creates and every command it executes.

Every test runs offline.

**The documentation**, per the steering, after `make doc`:

| File | What it gains |
|---|---|
| `docs/architecture.md` | the worker pipeline; transient errors versus envelopes; the gateway box and the `af` the profile's image must provide; measuring in the box under the service; the label mirror |
| `docs/configuration.md` and `docs/model-usage.md` | the `AF_*` hub variables, their defaults and ranges; the default model; where the vendor key comes from in the service |
| `docs/api.md` | the envelope artifacts `hub_session` and `sandbox`; the failure category `hub`; the carry-patch use of `agent_fox_jobs`'s `unsupported`; the warnings added here; that path artifacts are paths on the worker and cannot be retrieved |
| `docs/development.md` | the moved command package and the new worker package |
| `README.md` | that the service now runs attempts |

`docs/cli.md` is unchanged, because no command or flag changes.

## Design Decisions

1. **The loops move into an importable internal package.** The worker does not run `af` as a subprocess, and nothing is reimplemented. The split's scope and PRD 03 ask for the same code `af` runs, called in-process, and `package main` cannot be imported. The move includes the ring-1 commands of `06_af_ring1_facts`, which the box runs for measuring (R7).
2. **The worker drives the loops through `toolio`'s shell with an argv, using a fresh `App` per attempt.** This gives exactly `af`'s input resolution, run directory, envelope, events, timeout and grace. Flags are captured in per-`Register` closures, so one `App` cannot serve two attempts at once.
3. **In-process invocations install no signal handler.** A per-attempt `signal.NotifyContext` would let SIGHUP or SIGTERM cancel attempts behind the service's back. The jobs layer owns stopping, through the context.
4. **`toolio` gains an event sink and a preset run id.** Without the sink, events reach only files and cannot reach SSE live. The preset run id is needed because hub's session `run_id` must equal the loop's run id, and the session opens before the loop.
5. **Measuring runs in the box under the service, against PRD 03's "loop unchanged".**
   - As built, the gate runs brain-written tests on the host. The worker host holds every workspace's sealed PAT and the key, so a prompt-injected test could steal them.
   - PRD 01 §4.2 puts the gate in the box.
   - The ring-1 commands are `af`'s, so the fix pipeline only gains seams and a box-first ordering in this mode.
   - The profile's image must provide `af`, and the worker checks it (Design Decision 49).
6. **The session opens after the variables are read, not at claim.** The session's `model` comes from `AF_MODEL`. Attempts refused before that point have spent nothing and have no run id, so they get no session.
7. **The subscription's `label_prefix` sets the label prefix. `AF_LABEL_PREFIX` is ignored, with a warning when it differs.**
   - PRD 01 §3 puts the protocol "under the subscription's prefix".
   - The core stores that prefix per subscription and leaves the choice to this spec (core Design Decision 25).
   - This departs from PRD 03 §1, which lists `AF_LABEL_PREFIX` among the variables read for every job. It also departs from PRD 04 §1's wording, which names `AF_LABEL_PREFIX` as the prefix and expects a resubscribe after changing it.
   - A per-job variable could point at labels no subscribe created. With one source, the worker and `agent_fox_poll` cannot disagree.
   - The warning tells an operator who set the variable that it has no effect.
8. **`AF_MAX_ATTEMPTS` bounds attempt rows in the jobs layer, and the fix loop runs `--attempts 1`.** PRD 03 says brain-level attempts "are separate rows", and the jobs contract's first obligation asks for one brain-level attempt per attempt. Keeping the loop's in-run attempts as well would multiply the budget.
9. **The status label follows the attempt that just ended, except while the job goes on.**
   - A failed attempt that an automatic attempt follows, and an attempt that returns a transient error, both remove `in-progress`. They set no status label and post no comment.
   - Only an attempt that ends the job sets `failed` and comments, because PRD 01 §3 defines `af:failed` as "retries are spent".
   - `in-progress` means an attempt holds a sandbox, so it is not kept between attempts.
   - The worker knows whether an automatic attempt follows from the round position (Design Decision 41).
10. **Comments are posted for every `needs-human` outcome, for a landed or parked fix, and for a failure that ends the job. Each carries an idempotency marker. A triage that ends `ready` gets none.**
    - PRD 01 §3 says a `needs-human` issue has its question "on the issue as a comment". §4.1 gives the `not_reproducible` and question cases one comment with the answer path. §4.2 has a parked run's comment say what failed and how to answer.
    - A parked fix leaves the job `failed` (jobs Design Decision 7), so its way to ask again is `retry`.
    - The marker keeps a re-run from posting twice.
    - For a `ready` triage, the body is the deliverable.
11. **A triage that is `done` with confidence `suspected` or `not_reproducible` is `needs-human`, with a comment giving the reason.**
    - PRD 01 reserves `ready` for `confirmed` and `probable`.
    - A `suspected` diagnosis with a question already ends `needs_human`. One without a question is not `ready` either, and a person has to judge it.
    - The job is `done`, not blocked, so the comment's way to ask again is a new triage with `context`.
12. **The branch reaches the forge through hub's branch push route, called by a hook after `landing.push`, and its failure is the push's failure.**
    - `landing.push` is the one push site, and the hook covers `pr`, `branch` and a parked push alike.
    - A failed route call ends the loop `failed`, category `git`, not retryable, with the branch kept on hub's git server.
    - This deliberately departs from PRD 03 §6, which makes a forge write after the push a warning on a landed run. A branch only on hub's git server has not been published, and no pull request can be opened from it.
13. **The forge client lives in `internal/hub` and leaves unneeded methods unsupported.** The core keeps hub's paths in one package. The poll and PRD 04 add `ListIssues` and `CreateLabel` when they need them.
14. **Hub errors are mapped onto issuex's error types.** The loops' existing classification then decides `auth`, `forge` and `retryable` unchanged.
15. **The PAT reaches git as an HTTP Basic password under the fixed user name `agent-fox`, in a header scoped to hub's URL and passed through the command's environment.**
    - PRD 01 §6.4 and PRD 03 §6 give the PAT as the HTTP Basic password. The fake git server accepts only that form, so a Bearer header would fail the tests rather than the real hub.
    - A credential in the remote URL or in `.git/config` would be synced into the box. A credential on the command line would show in process listings.
    - The hermetic environment keeps the operator's own credential helpers from answering for hub.
    - The expected mechanism is in Verified External API.
16. **The committer identity is fixed: `agent-fox <agent-fox@noreply.invalid>`.** A fresh clone has none, and the hub user behind the PAT is known only by id. The reserved `.invalid` domain cannot route mail to anyone.
17. **`gateway` is not an `af --sandbox` choice.** It needs a lease, which only the worker has. PRD 10 left the provider to the service.
18. **In the service, brains take the vendor key from the box.** D4: hub injects it into the box from the profile, and the worker must not need it, send it, or hold it.
19. **Programs in a gateway box are called by name.** The image is hub's profile's, and the worker cannot know paths inside it, which is the same situation as `podman`. What it can check is that `af` answers, by name, at a compatible version (Design Decision 49).
20. **The lease request carries no TTL, and the attempt's timeout is cut to fit `expires_at`.** PRD 03 sends `{profile, purpose, labels}`, and hub's profile sets the TTL ceiling. A loop that outlives its box loses its work.
21. **Every lease refusal other than 401/403 is a transient error of category `sandbox`.** PRD 01 §6.4 names capacity and an unknown profile as retryable. The jobs contract's second obligation makes "a box or lease that cannot be had" a transient error. The jobs layer bounds the retries to three handovers.
22. **One failure category is added: `hub`, which the worker returns on transient errors when hub is unavailable.** `unsupported` is `agent_fox_jobs`'s (its Design Decision 24), and the carry-patch refusal reuses it. `toolio` categories are open strings, so `hub` needs no new mechanism.
23. **The lease is released after the box closes, on a detached, bounded context, and 404 counts as released.** Hub destroys the box on release, and its expiry destroys it anyway. A failed release is left to `agent_fox_worker_recovery`.
24. **The worker's environment lookup yields nothing.** That makes "no vendor key, no forge token" structural rather than a filter. It also means `AF_SANDBOX`, `AF_MODEL` or a stray key in the service's environment cannot change an attempt.
25. **The clone is removed when an attempt returns. It is kept when the service is stopping, or when the attempt returns a transient error.**
    - A clone is a full repository per attempt.
    - Both kept cases hand the same attempt over again. A clone kept from after the brain ran lets `agent_fox_worker_recovery` avoid running the brain twice.
    - Retention removes a clone left behind after spent transient retries, together with its attempt directory.
26. **One usage record is posted per attempt, not one per model turn.** The loops report usage as a running total (`brain.Tally`), and per-turn figures are not observable without changing every brain.
27. **Events are posted to hub in batches of at most 500, after the loop.** This keeps hub's audit complete without coupling the attempt's progress to hub's availability. Streaming is later work.
28. **Text goes to the loop on stdin, and a file is written under `input/`.** Text passed as an argument that happened to name a file would be read as that file. A file must keep `input.kind: file`.
29. **The worker turns every issue form the jobs layer accepts into the issue URL on the workspace's repository, and refuses a pull request at resolve.**
    - The loops resolve issues by URL (`issuex.ParseIssueURL`).
    - The jobs API accepts a number, a URL or `<path>#<N>`, and leaves the repository check and the pull-request check to the worker (jobs Design Decision 13).
    - Reading the issue at resolve refuses a pull request before `in-progress` is set on it.
    - The hub client's `ReadIssue` marks pull requests, so the loops' own refusal also holds.
30. **`AF_FIX_LAND=none` sets no status label, and its `fix_summary` says nothing was kept.**
    - `fix.Land` reports outcome `landed` without pushing in that mode, while PRD 01 §3 defines `af:landed` as a pushed branch with an open pull request.
    - The comment keeps the measurement visible. That is the use of `none`: evaluating a brain without keeping a change.
    - `AF_FIX_LAND=branch` pushes the branch, so it is `landed`, and the comment says no pull request was asked for.
31. **Hub references reach the jobs recorder as soon as they are known, and also go into the envelope as artifacts.** The jobs contract shows them in the job's `hub` field while the attempt runs (jobs R2, R4). The artifacts keep them in the stored envelope after the attempt.
32. **A cancelled attempt removes `in-progress` and restores the request label, within the grace, and reports both.**
    - The jobs contract's fourth obligation and its Design Decision 10 give this to the runner for a running attempt.
    - PRD 03 §9.4 asks for it.
    - Reporting the restored label lets the jobs layer clear `labels.restore`.
    - Queued and blocked jobs have no runner, so applying `restore` for them stays PRD 04's.
33. **The worker amends the envelope only by adding to it, and encodes it as `toolio` does.** The jobs layer stores the envelope byte for byte, so the loop's facts must survive unchanged.
34. **`scripted` is not accepted from a hub variable. Tests reach it through a test-only seam of the worker.** It needs a script file on the worker, which no hub variable can provide safely.
35. **Comments contain no absolute worker paths.** Issue comments may be public, and the service's state layout is not the reader's business.
36. **Infrastructure failures are transient errors, never failed envelopes. Envelopes say what a brain-level attempt came to, or a permanent refusal.**
    - The jobs contract's second obligation lists a box or lease that cannot be had, synced or exported; hub unavailable or 5xx; and a forge rate limit or lost connection.
    - The jobs layer fails a job at once on a retryable failed envelope of any category but `brain` or `no_result`. Returning these failures as envelopes would therefore end jobs that a retry would save.
    - A loop envelope with a retryable `sandbox`, `forge` or `hub` failure is turned into a transient error.
    - A push failure after the commit is not retryable in the loop, so it stays an envelope.
37. **Goal 1 asserts R8's order.** The session opens before the clone, and hub is reported to after the lease is released. A test written from the goals then checks the order the pipeline actually has.
38. **The worker's result carries the labels it set and removed, and `AF_MAX_ATTEMPTS`.** The jobs contract's result has these fields. The job document's `labels` and the clearing of `restore` depend on them.
39. **The request's context strings come first as `--context`, followed by the answers.** The jobs contract gives the runner both. Context is what the person asked with, and the answers follow in the order the questions were asked.
40. **Earlier questions are handed to the loops in-process, not found in run directories.** A fresh clone has no run directories. Without the questions the briefs would carry bare answers, and `af fix` would warn `question_not_found` for every answer, although the jobs layer has each question and its options (jobs Design Decision 11). A context string is marked as having no question, so it is neither framed as an answer to a lost question nor warned about.
41. **This spec adds the attempt's position in its round to what the jobs layer gives the runner.**
    - Only the jobs layer knows the round.
    - The worker needs it to tell an attempt that an automatic attempt follows from one that ends the job, and so to set `failed` and comment only once.
    - The rule the worker applies is the jobs layer's rule 5, with the same `AF_MAX_ATTEMPTS` it returns.
42. **`ready` survives a fix's `in-progress` and is replaced only by the fix's outcome label.** PRD 01 §3 says so. A cancelled fix, a transient error, and a fix with `AF_FIX_LAND=none` therefore leave a triaged issue `ready`.
43. **The worker gives the loop the workspace's forge repository as its target, host included, for every input kind.** Without it, text and file input would fall back to the origin of the clone, which is hub's git server. The hub client would then refuse that repository, so a text triage could not create its issue. A target that the in-process caller chooses avoids depending on `--repo`, which carries no host. The hub client also takes a host-less repo to be on the workspace's host.
44. **Only git commands that carry the PAT or can reach a remote are guaranteed hermetic.** These are the clone, the fix pipeline's git and the push. The box's sync and `checks.FileView` only read the local clone, which holds no credential. Giving them seams of their own would change the box and checks packages for no gain in what the PAT can reach.
45. **A refusal at resolve writes nothing to the forge and opens no session.** These refusals are a bad variable, a carry-patch fix, an input refusal, and a PAT that is unreadable or refused. They are about the service's configuration or the request, which the job record and the API answer already show. With no readable PAT, nothing could be written anyway. A failure after the mark, once the issue has been touched, goes through the mirror.
46. **Mark and mirror need an issue.** A triage of text or a file has none until it lands, and has none at all when it ends with a question. The question then lives only in the job record, so Goal 2's comment applies to issue inputs.
47. **The worker uses `clone/` and `input/` under the directory the jobs layer gives it, and removes only those.** The jobs layer writes `envelope.json` and `report.md` in that directory after the runner returns. A file input under `input/` cannot land inside the clone.
48. **The lease request and the branch push route have 5-minute bounds, or the attempt's context when shorter.** In both calls hub does slow, synchronous work: creating a box from an image, or pushing to the forge. With 10 seconds, leases would fail routinely. A lease that timed out may have left a box behind on hub. Its labels name the job and attempt, and `agent_fox_worker_recovery` releases orphans.
49. **The profile's image must provide `af`. The worker probes `af --version` once per box before the first ring-1 command.**
    - A missing `af`, or an envelope schema with another major version, is a permanent misconfiguration. It fails as `sandbox`, not retryable, naming the profile, so the jobs layer does not retry it three times.
    - Another version is the warning brain processes already raise.
    - A box lost mid-command is transient.
50. **The session's model, the usage record's model and the brain's model are one id: `AF_MODEL`, or the catalog's default Anthropic model.**
    - `toolio.Usage` has no model.
    - The brain's name is not a model id.
    - The agentkit brain's default is already that catalog model. Passing it to the claude brain too, in the service only, lets the session name the model that actually ran.
51. **The worker's workspace-record call reads hub's 403 as forbidden, not as not found.** The core reads a workspace 403 as not found for a caller's visibility, so that a workspace's existence is not revealed (its Design Decision 17). The worker's call is made with a PAT that already saw the workspace at subscribe, so a 403 means the PAT lost its reach. The core's duty for PAT calls is to say so and point to `PUT` (its Design Decision 22). Both readings end in `auth`, not retryable.
52. **The requirements state behaviour, and leave package names and mechanisms to design.** That means the package's name, the git configuration mechanism and the fake git server's construction. The core and the jobs spec chose the same (their Design Decision 36). The expected mechanisms stay listed in Verified External API, and `internal/hub/fakehub` stays named because the core named it for extension.
53. **Path artifacts are paths on the worker and cannot be retrieved.** The envelope is stored byte for byte (Design Decision 33), and the clone they point into is removed. The jobs layer keeps the envelope and the report, the events reach hub, and `docs/api.md` says what the paths are.

## Dependencies

| Spec | Relationship | Why |
|---|---|---|
| `11_agent_fox_service_core` | depends on, extends | Uses the store's `Token(ctx, slug)` and subscription `label_prefix`, the config's database path, the `internal/hub` client conventions and `fakehub`. Adds the worker's hub calls, the forge client and the fake's new endpoints, and reads a workspace 403 on a PAT call as forbidden. |
| `12_agent_fox_jobs` | depends on, modifies | The worker implements its `Runner` contract and replaces the `unsupported` placeholder. It reports events and hub references to the recorder, reads the cancellation cause, returns transient errors and the result the jobs layer stores (envelope, report, labels, `AF_MAX_ATTEMPTS`), and reuses its `unsupported` category. It adds the attempt's position in its round to what the runner is given. |
| `05_af_cli_envelope` | modifies | `toolio` gains an in-process invocation with no signal handling, an event sink, a preset run id and a caller-chosen target repository. |
| `06_af_ring1_facts` | modifies, depends on | Its commands move to the importable package. The worker runs `af lang detect`, `af gate run` and `af revert-check` in the box and reads their envelopes. |
| `07_af_brains` | modifies | Brain options take the environment lookup per invocation. Brains accept a vendor key supplied by the box, and processes name credentials the box holds. |
| `08_af_triage` | modifies | The triage loop moves to the importable package. It gains the earlier-question seam. Otherwise its behaviour is unchanged. |
| `09_af_fix` | modifies | The fix loop moves. It gains a git-runner seam, a detection seam, an earlier-question seam, a post-push hook, and the box-first ordering for measuring in the box. |
| `10_af_boxes_claude` | modifies | Adds the `gateway` provider, built from a lease in a per-attempt registry, and lets the registry be supplied per invocation (`Settings`, `driverInBox`, `BoxRequest`). |

## Verified External API

**`github.com/agent-fox-dev/aflib/issuex`.** Its source is in the module cache, outside the repository. These signatures are read from call sites in the repository (`internal/toolio/dryrun.go`, `forge.go`, `input.go`, `internal/fix`, `internal/triage`):

- `type Client interface`, with:
  - `Authenticated() bool` and `Close() error`;
  - `GetRepository(ctx, Repo) (Repository, error)`, `ReadIssue(ctx, IssueRef) (IssueThread, error)` and `ListIssues(ctx, Repo, IssueFilter) (IssueList, error)`;
  - `ListComments(ctx, IssueRef) (CommentList, error)`, `ReadPullRequest(ctx, IssueRef) (PullRequest, error)` and `ReadChangedFiles(ctx, IssueRef) ([]ChangedFile, error)`;
  - `GetPRState`, `GetCIChecks` and `GetPRReviews`;
  - `CreateIssue(ctx, Repo, CreateIssueRequest) (Issue, error)` and `UpdateIssue(ctx, IssueRef, UpdateIssueRequest) (Issue, error)`;
  - `CloseIssue(ctx, IssueRef, string) error` and `AddComment(ctx, IssueRef, string) (string, error)`;
  - `AddLabels(ctx, IssueRef, []string) error`, `RemoveLabel(ctx, IssueRef, string) error` and `CreateLabel(ctx, Repo, Label) error`;
  - `CreatePullRequest(ctx, Repo, CreatePullRequestRequest) (PullRequest, error)`;
  - `PostReviewComment`, `MergePullRequest(ctx, IssueRef, MergeOptions) (MergeResult, error)` and `ClosePullRequest(ctx, IssueRef) error`.
- `type BodyLimiter` (the method `MaxBodyLength() int`), `func MaxBodyLength(Client) int` and `GitLabMaxBodyLength`.
- `Options{UserAgent, Dir string; Repo Repo}` and `func NewWithOptions(Options) (Client, error)`.
- `Repo{Owner, Name, Host}` with `Valid()` and `String()`, `func ParseRepo(string) (Repo, bool)`.
- `IssueRef{Repo, Number, IsPullRequest}` with `URL()` and `String()`, `func ParseIssueURL(string) (IssueRef, bool)`.
- `IssueThread{Issue, Comments, Truncated, CommentsErr}`, `Issue{Number, Title, Body, State, Author User, Labels []string, IsPR, URL, HTMLURL, CreatedAt}` and `Comment{Body, User, CreatedAt}`. `toolio.Resolve` reads `Issue.IsPR` (`input.go:170`).
- `CreateIssueRequest{Title, Body, Labels}`, `UpdateIssueRequest{Title, Body}`, `CreatePullRequestRequest{Title, Body, Head, Base, Draft}`, `PullRequest{Number, URL, HTMLURL, Draft}` and `Repository{Archived}`.
- `*HTTPError{Status int; Message string}`, `ErrRateLimited`, `ErrNotFound`, `ErrConflict`, `IsNotFound`, `IsRateLimited` and `IsNoToken`.

**`github.com/agent-fox-dev/agentkit-go/sandbox`**, replaced to `../agentkit-go/sandbox`, outside the repository. These are read from call sites in `internal/box`:
- `Provider` with `Create(ctx, Spec) (Sandbox, error)` and `Capabilities() Capabilities`;
- `Spec{Env map[string]string; Labels map[string]string; TTL; Image; RequireLandlock; Network; Workdir}`;
- `Sandbox` with `Spec()`, `Tools(ctx, ToolOptions)`, `Export(ctx, name, Dest)` and `Close(ctx)`;
- `LocalDir{Path, Delete}`, `local.Unisolated` and `ErrNoAgent`.

**agentkit-go's model catalog.** `catalog.Default().DefaultModelID("anthropic")` is read from its call site in `internal/brain/agentkit_adapter.go` (`defaultModel`).

**`agentkit-go/sandbox/gateway` and `…/sandbox/gatewayd`.** These are **unverified**: no call site or source is in the repository.
- Assumed: `gateway.Dial(url, token string) (sandbox.Provider, error)`, possibly taking a context or options. It yields a provider whose `Create` honours the lease's spec.
- Assumed: a `gatewayd.Server` that is an `http.Handler`, constructed with a backend `sandbox.Provider` and the bearer tokens it accepts. It is served from `httptest.NewServer`, as PRD 01 cites `examples/gateway` doing.

**Hub's HTTP API.** Every shape is **unverified**: hub has no source or schema in this repository. Only the hub client will know these names.
- `GET /api/v1/workspaces/{slug}` with the PAT. Assumed fields:
  - `hub_url`, the clone URL;
  - `branch`, the base branch;
  - `mode`, `standard` or `carry_patch`;
  - `git_url`, the forge repository URL (the field name is from PRD 01 D12).
- `GET /api/v1/workspaces/{slug}/vars/resolved`. Assumed `{"vars": {NAME: value}}`; a list of `{name, value}` is also to be accepted.
- `POST /api/v1/sessions {workspace_slug, archetype, model, run_id}` answers `{id}`.
- `POST /api/v1/sessions/{id}/usage {model, input_tokens, output_tokens, cost_usd, turns}`.
- `POST /api/v1/sessions/{id}/complete {status}`.
- `POST /api/v1/workspaces/{slug}/runs/{run_id}/events/batch {events: [{seq, ts, type, data}]}`.
- `POST /api/v1/workspaces/{slug}/sandboxes {profile?, purpose, labels}` answers `{id, gateway_url, token, expires_at}`, with `expires_at` in RFC 3339. `DELETE …/sandboxes/{id}` releases it.
- `POST /api/v1/workspaces/{slug}/branches/{branch}/push`. This is hub PRD 05 in the input, and §9 item 6 of PRD 01. It is not known to exist in hub today.
- The forge API:
  - `GET`/`PATCH /api/v1/workspaces/{slug}/issues/{n}`. The issue object is assumed to say when it is a pull or merge request;
  - `POST …/issues`;
  - `GET`/`POST …/issues/{n}/comments`;
  - `POST …/issues/{n}/labels {labels}` and `DELETE …/issues/{n}/labels/{name}`;
  - `POST …/pulls {title, body, head, base, draft}`, answering `{number, url, html_url, draft}`.
- Errors use the apikit envelope `{error: {code, message}}`.
- Hub's git server accepts the PAT as the HTTP Basic password (PRD 01 §6.4, §8). The user name is assumed to be ignored.

**Standard library and git.** These are the expected mechanisms, which design confirms:
- `net/http/cgi.Handler{Path, Args, Dir, Env}` serves `git http-backend`, with `GIT_PROJECT_ROOT` and `GIT_HTTP_EXPORT_ALL`, for the fake git server.
- git honours `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_<n>` and `GIT_CONFIG_VALUE_<n>`, available since git 2.31, together with `http.<url>.extraHeader`. This is how the scoped Basic header reaches git without a command line or a file.
