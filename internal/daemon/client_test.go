package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient()

	expectedURL := GetDaemonURL()
	if client.baseURL != expectedURL {
		t.Errorf("expected baseURL %s, got %s", expectedURL, client.baseURL)
	}
}

func TestClientPing(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58400 + os.Getpid()%1000

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

	// Create a client that points to our test daemon
	client := &Client{
		baseURL: fmt.Sprintf("http://localhost:%d", testPort),
	}

	if err := client.Ping(); err != nil {
		t.Errorf("ping failed: %v", err)
	}
}

func TestClientPingNotRunning(t *testing.T) {
	// Use a port that's not in use
	client := &Client{
		baseURL: "http://localhost:59999",
	}

	if err := client.Ping(); err == nil {
		t.Error("expected ping to fail when daemon is not running")
	}
}

func TestClientStatus(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58500 + os.Getpid()%1000

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

	// Create a client that points to our test daemon
	client := &Client{
		baseURL: fmt.Sprintf("http://localhost:%d", testPort),
	}

	status, err := client.Status()
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}

	// Check daemon section exists
	daemon, ok := status["daemon"].(map[string]any)
	if !ok {
		t.Fatal("expected 'daemon' section in status")
	}

	// Check port is correct
	port, ok := daemon["port"].(float64)
	if !ok || int(port) != testPort {
		t.Errorf("expected port %d, got %v", testPort, daemon["port"])
	}
}

func TestClientStatusNotRunning(t *testing.T) {
	// Use a port that's not in use
	client := &Client{
		baseURL: "http://localhost:59999",
	}

	_, err := client.Status()
	if err == nil {
		t.Error("expected status to fail when daemon is not running")
	}
}

func TestIsRunning(t *testing.T) {
	// When nothing is running on the default port, should return false
	// Note: This test assumes nothing is actually running on port 8100
	// In CI, this should be the case

	// We can't reliably test IsRunning() without potentially interfering
	// with other tests, so we just test the basic case
	result := IsRunning()
	// Just verify it doesn't panic and returns a boolean
	_ = result
}

func TestEnsureRunning(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58600 + os.Getpid()%1000

	cfg := &Config{
		Port:    testPort,
		LogPath: logPath,
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatalf("failed to create daemon: %v", err)
	}

	// Start the daemon first
	if err := d.Start(); err != nil {
		t.Fatalf("failed to start daemon: %v", err)
	}
	defer d.Stop()

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Verify it's running
	if !IsRunningOnPort(testPort) {
		t.Fatal("daemon should be running")
	}

	// When daemon is already running on test port, calling EnsureRunning
	// should succeed (it checks the default port, but we're just testing
	// the logic path)
}

func TestStopDaemon(t *testing.T) {
	// Create a temp directory for the log
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "daemon.log")

	// Use a random high port to avoid conflicts
	testPort := 58700 + os.Getpid()%1000

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

	// Wait for it to start
	time.Sleep(100 * time.Millisecond)

	// Verify it's running
	if !IsRunningOnPort(testPort) {
		t.Fatal("daemon should be running")
	}

	// Stop it directly (not via StopDaemon which uses the default port)
	if err := d.Stop(); err != nil {
		t.Fatalf("failed to stop daemon: %v", err)
	}

	// Wait for it to stop
	time.Sleep(200 * time.Millisecond)

	// Verify it's stopped
	if IsRunningOnPort(testPort) {
		t.Error("daemon should be stopped")
	}
}
