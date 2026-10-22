# Erratum 12: where the jobs layer does something other than the spec says

**Spec:** `12_agent_fox_jobs`. Each divergence below was decided during the survey or on the way, and is
recorded here because the spec stays as it was written. The code and the test of each are named so that a
reader can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is
named.

## 1. Four tests of spec 11 state facts that this spec changes (task 1)

**Spec:** the core's configuration gains `[workers].count` and `[database].retain` (R1), and its migration
list gains three migrations (12-REQ-1.3). Spec 11 had tested, as facts of that release, that those two keys
are refused as unknown and that the list has one migration.

**Code and tests:** the tests keep what they were about and stop naming what changed.

- `TestLoadRefusesUnknownKeysAndWrongTypes_TS_11_14` (`internal/service/config/config_test.go`) and
  `TestUnknownKeysAndWrongTypesExit2NamingTheKey_TS_11_14` (`cmd/agent-fox/start_test.go`) refuse
  `workers.limit` in place of `workers.count`, and a `database.retain` that is not a duration.
- `TestMigrationsApplyOnceInOrderAndRollBack_TS_11_26` (`internal/service/store/store_test.go`) wants each
  migration of the list exactly once, in order, whatever the length of the list is.
- `TestADatabaseFromANewerBinaryIsRefusedAndLeftAlone_TS_11_27` (`cmd/agent-fox/start_test.go`) wants the
  error to name the highest migration the binary knows, which it reads from the file the binary's own code
  has just migrated.
- `docs/configuration.md` no longer says that a `[workers]` table and `database.retain` are refused, and
  `TestConfigurationMdListsTheKeysAndTheKeyRecipe_TS_11_75` still finds the terms it looks for.

## 2. An attempt's queue item is keyed by its nonce, and its group is the workspace (task 4)

**Spec:** R4 and 12-REQ-4.1 have each attempt one `aflib/jobqueue` item with "key: the workspace slug", and
the queue serialize by key.

**Queue:** `Queue.Enqueue` refuses an item whose type and key equal those of an item that is queued or
running (it answers the id of that item and `duplicate = true`, `jobqueue.go:349`). With the slug as the
key, a workspace could hold one attempt in the queue, not the many that wait their turn. The queue has a
second way to serialize, the `Group` of an item: of the items of one type that share a group, one runs at
a time (`claimAndExecute`).

**Code:** the item has type `attempt`, the nonce `<job id>:<n>` as its key and as its nonce, the payload
`{"job_id", "attempt"}`, and the workspace as its group (`internal/service/jobs/queue.go`, `Enqueue`). The
nonce, the payload and the one-at-a-time per workspace are the spec's; only the field that holds the
slug differs.

**Test:** TS-12-23 (`internal/service/jobs/queue_test.go`) reads the queue's table and asserts the type,
the key, the group, the nonce and the payload. It sets the row `failed` with a delay in the future by hand
for the "delayed" case, because the delays of the layer itself come with task 6. Commit trailer key:
`queue_item_key_is_the_nonce`.

## 3. The order of a workspace's attempts is kept by the layer (task 4)

**Spec:** attempts of a workspace run "in the order they were enqueued" (R4); the adapter may add the
per-workspace order "when the module lacks it".

**Queue:** it claims by `available_at` and `created_at`, both RFC 3339 to the second
(`claimAndExecute`), so two attempts made in the same second can be claimed in either order.

**Code:** every attempt has a `queue_seq`, which numbers the attempts of all jobs in the order they were
made (migration 3). `Store.Plan` finds the first open attempt of the workspace by it; a handler whose attempt
is open but is not that one returns an error that the queue is to retry, and its item comes back after a
delay (`internal/service/jobs/store.go`, `queue.go`). The delay is constant: the retry policy is
`Base = Cap = RetryDelay`, `Multiplier = 1`, and a `MaxRetries` that is never reached. Because every open
attempt keeps its own item, the attempt at the head of the line is always in the queue.

**Test:** `TestAnAttemptRunsAfterTheOnesBeforeItInItsWorkspaceWhateverOrderTheQueueHasThem`
(`queue_test.go`) gives the queue five attempts in the opposite order, in the same second, and the runner is
called in the order they were made; TS-12-25 checks the order of a burst.

## 4. The runner returns the envelope as the JSON it was written as (task 4)

**Spec:** "a result: the envelope, the report text, …" (R4), and `GET …/envelope` serves "the stored
`envelope.json` verbatim" (12-REQ-9.6); the names and types are left to design.

**Code:** `jobs.Result.Envelope` is a `json.RawMessage`: the bytes a command wrote, which the layer stores
as they are and decodes into a `toolio.Envelope` to decide what the attempt came to. A value of
`toolio.Envelope` would have been encoded again, and `TestTheEnvelopeRouteServesTheStoredBytes` (task 9) would
have had nothing to compare with. `fakerunner.Env` writes an envelope the way a command does, indented, with a
newline at the end.
