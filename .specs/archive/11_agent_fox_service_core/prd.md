---
spec_id: "11"
spec_name: "agent_fox_service_core"
title: "agent-fox service core: the process, its configuration and state, hub identity, and workspace subscriptions"
status: "active"
created_at: "2026-10-21T20:16:01.462291Z"
updated_at: "2026-10-21T20:16:01.462291Z"
intent_hash: "8898657c15a8d670ac28de048870a8c61c3d750a60903b244e9b464719bc333f"
schema_version: 2
source: "docs/prd/03-run-the-agent-fox-service.md"
---
## Intent

Give agent-fox a long-running process, `agent-fox`, that the later service specs build on. It starts from one `config.toml`, keeps its state in one SQLite file, and treats hub as the only authority on who is calling and which workspaces they may see. It also lets a caller subscribe a hub workspace by handing over a workspace PAT, which the service stores encrypted at rest and never returns. Jobs, the worker, the poll and the CLI client are built on this foundation and are not part of it.

## Goals

1. `agent-fox --config c.toml`, where the file sets only `[hub].url` and `[secrets].key_file`, starts and listens. `GET /healthz`, `GET /readyz` and `GET /version` answer 200 without a token.
2. A bad configuration makes `agent-fox` exit 2 before it listens. It writes one JSON log record at level `ERROR` on stderr, and that record's `error` attribute names the key or file at fault. This covers:
   - an unknown key, `server.external_url` included;
   - an `auth_cache_ttl` of `16m`;
   - an unset `${VAR}`;
   - a missing key file, a key of the wrong length, and a key file with a group or other permission bit set.
3. Against an `httptest` hub that counts its requests:
   - a caller token validated once is not sent to `GET /api/v1/user` again within `auth_cache_ttl`, and is sent again after the TTL;
   - a token hub refused is not sent again within the TTL;
   - when hub answers 401 to a later call made with a cached caller token, that token is evicted and the next request revalidates it.
4. `PUT /api/v1/workspaces/widgets` with a PAT that the fake hub lets see `widgets` stores a subscription:
   - neither the database file nor its WAL contains the PAT's bytes;
   - no response body contains the PAT;
   - `GET /api/v1/workspaces` lists the subscription for a caller who can see `widgets` on hub, and not for one who cannot;
   - `GET /api/v1/workspaces/widgets` answers 404 to the second caller.
5. After a restart with the same key file, the subscription reads back with `token_state: ok`. After a restart with a different key, it reads `token_state: unreadable` and the service still runs.
6. `make check` passes offline: no network beyond loopback, no hub, no key.

## Non-goals

- **Jobs, the queue and the events stream.** Covered by `agent_fox_jobs`: the job record, `aflib/jobqueue`, `POST /triage|/fix`, cancel, answer and retry, SSE, `[workers].count`, `[database].retain`, and cancelling open jobs on unsubscribe.
- **The worker.** Covered by `agent_fox_worker`: leases, the `gateway` box, the clone from `hub_url`, PRD 02's loops run in-process, every forge byte through hub, sessions, usage and audit, the `carry_patch` refusal, and the `AF_*` workspace variables.
- **Crash recovery and orphaned leases.** Covered by `agent_fox_worker_recovery`.
- **The poll trigger.** Covered by `agent_fox_poll`.
- **The `af` client.** Covered by `af_service_client`: `af login`, `af workspace`, `af jobs`, `--workspace`, `--wait`, `--server`/`AF_URL`, and an explicit `--local`. `af` is unchanged by this spec. The client is to treat both 200 and 201 from `PUT /api/v1/workspaces/{slug}` as success (Design Decision 23).
- **Labels created on subscribe, hub's event stream, `AF_AUTO_FIX`.** These belong to PRD 04. Carry patches belong to PRD 05.
- **Checking the PAT's scopes at subscribe.** A missing scope surfaces at the first hub call that needs it. `agent_fox_worker` and `agent_fox_poll` make those calls. They are to turn hub's 403 on a call made with the PAT into an error that does three things: it names the hub operation, it says the subscription's PAT lacks the scope for it, and it points to `PUT /api/v1/workspaces/{slug}` as the way to replace the token (Design Decision 22).
- **Resolved settings and label state in the subscription view.** PRD 01 §6.2 has `GET /workspaces/:slug` show these, but no scope of this split adds them. The view stays the subscription record of R9.
  - Resolved settings are hub's variables, read with `afc vars`, and the worker reads them for each attempt.
  - Label state belongs with the labels that PRD 04 creates, so PRD 04 may add it to the view (Design Decision 34).
