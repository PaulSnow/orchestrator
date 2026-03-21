package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDaemon_Register(t *testing.T) {
	d := New(0)

	// Create a test server using the daemon's handler
	mux := http.NewServeMux()
	mux.HandleFunc("/register", d.handleRegister)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Test successful registration
	body := `{"project":"test/repo","port":8123,"pid":12345,"workers":5,"issues":10}`
	resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %d", resp.StatusCode)
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if result["success"] != true {
		t.Errorf("expected success=true, got %v", result["success"])
	}

	// Verify registration stored
	entry := d.GetByProject("test/repo")
	if entry == nil {
		t.Fatal("expected entry to be stored")
	}
	if entry.Port != 8123 {
		t.Errorf("expected port 8123, got %d", entry.Port)
	}
	if entry.Workers != 5 {
		t.Errorf("expected 5 workers, got %d", entry.Workers)
	}
	if entry.Issues != 10 {
		t.Errorf("expected 10 issues, got %d", entry.Issues)
	}
}

func TestDaemon_DuplicateRegistration(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/register", d.handleRegister)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Register with current PID (will be "running")
	myPID := os.Getpid()
	resp1, err := http.Post(ts.URL+"/register", "application/json",
		strings.NewReader(`{"project":"dup/repo","port":8123,"pid":`+itoa(myPID)+`,"workers":5,"issues":10}`))
	if err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	resp1.Body.Close()

	// Try to register again with different PID
	resp2, err := http.Post(ts.URL+"/register", "application/json",
		strings.NewReader(`{"project":"dup/repo","port":8124,"pid":99999,"workers":3,"issues":5}`))
	if err != nil {
		t.Fatalf("second register failed: %v", err)
	}
	defer resp2.Body.Close()

	// Should be conflict since our PID is running
	if resp2.StatusCode != http.StatusConflict {
		t.Errorf("expected status Conflict, got %d", resp2.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp2.Body).Decode(&result)
	if !strings.Contains(result["error"].(string), "already running") {
		t.Errorf("expected 'already running' error, got %v", result["error"])
	}
}

func TestDaemon_Deregister(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/register", d.handleRegister)
	mux.HandleFunc("/deregister", d.handleDeregister)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// First register
	myPID := os.Getpid()
	resp1, _ := http.Post(ts.URL+"/register", "application/json",
		strings.NewReader(`{"project":"dereg/repo","port":8123,"pid":`+itoa(myPID)+`,"workers":5,"issues":10}`))
	resp1.Body.Close()

	// Verify registered
	if d.GetByProject("dereg/repo") == nil {
		t.Fatal("expected entry after registration")
	}

	// Now deregister
	resp2, err := http.Post(ts.URL+"/deregister", "application/json",
		strings.NewReader(`{"project":"dereg/repo","pid":`+itoa(myPID)+`}`))
	if err != nil {
		t.Fatalf("deregister request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %d", resp2.StatusCode)
	}

	// Verify deregistered
	if d.GetByProject("dereg/repo") != nil {
		t.Error("expected entry to be removed after deregistration")
	}
}

func TestDaemon_List(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/register", d.handleRegister)
	mux.HandleFunc("/orchestrators", d.handleList)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Register a few
	myPID := os.Getpid()
	for i, proj := range []string{"proj/a", "proj/b", "proj/c"} {
		resp, _ := http.Post(ts.URL+"/register", "application/json",
			strings.NewReader(`{"project":"`+proj+`","port":`+itoa(8100+i)+`,"pid":`+itoa(myPID)+`,"workers":2,"issues":5}`))
		resp.Body.Close()
	}

	// List all
	resp, err := http.Get(ts.URL + "/orchestrators")
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	defer resp.Body.Close()

	var result struct {
		Orchestrators []*OrchestratorEntry `json:"orchestrators"`
		Count         int                  `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}

	if result.Count != 3 {
		t.Errorf("expected count 3, got %d", result.Count)
	}
	if len(result.Orchestrators) != 3 {
		t.Errorf("expected 3 orchestrators, got %d", len(result.Orchestrators))
	}
}

func TestDaemon_ValidationErrors(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/register", d.handleRegister)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{"missing project", `{"port":8123,"pid":123}`, "project is required"},
		{"missing port", `{"project":"test","pid":123}`, "port is required"},
		{"missing pid", `{"project":"test","port":8123}`, "pid is required"},
		{"invalid json", `{invalid`, "invalid JSON"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+"/register", "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				t.Error("expected error status")
			}

			var result map[string]any
			json.NewDecoder(resp.Body).Decode(&result)
			if !strings.Contains(result["error"].(string), tc.expected) {
				t.Errorf("expected error containing %q, got %q", tc.expected, result["error"])
			}
		})
	}
}

func TestDaemon_Health(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.handleHealth)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status OK, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "healthy" {
		t.Errorf("expected status healthy, got %v", result["status"])
	}
}

func TestClient_RegisterDeregister(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/register", d.handleRegister)
	mux.HandleFunc("/deregister", d.handleDeregister)
	mux.HandleFunc("/orchestrators", d.handleList)
	mux.HandleFunc("/health", d.handleHealth)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Create client pointing to test server
	c := &Client{
		baseURL:    ts.URL,
		httpClient: http.DefaultClient,
	}

	// Test registration
	if err := c.Register("client/test", 9000, 3, 8); err != nil {
		t.Fatalf("client register failed: %v", err)
	}

	// Verify via list
	entries, err := c.List()
	if err != nil {
		t.Fatalf("client list failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(entries))
	}

	// Test deregistration
	if err := c.Deregister("client/test"); err != nil {
		t.Fatalf("client deregister failed: %v", err)
	}

	// Verify removed
	entries, _ = c.List()
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after deregister, got %d", len(entries))
	}
}

func TestClient_IsHealthy(t *testing.T) {
	d := New(0)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.handleHealth)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := &Client{
		baseURL:    ts.URL,
		httpClient: http.DefaultClient,
	}

	if !c.IsHealthy() {
		t.Error("expected healthy daemon")
	}

	// Stop server and check
	ts.Close()
	if c.IsHealthy() {
		t.Error("expected unhealthy after shutdown")
	}
}

// itoa is a simple int to string conversion for test use
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
