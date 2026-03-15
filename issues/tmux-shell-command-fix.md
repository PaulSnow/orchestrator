# tmux: Commands with shell features fail without shell wrapper

## Problem

When running commands via `tmux new-session -d -s name "command"`, shell features like command substitution (`$(cmd)`), pipes (`|`), and redirects (`>`) do not work correctly. tmux passes the command directly without shell interpretation, causing these features to fail silently or produce unexpected results.

## Example of the Bug

```python
# This FAILS - $(cat file) is not interpreted
subprocess.run([
    "tmux", "new-session", "-d", "-s", "my-session",
    'claude -p "$(cat prompt.txt)" > output.log 2>&1'
])
# Result: tmux runs the literal string, command substitution doesn't happen
```

## Root Cause

tmux executes the command argument directly via exec(), not through a shell. This means:
- `$(...)` command substitution is not processed
- `|` pipes don't work
- `>` redirects may not work as expected
- Variable expansion `$VAR` may not work

## Solution

Wrap commands in `bash -c '...'` (or `sh -c '...'`) to ensure proper shell interpretation:

```python
# This WORKS - shell processes the command
subprocess.run([
    "tmux", "new-session", "-d", "-s", "my-session",
    "bash -c 'claude -p \"$(cat prompt.txt)\" > output.log 2>&1'"
])
```

## Implementation

Added two helper functions to `orchestrator/tmux.py`:

1. **`create_session_with_command(session, command, working_dir, shell)`**
   - Creates a new tmux session that runs a shell command
   - Automatically wraps command in `shell -c '...'`
   - Handles escaping of single quotes in the command

2. **`run_shell_command(session, window, command, shell)`**
   - Runs a shell command in an existing tmux window
   - Uses the same wrapping approach

## Testing

```python
from orchestrator.tmux import create_session_with_command

# Command substitution works
create_session_with_command(
    "my-session",
    'echo "$(date)" > /tmp/test.txt'
)

# Pipes work
create_session_with_command(
    "my-session",
    'cat file.txt | grep pattern > results.txt'
)
```

## Affected Projects

- `topic-scanner`: Claude prompt invocation via tmux was failing
- Any project using tmux to run commands with shell features

## Migration

Projects should update their tmux command invocations:

**Before (broken):**
```python
tmux_cmd = f'tmux new-session -d -s {session} "claude -p \\"$(cat {file})\\" > {log}"'
subprocess.run(tmux_cmd, shell=True)
```

**After (fixed):**
```python
from orchestrator.tmux import create_session_with_command

create_session_with_command(
    session,
    f'claude -p "$(cat {file})" > {log} 2>&1'
)
```

Or manually wrap:
```python
tmux_cmd = f'tmux new-session -d -s {session} "bash -c \'claude -p \\"\\$(cat {file})\\" > {log} 2>&1\'"'
```