- **Which label prefix governs the labels.** This spec stores and returns the `label_prefix` given at subscribe. `agent_fox_worker` and `agent_fox_poll` decide whether it or the hub variable `AF_LABEL_PREFIX` governs (Design Decision 25).
- **`[server].external_url`.** PRD 01 §6.1's sample sets it, but nothing in PRD 01 or PRD 03 reads it: forge webhooks go to hub (PRD 01 D11). No scope of this split defines it, and a config file that sets it is refused as an unknown key (Design Decision 7).
- **TLS, rate limiting, metrics, key rotation, and two processes sharing one database.** The service serves plain HTTP, meant for a loopback address or a reverse proxy.
- **Holding a vendor key or a forge token.** The only credentials the service holds are the caller tokens it is sent and the sealed workspace PATs.

## Background

What exists today:

- **One binary.** `cmd/af` (`main.go`, `register`) is the only binary. Every command runs through `internal/toolio`, the CLI shell: one JSON envelope on stdout and an exit-code table. Nothing in the repository serves HTTP, holds a database, or talks to hub.
- **Dependencies.**
  - `go.mod` requires `github.com/agent-fox-dev/aflib`, of which only `issuex` is imported today. The README says aflib also holds `jobqueue`.
  - `github.com/BurntSushi/toml v1.6.0` is already in the module graph, as an indirect dependency.
  - There is no SQL driver in `go.mod` or `go.sum`.
- **The PRDs.**
  - PRD 01 decides the service's shape:
    - D1: two binaries, `agent-fox` and `af` as its client;
    - D2: hub is the identity provider, and its answers are cached by token hash with a TTL bounded to 15 minutes;
    - D3: a PAT for each subscribed workspace, encrypted at rest;
    - D4: no vendor key or forge token in the service.
  - PRD 01 §6.1 gives the configuration file. Its sample also sets `[server].external_url` and `[workers].count`.
  - PRD 01 §6.2 gives the API and the apikit error envelope `{error: {code, message, error_type?}}`.
  - PRD 01 §8 lists the scopes a subscription's PAT carries. It leaves the sandbox and forge scopes "*to be named by hub*".
  - PRD 03, the input to this spec, covers the whole service. It is larger than one spec, so it is split; this spec is the first scope (see Design Decision 1).
- **Test conventions.**
  - `internal/triage/fakeforge` is an `httptest` forge that records every request.
  - `cmd/af/smoke_test.go`'s `buildBinary` runs `make build` into a temporary `BIN_DIR`.
  - `internal/toolio/version.go` defines the version document (`name`, `version`, `commit`, `build_time`, `go_version`): values set by ldflags come first, and build info is the fallback. The Makefile sets `-X main.version/commit/buildTime`.
- **Docs.**
  - `docs/api.md` does not exist yet. The steering requires it for REST endpoints.
  - `docs/cli.md` describes only `af` today. The steering requires it for any new CLI flags.

## Requirements

### R1. The binary and its command line

`cmd/agent-fox` builds the `agent-fox` binary, and `make build` writes `$(BIN_DIR)/agent-fox` beside `$(BIN_DIR)/af` with the same ldflags.

Flags and outputs:
- `agent-fox [--config PATH]` starts the service.
- `agent-fox --version` prints the version document as one JSON object and exits 0. The document has `name` `agent-fox`, `version`, `commit`, `build_time` and `go_version`, with the same fallback rule as `af --version`.
- `--help` prints the usage and exits 0.
- An unknown flag or a positional argument prints a plain-text usage message on stderr and exits 2.

Logging starts before anything else, so every later stderr line is a `log/slog` JSON record (R7).

Start failures:
- An error in the configuration, the key file or the database exits 2.
- A failure to bind `server.listen` exits 1. That covers an address in use, permission denied, and a host that does not resolve or is not local.
- Either way, stderr carries exactly one JSON log record at level `ERROR`, with `msg` `start failed` and an `error` attribute that names the key, file, address or reason. Nothing listens and no `listening` record is written.

Running and stopping:
- Once bound, the service logs a `listening` record whose `addr` attribute is the bound address, so `listen = "127.0.0.1:0"` works in tests.
- SIGINT or SIGTERM stops accepting connections, waits up to 30 seconds for requests in flight, closes the database and exits 0.
- A listener failure after start exits 1.

