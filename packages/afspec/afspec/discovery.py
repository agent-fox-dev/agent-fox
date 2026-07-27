"""Spec root discovery and dependency graph construction.

Scans a spec root directory for spec folders matching the
``{NN}_{snake_case_name}`` pattern, loads lightweight metadata from
``prd.md`` frontmatter, and constructs a directed dependency graph
from ``tasks.json`` declarations.
"""

from __future__ import annotations

import json
import re
from collections import defaultdict, deque
from pathlib import Path
from typing import Any, Union

import yaml

from afspec.exceptions import LoadError, SpecError
from afspec.models import DependencyEdge, PRDFrontmatter, Requirements, SpecMeta

# Pattern for spec folder names: one or more digits, underscore, then
# a lowercase letter followed by lowercase letters/digits/underscores.
_SPEC_DIR_RE = re.compile(r"^\d+_[a-z][a-z0-9_]*$")

# Regex for extracting YAML frontmatter from prd.md (same as io.py).
_FRONTMATTER_RE = re.compile(r"\A---\r?\n(.*?)^---\r?\n", re.DOTALL | re.MULTILINE)


def is_spec_dir_name(name: str) -> bool:
    """Check whether *name* matches the canonical spec directory pattern.

    The pattern is ``{digits}_{snake_case_name}`` — one or more leading
    digits, an underscore, then a lowercase letter followed by lowercase
    letters, digits, or underscores.

    This is the single source of truth for spec-directory name matching.
    All packages should call this function (or :func:`parse_spec_dir_name`)
    instead of defining their own regex.
    """
    return bool(_SPEC_DIR_RE.match(name))


def parse_spec_dir_name(name: str) -> tuple[int, str] | None:
    """Parse a spec directory name into ``(prefix, spec_name)``.

    Returns ``None`` if *name* does not match the canonical spec directory
    pattern (see :func:`is_spec_dir_name`).

    >>> parse_spec_dir_name("01_core_foundation")
    (1, 'core_foundation')
    >>> parse_spec_dir_name("invalid") is None
    True
    """
    if not _SPEC_DIR_RE.match(name):
        return None
    prefix_str, _, rest = name.partition("_")
    return int(prefix_str), rest


class DependencyGraph:
    """Directed dependency graph across specs.

    Edges go from upstream (depended-on) spec to downstream (dependent)
    spec.  An edge ``DependencyEdge(from_spec="A", to_spec="B")`` means
    "B depends on A".
    """

    def __init__(self, edge_list: list[DependencyEdge], all_spec_ids: list[str]) -> None:
        self._edges = list(edge_list)
        self._all_spec_ids = list(all_spec_ids)

    def edges(self) -> list[DependencyEdge]:
        """Return all edges in the graph."""
        return list(self._edges)

    def dependencies(self, spec_id: str) -> list[DependencyEdge]:
        """Return direct dependencies of *spec_id*.

        Returns edges where ``to_spec == spec_id`` — i.e. the specs that
        *spec_id* depends on (its upstream dependencies).
        """
        return [e for e in self._edges if e.to_spec == spec_id]

    def dependents(self, spec_id: str) -> list[DependencyEdge]:
        """Return direct dependents of *spec_id*.

        Returns edges where ``from_spec == spec_id`` — i.e. the specs
        that depend on *spec_id* (its downstream dependents).
        """
        return [e for e in self._edges if e.from_spec == spec_id]

    def topological_sort(self) -> list[str]:
        """Return a topological ordering of spec IDs.

        Specs with no dependencies appear first.  Uses Kahn's algorithm.
        """
        # Build adjacency list and in-degree map for all known specs.
        in_degree: dict[str, int] = {sid: 0 for sid in self._all_spec_ids}
        adjacency: dict[str, list[str]] = {sid: [] for sid in self._all_spec_ids}

        for edge in self._edges:
            adjacency[edge.from_spec].append(edge.to_spec)
            in_degree[edge.to_spec] += 1

        # Seed with zero in-degree nodes, sorted for determinism.
        queue: deque[str] = deque(sorted(sid for sid, deg in in_degree.items() if deg == 0))
        result: list[str] = []

        while queue:
            node = queue.popleft()
            result.append(node)
            for neighbor in sorted(adjacency[node]):
                in_degree[neighbor] -= 1
                if in_degree[neighbor] == 0:
                    queue.append(neighbor)

        # If result doesn't contain all nodes, there is a cycle — but
        # build_dependency_graph already checks for cycles before
        # constructing the graph, so this is a defensive measure.
        if len(result) != len(self._all_spec_ids):
            raise SpecError("Dependency cycle detected during topological sort")

        return result


