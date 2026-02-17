"""CLI entry point: python -m orchestrator <command>"""

from __future__ import annotations

import argparse
import shutil
import sys
import time
from pathlib import Path

from .config import (
    NUM_WORKERS,
    default_config_dir,
    default_config_path,
    load_all_configs,
    load_config,
    validate_config,
)
from .state import StateManager, now_iso


def _resolve_configs(args: argparse.Namespace) -> list["RunConfig"]:
    """Resolve config(s) from CLI args.

    If --config-dir is given, load all configs from that directory.
    If --config is given, load a single config.
    """
    config_dir = getattr(args, "config_dir", None)
    config_file = getattr(args, "config", None)

    if config_dir:
        return load_all_configs(config_dir)
    elif config_file:
        return [load_config(config_file)]
    else:
        return load_all_configs(str(default_config_dir()))


def cmd_launch(args: argparse.Namespace) -> None:
    """Launch unified parallel workers in a tmux session."""
    from . import git, tmux
    from .issues import next_available_issue_global
    from .prompt import generate_prompt

    configs = _resolve_configs(args)
    num_workers = args.workers or NUM_WORKERS
    tmux_session = args.session or "orchestrator"
    dry_run = args.dry_run
    stagger_delay = max(c.stagger_delay for c in configs)

    # Validate all configs
    all_errors = []
    for cfg in configs:
        errors = validate_config(cfg)
        if errors:
            all_errors.extend(f"[{cfg.project}] {e}" for e in errors)
    if all_errors:
        print("Config validation errors:", file=sys.stderr)
        for e in all_errors:
            print(f"  - {e}", file=sys.stderr)
        sys.exit(1)

    # Use first config's state dir for shared worker state
    primary_cfg = configs[0]
    primary_cfg.num_workers = num_workers
    primary_cfg.tmux_session = tmux_session
    state = StateManager(primary_cfg)

    print("+" + "=" * 58 + "+")
    print("|  Unified Orchestrator — Multi-Project                    |")
    print("+" + "=" * 58 + "+")
    print()

    if dry_run:
        print("*** DRY RUN MODE -- no changes will be made ***")
        print()

    # Check prerequisites
    print("Checking prerequisites...")
    missing = []
    for cmd in ["tmux", "claude"]:
        if not shutil.which(cmd):
            missing.append(cmd)
    # Check for platform-specific tools across all configs
    platforms = set()
    for cfg in configs:
        for repo in cfg.repos.values():
            platforms.add(repo.platform)
    if "gitlab" in platforms and not shutil.which("glab"):
        missing.append("glab")
    if "github" in platforms and not shutil.which("gh"):
        missing.append("gh")
    if missing:
        print(f"ERROR: Missing required commands: {', '.join(missing)}", file=sys.stderr)
        sys.exit(1)
    print("  Prerequisites OK")

    # Show config info
    for cfg in configs:
        total = len(cfg.issues)
        pending = sum(1 for i in cfg.issues if i.status in ("pending", "in_progress"))
        completed = sum(1 for i in cfg.issues if i.status == "completed")
        failed = sum(1 for i in cfg.issues if i.status == "failed")
        print(f"  {cfg.project}: {total} issues ({completed} done, {pending} pending, {failed} failed)")
        print(f"    Pipeline: {' -> '.join(cfg.pipeline)}")
    print(f"  Workers: {num_workers}")
    print(f"  Session: {tmux_session}")
    print()

    # Fetch origin for all repos
    print("-- Fetching origin --")
    for cfg in configs:
        for name, repo_cfg in cfg.repos.items():
            if not dry_run:
                success = git.fetch(repo_cfg.path)
                status = "OK" if success else "FAILED"
            else:
                status = "(skipped)"
            print(f"  [{cfg.project}] {name}: {status}")
    print()

    # Assign initial work from global priority queue
    print("-- Initial assignments from global priority queue --")
    claimed: set[tuple[str, int]] = set()
    assignments: list[tuple[int, "RunConfig", "Issue"]] = []

    for worker_id in range(1, num_workers + 1):
        result = next_available_issue_global(configs, claimed)
        if result:
            issue_cfg, issue = result
            claimed.add((str(issue_cfg.config_path), issue.number))
            assignments.append((worker_id, issue_cfg, issue))
            print(f"  Worker {worker_id}: #{issue.number} ({issue.title[:40]}) [{issue_cfg.project}]")
        else:
            print(f"  Worker {worker_id}: (no issues available)")

    if not assignments:
        # Check if there are failed issues the monitor can retry
        from .issues import next_retriable_issue_global
        has_retriable = next_retriable_issue_global(configs) is not None
        if not has_retriable:
            print("No pending or retriable issues. Exiting.", file=sys.stderr)
            sys.exit(0)
        print("  (monitor will assign failed issues for retry)")
    print()

    # Create worktrees
    print("-- Creating worktrees --")
    for worker_id, issue_cfg, issue in assignments:
        repo_cfg = issue_cfg.repo_for_issue(issue)
        branch = f"{repo_cfg.branch_prefix}{issue.number}"
        wt_path = f"{repo_cfg.worktree_base}/issue-{issue.number}"

        if Path(wt_path).is_dir():
            print(f"  Worker {worker_id}: worktree exists: {wt_path} (reusing)")
        else:
            print(f"  Worker {worker_id}: creating {wt_path} branch={branch}")
            if not dry_run:
                success = git.create_worktree(
                    repo_cfg.path, wt_path, branch,
                    base_branch=f"origin/{repo_cfg.default_branch}",
                )
                if not success:
                    print(f"    WARNING: failed to create worktree", file=sys.stderr)
    print()

    # Initialize state
    print("-- Initializing state --")
    if not dry_run:
        state.ensure_dirs()
        # Initialize idle workers (no assignment yet — monitor handles retry)
        for wid in range(1, num_workers + 1):
            if not any(a[0] == wid for a in assignments):
                from .models import Worker
                idle_worker = Worker(worker_id=wid, status="idle")
                state.save_worker(idle_worker)
    for worker_id, issue_cfg, issue in assignments:
        repo_cfg = issue_cfg.repo_for_issue(issue)
        branch = f"{repo_cfg.branch_prefix}{issue.number}"
        wt_path = f"{repo_cfg.worktree_base}/issue-{issue.number}"

        print(f"  worker-{worker_id} -> #{issue.number} [{issue_cfg.project}]")
        if not dry_run:
            state.init_worker(worker_id, issue.number, branch, wt_path)
            # Track source config for cross-project
            worker = state.load_worker(worker_id)
            if worker:
                worker.source_config = str(issue_cfg.config_path)
                state.save_worker(worker)
    print()

    # Create tmux session
    print("-- Creating tmux session --")
    if not dry_run and tmux.session_exists(tmux_session):
        print(f"ERROR: tmux session '{tmux_session}' already exists.", file=sys.stderr)
        print(f"  Kill it first: tmux kill-session -t {tmux_session}", file=sys.stderr)
        sys.exit(1)

    if not dry_run:
        tmux.create_session(tmux_session, "orchestrator", str(primary_cfg.orch_root))
        for worker_id, _, _ in assignments:
            tmux.new_window(tmux_session, f"worker-{worker_id}", str(primary_cfg.orch_root))
        # Create remaining worker windows (idle workers)
        for worker_id in range(len(assignments) + 1, num_workers + 1):
            tmux.new_window(tmux_session, f"worker-{worker_id}", str(primary_cfg.orch_root))
        tmux.new_window(tmux_session, "dashboard", str(primary_cfg.orch_root))
    print(f"  {num_workers + 2} windows created (orchestrator, worker-1..{num_workers}, dashboard)")
    print()

    # Launch workers
    if assignments:
        print(f"-- Launching workers ({stagger_delay}s stagger) --")
    else:
        print("-- Workers idle, monitor will assign retry work --")
    for idx, (worker_id, issue_cfg, issue) in enumerate(assignments):
        repo_cfg = issue_cfg.repo_for_issue(issue)
        wt_path = f"{repo_cfg.worktree_base}/issue-{issue.number}"
        log_file = state.log_path(worker_id)
        signal_file = state.signal_path(worker_id)
        prompt_path = state.prompt_path(worker_id)

        print(f"  Worker {worker_id}: #{issue.number} [{issue_cfg.project}] -> {log_file}")

        if not dry_run:
            signal_file.unlink(missing_ok=True)

            stage_name = issue_cfg.pipeline[issue.pipeline_stage] if issue_cfg.pipeline else "implement"
            issue_state = StateManager(issue_cfg)
            prompt = generate_prompt(
                stage_name, issue, worker_id, wt_path, repo_cfg, issue_cfg, issue_state,
            )
            prompt_path.write_text(prompt)

            # Update worker state
            worker = state.load_worker(worker_id)
            if worker:
                worker.status = "running"
                worker.started_at = now_iso()
                worker.stage = stage_name
                state.save_worker(worker)

            # Update issue status in the owning config
            issue_state.update_issue_status(issue.number, "in_progress", assigned_worker=worker_id)

            # Launch in tmux
            from .monitor import _build_claude_cmd
            tmux.send_command(
                tmux_session,
                f"worker-{worker_id}",
                _build_claude_cmd(wt_path, str(prompt_path), str(log_file),
                                  str(signal_file), worker_id, issue.number,
                                  stage_name),
            )

        # Stagger launches
        if idx < len(assignments) - 1:
            print(f"  Waiting {stagger_delay}s before next launch...")
            if not dry_run:
                time.sleep(stagger_delay)
    print()

    # Build config-dir arg for monitor/dashboard
    config_dir_arg = str(configs[0].config_path.parent)

    # Launch monitor
    print("-- Starting monitor --")
    if not dry_run:
        pkg_dir = Path(__file__).resolve().parent.parent
        tmux.send_command(
            tmux_session,
            "orchestrator",
            f'cd {pkg_dir} && python3 -m orchestrator monitor '
            f'--config-dir {config_dir_arg} --session {tmux_session} '
            f'2>&1 | tee /tmp/orchestrator-monitor.log',
        )
    print("  Monitor loop started in window 0")
    print()

    # Launch dashboard
    print("-- Starting dashboard --")
    if not dry_run:
        pkg_dir = Path(__file__).resolve().parent.parent
        tmux.send_command(
            tmux_session,
            "dashboard",
            f'cd {pkg_dir} && python3 -m orchestrator dashboard '
            f'--config-dir {config_dir_arg}',
        )
    print("  Dashboard started in dashboard window")
    print()

    print("=" * 60)
    print(f"  All workers launched.")
    print(f"  Attach: tmux attach -t {tmux_session}")
    print(f"  Dashboard: Ctrl-b then select 'dashboard' window")
    print("=" * 60)


