package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// Client is an HTTP client for communicating with the daemon.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new daemon client.
func NewClient(port int) *Client {
	if port == 0 {
		port = DefaultPort
	}
	return &Client{
		baseURL: fmt.Sprintf("http://localhost:%d", port),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// DefaultClient returns a client for the default daemon port.
func DefaultClient() *Client {
	return NewClient(DefaultPort)
}

// Register registers an orchestrator with the daemon.
func (c *Client) Register(project string, port, workers, issues int) error {
	reg := Registration{
		Project: project,
		Port:    port,
		PID:     os.Getpid(),
		Workers: workers,
		Issues:  issues,
	}

	data, err := json.Marshal(reg)
	if err != nil {
		return fmt.Errorf("marshal registration: %w", err)
	}

	resp, err := c.httpClient.Post(c.baseURL+"/register", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("register request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if !result.Success && result.Error != "" {
		return fmt.Errorf("%s", result.Error)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registration failed: %s", result.Error)
	}

	return nil
}

// Deregister removes an orchestrator registration from the daemon.
func (c *Client) Deregister(project string) error {
	req := struct {
		Project string `json:"project"`
		PID     int    `json:"pid"`
	}{
		Project: project,
		PID:     os.Getpid(),
	}

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	resp, err := c.httpClient.Post(c.baseURL+"/deregister", "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("deregister request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	if !result.Success && result.Error != "" {
		return fmt.Errorf("%s", result.Error)
	}

	return nil
}

// List retrieves all registered orchestrators from the daemon.
func (c *Client) List() ([]*OrchestratorEntry, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/orchestrators")
	if err != nil {
		return nil, fmt.Errorf("list request: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Orchestrators []*OrchestratorEntry `json:"orchestrators"`
		Error         string               `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("%s", result.Error)
	}

	return result.Orchestrators, nil
}

// IsHealthy checks if the daemon is running and healthy.
func (c *Client) IsHealthy() bool {
	resp, err := c.httpClient.Get(c.baseURL + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// IsDaemonRunning checks if the daemon is accessible.
func (c *Client) IsDaemonRunning() bool {
	return c.IsHealthy()
}