### R2. Configuration

**Where the file is.** The file is `--config` when given. Otherwise it is `./config.toml`, then `$XDG_CONFIG_HOME/agent-fox/config.toml` (`$HOME/.config` when `XDG_CONFIG_HOME` is unset). If none exists, start fails and lists the paths it tried.

**The keys:**

| Key | Type | Default | Rule |
|---|---|---|---|
| `server.listen` | string | `127.0.0.1:8090` | a `host:port` that `net.SplitHostPort` accepts; port 0 allowed |
| `hub.url` | string | required | an absolute `http` or `https` URL. An optional path prefix is allowed; a query or fragment is not. A trailing `/` is dropped. Hub's API is `<url>/api/v1/…` |
| `hub.auth_cache_ttl` | duration (Go syntax) | `60s` | from `1s` to `15m` inclusive |
| `database.path` | string | `$XDG_STATE_HOME/agent-fox/agent-fox.db` (`$HOME/.local/state` when unset) | — |
| `secrets.key_file` | string | required | — |

**No other key is defined.** That includes `server.external_url`, `[workers]` and `database.retain`. Each of them is an unknown key, so PRD 01 §6.1's sample config is refused as it stands (Design Decision 7).

**Validation:**
- A relative `database.path` or `secrets.key_file` is resolved against the directory of the config file.
- `${NAME}` is expanded from the environment in every string value after decoding. `NAME` matches `[A-Za-z_][A-Za-z0-9_]*`.
- An unset variable is a configuration error that names it. A variable set to empty expands to empty, so a required key left empty fails as missing.
- An unknown key, a value of the wrong type, and a duration out of range are each an error that names the dotted key.

**Warning.** A `hub.url` with `http` and a host that is not a loopback address is accepted, because caller tokens and PATs will still reach hub. A warning is logged that they cross the link in clear. The warning is logged after the listener is bound and before the `listening` record, so a failed start writes only its one `ERROR` record.

### R3. The key and the sealing of workspace tokens

**The key file.** `secrets.key_file` holds the standard base64 encoding of exactly 32 random bytes, and surrounding whitespace is ignored. Start fails, naming the file, when the file is:
- missing or unreadable;
- not base64;
- not 32 bytes once decoded;
- on Unix, any group or other permission bit set (`mode & 0o077 != 0`). Owner bits are not checked, so `0600`, `0400` and `0700` are all accepted.

**Making a key.** The recipe is `(umask 077; openssl rand -base64 32 > key)`. A bare `openssl rand -base64 32 > key` under the usual umask creates a `0644` file, which start refuses. `docs/configuration.md` shows the recipe in the `umask 077` form.

**Sealing.** A token is sealed with AES-256-GCM:
- a fresh 12-byte nonce from `crypto/rand` for every seal;
- additional data `agent-fox/subscription/<slug>`, which binds the ciphertext to its row;
- stored as one version byte `0x01`, then the nonce, then the ciphertext with its tag.

**Opening.** Opening fails, with an error and never a panic, on any of: a wrong key, an altered byte, another slug, an unknown version byte, or a truncated value.

**Secrecy.** The key and any plaintext token are never logged and never written anywhere except into the AEAD. Neither ever appears in an error message.

### R4. The state database

**Opening the file.** The service opens the SQLite file at `database.path`:
- it creates the parent directory with mode `0700` when it is missing, and the file with mode `0600`;
- it sets `journal_mode=WAL`, `busy_timeout=5000` and `foreign_keys=ON`;
- `/readyz` pings the database.

**Tables and migrations.**
- Every table the service owns is named with the prefix `af_`. That leaves room for `aflib/jobqueue`'s tables in the same file later, whatever their names.
- Migrations are an ordered, append-only list.
- The service records which migrations it has applied in an `af_`-prefixed table of its own. It does not use `PRAGMA user_version`, which another module may use.
- Migrations are applied at start, each in one transaction.
- A database that records a version higher than this binary knows makes start fail, naming both versions.
- The service never reads or writes a table it does not own.

**Migration 1** creates the subscriptions table. A subscription records:
- the slug, unique;
- the label prefix;
- the sealed PAT;
- the hub user id behind the PAT, when hub said it (R8), else nothing;
- the user id of the caller who last subscribed it;
- when it was created and when it was last updated.

Times are RFC 3339 UTC. Column names and types are left to design.

