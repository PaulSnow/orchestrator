package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ConnectivityChecker periodically checks if orchestrators are reachable.
type ConnectivityChecker struct {
	mu              sync.RWMutex
	registry        *RegistryManager
	status          map[string]*ConnectivityState // keyed by project
	httpClient      *http.Client
	checkInterval   time.Duration
	requestTimeout  time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
}

// ConnectivityState tracks the connectivity state of an orchestrator.
type ConnectivityState struct {
	Project      string             `json:"project"`
	Port         int                `json:"port"`
	Status       ConnectivityStatus `json:"status"`
	LastSeen     time.Time          `json:"last_seen"`
	LastChecked  time.Time          `json:"last_checked"`
	LastError    string             `json:"last_error,omitempty"`
}

// NewConnectivityChecker creates a new connectivity checker.
func NewConnectivityChecker(registry *RegistryManager) *ConnectivityChecker {
	if registry == nil {
		registry = GetGlobalRegistry()
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &ConnectivityChecker{
		registry:       registry,
		status:         make(map[string]*ConnectivityState),
		checkInterval:  5 * time.Second, // Check every 5 seconds per requirement
		requestTimeout: 2 * time.Second, // Quick timeout for responsiveness
		httpClient: &http.Client{
			Timeout: 2 * time.Second,
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start begins the connectivity checking loop.
func (cc *ConnectivityChecker) Start() {
	cc.wg.Add(1)
	go cc.checkLoop()
}

// Stop stops the connectivity checker.
func (cc *ConnectivityChecker) Stop() {
	cc.cancel()
	cc.wg.Wait()
}

// checkLoop periodically checks all orchestrators.
func (cc *ConnectivityChecker) checkLoop() {
	defer cc.wg.Done()

	// Initial check
	cc.checkAll()

	ticker := time.NewTicker(cc.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cc.ctx.Done():
			return
		case <-ticker.C:
			cc.checkAll()
		}
	}
}

// checkAll checks the connectivity of all registered orchestrators.
func (cc *ConnectivityChecker) checkAll() {
	entries, err := cc.registry.ListOrchestrators()
	if err != nil {
		return
	}

	// Check all orchestrators concurrently
	var wg sync.WaitGroup
	for _, entry := range entries {
		wg.Add(1)
		go func(e OrchestratorEntry) {
			defer wg.Done()
			cc.checkOne(e)
		}(entry)
	}
	wg.Wait()

	// Clean up stale entries
	cc.cleanupStale(entries)
}

// checkOne checks the connectivity of a single orchestrator.
func (cc *ConnectivityChecker) checkOne(entry OrchestratorEntry) {
	cc.mu.Lock()
	state, exists := cc.status[entry.Project]
	if !exists {
		state = &ConnectivityState{
			Project: entry.Project,
			Port:    entry.Port,
			Status:  ConnectivityChecking,
		}
		cc.status[entry.Project] = state
	}
	state.Port = entry.Port // Update port in case it changed
	cc.mu.Unlock()

	// Ping the /api/status endpoint
	url := fmt.Sprintf("http://localhost:%d/api/status", entry.Port)
	ctx, cancel := context.WithTimeout(cc.ctx, cc.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cc.updateState(entry.Project, ConnectivityOffline, err.Error())
		return
	}

	resp, err := cc.httpClient.Do(req)
	if err != nil {
		cc.updateState(entry.Project, ConnectivityOffline, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cc.updateState(entry.Project, ConnectivityOffline, fmt.Sprintf("status %d", resp.StatusCode))
		return
	}

	// Verify it's valid JSON
	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		cc.updateState(entry.Project, ConnectivityOffline, "invalid response")
		return
	}

	cc.updateState(entry.Project, ConnectivityOnline, "")
}

// updateState updates the connectivity state for an orchestrator.
func (cc *ConnectivityChecker) updateState(project string, status ConnectivityStatus, lastError string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	state, exists := cc.status[project]
	if !exists {
		state = &ConnectivityState{Project: project}
		cc.status[project] = state
	}

	state.LastChecked = time.Now()
	state.LastError = lastError

	if status == ConnectivityOnline {
		state.LastSeen = time.Now()
	}

	state.Status = status
}

// cleanupStale removes state entries for orchestrators that are no longer registered.
func (cc *ConnectivityChecker) cleanupStale(entries []OrchestratorEntry) {
	cc.mu.Lock()
	defer cc.mu.Unlock()

	currentProjects := make(map[string]bool)
	for _, e := range entries {
		currentProjects[e.Project] = true
	}

	for project := range cc.status {
		if !currentProjects[project] {
			delete(cc.status, project)
		}
	}
}

// GetStatus returns the connectivity status for a specific orchestrator.
func (cc *ConnectivityChecker) GetStatus(project string) (ConnectivityStatus, time.Time) {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	if state, ok := cc.status[project]; ok {
		return state.Status, state.LastSeen
	}
	return ConnectivityChecking, time.Time{}
}

// GetAllStatus returns the connectivity status for all orchestrators.
func (cc *ConnectivityChecker) GetAllStatus() map[string]*ConnectivityState {
	cc.mu.RLock()
	defer cc.mu.RUnlock()

	result := make(map[string]*ConnectivityState)
	for k, v := range cc.status {
		copy := *v
		result[k] = &copy
	}
	return result
}

// ForceCheck immediately triggers a connectivity check for all orchestrators.
func (cc *ConnectivityChecker) ForceCheck() {
	cc.checkAll()
}

// global connectivity checker instance
var (
	globalConnChecker *ConnectivityChecker
	connCheckerOnce   sync.Once
)

// GetGlobalConnectivityChecker returns the global connectivity checker instance.
func GetGlobalConnectivityChecker() *ConnectivityChecker {
	connCheckerOnce.Do(func() {
		globalConnChecker = NewConnectivityChecker(GetGlobalRegistry())
	})
	return globalConnChecker
}

// StartGlobalConnectivityChecker starts the global connectivity checker.
func StartGlobalConnectivityChecker() {
	GetGlobalConnectivityChecker().Start()
}

// StopGlobalConnectivityChecker stops the global connectivity checker.
func StopGlobalConnectivityChecker() {
	if globalConnChecker != nil {
		globalConnChecker.Stop()
	}
}
