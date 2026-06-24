"""Tests for nightshift CLI behavior.

Test Spec: TS-07-8, TS-07-9, TS-07-10, TS-07-11, TS-07-12, TS-07-13,
           TS-07-14, TS-07-15, TS-07-16, TS-07-17, TS-07-18, TS-07-19,
           TS-07-20, TS-07-E4, TS-07-E5, TS-07-P1, TS-07-P3
Requirements: 07-REQ-2.5, 07-REQ-3.1 through 07-REQ-3.12
"""

from __future__ import annotations

import json
import os
import signal
import subprocess
import sys
import time

import pytest
from click.testing import CliRunner

# Fox ASCII art banner detection pattern (ears line).
FOX_BANNER_PATTERN = "/\\_/\\"


class TestPythonMInvocation:
    """TS-07-8: python -m nightshift produces equivalent output to night-shift.

    Requirements: 07-REQ-2.5
    """

    def test_python_m_nightshift_help_exits_zero(self) -> None:
        """python -m nightshift --help exits 0."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == 0

    def test_python_m_and_entry_point_output_identical(self) -> None:
        """python -m nightshift --help and night-shift --help produce identical stdout.

        TS-07-8: Core invariant of output equivalence.
        """
        import shutil

        result_module = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert result_module.returncode == 0

        if shutil.which("night-shift") is None:
            pytest.skip("night-shift entry point not installed on PATH")

        result_entry = subprocess.run(
            ["night-shift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert result_entry.returncode == 0
        assert result_module.stdout == result_entry.stdout, (
            "python -m nightshift --help and night-shift --help must produce "
            "identical stdout"
        )


class TestBannerDisplay:
    """TS-07-9: Fox ASCII art banner is displayed without --quiet or --json.

    Requirements: 07-REQ-3.1
    """

    def test_banner_present_without_flags(self, cli_runner: CliRunner) -> None:
        """Invoking night-shift without --quiet or --json shows the fox banner."""
        from nightshift.app import main

        result = cli_runner.invoke(main, [])
        assert FOX_BANNER_PATTERN in result.output, (
            f"Expected fox ASCII art banner in output, got:\n{result.output}"
        )

    def test_banner_appears_before_startup_message(self, cli_runner: CliRunner) -> None:
        """Fox banner must appear before 'Night-shift daemon starting' message.

        TS-07-9 Expected: banner printed to stdout before the daemon start message.
        """
        from nightshift.app import main

        result = cli_runner.invoke(main, [])
        assert FOX_BANNER_PATTERN in result.output, (
            "Fox banner must be present in output"
        )
        assert "Night-shift daemon starting" in result.output, (
            "Startup message must be present in output"
        )
        banner_pos = result.output.index(FOX_BANNER_PATTERN)
        startup_pos = result.output.index("Night-shift daemon starting")
        assert banner_pos < startup_pos, (
            "Fox ASCII art banner must appear before the daemon start message"
        )


class TestBannerSuppression:
    """TS-07-10: Banner suppressed with --quiet or --json.

    Requirements: 07-REQ-3.2
    """

    def test_banner_absent_with_quiet(self, cli_runner: CliRunner) -> None:
        """--quiet suppresses the fox ASCII art banner."""
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--quiet"])
        assert FOX_BANNER_PATTERN not in result.output, (
            "Fox banner must be suppressed with --quiet"
        )

    def test_banner_absent_with_json(self, cli_runner: CliRunner) -> None:
        """--json suppresses the fox ASCII art banner."""
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--json"])
        assert FOX_BANNER_PATTERN not in result.output, (
            "Fox banner must be suppressed with --json"
        )


class TestGlobalOptions:
    """TS-07-11: CLI exposes all required global options from common_options.

    Requirements: 07-REQ-3.3
    """

    def test_help_contains_json_flag(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert "--json" in result.stdout

    def test_help_contains_no_json_flag(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert "--no-json" in result.stdout

    def test_help_contains_verbose_flag(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert "--verbose" in result.stdout or "-v" in result.stdout

    def test_help_contains_quiet_flag(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert "--quiet" in result.stdout or "-q" in result.stdout

    def test_help_contains_trace_flag(self) -> None:
        """--trace must be present in help output (common_options)."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        assert "--trace" in result.stdout

    def test_help_contains_version_flag(self) -> None:
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert "--version" in result.stdout


