# Erratum: no request is sent to a host that is not known to be a forge

Project-wide, so no numeric prefix. Recorded because spec 03 and spec 01
disagreed about what `issuex.NewWithOptions` does with a host whose name says
neither `github` nor `gitlab`, and the code followed spec 03. This erratum
settles it for spec 01 and withdraws the part of spec 03 that caused the
disagreement. Reported as issue #88.

## What the two specs said

- `01-REQ-4.7`: when the forge cannot be determined from `BaseURL`, the
  environment or the `origin` remote, `NewWithOptions` returns `(nil, err)`
  with `errors.Is(err, ErrAmbiguousForge)`.
- `03-REQ-1.4`: before returning `ErrAmbiguousForge`, `NewWithOptions` sends
  `GET /api/v4/version` to the host and returns a GitLab client when the reply
  is a GitLab version payload.

## Why the probe is withdrawn

The probe carried the GitLab credential (`Options.Token`, else
`$GITLAB_TOKEN`) as `PRIVATE-TOKEN`. It went to whatever host the caller
derived from `BaseURL`, `RemoteURL` or `Repo.Host`, before that host was known
to be a GitLab. Those inputs include a mistyped `--repo`, a `GITLAB_API_URL`
typo, an `origin` remote, and the host of an issue URL given to a tool as its
argument, so a URL for `evil.example` was enough to send the token there. The
probe URL was `http://` whenever the input was.

No form of the probe is both safe and useful:

- with the credential, it needs a host the user has already named for GitLab,
  and the only such name is `GITLAB_API_URL`; when that is set, detection
  resolves GitLab without a probe;
- without the credential, it cannot succeed: `GET /api/v4/version` on
  GitLab answers `401 Unauthorized` to an anonymous request (checked against
  gitlab.com).

**Was:** `NewWithOptions` caught `ErrAmbiguousForge` and probed the host.

**Is:** `NewWithOptions` returns `ErrAmbiguousForge` and sends nothing. The
spec 01 contract stands unchanged, and `03-REQ-1.4` is withdrawn. A
self-hosted GitLab is named with `GITLAB_API_URL`, as `docs/configuration.md`
already says.

## What changed in the tests

- `TS-03-4` (the probe) is removed; `TestNewWithOptions_UnclassifiedHostSendsNothing`
  replaces it and asserts that an unclassified host gets no request and
  `ErrAmbiguousForge`, whether the host arrives as `BaseURL`, `RemoteURL`,
  `Repo.Host` (over TLS) or with an explicit `Options.Token`.
- `TS-03-32` keeps its `GetRepository` coverage of a self-hosted GitLab,
  reaching it through `GITLAB_API_URL` and asserting that no probe is sent.
- `TS-01-15` no longer opens a connection to `forge.example.com`, because the
  case it uses now returns before any request.