def cmd_monitor(args: argparse.Namespace) -> None:
    """Run the monitor loop."""
    config_dir = getattr(args, "config_dir", None)

    if config_dir:
        from .monitor import run_monitor_loop_global

        configs = load_all_configs(config_dir)
        tmux_session = args.session or "orchestrator"
        num_workers = args.workers or NUM_WORKERS

        # Use first config for shared state
        primary_cfg = configs[0]
        primary_cfg.num_workers = num_workers
        primary_cfg.tmux_session = tmux_session
        state = StateManager(primary_cfg)

        if args.cycle:
            for cfg in configs:
                cfg.cycle_interval = args.cycle

        run_monitor_loop_global(
            configs, state, num_workers, tmux_session,
            no_delay=args.no_delay,
        )
    else:
        from .monitor import run_monitor_loop

        cfg = load_config(args.config)
        if args.cycle:
            cfg.cycle_interval = args.cycle
        state = StateManager(cfg)
        run_monitor_loop(cfg, state, no_delay=args.no_delay)


def cmd_cleanup(args: argparse.Namespace) -> None:
    """Clean up tmux session and worktrees."""
    from .cleanup import run_cleanup

    cfg = load_config(args.config)
    run_cleanup(cfg, keep_worktrees=args.keep_worktrees)


