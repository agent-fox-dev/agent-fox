# Erratum 05: `--schema`, `--output` and `--emit-events` are gone

**Spec:** 05 (af CLI envelope), 16 (af service client).

## What the spec asks for

05-REQ-10 has `af --schema` print the JSON Schema of the envelope with the result schema of
every command under `$defs`, compared with committed golden files; 05-REQ-2.5 and 05-REQ-9.7
give every command `--output`, which writes a copy of the envelope, and `--emit-events`, which
appends the events to a file. 16-REQ-2.3 has both modes of `af triage` and `af fix` accept them.

## What the code does

None of the three exists. Nothing in agent-fox read them: a box takes a command's envelope from
its stdout, the worker reads the run directory, and a caller in the same process has
`App.EventSink`. Gone with them are the result schemas of the commands
(`internal/afcmd/schemas`, `cmd/af/schemas`, `Command.ResultSchema`), the envelope schema of
`internal/toolio`, the golden files and the `-update` flag of their tests, and the warning
`output_unwritable`. The envelope is unchanged, and `schema_version` stays `5.0.0`: it names
the envelope's fields, which are what they were. `af jobs watch` still writes each event of the
job to stderr.

**Test:** `TestEnvelopeCopiesAreIdentical_TS_05_13`, `TestEventStream_TS_05_66`,
`TestShortCircuitsDoNoWork_TS_05_8` (`internal/toolio`),
`TestContextAndTimeoutAreAcceptedInBothModes_TS_16_11` (`cmd/af/client_mode_test.go`).
