package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Coder handles complex tasks using agent teams.
// Used when simple workers fail or task complexity is detected upfront.
type Coder struct {
	cfg   *RunConfig
	state *StateManager

	// Tracking
	sessions []CoderSession
}

// CoderSession records when a coder team was used.
type CoderSession struct {
	Timestamp    time.Time     `json:"timestamp"`
	IssueNumber  int           `json:"issue_number"`
	Title        string        `json:"title"`
	TeamSize     int           `json:"team_size"`
	Duration     time.Duration `json:"duration"`
	Success      bool          `json:"success"`
	WhyNeeded    string        `json:"why_needed"`    // why simple worker failed
	ShouldFix    string        `json:"should_fix"`    // how to avoid needing coder
	Teammates    []string      `json:"teammates"`     // what each teammate did
}

// NewCoder creates a new coder supervisor.
func NewCoder(cfg *RunConfig, state *StateManager) *Coder {
	return &Coder{
		cfg:      cfg,
		state:    state,
		sessions: make([]CoderSession, 0),
	}
}

// HandleComplexIssue uses an agent team to solve a complex issue.
func (c *Coder) HandleComplexIssue(issue *Issue) error {
	session := CoderSession{
		Timestamp:   time.Now(),
		IssueNumber: issue.Number,
		Title:       issue.Title,
		WhyNeeded:   "escalated from simple worker",
	}

	// Analyze the issue to determine team structure
	tasks := c.analyzeAndPlanTasks(issue)
	session.TeamSize = len(tasks)

	if len(tasks) == 0 {
		session.Success = false
		session.ShouldFix = "issue needs clearer requirements"
		c.sessions = append(c.sessions, session)
		return fmt.Errorf("could not plan tasks for issue #%d", issue.Number)
	}

	// Build the agent team prompt
	repo := c.cfg.RepoForIssue(issue)
	workDir := repo.Path
	if repo.WorktreeBase != "" {
		workDir = filepath.Join(repo.WorktreeBase, fmt.Sprintf("issue-%d", issue.Number))
	}

	prompt := c.buildCoderPrompt(issue, tasks)

	// Launch agent team
	sessionName := fmt.Sprintf("coder-%d-%d", issue.Number, time.Now().Unix())
	team, err := LaunchAgentTeam(&AgentTeamConfig{
		SessionName:  sessionName,
		WorkDir:      workDir,
		Prompt:       prompt,
		NumTeammates: len(tasks),
	})

	if err != nil {
		session.Success = false
		session.ShouldFix = "agent team launch failed"
		c.sessions = append(c.sessions, session)
		return fmt.Errorf("failed to launch agent team: %w", err)
	}

	// Wait for completion
	timeout := 30 * time.Minute
	if len(tasks) > 4 {
		timeout = 45 * time.Minute
	}

	err = team.WaitWithProgress(timeout, 30*time.Second)
	session.Duration = team.GetElapsed()

	if err != nil {
		team.Kill()
		session.Success = false
		session.ShouldFix = "agent team timed out - break into smaller issues"
		c.sessions = append(c.sessions, session)
		return err
	}

	// Check if successful
	output, _ := team.CaptureOutput()
	session.Success = strings.Contains(output, "Build passes") ||
		strings.Contains(output, "completed") ||
		strings.Contains(output, "All done")

	if session.Success {
		session.ShouldFix = "consider breaking similar issues into smaller tasks upfront"
	} else {
		session.ShouldFix = "issue may need human review - too complex for automated solution"
	}

	// Record teammate activities
	for _, task := range tasks {
		session.Teammates = append(session.Teammates, task.Name)
	}

	team.Kill()
	c.sessions = append(c.sessions, session)

	LogMsg(fmt.Sprintf("[coder] Completed #%d in %v (success: %v)",
		issue.Number, session.Duration.Round(time.Second), session.Success))

	return nil
}