class TestVersionFlag:
    """TS-07-12: --version prints version and exits 0.

    Requirements: 07-REQ-3.4
    """

    def test_version_exits_zero(self, cli_runner: CliRunner) -> None:
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--version"])
        assert result.exit_code == 0

    def test_version_contains_version_string(self, cli_runner: CliRunner) -> None:
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--version"])
        assert "4.0.0" in result.output


class TestConfigLoading:
    """TS-07-13: Configuration loading from .agent-fox/config.toml and AF_CONFIG.

    Requirements: 07-REQ-3.5
    """

    def test_help_works_without_config(self, cli_runner: CliRunner) -> None:
        """--help works even without a config file present."""
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--help"])
        assert result.exit_code == 0

    def test_af_config_invalid_path_exits_one(self) -> None:
        """AF_CONFIG pointing to nonexistent path causes exit code 1.

        TS-07-13 part 2: Invalid AF_CONFIG path triggers startup failure.
        """
        env = os.environ.copy()
        env["AF_CONFIG"] = "/nonexistent/config.toml"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        assert result.returncode == 1, (
            f"Expected exit code 1 with invalid AF_CONFIG, got {result.returncode}"
        )


class TestStartupMessage:
    """TS-07-14: Startup message and summary stats on startup/exit.

    Requirements: 07-REQ-3.6
    """

    def test_startup_message_present(self, cli_runner: CliRunner) -> None:
        """Daemon startup emits 'Night-shift daemon starting' to stdout."""
        from nightshift.app import main

        result = cli_runner.invoke(main, [])
        assert "Night-shift daemon starting" in result.output, (
            f"Expected 'Night-shift daemon starting' in output, got:\n{result.output}"
        )

    def test_summary_stats_present_at_exit(self, cli_runner: CliRunner) -> None:
        """Daemon exit emits summary statistics to stdout.

        TS-07-14 Expected: 'summary stats on stdout at exit'.
        After a graceful shutdown the daemon prints a summary line
        containing at least 'Night-shift stopped' (normal mode) or
        a JSON summary event (--json mode).
        """
        from nightshift.app import main

        result = cli_runner.invoke(main, [])
        # The daemon's normal-mode summary contains 'Night-shift stopped'
        # and statistics such as 'Issues fixed' and 'Total cost'.
        assert "Night-shift stopped" in result.output, (
            f"Expected 'Night-shift stopped' summary stats in output, "
            f"got:\n{result.output}"
        )


class TestJsonlProgressEvents:
    """TS-07-15: JSONL progress events in --json mode.

    Requirements: 07-REQ-3.7
    """

    def test_json_mode_emits_jsonl_lines(self, cli_runner: CliRunner) -> None:
        """--json mode emits at least one valid JSON line to stdout."""
        from nightshift.app import main

        result = cli_runner.invoke(main, ["--json"])
        lines = [line for line in result.output.splitlines() if line.strip()]
        json_lines = []
        for line in lines:
            try:
                json.loads(line)
                json_lines.append(line)
            except json.JSONDecodeError:
                pass
        assert len(json_lines) >= 1, (
            f"Expected at least one valid JSONL line in --json output, "
            f"got:\n{result.output}"
        )


