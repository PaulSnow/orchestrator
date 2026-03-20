# Orchestrator Web Dashboard API

Base URL: `http://localhost:8123` (configurable via `--web-port`)

All endpoints return JSON unless otherwise noted. CORS is enabled for all endpoints (`Access-Control-Allow-Origin: *`).

## Endpoints

### GET /

**Summary:** Serves the HTML dashboard UI
**Content-Type:** `text/html`
**Description:** Returns the single-page dashboard application with real-time updates via SSE.

---

### GET /api/events

**Summary:** Server-Sent Events stream for real-time updates
**Content-Type:** `text/event-stream`
**Description:** Opens a persistent connection that streams events as they occur.

**Event Types:**

| Event | Description |
|-------|-------------|
| `connected` | Initial connection established |
| `state` | Full state update (phase, project, stats) |
| `workers` | Worker list update |
| `progress` | Progress stats update |
| `phase_changed` | Orchestration phase changed |
| `worker_assigned` | Worker assigned to issue |
| `worker_completed` | Worker finished issue |
| `worker_failed` | Worker failed on issue |
| `worker_idle` | Worker has no work |
| `issue_status` | Issue status changed |
| `progress_update` | Progress percentage update |
| `log_update` | Worker log updated |
| `gate_result` | Review gate result available |
| `reviewing_issue` | Issue being reviewed |
| `issue_review` | Individual issue review complete |

**Example:**
```bash
curl -N http://localhost:8123/api/events
```

---

### GET /api/state

**Summary:** Get current orchestrator state
**Content-Type:** `application/json`

**Response:**
```json
{
  "phase": "implementing",
  "project": "my-project",
  "version": "1.0.0",
  "started_at": "2024-01-15T10:30:00Z",
  "elapsed_seconds": 125.5,
  "total_issues": 10,
  "completed": 3,
  "in_progress": 2,
  "pending": 4,
  "failed": 1,
  "active_workers": 2,
  "total_workers": 5
}
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current phase: `review`, `implementing`, `testing`, `completed`, `failed` |
| `project` | string | Project name from config |
| `version` | string | Orchestrator version |
| `started_at` | string | ISO 8601 timestamp of start time |
| `elapsed_seconds` | float | Seconds since start |
| `total_issues` | int | Total number of issues |
| `completed` | int | Number of completed issues |
| `in_progress` | int | Number of issues currently being worked |
| `pending` | int | Number of issues waiting to start |
| `failed` | int | Number of failed issues |
| `active_workers` | int | Number of workers currently running |
| `total_workers` | int | Total configured workers |

**Example:**
```bash
curl http://localhost:8123/api/state
```

---

### GET /api/workers

**Summary:** Get status of all workers
**Content-Type:** `application/json`

**Response:**
```json
[
  {
    "worker_id": 1,
    "status": "running",
    "stage": "implement",
    "retry_count": 0,
    "branch": "feature/issue-42",
    "worktree": "/tmp/worktrees/issue-42",
    "issue_number": 42,
    "issue_title": "Add authentication",
    "started_at": "2024-01-15T10:31:00Z",
    "elapsed_seconds": 95.2,
    "log_tail": "Running tests..."
  }
]
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `worker_id` | int | Worker identifier (1-N) |
| `status` | string | Status: `running`, `idle`, `completed`, `failed`, `unknown` |
| `stage` | string | Current pipeline stage |
| `retry_count` | int | Number of times worker has been restarted |
| `branch` | string | Git branch name |
| `worktree` | string | Path to git worktree |
| `issue_number` | int | Assigned issue number (null if idle) |
| `issue_title` | string | Title of assigned issue |
| `started_at` | string | ISO 8601 timestamp when work started |
| `elapsed_seconds` | float | Seconds since work started |
| `log_tail` | string | Last line of worker log (truncated) |

**Example:**
```bash
curl http://localhost:8123/api/workers
```

---

### GET /api/progress

**Summary:** Get completion progress stats
**Content-Type:** `application/json`

