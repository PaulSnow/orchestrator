package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/PaulSnow/orchestrator/internal/repos"
	"github.com/PaulSnow/orchestrator/internal/runner"
	"github.com/PaulSnow/orchestrator/internal/tasks"
)

// ToolScanRepos scans all configured repositories and returns their git statuses.
func ToolScanRepos(s *Server) (string, error) {
	statuses := repos.ScanAll(s.Config)

	// Also persist the status file for other consumers.
	_ = repos.WriteStatusFile(s.RootPath, statuses)

	data, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling scan results: %w", err)
	}
	return string(data), nil
}

// ToolRepoStatus returns the git status of a single named repository.
func ToolRepoStatus(s *Server, repoName string) (string, error) {
	repo, ok := s.Config.GetRepo(repoName)
	if !ok {
		return "", fmt.Errorf("unknown repo: %s (available: %s)", repoName, allRepoNames(s))
	}

	status := repos.ScanRepo(repo)
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling status: %w", err)
	}
	return string(data), nil
}

// ToolRunTests runs tests for a named repository and returns the result.
func ToolRunTests(s *Server, repoName string) (string, error) {
	repo, ok := s.Config.GetRepo(repoName)
	if !ok {
		return "", fmt.Errorf("unknown repo: %s (available: %s)", repoName, allRepoNames(s))
	}

	result := runner.TestRepo(repo)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling test result: %w", err)
	}
	return string(data), nil
}

// ToolBuildRepo builds a named repository and returns the result.
func ToolBuildRepo(s *Server, repoName string) (string, error) {
	repo, ok := s.Config.GetRepo(repoName)
	if !ok {
		return "", fmt.Errorf("unknown repo: %s (available: %s)", repoName, allRepoNames(s))
	}

	result := runner.BuildRepo(repo)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling build result: %w", err)
	}
	return string(data), nil
}

// ToolListTasks returns all backlog and active tasks as JSON.
func ToolListTasks(s *Server) (string, error) {
	backlog, backlogErr := s.TaskMgr.ListBacklog()
	active, activeErr := s.TaskMgr.ListActive()

	type taskList struct {
		Active  []taskSummary `json:"active"`
		Backlog []taskSummary `json:"backlog"`
		Errors  []string      `json:"errors,omitempty"`
	}

	result := taskList{
		Active:  make([]taskSummary, 0),
		Backlog: make([]taskSummary, 0),
	}

	for _, t := range active {
		result.Active = append(result.Active, summarizeTask(t))
	}
	for _, t := range backlog {
		result.Backlog = append(result.Backlog, summarizeTask(t))
	}

	if backlogErr != nil {
		result.Errors = append(result.Errors, "backlog: "+backlogErr.Error())
	}
	if activeErr != nil {
		result.Errors = append(result.Errors, "active: "+activeErr.Error())
	}

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling tasks: %w", err)
	}
	return string(data), nil
}

// ToolStartTask moves a task from backlog to active.
func ToolStartTask(s *Server, taskID string) (string, error) {
	if err := s.TaskMgr.StartTask(taskID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task %s moved to active.", taskID), nil
}

// ToolCompleteTask moves a task from active to completed.
func ToolCompleteTask(s *Server, taskID string) (string, error) {
	if err := s.TaskMgr.CompleteTask(taskID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task %s completed.", taskID), nil
}

// taskSummary is a simplified view of a task for JSON output.
type taskSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Repo        string `json:"repo,omitempty"`
	Type        string `json:"type,omitempty"`
	Priority    string `json:"priority,omitempty"`
	Assigned    string `json:"assigned,omitempty"`
	Description string `json:"description,omitempty"`
}

func summarizeTask(t tasks.Task) taskSummary {
	return taskSummary{
		ID:          t.ID,
		Title:       t.Title,
		Repo:        t.Repo,
		Type:        t.Type,
		Priority:    t.Priority,
		Assigned:    t.Assigned,
		Description: t.Description,
	}
}

func allRepoNames(s *Server) string {
	var names []string
	for _, r := range s.Config.AllRepos() {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}