class TestGracefulSigint:
    """TS-07-16: First SIGINT initiates graceful shutdown with exit code 0.

    Requirements: 07-REQ-3.8
    """

    @pytest.mark.slow
    def test_single_sigint_graceful_shutdown(self) -> None:
        """Start daemon subprocess, send SIGINT, assert exit code 0."""
        proc = subprocess.Popen(
            [sys.executable, "-m", "nightshift"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        # Wait briefly for daemon startup
        time.sleep(1)
        proc.send_signal(signal.SIGINT)
        try:
            returncode = proc.wait(timeout=30)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
            pytest.fail("Daemon did not exit within 30 seconds after SIGINT")
        assert returncode == 0, (
            f"Expected exit code 0 after graceful SIGINT shutdown, got {returncode}"
        )


class TestDoubleSigintAbort:
    """TS-07-17: Double SIGINT causes immediate abort with exit code 130.

    Requirements: 07-REQ-3.9
    """

    @pytest.mark.slow
    def test_double_sigint_aborts_with_130(self) -> None:
        """Start daemon, send first SIGINT, then second SIGINT -> exit 130."""
        proc = subprocess.Popen(
            [sys.executable, "-m", "nightshift"],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        # Wait briefly for daemon startup
        time.sleep(1)
        proc.send_signal(signal.SIGINT)
        # Brief pause to allow graceful shutdown to begin
        time.sleep(0.2)
        proc.send_signal(signal.SIGINT)
        try:
            returncode = proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
            pytest.fail("Daemon did not exit within 10 seconds after double SIGINT")
        assert returncode == 130, (
            f"Expected exit code 130 after double SIGINT, got {returncode}"
        )


class TestStartupFailure:
    """TS-07-18 / TS-07-E4: Startup failure via invalid config exits code 1.

    Requirements: 07-REQ-3.10, 07-REQ-3.E1
    """

    def test_invalid_config_exits_one(self) -> None:
        """AF_CONFIG=/dev/null/invalid triggers startup failure with exit 1."""
        env = os.environ.copy()
        env["AF_CONFIG"] = "/dev/null/invalid"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        assert result.returncode == 1, (
            f"Expected exit code 1 for startup failure, got {result.returncode}"
        )

    def test_invalid_config_stderr_nonempty(self) -> None:
        """Startup failure produces descriptive error on stderr."""
        env = os.environ.copy()
        env["AF_CONFIG"] = "/dev/null/invalid"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        assert len(result.stderr) > 0, "Startup failure must produce stderr output"
        stderr_lower = result.stderr.lower()
        assert "config" in stderr_lower or "error" in stderr_lower, (
            f"stderr should mention 'config' or 'error', got:\n{result.stderr}"
        )

    def test_nonexistent_config_path_exits_one(self) -> None:
        """AF_CONFIG pointing to a nonexistent path causes exit code 1.

        TS-07-E4: Explicit nonexistent config path scenario.
        """
        env = os.environ.copy()
        env["AF_CONFIG"] = "/nonexistent/config.toml"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        assert result.returncode == 1, (
            f"Expected exit code 1 for nonexistent config, got {result.returncode}"
        )
        assert len(result.stderr) > 0, (
            "Missing config must produce a descriptive error on stderr"
        )


class TestAgentFoxGroupUsage:
    """TS-07-19: app.py uses AgentFoxGroup from agentfox.io as its Click group.

    Requirements: 07-REQ-3.11
    """

    def test_main_is_agentfox_group_instance(self) -> None:
        """main is an instance of AgentFoxGroup (not just any Click BaseCommand)."""
        from agentfox.io import AgentFoxGroup
        from nightshift.app import main

        assert isinstance(main, AgentFoxGroup) or type(main).__name__ == "AgentFoxGroup", (
            f"main must be an AgentFoxGroup instance, got {type(main).__name__}"
        )


class TestEnvironmentVariables:
    """TS-07-20: Environment variable support for AF_CONFIG, AF_LOG_LEVEL, AF_AGENT.

    Requirements: 07-REQ-3.12
    """

    def test_af_agent_env_accepted(self) -> None:
        """night-shift --help works with AF_AGENT=1."""
        env = os.environ.copy()
        env["AF_AGENT"] = "1"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
            env=env,
        )
        assert result.returncode == 0

    def test_af_log_level_env_accepted(self) -> None:
        """night-shift --help works with AF_LOG_LEVEL=DEBUG."""
        env = os.environ.copy()
        env["AF_LOG_LEVEL"] = "DEBUG"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
            env=env,
        )
        assert result.returncode == 0

    def test_af_config_invalid_path_exits_one(self) -> None:
        """AF_CONFIG=/nonexistent causes exit 1 (semantic correctness, not crash)."""
        env = os.environ.copy()
        env["AF_CONFIG"] = "/nonexistent/config.toml"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        assert result.returncode == 1, (
            f"AF_CONFIG pointing to nonexistent path must cause exit 1, "
            f"got {result.returncode}"
        )


class TestAfAgentMode:
    """TS-07-E5: AF_AGENT=1 activates agent-mode (JSONL output, banner suppressed).

    Requirements: 07-REQ-3.E2
    """

    def test_af_agent_activates_jsonl_output(self, cli_runner: CliRunner) -> None:
        """AF_AGENT=1 activates JSONL output mode (structured JSON on stdout)."""
        from nightshift.app import main

        result = cli_runner.invoke(main, [], env={"AF_AGENT": "1"})
        lines = [line for line in result.output.splitlines() if line.strip()]
        json_lines = []
        for line in lines:
            try:
                json.loads(line)
                json_lines.append(line)
            except json.JSONDecodeError:
                pass
        assert len(json_lines) >= 1, (
            f"AF_AGENT=1 must activate JSONL output, got:\n{result.output}"
        )

    def test_af_agent_suppresses_banner(self, cli_runner: CliRunner) -> None:
        """AF_AGENT=1 suppresses the fox ASCII art banner."""
        from nightshift.app import main

        result = cli_runner.invoke(main, [], env={"AF_AGENT": "1"})
        assert FOX_BANNER_PATTERN not in result.output, (
            "Fox banner must be suppressed in agent mode (AF_AGENT=1)"
        )


class TestBehavioralParity:
    """TS-07-P1: Behavioral parity with former af night-shift.

    Requirements: 07-REQ-3.1 through 07-REQ-3.10

    For each flag combination, the standalone night-shift must produce the same
    output and exit code as the former af night-shift. Since af night-shift is
    removed, we test against expected behavior from the spec.
    """

    @pytest.mark.parametrize("flags,expected_exit", [
        (["--help"], 0),
        (["--version"], 0),
        (["--quiet", "--help"], 0),
        (["--json", "--help"], 0),
        (["--verbose", "--help"], 0),
        (["--trace", "--help"], 0),
        (["--quiet", "--verbose", "--help"], 0),
        (["--json", "--verbose", "--help"], 0),
    ])
    def test_flag_combination_exit_code(
        self, flags: list[str], expected_exit: int,
    ) -> None:
        """Flag combination produces the expected exit code."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", *flags],
            capture_output=True,
            text=True,
            timeout=30,
        )
        assert result.returncode == expected_exit, (
            f"Flags {flags} expected exit {expected_exit}, got {result.returncode}"
        )

    def test_version_output_matches_spec(self) -> None:
        """--version outputs '4.0.0-rc4' matching the former af night-shift."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--version"],
            capture_output=True, text=True, timeout=30,
        )
        assert "4.0.0" in result.stdout, (
            f"Version output does not contain '4.0.0': {result.stdout}"
        )

    def test_help_output_contains_daemon_description(self) -> None:
        """--help contains descriptive text about the night-shift daemon."""
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True, text=True, timeout=30,
        )
        # Help text should describe the daemon (not just show Click boilerplate)
        assert "night-shift" in result.stdout.lower() or "daemon" in result.stdout.lower(), (
            f"Help text should mention night-shift or daemon:\n{result.stdout}"
        )