**Response:**
```json
{
  "total": 10,
  "completed": 3,
  "in_progress": 2,
  "pending": 4,
  "failed": 1,
  "percent_complete": 30.0
}
```

**Example:**
```bash
curl http://localhost:8123/api/progress
```

---

### GET /api/issues

**Summary:** Get all issues with their current status
**Content-Type:** `application/json`

**Response:**
```json
[
  {
    "number": 42,
    "title": "Add authentication",
    "status": "in_progress",
    "priority": 1,
    "wave": 1,
    "pipeline_stage": 0,
    "assigned_worker": 1,
    "depends_on": [41],
    "review": {
      "passed": true,
      "reasons": []
    }
  }
]
```

**Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `number` | int | Issue number |
| `title` | string | Issue title |
| `status` | string | Status: `pending`, `in_progress`, `completed`, `failed` |
| `priority` | int | Priority (1 = highest) |
| `wave` | int | Wave number for parallel execution |
| `pipeline_stage` | int | Current stage in pipeline |
| `assigned_worker` | int | Worker ID (null if not assigned) |
| `depends_on` | array | Issue numbers this depends on |
| `review` | object | Review result (present if reviewed) |

**Example:**
```bash
curl http://localhost:8123/api/issues
```

---

### GET /api/event-log

**Summary:** Get recent event history
**Content-Type:** `application/json`

**Response:**
```json
[
  {
    "type": "worker_assigned",
    "timestamp": "2024-01-15T10:31:00Z",
    "data": {"worker_id": 1, "issue_number": 42}
  }
]
```

Returns up to 100 most recent events, newest first.

**Example:**
```bash
curl http://localhost:8123/api/event-log
```

---

### GET /api/log/{worker_id}

**Summary:** Get worker log output
**Content-Type:** `text/plain`

**Parameters:**

| Parameter | Location | Type | Default | Description |
|-----------|----------|------|---------|-------------|
| `worker_id` | path | int | required | Worker ID (1-N) |
| `lines` | query | int | 100 | Number of lines to return |

**Response:** Plain text log output (last N lines)

**Example:**
```bash
curl "http://localhost:8123/api/log/1?lines=50"
```

---

### GET /api/status

**Summary:** Get basic status (legacy endpoint)
**Content-Type:** `application/json`

**Response:**
```json
{
  "project": "my-project",
  "timestamp": "2024-01-15T10:35:00Z",
  "issues": 10,
  "completed": 3,
  "pending": 4,
  "failed": 1,
  "num_workers": 5
}
```

**Example:**
```bash
curl http://localhost:8123/api/status
```

---

### GET /api/gate-result

**Summary:** Get review gate result
**Content-Type:** `application/json`

**Response:**
```json
{
  "passed": false,
  "summary": "3 of 10 issues failed review",
  "total_issues": 10,
  "passed_issues": 7,
  "failed_issues": 3,
  "skipped_issues": 0,
  "results": [
    {
      "issue_number": 42,
      "title": "Add feature",
      "passed": false,
      "reasons": ["Missing acceptance criteria"]
    }
  ]
}
```

**Example:**
```bash
curl http://localhost:8123/api/gate-result
```

---

## Common Usage Patterns

### Watch events in real-time
```bash
curl -N http://localhost:8123/api/events
```

### Get overall progress as formatted JSON
```bash
curl -s http://localhost:8123/api/progress | jq .
```

### Get failed issues only
```bash
curl -s http://localhost:8123/api/issues | jq '[.[] | select(.status == "failed")]'
```

### Get worker 1's recent log
```bash
curl "http://localhost:8123/api/log/1?lines=20"
```

### Check if orchestration is complete
```bash
curl -s http://localhost:8123/api/state | jq -r '.phase'
```

### Poll until complete (bash)
```bash
while [ "$(curl -s localhost:8123/api/state | jq -r .phase)" != "completed" ]; do
  sleep 10
done
echo "Done!"
```

### Get summary of all workers
```bash
curl -s http://localhost:8123/api/workers | jq '.[] | {id: .worker_id, status: .status, issue: .issue_number}'
```
