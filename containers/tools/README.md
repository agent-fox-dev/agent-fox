# The tools container

`containers/tools/Containerfile` builds `quay.io/agentfox/tools`: the four
agent-fox tools (`spec`, `issue`, `fix`, `impl`), the `af` and `nightshift`
stubs, the `pi` agent and Claude Code, on top of the sandbox image built from
`containers/sandbox/Containerfile` (`quay.io/agentfox/sandbox`, a RHEL 10
base with Go, Node, Rust and Python toolchains). `make build-containers` builds
both with `podman`; the tools image needs the `coder` checkout at `../coder`,
which it takes as a build context.

## Running with Podman

Start the container in the background:

```bash
podman run -d --name tools quay.io/agentfox/tools:latest sleep infinity
```

Enter the running container with an interactive shell to run the tools
(`spec`, `issue`, `fix`, `impl`) or the agents (`pi`, `claude`):

```bash
podman exec -it tools bash
```

When you're done, stop and remove the container:

```bash
podman rm -f tools
```

## Providing credentials

Pass credentials such as the Anthropic API key and a forge token via `-e` (or
`--env-file` for several secrets). The tools read the same variables as
outside the container — see [Configuration](../../docs/configuration.md):

```bash
podman run -d --name tools \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  -e GITHUB_TOKEN="$GITHUB_TOKEN" \
  quay.io/agentfox/tools:latest sleep infinity
```

## Mounting a local workspace

The container's working directory is `/opt/app-root/workspace` (`$WORKSPACE`),
separate from `$HOME` (`/opt/app-root/src`) where agent config, credentials,
and session state are stored. Bind-mount a local project directory onto
`$WORKSPACE` so the tools operate on your code without mixing agent state
into it:

```bash
podman run -d --name tools \
  -e ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  -v "$(pwd)":/opt/app-root/workspace:Z \
  quay.io/agentfox/tools:latest sleep infinity
```

Then enter the container and run a tool against the mounted workspace:

```bash
podman exec -it tools bash
cd /opt/app-root/workspace
issue ./crash.log --dry-run
```

Note: the `:Z` suffix relabels the volume for SELinux (needed on Fedora/RHEL
hosts); omit it on systems without SELinux.
