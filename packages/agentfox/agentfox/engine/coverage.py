"""Test coverage measurement."""

from __future__ import annotations

import json
import logging
import re
import shutil
import subprocess
import tomllib
from dataclasses import dataclass
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)


_COVERAGE_TIMEOUT = 600


@dataclass(frozen=True)
class CoverageTool:
    name: str
    command: list[str]
    result_path: str


@dataclass(frozen=True)
class FileCoverage:
    file_path: str
    covered_lines: int
    total_lines: int

    @property
    def percentage(self) -> float:
        if self.total_lines == 0:
            return 100.0
        return (self.covered_lines / self.total_lines) * 100.0


@dataclass(frozen=True)
class CoverageResult:
    files: dict[str, FileCoverage]

    def coverage_for(self, path: str) -> FileCoverage | None:
        return self.files.get(path)

    def to_json(self) -> str:
        return json.dumps(
            {
                path: {
                    "covered_lines": fc.covered_lines,
                    "total_lines": fc.total_lines,
                }
                for path, fc in self.files.items()
            }
        )

    @staticmethod
    def from_json(raw: str) -> CoverageResult | None:
        if not raw:
            return None
        try:
            data = json.loads(raw)
        except (json.JSONDecodeError, TypeError):
            return None
        files: dict[str, FileCoverage] = {}
        for path, info in data.items():
            files[path] = FileCoverage(
                file_path=path,
                covered_lines=info.get("covered_lines", 0),
                total_lines=info.get("total_lines", 0),
            )
        return CoverageResult(files=files)


@dataclass(frozen=True)
class CoverageRegression:
    file_path: str
    baseline_pct: float
    current_pct: float
    delta: float


def _has_pytest_cov_dependency(data: dict[str, Any]) -> bool:
    """Check if pytest-cov is declared anywhere in pyproject.toml dependencies."""
    sources: list[Any] = []
    project = data.get("project", {})
    sources.append(project.get("dependencies", []))
    sources.append(project.get("optional-dependencies", {}).values())
    for group_deps in data.get("dependency-groups", {}).values():
        sources.append(group_deps)
    for src in sources:
        if isinstance(src, list):
            items = src
        else:
            items = [item for sub in src for item in (sub if isinstance(sub, list) else [sub])]
        for dep in items:
            if isinstance(dep, str) and dep.lower().startswith("pytest-cov"):
                return True
    return False


def detect_coverage_tool(project_root: Path) -> CoverageTool | None:
    pyproject = project_root / "pyproject.toml"
    if pyproject.exists():
        try:
            data = tomllib.loads(pyproject.read_text(encoding="utf-8"))
            tool = data.get("tool", {})
            if "pytest" in tool and _has_pytest_cov_dependency(data):
                return CoverageTool(
                    name="pytest-cov",
                    command=[
                        "uv",
                        "run",
                        "pytest",
                        "--cov",
                        "--cov-report=json",
                        "-q",
                        "--no-header",
                        "-x",
                    ],
                    result_path="coverage.json",
                )
        except (tomllib.TOMLDecodeError, OSError):
            pass

    cargo = project_root / "Cargo.toml"
    if cargo.exists() and shutil.which("cargo-tarpaulin") is not None:
        try:
            data = tomllib.loads(cargo.read_text(encoding="utf-8"))
            if "package" in data:
                return CoverageTool(
                    name="cargo-tarpaulin",
                    command=["cargo", "tarpaulin", "--out", "json", "--output-dir", "."],
                    result_path="tarpaulin-report.json",
                )
        except (tomllib.TOMLDecodeError, OSError):
            pass

    go_mod = project_root / "go.mod"
    if go_mod.exists():
        return CoverageTool(
            name="go-cover",
            command=["go", "test", "-coverprofile=coverage.out", "-covermode=count", "./..."],
            result_path="coverage.out",
        )

    package_json = project_root / "package.json"
    if package_json.exists():
        try:
            pkg = json.loads(package_json.read_text(encoding="utf-8"))
            tool = _detect_js_coverage_tool(pkg)
            if tool is not None:
                return tool
        except (json.JSONDecodeError, OSError):
            pass

    return None


def _js_has_dep(pkg: dict[str, Any], name: str) -> bool:
    """Check if a JS package is in dependencies or devDependencies."""
    return name in pkg.get("dependencies", {}) or name in pkg.get("devDependencies", {})


def _detect_js_coverage_tool(pkg: dict[str, Any]) -> CoverageTool | None:
    if _js_has_dep(pkg, "vitest") and (
        _js_has_dep(pkg, "@vitest/coverage-v8") or _js_has_dep(pkg, "@vitest/coverage-istanbul")
    ):
        return CoverageTool(
            name="vitest",
            command=["npx", "vitest", "run", "--coverage.enabled", "--coverage.reporter=json"],
            result_path="coverage/coverage-final.json",
        )
    if _js_has_dep(pkg, "jest"):
        return CoverageTool(
            name="jest",
            command=["npx", "jest", "--coverage", "--coverageReporters=json", "--silent"],
            result_path="coverage/coverage-final.json",
        )
    return None


