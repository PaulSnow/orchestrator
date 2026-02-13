package main

import (
	"fmt"

	"github.com/PaulSnow/orchestrator/internal/config"
	"github.com/PaulSnow/orchestrator/internal/tasks"
)

// Server holds the orchestrator configuration and provides access to tools.
type Server struct {
	Config   *config.Config
	TaskMgr  *tasks.Manager
	RootPath string
}

// NewServer creates a new MCP server with the given orchestrator root path.
func NewServer(rootPath string) (*Server, error) {
	cfg, err := config.Load(rootPath)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	return &Server{
		Config:   cfg,
		TaskMgr:  tasks.NewManager(rootPath),
		RootPath: rootPath,
	}, nil
}

// Shutdown performs any cleanup needed when the server stops.
func (s *Server) Shutdown() {
	// Nothing to clean up currently; placeholder for future use.
}
