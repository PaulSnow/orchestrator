package orchestrator

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// AgentTeam manages a Claude agent team running in a tmux session.
// This is required because Claude can't spawn agent teams from within itself,
// but it CAN create tmux sessions and launch Claude there.
type AgentTeam struct {
	SessionName  string
	Prompt       string
	NumTeammates int
	StartedAt    time.Time
	Status       string // starting, running, completed, failed
}

// AgentTeamConfig holds configuration for launching an agent team.
type AgentTeamConfig struct {
	SessionName  string   // tmux session name
	WorkDir      string   // working directory for Claude
	Prompt       string   // the agent team prompt
	NumTeammates int      // number of teammates to spawn
	Mode         string   // "split-pane" or "in-process" (default: in-process)
	Files        []string // files to create/modify (for progress tracking)
}

// LaunchAgentTeam creates a tmux session, starts Claude, and sends an agent team prompt.
// Returns the session name for monitoring.
func LaunchAgentTeam(cfg *AgentTeamConfig) (*AgentTeam, error) {
	if cfg.SessionName == "" {
		cfg.SessionName = fmt.Sprintf("agent-team-%d", time.Now().Unix())
	}
	if cfg.Mode == "" {
		cfg.Mode = "in-process"
	}

	// Kill any existing session with this name
	exec.Command("tmux", "kill-session", "-t", cfg.SessionName).Run()

	// Create new tmux session
	args := []string{"new-session", "-d", "-s", cfg.SessionName, "-x", "200", "-y", "50"}
	if cfg.WorkDir != "" {
		args = append(args, "-c", cfg.WorkDir)
	}
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return nil, fmt.Errorf("failed to create tmux session: %w", err)
	}

	// Launch Claude in the session
	claudeCmd := "claude --dangerously-skip-permissions"
	if err := tmuxSendKeys(cfg.SessionName, claudeCmd, true); err != nil {
		return nil, fmt.Errorf("failed to launch claude: %w", err)
	}

	// Wait for Claude to start
	time.Sleep(3 * time.Second)

	// Send the agent team prompt
	if err := tmuxSendKeys(cfg.SessionName, cfg.Prompt, true); err != nil {
		return nil, fmt.Errorf("failed to send prompt: %w", err)
	}

	team := &AgentTeam{
		SessionName:  cfg.SessionName,
		Prompt:       cfg.Prompt,
		NumTeammates: cfg.NumTeammates,
		StartedAt:    time.Now(),
		Status:       "running",
	}

	LogMsg(fmt.Sprintf("[agent-team] Launched %s with %d teammates", cfg.SessionName, cfg.NumTeammates))
	return team, nil
}

// tmuxSendKeys sends keystrokes to a tmux session.
func tmuxSendKeys(session, keys string, enter bool) error {
	args := []string{"send-keys", "-t", session, keys}
	if enter {
		args = append(args, "Enter")
	}
	return exec.Command("tmux", args...).Run()
}