### R5. The hub client

One place in the code knows hub's paths and how to read hub's answers, including the field names of hub's user object. It makes two calls:
- *who a token belongs to*: `GET <url>/api/v1/user`;
- *a workspace as a token sees it*: `GET <url>/api/v1/workspaces/<slug>`, with the slug path-escaped.

**Every request:**
- sends `Authorization: Bearer <token>`, `Accept: application/json` and `User-Agent: agent-fox/<version>`;
- is bounded by 10 seconds unless the context is shorter;
- reads at most 1 MiB of the body;
- does not follow redirects, so a token never reaches another host.

**What an answer means:**

| Hub answers | who a token belongs to | a workspace |
|---|---|---|
| 200 and a JSON object | the user, identified by a non-empty `id` (a string, or a number kept as a string). `login` is optional. The field name `id` is an assumption, see Verified External API | the workspace object (this spec requires no field) |
| 401 | refused | refused |
| 403 | refused | not found |
| 404 | unavailable | not found |
| 3xx, 429, 5xx, a transport error, a timeout, a body that is not the expected JSON | unavailable | unavailable |

Callers tell the three failures (refused, not found, unavailable) apart without parsing message text. No failure value or message contains the token.

### R6. Who is calling, and what they may see

**Credentials.** Every route under `/api/v1` requires `Authorization: Bearer <token>`. The scheme is case-insensitive and the token must be non-empty. A missing or malformed header is answered with 401 `unauthenticated`, without calling hub.

**The identity cache:**
- It is keyed by the hex SHA-256 of the token and never by the token itself.
- A hit within `auth_cache_ttl` of hub's answer is used without calling hub.
- A positive entry holds the hub user id, which is the request's caller. R8 records it as `subscribed_by`.
- A refusal from hub is cached for the same TTL and answered with 401 `unauthenticated`.
- When hub is unavailable, nothing is cached and the request gets 502 `hub_unavailable`.

**The visibility cache.** A route with `{slug}` runs three checks in order:
1. The slug's syntax. A slug that does not match `^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$` is answered with 400 `invalid_request`.
2. The identity check.
3. Visibility: what hub answers to the workspace call made with the caller's token. The answer is cached by (token hash, slug) for the same TTL.

How the visibility answer is used:
- *found* lets the request through;
- *not found* is cached and answered with 404 `not_found`;
- *unavailable* is not cached and is answered with 502.

**Eviction.** Hub may answer 401 to a call made with the caller's token on the caller's behalf. That evicts the token's identity entry and all of its visibility entries, and the request is answered with 401. Calls made with a subscription PAT (R8) never read, fill or evict either cache, even when the PAT equals the caller's token.

**Bounds and timing.**
- Each cache holds at most 10,000 entries. When a cache is full, expired entries go first, then the oldest.
- Both caches are safe for concurrent use. Tests check their expiry by advancing a clock, without sleeping.
- A slug the caller cannot see answers exactly as a slug that is not subscribed: 404 with the same body.

### R7. The HTTP surface

**Routes:**
- `GET /healthz` answers 200 `{"status":"ok"}` while the process serves.
- `GET /readyz` answers 200 `{"status":"ready"}` when the database ping succeeds, and 503 `not_ready` otherwise.
- `GET /version` answers the version document of R1.

None of the three needs a token.

**Errors.** Every error is `application/json` with the body `{"error":{"code":…,"message":…}}`. This is the apikit envelope of PRD 01 §6.2. Its optional `error_type` is never sent in this release, so clients must tolerate its absence.

| Code | Status |
|---|---|
| `invalid_request` | 400 |
| `unauthenticated` | 401 |
| `not_found` | 404 (also for an unknown route) |
| `method_not_allowed` | 405 |
| `payload_too_large` | 413 |
| `unsupported_media_type` | 415 |
| `invalid_hub_token` | 422 |
| `internal` | 500 |
| `hub_unavailable` | 502 |
| `not_ready` | 503 |

**Request bodies.** A body must be `Content-Type: application/json` (parameters allowed) and at most 64 KiB. Unknown fields and trailing data are refused.

**Handlers and server.** A panic in a handler is answered with 500 `internal` and logged with its stack, and the process continues. The server sets a read-header timeout.

**Logging:**
- `log/slog` writes JSON on stderr, from the first line the process writes (R1).
- Each request gets one log record: method, path, status, duration and, when known, the caller's user id.
- The `Authorization` header, request and response bodies, tokens, ciphertext and the key are never logged.

