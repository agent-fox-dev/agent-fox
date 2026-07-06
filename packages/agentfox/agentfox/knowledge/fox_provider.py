"""KnowledgeProvider protocol and concrete implementation.

Defines the KnowledgeProvider protocol (the clean boundary between the
engine and any knowledge implementation) and the concrete
FoxKnowledgeProvider (review carry-forward + context summaries).

Requirements: 116-REQ-1.3, 116-REQ-1.4, 116-REQ-2.2,
              116-REQ-6.1, 116-REQ-6.2, 116-REQ-6.3, 116-REQ-6.E1,
              117-REQ-1.1, 117-REQ-6.1, 117-REQ-6.3, 117-REQ-7.4
"""

from __future__ import annotations

import logging
from typing import Any, Protocol, runtime_checkable

from agentfox.core.config import KnowledgeProviderConfig
from agentfox.core.errors import KnowledgeStoreError
from agentfox.knowledge.db import KnowledgeDB
from agentfox.knowledge.formatting import (
    _SEVERITY_RANK,
    _extract_keywords,
    _score_relevance,
    format_finding_parts,
    generate_archetype_summary,
    sort_findings,
)
from agentfox.knowledge.review_store import (
    supersede_drift_findings_by_files,
    supersede_injected_findings,
)

logger = logging.getLogger(__name__)


def _query_safe(query_fn, args, *, label: str, spec_name: str, default=None):
    """Run a query function, returning *default* on any exception.

    Centralises the try/except/log/return-empty pattern used by all
    ``_query_*`` helpers.  The *query_fn* is called with ``*args`` inside
    a broad ``except Exception`` handler; failures are logged at DEBUG
    level (the table may simply not exist in a fresh database).

    Args:
        query_fn: Callable to invoke.
        args: Positional arguments forwarded via ``query_fn(*args)``.
        label: Human-readable label for the debug log message
            (e.g. ``"review findings"``).
        spec_name: Spec name included in the debug log message.
        default: Value returned when *query_fn* raises.  Defaults to
            ``[]``; callers that expect a tuple should pass an explicit
            default such as ``([], [])``.
    """
    if default is None:
        default = []
    try:
        return query_fn(*args)
    except Exception:
        logger.debug("Could not query %s for %s", label, spec_name)
        return default


# Re-export formatting helpers so existing importers of fox_provider
# continue to work unchanged.
__all__ = [
    "KnowledgeProvider",
    "NoOpKnowledgeProvider",
    "FoxKnowledgeProvider",
    "_SEVERITY_RANK",
    "_extract_keywords",
    "_score_relevance",
    "generate_archetype_summary",
]


@runtime_checkable
class KnowledgeProvider(Protocol):
    """Protocol defining the interface between the engine and a knowledge implementation.

    Any class that implements both ``ingest`` and ``retrieve`` with the
    correct signatures satisfies this protocol at runtime (``isinstance``
    check) thanks to the ``@runtime_checkable`` decorator.
    """

    def ingest(
        self,
        session_id: str,
        spec_name: str,
        context: dict[str, Any],
    ) -> None:
        """Ingest knowledge from a completed session."""
        ...

    def retrieve(
        self,
        spec_name: str,
        task_description: str,
        task_group: str | None = None,
        session_id: str | None = None,
        file_footprint: list[str] | None = None,
    ) -> list[str]:
        """Retrieve knowledge context for an upcoming session."""
        ...


class NoOpKnowledgeProvider:
    """Knowledge provider that does nothing.

    Default implementation used when no knowledge system is configured.
    ``ingest()`` is a no-op and ``retrieve()`` always returns an empty list.
    """

    def ingest(
        self,
        session_id: str,
        spec_name: str,
        context: dict[str, Any],
    ) -> None:
        """Accept and discard session knowledge context."""
        return None

    def retrieve(
        self,
        spec_name: str,
        task_description: str,
        task_group: str | None = None,
        session_id: str | None = None,
        file_footprint: list[str] | None = None,
    ) -> list[str]:
        """Return an empty list --- no knowledge is available."""
        return []


