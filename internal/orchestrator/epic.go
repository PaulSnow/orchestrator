package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// RepoInfo contains information about a git repository.
type RepoInfo struct {
	Owner        string // GitHub/GitLab owner/org
	Name         string // Repository name
	Platform     string // "github" or "gitlab"
	LocalPath    string // Absolute path to repo
	DefaultBranch string // main, master, etc.
	RemoteURL    string // Original remote URL
}

// DetectRepoInfo detects repository information from the current directory.
func DetectRepoInfo() (*RepoInfo, error) {
	return DetectRepoInfoFromPath(".")
}

// DetectRepoInfoFromPath detects repository information from a given path.
func DetectRepoInfoFromPath(path string) (*RepoInfo, error) {
	// Get absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("get absolute path: %w", err)
	}

	// Check if it's a git repo
	cmd := exec.Command("git", "-C", absPath, "rev-parse", "--git-dir")
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("not a git repository: %s", absPath)
	}

	// Get remote URL
	cmd = exec.Command("git", "-C", absPath, "remote", "get-url", "origin")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("no origin remote configured")
	}
	remoteURL := strings.TrimSpace(string(output))

	// Parse remote URL to extract owner, repo, platform
	owner, repo, platform, err := parseRemoteURL(remoteURL)
	if err != nil {
		return nil, err
	}

	// Get default branch
	defaultBranch := detectDefaultBranch(absPath)

	return &RepoInfo{
		Owner:         owner,
		Name:          repo,
		Platform:      platform,
		LocalPath:     absPath,
		DefaultBranch: defaultBranch,
		RemoteURL:     remoteURL,
	}, nil
}

// parseRemoteURL parses a git remote URL and extracts owner, repo, and platform.
// Supports formats:
//   - https://github.com/owner/repo.git
//   - git@github.com:owner/repo.git
//   - https://gitlab.com/owner/repo.git
//   - git@gitlab.com:owner/repo.git
func parseRemoteURL(url string) (owner, repo, platform string, err error) {
	// HTTPS format: https://github.com/owner/repo.git
	httpsRe := regexp.MustCompile(`https?://([^/]+)/([^/]+)/([^/]+?)(?:\.git)?$`)
	if m := httpsRe.FindStringSubmatch(url); m != nil {
		host := m[1]
		owner = m[2]
		repo = strings.TrimSuffix(m[3], ".git")
		platform = detectPlatformFromHost(host)
		return owner, repo, platform, nil
	}

	// SSH format: git@github.com:owner/repo.git
	sshRe := regexp.MustCompile(`git@([^:]+):([^/]+)/([^/]+?)(?:\.git)?$`)
	if m := sshRe.FindStringSubmatch(url); m != nil {
		host := m[1]
		owner = m[2]
		repo = strings.TrimSuffix(m[3], ".git")
		platform = detectPlatformFromHost(host)
		return owner, repo, platform, nil
	}

	return "", "", "", fmt.Errorf("unable to parse remote URL: %s", url)
}

// detectPlatformFromHost determines the platform from the host name.
func detectPlatformFromHost(host string) string {
	host = strings.ToLower(host)
	if strings.Contains(host, "gitlab") {
		return "gitlab"
	}
	return "github" // Default to GitHub
}

// detectDefaultBranch tries to detect the default branch name.
func detectDefaultBranch(repoPath string) string {
	// Try to get from remote HEAD
	cmd := exec.Command("git", "-C", repoPath, "symbolic-ref", "refs/remotes/origin/HEAD")
	output, err := cmd.Output()
	if err == nil {
		ref := strings.TrimSpace(string(output))
		if strings.HasPrefix(ref, "refs/remotes/origin/") {
			return strings.TrimPrefix(ref, "refs/remotes/origin/")
		}
	}

	// Check for common branch names
	for _, branch := range []string{"main", "master"} {
		cmd = exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet",
			fmt.Sprintf("refs/remotes/origin/%s", branch))
		if err := cmd.Run(); err == nil {
			return branch
		}
	}

	return "main" // Default fallback
}

// LoadConfigFromEpicNumber creates a RunConfig from just an epic issue number.
// It auto-detects the repository from the current directory.
func LoadConfigFromEpicNumber(epicNumber int, workers int) (*RunConfig, error) {
	// Detect repo info from current directory
	repoInfo, err := DetectRepoInfo()
	if err != nil {
		return nil, fmt.Errorf("detect repo: %w", err)
	}

	// Set defaults
	worktreeBase := filepath.Join(os.TempDir(), fmt.Sprintf("%s-worktrees", repoInfo.Name))
	branchPrefix := "feature/issue-"

	return LoadConfigFromEpicFull(repoInfo, epicNumber, worktreeBase, branchPrefix, workers)
}

