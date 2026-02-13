package tasks

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Task represents a parsed task from the markdown files.
type Task struct {
	ID          string
	Title       string
	Repo        string
	Type        string
	Priority    string
	Assigned    string
	Description string
	Branch      string
	RawText     string
}

// Manager handles task lifecycle operations.
type Manager struct {
	tasksDir string
}

// NewManager creates a task manager for the given orchestrator root.
func NewManager(rootPath string) *Manager {
	return &Manager{
		tasksDir: filepath.Join(rootPath, "tasks"),
	}
}

var taskHeaderRe = regexp.MustCompile(`###\s+\[([^\]]+)\]\s+(.+)`)
var fieldRe = regexp.MustCompile(`-\s+\*\*(\w+)\*\*:\s+(.+)`)

// ParseTasks reads a task markdown file and returns parsed tasks.
func (m *Manager) ParseTasks(filename string) ([]Task, error) {
	path := filepath.Join(m.tasksDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var tasks []Task
	var current *Task

	for _, line := range strings.Split(string(data), "\n") {
		if matches := taskHeaderRe.FindStringSubmatch(line); matches != nil {
			if current != nil {
				tasks = append(tasks, *current)
			}
			current = &Task{
				ID:    matches[1],
				Title: strings.TrimSpace(matches[2]),
			}
			continue
		}

		if current != nil {
			if matches := fieldRe.FindStringSubmatch(line); matches != nil {
				key := strings.ToLower(matches[1])
				val := strings.TrimSpace(matches[2])
				switch key {
				case "repo":
					current.Repo = val
				case "type":
					current.Type = val
				case "priority":
					current.Priority = val
				case "assigned":
					current.Assigned = val
				case "description":
					current.Description = val
				case "branch":
					current.Branch = val
				}
			}
			current.RawText += line + "\n"
		}
	}

	if current != nil {
		tasks = append(tasks, *current)
	}

	return tasks, nil
}

// ListBacklog returns all tasks in the backlog.
func (m *Manager) ListBacklog() ([]Task, error) {
	return m.ParseTasks("backlog.md")
}

// ListActive returns all active tasks.
func (m *Manager) ListActive() ([]Task, error) {
	return m.ParseTasks("active.md")
}

// StartTask moves a task from backlog to active by ID.
func (m *Manager) StartTask(id string) error {
	backlogTasks, err := m.ListBacklog()
	if err != nil {
		return fmt.Errorf("reading backlog: %w", err)
	}

	var found *Task
	for i := range backlogTasks {
		if backlogTasks[i].ID == id {
			found = &backlogTasks[i]
			break
		}
	}

	if found == nil {
		return fmt.Errorf("task %s not found in backlog", id)
	}

	// Append to active.md
	activePath := filepath.Join(m.tasksDir, "active.md")
	f, err := os.OpenFile(activePath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := fmt.Sprintf("\n### [%s] %s\n", found.ID, found.Title)
	if found.Repo != "" {
		entry += fmt.Sprintf("- **repo**: %s\n", found.Repo)
	}
	if found.Type != "" {
		entry += fmt.Sprintf("- **type**: %s\n", found.Type)
	}
	entry += fmt.Sprintf("- **assigned**: in-progress\n")
	if found.Description != "" {
		entry += fmt.Sprintf("- **description**: %s\n", found.Description)
	}
	entry += fmt.Sprintf("- **started**: %s\n", time.Now().Format("2006-01-02"))

	_, err = f.WriteString(entry)
	if err != nil {
		return err
	}

	// Remove from backlog by rewriting without the task
	return m.removeTaskFromFile("backlog.md", id)
}

// CompleteTask moves a task from active to completed.
func (m *Manager) CompleteTask(id string) error {
	activeTasks, err := m.ListActive()
	if err != nil {
		return fmt.Errorf("reading active: %w", err)
	}

	var found *Task
	for i := range activeTasks {
		if activeTasks[i].ID == id {
			found = &activeTasks[i]
			break
		}
	}

	if found == nil {
		return fmt.Errorf("task %s not found in active tasks", id)
	}

	// Append to completed.md
	completedPath := filepath.Join(m.tasksDir, "completed.md")
	f, err := os.OpenFile(completedPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	entry := fmt.Sprintf("\n### [%s] %s\n", found.ID, found.Title)
	if found.Repo != "" {
		entry += fmt.Sprintf("- **repo**: %s\n", found.Repo)
	}
	if found.Type != "" {
		entry += fmt.Sprintf("- **type**: %s\n", found.Type)
	}
	entry += fmt.Sprintf("- **completed**: %s\n", time.Now().Format("2006-01-02"))
	if found.Description != "" {
		entry += fmt.Sprintf("- **description**: %s\n", found.Description)
	}

	_, err = f.WriteString(entry)
	if err != nil {
		return err
	}

	return m.removeTaskFromFile("active.md", id)
}

// removeTaskFromFile rewrites a task file without the specified task.
func (m *Manager) removeTaskFromFile(filename, id string) error {
	path := filepath.Join(m.tasksDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	lines := strings.Split(string(data), "\n")
	var result []string
	skip := false

	for _, line := range lines {
		if matches := taskHeaderRe.FindStringSubmatch(line); matches != nil {
			if matches[1] == id {
				skip = true
				continue
			}
			skip = false
		}

		if skip {
			// Skip field lines that belong to the removed task
			if strings.HasPrefix(strings.TrimSpace(line), "- **") {
				continue
			}
			// Stop skipping on non-field, non-empty lines (next section header, etc.)
			if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "- **") {
				skip = false
			} else {
				continue
			}
		}

		result = append(result, line)
	}

	return os.WriteFile(path, []byte(strings.Join(result, "\n")), 0644)
}
