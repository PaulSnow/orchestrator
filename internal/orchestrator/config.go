package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// NumWorkers is the default worker count.
const NumWorkers = 5

// RunConfig holds full run configuration.
type RunConfig struct {
	Project            string                 `json:"project,omitempty"`
	Repos              map[string]*RepoConfig `json:"repos,omitempty"`
	Issues             []*Issue               `json:"issues,omitempty"`
	InitialAssignments map[int]int            `json:"initial_assignments,omitempty"`
	NumWorkers         int                    `json:"num_workers,omitempty"`
	CycleInterval      int                    `json:"cycle_interval,omitempty"`
	MaxRetries         int                    `json:"max_retries,omitempty"`
	StallTimeout       int                    `json:"stall_timeout,omitempty"`
	WallClockTimeout   int                    `json:"wall_clock_timeout,omitempty"`
	PromptType         string                 `json:"prompt_type,omitempty"`
	Pipeline           []string               `json:"pipeline,omitempty"`
	ProjectContext     *ProjectContext        `json:"project_context,omitempty"`
	Review             *ReviewConfig          `json:"review,omitempty"`
	Web                *WebConfig             `json:"web,omitempty"`
	TmuxSession        string                 `json:"tmux_session,omitempty"`
	StaggerDelay       int                    `json:"stagger_delay,omitempty"`

	// Derived paths (set after loading)
	ConfigPath string `json:"-"`
	OrchRoot   string `json:"-"`
	StateDir   string `json:"-"`
}

// NewRunConfig creates a RunConfig with defaults.
func NewRunConfig() *RunConfig {
	return &RunConfig{
		Repos:              make(map[string]*RepoConfig),
		Issues:             []*Issue{},
		InitialAssignments: make(map[int]int),
		NumWorkers:         5,
		CycleInterval:      60,
		MaxRetries:         10,
		StallTimeout:       900,
		WallClockTimeout:   1800,
		PromptType:         "implement",
		Pipeline:           []string{"implement"},
		Review:             NewReviewConfig(),
		Web:                NewWebConfig(),
		TmuxSession:        "proof-orchestrator",
		StaggerDelay:       30,
	}
}

