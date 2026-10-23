# Erratum 15: where the poll does something other than the spec says

**Spec:** `15_agent_fox_poll`. Each divergence below was decided during the survey or on the way, and is recorded
here because the spec stays as it was written. The code and the test of each are named so that a reader can check
what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is named.

## 1. Hub's issues list has no pages, and cannot say that it cut a list short (task 1)

**Spec:** R3 and "Verified External API": hub is assumed to honour "a page size of 100 and say when a further page
exists, either with a cursor field or a `Link` header", and "the hub client confirms the real paging before
implementation". 15-REQ-3.3: "`Incomplete` is true only when the client stopped at `Limit` while hub reported a
further page".

**Code:** hub's OpenAPI document (`../hub/docs/openapi.yaml`, `listForgeIssues`) and its `docs/api.md` ("the list
routes answer the bare array and do not show those two flags") say that `GET /api/v1/workspaces/{slug}/issues`
answers a bare array, takes `limit` (1 to 100, 30 when it is absent, always passed on to the forge) and has no
pages, no cursor and no `Link` header; it leaves pull requests out itself. The client (`ListIssues`,
`internal/hub/forge.go`) therefore:

- asks for `state=open&labels=<label>&limit=100`, which is the page of 100;
- follows a further page only if an answer names one in a `Link` header with `rel="next"`, by asking again with
  `&page=N`. Real hub never does, so a list is one request; the fake hub does, so that `Limit` above 100 and the
  further-page rule of the spec are tested (`TestListIssuesFollowsFurtherPagesAndIsIncompleteOnlyWhenItStoppedAtTheLimitWithMoreToRead_TS_15_16`);
- leaves out an item marked `is_pr`, as the spec says, although real hub has done so already;
- sets `Incomplete` when it stopped at `Limit` with issues left, as the spec says, **and also when the last page
  is as long as the page asked for and says nothing of a further page**. Hub cannot say it cut a list at 100, so a
  list of exactly 100 is not known to be complete, and release and settlement (R5, R6) must not treat it as complete.
  The cost is a `WARN` and a hold or an owed removal left for a cycle, for a workspace with 100 labelled issues.

The fake hub serves the same route from its issue state, with `Link` paging that a test can switch off to behave
as the real hub does (`SetPageLinks`), the two lags, and an order a test can set (`internal/hub/fakehub/issues.go`).
Deviation key: `issues_list_has_no_pages`.