def _load_frontmatter_only(prd_path: Path) -> PRDFrontmatter:
    """Parse only the YAML frontmatter from a prd.md file.

    This avoids fully loading all four artifacts — only the frontmatter
    fields are needed for ``SpecMeta`` population.
    """
    text = prd_path.read_text(encoding="utf-8")
    m = _FRONTMATTER_RE.match(text)
    if m is None:
        raise LoadError(f"{prd_path}: missing or invalid YAML frontmatter")

    yaml_text = m.group(1)
    try:
        data = yaml.safe_load(yaml_text)
    except yaml.YAMLError as exc:
        raise LoadError(f"{prd_path}: invalid YAML: {exc}") from exc

    if not isinstance(data, dict):
        raise LoadError(f"{prd_path}: frontmatter is not a mapping")

    try:
        return PRDFrontmatter.model_validate(data)
    except Exception as exc:
        raise LoadError(f"{prd_path}: invalid frontmatter fields: {exc}") from exc


def discover_specs(root: Union[str, Path]) -> list[SpecMeta]:
    """Discover spec folders in a root directory.

    Scans *root* for subdirectories matching the ``{NN}_{snake_case_name}``
    naming pattern.  Skips the ``archive/`` subdirectory.  Returns a list
    of ``SpecMeta`` values populated from each spec's ``prd.md``
    frontmatter.

    Raises ``SpecError`` if *root* does not exist.
    """
    root_path = Path(root)
    if not root_path.is_dir():
        raise SpecError(f"Root directory does not exist: {root_path}")

    metas: list[SpecMeta] = []
    try:
        entries = sorted(root_path.iterdir())
    except OSError as exc:
        raise SpecError(f"Cannot read root directory {root_path}: {exc}") from exc

    for entry in entries:
        if not entry.is_dir():
            continue
        name = entry.name
        # Skip the archive directory and non-matching names.
        if name == "archive":
            continue
        if not _SPEC_DIR_RE.match(name):
            continue

        # Must have a prd.md to extract metadata.
        prd_path = entry / "prd.md"
        if not prd_path.is_file():
            continue

        fm = _load_frontmatter_only(prd_path)
        metas.append(
            SpecMeta(
                spec_id=fm.spec_id,
                spec_name=fm.spec_name,
                status=fm.status,
                dir=str(entry),
            )
        )

    return metas


def build_dependency_graph(metas: list[SpecMeta], root: Union[str, Path]) -> DependencyGraph:
    """Build a dependency graph from discovered specs.

    Reads ``tasks.json`` from each spec folder referenced in *metas*,
    extracts ``dependencies`` declarations, validates that every
    ``depends_on_spec`` references a known spec, checks for cycles, and
    returns a ``DependencyGraph``.

    Raises ``SpecError`` if a referenced spec is unknown or if the
    dependency graph contains a cycle.
    """
    known_ids = {m.spec_id for m in metas}

    edges: list[DependencyEdge] = []

    for meta in metas:
        tasks_path = Path(meta.dir) / "tasks.json"
        if not tasks_path.is_file():
            continue

        try:
            tasks_data = json.loads(tasks_path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError) as exc:
            raise SpecError(f"Failed to read tasks.json for spec {meta.spec_id}: {exc}") from exc

        deps = tasks_data.get("dependencies", [])
        for dep in deps:
            dep_spec = dep.get("depends_on_spec", "")
            if dep_spec not in known_ids:
                raise SpecError(f"Spec {meta.spec_id} references unknown dependency spec '{dep_spec}'")
            edges.append(
                DependencyEdge(
                    from_spec=dep_spec,
                    to_spec=meta.spec_id,
                    from_group=dep.get("from_group", 0),
                    to_group=dep.get("to_group", 0),
                    relationship=dep.get("relationship", ""),
                )
            )

    all_spec_ids = sorted(known_ids)

    # Cycle detection via DFS.
    _check_cycles(all_spec_ids, edges)

    return DependencyGraph(edges, all_spec_ids)


