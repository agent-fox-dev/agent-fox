"""Central schema definitions for agent-produced JSON artifacts.

Provides Pydantic models that serve as the single source of truth for
the shape of structured artifacts agents write during sessions (e.g.
``session-summary.json``).  Both prompt templates and consumer code
import from here so the contract cannot silently diverge.
"""
