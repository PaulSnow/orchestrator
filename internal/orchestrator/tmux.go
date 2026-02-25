package orchestrator

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const tmuxTimeout = 30 * time.Second

// runTmux executes a tmux command with timeout.
func runTmux(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tmuxTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "tmux", args...)
	out, err := cmd.Output()
	return string(out), err
}

// SessionExists checks if a tmux session exists.
func SessionExists(session string) bool {
	_, err := runTmux("has-session", "-t", session)
	return err == nil
}

// CreateSession creates a new tmux session with a named first window.
func CreateSession(session, firstWindowName, workingDir string) error {
	if firstWindowName == "" {
		firstWindowName = "orchestrator"
	}
	args := []string{"new-session", "-d", "-s", session, "-n", firstWindowName}
	if workingDir != "" {
		args = append(args, "-c", workingDir)
	}
	_, err := runTmux(args...)
	return err
}

// NewWindow creates a new window in an existing tmux session.
func NewWindow(session, name, workingDir string) error {
	args := []string{"new-window", "-t", session, "-n", name}
	if workingDir != "" {
		args = append(args, "-c", workingDir)
	}
	_, err := runTmux(args...)
	return err
}

// SendCommand sends a command to a tmux window via send-keys.
func SendCommand(session, window, command string) error {
	target := session + ":" + window
	_, err := runTmux("send-keys", "-t", target, command, "Enter")
	return err
}

// SendCtrlC sends Ctrl-C to a tmux window.
func SendCtrlC(session, window string) {
	target := session + ":" + window
	_, _ = runTmux("send-keys", "-t", target, "C-c")
}

// GetPanePID gets the PID of the shell process in a tmux pane.
func GetPanePID(session, window string) *int {
	target := session + ":" + window
	out, err := runTmux("list-panes", "-t", target, "-F", "#{pane_pid}")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil
	}
	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return nil
	}
	return &pid
}

// KillSession kills a tmux session. Returns true if it existed and was killed.
func KillSession(session string) bool {
	if !SessionExists(session) {
		return false
	}
	_, err := runTmux("kill-session", "-t", session)
	return err == nil
}

// ListWindows lists window names in a session.
func ListWindows(session string) []string {
	out, err := runTmux("list-windows", "-t", session, "-F", "#{window_name}")
	if err != nil {
		return nil
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
