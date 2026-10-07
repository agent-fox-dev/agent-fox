# Hermetic failure: envtest in a clean environment

**Date:** 2026-10-07
**Spec:** 18_find_references_wiring

## Situation

`make test` fails in a clean environment (empty HOME, no global git
configuration) because `TestClearModelCredentialsLeavesNoWayToBeCredentialed`
in `internal/envtest/envtest_test.go:30` expects that setting
`ANTHROPIC_BASE_URL` alone passes `CheckCredentials`. In a clean environment
without Google Application Default Credentials, the Vertex AI path is selected
and the credential check fails with "no Google credential was found".

## Scope

This test and the `internal/envtest` package were **not changed** by spec 18.
The failure is pre-existing and environment-dependent: it passes on machines
with Google ADC configured and fails on machines without.

## Delivered behaviour

No file in `internal/envtest/` was modified by this change. The hermetic
failure is unrelated to the `find_references` wiring.
