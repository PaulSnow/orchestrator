package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"
)

// DefaultPort is the default port for the daemon server.
const DefaultPort = 8200

// Status represents the status of a registered orchestrator.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Registration represents an orchestrator registration request.
type Registration struct {
	Project string `json:"project"`
	Port    int    `json:"port"`
	PID     int    `json:"pid"`
	Workers int    `json:"workers"`
	Issues  int    `json:"issues"`
}

// OrchestratorEntry represents a registered orchestrator.
type OrchestratorEntry struct {
	Project    string    `json:"project"`
	Port       int       `json:"port"`
	PID        int       `json:"pid"`
	Workers    int       `json:"workers"`
	Issues     int       `json:"issues"`
	Status     Status    `json:"status"`
	StartTime  time.Time `json:"start_time"`
	LastUpdate time.Time `json:"last_update"`
}

// Daemon is the orchestrator registration daemon.
type Daemon struct {
	mu            sync.RWMutex
	orchestrators map[string]*OrchestratorEntry // keyed by project
	server        *http.Server
	port          int
}

// New creates a new daemon instance.
func New(port int) *Daemon {
	if port == 0 {
		port = DefaultPort
	}
	return &Daemon{
		orchestrators: make(map[string]*OrchestratorEntry),
		port:          port,
	}
}

// Start starts the daemon HTTP server.
func (d *Daemon) Start() error {
	mux := http.NewServeMux()

	mux.HandleFunc("/register", d.handleRegister)
	mux.HandleFunc("/deregister", d.handleDeregister)
	mux.HandleFunc("/orchestrators", d.handleList)
	mux.HandleFunc("/health", d.handleHealth)

	d.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", d.port),
		Handler: mux,
	}

	fmt.Printf("Daemon listening on port %d\n", d.port)
	return d.server.ListenAndServe()
}

// Stop gracefully shuts down the daemon.
func (d *Daemon) Stop() error {
	if d.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.server.Shutdown(ctx)
}

// handleRegister handles POST /register
func (d *Daemon) handleRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "method not allowed",
		})
		return
	}

	var reg Registration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid JSON: " + err.Error(),
		})
		return
	}

	// Validate required fields
	if reg.Project == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "project is required",
		})
		return
	}
	if reg.Port == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "port is required",
		})
		return
	}
	if reg.PID == 0 {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "pid is required",
		})
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Check for duplicate - reject if another orchestrator is running on same project
	if existing, ok := d.orchestrators[reg.Project]; ok {
		// Check if the existing process is still alive
		if isProcessRunning(existing.PID) && existing.PID != reg.PID {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("another orchestrator (PID %d) is already running on project %q", existing.PID, reg.Project),
			})
			return
		}
		// Existing process is dead, allow re-registration
	}

	// Register the orchestrator
	entry := &OrchestratorEntry{
		Project:    reg.Project,
		Port:       reg.Port,
		PID:        reg.PID,
		Workers:    reg.Workers,
		Issues:     reg.Issues,
		Status:     StatusRunning,
		StartTime:  time.Now(),
		LastUpdate: time.Now(),
	}
	d.orchestrators[reg.Project] = entry

	fmt.Printf("Registered orchestrator: %s (PID %d, port %d)\n", reg.Project, reg.PID, reg.Port)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "registered successfully",
		"entry":   entry,
	})
}

// handleDeregister handles POST /deregister
func (d *Daemon) handleDeregister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "method not allowed",
		})
		return
	}

	var req struct {
		Project string `json:"project"`
		PID     int    `json:"pid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid JSON: " + err.Error(),
		})
		return
	}

	if req.Project == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "project is required",
		})
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	entry, ok := d.orchestrators[req.Project]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "orchestrator not found",
		})
		return
	}

	// Only allow deregistration by the same PID or if the process is dead
	if req.PID != 0 && entry.PID != req.PID && isProcessRunning(entry.PID) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "can only deregister own orchestrator",
		})
		return
	}

	delete(d.orchestrators, req.Project)
	fmt.Printf("Deregistered orchestrator: %s (PID %d)\n", req.Project, entry.PID)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"message": "deregistered successfully",
	})
}

// handleList handles GET /orchestrators
func (d *Daemon) handleList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "method not allowed",
		})
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// Clean up dead orchestrators
	var toDelete []string
	for project, entry := range d.orchestrators {
		if !isProcessRunning(entry.PID) {
			toDelete = append(toDelete, project)
		}
	}
	for _, project := range toDelete {
		delete(d.orchestrators, project)
	}

	// Build response
	entries := make([]*OrchestratorEntry, 0, len(d.orchestrators))
	for _, entry := range d.orchestrators {
		entries = append(entries, entry)
	}

	json.NewEncoder(w).Encode(map[string]any{
		"orchestrators": entries,
		"count":         len(entries),
	})
}

// handleHealth handles GET /health
func (d *Daemon) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "healthy",
		"uptime": time.Since(time.Now()).String(),
	})
}

// isProcessRunning checks if a process with the given PID is running.
func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// On Unix, FindProcess always succeeds, so we need to send signal 0
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// CleanupStale removes entries for dead processes.
func (d *Daemon) CleanupStale() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	var cleaned int
	var toDelete []string
	for project, entry := range d.orchestrators {
		if !isProcessRunning(entry.PID) {
			toDelete = append(toDelete, project)
			cleaned++
		}
	}
	for _, project := range toDelete {
		fmt.Printf("Cleaning up stale orchestrator: %s\n", project)
		delete(d.orchestrators, project)
	}
	return cleaned
}

// GetOrchestrators returns a copy of all registered orchestrators.
func (d *Daemon) GetOrchestrators() []*OrchestratorEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	entries := make([]*OrchestratorEntry, 0, len(d.orchestrators))
	for _, entry := range d.orchestrators {
		// Make a copy
		e := *entry
		entries = append(entries, &e)
	}
	return entries
}

// GetByProject returns the orchestrator entry for a project.
func (d *Daemon) GetByProject(project string) *OrchestratorEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if entry, ok := d.orchestrators[project]; ok {
		e := *entry
		return &e
	}
	return nil
}
