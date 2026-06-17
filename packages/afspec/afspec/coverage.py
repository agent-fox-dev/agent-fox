"""Coverage computation for test spec against requirements.

Scans test cases, property tests, edge case tests, and smoke tests
against the requirements, correctness properties, and execution paths
to produce a Coverage summary.
"""

from __future__ import annotations

from afspec.models import Coverage, Requirements, TestSpec


def compute_coverage(ts: TestSpec, req: Requirements) -> Coverage:
    """Compute coverage of requirements by tests.

    Scans:
    - test_cases and edge_case_tests → requirements_covered (acceptance criteria + edge case IDs)
    - property_tests → properties_covered (correctness property IDs)
    - smoke_tests → paths_covered (execution path IDs)

    Everything not covered goes into gaps.
    """
    # Collect all IDs that need coverage
    all_criterion_ids: set[str] = set()
    for r in req.requirements:
        for c in r.acceptance_criteria:
            all_criterion_ids.add(c.id)
        for c in r.edge_cases:
            all_criterion_ids.add(c.id)

    all_property_ids = {p.id for p in req.correctness_properties}
    all_path_ids = {p.id for p in req.execution_paths}

    # Scan test entries for covered IDs
    tested_req_ids: set[str] = set()
    for tc in ts.test_cases:
        tested_req_ids.add(tc.requirement_id)
    for et in ts.edge_case_tests:
        tested_req_ids.add(et.requirement_id)

    tested_prop_ids: set[str] = set()
    for pt in ts.property_tests:
        tested_prop_ids.add(pt.property_id)

    tested_path_ids: set[str] = set()
    for st in ts.smoke_tests:
        tested_path_ids.add(st.execution_path_id)

    # Intersect with actual IDs to get covered sets
    requirements_covered = sorted(all_criterion_ids & tested_req_ids)
    properties_covered = sorted(all_property_ids & tested_prop_ids)
    paths_covered = sorted(all_path_ids & tested_path_ids)

    # Gaps: IDs that exist but aren't covered
    covered_set = set(requirements_covered) | set(properties_covered) | set(paths_covered)
    all_ids = all_criterion_ids | all_property_ids | all_path_ids
    gaps = sorted(all_ids - covered_set)

    return Coverage(
        requirements_covered=requirements_covered,
        properties_covered=properties_covered,
        paths_covered=paths_covered,
        gaps=gaps,
    )
