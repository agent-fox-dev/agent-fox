# Errata: 05 — GiteaPlatform factory path and afissues re-exports

## platform_factory.py path

**Spec says:** `agentfox/nightshift/platform_factory.py`

**Actual path:** `packages/agentfox/agentfox/nightshift/platform_factory.py`

The spec omits the `packages/agentfox/` monorepo prefix. All references to
`agentfox/nightshift/platform_factory.py` should read
`packages/agentfox/agentfox/nightshift/platform_factory.py`.

## afissues re-exports (05-REQ-19.1)

**Spec says:** `afissues.__init__` should re-export `GiteaPlatform` from
`afissues.gitea` alongside `GitHubPlatform` and `GitLabPlatform`.

**Actual:** The `afissues` package (`packages/afissues/`) does not contain
the platform implementations. The concrete classes live under
`agentfox.platform.*` (e.g. `agentfox.platform.gitea.GiteaPlatform`).
The `afissues` package cannot import from `agentfox` because `afissues`
has no dependency on `agentfox` (its only dependency is `httpx>=0.27`).

**Resolution:** `packages/afissues/afissues/gitea.py` contains a stub
docstring noting that the real implementation lives at
`agentfox.platform.gitea`. Re-exports will be wired once spec 03
(extract_platform_afissues) completes the full extraction. The test
`TS-05-52` is adapted to import from `agentfox.platform.*` instead of
`afissues.*`.

## platform_factory routing model

**Spec says:** The factory hard-codes GitHub-only logic and spec 04 was
supposed to add GitLab routing and a Gitea stub.

**Actual:** The factory was generalized to support multiple platform types
using lookup tables (`_TOKEN_ENV_VARS`, `_REMOTE_PARSERS`, `_DEFAULT_URLS`)
and a shared `_resolve_remote()` helper. The Gitea branch was added
directly without an intermediate stub phase.

## create_pr keyword-only arguments

**Spec says:** `create_pr(self, title, body, head, base)` with positional
parameters.

**Actual:** `PlatformProtocol` declares `create_pr(self, *, title, body,
head, base)` where all arguments are keyword-only. `GiteaPlatform`
follows the protocol. Tests use keyword arguments accordingly.

## close() is async

**Spec says:** `close()` should be synchronous.

**Actual:** `PlatformProtocol.close()` is declared as `async def`.
`GiteaPlatform.close()` follows the protocol and is also `async def`.
