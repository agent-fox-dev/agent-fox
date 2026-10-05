# Erratum: the shell builds the forge client from the parsed repository, not from `https://<host>`

Project-wide, so no numeric prefix beyond the spec it concerns. Recorded because
`04-REQ-3.2` says one thing and the code, deliberately, does another.

## What the spec said

`04-REQ-3.2`: when `App.execute` receives an argument `issuex.ParseIssueURL`
recognises with a non-empty `Repo.Host`, it instantiates an `issuex.Client` via
`issuex.NewWithOptions` "targeting the parsed host URL" — in the first
implementation, `Options{BaseURL: "https://" + host}`.

## Why that was wrong

The host in an issue URL is the **web** host. For GitHub that is `github.com`,
whose REST API is served from `api.github.com`; a client with `BaseURL`
`https://github.com` sent every call to `https://github.com/repos/...` and got a
404. The shared shell serves `spec`, `triage`, `fix` and `impl`, so all four were
affected.

**Was:** `NewWithOptions(Options{BaseURL: "https://" + ref.Repo.Host, ...})`.

**Is:** `NewWithOptions(Options{Repo: ref.Repo, UserAgent: ...})`, with the
parsed `Repo` (host included) and no `BaseURL`. Forge detection reads the host
from the repository and derives the right API base: `https://api.github.com` for
`github.com`, the host of `GITHUB_API_URL` for a GitHub Enterprise host, the
`/api/v4` address for GitLab. See [Choosing the forge](../configuration.md#choosing-the-forge).

The behaviour is covered by `issuex/host_repo_test.go` and by the shell's own
test of an issue URL (`internal/toolio/app_test.go`), introduced with the fix
(commit `9baa5fe`).
