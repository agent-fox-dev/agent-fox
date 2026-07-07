"""Daemon startup helpers — knowledge store, migrations, progress bridge."""

from __future__ import annotations

import logging

logger = logging.getLogger(__name__)


def init_knowledge(config, project_root):
    """Open knowledge store, run migrations. Returns (db, sink, provider)."""
    kdb = sink = kprov = None
    try:
        from afaudit.sink import SinkDispatcher
        from agentfox.knowledge.db import open_knowledge_store
        from agentfox.knowledge.duckdb_sink import DuckDBSink
        from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

        kdb = open_knowledge_store(config.knowledge, read_only=False)
        sink = SinkDispatcher([DuckDBSink(kdb.connection)])
        kprov = FoxKnowledgeProvider(kdb, config.knowledge.provider)
    except Exception:
        logger.warning("Failed to open knowledge store", exc_info=True)
        return None, None, None
    # Run legacy migrations at startup via the canonical helper.
    from agentfox.core.config import resolve_spec_root
    from agentfox.engine.run import _run_startup_migrations

    specs = resolve_spec_root(config, project_root)
    try:
        _run_startup_migrations(kdb, specs, project_root)
    except Exception:
        logger.warning("Startup migrations failed", exc_info=True)
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
