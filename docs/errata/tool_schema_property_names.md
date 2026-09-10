# Erratum: the artifact `$schema` field is written by the tool, not the model

Project-wide, so no numeric prefix. Recorded because the tool schema declared
to the model for a generation step is no longer byte-for-byte the artifact's
own JSON Schema, and the difference is deliberate.

## What a vendor accepts as a property name

A tool's input schema may only declare property names matching
`^[a-zA-Z0-9_.-]{1,64}$`. Anthropic's Messages API enforces it on the whole
request:

```
HTTP 400: invalid_request_error: tools.0.custom.input_schema.properties:
Property keys should match pattern '^[a-zA-Z0-9_.-]{1,64}$'
```

The three v2 artifacts each declare a property named `$schema` — the URI of
the schema the file is validated against, which is ordinary and correct in a
JSON document. `specgen.ToolSchema` converts those schemas verbatim, so
`submit_requirements`, `submit_test_spec` and `submit_tasks` each declared a
property no vendor would accept, and every `spec` run died on its first
generation phase, HTTP 400, *after* paying for the PRD phase.

**Was:** the model was asked for `$schema` and typed the URI from memory.

**Is:** `ToolSchema` drops a property name a tool schema cannot carry and
returns the names it dropped; `ArtifactSchema` accepts exactly one such name,
`$schema`, and the submit handler writes its value from the schema document's
own `$id` before the artifact is validated. The prompt templates no longer
show the field.

**Why not rename it in the format:** `$schema` is what makes the artifact
readable by every other JSON Schema tool, and the format is defined in the
[`spec`](https://github.com/agent-fox-dev/spec) repository rather than here.
The field also fails the test of something worth asking a model for: its value
is a constant, and a constant the program holds cannot be got wrong.

## What happens to any other undeclarable name

`ArtifactSchema` refuses it, naming the property, and `spec` fails in
pre-flight before a token is spent — the three tool schemas are converted at
the start of a run for that reason. The alternative is worse than a refusal: a
property the format requires and the tool schema never shows the model is a
validation loop nothing the model can say will escape.
