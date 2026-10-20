<!-- agent-fox:triage v1 run=20261020_072649_a31f9c -->
## Diagnosis

**Severity:** high · **Confidence:** suspected

The handler dereferences a nil session.
It is created lazily.

## Affected files

- `auth/login.go:42` — nil session dereference
- `auth/session.go` — lazy creation

## Suggested fix

Create the session before the handler runs.

## Acceptance Criteria

- [ ] Submitting the login form with valid credentials returns 200
- [ ] No panic is logged

## Question

Should an expired session log the user out?

A. Yes
B. No

<sub>Diagnosed by agent-fox `af triage` (run 20261020_072649_a31f9c). The original report is kept below.</sub>

## Original report

<details>
<summary>Original report</summary>

<!-- agent-fox:original-report:begin -->
The login button does nothing.
It used to work.

<!-- agent-fox:original-report:end -->

</details>
