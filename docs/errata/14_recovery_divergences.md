# Erratum 14: where the recovery of the worker does something other than the spec says

**Spec:** `14_agent_fox_worker_recovery`. Each divergence below was decided during the survey or on the way,
and is recorded here because the spec stays as it was written. The code and the test of each are named so that
a reader can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is
named.

## 1. Hub lists leases in one array, without pages (task 1)

**Spec:** "following hub's pagination if it has any" (R8, 14-REQ-8.2), step 1.5 ("`ListSandboxes` (following
pagination)"), and TS-14-51, which lists an orphan "across two pages".

**Code:** hub's OpenAPI document (`../hub/docs/openapi.yaml`, `listSandboxLeases`) lists the leases of a
workspace as a bare array, newest first, at most 200, with the live ones only (`leased` and `active`) unless
`all=true` is asked for, and has no pages. `hub.Client.ListSandboxes` (`internal/hub/calls.go`) reads that one
array and keeps the id, the purpose, the state, the labels and the end of each lease; the fake hub answers the
same way (`listLeases`, `internal/hub/fakehub/gateway.go`). There is nothing to follow, so the "two pages" of
TS-14-51 are several leases in one listing
(`TestTheLeasesOfAWorkspaceAreListedWithTheirLabelsAndEnd`, `internal/hub/calls_test.go`). Deviation key:
`lease_listing_has_no_pages`.