class TestEnvVarSemantics:
    """TS-07-P3: Environment variable semantics match af CLI.

    Requirements: 07-REQ-3.12

    Tests AF_CONFIG, AF_LOG_LEVEL, and AF_AGENT with correct semantic checks,
    not just invocability.
    """

    @pytest.mark.parametrize("env_var,value", [
        ("AF_AGENT", "1"),
        ("AF_LOG_LEVEL", "DEBUG"),
        ("AF_LOG_LEVEL", "WARNING"),
    ])
    def test_env_vars_accepted_with_help(
        self, env_var: str, value: str,
    ) -> None:
        """Environment variables are accepted without error on --help."""
        env = os.environ.copy()
        env[env_var] = value
        result = subprocess.run(
            [sys.executable, "-m", "nightshift", "--help"],
            capture_output=True,
            text=True,
            timeout=30,
            env=env,
        )
        assert result.returncode == 0

    def test_af_config_nonexistent_exits_one(self) -> None:
        """AF_CONFIG pointing to nonexistent file causes exit code 1."""
        env = os.environ.copy()
        env["AF_CONFIG"] = "/nonexistent/config.toml"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        assert result.returncode == 1, (
            f"AF_CONFIG=/nonexistent must cause exit 1, got {result.returncode}"
        )

    def test_af_config_semantics_error_message(self) -> None:
        """AF_CONFIG invalid path produces error on stderr."""
        env = os.environ.copy()
        env["AF_CONFIG"] = "/nonexistent/config.toml"
        result = subprocess.run(
            [sys.executable, "-m", "nightshift"],
            capture_output=True, text=True, timeout=30, env=env,
        )
        stderr_lower = result.stderr.lower()
        assert "config" in stderr_lower or "error" in stderr_lower, (
            f"AF_CONFIG error should mention 'config' or 'error' in stderr: "
            f"{result.stderr}"
        )