def measure_coverage(project_root: Path, tool: CoverageTool) -> CoverageResult | None:
    try:
        subprocess.run(
            tool.command,
            cwd=project_root,
            capture_output=True,
            text=True,
            timeout=_COVERAGE_TIMEOUT,
        )
    except subprocess.TimeoutExpired:
        logger.warning("Coverage measurement timed out after %ds", _COVERAGE_TIMEOUT)
        return None
    except Exception:
        logger.debug("Coverage measurement failed", exc_info=True)
        return None

    result_path = project_root / tool.result_path
    if not result_path.exists():
        logger.debug("Coverage result file not found: %s", result_path)
        return None

    try:
        if tool.name == "pytest-cov":
            return _parse_pytest_cov(result_path)
        if tool.name == "cargo-tarpaulin":
            return _parse_tarpaulin(result_path)
        if tool.name == "go-cover":
            return _parse_go_cover(result_path)
        if tool.name in ("vitest", "jest"):
            return _parse_istanbul(result_path)
    except Exception:
        logger.debug("Failed to parse coverage output from %s", tool.name, exc_info=True)
    finally:
        try:
            result_path.unlink(missing_ok=True)
        except Exception:
            pass

    return None


def _parse_pytest_cov(result_path: Path) -> CoverageResult:
    data = json.loads(result_path.read_text())
    files: dict[str, FileCoverage] = {}
    for file_path, info in data.get("files", {}).items():
        rel_path = file_path
        if Path(file_path).is_absolute():
            try:
                rel_path = str(Path(file_path).relative_to(result_path.parent))
            except ValueError:
                pass
        summary = info.get("summary", {})
        files[rel_path] = FileCoverage(
            file_path=rel_path,
            covered_lines=summary.get("covered_lines", 0),
            total_lines=summary.get("num_statements", 0),
        )
    return CoverageResult(files=files)


def _parse_tarpaulin(result_path: Path) -> CoverageResult:
    data = json.loads(result_path.read_text())
    files: dict[str, FileCoverage] = {}
    for entry in data.get("files", []):
        path = entry.get("path", "")
        traces = entry.get("traces", [])
        total = len(traces)
        covered = sum(1 for t in traces if t.get("stats", {}).get("Line", 0) > 0)
        files[path] = FileCoverage(
            file_path=path,
            covered_lines=covered,
            total_lines=total,
        )
    return CoverageResult(files=files)


def _parse_go_cover(result_path: Path) -> CoverageResult:
    content = result_path.read_text()
    file_stats: dict[str, tuple[int, int]] = {}
    for line in content.splitlines():
        if line.startswith("mode:"):
            continue
        match = re.match(r"^(.+?):(\d+)\.\d+,(\d+)\.\d+\s+(\d+)\s+(\d+)$", line)
        if match:
            path = match.group(1)
            num_statements = int(match.group(4))
            count = int(match.group(5))
            prev_c, prev_t = file_stats.get(path, (0, 0))
            file_stats[path] = (
                prev_c + (num_statements if count > 0 else 0),
                prev_t + num_statements,
            )
    files = {path: FileCoverage(file_path=path, covered_lines=c, total_lines=t) for path, (c, t) in file_stats.items()}
    return CoverageResult(files=files)


def _parse_istanbul(result_path: Path) -> CoverageResult:
    """Parse Istanbul/NYC coverage-final.json (used by Jest and Vitest)."""
    data = json.loads(result_path.read_text())
    files: dict[str, FileCoverage] = {}
    for file_path, info in data.items():
        rel_path = file_path
        if Path(file_path).is_absolute():
            try:
                rel_path = str(Path(file_path).relative_to(result_path.parent.parent))
            except ValueError:
                pass
        stmts = info.get("s", {})
        total = len(stmts)
        covered = sum(1 for count in stmts.values() if count > 0)
        files[rel_path] = FileCoverage(
            file_path=rel_path,
            covered_lines=covered,
            total_lines=total,
        )
    return CoverageResult(files=files)


def find_regressions(
    baseline: CoverageResult,
    current: CoverageResult,
    modified_files: list[str],
) -> list[CoverageRegression]:
    regressions: list[CoverageRegression] = []
    for file_path in modified_files:
        base = baseline.coverage_for(file_path)
        curr = current.coverage_for(file_path)
        if base is None or curr is None:
            continue
        if base.total_lines == 0:
            continue
        delta = curr.percentage - base.percentage
        if delta < 0:
            regressions.append(
                CoverageRegression(
                    file_path=file_path,
                    baseline_pct=base.percentage,
                    current_pct=curr.percentage,
                    delta=delta,
                )
            )
    return regressions
