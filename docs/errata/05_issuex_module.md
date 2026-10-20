# Erratum 05: issuex is imported from aflib

**Spec:** `05_af_cli_envelope`: the non-goal "Changing `issuex`. It is consumed
from this module as it is", Design Decision 24, and the *Verified External API*
and `external_apis` entries, which give the import path
`github.com/agent-fox-dev/agentfox/issuex` and note that "`issuex` lives in this
module today ... and moves to its own module later (PRD 01, D9)".

## What changed

`issuex` has moved, as the spec expected. It is the `issuex` package of
`github.com/agent-fox-dev/aflib`, and this module no longer has an `issuex`
directory. `internal/toolio` imports `github.com/agent-fox-dev/aflib/issuex`
(for example `internal/toolio/forge.go:8` and `internal/toolio/app.go:26`), and
`go.mod:6` requires `aflib` at a commit, as a pseudo-version, because `aflib`
has no tag yet.

Every signature the spec lists is the same: `aflib`'s non-test sources are
byte-identical to those this module had, and its tests are this module's with
the import path changed. The spec's statements about what `issuex` provides, and
the tests that exercise it through `internal/toolio`, stand. Only the import
path and the file names in the notes column (`issuex/client.go` and so on) are
out of date; they are `aflib/issuex/client.go` and so on.

## Other specs

Specs `08_af_triage` and `09_af_fix`, not yet implemented, also give the old
import path in their `external_apis` and describe `issuex` as "read in this
repository". Whoever implements them will meet the drift described here.