// analyzeAndPlanTasks breaks down an issue into teammate tasks.
func (c *Coder) analyzeAndPlanTasks(issue *Issue) []AgentTask {
	var tasks []AgentTask

	// Get context from issue description
	desc := issue.Description
	if desc == "" {
		desc = issue.Title
	}

	// Default task structure based on common patterns
	repo := c.cfg.RepoForIssue(issue)
	lang := "Go" // default
	if c.cfg.ProjectContext != nil && c.cfg.ProjectContext.Language != "" {
		lang = c.cfg.ProjectContext.Language
	}

	// Determine task breakdown based on issue type
	taskType := classifyIssueType(issue)

	switch taskType {
	case "feature":
		tasks = c.planFeatureTasks(issue, repo, lang)
	case "bug":
		tasks = c.planBugFixTasks(issue, repo, lang)
	case "refactor":
		tasks = c.planRefactorTasks(issue, repo, lang)
	default:
		tasks = c.planGenericTasks(issue, repo, lang)
	}

	return tasks
}

// classifyIssueType determines what kind of issue this is.
func classifyIssueType(issue *Issue) string {
	title := strings.ToLower(issue.Title)
	desc := strings.ToLower(issue.Description)
	text := title + " " + desc

	if strings.Contains(text, "bug") || strings.Contains(text, "fix") ||
		strings.Contains(text, "broken") || strings.Contains(text, "error") {
		return "bug"
	}

	if strings.Contains(text, "refactor") || strings.Contains(text, "cleanup") ||
		strings.Contains(text, "reorganize") || strings.Contains(text, "simplify") {
		return "refactor"
	}

	if strings.Contains(text, "add") || strings.Contains(text, "implement") ||
		strings.Contains(text, "create") || strings.Contains(text, "new") {
		return "feature"
	}

	return "generic"
}

// planFeatureTasks creates tasks for a new feature.
func (c *Coder) planFeatureTasks(issue *Issue, repo *RepoConfig, lang string) []AgentTask {
	return []AgentTask{
		{
			Name: "Implementation",
			Items: []string{
				fmt.Sprintf("Implement the feature for issue #%d: %s", issue.Number, issue.Title),
				"Follow existing code patterns",
				"Add appropriate error handling",
			},
		},
		{
			Name: "Tests",
			Items: []string{
				"Write unit tests for the new implementation",
				"Ensure good coverage of edge cases",
				"Run tests to verify they pass",
			},
		},
		{
			Name: "Integration",
			Items: []string{
				"Integrate with existing code",
				"Update any affected imports/exports",
				"Verify build passes",
			},
		},
		{
			Name: "Documentation",
			Items: []string{
				"Add code comments where needed",
				"Update any relevant documentation",
				"Prepare commit message",
			},
		},
	}
}

// planBugFixTasks creates tasks for a bug fix.
func (c *Coder) planBugFixTasks(issue *Issue, repo *RepoConfig, lang string) []AgentTask {
	return []AgentTask{
		{
			Name: "Analysis",
			Items: []string{
				fmt.Sprintf("Investigate bug #%d: %s", issue.Number, issue.Title),
				"Find root cause",
				"Document findings",
			},
		},
		{
			Name: "Fix",
			Items: []string{
				"Implement the fix",
				"Ensure no regressions",
				"Add regression test",
			},
		},
		{
			Name: "Verification",
			Items: []string{
				"Run all tests",
				"Verify fix works",
				"Check for side effects",
			},
		},
	}
}

// planRefactorTasks creates tasks for a refactoring.
func (c *Coder) planRefactorTasks(issue *Issue, repo *RepoConfig, lang string) []AgentTask {
	return []AgentTask{
		{
			Name: "Analysis",
			Items: []string{
				fmt.Sprintf("Analyze code for refactoring #%d: %s", issue.Number, issue.Title),
				"Identify all affected files",
				"Plan changes",
			},
		},
		{
			Name: "Refactor",
			Items: []string{
				"Apply refactoring changes",
				"Maintain functionality",
				"Update all references",
			},
		},
		{
			Name: "Tests",
			Items: []string{
				"Ensure all tests still pass",
				"Add tests for any new code paths",
				"Verify no regressions",
			},
		},
	}
}

