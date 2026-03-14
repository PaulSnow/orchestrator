#!/usr/bin/env python3
"""
Claude Code Tmux Worker Orchestrator

Spawns tmux workers running Claude Code sessions to process issues.
Supports both local issue files AND GitLab/GitHub issue fetching.
Workers communicate via IPC (file-based message queue).
Unknown errors spawn a supervisor worker to decide recovery actions.

Usage:
    # Local issue files
    python3 orchestrator.py --issues-dir ./docs/plans/issues/ --max-workers 4

    # GitLab issues (fetches prompts from GitLab issue bodies)
    python3 orchestrator.py --gitlab-issues 193,194,195 --repo /path/to/repo --max-workers 4

    # Mixed: GitLab project with issue numbers from JSON config
    python3 orchestrator.py --config ./config/issues.json --max-workers 4
"""

import argparse
import json
import os
import subprocess
import sys
import time
import uuid
from dataclasses import dataclass, asdict, field
from datetime import datetime
from enum import Enum
from pathlib import Path
from typing import Optional, Dict, List
import threading
import queue


class WorkerState(Enum):
    STARTING = "starting"
    RUNNING = "running"
    COMPLETED = "completed"
    ERROR = "error"
    KILLED = "killed"


class MessageType(Enum):
    STATE_UPDATE = "state_update"
    LOG = "log"
    ERROR = "error"
    COMPLETION = "completion"
    UNKNOWN = "unknown"
    SUPERVISOR_DECISION = "supervisor_decision"


@dataclass
class WorkerMessage:
    worker_id: str
    message_type: str
    payload: dict
    timestamp: str = ""

    def __post_init__(self):
        if not self.timestamp:
            self.timestamp = datetime.now().isoformat()


@dataclass
class Issue:
    """Represents an issue to work on."""
    number: int
    title: str
    body: str
    repo_path: str
    platform: str = "gitlab"  # gitlab or github
    source: str = "remote"    # remote or local


@dataclass
class Worker:
    worker_id: str
    issue_file: str
    tmux_session: str
    state: WorkerState
    issue: Optional[Issue] = None
    pid: Optional[int] = None
    started_at: Optional[str] = None
    completed_at: Optional[str] = None
    error: Optional[str] = None


def fetch_gitlab_issue(issue_number: int, repo_path: str) -> Optional[Issue]:
    """Fetch an issue from GitLab using glab CLI."""
    try:
        result = subprocess.run(
            ["glab", "issue", "view", str(issue_number)],
            capture_output=True,
            text=True,
            cwd=repo_path,
            timeout=30,
            check=False,
        )
        if result.returncode == 0:
            # Parse the output - glab outputs title on first line, then body
            lines = result.stdout.strip().split('\n')
            title = ""
            body_lines = []
            in_body = False

            for line in lines:
                if line.startswith("title:"):
                    title = line.replace("title:", "").strip()
                elif line.startswith("--"):
                    in_body = True
                elif in_body:
                    body_lines.append(line)

            return Issue(
                number=issue_number,
                title=title,
                body='\n'.join(body_lines),
                repo_path=repo_path,
                platform="gitlab",
                source="remote"
            )
        else:
            print(f"[WARN] Could not fetch GitLab issue #{issue_number}: {result.stderr}")
            return None
    except Exception as e:
        print(f"[ERROR] Failed to fetch GitLab issue #{issue_number}: {e}")
        return None


def fetch_github_issue(issue_number: int, repo_path: str) -> Optional[Issue]:
    """Fetch an issue from GitHub using gh CLI."""
    try:
        result = subprocess.run(
            ["gh", "issue", "view", str(issue_number)],
            capture_output=True,
            text=True,
            cwd=repo_path,
            timeout=30,
            check=False,
        )
        if result.returncode == 0:
            lines = result.stdout.strip().split('\n')
            title = ""
            body_lines = []
            in_body = False

            for line in lines:
                if line.startswith("title:"):
                    title = line.replace("title:", "").strip()
                elif line.startswith("--"):
                    in_body = True
                elif in_body:
                    body_lines.append(line)

            return Issue(
                number=issue_number,
                title=title,
                body='\n'.join(body_lines),
                repo_path=repo_path,
                platform="github",
                source="remote"
            )
        else:
            print(f"[WARN] Could not fetch GitHub issue #{issue_number}: {result.stderr}")
            return None
    except Exception as e:
        print(f"[ERROR] Failed to fetch GitHub issue #{issue_number}: {e}")
        return None


