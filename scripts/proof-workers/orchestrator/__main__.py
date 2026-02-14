"""CLI entry point: python -m orchestrator <command>"""

from __future__ import annotations

import argparse
import shutil
import sys
import time
from pathlib import Path

from .config import default_config_path, load_config, validate_config
from .state import StateManager, now_iso


def cmd_launch(args: argparse.Namespace) -> None:
    """Launch parallel workers in a tmux session."""
    from . import git, tmux
    from .prompt import generate_prompt

    cfg = load_config(args.config)
    if args.workers:
        cfg.num_workers = args.workers

    errors = validate_config(cfg)
    if errors:
        print("Config validation errors:", file=sys.stderr)
        for e in errors:
            print(f"  - {e}", file=sys.stderr)
        sys.exit(1)

    state = StateManager(cfg)
    dry_run = args.dry_run

    print("+" + "=" * 58 + "+")
    print("|  Parallel Claude Code Workers -- Orchestrator             |")
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
    # Check for platform-specific tools
    repo = cfg.primary_repo()
    if repo.platform == "gitlab" and not shutil.which("glab"):
        missing.append("glab")
    elif repo.platform == "github" and not shutil.which("gh"):
        missing.append("gh")

    if missing:
        print(f"ERROR: Missing required commands: {', '.join(missing)}", file=sys.stderr)
        sys.exit(1)
    print("  Prerequisites OK")

    # Load config info
    print(f"  Project: {cfg.project}")
    print(f"  Repos: {list(cfg.repos.keys())}")
    print(f"  Issues: {len(cfg.issues)}")
    print(f"  Workers: {cfg.num_workers}")
    print(f"  Pipeline: {' -> '.join(cfg.pipeline)}")
    if cfg.project_context.language:
        print(f"  Language: {cfg.project_context.language}")
    print()

    # Fetch origin
    print("-- Fetching origin --")
    for name, repo_cfg in cfg.repos.items():
        if not dry_run:
            success = git.fetch(repo_cfg.path)
            status = "OK" if success else "FAILED"
        else:
            status = "(skipped)"
        print(f"  {name}: {status}")
    print()

    # Create worktrees
    print("-- Creating worktrees --")
    for worker_id, issue_num in cfg.initial_assignments.items():
        repo_cfg = cfg.repo_for_issue_by_number(issue_num)
        if not repo_cfg:
            print(f"  Worker {worker_id}: no repo for issue #{issue_num}", file=sys.stderr)
            continue

        branch = f"{repo_cfg.branch_prefix}{issue_num}"
        wt_path = f"{repo_cfg.worktree_base}/issue-{issue_num}"

        if Path(wt_path).is_dir():
            print(f"  Worker {worker_id}: worktree exists: {wt_path} (reusing)")
            continue

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
    for worker_id, issue_num in cfg.initial_assignments.items():
        repo_cfg = cfg.repo_for_issue_by_number(issue_num)
        if not repo_cfg:
            continue
        branch = f"{repo_cfg.branch_prefix}{issue_num}"
        wt_path = f"{repo_cfg.worktree_base}/issue-{issue_num}"

        print(f"  worker-{worker_id} -> issue #{issue_num}")
        if not dry_run:
            state.init_worker(worker_id, issue_num, branch, wt_path)
    print()

    # Create tmux session
    print("-- Creating tmux session --")
    if not dry_run and tmux.session_exists(cfg.tmux_session):
        print(f"ERROR: tmux session '{cfg.tmux_session}' already exists.", file=sys.stderr)
        print(f"  Kill it first: tmux kill-session -t {cfg.tmux_session}", file=sys.stderr)
        print(f"  Or run cleanup: python -m orchestrator cleanup", file=sys.stderr)
        sys.exit(1)

    if not dry_run:
        tmux.create_session(cfg.tmux_session, "orchestrator", str(cfg.orch_root))
        for worker_id, issue_num in cfg.initial_assignments.items():
            repo_cfg = cfg.repo_for_issue_by_number(issue_num)
            wt_path = f"{repo_cfg.worktree_base}/issue-{issue_num}" if repo_cfg else ""
            tmux.new_window(cfg.tmux_session, f"worker-{worker_id}", wt_path)
        tmux.new_window(cfg.tmux_session, "dashboard", str(cfg.orch_root))
    print(f"  {cfg.num_workers + 2} windows created (orchestrator, worker-1..{cfg.num_workers}, dashboard)")
    print()

    # Launch workers
    print(f"-- Launching workers ({cfg.stagger_delay}s stagger) --")
    for worker_id in sorted(cfg.initial_assignments.keys()):
        issue_num = cfg.initial_assignments[worker_id]
        repo_cfg = cfg.repo_for_issue_by_number(issue_num)
        if not repo_cfg:
            continue

        wt_path = f"{repo_cfg.worktree_base}/issue-{issue_num}"
        log_file = StateManager.log_path(worker_id)
        signal_file = StateManager.signal_path(worker_id)
        prompt_path = StateManager.prompt_path(worker_id)

        print(f"  Worker {worker_id}: issue #{issue_num} -> {log_file}")

        if not dry_run:
            # Clear stale signal
            signal_file.unlink(missing_ok=True)

            # Generate prompt
            issue = cfg.get_issue(issue_num)
            if issue:
                stage_name = cfg.pipeline[issue.pipeline_stage] if cfg.pipeline else "implement"
                prompt = generate_prompt(
                    stage_name, issue, worker_id, wt_path, repo_cfg, cfg, state,
                )
                prompt_path.write_text(prompt)

                # Update state to running
                worker = state.load_worker(worker_id)
                if worker:
                    worker.status = "running"
                    worker.started_at = now_iso()
                    worker.stage = stage_name
                    state.save_worker(worker)

                # Update issue status
                state.update_issue_status(issue_num, "in_progress", assigned_worker=worker_id)

                # Launch in tmux
                tmux.send_command(
                    cfg.tmux_session,
                    f"worker-{worker_id}",
                    f'cd {wt_path} && claude -p --dangerously-skip-permissions '
                    f'"$(cat {prompt_path})" > {log_file} 2>&1; echo $? > {signal_file}',
                )

            # Stagger launches
            if worker_id < cfg.num_workers:
                print(f"  Waiting {cfg.stagger_delay}s before next launch...")
                time.sleep(cfg.stagger_delay)
    print()

    # Launch monitor
    print("-- Starting monitor --")
    if not dry_run:
        pkg_dir = Path(__file__).resolve().parent.parent
        tmux.send_command(
            cfg.tmux_session,
            "orchestrator",
            f'cd {pkg_dir} && python3 -m orchestrator monitor --config {cfg.config_path} '
            f'2>&1 | tee /tmp/orchestrator-monitor.log',
        )
    print("  Monitor loop started in window 0")
    print()

    # Launch dashboard
    print("-- Starting dashboard --")
    if not dry_run:
        pkg_dir = Path(__file__).resolve().parent.parent
        tmux.send_command(
            cfg.tmux_session,
            "dashboard",
            f'cd {pkg_dir} && python3 -m orchestrator dashboard --config {cfg.config_path}',
        )
    print("  Dashboard started in dashboard window")
    print()

    print("=" * 60)
    print(f"  All workers launched.")
    print(f"  Attach: tmux attach -t {cfg.tmux_session}")
    print(f"  Dashboard: Ctrl-b then select 'dashboard' window")
    print(f"  Cleanup: python -m orchestrator cleanup --config {cfg.config_path}")
    print("=" * 60)