def _check_cycles(spec_ids: list[str], edges: list[DependencyEdge]) -> None:
    """Detect cycles in the dependency graph using DFS.

    Raises ``SpecError`` if a cycle is found, naming the involved specs.
    """
    adjacency: dict[str, list[str]] = defaultdict(list)
    for edge in edges:
        adjacency[edge.from_spec].append(edge.to_spec)

    WHITE, GRAY, BLACK = 0, 1, 2
    color: dict[str, int] = {sid: WHITE for sid in spec_ids}
    parent: dict[str, str | None] = {sid: None for sid in spec_ids}

    def dfs(node: str) -> list[str] | None:
        color[node] = GRAY
        for neighbor in adjacency.get(node, []):
            if color[neighbor] == GRAY:
                # Found a cycle — reconstruct it.
                cycle = [neighbor, node]
                return cycle
            if color[neighbor] == WHITE:
                parent[neighbor] = node
                result = dfs(neighbor)
                if result is not None:
                    return result
        color[node] = BLACK
        return None

    for sid in spec_ids:
        if color[sid] == WHITE:
            cycle = dfs(sid)
            if cycle is not None:
                cycle_str = " -> ".join(cycle)
                raise SpecError(f"Dependency cycle detected: {cycle_str}")


def load_dependent_interfaces(
    spec_id: str,
    spec_root: Path,
) -> list[dict[str, Any]]:
    """Load interface summaries from upstream dependency specs.

    Discovers all specs under *spec_root*, builds the dependency graph,
    and for each spec that *spec_id* depends on, extracts the
    introduction, glossary, external APIs, and interface symbols from
    the upstream ``requirements.json``.

    Returns a list of dicts, one per upstream spec, each containing:
    ``spec_id``, ``spec_name``, ``introduction``, ``glossary``,
    ``external_apis``, and ``interface_symbols``.

    Returns ``[]`` on any failure (graceful degradation).
    """
    try:
        metas = discover_specs(spec_root)
        graph = build_dependency_graph(metas, spec_root)
        upstream_edges = graph.dependencies(spec_id)

        # Deduplicate upstream spec_ids
        seen_upstream: set[str] = set()
        unique_upstream: list[str] = []
        for edge in upstream_edges:
            if edge.from_spec not in seen_upstream:
                seen_upstream.add(edge.from_spec)
                unique_upstream.append(edge.from_spec)

        # Build a lookup from spec_id to SpecMeta
        meta_by_id = {m.spec_id: m for m in metas}

        results: list[dict[str, Any]] = []
        for upstream_id in unique_upstream:
            meta = meta_by_id.get(upstream_id)
            if meta is None:
                continue

            req_path = Path(meta.dir) / "requirements.json"
            if not req_path.is_file():
                continue

            try:
                reqs = Requirements.model_validate_json(req_path.read_text(encoding="utf-8"))
            except Exception:
                continue

            # Extract interface symbols from criteria
            interface_symbols: list[dict[str, Any]] = []
            for req in reqs.requirements:
                for criterion in req.acceptance_criteria + req.edge_cases:
                    interface_symbols.append(
                        {
                            "criterion_id": criterion.id,
                            "action": criterion.action,
                            "return_contract": criterion.return_contract or "",
                        }
                    )

            results.append(
                {
                    "spec_id": upstream_id,
                    "spec_name": reqs.spec_name,
                    "introduction": reqs.introduction,
                    "glossary": dict(reqs.glossary),
                    "external_apis": [api.model_dump() for api in reqs.external_apis],
                    "interface_symbols": interface_symbols,
                }
            )

        return results
    except Exception:
        return []


# ---------------------------------------------------------------------------
# Intent extraction
# ---------------------------------------------------------------------------

_INTENT_HEADING_RE = re.compile(r"^## Intent\s*$", re.MULTILINE)
_NEXT_HEADING_RE = re.compile(r"^## ", re.MULTILINE)


