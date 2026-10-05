# Erratum: the shared transport is one request loop, not a `Transport` type

Project-wide, so no numeric prefix. Recorded because spec 01 named an exported
`Transport` as the transport every adapter would share, and the adapters never
used it. Reported as issue #91.

## What the specs said

- `01-REQ-6.1..6.5`, PRD 01 design decision 6: the 8 MB body cap, the single
  rate-limit retry with an injectable sleep, and the unauthenticated-404
  annotation live in a shared transport (`Transport.ExecuteRequest`,
  `HandleResponse`, `ReadResponse*`), "to be used across forge
  implementations".
- `02-REQ-7`: the GitHub adapter routes all HTTP requests through it.

## What happened

`githubClient.do` and `gitlabClient.do` each carried their own copy of the
body read, the retry loop and the status mapping, and nothing called the shared
code. The copies drifted from it and from each other: the shared mapping knew
only 409 as a conflict, GitHub also mapped 405, GitLab also 405 and 406, and
the shared `HTTPErrorFromResponse` read neither an error payload nor GitLab's
`RateLimit-*` headers. `TS-01-24` to `TS-01-26` tested code production never ran.

## What it is now

**Was:** an exported `Transport` (an `http.RoundTripper` with `ExecuteRequest`
and `HandleResponse`), unused by either adapter.

**Is:** one unexported request loop, `forgeHTTP.do` in `issuex/transport.go`,
that both adapters call. It owns the body cap, the retry, the sleep and the
status mapping (`classifyError`, which `HandleResponse` also uses). What
differs per forge is passed in by the adapter's `forge()` method: the headers
and credential, the error constructor that reads the forge's payload and
rate-limit headers, and the extra conflict statuses (GitLab's 406).

A third adapter supplies those three things and gets the rest.

## What changed that a caller or spec reader can see

- The exported `Transport` type, its `RoundTrip`, `ExecuteRequest` and
  `HandleResponse` methods are removed. Nothing in this repository used them.
  `MaxResponseBodyBytes`, `LimitResponseBody`, `ReadResponseBody`,
  `ReadResponse`, `HTTPErrorFromResponse`, `HandleResponse` and
  `HandleResponseStatus` stay.
- `HandleResponse` maps 405 to `ErrConflict`, as `02-REQ-7.5` already required
  of the GitHub adapter.
- A 429 that carries no `Retry-After` or reset header is no longer returned as
  a bare `HTTPError` by the adapters; it wraps `ErrRateLimited`, as
  `HandleResponse` always did. `IsRateLimited` was already true for it.
- `TS-01-24` and `TS-01-25` now drive `forgeHTTP.do` over a scripted
  `http.RoundTripper` instead of `Transport.ExecuteRequest`, with the same
  assertions. `TS-01-23` and `TS-01-26` are unchanged apart from a 405
  assertion. `TestTransportRoundTripper` is removed with the type it tested.
- `TestAdaptersShareOneTransportContract` runs the same upstream answers
  against both adapters and asserts the same error, so they cannot drift again.