def cmd_monitor(args: argparse.Namespace) -> None:
    """Run the monitor loop."""
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
    cfg = load_config(args.config)
    state = StateManager(cfg)

    print(f"Project: {cfg.project}")
    print(f"Session: {cfg.tmux_session}")
    print()

    # Issue summary
    from .issues import get_completed_count, get_failed_count, get_pending_count
    total = len(cfg.issues)
    completed = get_completed_count(cfg)
    pending = get_pending_count(cfg)
    failed = get_failed_count(cfg)
    print(f"Issues: {completed}/{total} completed, {pending} pending, {failed} failed")
    print()

    # Worker summary
    print(f"{'Worker':<8} {'Issue':<8} {'Status':<10} {'Retries':<8} {'Branch'}")
    print("-" * 60)
    for i in range(1, cfg.num_workers + 1):
        worker = state.load_worker(i)
        if worker:
            issue_str = f"#{worker.issue_number}" if worker.issue_number else "--"
            branch_str = worker.branch or "--"
            print(f"  {i:<6} {issue_str:<8} {worker.status:<10} {worker.retry_count:<8} {branch_str}")
        else:
            print(f"  {i:<6} {'--':<8} {'no state':<10}")
    print()

    # Pending issues
    pending_issues = [i for i in cfg.issues if i.status == "pending"]
    if pending_issues:
        print("Pending issues:")
        for issue in sorted(pending_issues, key=lambda i: (i.wave, i.priority)):
            deps = f" (depends on: {issue.depends_on})" if issue.depends_on else ""
            print(f"  #{issue.number}: {issue.title} [wave {issue.wave}]{deps}")


def cmd_dashboard(args: argparse.Namespace) -> None:
    """Run the live dashboard."""
    from .dashboard import run_dashboard

    cfg = load_config(args.config)
    state = StateManager(cfg)
    run_dashboard(cfg, state)


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
    p_launch = subparsers.add_parser("launch", help="Launch parallel workers")
    p_launch.add_argument("--dry-run", action="store_true", help="Validate without making changes")
    p_launch.add_argument("--workers", type=int, help="Override number of workers")
    p_launch.add_argument("--config", type=str, default=str(default_config_path()),
                          help="Path to config file")

    # monitor
    p_monitor = subparsers.add_parser("monitor", help="Run the monitor loop")
    p_monitor.add_argument("--cycle", type=int, help="Cycle interval in seconds")
    p_monitor.add_argument("--no-delay", action="store_true", help="Skip initial 60s delay")
    p_monitor.add_argument("--config", type=str, default=str(default_config_path()),
                           help="Path to config file")

    # cleanup
    p_cleanup = subparsers.add_parser("cleanup", help="Clean up tmux session and worktrees")
    p_cleanup.add_argument("--keep-worktrees", action="store_true",
                           help="Keep worktrees for inspection")
    p_cleanup.add_argument("--config", type=str, default=str(default_config_path()),
                           help="Path to config file")

    # status
    p_status = subparsers.add_parser("status", help="Display one-shot status")
    p_status.add_argument("--config", type=str, default=str(default_config_path()),
                          help="Path to config file")

    # dashboard
    p_dashboard = subparsers.add_parser("dashboard", help="Live terminal dashboard")
    p_dashboard.add_argument("--config", type=str, default=str(default_config_path()),
                             help="Path to config file")

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
