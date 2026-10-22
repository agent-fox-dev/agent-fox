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