// PrimaryRepo returns the first (or only) repo config.
func (c *RunConfig) PrimaryRepo() (*RepoConfig, error) {
	if len(c.Repos) == 0 {
		return nil, fmt.Errorf("no repos configured")
	}
	// Get first key (deterministic order)
	keys := make([]string, 0, len(c.Repos))
	for k := range c.Repos {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return c.Repos[keys[0]], nil
}

// RepoForIssue returns the repo config for a given issue.
func (c *RunConfig) RepoForIssue(issue *Issue) *RepoConfig {
	if issue.Repo != "" {
		if repo, ok := c.Repos[issue.Repo]; ok {
			return repo
		}
	}
	repo, _ := c.PrimaryRepo()
	return repo
}

// GetIssue finds an issue by number.
func (c *RunConfig) GetIssue(number int) *Issue {
	for _, issue := range c.Issues {
		if issue.Number == number {
			return issue
		}
	}
	return nil
}

// RepoForIssueByNumber returns the repo config for an issue by its number.
func (c *RunConfig) RepoForIssueByNumber(issueNumber int) *RepoConfig {
	issue := c.GetIssue(issueNumber)
	if issue != nil {
		return c.RepoForIssue(issue)
	}
	repo, _ := c.PrimaryRepo()
	return repo
}

// LoadConfig loads and validates configuration from a JSON file.
func LoadConfig(configPath string) (*RunConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	cfg := NewRunConfig()
	absPath, _ := filepath.Abs(configPath)
	cfg.ConfigPath = absPath
	cfg.OrchRoot = findOrchRoot(configPath)

	// Load simple fields
	if v, ok := raw["project"].(string); ok {
		cfg.Project = v
	}
	if v, ok := raw["tmux_session"].(string); ok {
		cfg.TmuxSession = v
	}
	if v, ok := raw["num_workers"].(float64); ok {
		cfg.NumWorkers = int(v)
	}
	if v, ok := raw["cycle_interval"].(float64); ok {
		cfg.CycleInterval = int(v)
	}
	if v, ok := raw["max_retries"].(float64); ok {
		cfg.MaxRetries = int(v)
	}
	if v, ok := raw["stall_timeout"].(float64); ok {
		cfg.StallTimeout = int(v)
	}
	if v, ok := raw["wall_clock_timeout"].(float64); ok {
		cfg.WallClockTimeout = int(v)
	}
	if v, ok := raw["prompt_type"].(string); ok {
		cfg.PromptType = v
	}
	if v, ok := raw["stagger_delay"].(float64); ok {
		cfg.StaggerDelay = int(v)
	}

	// Load pipeline
	if v, ok := raw["pipeline"].([]any); ok {
		cfg.Pipeline = make([]string, 0, len(v))
		for _, s := range v {
			if str, ok := s.(string); ok {
				cfg.Pipeline = append(cfg.Pipeline, str)
			}
		}
	}

	// Load project context
	if v, ok := raw["project_context"].(map[string]any); ok {
		cfg.ProjectContext = projectContextFromMap(v)
	}

	// Load review config
	if v, ok := raw["review"].(map[string]any); ok {
		cfg.Review = reviewConfigFromMap(v)
	}

	// Load web config
	if v, ok := raw["web"].(map[string]any); ok {
		cfg.Web = webConfigFromMap(v)
	}

	// Set state directory
	if cfg.Project != "" {
		cfg.StateDir = filepath.Join(cfg.OrchRoot, "state", cfg.Project)
	} else {
		cfg.StateDir = filepath.Join(cfg.OrchRoot, "state")
	}

	// Load repos: new format or legacy single-repo
	if repos, ok := raw["repos"].(map[string]any); ok {
		for name, rdata := range repos {
			if rd, ok := rdata.(map[string]any); ok {
				repo := &RepoConfig{Name: name}
				if v, ok := rd["path"].(string); ok {
					repo.Path = v
				}
				if v, ok := rd["default_branch"].(string); ok {
					repo.DefaultBranch = v
				}
				if v, ok := rd["worktree_base"].(string); ok {
					repo.WorktreeBase = v
				}
				if v, ok := rd["branch_prefix"].(string); ok {
					repo.BranchPrefix = v
				}
				if v, ok := rd["platform"].(string); ok {
					repo.Platform = v
				}
				repo.Init()
				cfg.Repos[name] = repo
			}
		}
	} else if repoPath, ok := raw["repo_path"].(string); ok {
		// Legacy single-repo format
		name := "default"
		if v, ok := raw["repo"].(string); ok {
			name = v
		}
		repo := &RepoConfig{
			Name: name,
			Path: repoPath,
		}
		if v, ok := raw["default_branch"].(string); ok {
			repo.DefaultBranch = v
		}
		if v, ok := raw["worktree_base"].(string); ok {
			repo.WorktreeBase = v
		}
		if v, ok := raw["branch_prefix"].(string); ok {
			repo.BranchPrefix = v
		}
		if v, ok := raw["platform"].(string); ok {
			repo.Platform = v
		}
		repo.Init()
		cfg.Repos[name] = repo
	}

	// Load issues
	if issues, ok := raw["issues"].([]any); ok {
		for _, idata := range issues {
			if id, ok := idata.(map[string]any); ok {
				issue := issueFromMap(id)
				// If no repo set and we have a single repo, default to it
				if issue.Repo == "" && len(cfg.Repos) == 1 {
					for k := range cfg.Repos {
						issue.Repo = k
						break
					}
				}
				cfg.Issues = append(cfg.Issues, issue)
			}
		}
	}

	// Load initial assignments
	if assignments, ok := raw["initial_assignments"].(map[string]any); ok {
		for k, v := range assignments {
			var workerID int
			fmt.Sscanf(k, "%d", &workerID)
			if num, ok := v.(float64); ok {
				cfg.InitialAssignments[workerID] = int(num)
			}
		}
	}

	return cfg, nil
}

func projectContextFromMap(m map[string]any) *ProjectContext {
	pc := &ProjectContext{}
	if v, ok := m["language"].(string); ok {
		pc.Language = v
	}
	if v, ok := m["build_command"].(string); ok {
		pc.BuildCommand = v
	}
	if v, ok := m["test_command"].(string); ok {
		pc.TestCommand = v
	}
	if v, ok := m["commit_prefix"].(string); ok {
		pc.CommitPrefix = v
	}
	if rules, ok := m["safety_rules"].([]any); ok {
		for _, r := range rules {
			if s, ok := r.(string); ok {
				pc.SafetyRules = append(pc.SafetyRules, s)
			}
		}
	}
	if files, ok := m["key_files"].([]any); ok {
		for _, f := range files {
			if s, ok := f.(string); ok {
				pc.KeyFiles = append(pc.KeyFiles, s)
			}
		}
	}
	return pc
}

func reviewConfigFromMap(m map[string]any) *ReviewConfig {
	rc := NewReviewConfig()
	if v, ok := m["enabled"].(bool); ok {
		rc.Enabled = v
	}
	if v, ok := m["parallel_workers"].(float64); ok {
		rc.ParallelWorkers = int(v)
	}
	if v, ok := m["session_timeout"].(float64); ok {
		rc.SessionTimeout = int(v)
	}
	if v, ok := m["post_comments"].(bool); ok {
		rc.PostComments = v
	}
	if v, ok := m["strict_mode"].(bool); ok {
		rc.StrictMode = v
	}
	return rc
}

func webConfigFromMap(m map[string]any) *WebConfig {
	wc := NewWebConfig()
	if v, ok := m["enabled"].(bool); ok {
		wc.Enabled = v
	}
	if v, ok := m["port"].(float64); ok {
		wc.Port = int(v)
	}
	if v, ok := m["host"].(string); ok {
		wc.Host = v
	}
	return wc
}

func issueFromMap(m map[string]any) *Issue {
	issue := &Issue{}
	if v, ok := m["number"].(float64); ok {
		issue.Number = int(v)
	}
	if v, ok := m["title"].(string); ok {
		issue.Title = v
	}
	if v, ok := m["priority"].(float64); ok {
		issue.Priority = int(v)
	}
	if v, ok := m["wave"].(float64); ok {
		issue.Wave = int(v)
	}
	if v, ok := m["status"].(string); ok {
		issue.Status = v
	}
	if v, ok := m["assigned_worker"].(float64); ok {
		w := int(v)
		issue.AssignedWorker = &w
	}
	if v, ok := m["repo"].(string); ok {
		issue.Repo = v
	}
	if v, ok := m["task_type"].(string); ok {
		issue.TaskType = v
	}
	if v, ok := m["pipeline_stage"].(float64); ok {
		issue.PipelineStage = int(v)
	}
	if v, ok := m["description"].(string); ok {
		issue.Description = v
	}
	if deps, ok := m["depends_on"].([]any); ok {
		for _, d := range deps {
			if num, ok := d.(float64); ok {
				issue.DependsOn = append(issue.DependsOn, int(num))
			}
		}
	}
	issue.Init()
	return issue
}

func findOrchRoot(configPath string) string {
	absPath, _ := filepath.Abs(configPath)
	current := filepath.Dir(absPath)
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(current, "claude.md")); err == nil {
			return current
		}
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	// Fall back to two levels up from config
	return filepath.Dir(filepath.Dir(absPath))
}

