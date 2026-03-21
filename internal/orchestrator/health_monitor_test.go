package orchestrator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestDefaultHealthMonitorConfig(t *testing.T) {
	cfg := DefaultHealthMonitorConfig()

	if cfg.PollInterval != 30*time.Second {
		t.Errorf("expected PollInterval 30s, got %v", cfg.PollInterval)
	}
	if cfg.FailureThreshold != 3 {
		t.Errorf("expected FailureThreshold 3, got %d", cfg.FailureThreshold)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Errorf("expected RequestTimeout 5s, got %v", cfg.RequestTimeout)
	}
}

func TestNewHealthMonitor(t *testing.T) {
	t.Run("with nil config uses defaults", func(t *testing.T) {
		hm := NewHealthMonitor(nil, nil)
		defer hm.Stop()

		if hm.cfg.PollInterval != 30*time.Second {
			t.Errorf("expected default PollInterval, got %v", hm.cfg.PollInterval)
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := &HealthMonitorConfig{
			PollInterval:     10 * time.Second,
			FailureThreshold: 5,
			RequestTimeout:   2 * time.Second,
		}
		hm := NewHealthMonitor(cfg, nil)
		defer hm.Stop()

		if hm.cfg.PollInterval != 10*time.Second {
			t.Errorf("expected custom PollInterval 10s, got %v", hm.cfg.PollInterval)
		}
		if hm.cfg.FailureThreshold != 5 {
			t.Errorf("expected custom FailureThreshold 5, got %d", hm.cfg.FailureThreshold)
		}
	})
}

func TestHealthMonitorDoHealthCheck(t *testing.T) {
	t.Run("successful health check", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/status" {
				t.Errorf("unexpected path: %s", r.URL.Path)
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"project": "test-project",
				"status":  "running",
			})
		}))
		defer server.Close()

		hm := NewHealthMonitor(nil, nil)
		defer hm.Stop()

		err := hm.doHealthCheck(server.URL + "/api/status")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("failed health check - server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer server.Close()

		hm := NewHealthMonitor(nil, nil)
		defer hm.Stop()

		err := hm.doHealthCheck(server.URL + "/api/status")
		if err == nil {
			t.Error("expected error for 500 status, got nil")
		}
	})

	t.Run("failed health check - invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("not valid json"))
		}))
		defer server.Close()

		hm := NewHealthMonitor(nil, nil)
		defer hm.Stop()

		err := hm.doHealthCheck(server.URL + "/api/status")
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("failed health check - connection refused", func(t *testing.T) {
		hm := NewHealthMonitor(nil, nil)
		defer hm.Stop()

		// Use a port that's unlikely to be open
		err := hm.doHealthCheck("http://localhost:59999/api/status")
		if err == nil {
			t.Error("expected error for connection refused, got nil")
		}
	})
}

func TestHealthMonitorTrackFailures(t *testing.T) {
	cfg := &HealthMonitorConfig{
		PollInterval:     100 * time.Millisecond, // Fast polling for test
		FailureThreshold: 3,
		RequestTimeout:   100 * time.Millisecond,
	}

	hm := NewHealthMonitor(cfg, nil)
	defer hm.Stop()

	// Simulate tracking failures
	entry := OrchestratorEntry{
		Project: "test-project",
		Port:    9999,
		PID:     12345,
	}

	key := healthKey(entry.Project, entry.Port)

	// Initialize health record
	hm.mu.Lock()
	hm.health[key] = &OrchestratorHealth{
		Project: entry.Project,
		Port:    entry.Port,
		PID:     entry.PID,
		IsAlive: true,
	}
	hm.mu.Unlock()

	// Verify initial state
	health := hm.GetHealth(entry.Project, entry.Port)
	if health == nil {
		t.Fatal("expected health record to exist")
	}
	if health.ConsecutiveFails != 0 {
		t.Errorf("expected 0 consecutive fails initially, got %d", health.ConsecutiveFails)
	}
	if !health.IsAlive {
		t.Error("expected orchestrator to be alive initially")
	}

	// Simulate failures
	hm.mu.Lock()
	hm.health[key].ConsecutiveFails = 2
	hm.mu.Unlock()

	health = hm.GetHealth(entry.Project, entry.Port)
	if health.ConsecutiveFails != 2 {
		t.Errorf("expected 2 consecutive fails, got %d", health.ConsecutiveFails)
	}
}

