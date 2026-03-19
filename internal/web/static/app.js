// Orchestrator Dashboard - SSE Client and UI Rendering

(function() {
    'use strict';

    // State
    let state = {
        issues: [],
        sessions: [],
        events: [],
        connected: false,
        orchestratorStatus: 'disconnected'
    };

    // DOM Elements
    const elements = {
        connectionStatus: document.getElementById('connection-status'),
        portDisplay: document.getElementById('port-display'),
        abortBtn: document.getElementById('abort-btn'),
        issuesBody: document.getElementById('issues-body'),
        sessionsList: document.getElementById('sessions-list'),
        eventLog: document.getElementById('event-log'),
        confirmModal: document.getElementById('confirm-modal'),
        confirmCancel: document.getElementById('confirm-cancel'),
        confirmAbort: document.getElementById('confirm-abort')
    };

    // SSE Connection
    let eventSource = null;
    let reconnectTimeout = null;
    const MAX_EVENTS = 100;

    function connect() {
        if (eventSource) {
            eventSource.close();
        }

        const protocol = window.location.protocol === 'https:' ? 'https:' : 'http:';
        const sseUrl = `${protocol}//${window.location.host}/api/events`;

        elements.portDisplay.textContent = window.location.host;

        eventSource = new EventSource(sseUrl);

        eventSource.onopen = function() {
            state.connected = true;
            updateConnectionStatus('connected');
            clearTimeout(reconnectTimeout);
        };

        eventSource.onerror = function() {
            state.connected = false;
            updateConnectionStatus('disconnected');
            eventSource.close();
            scheduleReconnect();
        };

        eventSource.onmessage = function(event) {
            try {
                const data = JSON.parse(event.data);
                handleEvent(data);
            } catch (e) {
                console.error('Failed to parse SSE event:', e);
            }
        };

        // Named event handlers
        eventSource.addEventListener('state', function(event) {
            try {
                const data = JSON.parse(event.data);
                handleStateUpdate(data);
            } catch (e) {
                console.error('Failed to parse state event:', e);
            }
        });

        eventSource.addEventListener('issue', function(event) {
            try {
                const data = JSON.parse(event.data);
                handleIssueUpdate(data);
            } catch (e) {
                console.error('Failed to parse issue event:', e);
            }
        });

        eventSource.addEventListener('worker', function(event) {
            try {
                const data = JSON.parse(event.data);
                handleWorkerUpdate(data);
            } catch (e) {
                console.error('Failed to parse worker event:', e);
            }
        });

        eventSource.addEventListener('log', function(event) {
            try {
                const data = JSON.parse(event.data);
                addEvent(data);
            } catch (e) {
                console.error('Failed to parse log event:', e);
            }
        });
    }

    function scheduleReconnect() {
        clearTimeout(reconnectTimeout);
        reconnectTimeout = setTimeout(function() {
            connect();
        }, 3000);
    }

    function updateConnectionStatus(status) {
        elements.connectionStatus.className = 'status-indicator ' + status;
        elements.connectionStatus.textContent = status === 'connected' ? 'Connected' : 'Disconnected';
    }

    // Event Handlers
    function handleEvent(data) {
        if (data.type === 'state') {
            handleStateUpdate(data);
        } else if (data.type === 'issue') {
            handleIssueUpdate(data);
        } else if (data.type === 'worker') {
            handleWorkerUpdate(data);
        } else if (data.type === 'log') {
            addEvent(data);
        } else {
            addEvent(data);
        }
    }

    function handleStateUpdate(data) {
        if (data.status) {
            state.orchestratorStatus = data.status;
        }
        if (data.issues) {
            state.issues = data.issues;
            renderIssues();
        }
        if (data.workers) {
            state.sessions = data.workers;
            renderSessions();
        }
    }

    function handleIssueUpdate(data) {
        const index = state.issues.findIndex(function(i) { return i.number === data.number; });
        if (index >= 0) {
            state.issues[index] = Object.assign({}, state.issues[index], data);
        } else {
            state.issues.push(data);
        }
        renderIssues();
    }

    function handleWorkerUpdate(data) {
        const index = state.sessions.findIndex(function(s) { return s.worker_id === data.worker_id; });
        if (index >= 0) {
            state.sessions[index] = Object.assign({}, state.sessions[index], data);
        } else {
            state.sessions.push(data);
        }
        renderSessions();
    }

    function addEvent(data) {
        const event = {
            timestamp: data.timestamp || new Date().toISOString(),
            type: data.action || data.type || 'info',
            message: formatEventMessage(data)
        };
        state.events.push(event);
        if (state.events.length > MAX_EVENTS) {
            state.events.shift();
        }
        renderEvents();
    }

    function formatEventMessage(data) {
        if (data.message) {
            return data.message;
        }

        const action = data.action || data.type || 'event';
        const parts = [action];

        if (data.worker !== undefined) {
            parts.push('worker ' + data.worker);
        }
        if (data.issue !== undefined) {
            parts.push('issue #' + data.issue);
        }
        if (data.new_issue !== undefined) {
            parts.push('-> issue #' + data.new_issue);
        }
        if (data.stage) {
            parts.push('stage: ' + data.stage);
        }
        if (data.reason) {
            parts.push('(' + data.reason + ')');
        }

        return parts.join(' ');
    }

    // Rendering Functions
    function renderIssues() {
        if (state.issues.length === 0) {
            elements.issuesBody.innerHTML = '<tr class="empty-row"><td colspan="6">No issues loaded</td></tr>';
            return;
        }

        elements.issuesBody.innerHTML = state.issues.map(function(issue) {
            const deps = issue.depends_on && issue.depends_on.length > 0
                ? issue.depends_on.map(function(d) { return '#' + d; }).join(', ')
                : 'None';

            const completeness = formatScore(issue.completeness_score, issue.completeness_status);
            const suitability = formatScore(issue.suitability_score, issue.suitability_status);

            return '<tr>' +
                '<td>' + issue.number + '</td>' +
                '<td>' + escapeHtml(issue.title || 'Untitled') + '</td>' +
                '<td>' + completeness + '</td>' +
                '<td>' + suitability + '</td>' +
                '<td>' + deps + '</td>' +
                '<td><span class="status-badge ' + (issue.status || 'pending') + '">' +
                    escapeHtml(issue.status || 'pending') + '</span></td>' +
            '</tr>';
        }).join('');
    }

    function formatScore(score, status) {
        if (status === 'reviewing') {
            return '<span class="status-badge reviewing">Reviewing...</span>';
        }
        if (score === undefined || score === null) {
            return '-';
        }
        const className = score >= 7 ? 'good' : (score >= 4 ? 'warning' : 'bad');
        const statusText = status ? ' (' + status + ')' : '';
        return '<span class="score ' + className + '">' + score + '/10' + statusText + '</span>';
    }

    function renderSessions() {
        if (state.sessions.length === 0) {
            elements.sessionsList.innerHTML = '<div class="empty-message">No active sessions</div>';
            return;
        }

        elements.sessionsList.innerHTML = state.sessions.map(function(session) {
            const name = 'worker-' + session.worker_id;
            const status = session.status || 'idle';
            const elapsed = formatElapsed(session.started_at);
            const issue = session.issue_number ? '#' + session.issue_number : 'idle';

            return '<div class="session-item" data-worker="' + session.worker_id + '">' +
                '<div>' +
                    '<span class="session-name">' + name + '</span>' +
                    '<span style="margin-left: 0.5rem; color: var(--text-secondary);">' + issue + '</span>' +
                '</div>' +
                '<div>' +
                    '<span class="session-time">' + elapsed + '</span>' +
                    '<span class="session-status ' + status + '" style="margin-left: 0.5rem;">' + status + '</span>' +
                '</div>' +
            '</div>';
        }).join('');

        // Add click handlers
        elements.sessionsList.querySelectorAll('.session-item').forEach(function(item) {
            item.addEventListener('click', function() {
                const workerId = this.getAttribute('data-worker');
                showWorkerOutput(workerId);
            });
        });
    }

    function renderEvents() {
        if (state.events.length === 0) {
            elements.eventLog.innerHTML = '<div class="empty-message">No events yet</div>';
            return;
        }

        elements.eventLog.innerHTML = state.events.map(function(event) {
            const time = formatTime(event.timestamp);
            const typeClass = getEventTypeClass(event.type);

            return '<div class="event-entry">' +
                '<span class="event-time">' + time + '</span>' +
                '<span class="event-type ' + typeClass + '">' + escapeHtml(event.type) + '</span>' +
                '<span class="event-message">' + escapeHtml(event.message) + '</span>' +
            '</div>';
        }).join('');

        // Auto-scroll to bottom
        elements.eventLog.scrollTop = elements.eventLog.scrollHeight;
    }

    function formatTime(timestamp) {
        if (!timestamp) {
            return '--:--:--';
        }
        try {
            const date = new Date(timestamp);
            return date.toLocaleTimeString('en-US', { hour12: false });
        } catch (e) {
            return '--:--:--';
        }
    }

    function formatElapsed(startedAt) {
        if (!startedAt) {
            return '--';
        }
        try {
            const start = new Date(startedAt);
            const now = new Date();
            const seconds = Math.floor((now - start) / 1000);

            if (seconds < 60) {
                return seconds + 's';
            }
            if (seconds < 3600) {
                return Math.floor(seconds / 60) + 'm';
            }
            const hours = Math.floor(seconds / 3600);
            const mins = Math.floor((seconds % 3600) / 60);
            return hours + 'h ' + mins + 'm';
        } catch (e) {
            return '--';
        }
    }

    function getEventTypeClass(type) {
        const successTypes = ['mark_complete', 'completed', 'advance_stage', 'push'];
        const warningTypes = ['restart', 'reassign', 'defer', 'retry'];
        const errorTypes = ['skip', 'failed', 'error', 'shutdown'];

        if (successTypes.some(function(t) { return type.includes(t); })) {
            return 'success';
        }
        if (warningTypes.some(function(t) { return type.includes(t); })) {
            return 'warning';
        }
        if (errorTypes.some(function(t) { return type.includes(t); })) {
            return 'error';
        }
        return 'info';
    }

    function escapeHtml(text) {
        if (typeof text !== 'string') {
            return text;
        }
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    function showWorkerOutput(workerId) {
        // Fetch recent output for worker
        fetch('/api/worker/' + workerId + '/output')
            .then(function(response) { return response.json(); })
            .then(function(data) {
                if (data.output) {
                    addEvent({
                        type: 'info',
                        message: 'Worker ' + workerId + ' output: ' + data.output.substring(0, 200)
                    });
                }
            })
            .catch(function(e) {
                console.error('Failed to fetch worker output:', e);
            });
    }

    // Abort Functionality
    function showAbortModal() {
        elements.confirmModal.classList.remove('hidden');
    }

    function hideAbortModal() {
        elements.confirmModal.classList.add('hidden');
    }

    function sendAbort() {
        fetch('/api/abort', { method: 'POST' })
            .then(function(response) {
                if (response.ok) {
                    addEvent({
                        type: 'warning',
                        message: 'Abort signal sent to orchestrator'
                    });
                } else {
                    addEvent({
                        type: 'error',
                        message: 'Failed to send abort signal'
                    });
                }
            })
            .catch(function(e) {
                addEvent({
                    type: 'error',
                    message: 'Failed to send abort signal: ' + e.message
                });
            });
        hideAbortModal();
    }

    // Event Listeners
    elements.abortBtn.addEventListener('click', showAbortModal);
    elements.confirmCancel.addEventListener('click', hideAbortModal);
    elements.confirmAbort.addEventListener('click', sendAbort);

    // Close modal on background click
    elements.confirmModal.addEventListener('click', function(e) {
        if (e.target === elements.confirmModal) {
            hideAbortModal();
        }
    });

    // Keyboard shortcuts
    document.addEventListener('keydown', function(e) {
        if (e.key === 'Escape' && !elements.confirmModal.classList.contains('hidden')) {
            hideAbortModal();
        }
    });

    // Initialize
    function init() {
        connect();
        renderIssues();
        renderSessions();
        renderEvents();
    }

    // Start when DOM is ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