def load_issues_from_config(config_path: Path) -> List[Issue]:
    """Load issues from a JSON config file.

    Config format:
    {
        "repo_path": "/path/to/repo",
        "platform": "gitlab",
        "issues": [193, 194, 195]
    }
    """
    with open(config_path) as f:
        config = json.load(f)

    repo_path = config.get("repo_path", os.getcwd())
    platform = config.get("platform", "gitlab")
    issue_numbers = config.get("issues", [])

    issues = []
    for num in issue_numbers:
        if platform == "gitlab":
            issue = fetch_gitlab_issue(num, repo_path)
        else:
            issue = fetch_github_issue(num, repo_path)
        if issue:
            issues.append(issue)

    return issues


class IPCChannel:
    """File-based IPC for worker communication."""

    def __init__(self, base_dir: Path):
        self.base_dir = base_dir
        self.inbox_dir = base_dir / "inbox"
        self.outbox_dir = base_dir / "outbox"
        self.inbox_dir.mkdir(parents=True, exist_ok=True)
        self.outbox_dir.mkdir(parents=True, exist_ok=True)

    def send_to_worker(self, worker_id: str, message: WorkerMessage):
        """Send message to a specific worker."""
        msg_file = self.outbox_dir / f"{worker_id}_{uuid.uuid4().hex[:8]}.json"
        with open(msg_file, 'w') as f:
            json.dump(asdict(message), f)

    def receive_from_workers(self) -> List[WorkerMessage]:
        """Receive all pending messages from workers."""
        messages = []
        for msg_file in self.inbox_dir.glob("*.json"):
            try:
                with open(msg_file) as f:
                    data = json.load(f)
                messages.append(WorkerMessage(**data))
                msg_file.unlink()  # Remove processed message
            except Exception as e:
                print(f"[WARN] Failed to read message {msg_file}: {e}")
        return messages

    def get_worker_inbox_path(self, worker_id: str) -> Path:
        """Get path for worker to write messages."""
        return self.inbox_dir

    def get_worker_outbox_path(self, worker_id: str) -> Path:
        """Get path for worker to read messages."""
        return self.outbox_dir