// ValidateConfig validates configuration and returns list of errors.
func ValidateConfig(cfg *RunConfig, validStages []string) []string {
	var errors []string

	if len(cfg.Repos) == 0 {
		errors = append(errors, "No repositories configured")
	}

	for name, repo := range cfg.Repos {
		if info, err := os.Stat(repo.Path); err != nil || !info.IsDir() {
			errors = append(errors, fmt.Sprintf("Repo '%s' path does not exist: %s", name, repo.Path))
		}
		if repo.BranchPrefix == "" {
			errors = append(errors, fmt.Sprintf("Repo '%s' has no branch_prefix", name))
		}
	}

	if len(cfg.Issues) == 0 {
		errors = append(errors, "No issues configured")
	}

	// Validate pipeline stages
	if len(cfg.Pipeline) == 0 {
		errors = append(errors, "pipeline is empty - must have at least one stage")
	} else {
		validSet := make(map[string]bool)
		for _, s := range validStages {
			validSet[s] = true
		}
		for _, stage := range cfg.Pipeline {
			if !validSet[stage] {
				errors = append(errors, fmt.Sprintf("Invalid pipeline stage '%s'. Valid: %v", stage, validStages))
			}
		}
	}

	// Check issue dependencies
	issueNumbers := make(map[int]bool)
	for _, i := range cfg.Issues {
		issueNumbers[i.Number] = true
	}
	for _, issue := range cfg.Issues {
		for _, dep := range issue.DependsOn {
			if dep == issue.Number {
				errors = append(errors, fmt.Sprintf("Issue #%d depends on itself", issue.Number))
			} else if !issueNumbers[dep] {
				errors = append(errors, fmt.Sprintf("Issue #%d depends on #%d which is not in the issue list", issue.Number, dep))
			}
		}
	}

	// Detect circular dependencies using DFS
	depMap := make(map[int][]int)
	for _, i := range cfg.Issues {
		depMap[i.Number] = i.DependsOn
	}

	var hasCycle func(node int, visited, stack map[int]bool) bool
	hasCycle = func(node int, visited, stack map[int]bool) bool {
		visited[node] = true
		stack[node] = true
		for _, dep := range depMap[node] {
			if !visited[dep] {
				if hasCycle(dep, visited, stack) {
					return true
				}
			} else if stack[dep] {
				return true
			}
		}
		delete(stack, node)
		return false
	}

	visited := make(map[int]bool)
	for num := range issueNumbers {
		if !visited[num] {
			if hasCycle(num, visited, make(map[int]bool)) {
				errors = append(errors, fmt.Sprintf("Circular dependency detected involving issue #%d", num))
				break
			}
		}
	}

	// Check initial assignments
	for workerID, issueNum := range cfg.InitialAssignments {
		if workerID < 1 || workerID > cfg.NumWorkers {
			errors = append(errors, fmt.Sprintf("Initial assignment: worker %d is out of range (1-%d)", workerID, cfg.NumWorkers))
		}
		if !issueNumbers[issueNum] {
			errors = append(errors, fmt.Sprintf("Initial assignment: issue #%d is not in the issue list", issueNum))
		}
	}

	return errors
}

