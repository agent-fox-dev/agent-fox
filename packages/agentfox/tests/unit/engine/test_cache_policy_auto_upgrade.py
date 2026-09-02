"""Cache policy auto-upgrade tests for multi-session orchestrator runs.

Verify the orchestrator auto-selects EXTENDED cache policy when running
multi-session runs (>3 nodes or parallel > 1) without explicit user
configuration, and preserves user-configured policies.

Tests call ``_init_run()`` directly (not ``run()``) to avoid subprocess
health checks that are tested separately in dispatch tests.

Test Spec: TS-NS-1 through TS-NS-5
Requirements: NS-REQ-1 through NS-REQ-5 (issue #743)
"""

from __future__ import annotations

import logging

import pytest
from agentfox.core.config import AgentFoxConfig, CachePolicy, OrchestratorConfig
from agentfox.engine.engine import Orchestrator

from .conftest import MockSessionRunner, write_plan_to_db


def _make_orchestrator(
    db_conn,
    full_config: AgentFoxConfig,
    parallel: int = 1,
) -> Orchestrator:
    """Build an Orchestrator wired to the given DB and config."""
    mock = MockSessionRunner()
    config = OrchestratorConfig(parallel=parallel, inter_session_delay=0, sync_interval=0, hot_load=False)
    return Orchestrator(
        config=config,
        session_runner_factory=lambda nid, **kw: mock,
        knowledge_db_conn=db_conn,
        full_config=full_config,
    )


class TestCachePolicyAutoUpgradeMultiSession:
    """TS-NS-1: EXTENDED auto-selected for multi-session runs (>3 nodes).

    When no explicit cache policy is configured and the task graph has
    more than 3 nodes, the orchestrator should auto-upgrade to EXTENDED.
    """

    def test_auto_upgrade_to_extended_with_4_nodes(self) -> None:
        """AC-1: >3 nodes + no explicit config -> EXTENDED selected."""
        db_conn = write_plan_to_db(
            nodes={
                "spec:1": {"title": "Task A"},
                "spec:2": {"title": "Task B"},
                "spec:3": {"title": "Task C"},
                "spec:4": {"title": "Task D"},
            },
            edges=[
                {"source": "spec:1", "target": "spec:2", "kind": "intra_spec"},
                {"source": "spec:2", "target": "spec:3", "kind": "intra_spec"},
                {"source": "spec:3", "target": "spec:4", "kind": "intra_spec"},
            ],
            order=["spec:1", "spec:2", "spec:3", "spec:4"],
        )

        full_config = AgentFoxConfig()
        assert full_config.caching.cache_policy == CachePolicy.DEFAULT
        assert not full_config._caching_explicit

        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.EXTENDED

    def test_auto_upgrade_with_5_nodes(self) -> None:
        """More than 4 nodes also triggers upgrade."""
        db_conn = write_plan_to_db(
            nodes={f"spec:{i}": {"title": f"Task {i}"} for i in range(1, 6)},
            edges=[],
        )

        full_config = AgentFoxConfig()
        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.EXTENDED


class TestCachePolicyExplicitOverridePreserved:
    """TS-NS-2: Explicit user config is never overridden.

    When a user explicitly configures a cache policy (even DEFAULT or NONE),
    the auto-selection logic must not change it.
    """

    def test_explicit_none_preserved(self) -> None:
        """AC-2: Explicit NONE is preserved even with >3 nodes."""
        db_conn = write_plan_to_db(
            nodes={f"spec:{i}": {"title": f"Task {i}"} for i in range(1, 5)},
            edges=[],
        )

        full_config = AgentFoxConfig(caching={"cache_policy": "NONE"})
        full_config._caching_explicit = True

        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.NONE

    def test_explicit_default_preserved(self) -> None:
        """AC-2: Explicit DEFAULT is preserved even with >3 nodes."""
        db_conn = write_plan_to_db(
            nodes={f"spec:{i}": {"title": f"Task {i}"} for i in range(1, 5)},
            edges=[],
        )

        full_config = AgentFoxConfig(caching={"cache_policy": "DEFAULT"})
        full_config._caching_explicit = True

        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.DEFAULT

    def test_explicit_extended_preserved(self) -> None:
        """AC-2: Explicit EXTENDED is preserved (no double upgrade)."""
        db_conn = write_plan_to_db(
            nodes={f"spec:{i}": {"title": f"Task {i}"} for i in range(1, 5)},
            edges=[],
        )

        full_config = AgentFoxConfig(caching={"cache_policy": "EXTENDED"})
        full_config._caching_explicit = True

        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.EXTENDED


