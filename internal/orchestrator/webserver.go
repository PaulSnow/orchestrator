package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// WebServer provides HTTP endpoints for the review gate dashboard.
type WebServer struct {
	reviewGate *ReviewGate
	cfg        *RunConfig
	state      *StateManager
	server     *http.Server
	port       int
	mu         sync.Mutex
}

// NewWebServer creates a new web server instance.
func NewWebServer(rg *ReviewGate, cfg *RunConfig, state *StateManager, port int) *WebServer {
	if port == 0 {
		port = 8080
	}
	return &WebServer{
		reviewGate: rg,
		cfg:        cfg,
		state:      state,
		port:       port,
	}
}

// Start starts the web server in a background goroutine.
func (ws *WebServer) Start() error {
	mux := http.NewServeMux()

	// SSE endpoint for real-time updates
	mux.HandleFunc("/api/events", ws.handleSSE)

	// Status endpoint
	mux.HandleFunc("/api/status", ws.handleStatus)

	// Gate result endpoint
	mux.HandleFunc("/api/gate-result", ws.handleGateResult)

	// Issue results endpoint
	mux.HandleFunc("/api/issues", ws.handleIssues)

	// Dashboard HTML
	mux.HandleFunc("/", ws.handleDashboard)

	ws.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", ws.port),
		Handler: mux,
	}

	go func() {
		LogMsg(fmt.Sprintf("Web server starting on http://localhost:%d", ws.port))
		if err := ws.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			LogMsg(fmt.Sprintf("Web server error: %v", err))
		}
	}()

	return nil
}

// Stop gracefully shuts down the web server.
func (ws *WebServer) Stop() error {
	if ws.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ws.server.Shutdown(ctx)
}

// handleSSE handles Server-Sent Events connections.
func (ws *WebServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create client channel
	clientCh := make(chan SSEEvent, 10)
	ws.reviewGate.AddSSEClient(clientCh)
	defer func() {
		ws.reviewGate.RemoveSSEClient(clientCh)
		close(clientCh)
	}()

	// Get flush support
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Send initial connection event
	fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	// Send current state
	if result, err := ws.reviewGate.LoadGateResult(); err == nil {
		data, _ := json.Marshal(result)
		fmt.Fprintf(w, "event: gate_result\ndata: %s\n\n", string(data))
		flusher.Flush()
	}

	// Listen for events
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-clientCh:
			if !ok {
				return
			}
			data, err := json.Marshal(event.Data)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, string(data))
			flusher.Flush()
		}
	}
}

// handleStatus returns the current orchestrator status.
func (ws *WebServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	status := map[string]any{
		"project":    ws.cfg.Project,
		"timestamp":  NowISO(),
		"issues":     len(ws.cfg.Issues),
		"completed":  GetCompletedCount(ws.cfg),
		"pending":    GetPendingCount(ws.cfg),
		"failed":     GetFailedCount(ws.cfg),
		"num_workers": ws.cfg.NumWorkers,
	}

	json.NewEncoder(w).Encode(status)
}

// handleGateResult returns the gate result.
func (ws *WebServer) handleGateResult(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	result, err := ws.reviewGate.LoadGateResult()
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{
			"error": "no gate result available",
		})
		return
	}

	json.NewEncoder(w).Encode(result)
}

// handleIssues returns all issues with their review status.
func (ws *WebServer) handleIssues(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	issues := make([]map[string]any, 0, len(ws.cfg.Issues))
	for _, issue := range ws.cfg.Issues {
		issueData := map[string]any{
			"number":   issue.Number,
			"title":    issue.Title,
			"status":   issue.Status,
			"priority": issue.Priority,
			"wave":     issue.Wave,
		}

		// Try to load review result
		if review, err := ws.reviewGate.LoadIssueReview(issue.Number); err == nil {
			issueData["review"] = review
		}

		issues = append(issues, issueData)
	}

	json.NewEncoder(w).Encode(issues)
}

