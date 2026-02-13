package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/PaulSnow/orchestrator/internal/config"
)

// Result captures the outcome of running a command in a repository.
type Result struct {
	Repo     string    `json:"repo"`
	Command  string    `json:"command"`
	LogFile  string    `json:"log_file"`
	ExitCode int       `json:"exit_code"`
	Success  bool      `json:"success"`
	Duration float64   `json:"duration_seconds"`
	RunAt    time.Time `json:"run_at"`
}

// RunInRepo executes a command in a repository directory, capturing output to a log file.
func RunInRepo(repo config.RepoConfig, command string, args []string, logPrefix string) Result {
	logFile := fmt.Sprintf("/tmp/orchestrator-%s-%s.log", logPrefix, repo.Name)

	result := Result{
		Repo:    repo.Name,
		Command: fmt.Sprintf("%s %s", command, joinArgs(args)),
		LogFile: logFile,
		RunAt:   time.Now(),
	}

	if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
		result.ExitCode = 1
		os.WriteFile(logFile, []byte(fmt.Sprintf("ERROR: directory %s does not exist\n", repo.Local)), 0644)
		return result
	}

	f, err := os.Create(logFile)
	if err != nil {
		result.ExitCode = 1
		return result
	}
	defer f.Close()

	cmd := exec.Command(command, args...)
	cmd.Dir = repo.Local
	cmd.Stdout = f
	cmd.Stderr = f

	start := time.Now()
	err = cmd.Run()
	result.Duration = time.Since(start).Seconds()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
	} else {
		result.Success = true
	}

	return result
}

// BuildRepo builds a repository based on its language.
func BuildRepo(repo config.RepoConfig) Result {
	switch repo.Language {
	case "go":
		return RunInRepo(repo, "go", []string{"build", "./..."}, "build")
	case "javascript":
		return RunInRepo(repo, "npm", []string{"run", "build"}, "build")
	default:
		return Result{
			Repo:     repo.Name,
			Command:  "unknown language: " + repo.Language,
			ExitCode: 1,
		}
	}
}

// TestRepo runs tests for a repository based on its language.
func TestRepo(repo config.RepoConfig) Result {
	switch repo.Language {
	case "go":
		return RunInRepo(repo, "go", []string{"test", "./...", "-short", "-timeout", "10m"}, "test")
	case "javascript":
		return RunInRepo(repo, "npm", []string{"test"}, "test")
	default:
		return Result{
			Repo:     repo.Name,
			Command:  "unknown language: " + repo.Language,
			ExitCode: 1,
		}
	}
}

// WriteResults writes a results file to the state directory.
func WriteResults(rootPath string, filename string, results []Result) error {
	stateDir := filepath.Join(rootPath, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return err
	}

	// Simple text format for easy reading
	f, err := os.Create(filepath.Join(stateDir, filename))
	if err != nil {
		return err
	}
	defer f.Close()

	for _, r := range results {
		status := "PASS"
		if !r.Success {
			status = "FAIL"
		}
		fmt.Fprintf(f, "[%s] %s: %s (%.1fs) -> %s\n", status, r.Repo, r.Command, r.Duration, r.LogFile)
	}

	return nil
}

func joinArgs(args []string) string {
	s := ""
	for i, a := range args {
		if i > 0 {
			s += " "
		}
		s += a
	}
	return s
}