class TestCachePolicySingleSessionDefault:
    """TS-NS-3: Single-session runs retain DEFAULT.

    With <=3 nodes and parallel=1, the cache policy should remain DEFAULT
    when no explicit config is set.
    """

    def test_single_node_retains_default(self) -> None:
        """AC-3: 1 node + no explicit config -> DEFAULT retained."""
        db_conn = write_plan_to_db(
            nodes={"spec:1": {"title": "Task A"}},
            edges=[],
        )

        full_config = AgentFoxConfig()
        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.DEFAULT

    def test_three_nodes_retains_default(self) -> None:
        """AC-3: 3 nodes (boundary) + no explicit config -> DEFAULT retained."""
        db_conn = write_plan_to_db(
            nodes={
                "spec:1": {"title": "Task A"},
                "spec:2": {"title": "Task B"},
                "spec:3": {"title": "Task C"},
            },
            edges=[
                {"source": "spec:1", "target": "spec:2", "kind": "intra_spec"},
                {"source": "spec:2", "target": "spec:3", "kind": "intra_spec"},
            ],
            order=["spec:1", "spec:2", "spec:3"],
        )

        full_config = AgentFoxConfig()
        orchestrator = _make_orchestrator(db_conn, full_config)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.DEFAULT


class TestCachePolicyAutoUpgradeLogging:
    """TS-NS-4: Auto-selection is logged at INFO level.

    When the auto-upgrade activates, an INFO-level log message must
    mention the cache policy upgrade.
    """

    def test_info_log_emitted_on_upgrade(self, caplog: pytest.LogCaptureFixture) -> None:
        """AC-4: INFO log emitted mentioning EXTENDED and cache."""
        db_conn = write_plan_to_db(
            nodes={f"spec:{i}": {"title": f"Task {i}"} for i in range(1, 5)},
            edges=[],
        )

        full_config = AgentFoxConfig()
        orchestrator = _make_orchestrator(db_conn, full_config)

        with caplog.at_level(logging.INFO, logger="agentfox.engine.engine"):
            orchestrator._init_run()

        cache_logs = [r for r in caplog.records if "extended" in r.message.lower() and "cache" in r.message.lower()]
        assert len(cache_logs) >= 1, (
            f"Expected INFO log about EXTENDED cache policy, got: {[r.message for r in caplog.records]}"
        )
        assert cache_logs[0].levelno == logging.INFO

    def test_no_log_when_not_upgraded(self, caplog: pytest.LogCaptureFixture) -> None:
        """No cache upgrade log when conditions are not met."""
        db_conn = write_plan_to_db(
            nodes={"spec:1": {"title": "Task A"}},
            edges=[],
        )

        full_config = AgentFoxConfig()
        orchestrator = _make_orchestrator(db_conn, full_config)

        with caplog.at_level(logging.INFO, logger="agentfox.engine.engine"):
            orchestrator._init_run()

        cache_logs = [r for r in caplog.records if "extended" in r.message.lower() and "cache" in r.message.lower()]
        assert len(cache_logs) == 0


class TestCachePolicyParallelAutoUpgrade:
    """TS-NS-5: Parallel runs (parallel > 1) trigger auto-upgrade.

    Even with <=3 nodes, parallel > 1 indicates a multi-session run
    and should trigger the EXTENDED cache policy upgrade.
    """

    def test_parallel_2_with_2_nodes_upgrades(self) -> None:
        """AC-5: parallel=2, 2 nodes, no explicit config -> EXTENDED."""
        db_conn = write_plan_to_db(
            nodes={
                "spec:1": {"title": "Task A"},
                "spec:2": {"title": "Task B"},
            },
            edges=[],
        )

        full_config = AgentFoxConfig()
        orchestrator = _make_orchestrator(db_conn, full_config, parallel=2)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.EXTENDED

    def test_parallel_with_explicit_config_preserved(self) -> None:
        """parallel > 1 with explicit config still preserved."""
        db_conn = write_plan_to_db(
            nodes={
                "spec:1": {"title": "Task A"},
                "spec:2": {"title": "Task B"},
            },
            edges=[],
        )

        full_config = AgentFoxConfig(caching={"cache_policy": "NONE"})
        full_config._caching_explicit = True

        orchestrator = _make_orchestrator(db_conn, full_config, parallel=2)
        orchestrator._init_run()

        assert full_config.caching.cache_policy == CachePolicy.NONE
