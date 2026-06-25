# Erratum: CI-required marker registration (spec 09)

**Spec:** 09_worktree_path_collision
**Requirement:** 09-REQ-8.4
**Task:** 6.2

## Divergence

The spec mandates that PRD test 4 (concurrent-dispatch test,
`TestConcurrentDistinctPaths::test_concurrent_distinct_paths`) be registered
as a "blocking required check in the PR pytest pipeline."

## Current state

This repository does not have a CI pipeline configuration (no `.github/workflows/`,
`.gitlab-ci.yml`, `Jenkinsfile`, or equivalent). Therefore:

1. The test is marked with `@pytest.mark.ci_required` in the test file.
2. The `ci_required` marker is registered in `packages/agentfox/pyproject.toml`
   under `[tool.pytest.ini_options].markers`.
3. When a CI pipeline is introduced, it should include a step that runs
   `pytest -m ci_required` (or includes the full test suite) and is configured
   as a required status check blocking PR merge.

## Rationale

Registering the marker now ensures:
- No pytest warnings about unknown markers.
- The marker is discoverable via `pytest --markers`.
- CI pipeline setup can filter on `-m ci_required` when added.

The intent of 09-REQ-8.4 is satisfied at the test-level; the CI infrastructure
step is deferred until the project introduces a CI pipeline.
