# Errata: PR creation exception propagation supersedes branch-mode fallback

**Spec:** 06_pr_lifecycle_labels
**Requirement:** 06-REQ-8.4
**Supersedes:** 02-REQ-4.E3

## Summary

Spec 06 (06-REQ-8.4) requires that when `create_pr()` raises an exception
during `_integrate_fix()` in PR mode, the exception must propagate to the
caller without setting `self._pr_number`. This ensures `_handle_result()` is
never called with a `"pr_created"` status when no PR was actually created.

The previous behavior from spec 02 (02-REQ-4.E3) caught `IntegrationError`
from `create_pr()` and fell back to branch-mode semantics (logging the error,
posting a branch-mode comment, and returning `("merged", changed_files)`).

## Rationale

The branch-mode fallback caused a premature-close bug: when `_integrate_fix()`
returned `"merged"`, `_handle_result()` would close the issue and apply the
`af:fixed` label even though no PR or merge had occurred. By letting the
exception propagate, the caller (`process_issue`) handles it in its outer
`try/except` block, posting a safe failure comment and leaving the issue open.

## Impact

If `create_pr()` fails after the branch has been pushed to the remote, the
branch remains available on the remote but no fallback comment is posted
directly by `_integrate_fix()`. Instead, the `process_issue` exception handler
posts a generic failure comment that includes the branch name, giving the
operator enough context to investigate.
