# Erratum 05: cmd/af holds more than main.go

**Spec:** `05_af_cli_envelope`, requirement 05-REQ-1.7 and test `TS-05-7`:
"`cmd/af` contains only package main wiring that exits with App.Main's return
value".

## What the spec asks for

That `cmd/af` is package main wiring and nothing else, and that it exits with
the code `App.Main` returns. The test as first written (`TestMakeBuildWritesBinary_TS_05_7`
in `internal/toolio/docs_test.go`) read that as a single file, `main.go`,
because spec 05 registers no command. That is stricter than the sentence.

## What the code does

`TestMakeBuildWritesBinary_TS_05_7` (`internal/toolio/docs_test.go`) now
asserts what the sentence is for: `cmd/af/main.go` exits with `App.Main`'s
return value, and no other file of `cmd/af` calls `os.Exit`. Other files may
exist; `cmd/af/lang.go` registers `af lang detect`, and the files of the other
ring-1 commands follow it.

## Why

Spec `06_af_ring1_facts` registers its six commands "on the spec-05 `toolio`
shell in `cmd/af`" (06-REQ-10.1) and names `cmd/af/lang.go` among the files of
its task 3. Design decision 3 of that spec keeps the command wiring in `cmd/af`
and the packages it calls free of `toolio`. A `cmd/af` of one file would put
every command's flags, result type and schema in `main.go`. The behaviour that
05-REQ-1.7 protects (the process is handed to the shell in one place, and the
binary builds with the Makefile's identity) is unchanged and still tested.
