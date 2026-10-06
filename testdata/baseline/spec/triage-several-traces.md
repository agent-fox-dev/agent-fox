# Let triage take several stack traces in one input

A crash report pasted from a CI log often holds several stack traces: the same
panic on three runners, or a panic and the timeout that followed it. `triage`
reads the input as one problem and files one issue.

Have `triage` recognise when an input holds more than one stack trace, group
the traces by their root cause, and file one issue per cause, each citing the
traces it covers. An input with one trace, or with none, behaves as today. A
`--dry-run` run reports the issues it would have filed.