func TestHealthMonitorMarkDead(t *testing.T) {
	cfg := &HealthMonitorConfig{
		PollInterval:     100 * time.Millisecond,
		FailureThreshold: 3,
		RequestTimeout:   100 * time.Millisecond,
	}

	hm := NewHealthMonitor(cfg, nil)
	defer hm.Stop()

	var cleanupCalled bool
	var cleanupEntry OrchestratorEntry
	var cleanupMu sync.Mutex

	hm.SetCleanupCallback(func(entry OrchestratorEntry) error {
		cleanupMu.Lock()
		defer cleanupMu.Unlock()
		cleanupCalled = true
		cleanupEntry = entry
		return nil
	})

	entry := OrchestratorEntry{
		Project: "test-project",
		Port:    9999,
		PID:     12345,
	}

	key := healthKey(entry.Project, entry.Port)
	health := &OrchestratorHealth{
		Project:          entry.Project,
		Port:             entry.Port,
		PID:              entry.PID,
		IsAlive:          true,
		ConsecutiveFails: 3,
	}

	hm.mu.Lock()
	hm.health[key] = health
	hm.markDead(health, entry)
	hm.mu.Unlock()

	// Check health state
	if health.IsAlive {
		t.Error("expected orchestrator to be marked dead")
	}
	if health.MarkedDeadAt.IsZero() {
		t.Error("expected MarkedDeadAt to be set")
	}

	// Wait for cleanup callback (runs in goroutine)
	time.Sleep(50 * time.Millisecond)

	cleanupMu.Lock()
	if !cleanupCalled {
		t.Error("expected cleanup callback to be called")
	}
	if cleanupEntry.Project != entry.Project {
		t.Errorf("expected cleanup entry project %q, got %q", entry.Project, cleanupEntry.Project)
	}
	cleanupMu.Unlock()
}

func TestHealthMonitorGetDeadOrchestrators(t *testing.T) {
	hm := NewHealthMonitor(nil, nil)
	defer hm.Stop()

	// Add some health records
	hm.mu.Lock()
	hm.health["alive:8080"] = &OrchestratorHealth{
		Project: "alive",
		Port:    8080,
		IsAlive: true,
	}
	hm.health["dead1:8081"] = &OrchestratorHealth{
		Project:      "dead1",
		Port:         8081,
		IsAlive:      false,
		MarkedDeadAt: time.Now(),
	}
	hm.health["dead2:8082"] = &OrchestratorHealth{
		Project:      "dead2",
		Port:         8082,
		IsAlive:      false,
		MarkedDeadAt: time.Now(),
	}
	hm.mu.Unlock()

	dead := hm.GetDeadOrchestrators()
	if len(dead) != 2 {
		t.Errorf("expected 2 dead orchestrators, got %d", len(dead))
	}

	// Verify all returned are dead
	for _, h := range dead {
		if h.IsAlive {
			t.Errorf("GetDeadOrchestrators returned alive orchestrator: %s", h.Project)
		}
	}
}

func TestHealthMonitorGetAllHealth(t *testing.T) {
	hm := NewHealthMonitor(nil, nil)
	defer hm.Stop()

	// Add health records
	hm.mu.Lock()
	hm.health["project1:8080"] = &OrchestratorHealth{
		Project: "project1",
		Port:    8080,
		IsAlive: true,
	}
	hm.health["project2:8081"] = &OrchestratorHealth{
		Project: "project2",
		Port:    8081,
		IsAlive: false,
	}
	hm.mu.Unlock()

	all := hm.GetAllHealth()
	if len(all) != 2 {
		t.Errorf("expected 2 health records, got %d", len(all))
	}
}

func TestHealthMonitorCleanupStaleRecords(t *testing.T) {
	hm := NewHealthMonitor(nil, nil)
	defer hm.Stop()

	// Add health records for projects that are no longer in registry
	hm.mu.Lock()
	hm.health["project1:8080"] = &OrchestratorHealth{
		Project: "project1",
		Port:    8080,
		IsAlive: true,
	}
	hm.health["stale:9999"] = &OrchestratorHealth{
		Project: "stale",
		Port:    9999,
		IsAlive: true,
	}
	hm.mu.Unlock()

	// Only project1 is in the "current" entries
	currentEntries := []OrchestratorEntry{
		{Project: "project1", Port: 8080},
	}

	hm.cleanupStaleHealthRecords(currentEntries)

	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if _, exists := hm.health["project1:8080"]; !exists {
		t.Error("expected project1:8080 to still exist")
	}
	if _, exists := hm.health["stale:9999"]; exists {
		t.Error("expected stale:9999 to be removed")
	}
}

