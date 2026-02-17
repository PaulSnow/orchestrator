"""Rich TUI dashboard for monitoring orchestrator runs."""

from __future__ import annotations

import time
from datetime import datetime, timezone
from pathlib import Path

from .config import RunConfig, load_config
from .issues import get_completed_count, get_failed_count, get_pending_count
from .state import StateManager


def _discover_all_configs(cfg: RunConfig) -> list[Path]:
    """Find all *-issues.json config files in the config directory."""
    config_dir = cfg.config_path.parent
    return sorted(config_dir.glob("*-issues.json"))


def _format_duration(seconds: float) -> str:
    """Format seconds as a human-readable duration."""
    if seconds < 60:
        return f"{int(seconds)}s"
    elif seconds < 3600:
        return f"{int(seconds // 60)}m"
    else:
        hours = int(seconds // 3600)
        mins = int((seconds % 3600) // 60)
        return f"{hours}h {mins}m"


def _get_log_last_line(state: StateManager, worker_id: int) -> str:
    """Get a summary of the last log activity."""
    path = state.log_path(worker_id)
    if not path.exists():
        return "--"
    try:
        text = path.read_text(errors="replace")
        lines = [l.strip() for l in text.splitlines() if l.strip()]
        if not lines:
            return "(empty)"
        last = lines[-1][:40]
        return last
    except OSError:
        return "(error)"


def run_dashboard(cfg: RunConfig, state: StateManager) -> None:
    """Run the live dashboard.

    Uses rich if available, falls back to plain text refresh.
    """
    try:
        from rich.console import Console  # noqa: F401
        _run_rich_dashboard(cfg, state)
    except ImportError:
        _run_plain_dashboard(cfg, state)


def _run_rich_dashboard(cfg: RunConfig, state: StateManager) -> None:
    """Run the dashboard with rich library."""
    from rich.console import Console
    from rich.live import Live

    console = Console()
    start_time = time.time()

    try:
        with Live(console=console, refresh_per_second=0.5, screen=True) as live:
            while True:
                # Reload config from disk to get fresh issue statuses
                from .config import load_config
                fresh_cfg = load_config(cfg.config_path)
                cfg.issues = fresh_cfg.issues

                table = _build_rich_display(cfg, state, start_time)
                live.update(table)
                time.sleep(2)
    except KeyboardInterrupt:
        pass


def _build_all_progress(cfg: RunConfig) -> list[str]:
    """Build progress bars for all configured projects."""
    lines = []
    bar_width = 30
    config_files = _discover_all_configs(cfg)

    for config_path in config_files:
        try:
            other_cfg = load_config(config_path)
        except (SystemExit, Exception):
            continue
        c = get_completed_count(other_cfg)
        f = get_failed_count(other_cfg)
        t = len(other_cfg.issues)
        if t == 0:
            continue
        pct = c / t
        filled = int(pct * bar_width)
        bar = "#" * filled + "-" * (bar_width - filled)
        label = other_cfg.project or config_path.stem
        fail_str = f"  ({f} failed)" if f > 0 else ""
        lines.append(f"  {label:<22} [{bar}] {c}/{t}{fail_str}")

    return lines


def _build_rich_display(cfg: RunConfig, state: StateManager,
                        start_time: float) -> "Panel":
    """Build the rich display panel."""
    from rich.table import Table
    from rich.panel import Panel
    from rich.text import Text
    from rich import box

    elapsed = time.time() - start_time
    completed = get_completed_count(cfg)
    total = len(cfg.issues)
    failed = get_failed_count(cfg)
    pending = get_pending_count(cfg)

    # Worker table
    worker_table = Table(box=box.SIMPLE_HEAVY, show_edge=False, pad_edge=False)
    worker_table.add_column("W#", style="bold", width=4)
    worker_table.add_column("Issue", width=8)
    worker_table.add_column("Stage", width=14)
    worker_table.add_column("Status", width=10)
    worker_table.add_column("Time", width=8)
    worker_table.add_column("Retries", width=8)
    worker_table.add_column("Log (last)", width=40, no_wrap=True)

    for i in range(1, cfg.num_workers + 1):
        worker = state.load_worker(i)
        if not worker:
            worker_table.add_row(str(i), "--", "--", "no state", "--", "--", "--")
            continue

        issue_str = f"#{worker.issue_number}" if worker.issue_number else "idle"
        stage_str = worker.stage if worker.stage else "--"

        # Color status
        status = worker.status
        if status == "running":
            status_text = Text(status, style="green")
        elif status == "idle":
            status_text = Text(status, style="dim")
        elif status == "failed":
            status_text = Text(status, style="red bold")
        elif status == "pending":
            status_text = Text(status, style="yellow")
        else:
            status_text = Text(status)

        # Calculate elapsed time for this worker
        time_str = "--"
        if worker.started_at:
            try:
                started = datetime.fromisoformat(worker.started_at.replace("Z", "+00:00"))
                worker_elapsed = (datetime.now(timezone.utc) - started).total_seconds()
                time_str = _format_duration(worker_elapsed)
            except (ValueError, TypeError):
                pass

        retries_str = str(worker.retry_count) if worker.retry_count > 0 else "--"
        log_last = _get_log_last_line(state, i)

        worker_table.add_row(
            str(i), issue_str, stage_str, status_text, time_str, retries_str, log_last,
        )

    # Build status lines
    pipeline_str = " -> ".join(cfg.pipeline) if len(cfg.pipeline) > 1 else cfg.pipeline[0] if cfg.pipeline else "implement"
    status_lines = [
        f"  Running for {_format_duration(elapsed)} | {completed}/{total} issues complete",
        f"  Pipeline: {pipeline_str}",
        "",
    ]

    # Find current wave
    in_progress_issues = [i for i in cfg.issues if i.status == "in_progress"]
    if in_progress_issues:
        waves = sorted(set(i.wave for i in in_progress_issues))
        wave_str = ", ".join(str(w) for w in waves)
        status_lines.append(f"  Active waves: {wave_str}")

    # Next blocked issue
    from .issues import next_available_issue
    completed_set = {i.number for i in cfg.issues if i.status == "completed"}
    in_progress_set = {i.number for i in cfg.issues if i.status == "in_progress"}
    next_issue = next_available_issue(cfg, completed_set, in_progress_set)
    if next_issue:
        status_lines.append(f"  Next available: #{next_issue.number} ({next_issue.title[:40]})")

    total_retries = 0
    for i in range(1, cfg.num_workers + 1):
        w = state.load_worker(i)
        if w:
            total_retries += w.retry_count
    status_lines.append(f"  Retries: {total_retries} | Failures: {failed}")
    status_lines.append("")

    # Progress bars for all configured projects
    progress_lines = _build_all_progress(cfg)
    status_lines.extend(progress_lines)

    # Combine into panel
    from rich.console import Group
    header = Text(f"  Orchestrator: {cfg.project}", style="bold cyan")
    status_text = Text("\n".join(status_lines))

    content = Group(header, Text(""), worker_table, Text(""), status_text)

    return Panel(
        content,
        title="Orchestrator Dashboard",
        border_style="blue",
    )


def _run_plain_dashboard(cfg: RunConfig, state: StateManager) -> None:
    """Fallback plain-text dashboard (no rich library)."""
    start_time = time.time()

    try:
        while True:
            # Clear screen
            print("\033[2J\033[H", end="")

            elapsed = time.time() - start_time
            completed = get_completed_count(cfg)
            total = len(cfg.issues)
            failed = get_failed_count(cfg)

            print(f"=== Orchestrator: {cfg.project} ===")
            print(f"Running for {_format_duration(elapsed)} | {completed}/{total} issues complete")
            print()

            print(f"{'W#':<4} {'Issue':<8} {'Stage':<15} {'Status':<10} {'Retries':<8} {'Log (last)'}")
            print("-" * 75)

            for i in range(1, cfg.num_workers + 1):
                worker = state.load_worker(i)
                if not worker:
                    print(f"{i:<4} {'--':<8} {'--':<15} {'no state':<10}")
                    continue

                issue_str = f"#{worker.issue_number}" if worker.issue_number else "idle"
                stage_str = worker.stage if worker.stage else "--"
                retries = str(worker.retry_count) if worker.retry_count > 0 else "--"
                log_last = _get_log_last_line(state, i)[:35]
                print(f"{i:<4} {issue_str:<8} {stage_str:<15} {worker.status:<10} {retries:<8} {log_last}")

            print()

            # Progress bars for all projects
            for line in _build_all_progress(cfg):
                print(line)

            print(f"\nFailures: {failed}")
            print("\nPress Ctrl-C to exit dashboard")

            # Reload config to get fresh status
            from .config import load_config
            fresh_cfg = load_config(cfg.config_path)
            cfg.issues = fresh_cfg.issues

            time.sleep(5)

    except KeyboardInterrupt:
        print("\nDashboard stopped.")