// LoadConfigFromEpicFull creates a RunConfig from repo info and epic number.
func LoadConfigFromEpicFull(repoInfo *RepoInfo, epicNumber int, worktreeBase, branchPrefix string, workers int) (*RunConfig, error) {
	// Fetch the epic issue
	epic, err := FetchIssueFromPlatform(repoInfo.Owner, repoInfo.Name, epicNumber, repoInfo.Platform)
	if err != nil {
		return nil, fmt.Errorf("fetch epic #%d: %w", epicNumber, err)
	}

	// Parse task list from epic body
	tasks := ParseTaskList(epic.Body)
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no task items found in epic #%d body", epicNumber)
	}

	// Create config
	cfg := NewRunConfig()
	cfg.Project = fmt.Sprintf("%s/%s", repoInfo.Owner, repoInfo.Name)

	if branchPrefix == "" {
		branchPrefix = "feature/issue-"
	}

	// Add repo config
	cfg.Repos["default"] = &RepoConfig{
		Name:          "default",
		Path:          repoInfo.LocalPath,
		WorktreeBase:  worktreeBase,
		BranchPrefix:  branchPrefix,
		Platform:      repoInfo.Platform,
		DefaultBranch: repoInfo.DefaultBranch,
	}

	// Store epic info for hot-reload
	cfg.EpicNumber = epicNumber
	cfg.EpicURL = epic.HTMLURL

	// Process each task item
	for _, task := range tasks {
		// Skip completed tasks
		if task.Completed {
			continue
		}

		// Fetch full issue details
		subIssue, err := FetchIssueFromPlatform(repoInfo.Owner, repoInfo.Name, task.IssueNumber, repoInfo.Platform)
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
		return nil, fmt.Errorf("no open issues found in epic #%d", epicNumber)
	}

	// Set workers
	if workers > 0 {
		cfg.NumWorkers = workers
	}

	// Use repo name as tmux session
	cfg.TmuxSession = repoInfo.Name

	return cfg, nil
}

// ReloadFromEpic reloads the config from the epic issue.
// It preserves the status of completed issues.
func ReloadFromEpic(cfg *RunConfig) error {
	if cfg.EpicNumber == 0 {
		return nil // Not an epic-based config
	}

	repo, _ := cfg.PrimaryRepo()
	if repo == nil {
		return fmt.Errorf("no primary repo configured")
	}

	repoInfo := &RepoInfo{
		Owner:         extractOwnerFromProject(cfg.Project),
		Name:          extractRepoFromProject(cfg.Project),
		Platform:      repo.Platform,
		LocalPath:     repo.Path,
		DefaultBranch: repo.DefaultBranch,
	}

	// Fetch the epic
	epic, err := FetchIssueFromPlatform(repoInfo.Owner, repoInfo.Name, cfg.EpicNumber, repoInfo.Platform)
	if err != nil {
		return fmt.Errorf("fetch epic: %w", err)
	}

	// Parse tasks
	tasks := ParseTaskList(epic.Body)

	// Build map of current issue statuses to preserve
	existingStatus := make(map[int]string)
	for _, issue := range cfg.Issues {
		existingStatus[issue.Number] = issue.Status
	}

	// Rebuild issues list
	var newIssues []*Issue
	for _, task := range tasks {
		// Check if issue already exists
		status := "pending"
		if task.Completed {
			status = "completed"
		} else if existing, ok := existingStatus[task.IssueNumber]; ok {
			status = existing // Preserve existing status
		}

		// Fetch issue details if not completed
		var title, description string
		if status != "completed" {
			subIssue, err := FetchIssueFromPlatform(repoInfo.Owner, repoInfo.Name, task.IssueNumber, repoInfo.Platform)
			if err != nil {
				LogMsg(fmt.Sprintf("Warning: could not fetch #%d: %v", task.IssueNumber, err))
				continue
			}
			title = subIssue.Title
			description = subIssue.Body
		} else {
			// For completed, just use task title
			title = task.Title
		}

		issue := &Issue{
			Number:      task.IssueNumber,
			Title:       title,
			Description: description,
			DependsOn:   task.BlockedBy,
			Status:      status,
			Repo:        "default",
		}

		newIssues = append(newIssues, issue)
	}

	cfg.Issues = newIssues
	return nil
}

func extractOwnerFromProject(project string) string {
	parts := strings.Split(project, "/")
	if len(parts) >= 1 {
		return parts[0]
	}
	return ""
}

