"""Bootstrap mode for incremental spec creation.

Allows sequential creation of spec artifacts with cross-file validation
deferred until ``finalize()`` is called.
"""

from __future__ import annotations

from typing import Optional

from afspec.models import PRDDocument, Requirements, Spec, Tasks, TestSpec
from afspec.validation import ValidationError


class BootstrapSpec:
    """Bootstrap handle for incremental spec population.

    Allows setting each artifact independently without running
    cross-file validation until ``finalize()`` is called.
    """

    def __init__(self, spec_id: str, spec_name: str) -> None:
        self._spec_id = spec_id
        self._spec_name = spec_name
        self._prd: Optional[PRDDocument] = None
        self._requirements: Optional[Requirements] = None
        self._test_spec: Optional[TestSpec] = None
        self._tasks: Optional[Tasks] = None
        self._architecture: Optional[str] = None

    def set_prd(self, prd: PRDDocument) -> None:
        """Set the PRD artifact."""
        self._prd = prd

    def set_requirements(self, req: Requirements) -> None:
        """Set the requirements artifact."""
        self._requirements = req

    def set_test_spec(self, ts: TestSpec) -> None:
        """Set the test spec artifact."""
        self._test_spec = ts

    def set_tasks(self, t: Tasks) -> None:
        """Set the tasks artifact."""
        self._tasks = t

    def set_architecture(self, content: str) -> None:
        """Set the optional architecture content."""
        self._architecture = content

    def finalize(self) -> tuple[Spec | None, list[ValidationError]]:
        """Validate and return a Spec, or None with errors.

        Checks that all four artifacts have been set. If any are
        missing, returns ``(None, errors)`` listing the missing
        artifact names.

        If all artifacts are set, assembles a ``Spec``, runs
        schema and cross-file validation, and returns either
        ``(spec, [])`` or ``(None, errors)``.
        """
        # Check for missing artifacts
        missing: list[str] = []
        if self._prd is None:
            missing.append("prd")
        if self._requirements is None:
            missing.append("requirements")
        if self._test_spec is None:
            missing.append("test_spec")
        if self._tasks is None:
            missing.append("tasks")

        if missing:
            errors = [
                ValidationError(
                    file="",
                    path="",
                    message=f"Missing artifact: {name}",
                    rule="bootstrap",
                )
                for name in missing
            ]
            return None, errors

        # Assemble Spec
        assert self._prd is not None
        assert self._requirements is not None
        assert self._test_spec is not None
        assert self._tasks is not None

        spec = Spec(
            prd=self._prd,
            requirements=self._requirements,
            test_spec=self._test_spec,
            tasks=self._tasks,
            architecture=self._architecture,
        )

        # Run full validation
        from afspec.validation import validate

        errors = validate(spec)

        if errors:
            return None, errors

        return spec, []