class Orchestrator:
    """Main orchestrator that manages tmux workers."""

    def __init__(self, issues_dir: Optional[Path] = None, issues_list: Optional[List[Issue]] = None,
                 max_workers: int = 4, work_dir: Optional[Path] = None):
        self.issues_dir = issues_dir
        self.issues_list = issues_list or []
        self.max_workers = max_workers
        self.work_dir = work_dir or Path("/tmp/claude-orchestrator")
        self.work_dir.mkdir(parents=True, exist_ok=True)

        self.ipc = IPCChannel(self.work_dir / "ipc")
        self.workers: Dict[str, Worker] = {}
        self.pending_issues: queue.Queue = queue.Queue()
        self.running = False
        self.log_file = self.work_dir / "orchestrator.log"

    def log(self, message: str, level: str = "INFO"):
        """Log message to file and stdout."""
        timestamp = datetime.now().isoformat()
        log_line = f"[{timestamp}] [{level}] {message}"
        print(log_line)
        with open(self.log_file, 'a') as f:
            f.write(log_line + "\n")

    def discover_issues(self) -> List:
        """Find all issues - either from directory or pre-loaded list."""
        if self.issues_list:
            self.log(f"Using {len(self.issues_list)} pre-loaded issues")
            return self.issues_list
        elif self.issues_dir:
            issues = sorted(self.issues_dir.glob("issue-*.md"))
            self.log(f"Discovered {len(issues)} issues in {self.issues_dir}")
            return issues
        else:
            self.log("No issues configured", "ERROR")
            return []

    def generate_worker_script(self, worker_id: str, issue_file: Optional[Path], issue: Optional[Issue] = None) -> Path:
        """Generate the bash script that runs Claude Code for an issue.

        Args:
            worker_id: Unique worker identifier
            issue_file: Path to local issue file (if using local mode)
            issue: Issue object (if using GitLab/GitHub mode)
        """
        script_path = self.work_dir / f"worker_{worker_id}.sh"
        inbox_path = self.ipc.get_worker_inbox_path(worker_id)

        # Determine issue source and repo path
        if issue:
            issue_name = f"Issue #{issue.number}: {issue.title}"
            repo_path = issue.repo_path
            # Write issue body to a temp file
            issue_body_file = self.work_dir / f"issue_{issue.number}_body.md"
            with open(issue_body_file, 'w') as f:
                f.write(f"# Issue #{issue.number}: {issue.title}\n\n")
                f.write(issue.body)
            issue_file_path = str(issue_body_file)
        else:
            issue_name = issue_file.name
            repo_path = os.getcwd()
            issue_file_path = str(issue_file.absolute())

        script_content = f'''#!/bin/bash
# Worker script for {worker_id}
# Issue: {issue_name}

WORKER_ID="{worker_id}"
ISSUE_FILE="{issue_file_path}"
INBOX_PATH="{inbox_path}"
LOG_FILE="{self.work_dir}/worker_{worker_id}.log"
REPO_DIR="{repo_path}"

# Function to send message to orchestrator
send_message() {{
    local msg_type="$1"
    local payload="$2"
    local timestamp=$(date -Iseconds)
    local msg_file="$INBOX_PATH/${{WORKER_ID}}_$(date +%s%N).json"
    cat > "$msg_file" << EOF
{{"worker_id": "$WORKER_ID", "message_type": "$msg_type", "payload": $payload, "timestamp": "$timestamp"}}
EOF
}}

# Signal start
send_message "state_update" '{{"state": "running"}}'

# Read the issue file to get the prompt
PROMPT=$(cat "$ISSUE_FILE")

# Set up git worktree for isolated work
BRANCH_NAME="issue/{worker_id}"
WORKTREE_DIR="{self.work_dir}/worktrees/{worker_id}"

# Create worktree (remove old one if exists)
cd "$REPO_DIR"
git worktree remove "$WORKTREE_DIR" --force 2>/dev/null || true
git branch -D "$BRANCH_NAME" 2>/dev/null || true
git worktree add "$WORKTREE_DIR" -b "$BRANCH_NAME"

# Work in the worktree
cd "$WORKTREE_DIR"
echo "Working in worktree: $WORKTREE_DIR (branch: $BRANCH_NAME)" >> "$LOG_FILE"

# Create a prompt file for Claude
PROMPT_FILE="{self.work_dir}/prompt_{worker_id}.txt"
cat > "$PROMPT_FILE" << 'PROMPT_EOF'
You are a tmux worker implementing a specific issue. Read the issue file and implement ONLY what is described.

CRITICAL RULES:
1. You are working in a git worktree at: {self.work_dir}/worktrees/{worker_id}
2. Your branch is: issue/{worker_id}
3. Implement the code changes described in the issue
4. Run the acceptance criteria tests
5. Commit your changes with message: "{worker_id}: <summary>"
6. If successful, signal completion
7. If you encounter an error you cannot resolve, signal error with details
8. Do NOT ask questions - make reasonable decisions
9. Do NOT modify files outside the scope of your issue
10. Do NOT merge or push - just commit to your branch

Issue file to implement:
PROMPT_EOF

cat "$ISSUE_FILE" >> "$PROMPT_FILE"

cat >> "$PROMPT_FILE" << 'PROMPT_EOF'

After implementation:
1. Run: go build ./...
2. Run any tests specified in acceptance criteria
3. If all pass, respond with: WORKER_STATUS:COMPLETED
4. If errors occur, respond with: WORKER_STATUS:ERROR:<description>

Begin implementation now.
PROMPT_EOF

# Run Claude Code (non-interactive, skip permission prompts)
echo "Starting Claude Code session..." >> "$LOG_FILE"
claude -p --dangerously-skip-permissions "$(cat $PROMPT_FILE)" 2>&1 | tee -a "$LOG_FILE" | while read line; do
    # Check for completion signals
    if echo "$line" | grep -q "WORKER_STATUS:COMPLETED"; then
        send_message "completion" '{{"success": true}}'
        exit 0
    elif echo "$line" | grep -q "WORKER_STATUS:ERROR:"; then
        error_msg=$(echo "$line" | sed 's/.*WORKER_STATUS:ERROR://')
        send_message "error" "{{\\"error\\": \\"$error_msg\\"}}"
        exit 1
    fi
done

# If we get here without explicit status, check exit code
EXIT_CODE=$?
if [ $EXIT_CODE -eq 0 ]; then
    send_message "completion" '{{"success": true, "implicit": true}}'
else
    send_message "error" '{{"error": "Worker exited with code '$EXIT_CODE'"}}'
fi
'''

        with open(script_path, 'w') as f:
            f.write(script_content)
        os.chmod(script_path, 0o755)
        return script_path

    def spawn_worker(self, issue_source) -> Worker:
        """Spawn a new tmux worker for an issue.

        Args:
            issue_source: Either a Path to a local issue file, or an Issue object
        """
        # Handle both Path (local file) and Issue (GitLab/GitHub) objects
        if isinstance(issue_source, Issue):
            issue = issue_source
            worker_id = f"worker-issue-{issue.number}"
            issue_file = None
        else:
            issue = None
            issue_file = issue_source
            worker_id = f"worker-{issue_file.stem}"

        tmux_session = f"claude-{worker_id}"

        # Generate worker script
        script_path = self.generate_worker_script(worker_id, issue_file, issue)

        # Kill existing session if any
        subprocess.run(["tmux", "kill-session", "-t", tmux_session],
                      capture_output=True)

        # Create new tmux session running the worker script
        subprocess.run([
            "tmux", "new-session", "-d", "-s", tmux_session,
            str(script_path)
        ], check=True)

        # Determine issue file string for Worker
        if issue:
            issue_file_str = f"GitLab #{issue.number}"
            issue_name = f"#{issue.number}: {issue.title}"
        else:
            issue_file_str = str(issue_file)
            issue_name = issue_file.name

        worker = Worker(
            worker_id=worker_id,
            issue_file=issue_file_str,
            tmux_session=tmux_session,
            state=WorkerState.STARTING,
            issue=issue,
            started_at=datetime.now().isoformat()
        )

        self.workers[worker_id] = worker
        self.log(f"Spawned worker {worker_id} for {issue_name}")
        return worker

    def spawn_supervisor(self, original_worker: Worker, error_context: dict) -> Worker:
        """Spawn a supervisor worker to decide how to handle an error."""
        supervisor_id = f"supervisor-{original_worker.worker_id}-{uuid.uuid4().hex[:4]}"
        tmux_session = f"claude-{supervisor_id}"

        # Create supervisor prompt
        prompt_file = self.work_dir / f"supervisor_{supervisor_id}.txt"
        prompt_content = f'''You are a supervisor AI deciding how to handle a worker error.

WORKER: {original_worker.worker_id}
ISSUE FILE: {original_worker.issue_file}
ERROR CONTEXT:
{json.dumps(error_context, indent=2)}

Your options:
1. REBOOT - Kill and restart the worker with the same issue
2. KILL - Abandon this issue (too complex, blocked, etc.)
3. PROMPT:<new instructions> - Send new instructions to guide the worker

Analyze the error and respond with ONE of:
SUPERVISOR_DECISION:REBOOT
SUPERVISOR_DECISION:KILL:<reason>
SUPERVISOR_DECISION:PROMPT:<detailed instructions to fix the issue>

Be decisive. If the error is transient, REBOOT. If it's a fundamental blocker, KILL.
If more guidance would help, provide a PROMPT with specific instructions.
'''

        with open(prompt_file, 'w') as f:
            f.write(prompt_content)

        # Generate supervisor script
        script_path = self.work_dir / f"supervisor_{supervisor_id}.sh"
        script_content = f'''#!/bin/bash
INBOX_PATH="{self.ipc.get_worker_inbox_path(supervisor_id)}"

send_message() {{
    local msg_type="$1"
    local payload="$2"
    local timestamp=$(date -Iseconds)
    local msg_file="$INBOX_PATH/supervisor_$(date +%s%N).json"
    cat > "$msg_file" << EOF
{{"worker_id": "{supervisor_id}", "message_type": "$msg_type", "payload": $payload, "timestamp": "$timestamp"}}
EOF
}}

claude --print "{prompt_file}" 2>&1 | while read line; do
    if echo "$line" | grep -q "SUPERVISOR_DECISION:"; then
        decision=$(echo "$line" | sed 's/.*SUPERVISOR_DECISION://')
        send_message "supervisor_decision" "{{\\"decision\\": \\"$decision\\", \\"original_worker\\": \\"{original_worker.worker_id}\\"}}"
        exit 0
    fi
done
'''

        with open(script_path, 'w') as f:
            f.write(script_content)
        os.chmod(script_path, 0o755)

        subprocess.run(["tmux", "kill-session", "-t", tmux_session], capture_output=True)
        subprocess.run(["tmux", "new-session", "-d", "-s", tmux_session, str(script_path)], check=True)

        supervisor = Worker(
            worker_id=supervisor_id,
            issue_file=original_worker.issue_file,
            tmux_session=tmux_session,
            state=WorkerState.RUNNING,
            started_at=datetime.now().isoformat()
        )

        self.workers[supervisor_id] = supervisor
        self.log(f"Spawned supervisor {supervisor_id} for {original_worker.worker_id}")
        return supervisor

    def kill_worker(self, worker_id: str, cleanup_worktree: bool = False):
        """Kill a worker's tmux session and optionally clean up worktree."""
        if worker_id in self.workers:
            worker = self.workers[worker_id]
            subprocess.run(["tmux", "kill-session", "-t", worker.tmux_session],
                          capture_output=True)
            worker.state = WorkerState.KILLED
            worker.completed_at = datetime.now().isoformat()
            self.log(f"Killed worker {worker_id}")

            # Optionally clean up worktree (not on success - keep for review)
            if cleanup_worktree and "supervisor" not in worker_id:
                worktree_dir = self.work_dir / "worktrees" / worker_id
                if worktree_dir.exists():
                    subprocess.run(["git", "worktree", "remove", str(worktree_dir), "--force"],
                                  capture_output=True, cwd=os.getcwd())
                    self.log(f"Cleaned up worktree for {worker_id}")

    def handle_message(self, message: WorkerMessage):
        """Handle a message from a worker."""
        worker_id = message.worker_id
        msg_type = message.message_type
        payload = message.payload

        self.log(f"Message from {worker_id}: {msg_type} - {payload}")

        if msg_type == "state_update":
            if worker_id in self.workers:
                self.workers[worker_id].state = WorkerState(payload.get("state", "running"))

        elif msg_type == "completion":
            if worker_id in self.workers:
                self.workers[worker_id].state = WorkerState.COMPLETED
                self.workers[worker_id].completed_at = datetime.now().isoformat()
                self.log(f"Worker {worker_id} COMPLETED successfully", "SUCCESS")
                self.kill_worker(worker_id)

        elif msg_type == "error":
            if worker_id in self.workers:
                worker = self.workers[worker_id]
                worker.state = WorkerState.ERROR
                worker.error = payload.get("error", "Unknown error")
                self.log(f"Worker {worker_id} ERROR: {worker.error}", "ERROR")
                # Spawn supervisor to decide what to do
                self.spawn_supervisor(worker, payload)

        elif msg_type == "supervisor_decision":
            decision = payload.get("decision", "")
            original_worker_id = payload.get("original_worker", "")

            if decision.startswith("REBOOT"):
                self.log(f"Supervisor decided: REBOOT {original_worker_id}")
                if original_worker_id in self.workers:
                    issue_file = Path(self.workers[original_worker_id].issue_file)
                    self.kill_worker(original_worker_id)
                    self.spawn_worker(issue_file)

            elif decision.startswith("KILL:"):
                reason = decision.split(":", 1)[1] if ":" in decision else "No reason"
                self.log(f"Supervisor decided: KILL {original_worker_id} - {reason}", "WARN")
                self.kill_worker(original_worker_id)

            elif decision.startswith("PROMPT:"):
                new_prompt = decision.split(":", 1)[1] if ":" in decision else ""
                self.log(f"Supervisor decided: PROMPT {original_worker_id}")
                # Send new instructions to worker
                self.ipc.send_to_worker(original_worker_id, WorkerMessage(
                    worker_id="orchestrator",
                    message_type="new_instructions",
                    payload={"instructions": new_prompt}
                ))

            # Kill supervisor after decision
            self.kill_worker(worker_id)

        else:
            self.log(f"Unknown message type: {msg_type}", "WARN")
            # Spawn supervisor for unknown messages
            if worker_id in self.workers:
                self.spawn_supervisor(self.workers[worker_id], {
                    "unknown_message": message.message_type,
                    "payload": payload
                })

    def get_active_worker_count(self) -> int:
        """Count currently active workers."""
        return sum(1 for w in self.workers.values()
                   if w.state in (WorkerState.STARTING, WorkerState.RUNNING))

    def run(self):
        """Main orchestration loop."""
        self.log("=" * 60)
        self.log("Claude Code Tmux Worker Orchestrator Starting")
        self.log("=" * 60)

        # Discover issues
        issues = self.discover_issues()
        for issue in issues:
            self.pending_issues.put(issue)

        self.running = True

        while self.running:
            # Process incoming messages
            messages = self.ipc.receive_from_workers()
            for msg in messages:
                self.handle_message(msg)

            # Spawn new workers if capacity available
            while (self.get_active_worker_count() < self.max_workers and
                   not self.pending_issues.empty()):
                issue = self.pending_issues.get()
                self.spawn_worker(issue)

            # Check if all done
            all_done = (self.pending_issues.empty() and
                        self.get_active_worker_count() == 0)

            if all_done:
                self.log("All workers completed!")
                self.running = False
                break

            # Status update
            active = self.get_active_worker_count()
            completed = sum(1 for w in self.workers.values()
                           if w.state == WorkerState.COMPLETED)
            errors = sum(1 for w in self.workers.values()
                        if w.state == WorkerState.ERROR)

            self.log(f"Status: {active} active, {completed} completed, {errors} errors, "
                    f"{self.pending_issues.qsize()} pending")

            time.sleep(5)  # Poll interval

        self.print_summary()

    def print_summary(self):
        """Print final summary."""
        self.log("=" * 60)
        self.log("ORCHESTRATION COMPLETE")
        self.log("=" * 60)

        for worker_id, worker in self.workers.items():
            if "supervisor" in worker_id:
                continue
            status = "SUCCESS" if worker.state == WorkerState.COMPLETED else str(worker.state.value)
            self.log(f"  {worker_id}: {status}")
            if worker.error:
                self.log(f"    Error: {worker.error}")


