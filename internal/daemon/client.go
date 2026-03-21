package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// Client provides methods for orchestrators to interact with the daemon.
type Client struct {
	baseURL string
}

// NewClient creates a new daemon client.
func NewClient() *Client {
	return &Client{
		baseURL: GetDaemonURL(),
	}
}

// Ping checks if the daemon is responsive.
func (c *Client) Ping() error {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(c.baseURL + "/api/ping")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	return nil
}

// Status retrieves the daemon's status.
func (c *Client) Status() (map[string]any, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(c.baseURL + "/api/status")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, err
	}
	return status, nil
}

// EnsureRunning makes sure the daemon is running.
// If not running, it starts a new daemon process in the background.
func EnsureRunning() error {
	// Check if daemon is already running
	if IsRunning() {
		return nil
	}

	// Start the daemon
	return StartDaemon()
}

// StartDaemon starts a new daemon process in the background.
func StartDaemon() error {
	// Find our own executable path
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable path: %w", err)
	}

	// Start the daemon command in background
	cmd := exec.Command(execPath, "daemon", "start")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	// Detach from parent process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting daemon process: %w", err)
	}

	// Don't wait for the process - let it run in background
	go cmd.Wait()

	// Wait a moment for the daemon to start
	time.Sleep(500 * time.Millisecond)

	// Verify it started
	for range 10 {
		if IsRunning() {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("daemon did not start within timeout")
}

// StopDaemon requests the daemon to shut down gracefully.
func StopDaemon() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(GetDaemonURL()+"/api/shutdown", "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("shutdown returned status %d", resp.StatusCode)
	}
	return nil
}
