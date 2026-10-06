---
spec_id: "01"
spec_name: "repo_web_url"
title: "A repository's web URL"
status: "active"
created_at: "2026-10-05T00:00:00Z"
updated_at: "2026-10-05T00:00:00Z"
intent_hash: null
schema_version: 2
source: "docs/development.md#navigation-baseline"
---
# A repository's web URL

## Intent

`issuex.IssueRef.URL` builds an issue's web URL, and with it the rules for a
repository's: the host defaults to `github.com`, a scheme written into `Host`
is honoured, a trailing slash is dropped, and a GitLab owner keeps its nested
groups. Nothing exposes the repository's own URL, so a caller that wants the
project page repeats those rules. This spec adds `Repo.WebURL` and has
`IssueRef.URL` build on it.

This package is the fixed `impl` input of the navigation baseline in
`docs/development.md`. It is run in a throwaway clone and never merged.

## Goals

- `issuex.Repo.WebURL()` returns a repository's web URL by the rules
  `IssueRef.URL` uses today.
- `IssueRef.URL()` returns exactly what it returns today, for every input,
  with the host and scheme rules in one place.

## Non-goals

- Changing any URL a tool prints or records.
- Parsing a web URL back into a `Repo`; `ParseRemote` and `ParseIssueURL`
  already do.
