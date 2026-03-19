package web

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// WebConfig holds configuration for the web server.
type WebConfig struct {
	Port         int
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// DefaultWebConfig returns a WebConfig with sensible defaults.
func DefaultWebConfig() *WebConfig {
	return &WebConfig{
		Port:         8080,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // No timeout for SSE
	}
}

// ReviewGate provides access to orchestrator state for the web dashboard.
// This interface allows the web server to query current status.
type ReviewGate interface {
	// GetStatus returns the overall orchestrator status.
	GetStatus() Status

	// GetIssues returns all issues with their review state.
	GetIssues() []IssueState

	// GetIssue returns a single issue by ID.
	GetIssue(id int) *IssueState

	// GetSessions returns active Claude sessions.
	GetSessions() []SessionState

	// TriggerAbort initiates a graceful abort of all workers.
	TriggerAbort() error
}

// Status represents the overall orchestrator status.
type Status struct {
	Running       bool      `json:"running"`
	StartedAt     time.Time `json:"started_at"`
	Project       string    `json:"project"`
	TotalIssues   int       `json:"total_issues"`
	Completed     int       `json:"completed"`
	InProgress    int       `json:"in_progress"`
	Pending       int       `json:"pending"`
	Failed        int       `json:"failed"`
	ActiveWorkers int       `json:"active_workers"`
	TotalWorkers  int       `json:"total_workers"`
}

// IssueState represents the state of a single issue.
type IssueState struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Stage          string    `json:"stage"`
	AssignedWorker *int      `json:"assigned_worker,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	CompletedAt    time.Time `json:"completed_at,omitempty"`
	RetryCount     int       `json:"retry_count"`
	LastActivity   string    `json:"last_activity,omitempty"`
}

// SessionState represents the state of a Claude session.
type SessionState struct {
	WorkerID    int       `json:"worker_id"`
	IssueNumber *int      `json:"issue_number,omitempty"`
	Status      string    `json:"status"`
	Stage       string    `json:"stage"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	LogTail     string    `json:"log_tail,omitempty"`
}

// Server is the HTTP server for the web dashboard.
type Server struct {
	http.Server
	cfg     *WebConfig
	gate    ReviewGate
	sseHub  *SSEHub
	handler *Handler
}

// NewServer creates a new web dashboard server.
func NewServer(cfg *WebConfig, gate ReviewGate) *Server {
	if cfg == nil {
		cfg = DefaultWebConfig()
	}

	sseHub := NewSSEHub()
	handler := NewHandler(gate, sseHub)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	s := &Server{
		Server: http.Server{
			Addr:         formatAddr(cfg.Port),
			Handler:      mux,
			ReadTimeout:  cfg.ReadTimeout,
			WriteTimeout: cfg.WriteTimeout,
		},
		cfg:     cfg,
		gate:    gate,
		sseHub:  sseHub,
		handler: handler,
	}

	return s
}

// Start begins listening for HTTP connections.
func (s *Server) Start() error {
	// Start the SSE hub
	go s.sseHub.Run()
	return s.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	// Close the SSE hub
	s.sseHub.Close()
	return s.Server.Shutdown(ctx)
}

// Broadcast sends an event to all connected SSE clients.
func (s *Server) Broadcast(event Event) {
	s.sseHub.Broadcast(event)
}

// SSEHub returns the SSE hub for external event broadcasting.
func (s *Server) SSEHub() *SSEHub {
	return s.sseHub
}

func formatAddr(port int) string {
	if port == 0 {
		port = 8080
	}
	return fmt.Sprintf(":%d", port)
}
