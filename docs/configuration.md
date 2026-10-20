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
`.agent-fox/`. It is never part of the file view that detection, the tree hash
and the map read, and its `.gitignore` keeps it out of the repository.

| Path | What it is |
|---|---|
| `.gitignore` | Ignores the whole directory, without touching the repository's own `.gitignore`. |
| `gate.json` | The table of checks that the gate runs; see [gate.json](#gatejson). |
| `guard.json` | The policy of `af guard check`; see [guard.json](#guardjson). |
| `gate/<id>/` | The logs and the stored result of a gate run (see [cli.md](cli.md#gate-run)). Safe to delete: the next run makes them again. |
| `gate/labels.json` | Maps labels to those ids. |
| `revert/<id>/` | The backup that [`revert-check`](cli.md#revert-check) keeps while it works and removes when the tree is back. One that is left means a check was killed: look at it before checking again. |
| `run/<id>/` | One directory per invocation of a command that has one: the envelope, the event stream, the ledger, and a report or a panic's stack. Delete one when it is no use; nothing refers to one except the commands that carry a run on. The layout is in [cli.md](cli.md#the-run-directory). |

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

### guard.json

`.agent-fox/guard.json` is the policy of [`af guard check`](cli.md#guard-check):
what a brain may not do. A loop writes it once for a run; a person may write it
by hand.

```json
{
  "version": 1,
  "suite": [["make", "check"], ["go", "test", "./..."]],
  "protected_paths": [".git/", ".agent-fox/", ".specs/", ".github/workflows/", ".gitlab-ci.yml"],
  "allow_programs": []
}
```

| Key | Meaning | Default when the key is missing |
|---|---|---|
| `version` | Must be `1`. | (required) |
| `suite` | The command forms that count as the full test suite, each a non-empty list of words. A shell command equal to one of them, ignoring the words that start with `-`, is refused with a pointer to `af gate run`. | The `suite` that [lang detect](cli.md#lang-detect) reports for `--dir`: the gate checks, `make check` and `make test` where the Makefile has them, and the forms of each language. |
| `protected_paths` | Paths relative to the checkout that only af writes. One that ends in `/` is a directory. A path may not be absolute or above the checkout. | `.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/` and `.gitlab-ci.yml`. |
| `allow_programs` | When it is not empty, the only programs a shell command may run, besides `af` and `git`. | Empty: no allowlist. |

A key that is there is taken as it is, an empty list included: `"protected_paths": []` protects nothing. Any other key is an error.

**Precedence.** The policy is the first that exists of: the file named by
`--policy`, which must be there; `<dir>/.agent-fox/guard.json`; the defaults,
all of them. A key a file does not have takes its default, and a file that is
used replaces the files after it entirely (`guard.json` is not read when
`--policy` is given). A file that does not parse, names another version, has an
unknown key or an entry that cannot be used is `failed`/`config`, naming the
file; no decision is made with a policy that was half read.