def cmd_status(args: argparse.Namespace) -> None:
    """Display one-shot status."""
    from .issues import get_completed_count, get_failed_count, get_pending_count

    configs = _resolve_configs(args)
    num_workers = getattr(args, "workers", None) or NUM_WORKERS

    # Use first config for shared state
    primary_cfg = configs[0]
    primary_cfg.num_workers = num_workers
    state = StateManager(primary_cfg)

    print("=" * 60)
    print("  Unified Orchestrator Status")
    print("=" * 60)
    print()

    # Per-project summary
    total_all = 0
    completed_all = 0
    pending_all = 0
    failed_all = 0

    for cfg in configs:
        total = len(cfg.issues)
        completed = get_completed_count(cfg)
        pending = get_pending_count(cfg)
        failed = get_failed_count(cfg)
        total_all += total
        completed_all += completed
        pending_all += pending
        failed_all += failed
        print(f"  {cfg.project}: {completed}/{total} completed, {pending} pending, {failed} failed")
    print()
    print(f"  TOTAL: {completed_all}/{total_all} completed, {pending_all} pending, {failed_all} failed")
    print(f"  Workers: {num_workers}")
    print()

    # Worker summary
    print(f"{'Worker':<8} {'Issue':<8} {'Project':<20} {'Status':<10} {'Retries':<8} {'Stage'}")
    print("-" * 70)
    for i in range(1, num_workers + 1):
        worker = state.load_worker(i)
        if worker:
            issue_str = f"#{worker.issue_number}" if worker.issue_number else "--"
            # Determine project from source_config
            project = "--"
            if worker.source_config:
                try:
                    src_cfg = load_config(worker.source_config)
                    project = src_cfg.project
                except (SystemExit, Exception):
                    pass
            stage_str = worker.stage or "--"
            print(f"  {i:<6} {issue_str:<8} {project:<20} {worker.status:<10} {worker.retry_count:<8} {stage_str}")
        else:
            print(f"  {i:<6} {'--':<8} {'--':<20} {'no state':<10}")
    print()

    # Pending issues across all projects
    for cfg in configs:
        pending_issues = [i for i in cfg.issues if i.status == "pending"]
        if pending_issues:
            print(f"Pending [{cfg.project}]:")
            for issue in sorted(pending_issues, key=lambda i: (i.wave, i.priority)):
                deps = f" (depends on: {issue.depends_on})" if issue.depends_on else ""
                print(f"  #{issue.number}: {issue.title} [wave {issue.wave}]{deps}")

    # Failed issues across all projects
    for cfg in configs:
        failed_issues = [i for i in cfg.issues if i.status == "failed"]
        if failed_issues:
            print(f"Failed [{cfg.project}]:")
            for issue in sorted(failed_issues, key=lambda i: (i.wave, i.priority)):
                print(f"  #{issue.number}: {issue.title}")