// handleDashboard serves the HTML dashboard.
func (ws *WebServer) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")

	html := `<!DOCTYPE html>
<html>
<head>
    <title>Orchestrator Review Gate Dashboard</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
            max-width: 1200px;
            margin: 0 auto;
            padding: 20px;
            background: #1a1a2e;
            color: #eee;
        }
        h1 { color: #00d9ff; }
        .status-bar {
            display: flex;
            gap: 20px;
            margin-bottom: 20px;
            padding: 15px;
            background: #16213e;
            border-radius: 8px;
        }
        .status-item {
            text-align: center;
        }
        .status-item .value {
            font-size: 24px;
            font-weight: bold;
        }
        .status-item .label {
            font-size: 12px;
            color: #888;
        }
        .passed { color: #00ff88; }
        .failed { color: #ff4444; }
        .pending { color: #ffaa00; }
        .gate-result {
            padding: 20px;
            border-radius: 8px;
            margin-bottom: 20px;
        }
        .gate-passed { background: #0a3d1a; border: 2px solid #00ff88; }
        .gate-failed { background: #3d0a0a; border: 2px solid #ff4444; }
        .gate-pending { background: #3d3d0a; border: 2px solid #ffaa00; }
        .issue-list {
            display: grid;
            gap: 10px;
        }
        .issue-card {
            padding: 15px;
            background: #16213e;
            border-radius: 8px;
            border-left: 4px solid #444;
        }
        .issue-card.passed { border-left-color: #00ff88; }
        .issue-card.failed { border-left-color: #ff4444; }
        .issue-card.reviewing { border-left-color: #00d9ff; animation: pulse 1s infinite; }
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.5; }
        }
        .issue-header {
            display: flex;
            justify-content: space-between;
            margin-bottom: 8px;
        }
        .issue-number { font-weight: bold; color: #00d9ff; }
        .issue-title { color: #ccc; }
        .reasons {
            font-size: 12px;
            color: #ff8888;
            margin-top: 8px;
        }
        .log {
            background: #0a0a0a;
            padding: 10px;
            border-radius: 4px;
            font-family: monospace;
            font-size: 12px;
            max-height: 200px;
            overflow-y: auto;
            margin-top: 20px;
        }
    </style>
</head>
<body>
    <h1>Orchestrator Review Gate</h1>

    <div class="status-bar" id="status-bar">
        <div class="status-item">
            <div class="value" id="total-issues">-</div>
            <div class="label">Total</div>
        </div>
        <div class="status-item">
            <div class="value passed" id="passed-issues">-</div>
            <div class="label">Passed</div>
        </div>
        <div class="status-item">
            <div class="value failed" id="failed-issues">-</div>
            <div class="label">Failed</div>
        </div>
        <div class="status-item">
            <div class="value pending" id="pending-issues">-</div>
            <div class="label">Pending</div>
        </div>
    </div>

    <div class="gate-result gate-pending" id="gate-result">
        <h2>Gate Status: <span id="gate-status">Pending</span></h2>
        <p id="gate-summary">Waiting for review to complete...</p>
    </div>

    <h2>Issues</h2>
    <div class="issue-list" id="issue-list">
        Loading...
    </div>

    <div class="log" id="event-log">
        Connecting to event stream...
    </div>

    <script>
        const issues = {};
        let gateResult = null;

        function log(msg) {
            const logEl = document.getElementById('event-log');
            const time = new Date().toLocaleTimeString();
            logEl.innerHTML = ` + "`[${time}] ${msg}\n`" + ` + logEl.innerHTML;
            if (logEl.innerHTML.length > 10000) {
                logEl.innerHTML = logEl.innerHTML.substring(0, 10000);
            }
        }

        function updateUI() {
            const listEl = document.getElementById('issue-list');

            const issueArray = Object.values(issues).sort((a, b) => a.number - b.number);

            if (issueArray.length === 0) {
                listEl.innerHTML = '<p>No issues loaded yet</p>';
                return;
            }

            listEl.innerHTML = issueArray.map(issue => {
                let statusClass = '';
                if (issue.reviewing) statusClass = 'reviewing';
                else if (issue.review) statusClass = issue.review.passed ? 'passed' : 'failed';

                let reasonsHtml = '';
                if (issue.review && issue.review.reasons && issue.review.reasons.length > 0) {
                    reasonsHtml = '<div class="reasons">' + issue.review.reasons.map(r => '- ' + r).join('<br>') + '</div>';
                }

                return ` + "`" + `
                    <div class="issue-card ${statusClass}">
                        <div class="issue-header">
                            <span class="issue-number">#${issue.number}</span>
                            <span>${issue.status || ''}</span>
                        </div>
                        <div class="issue-title">${issue.title || ''}</div>
                        ${reasonsHtml}
                    </div>
                ` + "`" + `;
            }).join('');
        }

        function updateGateResult(result) {
            gateResult = result;
            const el = document.getElementById('gate-result');
            const statusEl = document.getElementById('gate-status');
            const summaryEl = document.getElementById('gate-summary');

            el.className = 'gate-result ' + (result.passed ? 'gate-passed' : 'gate-failed');
            statusEl.textContent = result.passed ? 'PASSED' : 'FAILED';
            summaryEl.textContent = result.summary || '';

            document.getElementById('total-issues').textContent = result.total_issues;
            document.getElementById('passed-issues').textContent = result.passed_issues;
            document.getElementById('failed-issues').textContent = result.failed_issues;
            document.getElementById('pending-issues').textContent = result.skipped_issues;
        }

        // Load initial issues
        fetch('/api/issues')
            .then(r => r.json())
            .then(data => {
                data.forEach(issue => {
                    issues[issue.number] = issue;
                });
                updateUI();
            });

        // Load initial gate result
        fetch('/api/gate-result')
            .then(r => r.json())
            .then(data => {
                if (data && !data.error) {
                    updateGateResult(data);
                }
            });

        // Connect to SSE
        const evtSource = new EventSource('/api/events');

        evtSource.addEventListener('connected', (e) => {
            log('Connected to event stream');
        });

        evtSource.addEventListener('reviewing_issue', (e) => {
            const data = JSON.parse(e.data);
            log('Reviewing issue #' + data.issue_number);
            if (!issues[data.issue_number]) {
                issues[data.issue_number] = { number: data.issue_number, title: data.title };
            }
            issues[data.issue_number].reviewing = true;
            updateUI();
        });

        evtSource.addEventListener('issue_review', (e) => {
            const data = JSON.parse(e.data);
            log('Completed review of #' + data.issue_number + ': ' + (data.passed ? 'PASSED' : 'FAILED'));
            if (!issues[data.issue_number]) {
                issues[data.issue_number] = { number: data.issue_number };
            }
            issues[data.issue_number].reviewing = false;
            issues[data.issue_number].review = data;
            issues[data.issue_number].title = data.title;
            updateUI();
        });

        evtSource.addEventListener('gate_result', (e) => {
            const data = JSON.parse(e.data);
            log('Gate decision: ' + (data.passed ? 'PASSED' : 'FAILED'));
            updateGateResult(data);
        });

        evtSource.onerror = (e) => {
            log('Connection error - will reconnect...');
        };
    </script>
</body>
</html>`

	fmt.Fprint(w, html)
}