// LoadAllConfigs loads all *-issues.json configs from a directory.
func LoadAllConfigs(configDir string) ([]*RunConfig, error) {
	info, err := os.Stat(configDir)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("config directory not found: %s", configDir)
	}

	matches, err := filepath.Glob(filepath.Join(configDir, "*-issues.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	var configs []*RunConfig
	for _, configPath := range matches {
		cfg, err := LoadConfig(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: Failed to load %s: %v\n", configPath, err)
			continue
		}
		configs = append(configs, cfg)
	}

	if len(configs) == 0 {
		return nil, fmt.Errorf("no *-issues.json files found in %s", configDir)
	}

	return configs, nil
}

// SaveConfig atomically saves the configuration to a JSON file.
func SaveConfig(cfg *RunConfig) error {
	if cfg.ConfigPath == "" {
		return fmt.Errorf("config path not set")
	}

	// Marshal configuration to JSON with indentation
	data, err := json.MarshalIndent(buildConfigMap(cfg), "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	// Write atomically: write to temp file, then rename
	tempPath := cfg.ConfigPath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return fmt.Errorf("writing temp config: %w", err)
	}

	if err := os.Rename(tempPath, cfg.ConfigPath); err != nil {
		os.Remove(tempPath) // Clean up temp file on rename failure
		return fmt.Errorf("renaming config: %w", err)
	}

	return nil
}

// buildConfigMap creates a map representation of RunConfig for JSON serialization.
func buildConfigMap(cfg *RunConfig) map[string]any {
	result := make(map[string]any)

	if cfg.Project != "" {
		result["project"] = cfg.Project
	}
	if cfg.TmuxSession != "" {
		result["tmux_session"] = cfg.TmuxSession
	}
	if cfg.NumWorkers > 0 {
		result["num_workers"] = cfg.NumWorkers
	}
	if cfg.CycleInterval > 0 {
		result["cycle_interval"] = cfg.CycleInterval
	}
	if cfg.MaxRetries > 0 {
		result["max_retries"] = cfg.MaxRetries
	}
	if cfg.StallTimeout > 0 {
		result["stall_timeout"] = cfg.StallTimeout
	}
	if cfg.WallClockTimeout > 0 {
		result["wall_clock_timeout"] = cfg.WallClockTimeout
	}
	if cfg.PromptType != "" {
		result["prompt_type"] = cfg.PromptType
	}
	if cfg.StaggerDelay > 0 {
		result["stagger_delay"] = cfg.StaggerDelay
	}
	if len(cfg.Pipeline) > 0 {
		result["pipeline"] = cfg.Pipeline
	}

	// Build repos map
	if len(cfg.Repos) > 0 {
		repos := make(map[string]any)
		for name, repo := range cfg.Repos {
			repoMap := make(map[string]any)
			if repo.Path != "" {
				repoMap["path"] = repo.Path
			}
			if repo.DefaultBranch != "" {
				repoMap["default_branch"] = repo.DefaultBranch
			}
			if repo.WorktreeBase != "" {
				repoMap["worktree_base"] = repo.WorktreeBase
			}
			if repo.BranchPrefix != "" {
				repoMap["branch_prefix"] = repo.BranchPrefix
			}
			if repo.Platform != "" {
				repoMap["platform"] = repo.Platform
			}
			repos[name] = repoMap
		}
		result["repos"] = repos
	}

	// Build issues array
	if len(cfg.Issues) > 0 {
		issues := make([]map[string]any, 0, len(cfg.Issues))
		for _, issue := range cfg.Issues {
			issueMap := map[string]any{
				"number": issue.Number,
			}
			if issue.Title != "" {
				issueMap["title"] = issue.Title
			}
			if issue.Priority > 0 {
				issueMap["priority"] = issue.Priority
			}
			if issue.Wave > 0 {
				issueMap["wave"] = issue.Wave
			}
			if issue.Status != "" {
				issueMap["status"] = issue.Status
			}
			if issue.AssignedWorker != nil {
				issueMap["assigned_worker"] = *issue.AssignedWorker
			}
			if issue.Repo != "" {
				issueMap["repo"] = issue.Repo
			}
			if issue.TaskType != "" {
				issueMap["task_type"] = issue.TaskType
			}
			if issue.PipelineStage > 0 {
				issueMap["pipeline_stage"] = issue.PipelineStage
			}
			if issue.Description != "" {
				issueMap["description"] = issue.Description
			}
			if len(issue.DependsOn) > 0 {
				issueMap["depends_on"] = issue.DependsOn
			}
			issues = append(issues, issueMap)
		}
		result["issues"] = issues
	}

	// Build initial assignments
	if len(cfg.InitialAssignments) > 0 {
		assignments := make(map[string]any)
		for k, v := range cfg.InitialAssignments {
			assignments[fmt.Sprintf("%d", k)] = v
		}
		result["initial_assignments"] = assignments
	}

	// Project context
	if cfg.ProjectContext != nil {
		pc := make(map[string]any)
		if cfg.ProjectContext.Language != "" {
			pc["language"] = cfg.ProjectContext.Language
		}
		if cfg.ProjectContext.BuildCommand != "" {
			pc["build_command"] = cfg.ProjectContext.BuildCommand
		}
		if cfg.ProjectContext.TestCommand != "" {
			pc["test_command"] = cfg.ProjectContext.TestCommand
		}
		if cfg.ProjectContext.CommitPrefix != "" {
			pc["commit_prefix"] = cfg.ProjectContext.CommitPrefix
		}
		if len(cfg.ProjectContext.SafetyRules) > 0 {
			pc["safety_rules"] = cfg.ProjectContext.SafetyRules
		}
		if len(cfg.ProjectContext.KeyFiles) > 0 {
			pc["key_files"] = cfg.ProjectContext.KeyFiles
		}
		if len(pc) > 0 {
			result["project_context"] = pc
		}
	}

	// Review config
	if cfg.Review != nil {
		rc := make(map[string]any)
		rc["enabled"] = cfg.Review.Enabled
		if cfg.Review.ParallelWorkers > 0 {
			rc["parallel_workers"] = cfg.Review.ParallelWorkers
		}
		if cfg.Review.SessionTimeout > 0 {
			rc["session_timeout"] = cfg.Review.SessionTimeout
		}
		rc["post_comments"] = cfg.Review.PostComments
		rc["strict_mode"] = cfg.Review.StrictMode
		result["review"] = rc
	}

	// Web config
	if cfg.Web != nil {
		wc := make(map[string]any)
		wc["enabled"] = cfg.Web.Enabled
		if cfg.Web.Port > 0 {
			wc["port"] = cfg.Web.Port
		}
		if cfg.Web.Host != "" {
			wc["host"] = cfg.Web.Host
		}
		result["web"] = wc
	}

	return result
}
