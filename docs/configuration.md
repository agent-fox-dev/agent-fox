# Configuration

`af` has no configuration file. What it reads from the environment, it reads
through `issuex`, the forge client of
[aflib](https://github.com/agent-fox-dev/aflib); everything else is a flag (see
[cli.md](cli.md)).

## Environment variables

| Variable | Used for |
|---|---|
| `GITHUB_TOKEN` | The token for GitHub. |
| `GH_TOKEN` | The same token under the name the `gh` CLI uses; read when `GITHUB_TOKEN` is not set. |
| `GITLAB_TOKEN` | The token for GitLab. |
| `GITHUB_API_URL` | The API URL of a GitHub Enterprise Server, such as `https://ghe.example.com/api/v3`. Default `https://api.github.com`. |
| `GITLAB_API_URL` | The API URL of a self-hosted GitLab, such as `https://gitlab.example.com/api/v4`. Default `https://gitlab.com/api/v4`; `/api/v4` is added if it is missing. |

Without a token, `af` reads public issues and a 404 means the issue may be
private (`failed`/`auth` with the token to set in `fix_hint`). Writing needs a
token.

**Which forge.** The forge of a command is the one the issue URL names, else
the one `--repo` and the environment say, else the one the `origin` remote of
`--dir` names. If only the GitHub variables are set, it is GitHub; if only
the GitLab ones, GitLab; if both or neither, the origin remote decides. A
host that names neither forge (`git.example.com`) needs its API URL in
`GITHUB_API_URL` or `GITLAB_API_URL`; an issue URL on such a host is a `usage`
error until it has one, and a command that was given no issue URL runs with a
client that does nothing.

**Where a token goes.** The token of the environment is sent only to the host
the environment points it at: the host of `GITHUB_API_URL` or
`GITLAB_API_URL` when that names the forge's host, or the public forge when no
API URL is set. A request to any other host carries no token.

## The `.agent-fox` directory

`af` keeps its local state under the checkout it works in (`--dir`), in
`.agent-fox/`. It holds `.gitignore`, which ignores the whole directory
without touching the repository's own `.gitignore`, and `run/<id>/`, one
directory per invocation of a command that has one: the envelope, the event
stream, the ledger, and a report or a panic's stack. Delete a run directory
when it is no use; nothing refers to one except the commands that carry a run
on. The layout is in [cli.md](cli.md#the-run-directory).