def _extract_intent(prd_path: Path) -> str:
    """Extract a concise intent string from a spec's ``prd.md``.

    Looks for a ``## Intent`` section and returns its text content up to
    the next ``##`` heading or end of file, truncated to 300 characters.

    Falls back to the first non-empty paragraph of the body when no
    ``## Intent`` section exists.  Returns ``""`` if the file is missing,
    unreadable, or contains no extractable text.
    """
    try:
        text = prd_path.read_text(encoding="utf-8")
    except Exception:
        return ""

    # Strip YAML frontmatter
    fm_match = _FRONTMATTER_RE.match(text)
    body = text[fm_match.end() :] if fm_match else text

    # Try to find ## Intent section
    intent_match = _INTENT_HEADING_RE.search(body)
    if intent_match:
        after_heading = body[intent_match.end() :]
        next_heading = _NEXT_HEADING_RE.search(after_heading)
        if next_heading:
            section_text = after_heading[: next_heading.start()]
        else:
            section_text = after_heading
        intent = section_text.strip()
        return intent[:300]

    # Fallback: first non-empty paragraph
    paragraphs = body.split("\n\n")
    for para in paragraphs:
        stripped = para.strip()
        # Skip headings and empty paragraphs
        if stripped and not stripped.startswith("#"):
            return stripped[:300]

    return ""


# ---------------------------------------------------------------------------
# Spec landscape loading
# ---------------------------------------------------------------------------


def load_spec_landscape(
    spec_root: Union[str, Path],
    *,
    include_archive: bool = True,
    current_spec_id: str | None = None,
) -> list[dict[str, Any]]:
    """Collect metadata for all specs (active and optionally archived).

    Returns a ``list[dict[str, Any]]`` containing one entry per discovered
    spec, excluding any spec whose ``spec_id`` matches *current_spec_id*.

    Active entries contain keys: ``spec_id``, ``spec_name``, ``title``,
    ``status``, ``intent``, ``archived`` (``False``).

    Archived entries contain keys: ``spec_id``, ``spec_name``, ``title``,
    ``status``, ``archived`` (``True``).  No ``intent`` key is included.

    Returns ``[]`` on any failure (graceful degradation), matching the
    pattern used by :func:`load_dependent_interfaces`.
    """
    try:
        spec_root = Path(spec_root)
        results: list[dict[str, Any]] = []

        # ---- Active specs ------------------------------------------------
        active_metas = discover_specs(spec_root)
        for meta in active_metas:
            if current_spec_id is not None and meta.spec_id == current_spec_id:
                continue

            prd_path = Path(meta.dir) / "prd.md"

            # Retrieve title from frontmatter (SpecMeta lacks title field)
            title = ""
            try:
                fm = _load_frontmatter_only(prd_path)
                title = fm.title
            except Exception:
                pass

            intent = _extract_intent(prd_path)

            glossary_terms: list[str] = []
            try:
                req_path = Path(meta.dir) / "requirements.json"
                if req_path.exists():
                    req_data = json.loads(req_path.read_text(encoding="utf-8"))
                    glossary_terms = list(req_data.get("glossary", {}).keys())
            except Exception:
                pass

            results.append(
                {
                    "spec_id": meta.spec_id,
                    "spec_name": meta.spec_name,
                    "title": title,
                    "status": meta.status.value if hasattr(meta.status, "value") else str(meta.status),
                    "intent": intent,
                    "glossary_terms": glossary_terms,
                    "archived": False,
                }
            )

        # ---- Archived specs ----------------------------------------------
        if include_archive:
            archive_dir = spec_root / "archive"
            if archive_dir.is_dir():
                try:
                    entries = sorted(archive_dir.iterdir())
                except OSError:
                    entries = []

                for entry in entries:
                    if not entry.is_dir():
                        continue
                    if not _SPEC_DIR_RE.match(entry.name):
                        continue

                    prd_path = entry / "prd.md"
                    try:
                        fm = _load_frontmatter_only(prd_path)
                    except Exception:
                        continue

                    if current_spec_id is not None and fm.spec_id == current_spec_id:
                        continue

                    results.append(
                        {
                            "spec_id": fm.spec_id,
                            "spec_name": fm.spec_name,
                            "title": fm.title,
                            "status": fm.status.value if hasattr(fm.status, "value") else str(fm.status),
                            "archived": True,
                        }
                    )

        return results
    except Exception:
        return []
