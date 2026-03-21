package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Port != DefaultPort {
		t.Errorf("expected default port %d, got %d", DefaultPort, cfg.Port)
	}

	if cfg.LogPath == "" {
		t.Error("expected LogPath to be set")
	}
}

func TestIsRunningOnPort(t *testing.T) {
	// Test with a port that's not in use
	unusedPort := 59123 // High port unlikely to be in use
	if IsRunningOnPort(unusedPort) {
		t.Error("expected IsRunningOnPort to return false for unused port")
	}
}

func TestGetDaemonURL(t *testing.T) {
	url := GetDaemonURL()
	expected := fmt.Sprintf("http://localhost:%d", DefaultPort)
	if url != expected {
		t.Errorf("expected %s, got %s", expected, url)
	}
}

func TestDaemonNew(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	cfg := &Config{
		Port:    9999,
		LogPath: logPath,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	if d.port != 9999 {
		t.Errorf("expected port 9999, got %d", d.port)
	}

	if d.logFile == nil {
		t.Error("expected logFile to be set")
	}

	if d.shutdown == nil {
		t.Error("expected shutdown channel to be initialized")
	}

	// Cleanup
	d.logFile.Close()
}

func TestDaemonNewWithNilConfig(t *testing.T) {
	// When nil config is passed, it should use defaults
	d, err := New(nil)
	if err != nil {
		t.Fatalf("failed to create daemon with nil config: %v", err)
	}

	if d.port != DefaultPort {
		t.Errorf("expected default port %d, got %d", DefaultPort, d.port)
	}

	// Cleanup
	d.logFile.Close()
}

func TestDaemonStartAndStop(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58100 + os.Getpid()%1000

	cfg := &Config{
		Port:    testPort,
		LogPath: logPath,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	// Start the daemon
	if err := d.Start(); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Verify it's running by checking the port
	if !IsRunningOnPort(testPort) {
		t.Error("expected daemon to be running on test port")
	}

	// Make a request to the ping endpoint
	resp, err := http.Get(fmt.Sprintf("http://localhost:%d/api/ping", testPort))
	if err != nil {
		t.Fatalf("failed to ping daemon: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	// Stop the daemon
	if err := d.Stop(); err != nil {
		t.Fatalf("failed to stop daemon: %v", err)
	}

	// Wait for it to stop
	time.Sleep(100 * time.Millisecond)

	// Verify it's no longer running
	if IsRunningOnPort(testPort) {
		t.Error("expected daemon to be stopped")
	}
}

func TestDaemonAPIEndpoints(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58200 + os.Getpid()%1000

	cfg := &Config{
		Port:    testPort,
		LogPath: logPath,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	if err := d.Start(); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	defer d.Stop()

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	baseURL := fmt.Sprintf("http://localhost:%d", testPort)

	// Test /api/ping
	t.Run("ping", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/ping")
		if err != nil {
			t.Fatalf("failed to ping: %v", err)
		}
		defer resp.Body.Close()

		var result map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if result["status"] != "ok" {
			t.Errorf("expected status 'ok', got '%s'", result["status"])
		}
	})

	// Test /api/status
	t.Run("status", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/status")
		if err != nil {
			t.Fatalf("failed to get status: %v", err)
		}
		defer resp.Body.Close()

		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Check daemon section exists
		daemon, ok := result["daemon"].(map[string]any)
		if !ok {
			t.Fatal("expected 'daemon' section in response")
		}

		// Check port is correct
		port, ok := daemon["port"].(float64)
		if !ok || int(port) != testPort {
			t.Errorf("expected port %d, got %v", testPort, daemon["port"])
		}

		// Check uptime is present
		if daemon["uptime"] == nil {
			t.Error("expected 'uptime' in daemon section")
		}
	})

	// Test /api/orchestrators
	t.Run("orchestrators", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/orchestrators")
		if err != nil {
			t.Fatalf("failed to get orchestrators: %v", err)
		}
		defer resp.Body.Close()

		// Should return an array (possibly empty)
		var result []any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
	})

	// Test /api/activity
	t.Run("activity", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/activity")
		if err != nil {
			t.Fatalf("failed to get activity: %v", err)
		}
		defer resp.Body.Close()

		// Should return an array (possibly empty)
		var result []any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
	})

	// Test /api/metrics
	t.Run("metrics", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/metrics")
		if err != nil {
			t.Fatalf("failed to get metrics: %v", err)
		}
		defer resp.Body.Close()

		// Should return an object (may be an error response if no metrics)
		var result map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			// Empty response is acceptable when no metrics data exists
			if resp.StatusCode == http.StatusOK {
				return
			}
			t.Fatalf("failed to decode response: %v", err)
		}
	})

	// Test / (dashboard)
	t.Run("dashboard", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/")
		if err != nil {
			t.Fatalf("failed to get dashboard: %v", err)
		}
		defer resp.Body.Close()

		if resp.Header.Get("Content-Type") != "text/html" {
			t.Errorf("expected Content-Type text/html, got %s", resp.Header.Get("Content-Type"))
		}
	})
}

func TestDaemonShutdownEndpoint(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58300 + os.Getpid()%1000

	cfg := &Config{
		Port:    testPort,
		LogPath: logPath,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	// Start daemon using Run() in a goroutine so the shutdown signal is handled
	runDone := make(chan struct{})
	go func() {
		d.Run()
		close(runDone)
	}()

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	baseURL := fmt.Sprintf("http://localhost:%d", testPort)

	// Shutdown endpoint requires POST
	t.Run("shutdown_requires_post", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/api/shutdown")
		if err != nil {
			t.Fatalf("failed to call shutdown: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("expected status 405, got %d", resp.StatusCode)
		}
	})

	// Test actual shutdown via POST
	t.Run("shutdown_post", func(t *testing.T) {
		resp, err := http.Post(baseURL+"/api/shutdown", "application/json", nil)
		if err != nil {
			t.Fatalf("failed to call shutdown: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected status 200, got %d", resp.StatusCode)
		}

		var result map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if result["status"] != "shutting_down" {
			t.Errorf("expected status 'shutting_down', got '%s'", result["status"])
		}
	})

	// Wait for the Run() goroutine to complete (daemon fully stopped)
	select {
	case <-runDone:
		// Successfully stopped
	case <-time.After(5 * time.Second):
		t.Error("daemon did not shut down within timeout")
		d.Stop() // Force cleanup
	}

	// Verify port is released
	if IsRunningOnPort(testPort) {
		t.Error("expected daemon port to be released after shutdown")
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{5 * time.Second, "5s"},
		{65 * time.Second, "1m 5s"},
		{3665 * time.Second, "1h 1m 5s"},
		{7200 * time.Second, "2h 0m 0s"},
	}

	for _, test := range tests {
		t.Run(test.expected, func(t *testing.T) {
			result := formatUptime(test.duration)
			if result != test.expected {
				t.Errorf("formatUptime(%v) = %s, expected %s", test.duration, result, test.expected)
			}
		})
	}
}

func TestDaemonLog(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	cfg := &Config{
		Port:    9999,
		LogPath: logPath,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}
	defer d.logFile.Close()

	// Log a message
	d.log("test message")

	// Read the log file
	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	if len(content) == 0 {
		t.Error("expected log file to have content")
	}

	// Check that the message is in the log
	if !contains(string(content), "test message") {
		t.Error("expected log to contain 'test message'")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