class FoxKnowledgeProvider:
    """Concrete KnowledgeProvider: review carry-forward + context summaries.

    Retrieves active critical/major review findings, cross-group reviews,
    and same-spec context summaries for a spec.  Stores session summaries
    on ingestion.  Satisfies the ``KnowledgeProvider`` protocol defined
    in spec 114 (``@runtime_checkable``).
    """

    def __init__(
        self,
        knowledge_db: KnowledgeDB,
        config: KnowledgeProviderConfig,
    ) -> None:
        self._knowledge_db = knowledge_db
        self._config = config
        self._run_id: str | None = None

    def set_run_id(self, run_id: str) -> None:
        """Set the current run ID for summary queries.

        Stores the run ID for use in ``_query_same_spec_summaries()``.
        An empty string is treated as unset (``None``).

        Requirements: 120-REQ-1.1, 120-REQ-1.2
        """
        self._run_id = run_id if run_id else None

    # ------------------------------------------------------------------
    # KnowledgeProvider protocol methods
    # ------------------------------------------------------------------

    def retrieve(
        self,
        spec_name: str,
        task_description: str,
        task_group: str | None = None,
        session_id: str | None = None,
        file_footprint: list[str] | None = None,
    ) -> list[str]:
        """Retrieve knowledge context for an upcoming session.

        Queries active critical/major review findings for the given spec
        and returns them as prefixed strings, capped at ``max_items``.

        When *session_id* is provided, the IDs of every review finding
        that appears in the returned list are recorded in the
        ``finding_injections`` table.  A subsequent successful
        ``ingest()`` call for the same session then supersedes those
        findings so they are not re-injected into future sessions.

        Args:
            spec_name: Name of the spec being worked on.
            task_description: Human-readable description of the task.
            task_group: Optional task group identifier to restrict review
                findings to those tagged for this group.  When ``None``,
                findings from all task groups are returned.
            session_id: Optional node ID of the current session.  When
                provided, injected finding IDs are persisted for later
                deduplication.  Callers that omit this parameter get the
                same retrieval behaviour as before (backward-compatible
                default).
            file_footprint: Optional list of file paths the current spec
                modifies.  Used to find cross-spec drift findings from
                other specs that reference overlapping files.

        Returns:
            List of formatted text blocks ready for prompt injection.

        Raises:
            KnowledgeStoreError: If the database connection is closed or
                a query fails unexpectedly.

        Requirements: 117-REQ-6.1, 117-REQ-6.3, 558-AC-1, 558-AC-4
        """
        try:
            conn = self._knowledge_db.connection
        except KnowledgeStoreError:
            raise

        reviews, review_ids = self._query_reviews(
            conn, spec_name, task_group=task_group, task_description=task_description
        )

        drift, drift_ids = self._query_drift(conn, spec_name, task_group=task_group, task_description=task_description)

        # Build a parallel list of (text, finding_id) so we can track which
        # finding IDs survive the max_items cap.  Review and drift findings
        # share the same cap and injection lifecycle.
        items_with_ids: list[tuple[str, str]] = list(zip(reviews, review_ids)) + list(zip(drift, drift_ids))

        # Cross-group items: findings from other task groups in the same spec.
        # These are informational (not tracked for injection) and have their
        # own cap (issue #559).
        cross_group_items: list[str] = []
        if task_group is not None:
            cross_reviews = self._query_cross_group_reviews(conn, spec_name, task_group, task_description)
            cross_group_items = cross_reviews[: self._config.max_cross_group_items]

        # Cross-spec drift items: drift findings from other specs that
        # reference the same files.  Informational only, not tracked.
        cross_spec_items: list[str] = []
        if task_group is not None and file_footprint:
            cross_spec = self._query_cross_spec_drift(conn, spec_name, file_footprint, task_description)
            cross_spec_items = cross_spec[: self._config.max_cross_spec_items]

        capped = items_with_ids[: self._config.max_items]
        result = [text for text, _ in capped] + cross_group_items + cross_spec_items

        # Session summary injection (119-REQ-2.1)
        same_spec_summaries = self._query_same_spec_summaries(conn, spec_name, task_group)
        result.extend(same_spec_summaries)

        logger.debug(
            "Retrieved %d review + %d drift + %d cross-group + %d cross-spec + %d context items for %s",
            len(reviews),
            len(drift),
            len(cross_group_items),
            len(cross_spec_items),
            len(same_spec_summaries),
            spec_name,
        )

        # Record which finding IDs were injected into this session so
        # that a successful ingest() can supersede them later (558-AC-1).
        # Cross-group items are NOT tracked — they are informational context.
        if session_id:
            injected_ids = [id_ for _, id_ in capped if id_ is not None]
            if injected_ids:
                try:
                    from agentfox.knowledge.review_store import record_finding_injections

                    record_finding_injections(conn, injected_ids, session_id)
                except Exception:
                    logger.warning(
                        "Failed to record injection log for session %s",
                        session_id,
                        exc_info=True,
                    )

        return result

    def ingest(
        self,
        session_id: str,
        spec_name: str,
        context: dict[str, Any],
    ) -> None:
        """Ingest knowledge from a completed session.

        On successful completion (``context['session_status'] == 'completed'``),
        supersedes all review findings that were previously injected into the
        session (recorded in the ``finding_injections`` table), preventing
        them from being re-injected into subsequent sessions for the same spec.

        Args:
            session_id: Node ID of the completed session.
            spec_name: Name of the spec the session belongs to.
            context: Dict with ``session_status``, ``touched_files``,
                ``commit_sha``, and ``project_root``.

        Requirements: 117-REQ-1.1, 117-REQ-7.4, 558-AC-2
        """
        session_status = context.get("session_status", "")

        # Acquire the DB connection once for finding supersession, drift
        # supersession, and summary storage.  If unavailable, log and bail.

        try:
            conn = self._knowledge_db.connection
        except KnowledgeStoreError:
            logger.warning(
                "Knowledge DB unavailable for ingestion in session %s",
                session_id,
            )
            return

        # Supersede injected findings when the session completed successfully
        # (558-AC-2).  A failed or incomplete session must NOT supersede findings
        # so retry sessions still see them (558-AC-3).
        if session_status == "completed":
            try:
                supersede_injected_findings(conn, session_id)
            except Exception:
                logger.warning(
                    "Failed to supersede injected findings for session %s",
                    session_id,
                    exc_info=True,
                )

            # 12-REQ-3.1, 12-REQ-3.2: File-based drift finding supersession
            # for coder sessions only.  Reviewer and verifier sessions must
            # not trigger drift finding supersession.
            archetype = context.get("archetype", "coder")
            if archetype == "coder":
                try:
                    supersede_drift_findings_by_files(
                        conn,
                        spec_name,
                        context.get("touched_files"),
                        session_id,
                    )
                except Exception:
                    logger.warning(
                        "Failed to supersede drift findings for session %s",
                        session_id,
                        exc_info=True,
                    )

                try:
                    from agentfox.knowledge.review_store import supersede_stale_pre_code_findings

                    supersede_stale_pre_code_findings(conn, spec_name, session_id)
                except Exception:
                    logger.warning(
                        "Failed to supersede stale pre-code findings for session %s",
                        session_id,
                        exc_info=True,
                    )

        # Session summary storage (119-REQ-5.2).
        # Only store for completed sessions with a non-empty summary.
        summary_text = context.get("summary")
        if session_status == "completed" and summary_text:
            self._store_summary(conn, session_id, spec_name, context)

    # ------------------------------------------------------------------
    # Internal query helpers
    # ------------------------------------------------------------------

    def _query_reviews(
        self,
        conn: Any,
        spec_name: str,
        task_group: str | None = None,
        task_description: str = "",
    ) -> tuple[list[str], list[str]]:
        """Query unresolved critical/major review findings for the spec.

        Returns a tuple of ``(formatted_strings, finding_ids)`` so that
        ``retrieve()`` can record which finding IDs were injected.

        Handles missing ``review_findings`` table gracefully by returning
        empty lists (116-REQ-6.E1).  Filters to ``critical`` and
        ``major`` severity only (116-REQ-6.1).

        When ``task_group`` is provided, only findings tagged for that group
        are returned, reducing noise for sessions focused on a specific
        task group.  When ``None``, all active findings for the spec are
        returned (backward-compatible behaviour).

        Findings are sorted by:
          1. Severity (critical before major — primary key, always preserved).
          2. Relevance score — keyword overlap with ``task_description``
             (higher overlap ranks first within a severity tier).
          3. Description (alphabetical — stable tiebreaker).

        When ``task_description`` is blank, relevance scores are all zero and
        the sort reduces to the existing severity/description order (AC-3).

        ``query_active_findings`` already excludes non-actionable severities;
        the ``if f.severity in (...)`` guard below is defense-in-depth and
        kept consistent with that filter (issue #553).
        """
        # Elevate pre-review (group 0) findings into primary review results
        # when the session targets a non-zero task group so they are tracked
        # via finding_injections and can be superseded (120-REQ-2.1, 120-REQ-2.2).
        include_prereview = task_group is not None and task_group != "0"

        def _do_query():
            from agentfox.knowledge.review_store import query_active_findings

            return query_active_findings(conn, spec_name, task_group=task_group, include_prereview=include_prereview)

        # Table may not exist in a fresh database (116-REQ-6.E1).
        findings = _query_safe(_do_query, (), label="review findings", spec_name=spec_name)

        keywords = _extract_keywords(task_description)
        actionable = [f for f in findings if f.severity in ("critical", "major")]
        actionable = sort_findings(actionable, keywords)

        result: list[str] = []
        ids: list[str] = []
        for f in actionable:
            result.append(f"[REVIEW] {format_finding_parts(f)}")
            ids.append(f.id)
        return result, ids

    def _query_cross_group_reviews(
        self,
        conn: Any,
        spec_name: str,
        task_group: str,
        task_description: str,
    ) -> list[str]:
        """Query active findings from *other* task groups in the same spec.

        Returns formatted strings with a ``[CROSS-GROUP]`` prefix that includes
        the source task group for context.  Uses the same relevance scoring as
        same-group retrieval so the most relevant cross-group findings surface
        first.

        These items are informational — they are NOT tracked in
        ``finding_injections`` and are not expected to be "fixed" by the
        current session.
        """
        # Exclude pre-review (group 0) findings from cross-group results
        # when the caller is not group 0 itself, since those findings are
        # elevated into primary review results (120-REQ-2.3, 120-REQ-2.E2).
        exclude_prereview = task_group != "0"

        def _do_query():
            from agentfox.knowledge.review_store import query_cross_group_findings

            return query_cross_group_findings(conn, spec_name, task_group, exclude_prereview=exclude_prereview)

        findings = _query_safe(_do_query, (), label="cross-group findings", spec_name=spec_name)

        keywords = _extract_keywords(task_description)
        actionable = [f for f in findings if f.severity in ("critical", "major")]
        actionable = sort_findings(actionable, keywords)

        result: list[str] = []
        for f in actionable:
            result.append(f"[CROSS-GROUP] (group {f.task_group}) {format_finding_parts(f)}")
        return result

    def _query_cross_spec_drift(
        self,
        conn: Any,
        spec_name: str,
        file_footprint: list[str],
        task_description: str,
    ) -> list[str]:
        """Query active drift findings from OTHER specs referencing overlapping files.

        Returns formatted strings with a ``[CROSS-SPEC]`` prefix that includes
        the source spec name.  Uses relevance scoring so the most relevant
        cross-spec findings surface first.

        These items are informational — they are NOT tracked in
        ``finding_injections``.
        """

        def _do_query():
            from agentfox.knowledge.review_store import query_cross_spec_drift_findings

            return query_cross_spec_drift_findings(conn, spec_name, file_footprint)

        findings = _query_safe(_do_query, (), label="cross-spec drift findings", spec_name=spec_name)

        keywords = _extract_keywords(task_description)
        actionable = [f for f in findings if f.severity in ("critical", "major")]
        actionable = sort_findings(actionable, keywords)

        from agentfox.knowledge.formatting import format_drift_finding_parts

        result: list[str] = []
        for f in actionable:
            result.append(f"[CROSS-SPEC] (spec: {f.spec_name}) {format_drift_finding_parts(f)}")
        return result

    def _query_drift(
        self,
        conn: Any,
        spec_name: str,
        task_group: str | None = None,
        task_description: str = "",
    ) -> tuple[list[str], list[str]]:
        """Query unresolved critical/major drift findings for the spec.

        Mirrors ``_query_reviews()`` but queries ``drift_findings`` via
        ``query_active_drift_findings()``.  Returns ``(formatted_strings,
        finding_ids)`` for injection tracking.
        """
        include_prereview = task_group is not None and task_group != "0"

        def _do_query():
            from agentfox.knowledge.review_store import query_active_drift_findings

            return query_active_drift_findings(
                conn,
                spec_name,
                task_group=task_group,
                include_prereview=include_prereview,
                max_age_days=self._config.max_drift_age_days,
            )

        findings = _query_safe(_do_query, (), label="drift findings", spec_name=spec_name)

        keywords = _extract_keywords(task_description)
        actionable = [f for f in findings if f.severity in ("critical", "major")]
        actionable = sort_findings(actionable, keywords)

        from agentfox.knowledge.formatting import format_drift_finding_parts

        result: list[str] = []
        ids: list[str] = []
        for f in actionable:
            result.append(f"[DRIFT] {format_drift_finding_parts(f)}")
            ids.append(f.id)
        return result, ids

    # ------------------------------------------------------------------
    # Session summary helpers (spec 119)
    # ------------------------------------------------------------------

    def _query_same_spec_summaries(
        self,
        conn: Any,
        spec_name: str,
        task_group: str | None,
    ) -> list[str]:
        """Query and format same-spec summaries as [CONTEXT] items.

        Requirements: 119-REQ-2.1, 119-REQ-2.2
        """
        if task_group is None:
            return []

        run_id = self._run_id
        if not run_id:
            return []

        def _do_query():
            from agentfox.knowledge.summary_store import query_same_spec_summaries

            return query_same_spec_summaries(
                conn,
                spec_name,
                task_group,
                run_id,
                max_items=self._config.max_summary_items,
            )

        records = _query_safe(_do_query, (), label="same-spec summaries", spec_name=spec_name)

        return [f"[CONTEXT] ({r.archetype}, group {r.task_group}, attempt {r.attempt}) {r.summary}" for r in records]

    def _store_summary(
        self,
        conn: Any,
        session_id: str,
        spec_name: str,
        context: dict[str, Any],
    ) -> None:
        """Store a session summary in the database.

        Extracts archetype, task_group, and attempt from the context dict
        and inserts a SummaryRecord.  Calls ``compose_enriched_summary``
        to merge structured fields (rejected_approaches, gotchas,
        assumptions) into the stored summary text.

        Requirements: 119-REQ-5.2, 119-REQ-5.E1, 11-REQ-3.5
        """
        import uuid

        try:
            from agentfox.engine.session_lifecycle import compose_enriched_summary
            from agentfox.knowledge.summary_store import SummaryRecord, insert_summary

            raw_summary = context.get("summary", "")
            archetype = context.get("archetype", "coder")
            task_group = str(context.get("task_group", "0"))
            attempt = int(context.get("attempt", 1))
            run_id = context.get("run_id", "") or (self._run_id or "")

            # 11-REQ-3.5: Compose enriched summary from structured fields.
            summary_text = compose_enriched_summary(
                summary=raw_summary,
                rejected_approaches=context.get("rejected_approaches"),
                gotchas=context.get("gotchas"),
                assumptions=context.get("assumptions"),
            )

            record = SummaryRecord(
                id=str(uuid.uuid4()),
                node_id=session_id,
                run_id=run_id,
                spec_name=spec_name,
                task_group=task_group,
                archetype=archetype,
                attempt=attempt,
                summary=summary_text,
                created_at=context.get("created_at", "")
                or __import__("datetime").datetime.now(__import__("datetime").timezone.utc).isoformat(),
            )
            insert_summary(conn, record)
            logger.info(
                "Stored session summary for %s (group %s, attempt %d)",
                session_id,
                task_group,
                attempt,
            )
        except Exception:
            logger.warning(
                "Failed to store session summary for %s",
                session_id,
                exc_info=True,
            )