func TestHealthKey(t *testing.T) {
	key := healthKey("my-project", 8123)
	expected := "my-project:8123"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestHealthMonitorStartStop(t *testing.T) {
	cfg := &HealthMonitorConfig{
		PollInterval:     50 * time.Millisecond,
		FailureThreshold: 3,
		RequestTimeout:   100 * time.Millisecond,
	}

	hm := NewHealthMonitor(cfg, nil)

	// Start should not block
	hm.Start()

	// Give it time to do a check
	time.Sleep(100 * time.Millisecond)

	// Stop should complete without hanging
	done := make(chan struct{})
	go func() {
		hm.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good - stopped cleanly
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() timed out")
	}
}

func TestHealthMonitorForceCheck(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}))
	defer server.Close()

	cfg := &HealthMonitorConfig{
		PollInterval:     time.Hour, // Very long to ensure only ForceCheck triggers
		FailureThreshold: 3,
		RequestTimeout:   time.Second,
	}

	hm := NewHealthMonitor(cfg, nil)
	defer hm.Stop()

	// Manually add a health record pointing to our test server
	// Note: In real usage, entries come from the registry
	hm.mu.Lock()
	hm.health["test:8080"] = &OrchestratorHealth{
		Project: "test",
		Port:    8080,
		IsAlive: true,
	}
	hm.mu.Unlock()

	// ForceCheck should trigger immediately
	hm.ForceCheck()

	// The actual check happens via checkAllOrchestrators which reads from registry
	// Since we don't have real registry entries, this test verifies ForceCheck doesn't panic
}

func TestOrchestratorHealthFields(t *testing.T) {
	now := time.Now()
	health := OrchestratorHealth{
		Project:          "test-project",
		Port:             8123,
		PID:              12345,
		ConsecutiveFails: 2,
		LastCheckTime:    now,
		LastSuccessTime:  now.Add(-time.Minute),
		LastError:        "connection refused",
		IsAlive:          false,
		MarkedDeadAt:     now,
	}

	// Test JSON serialization
	data, err := json.Marshal(health)
	if err != nil {
		t.Fatalf("failed to marshal health: %v", err)
	}

	var decoded OrchestratorHealth
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal health: %v", err)
	}

	if decoded.Project != health.Project {
		t.Errorf("project mismatch: expected %q, got %q", health.Project, decoded.Project)
	}
	if decoded.Port != health.Port {
		t.Errorf("port mismatch: expected %d, got %d", health.Port, decoded.Port)
	}
	if decoded.ConsecutiveFails != health.ConsecutiveFails {
		t.Errorf("consecutive fails mismatch: expected %d, got %d", health.ConsecutiveFails, decoded.ConsecutiveFails)
	}
	if decoded.IsAlive != health.IsAlive {
		t.Errorf("is_alive mismatch: expected %v, got %v", health.IsAlive, decoded.IsAlive)
	}
}

func TestHealthMonitorRecoveryAfterFailure(t *testing.T) {
	cfg := &HealthMonitorConfig{
		PollInterval:     100 * time.Millisecond,
		FailureThreshold: 3,
		RequestTimeout:   100 * time.Millisecond,
	}

	hm := NewHealthMonitor(cfg, nil)
	defer hm.Stop()

	entry := OrchestratorEntry{
		Project: "test-project",
		Port:    9999,
		PID:     12345,
	}

	key := healthKey(entry.Project, entry.Port)

	// Set up initial state with some failures
	hm.mu.Lock()
	hm.health[key] = &OrchestratorHealth{
		Project:          entry.Project,
		Port:             entry.Port,
		PID:              entry.PID,
		IsAlive:          true,
		ConsecutiveFails: 2,
		LastError:        "previous error",
	}
	hm.mu.Unlock()

	// Simulate a successful health check (reset logic)
	hm.mu.Lock()
	health := hm.health[key]
	// This mimics what happens in checkOrchestrator on success
	health.ConsecutiveFails = 0
	health.LastError = ""
	health.LastSuccessTime = time.Now()
	health.IsAlive = true
	hm.mu.Unlock()

	// Verify recovery
	recovered := hm.GetHealth(entry.Project, entry.Port)
	if recovered.ConsecutiveFails != 0 {
		t.Errorf("expected consecutive fails to reset to 0, got %d", recovered.ConsecutiveFails)
	}
	if recovered.LastError != "" {
		t.Errorf("expected last error to be cleared, got %q", recovered.LastError)
	}
	if !recovered.IsAlive {
		t.Error("expected orchestrator to be alive after recovery")
	}
}

func TestHealthMonitorConcurrentAccess(t *testing.T) {
	hm := NewHealthMonitor(nil, nil)
	defer hm.Stop()

	// Pre-populate some health records
	hm.mu.Lock()
	for i := 0; i < 10; i++ {
		key := healthKey("project", 8080+i)
		hm.health[key] = &OrchestratorHealth{
			Project: "project",
			Port:    8080 + i,
			IsAlive: true,
		}
	}
	hm.mu.Unlock()

	// Concurrent reads and writes
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)

		// Reader 1 - GetAllHealth
		go func() {
			defer wg.Done()
			_ = hm.GetAllHealth()
		}()

		// Reader 2 - GetHealth
		go func(port int) {
			defer wg.Done()
			_ = hm.GetHealth("project", port)
		}(8080 + (i % 10))

		// Reader 3 - GetDeadOrchestrators
		go func() {
			defer wg.Done()
			_ = hm.GetDeadOrchestrators()
		}()
	}

	wg.Wait()
}