### R8. Subscribing a workspace

`PUT /api/v1/workspaces/{slug}` takes `{"hub_token": string, "label_prefix"?: string}`. Nothing is written before every check has passed.

**The steps, in order:**
1. R6's checks for the caller and the slug.
2. Body validation. `hub_token` must be non-empty. `label_prefix` defaults to `af:` and must be 1 to 32 characters, with no whitespace, no control character and no comma.
3. Two fresh hub calls with the PAT, which bypass the caches (R6), in this order:
   - *the gate*: the workspace call for `slug` must find the workspace;
   - *best effort*: the user call. When hub answers with a user, its id becomes `token_user`. When hub refuses the PAT on this call, `token_user` is left empty and the subscription is still stored.

**How the PAT checks fail:**
- The gate is refused (hub's 401): 422 `invalid_hub_token`, with a message saying that hub refused the token.
- The gate does not find the workspace (hub's 403 or 404): 422 `invalid_hub_token`, with a message that names the workspace.
- Hub is unavailable on either call: 502 `hub_unavailable`.

None of these touches the identity or visibility caches.

**Storing.** On success the PAT is sealed (R3) and upserted in one statement.
- A new row answers 201. An existing row is replaced in full and answers 200. The input wrote `→ 201` for every `PUT`, so the 200 is a deliberate change that `af_service_client` honours (Design Decision 23).
- An absent `label_prefix` resets the prefix to `af:`.
- `token_user` is replaced by this request's answer, and an empty one stays empty.
- `subscribed_by` becomes the current caller, `created_at` is kept, and `updated_at` is now.
- The body is the subscription view (R9).

### R9. Reading and removing subscriptions

**The subscription view** is `{slug, label_prefix, token_user, subscribed_by, token_state, created_at, updated_at}`.
- `label_prefix` is the prefix given at subscribe, or `af:`. Whether it is the prefix in effect is for the later specs to decide (Design Decision 25).
- `token_user` is the hub user id behind the PAT, or `null` when hub would not say (R8).
- `token_state` is `ok` when the stored token opens with this deployment's key, and `unreadable` otherwise.
- The view never contains the token or its ciphertext.

**The routes:**
- `GET /api/v1/workspaces` answers `{"workspaces":[…]}`, sorted by slug.
  - It holds every subscription whose slug the caller can see (R6, cached).
  - If hub is unavailable for any of them, the request answers 502 rather than a partial list.
- `GET /api/v1/workspaces/{slug}` answers the view. It answers 404 when the slug is not subscribed or the caller cannot see it.
- `DELETE /api/v1/workspaces/{slug}` removes the row, sealed token included, and answers 204. It answers 404 under the same rule as `GET`.

**For later specs.** The later scopes can read a subscription's opened PAT by slug, from inside the process.
- The read fails with an error, never a panic, when the slug is not subscribed or the token does not open.
- The plaintext appears in no error.
- This spec tests the read.

### R10. Documentation and test support

**Test support.** `internal/hub/fakehub` is an `httptest` hub in the manner of `internal/triage/fakeforge`. It serves `GET /api/v1/user` and `GET /api/v1/workspaces/{slug}` from a table:
- the table maps tokens to users, and users to the workspaces they may see;
- a token can also be given workspaces without a user, so that `/user` refuses it while the workspace call finds the workspace.

The fake also:
- counts and records every request by path and token hash;
- can be told to fail a route with a status, or by hanging up.

Later specs extend it.

**Documentation.** Per the steering:
- `docs/api.md` is new. It covers:
  - the routes and the authentication rule;
  - the subscription view, with `token_user` possibly `null` and `label_prefix` being the value given at subscribe;
  - `PUT`'s 201 on create and 200 on replace;
  - the error codes, and the envelope without `error_type`.
- `docs/cli.md` gains an `agent-fox` section: the flags `--config`, `--version` and `--help`, the exit codes 0, 1 and 2 with what each means, and the config file lookup order.
- `docs/configuration.md` gains an `agent-fox` section. It gives the keys of R2, a note that `server.external_url` and `[workers]` are not accepted in this release, and the key file of R3 with the `(umask 077; openssl rand -base64 32 > key)` recipe.
- `docs/model-usage.md` says that `agent-fox` runs no model in this release.
- `docs/architecture.md` gains a service section with the process, the state file and hub as the identity provider.
- `docs/development.md` lists the new packages in its layout table.
- `README.md` describes the binary and how to start it.
- `make doc` is run first and its output under `docs/generated/` is committed.

## Design Decisions

1. **The input is split into six specs, and this is the first.** PRD 03 has six areas that can be built and tested independently: the process and subscriptions, jobs and SSE, the worker, recovery, the poll, and the CLI. Together they come to well over 10 requirements and 12 tasks. Everything else stands on the process, the store, the hub client and identity, so they come first.
2. **`agent-fox` is a separate binary under `cmd/agent-fox`, not `af serve`.** PRD 01 D1 decided two binaries, and a server's lifecycle (flags, signals, logs) does not fit `toolio`'s one-envelope command shell.
3. **The config file is found by `--config`, then `./config.toml`, then `$XDG_CONFIG_HOME/agent-fox/config.toml`.** This is PRD 01 §6.1's lookup with an explicit override for tests and deployments.
4. **TOML is parsed with `github.com/BurntSushi/toml`.** It is already in the module graph and the standard library has no TOML parser. The steering asks for reuse of what `go.mod` holds.
5. **`${VAR}` is expanded in decoded string values, and an unset variable is an error.** Expanding the raw text could break TOML quoting, and a silently empty secret path or URL is worse than a refusal.
6. **Unknown keys are refused.** A misspelt key such as `auth_cach_ttl` must not silently fall back to a default.
7. **Only the keys this spec uses are defined, and `[server].external_url` is dropped.**
   - `[workers]` and `[database].retain` arrive with `agent_fox_jobs`, which uses them.
   - `external_url` has no consumer in PRD 01 or PRD 03, since forge webhooks go to hub (PRD 01 D11), and no scope of this split defines it. It is refused as an unknown key, and `docs/configuration.md` does not show it.
   - So PRD 01 §6.1's sample config is refused as it stands. Before `agent_fox_jobs` it fails on `[workers]`; after that, on `external_url`. The docs never describe a key that does nothing.
8. **`auth_cache_ttl` must be between 1s and 15m, and a value outside is refused rather than clamped.** PRD 01 D2 sets the 15-minute ceiling, and a refusal tells the operator what a clamp would hide.
9. **The default database is `$XDG_STATE_HOME/agent-fox/agent-fox.db`, and relative paths resolve against the config file's directory.** PRD 03 §8 names the XDG location. Resolving against the config file makes the file's meaning independent of the working directory.
10. **The SQLite driver is `modernc.org/sqlite` behind `database/sql`.** It is pure Go, so `make check` needs no cgo or C toolchain. It is also the driver of hub's lineage, from which `jobqueue` was extracted.
11. **The service's tables carry an `af_` prefix and their own migration record.** This settles PRD 03 §8's *spec time* question about two `jobs` tables. The service controls only its own names, and `jobqueue` may use `PRAGMA user_version`.
12. **A database newer than the binary is refused.** Running old code on a schema it does not know risks corrupting it.
13. **The key file holds base64 of 32 bytes, and no group or other permission bit may be set.**
    - The one rule is `mode & 0o077 == 0`, and owner bits are not checked. A world-readable key defeats encryption at rest, as it would for an ssh key, while an owner-only `0400` or `0700` file is just as private as `0600`.
    - The recipe is `(umask 077; openssl rand -base64 32 > key)`, because a bare `openssl rand -base64 32 > key` makes a `0644` file under the usual umask. `docs/configuration.md` shows the recipe in that form.
14. **Tokens are sealed with AES-256-GCM, with the slug as additional data and a version byte in front.** PRD 03 names AES-GCM. Binding the slug stops a ciphertext being copied to another row, and the version byte allows a later format change.
15. **A token that cannot be opened is reported as `token_state: unreadable` and the service keeps running.** Refusing to start would also block the `DELETE` or `PUT` that repairs it.
16. **Every hub path is under `<hub.url>/api/v1`.** The input writes both `/workspaces/:slug` and `/api/v1/user`, and hub serves its API under one prefix.
17. **For a workspace, hub's 403 means not found. For `/user`, 403 means refused.** PRD 01 D2 says "404 is 404", and a workspace that exists but is not the caller's must not be distinguishable from one that does not exist.
18. **Hub being unavailable gives 502 `hub_unavailable` and is never cached.** Caching an outage would lock out valid callers after hub recovers.
19. **A 401 from hub on a call made with the caller's token evicts all of that token's cache entries.** PRD 03 §2 says to evict on a 401, and a token revoked on hub must stop working at the next call that notices. Calls made with a PAT are excluded (Design Decision 31).
20. **Each cache is bounded to 10,000 entries.** Negative caching of arbitrary tokens would otherwise let a client grow memory without limit.
21. **Hub's visibility is the whole access rule.** Anyone who can see the workspace on hub may subscribe it, read it, replace it and delete it. PRD 01's non-goal says the service adds no tenancy of its own, and there is no user table (D2).
22. **The PAT's scopes are not checked at subscribe. The specs that use the scopes name a missing one at first use.** PRD 03 §3 suggests `GET /user/tokens` for a PAT that is the caller's, and leaves the check to *spec time*. The check is not built, for three reasons:
    - The scope list it would enforce is not fixed. PRD 01 §8 leaves the sandbox and forge scopes "*to be named by hub*", so a check against guessed names (`sandboxes:write`, `forge:write`) would refuse valid PATs or pass PATs that lack the real scopes.
    - Hub would not return a token's value, and no field is documented that would tell which listed token is the PAT. Without one, "the PAT is the caller's" cannot be decided.
    - No call in this spec needs any of those scopes.

    Instead, the Non-goals hand `agent_fox_worker` and `agent_fox_poll` a duty: turn hub's 403 on a PAT call into an error that names the hub operation, says the PAT lacks its scope, and points to `PUT` to replace it. A later spec may add the check here once hub names the scopes and documents the listing.
23. **`PUT` is a full replace: 201 on create, 200 on replace, and an absent `label_prefix` resets to `af:`.** The input writes `PUT … → 201` for every `PUT`, so this is a deliberate change from it.
    - RFC 9110 has an update of an existing resource answer 200 or 204, not 201.
    - The status tells a client whether it created or replaced a subscription.
    - `af_service_client` treats both 200 and 201 as success.
    - Every `PUT` asks for the token afresh anyway.
24. **The label prefix is 1–32 characters with no whitespace, control character or comma.** A comma would break the `labels=` filter of hub's issues API, which the poll uses. 32 characters plus the longest suffix (`needs-human`) stays within GitHub's 50-character label limit.
25. **This spec does not decide whether the subscription's `label_prefix` or the hub variable `AF_LABEL_PREFIX` governs the labels.** Only the specs that apply labels can deliver and test that choice, and those are `agent_fox_worker` and `agent_fox_poll`. This spec stores the prefix given at subscribe and returns it in the view. `docs/api.md` says it is the value given at subscribe, not necessarily the prefix in effect.
26. **`DELETE` in this spec only removes the row.** There are no jobs yet; `agent_fox_jobs` adds the cancelling of open jobs to it.
27. **The service serves plain HTTP on loopback by default, and TLS is a reverse proxy's job.** It keeps the first release small. A non-loopback `http` hub URL logs a warning instead of being refused, because compose-style internal networks are common.
28. **Errors use apikit's envelope `{error: {code, message, error_type?}}`, with the fixed codes of R7, and `error_type` is omitted in this release.** PRD 01 §6.2 wants a hub client to need no second error decoder. Because `error_type` is optional in that shape, leaving it out stays compatible, and no code of R7 needs a finer type.
29. **The version document reuses `af --version`'s fields and fallback rule.** One identity format across both binaries lets `af` compare itself with the service later.
30. **At subscribe, the workspace call with the PAT is the only gate, and `token_user` is best effort.** PRD 03 §3's validation is "`GET /workspaces/:slug` with it must answer 200". Requiring `/user` as well could refuse a workspace-bound credential (hub PRD 03) that sees the workspace but not `/user`.
    - `token_user` is kept because it tells an operator which hub user the PAT acts as. It is empty when hub refuses the PAT on `/user`.
    - Hub being unavailable on either call still gives 502, so the outcome never depends on a transient failure.
31. **Eviction-and-401 applies only to the caller's token. A PAT call never touches either cache, even when the PAT equals the caller's token.** This keeps R8's 422 for a refused PAT from contradicting R6's 401. It also keeps a PAT out of the caches entirely, so the caches only ever hold answers about the caller.
32. **Failing to bind `server.listen` at start exits 1.** Exit 2 means the operator must fix the configuration, the key or the database. A port in use or a denied bind is an environmental failure, like a listener failure after start, which also exits 1.
33. **Every start failure is one JSON log record at level `ERROR`, with `msg` `start failed` and an `error` attribute naming the key, file, address or reason.** Logging starts first, so stderr has one format that a log collector and a test can both read. Only flag-parsing usage text is plain, as `flag`'s convention is.
34. **The subscription view carries neither resolved settings nor label state in any scope of this split.** PRD 01 §6.2 lists them, but no scope owns them. Resolved settings are hub's variables, which `afc vars` already shows and which the worker reads for each attempt. Label state follows the labels that PRD 04 creates, and PRD 04 may add it.
35. **The user object's `id` field is an assumption to confirm against hub's published API description before implementation, and only the hub client knows the field's name.** Neither PRD 01 D2 nor §8 names a response field. Keeping the name in one place makes a correction a one-line change rather than a cause of 502 everywhere.
36. **The requirements state behaviour, and leave packages, types, signatures and column definitions to design.** R4, R5, R6 and R9 say what is stored, how each hub answer maps to the service's response, that tables are `af_`-prefixed with their own migration record, and that later scopes can read an opened PAT. How that is coded is not a requirement. `internal/hub/fakehub` stays named because later specs extend it.
37. **`docs/cli.md` gains an `agent-fox` section.** The steering requires `docs/cli.md` to change whenever CLI flags are added, and `agent-fox` adds `--config`, `--version` and `--help`.

## Dependencies

| Spec | Relationship | Why |
|---|---|---|
| `05_af_cli_envelope` | depends on | `agent-fox --version` and `GET /version` reuse `internal/toolio`'s version document and its ldflags-then-build-info rule (`internal/toolio/version.go`). Exporting the full document from `toolio` is the only change to that package. |

## Verified External API

None of the external sources could be read from this repository, so every signature below is **unverified**.

**`github.com/BurntSushi/toml` v1.6.0.** It is in `go.sum` and indirect in `go.mod`, and becomes a direct requirement. The source is in the module cache, outside the repository.
- `func DecodeFile(path string, v any) (MetaData, error)`
- `func (md *MetaData) Undecoded() []Key`, to refuse unknown keys
- `func (k Key) String() string`, giving the dotted key
- `toml.ParseError`, which carries the position of a syntax error

These are the library's long-stable v1 API.

**`modernc.org/sqlite`.** **NOT FOUND** in `go.mod` or `go.sum`, so it is a new requirement.
- Assumed: a blank import `_ "modernc.org/sqlite"` registers the `database/sql` driver named `"sqlite"`.
- Assumed: pragmas are set in the DSN as `file:<path>?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)`.

If `aflib/jobqueue` turns out to register a different driver, `agent_fox_jobs` switches to that one. The store works over a plain `database/sql` handle, so the switch is local.

**`github.com/agent-fox-dev/aflib/jobqueue`.** Not used by this spec. It is named here only because Design Decision 11 keeps its tables apart. Its API is read in `agent_fox_jobs`.

**Hub's HTTP API.** Hub is an external service with no source or schema in this repository. PRD 01 D2 and §8 name the two endpoints but no response field.
- `GET {hub}/api/v1/user` with `Authorization: Bearer <token>`: 200 with a JSON user object, or 401 for a refused token.
  - **Assumption, to confirm against hub's published API description before implementation:** the user's identifier is the field `id`, a string or a number, and `login` is an optional string.
  - The only id form in the docs is `usr_…`, in job and task examples.
  - Only the hub client knows these field names (Design Decision 35).
- `GET {hub}/api/v1/workspaces/{slug}` with a bearer token: 200 with a workspace JSON object, or 401, 403 or 404. No field of it is read by this spec.
- `GET {hub}/api/v1/user/tokens`: not used (Design Decision 22).

**Standard library** (stable, listed for the generator):
- `crypto/aes.NewCipher(key []byte) (cipher.Block, error)`
- `crypto/cipher.NewGCM(cipher.Block) (cipher.AEAD, error)`
- `AEAD.Seal(dst, nonce, plaintext, additionalData []byte) []byte` and `AEAD.Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)`
- `crypto/sha256.Sum256`
- `net/http.ServeMux` method-and-wildcard patterns (`"GET /api/v1/workspaces/{slug}"`, `(*http.Request).PathValue`)
- `http.MaxBytesReader`
- `log/slog.NewJSONHandler`
- `os/signal.NotifyContext`