// CaptureOutput captures the current tmux pane content.
func (t *AgentTeam) CaptureOutput() (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-t", t.SessionName, "-p").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// CaptureOutputTail captures the last n lines of tmux pane content.
func (t *AgentTeam) CaptureOutputTail(lines int) (string, error) {
	out, err := t.CaptureOutput()
	if err != nil {
		return "", err
	}

	allLines := strings.Split(out, "\n")
	if len(allLines) <= lines {
		return out, nil
	}
	return strings.Join(allLines[len(allLines)-lines:], "\n"), nil
}

// IsComplete checks if the agent team has finished.
// Looks for completion indicators in the tmux output.
func (t *AgentTeam) IsComplete() (bool, error) {
	out, err := t.CaptureOutputTail(30)
	if err != nil {
		return false, err
	}

	// Look for completion indicators
	completionIndicators := []string{
		"Task agents finished",
		"All teammates completed",
		"agents finished",
		"Build passes",
		"Build: OK",
		"All done",
	}

	for _, indicator := range completionIndicators {
		if strings.Contains(out, indicator) {
			t.Status = "completed"
			return true, nil
		}
	}

	// Check for failure indicators
	failureIndicators := []string{
		"Error:",
		"failed to",
		"FATAL",
		"panic:",
	}

	for _, indicator := range failureIndicators {
		if strings.Contains(out, indicator) && strings.Contains(out, "agent") {
			t.Status = "failed"
			return true, nil
		}
	}

	return false, nil
}

// Wait blocks until the agent team completes or timeout is reached.
func (t *AgentTeam) Wait(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	checkInterval := 10 * time.Second

	for time.Now().Before(deadline) {
		complete, err := t.IsComplete()
		if err != nil {
			return err
		}
		if complete {
			return nil
		}
		time.Sleep(checkInterval)
	}

	return fmt.Errorf("agent team %s timed out after %v", t.SessionName, timeout)
}

// WaitWithProgress blocks until complete, logging progress periodically.
func (t *AgentTeam) WaitWithProgress(timeout time.Duration, progressInterval time.Duration) error {
	deadline := time.Now().Add(timeout)
	lastProgress := time.Now()

	for time.Now().Before(deadline) {
		complete, err := t.IsComplete()
		if err != nil {
			return err
		}
		if complete {
			LogMsg(fmt.Sprintf("[agent-team] %s completed with status: %s", t.SessionName, t.Status))
			return nil
		}

		// Log progress
		if time.Since(lastProgress) >= progressInterval {
			out, _ := t.CaptureOutputTail(10)
			// Extract progress info
			progress := extractProgress(out)
			if progress != "" {
				LogMsg(fmt.Sprintf("[agent-team] %s: %s", t.SessionName, progress))
			}
			lastProgress = time.Now()
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("agent team %s timed out after %v", t.SessionName, timeout)
}

// extractProgress extracts progress information from tmux output.
func extractProgress(output string) string {
	lines := strings.Split(output, "\n")

	// Look for agent status lines
	for _, line := range lines {
		if strings.Contains(line, "tool uses") ||
		   strings.Contains(line, "Running") ||
		   strings.Contains(line, "agents") {
			return strings.TrimSpace(line)
		}
	}

	return ""
}

// Kill terminates the agent team's tmux session.
func (t *AgentTeam) Kill() error {
	t.Status = "killed"
	return exec.Command("tmux", "kill-session", "-t", t.SessionName).Run()
}

// SendCommand sends an additional command to the running agent team.
func (t *AgentTeam) SendCommand(cmd string) error {
	return tmuxSendKeys(t.SessionName, cmd, true)
}

// GetElapsed returns how long the agent team has been running.
func (t *AgentTeam) GetElapsed() time.Duration {
	return time.Since(t.StartedAt)
}

// BuildAgentTeamPrompt constructs a standard agent team prompt.
func BuildAgentTeamPrompt(workDir string, tasks []AgentTask) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Create an agent team with %d teammates.\n\n", len(tasks)))
	sb.WriteString(fmt.Sprintf("Working directory: %s\n\n", workDir))

	for i, task := range tasks {
		sb.WriteString(fmt.Sprintf("Teammate %d - %s:\n", i+1, task.Name))
		for _, item := range task.Items {
			sb.WriteString(fmt.Sprintf("- %s\n", item))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Use in-process mode for teammates.\n")
	sb.WriteString("Coordinate dependencies between tasks.\n")

	return sb.String()
}

// AgentTask describes work for one teammate.
type AgentTask struct {
	Name  string   // e.g., "supervisor_detect.go"
	Items []string // bullet points of what to do
}

// QuickTeam is a convenience function for simple parallel tasks.
// It launches an agent team and waits for completion.
func QuickTeam(sessionName, workDir, prompt string, timeout time.Duration) error {
	cfg := &AgentTeamConfig{
		SessionName: sessionName,
		WorkDir:     workDir,
		Prompt:      prompt,
	}

	team, err := LaunchAgentTeam(cfg)
	if err != nil {
		return err
	}

	err = team.WaitWithProgress(timeout, 30*time.Second)
	if err != nil {
		team.Kill()
		return err
	}

	// Clean up on success
	team.Kill()
	return nil
}
