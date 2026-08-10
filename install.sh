#!/bin/sh

set -eu

uv tool install af --from git+https://github.com/agent-fox-dev/agent-fox.git#subdirectory=packages/af
uv tool install nightshift --from git+https://github.com/agent-fox-dev/agent-fox.git#subdirectory=packages/nightshift