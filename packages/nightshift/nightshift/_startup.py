"""Daemon startup helpers — knowledge store, migrations, progress bridge."""

from __future__ import annotations

import logging

logger = logging.getLogger(__name__)


def init_knowledge(config, project_root):
    """Open knowledge store, run migrations. Returns (db, sink, provider)."""
    kdb = sink = kprov = None
    try:
        from agentfox.knowledge.db import open_knowledge_store
        from agentfox.knowledge.duckdb_sink import DuckDBSink
        from agentfox.knowledge.fox_provider import FoxKnowledgeProvider
        from agentfox.knowledge.sink import SinkDispatcher

        kdb = open_knowledge_store(config.knowledge, read_only=False)
        sink = SinkDispatcher([DuckDBSink(kdb.connection)])
        kprov = FoxKnowledgeProvider(kdb, config.knowledge.provider)
    except Exception:
        logger.warning("Failed to open knowledge store", exc_info=True)
        return None, None, None
    # Run legacy migrations and errata indexing at startup.
    from agentfox.core.config import resolve_spec_root
    from agentfox.knowledge.errata import index_errata_from_markdown
    from agentfox.session.context import _migrate_legacy_files

    specs = resolve_spec_root(config, project_root)
    if specs.is_dir():
        for d in sorted(specs.iterdir()):
            if d.is_dir():
                try:
                    _migrate_legacy_files(kdb.connection, d, d.name)
                except Exception:
                    logger.warning("Migration failed for %s", d.name, exc_info=True)
    try:
        index_errata_from_markdown(kdb.connection, project_root)
    except Exception:
        logger.warning("Failed to index errata", exc_info=True)
    return kdb, sink, kprov


def wrap_task_callback(progress, om):
    """Bridge UI task events to JSONL when ``om.json_mode`` is active."""
    if not om.json_mode:
        return progress.task_callback
    from agentfox.io.progress import ProgressDisplay as JsonlProgress

    jl = JsonlProgress(output_manager=om, json_mode=True)
    ui_cb = progress.task_callback

    def _cb(event):
        ui_cb(event)
        nid = getattr(event, "node_id", None)
        status = getattr(event, "status", "")
        if status == "completed":
            jl.task_started(node_id=nid)
            jl.task_completed(node_id=nid)
        elif status == "failed":
            jl.task_failed(node_id=nid, error=getattr(event, "error_message", "") or "")
        else:
            jl.task_started(node_id=nid)

    return _cb