// planGenericTasks creates default tasks.
func (c *Coder) planGenericTasks(issue *Issue, repo *RepoConfig, lang string) []AgentTask {
	return []AgentTask{
		{
			Name: "Main Work",
			Items: []string{
				fmt.Sprintf("Complete issue #%d: %s", issue.Number, issue.Title),
				"Implement required changes",
				"Follow code standards",
			},
		},
		{
			Name: "Tests",
			Items: []string{
				"Add or update tests",
				"Verify all tests pass",
			},
		},
	}
}

// buildCoderPrompt builds the full prompt for the agent team.
func (c *Coder) buildCoderPrompt(issue *Issue, tasks []AgentTask) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Create an agent team with %d teammates to complete this issue.\n\n", len(tasks)))
	sb.WriteString(fmt.Sprintf("## Issue #%d: %s\n\n", issue.Number, issue.Title))

	if issue.Description != "" {
		sb.WriteString("### Description\n")
		sb.WriteString(issue.Description)
		sb.WriteString("\n\n")
	}

	sb.WriteString("### Tasks\n\n")
	for i, task := range tasks {
		sb.WriteString(fmt.Sprintf("**Teammate %d - %s:**\n", i+1, task.Name))
		for _, item := range task.Items {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Instructions\n\n")
	sb.WriteString("- Use in-process mode for teammates\n")
	sb.WriteString("- Coordinate dependencies between tasks\n")
	sb.WriteString("- Commit with message referencing issue number\n")
	sb.WriteString("- Ensure build passes before finishing\n")

	return sb.String()
}

// GetSessions returns all coder sessions.
func (c *Coder) GetSessions() []CoderSession {
	return c.sessions
}

// GenerateCoderReport writes a report of coder sessions.
func (c *Coder) GenerateCoderReport() error {
	if len(c.sessions) == 0 {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	improvementsDir := filepath.Join(homeDir, ".orchestrator", "improvements")
	os.MkdirAll(improvementsDir, 0755)

	filename := fmt.Sprintf("coder-%s.md", time.Now().Format("2006-01-02"))
	reportPath := filepath.Join(improvementsDir, filename)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Coder Sessions Report - %s\n\n", time.Now().Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("Total sessions: %d\n\n", len(c.sessions)))

	// Stats
	successCount := 0
	var totalDuration time.Duration
	for _, s := range c.sessions {
		if s.Success {
			successCount++
		}
		totalDuration += s.Duration
	}

	sb.WriteString(fmt.Sprintf("- Successful: %d/%d (%.0f%%)\n",
		successCount, len(c.sessions),
		float64(successCount)/float64(len(c.sessions))*100))
	sb.WriteString(fmt.Sprintf("- Total time: %v\n\n", totalDuration.Round(time.Minute)))

	sb.WriteString("## Why Coder Was Needed\n\n")
	reasons := make(map[string]int)
	for _, s := range c.sessions {
		reasons[s.WhyNeeded]++
	}
	for reason, count := range reasons {
		sb.WriteString(fmt.Sprintf("- %s (x%d)\n", reason, count))
	}
	sb.WriteString("\n")

	sb.WriteString("## Improvements Needed\n\n")
	fixes := make(map[string]int)
	for _, s := range c.sessions {
		if s.ShouldFix != "" {
			fixes[s.ShouldFix]++
		}
	}
	for fix, count := range fixes {
		sb.WriteString(fmt.Sprintf("- %s (x%d)\n", fix, count))
	}
	sb.WriteString("\n")

	sb.WriteString("## Session Details\n\n")
	for _, s := range c.sessions {
		status := "FAILED"
		if s.Success {
			status = "SUCCESS"
		}
		sb.WriteString(fmt.Sprintf("### Issue #%d: %s [%s]\n\n", s.IssueNumber, s.Title, status))
		sb.WriteString(fmt.Sprintf("- Team size: %d\n", s.TeamSize))
		sb.WriteString(fmt.Sprintf("- Duration: %v\n", s.Duration.Round(time.Second)))
		sb.WriteString(fmt.Sprintf("- Why needed: %s\n", s.WhyNeeded))
		if len(s.Teammates) > 0 {
			sb.WriteString(fmt.Sprintf("- Teammates: %s\n", strings.Join(s.Teammates, ", ")))
		}
		sb.WriteString("\n")
	}

	return os.WriteFile(reportPath, []byte(sb.String()), 0644)
}
