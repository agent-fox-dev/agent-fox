# agent-fox

agent-fox is an autonomous spec-first coding agent (golang version).

The new mono-repo for all agent-fox (golang) code. It provides library modules, CLI tools and services. The repo depends on repos

- [`coder`](https://github.com/agent-fox-dev/coder)
- [`spec`](https://github.com/agent-fox-dev/spec)
- [`apikit`](https://github.com/txsvc/apikit)

It works with [`hub`](https://github.com/agent-fox-dev/hub) as its backend service.

The repo is a typical `golang` project:

- cmd/ for executable commands, CLIs etc
- internal/ for any code that must not be included by anyone outside the repo
- <feature module>/ for any "features" that can be used "standalone" or conceptually belong to the same domain
- <repo root>/ the most important entry points into the repo.

