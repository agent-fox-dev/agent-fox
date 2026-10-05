# Erratum: the rate-limit backoff is `Retry-After` plus one second

Recorded because spec 03 and spec 01 disagree, and the code follows spec 01 for
both adapters.

## What the specs said

- `01-REQ-6.2` and `TS-01-24`: a rate-limited request sleeps for the duration
  `HTTPError.RetryAfter()` calculates, which adds a one-second buffer to the
  `Retry-After` header (a header of 10 s sleeps 11 s), and retries once.
- `03-REQ-7.2` and the pseudocode of `TS-03-26` and `TS-03-31`: the sleep equals
  the header.

## What the code does

`HTTPError.RetryAfter()` returns `Retry-After` plus one second, and both adapters
sleep that long: `Retry-After: 10` sleeps 11 s, `Retry-After: 2` sleeps 3 s. The
buffer keeps the retry from landing in the same second the forge is still
counting. The 120-second ceiling applies to the buffered figure (`Retry-After:
119` retries after 120 s; `120` aborts).

## What changed in the tests

`TS-03-26` accepted either 10 s or 11 s and `TS-03-31` sent `Retry-After: 1` to
observe 2 s, so neither said which rule held. Both are exact now (11 s for 10; 3 s
for 2), and `TestRateLimitBackoffIsRetryAfterPlusOneSecond` asserts the rule for
GitHub and GitLab alike.
