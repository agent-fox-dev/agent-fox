# Gemini resolves `$ref` inside a tool result

**Affects:** every tool, on the `google-generative-ai` wire, whenever a
read-only phase reads a JSON file that contains a `$`-prefixed key
**Fixed in:** `coder` — `provider/google`, `responseBytes`
**Seen:** `spec` on `docs/drafts/forge_neutral_issue_pr_client.md`, scope 2
of 4, `generate:test_spec`, with `google/gemini-3.8-flash`

## What happened

```
"error": {
  "stage": "test_spec", "category": "api",
  "message": "scope 2 of 4 (issuex_github): generate:test_spec: google: HTTP 400:
    INVALID_ARGUMENT: The referenced name `#/$defs/test` in function_response.response
    does not match to a display_name in the function_response.parts."
}
```

The model, writing `test_spec.json`, did the sensible thing and read the
format's own schema, `afspec/schemas/test-spec.v2.json`, with `read_file`.
The next request was refused by Gemini.

## Why

Two facts meet on this wire.

`read_file` returns the file verbatim as the tool result's text — no
envelope, no escaping — so the model reads a file rather than a JSON string
containing one. And AgentKit's Google provider, which must send
`functionResponse.response` as a JSON object because a bare string is a 400,
passes a result whose text already *is* a JSON object through unchanged,
keeping the tool's key order.

Together they put the schema's `{"$ref": "#/$defs/test"}` on the wire as real
keys of `function_response.response`. That field is not opaque to Gemini: a
`{"$ref": name}` object inside it is Gemini's own syntax for a reference to a
`function_response.parts` entry by `display_name`. There were no parts, so the
name matched nothing, and the request failed.

Nothing in this repository can prevent it. The tools run on AgentKit's
builtin `read_file`, and the encoding is the provider's. Anthropic and the
OpenAI wires carry tool results as strings and are unaffected.

## The fix

In `coder`, `provider/google/google.go`: a tool result whose text is a JSON
object with a `$`-prefixed key at any depth is wrapped under `"output"` like
plain text, where it is a string the model reads and nothing Gemini resolves.
Every other object still passes through verbatim. Rebuild the tools against a
`coder` checkout carrying that change.

The change is beside this file as
[`google_function_response_references.patch`](google_function_response_references.patch),
a `git format-patch` mailbox, until it lands upstream:

```sh
cd ../coder
git am ../agent-fox/docs/errata/google_function_response_references.patch
```

## Until then

The run is resumable. `spec` recorded the split in
`.specs/issuex_core.split.json` with scope 1 done, and running it on the same
input again starts at scope 2; the same read can recur under the same model
until `coder` is updated, so use `--vendor anthropic` or an OpenAI-wire model
for the remaining scopes if it does.
