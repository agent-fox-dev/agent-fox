# Configuration

`af` needs no configuration. What it reads from the environment, it reads
through `issuex`, the forge client of
[aflib](https://github.com/agent-fox-dev/aflib); the checks of the gate can be
pinned in [`.agent-fox/gate.json`](#gatejson); everything else is a flag (see
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
without touching the repository's own `.gitignore`, `gate.json` (below), `gate/<id>/`, the logs and the stored result of a gate run, and `gate/labels.json`, which maps labels to those ids (see [cli.md](cli.md#gate-run); safe to delete, the next run makes them again), and `run/<id>/`, one
directory per invocation of a command that has one: the envelope, the event
stream, the ledger, and a report or a panic's stack. Delete a run directory
when it is no use; nothing refers to one except the commands that carry a run
on. The layout is in [cli.md](cli.md#the-run-directory).

### gate.json

`.agent-fox/gate.json` pins the table of checks that the gate runs, so that
the table is the one that was measured and not whatever the files say today.
`af lang detect --write` writes it from the repository's own files; a person
or a loop may write it by hand.

```json
{
  "version": 1,
  "checks": [
    {"name": "make check", "argv": ["make", "check"], "kind": "check", "source": "Makefile:check"}
  ]
}
```

| Field | Meaning |
|---|---|
| `version` | Must be `1`. |
| `checks[].argv` | The command, with no shell. Required, and its first word is the program. |
| `checks[].name` | What the check is called; unique in the file. Default: `argv` joined by spaces. |
| `checks[].kind` | `lint`, `test` or `check` (both). Default `check`. |
| `checks[].source` | Where the check came from. Default `gate.json`. |

`available` is accepted in a check and ignored: whether a program can be run
is a fact about the machine, worked out on every read and never stored. Any
other field is an error.

**Precedence.** The table is `gate.json` when that file exists. Otherwise it
is detected from the files at the root: a Makefile with a `check` target, else
a Makefile with a `test` target (and `lint` if it has one), else the language
manifests (see [cli.md](cli.md#lang-detect)). A `gate.json` that does not
parse, names another version or holds a check that cannot run is
`failed`/`config`, naming the file; detection does not fall back to the
Makefile.
