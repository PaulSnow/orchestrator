package orchestrator

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// EpicConfig holds minimal config for epic-based orchestration.
type EpicConfig struct {
	Epic         string `json:"epic"`          // GitHub/GitLab issue URL
	RepoPath     string `json:"repo_path"`     // Local repo path
	WorktreeBase string `json:"worktree_base"` // Worktree directory
	BranchPrefix string `json:"branch_prefix"` // Branch prefix (default: feature/issue-)
	Workers      int    `json:"workers"`       // Number of workers
}

// GitHubIssue represents a GitHub issue from the API.
type GitHubIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// ParseEpicURL extracts owner, repo, and issue number from a GitHub URL.
// Supports formats:
//   - https://github.com/owner/repo/issues/123
//   - owner/repo#123
func ParseEpicURL(url string) (owner, repo string, number int, err error) {
	// Full URL format
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	if matches := re.FindStringSubmatch(url); matches != nil {
		number, _ = strconv.Atoi(matches[3])
		return matches[1], matches[2], number, nil
	}

	// Short format: owner/repo#123
	re = regexp.MustCompile(`^([^/]+)/([^#]+)#(\d+)$`)
	if matches := re.FindStringSubmatch(url); matches != nil {
		number, _ = strconv.Atoi(matches[3])
		return matches[1], matches[2], number, nil
	}

	return "", "", 0, fmt.Errorf("invalid epic URL format: %s", url)
}

// FetchGitHubIssue fetches an issue using the gh CLI.
func FetchGitHubIssue(owner, repo string, number int) (*GitHubIssue, error) {
	cmd := exec.Command("gh", "api",
		fmt.Sprintf("repos/%s/%s/issues/%d", owner, repo, number),
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api failed: %w", err)
	}

	var issue GitHubIssue
	if err := json.Unmarshal(output, &issue); err != nil {
		return nil, fmt.Errorf("parse issue JSON: %w", err)
	}
	return &issue, nil
}

// TaskItem represents a parsed task from an epic's body.
type TaskItem struct {
	IssueNumber int
	Title       string
	Completed   bool
	BlockedBy   []int
}

// ParseTaskList extracts task items from markdown body.
// Supports formats:
//   - [ ] #123 - Title
//   - [x] #123 - Title (blocked by #100, #101)
//   - [ ] #123 Title (depends on #100)
func ParseTaskList(body string) []TaskItem {
	var items []TaskItem

	// Match task list items with issue references
	// Pattern: - [ ] or - [x] followed by #number and optional title/dependencies
	re := regexp.MustCompile(`(?m)^[\s]*[-*]\s*\[([ xX])\]\s*#(\d+)\s*[-:]?\s*([^\n]*)`)

	for _, match := range re.FindAllStringSubmatch(body, -1) {
		completed := strings.ToLower(match[1]) == "x"
		number, _ := strconv.Atoi(match[2])
		rest := match[3]

		item := TaskItem{
			IssueNumber: number,
			Completed:   completed,
			BlockedBy:   []int{},
		}

		// Extract title (everything before parentheses)
		if idx := strings.Index(rest, "("); idx > 0 {
			item.Title = strings.TrimSpace(rest[:idx])
			rest = rest[idx:]
		} else {
			item.Title = strings.TrimSpace(rest)
			rest = ""
		}

		// Extract dependencies from (blocked by #N, #M) or (depends on #N)
		depRe := regexp.MustCompile(`(?i)\((?:blocked by|depends on)[:\s]*([^)]+)\)`)
		if depMatch := depRe.FindStringSubmatch(rest); depMatch != nil {
			numRe := regexp.MustCompile(`#(\d+)`)
			for _, numMatch := range numRe.FindAllStringSubmatch(depMatch[1], -1) {
				depNum, _ := strconv.Atoi(numMatch[1])
				item.BlockedBy = append(item.BlockedBy, depNum)
			}
		}

		items = append(items, item)
	}

	return items
}

// LoadConfigFromEpic creates a RunConfig from a GitHub epic issue.
func LoadConfigFromEpic(epicURL, repoPath, worktreeBase, branchPrefix string, workers int) (*RunConfig, error) {
	owner, repo, number, err := ParseEpicURL(epicURL)
	if err != nil {
		return nil, err
	}

	// Fetch the epic issue
	epic, err := FetchGitHubIssue(owner, repo, number)
	if err != nil {
		return nil, fmt.Errorf("fetch epic #%d: %w", number, err)
	}

	// Parse task list from epic body
	tasks := ParseTaskList(epic.Body)
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no task items found in epic #%d body", number)
	}

	// Fetch details for each sub-issue
	cfg := NewRunConfig()
	cfg.Project = fmt.Sprintf("%s/%s", owner, repo)

	if branchPrefix == "" {
		branchPrefix = "feature/issue-"
	}

	// Add repo config
	cfg.Repos["default"] = &RepoConfig{
		Name:         "default",
		Path:         repoPath,
		WorktreeBase: worktreeBase,
		BranchPrefix: branchPrefix,
		Platform:     "github",
	}

	// Process each task item
	for _, task := range tasks {
		// Skip completed tasks
		if task.Completed {
			continue
		}

		// Fetch full issue details
		subIssue, err := FetchGitHubIssue(owner, repo, task.IssueNumber)
		if err != nil {
			LogMsg(fmt.Sprintf("Warning: could not fetch #%d: %v", task.IssueNumber, err))
			continue
		}

		issue := &Issue{
			Number:      task.IssueNumber,
			Title:       subIssue.Title,
			Description: subIssue.Body,
			DependsOn:   task.BlockedBy,
			Status:      "pending",
			Repo:        "default",
		}

		cfg.Issues = append(cfg.Issues, issue)
	}

	if len(cfg.Issues) == 0 {
		return nil, fmt.Errorf("no open issues found in epic #%d", number)
	}

	// Set workers
	if workers > 0 {
		cfg.NumWorkers = workers
	}

	// Use repo name as tmux session
	cfg.TmuxSession = repo

	return cfg, nil
}

// UpdateEpicCheckbox updates a task checkbox in the epic when an issue completes.
func UpdateEpicCheckbox(epicURL string, issueNumber int, completed bool) error {
	owner, repo, epicNumber, err := ParseEpicURL(epicURL)
	if err != nil {
		return err
	}

	// Fetch current epic body
	epic, err := FetchGitHubIssue(owner, repo, epicNumber)
	if err != nil {
		return err
	}

	// Update checkbox for the issue
	var newMark string
	if completed {
		newMark = "[x]"
	} else {
		newMark = "[ ]"
	}

	// Replace checkbox for this issue number
	re := regexp.MustCompile(fmt.Sprintf(`(\s*[-*]\s*)\[[ xX]\](\s*#%d\b)`, issueNumber))
	newBody := re.ReplaceAllString(epic.Body, fmt.Sprintf("${1}%s${2}", newMark))

	if newBody == epic.Body {
		// No change needed
		return nil
	}

	// Update the issue via gh CLI
	cmd := exec.Command("gh", "issue", "edit", strconv.Itoa(epicNumber),
		"--repo", fmt.Sprintf("%s/%s", owner, repo),
		"--body", newBody,
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update epic body: %w", err)
	}

	return nil
}