def main():
    parser = argparse.ArgumentParser(
        description="Claude Code Tmux Worker Orchestrator",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Process local issue files
  %(prog)s --issues-dir ./docs/plans/issues/

  # Process GitLab issues by number
  %(prog)s --gitlab-issues 193,194,195 --repo /path/to/repo

  # Process GitHub issues by number
  %(prog)s --github-issues 42,43 --repo /path/to/repo

  # Use a JSON config file
  %(prog)s --config ./config/wallet-issues.json

Config file format:
  {
    "repo_path": "/path/to/repo",
    "platform": "gitlab",
    "issues": [193, 194, 195]
  }
"""
    )

    # Issue sources (mutually exclusive)
    source_group = parser.add_mutually_exclusive_group(required=True)
    source_group.add_argument("--issues-dir", type=Path,
                              help="Directory containing issue-*.md files")
    source_group.add_argument("--gitlab-issues", type=str,
                              help="Comma-separated GitLab issue numbers (e.g., 193,194,195)")
    source_group.add_argument("--github-issues", type=str,
                              help="Comma-separated GitHub issue numbers")
    source_group.add_argument("--config", type=Path,
                              help="JSON config file with issues")

    # Common options
    parser.add_argument("--repo", type=Path, default=None,
                       help="Repository path (required for --gitlab-issues/--github-issues)")
    parser.add_argument("--max-workers", type=int, default=4,
                       help="Maximum concurrent workers (default: 4)")
    parser.add_argument("--work-dir", type=Path, default=None,
                       help="Working directory for IPC and logs (default: /tmp/claude-orchestrator)")

    args = parser.parse_args()

    # Determine issues based on source
    issues: List[Issue] = []

    if args.issues_dir:
        if not args.issues_dir.exists():
            print(f"Error: Issues directory not found: {args.issues_dir}")
            sys.exit(1)
        # Create orchestrator with local files mode
        orchestrator = Orchestrator(
            issues_dir=args.issues_dir,
            max_workers=args.max_workers,
            work_dir=args.work_dir
        )
    elif args.gitlab_issues:
        if not args.repo:
            print("Error: --repo is required when using --gitlab-issues")
            sys.exit(1)
        issue_numbers = [int(n.strip()) for n in args.gitlab_issues.split(',')]
        print(f"Fetching {len(issue_numbers)} GitLab issues...")
        for num in issue_numbers:
            issue = fetch_gitlab_issue(num, str(args.repo))
            if issue:
                issues.append(issue)
                print(f"  #{num}: {issue.title}")
            else:
                print(f"  #{num}: FAILED to fetch")
        if not issues:
            print("Error: No issues could be fetched")
            sys.exit(1)
        orchestrator = Orchestrator(
            issues_dir=None,
            issues_list=issues,
            max_workers=args.max_workers,
            work_dir=args.work_dir
        )
    elif args.github_issues:
        if not args.repo:
            print("Error: --repo is required when using --github-issues")
            sys.exit(1)
        issue_numbers = [int(n.strip()) for n in args.github_issues.split(',')]
        print(f"Fetching {len(issue_numbers)} GitHub issues...")
        for num in issue_numbers:
            issue = fetch_github_issue(num, str(args.repo))
            if issue:
                issues.append(issue)
                print(f"  #{num}: {issue.title}")
            else:
                print(f"  #{num}: FAILED to fetch")
        if not issues:
            print("Error: No issues could be fetched")
            sys.exit(1)
        orchestrator = Orchestrator(
            issues_dir=None,
            issues_list=issues,
            max_workers=args.max_workers,
            work_dir=args.work_dir
        )
    elif args.config:
        if not args.config.exists():
            print(f"Error: Config file not found: {args.config}")
            sys.exit(1)
        issues = load_issues_from_config(args.config)
        if not issues:
            print("Error: No issues could be loaded from config")
            sys.exit(1)
        print(f"Loaded {len(issues)} issues from config")
        orchestrator = Orchestrator(
            issues_dir=None,
            issues_list=issues,
            max_workers=args.max_workers,
            work_dir=args.work_dir
        )

    try:
        orchestrator.run()
    except KeyboardInterrupt:
        print("\nInterrupted. Killing all workers...")
        for worker_id in list(orchestrator.workers.keys()):
            orchestrator.kill_worker(worker_id)


if __name__ == "__main__":
    main()
