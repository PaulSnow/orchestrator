"""Session teardown and worktree removal."""

from __future__ import annotations

from pathlib import Path

from . import git, tmux
from .config import RunConfig
from .state import StateManager


def run_cleanup(cfg: RunConfig, keep_worktrees: bool = False) -> None:
    """Clean up tmux session, signal files, and optionally worktrees."""

    print("+" + "=" * 38 + "+")
    print("|  Orchestrator Cleanup                 |")
    print("+" + "=" * 38 + "+")
    print()

    # Kill tmux session
    if tmux.session_exists(cfg.tmux_session):
        print(f"Killing tmux session: {cfg.tmux_session}")
        tmux.kill_session(cfg.tmux_session)
        print("  Done.")
    else:
        print(f"No tmux session '{cfg.tmux_session}' found.")

    # Clean signal files
    print()
    print("Cleaning signal files...")
    for i in range(1, cfg.num_workers + 1):
        StateManager.signal_path(i).unlink(missing_ok=True)
    print("  Done.")

    # Remove worktrees
    if keep_worktrees:
        print()
        print("Keeping worktrees (--keep-worktrees specified).")
    else:
        print()
        print("Removing worktrees...")
        for name, repo_cfg in cfg.repos.items():
            wt_base = Path(repo_cfg.worktree_base)
            if not wt_base.is_dir():
                print(f"  No worktree directory for {name}: {wt_base}")
                continue

            for wt_dir in sorted(wt_base.glob("issue-*")):
                if wt_dir.is_dir():
                    print(f"  Removing: {wt_dir.name}")
                    success = git.remove_worktree(repo_cfg.path, str(wt_dir), force=True)
                    if not success:
                        print(f"    WARNING: Could not remove {wt_dir} (may need manual cleanup)")

            # Prune stale worktree references
            print(f"  Pruning stale worktree references for {name}...")
            git.prune_worktrees(repo_cfg.path)

            # Remove the worktree base dir if empty
            try:
                wt_base.rmdir()
            except OSError:
                pass  # Not empty, that's fine

    # Summary
    print()
    print("Cleanup complete.")
    print()
    for name, repo_cfg in cfg.repos.items():
        prefix = repo_cfg.branch_prefix
        if prefix:
            print(f"Branches are preserved for {name}. To list:")
            print(f"  git -C {repo_cfg.path} branch --list '{prefix}*'")
            print()
            print(f"To delete all branches with prefix '{prefix}':")
            print(f"  git -C {repo_cfg.path} branch --list '{prefix}*' | xargs -r git -C {repo_cfg.path} branch -D")
            print()
