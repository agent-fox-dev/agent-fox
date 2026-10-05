# Show each phase's tool bytes in the summary view

The envelope's `usage.phases[]` carries `tool_calls` and `tool_result_bytes`,
but only the full view and the report file show them: `--detail summary`, the
default, drops them with the rest of `usage.phases[]`. A caller that watches
navigation cost has to ask for `--detail full` on every run.

Keep, in every tool's summary view, one figure per phase: the total of its
`tool_result_bytes`, under the phase's name. The full view and the report file
do not change.