func extractRepoFromProject(project string) string {
	parts := strings.Split(project, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// GitHubIssue represents a GitHub issue from the API.
type GitHubIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	HTMLURL string `json:"html_url"`
}

// ParseEpicURL extracts owner, repo, issue number, and platform from a GitHub/GitLab URL.
// Supports formats:
//   - https://github.com/owner/repo/issues/123
//   - https://gitlab.com/owner/repo/-/issues/123
//   - owner/repo#123 (defaults to GitHub)
// Returns: owner, repo, number, platform ("github" or "gitlab"), error
func ParseEpicURL(url string) (owner, repo string, number int, err error) {
	_, _, _, platform, err := ParseEpicURLWithPlatform(url)
	if err != nil {
		return "", "", 0, err
	}
	owner, repo, number, _, _ = ParseEpicURLWithPlatform(url)
	_ = platform // handled by full function
	return owner, repo, number, nil
}

// ParseEpicURLWithPlatform extracts owner, repo, issue number, and platform.
func ParseEpicURLWithPlatform(url string) (owner, repo string, number int, platform string, err error) {
	// GitHub full URL format
	re := regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/issues/(\d+)`)
	if matches := re.FindStringSubmatch(url); matches != nil {
		number, _ = strconv.Atoi(matches[3])
		return matches[1], matches[2], number, "github", nil
	}

	// GitLab full URL format (handles both gitlab.com and self-hosted)
	re = regexp.MustCompile(`gitlab[^/]*/([^/]+)/([^/]+)/-/issues/(\d+)`)
	if matches := re.FindStringSubmatch(url); matches != nil {
		number, _ = strconv.Atoi(matches[3])
		return matches[1], matches[2], number, "gitlab", nil
	}

	// Short format: owner/repo#123 (default to GitHub)
	re = regexp.MustCompile(`^([^/]+)/([^#]+)#(\d+)$`)
	if matches := re.FindStringSubmatch(url); matches != nil {
		number, _ = strconv.Atoi(matches[3])
		return matches[1], matches[2], number, "github", nil
	}

	return "", "", 0, "", fmt.Errorf("invalid epic URL format: %s", url)
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

// GitLabIssue represents a GitLab issue from the API.
type GitLabIssue struct {
	IID         int    `json:"iid"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	WebURL      string `json:"web_url"`
}

// FetchGitLabIssue fetches an issue using the glab CLI.
func FetchGitLabIssue(owner, repo string, number int) (*GitHubIssue, error) {
	cmd := exec.Command("glab", "api",
		fmt.Sprintf("projects/%s%%2F%s/issues/%d", owner, repo, number),
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("glab api failed: %w", err)
	}

	var glIssue GitLabIssue
	if err := json.Unmarshal(output, &glIssue); err != nil {
		return nil, fmt.Errorf("parse issue JSON: %w", err)
	}

	// Convert to common format
	return &GitHubIssue{
		Number:  glIssue.IID,
		Title:   glIssue.Title,
		Body:    glIssue.Description,
		State:   glIssue.State,
		HTMLURL: glIssue.WebURL,
	}, nil
}

// FetchIssueFromPlatform fetches an issue from GitHub or GitLab based on platform.
func FetchIssueFromPlatform(owner, repo string, number int, platform string) (*GitHubIssue, error) {
	switch platform {
	case "gitlab":
		return FetchGitLabIssue(owner, repo, number)
	default:
		return FetchGitHubIssue(owner, repo, number)
	}
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

// LoadConfigFromEpic creates a RunConfig from a GitHub or GitLab epic issue.
func LoadConfigFromEpic(epicURL, repoPath, worktreeBase, branchPrefix string, workers int) (*RunConfig, error) {
	owner, repo, number, platform, err := ParseEpicURLWithPlatform(epicURL)
	if err != nil {
		return nil, err
	}

	// Fetch the epic issue
	epic, err := FetchIssueFromPlatform(owner, repo, number, platform)
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
		Platform:     platform,
	}

	// Process each task item
	for _, task := range tasks {
		// Skip completed tasks
		if task.Completed {
			continue
		}

		// Fetch full issue details
		subIssue, err := FetchIssueFromPlatform(owner, repo, task.IssueNumber, platform)
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
// Supports both GitHub and GitLab platforms.
func UpdateEpicCheckbox(epicURL string, issueNumber int, completed bool) error {
	owner, repo, epicNumber, platform, err := ParseEpicURLWithPlatform(epicURL)
	if err != nil {
		return err
	}

	// Fetch current epic body
	epic, err := FetchIssueFromPlatform(owner, repo, epicNumber, platform)
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

	// Update the issue via platform-specific CLI
	var cmd *exec.Cmd
	switch platform {
	case "gitlab":
		// GitLab uses glab CLI with project API
		cmd = exec.Command("glab", "api", "-X", "PUT",
			fmt.Sprintf("projects/%s%%2F%s/issues/%d", owner, repo, epicNumber),
			"-f", fmt.Sprintf("description=%s", newBody),
		)
	default:
		// GitHub uses gh CLI
		cmd = exec.Command("gh", "issue", "edit", strconv.Itoa(epicNumber),
			"--repo", fmt.Sprintf("%s/%s", owner, repo),
			"--body", newBody,
		)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update epic body on %s: %w", platform, err)
	}

	return nil
}
