# Bound the tool calls a phase may make

A phase is bounded by turns and by cost, but a model that walks the tree with
`list_files` and `read_file` spends its turns on navigation long before either
bound is reached, and nothing says so until the phase runs out.

Add a shared flag, `--max-tool-calls N`, that bounds the calls one phase may
make to the read-only file tools. Past the bound, each further call is refused
with a tool error that says how many calls the phase made, so the model can
finish with what it has. `0`, the default, means no bound. The refusals count
in `tool_calls` and `tool_errors` like any other, and a phase that reached the
bound carries a `low` warning.
