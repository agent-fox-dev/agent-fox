# Erratum: AuditEventType has 55 members, not 49

**Spec:** 01_afaudit_package
**Requirement:** 01-REQ-3.1
**Test Spec:** TS-01-10

## Divergence

The specification states:

> AuditEventType StrEnum (49 values)

and test TS-01-10 asserts `len(AuditEventType) == 49`.

However, the actual `AuditEventType` enum in
`packages/agentfox/agentfox/knowledge/audit.py` has **55 members**. Members
added since the spec was drafted include:

- `GIT_PUSH_FAILED` ("git.push_failed")
- `GIT_PUSH_RETRY_SUCCESS` ("git.push_retry_success")
- `WORKSPACE_SETUP_FAILED` ("workspace.setup_failed")
- `RUN_PREFLIGHT` ("run.preflight")
- `RUN_STALE_DETECTED` ("run.stale_detected")
- `WORKSPACE_HEALTH_CHECK` ("workspace.health_check")
- `WORKSPACE_FORCE_CLEAN` ("workspace.force_clean")
- `DEVELOP_SYNC` ("develop.sync")
- `DEVELOP_SYNC_FAILED` ("develop.sync_failed")
- `DEVELOP_FETCH_FAILED` ("develop.fetch_failed")

These were added to `AuditEventType` after the spec was authored but before
the afaudit extraction began.

## Resolution

Test TS-01-10 asserts `len(AuditEventType) == 55` to match the actual source
code being migrated. The migration preserves all 55 members verbatim from
`agentfox.knowledge.audit.AuditEventType`.

If new members are added in the future, the test assertion should be updated
accordingly — the exact count is a snapshot validation, not a cap.