def cmd_dashboard(args: argparse.Namespace) -> None:
    """Run the live dashboard."""
    from .dashboard import run_dashboard

    configs = _resolve_configs(args)
    # Use first config for state/dashboard — dashboard already discovers all configs
    primary_cfg = configs[0]
    primary_cfg.num_workers = getattr(args, "workers", None) or NUM_WORKERS
    state = StateManager(primary_cfg)
    run_dashboard(primary_cfg, state)


def cmd_add_issue(args: argparse.Namespace) -> None:
    """Add an issue mid-run."""
    import json

    cfg = load_config(args.config)

    # Check if issue already exists
    existing = cfg.get_issue(args.number)
    if existing:
        print(f"Issue #{args.number} already exists: {existing.title}")
        sys.exit(1)

    # Add to config file
    with open(cfg.config_path) as f:
        raw = json.load(f)

    new_issue = {
        "number": args.number,
        "title": args.title or f"Issue #{args.number}",
        "priority": args.priority,
        "depends_on": [],
        "wave": args.wave,
        "status": "pending",
        "assigned_worker": None,
    }
    raw["issues"].append(new_issue)

    from .state import atomic_write
    atomic_write(cfg.config_path, raw)

    print(f"Added issue #{args.number}: {new_issue['title']} (wave {args.wave}, priority {args.priority})")


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="orchestrator",
        description="General-purpose parallel Claude Code worker orchestration via tmux",
    )
    subparsers = parser.add_subparsers(dest="command", required=True)

    # launch
    p_launch = subparsers.add_parser("launch", help="Launch unified parallel workers")
    p_launch.add_argument("--dry-run", action="store_true", help="Validate without making changes")
    p_launch.add_argument("--workers", type=int, help="Override number of workers")
    p_launch.add_argument("--session", type=str, default="orchestrator", help="Tmux session name")
    p_launch.add_argument("--config-dir", type=str, default=None,
                          help="Directory with *-issues.json configs")
    p_launch.add_argument("--config", type=str, default=None,
                          help="Single config file (backward compat)")

    # monitor
    p_monitor = subparsers.add_parser("monitor", help="Run the monitor loop")
    p_monitor.add_argument("--cycle", type=int, help="Cycle interval in seconds")
    p_monitor.add_argument("--no-delay", action="store_true", help="Skip initial 60s delay")
    p_monitor.add_argument("--workers", type=int, help="Override number of workers")
    p_monitor.add_argument("--session", type=str, default="orchestrator", help="Tmux session name")
    p_monitor.add_argument("--config-dir", type=str, default=None,
                           help="Directory with *-issues.json configs")
    p_monitor.add_argument("--config", type=str, default=str(default_config_path()),
                           help="Single config file (backward compat)")

    # cleanup
    p_cleanup = subparsers.add_parser("cleanup", help="Clean up tmux session and worktrees")
    p_cleanup.add_argument("--keep-worktrees", action="store_true",
                           help="Keep worktrees for inspection")
    p_cleanup.add_argument("--config", type=str, default=str(default_config_path()),
                           help="Path to config file")

    # status
    p_status = subparsers.add_parser("status", help="Display one-shot status")
    p_status.add_argument("--workers", type=int, help="Override number of workers")
    p_status.add_argument("--config-dir", type=str, default=None,
                          help="Directory with *-issues.json configs")
    p_status.add_argument("--config", type=str, default=None,
                          help="Single config file")

    # dashboard
    p_dashboard = subparsers.add_parser("dashboard", help="Live terminal dashboard")
    p_dashboard.add_argument("--workers", type=int, help="Override number of workers")
    p_dashboard.add_argument("--config-dir", type=str, default=None,
                             help="Directory with *-issues.json configs")
    p_dashboard.add_argument("--config", type=str, default=None,
                             help="Single config file")

    # add-issue
    p_add = subparsers.add_parser("add-issue", help="Add an issue mid-run")
    p_add.add_argument("number", type=int, help="Issue number")
    p_add.add_argument("--title", type=str, default="", help="Issue title")
    p_add.add_argument("--priority", type=int, default=1, help="Priority (1=high)")
    p_add.add_argument("--wave", type=int, default=99, help="Wave number")
    p_add.add_argument("--config", type=str, default=str(default_config_path()),
                        help="Path to config file")

    args = parser.parse_args()

    commands = {
        "launch": cmd_launch,
        "monitor": cmd_monitor,
        "cleanup": cmd_cleanup,
        "status": cmd_status,
        "dashboard": cmd_dashboard,
        "add-issue": cmd_add_issue,
    }

    handler = commands.get(args.command)
    if handler:
        handler(args)
    else:
        parser.print_help()
        sys.exit(1)


if __name__ == "__main__":
    main()
