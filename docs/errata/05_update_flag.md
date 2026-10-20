# Erratum 05: the golden files are regenerated with `go test ./internal/toolio ./cmd/af -update`

**Spec:** `05_af_cli_envelope`, requirement 05-REQ-10.3 and the schema
section of the PRD, which say the golden files are rewritten "with `go test
./... -update`".

## What the spec asks for

`internal/toolio/testdata/schema.golden.json` and
`cmd/af/testdata/schema.golden.json` are compared with the output of
`af --schema` and rewritten when the tests run with `-update`, as `go test
./... -update`.

## What the code does

The `-update` flag is defined by the two test packages that own a golden
file (`internal/toolio/schema_test.go:20` and `cmd/af/schema_test.go:14`).
`go test ./... -update` passes the flag to every test binary of the module,
and the test binary of `internal/gitx` does not define it (nor did the one of
`issuex`, while that package was part of this module):

```
flag provided but not defined: -update
FAIL	github.com/agent-fox-dev/agentfox/internal/gitx
```

so that command stops with a failure in a package that has no golden file. The golden files are regenerated with

```sh
go test ./internal/toolio ./cmd/af -update
```

which rewrites both and passes. `TestSchemaGolden_TS_05_70` of each package
compares the output with the committed file, and `TestGoldenMechanism`
(`internal/toolio/schema_test.go`) shows that a missing or modified file
fails the comparison and that rewriting makes it pass.

## Why it was not made to work

The spec's own non-goal was "Changing `issuex`". Defining an unused `update`
flag in the test files of `internal/gitx` (and then of `issuex`) would make
the literal command pass, at the price of a flag in packages that have
nothing to rewrite, and of every future package having to do the same.
`docs/cli.md` gives the command that works.
