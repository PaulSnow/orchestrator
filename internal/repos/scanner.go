package repos

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PaulSnow/orchestrator/internal/config"
)

// RepoStatus captures the git status of a repository.
type RepoStatus struct {
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	Exists        bool      `json:"exists"`
	Branch        string    `json:"branch,omitempty"`
	Clean         bool      `json:"clean"`
	ModifiedFiles int       `json:"modified_files"`
	UntrackedFiles int      `json:"untracked_files"`
	Ahead         int       `json:"ahead"`
	Behind        int       `json:"behind"`
	LastCommit    string    `json:"last_commit,omitempty"`
	Error         string    `json:"error,omitempty"`
	ScannedAt     time.Time `json:"scanned_at"`
}

// ScanRepo checks the git status of a single repository.
func ScanRepo(repo config.RepoConfig) RepoStatus {
	status := RepoStatus{
		Name:      repo.Name,
		Path:      repo.Local,
		ScannedAt: time.Now(),
	}

	if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
		status.Error = "directory does not exist"
		return status
	}
	status.Exists = true

	// Current branch
	if out, err := gitCmd(repo.Local, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		status.Branch = strings.TrimSpace(out)
	}

	// Porcelain status
	if out, err := gitCmd(repo.Local, "status", "--porcelain"); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) == 1 && lines[0] == "" {
			status.Clean = true
		} else {
			for _, line := range lines {
				if strings.HasPrefix(line, "??") {
					status.UntrackedFiles++
				} else {
					status.ModifiedFiles++
				}
			}
		}
	}

	// Last commit
	if out, err := gitCmd(repo.Local, "log", "--oneline", "-1"); err == nil {
		status.LastCommit = strings.TrimSpace(out)
	}

	// Ahead/behind tracking branch
	if out, err := gitCmd(repo.Local, "rev-list", "--left-right", "--count", "HEAD...@{upstream}"); err == nil {
		parts := strings.Fields(strings.TrimSpace(out))
		if len(parts) == 2 {
			fmt.Sscanf(parts[0], "%d", &status.Ahead)
			fmt.Sscanf(parts[1], "%d", &status.Behind)
		}
	}

	return status
}

// ScanAll scans all configured repositories and returns their statuses.
func ScanAll(cfg *config.Config) []RepoStatus {
	var results []RepoStatus
	for _, repo := range cfg.AllRepos() {
		results = append(results, ScanRepo(repo))
	}
	return results
}

// WriteStatusFile writes scan results to the state directory.
func WriteStatusFile(rootPath string, statuses []RepoStatus) error {
	stateDir := filepath.Join(rootPath, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(statuses, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(stateDir, "repo-status.json"), data, 0644)
}

func gitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
