# Erratum: `detect.go` changed outside spec 03's task list, and a third forge withdrawn

Recorded because PR #23 (spec 03) edited `issuex/detect.go` (+42/−3) and
`issuex/options_test.go`, neither in any of spec 03's tasks' `touches`, and one of
the changes went against the PRDs' non-goals ("supporting git forges other than
GitHub and GitLab"). Reported as issue #124.

## What PR #23 changed

1. **`extractHostFromRemote` and the ambiguous-remote handling.** A prerequisite
   for the version probe of `03-REQ-1.4`. The probe has since been withdrawn
   (see [unclassified_host_probe](unclassified_host_probe.md)); the helper stays,
   because it names the offending host in the `ErrAmbiguousForge` message for a
   remote whose host says neither `github` nor `gitlab`.
2. **A third forge type**, `ForgeTypeBitbucket`, and a `hasBB` branch in the base
   URL classification, which no adapter implements. It was introduced so that
   `TS-01-19` still had an unsupported forge to exercise once both real adapters
   were registered: that test was retargeted to `https://bitbucket.example.com`.

   Side effect: a GitHub base URL that happened to contain the substring
   `bitbucket` (`https://github.com/bitbucket-mirror`) returned
   `ErrAmbiguousForge`, where `01-REQ-4.2` says a URL containing `github`
   resolves GitHub.

## What it is now

**Was:** three forge types, with a Bitbucket branch no adapter served, and
`GitHub` and `GitLab` built by direct branches in `NewWithOptions`, so
`ErrUnsupportedForge` (`01-REQ-4.8`) could only be reached through the third
type.

**Is:** `ForgeTypeBitbucket` and the `hasBB` branch are removed; a base URL is
GitHub when it contains `github` and not `gitlab`, GitLab when the reverse, and
ambiguous only when it names both. `NewWithOptions` builds every client through
the adapter registry (GitHub and GitLab are registered in `init`), so
`ErrUnsupportedForge` is a registry miss, and `TS-01-19` now exercises exactly
that: it removes an adapter for the test's duration and asserts the error and a
nil client (`issuex/registry_test.go`). Another forge would be added by
registering an adapter and teaching detection its name, as a spec of its own.
