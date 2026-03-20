package orchestrator

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ClaudeSession represents an interactive Claude Code session.
type ClaudeSession struct {
	ID           string           `json:"id"`
	WorkingDir   string           `json:"working_dir"`
	Context      string           `json:"context,omitempty"`
	Messages     []SessionMessage `json:"messages"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Status       string           `json:"status"` // active, completed, error
	mu           sync.Mutex
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	cancelFunc   func()
}

// SessionMessage represents a message in a Claude session.
type SessionMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"` // user, assistant
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// SessionManager manages Claude sessions.
type SessionManager struct {
	sessions   map[string]*ClaudeSession
	sessionDir string
	mu         sync.RWMutex
}

// NewSessionManager creates a new session manager.
func NewSessionManager(stateDir string) *SessionManager {
	sessionDir := filepath.Join(stateDir, "sessions")
	os.MkdirAll(sessionDir, 0755)

	sm := &SessionManager{
		sessions:   make(map[string]*ClaudeSession),
		sessionDir: sessionDir,
	}
	sm.loadExistingSessions()
	return sm
}

// loadExistingSessions loads sessions from disk.
func (sm *SessionManager) loadExistingSessions() {
	files, err := filepath.Glob(filepath.Join(sm.sessionDir, "*.json"))
	if err != nil {
		return
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var session ClaudeSession
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}
		// Mark old sessions as completed if they were active
		if session.Status == "active" {
			session.Status = "completed"
		}
		sm.sessions[session.ID] = &session
	}
}

// CreateSession creates a new Claude session.
func (sm *SessionManager) CreateSession(workingDir, context string) (*ClaudeSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Validate working directory
	if workingDir == "" {
		workingDir = "."
	}
	absPath, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, fmt.Errorf("invalid working directory: %v", err)
	}
	if info, err := os.Stat(absPath); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("working directory does not exist: %s", absPath)
	}

	session := &ClaudeSession{
		ID:         fmt.Sprintf("session-%d", time.Now().UnixNano()),
		WorkingDir: absPath,
		Context:    context,
		Messages:   []SessionMessage{},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Status:     "active",
	}

	sm.sessions[session.ID] = session
	sm.saveSession(session)

	return session, nil
}

// GetSession returns a session by ID.
func (sm *SessionManager) GetSession(id string) (*ClaudeSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[id]
	return session, ok
}

// ListSessions returns all sessions sorted by update time (newest first).
func (sm *SessionManager) ListSessions() []*ClaudeSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	sessions := make([]*ClaudeSession, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		sessions = append(sessions, s)
	}

	// Sort by updated time (newest first)
	for i := 0; i < len(sessions)-1; i++ {
		for j := i + 1; j < len(sessions); j++ {
			if sessions[j].UpdatedAt.After(sessions[i].UpdatedAt) {
				sessions[i], sessions[j] = sessions[j], sessions[i]
			}
		}
	}

	return sessions
}

// DeleteSession deletes a session.
func (sm *SessionManager) DeleteSession(id string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[id]
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}

	// Stop the session if it's running
	if session.cancelFunc != nil {
		session.cancelFunc()
	}

	delete(sm.sessions, id)

	// Remove session file
	sessionPath := filepath.Join(sm.sessionDir, id+".json")
	os.Remove(sessionPath)

	return nil
}

// saveSession saves a session to disk.
func (sm *SessionManager) saveSession(session *ClaudeSession) error {
	session.mu.Lock()
	data, err := json.MarshalIndent(session, "", "  ")
	session.mu.Unlock()
	if err != nil {
		return err
	}

	sessionPath := filepath.Join(sm.sessionDir, session.ID+".json")
	return os.WriteFile(sessionPath, data, 0644)
}

// AddMessage adds a message to a session.
func (sm *SessionManager) AddMessage(sessionID, role, content string) (*SessionMessage, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	session.mu.Lock()
	msg := SessionMessage{
		ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}
	session.Messages = append(session.Messages, msg)
	session.UpdatedAt = time.Now()
	session.mu.Unlock()

	sm.saveSession(session)
	return &msg, nil
}

// StreamResponse holds a chunk of streaming response.
type StreamResponse struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Content   string `json:"content"`
	Done      bool   `json:"done"`
	Error     string `json:"error,omitempty"`
}

// SendPrompt sends a prompt to Claude and streams the response.
func (sm *SessionManager) SendPrompt(sessionID, prompt string, responseCh chan<- StreamResponse) {
	defer close(responseCh)

	session, ok := sm.GetSession(sessionID)
	if !ok {
		responseCh <- StreamResponse{
			SessionID: sessionID,
			Error:     "session not found",
			Done:      true,
		}
		return
	}

	// Add user message
	userMsg, err := sm.AddMessage(sessionID, "user", prompt)
	if err != nil {
		responseCh <- StreamResponse{
			SessionID: sessionID,
			Error:     fmt.Sprintf("failed to add message: %v", err),
			Done:      true,
		}
		return
	}

	// Prepare assistant message ID
	assistantMsgID := fmt.Sprintf("msg-%d", time.Now().UnixNano())

	// Build the prompt with context
	fullPrompt := prompt
	if session.Context != "" {
		fullPrompt = fmt.Sprintf("Context: %s\n\nPrompt: %s", session.Context, prompt)
	}

	// Build command args
	args := []string{"--print", "--dangerously-skip-permissions"}

	// Run claude command
	cmd := exec.Command("claude", args...)
	cmd.Dir = session.WorkingDir
	cmd.Stdin = strings.NewReader(fullPrompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		responseCh <- StreamResponse{
			SessionID: sessionID,
			MessageID: userMsg.ID,
			Error:     fmt.Sprintf("failed to create stdout pipe: %v", err),
			Done:      true,
		}
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		responseCh <- StreamResponse{
			SessionID: sessionID,
			MessageID: userMsg.ID,
			Error:     fmt.Sprintf("failed to create stderr pipe: %v", err),
			Done:      true,
		}
		return
	}

	if err := cmd.Start(); err != nil {
		responseCh <- StreamResponse{
			SessionID: sessionID,
			MessageID: userMsg.ID,
			Error:     fmt.Sprintf("failed to start claude: %v", err),
			Done:      true,
		}
		return
	}

	session.mu.Lock()
	session.cmd = cmd
	session.mu.Unlock()

	// Stream stdout
	var fullContent strings.Builder
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
	for scanner.Scan() {
		line := scanner.Text()
		fullContent.WriteString(line)
		fullContent.WriteString("\n")
		responseCh <- StreamResponse{
			SessionID: sessionID,
			MessageID: assistantMsgID,
			Content:   line + "\n",
			Done:      false,
		}
	}

	// Read any stderr
	stderrBytes, _ := io.ReadAll(stderr)

	if err := cmd.Wait(); err != nil {
		errMsg := string(stderrBytes)
		if errMsg == "" {
			errMsg = err.Error()
		}
		responseCh <- StreamResponse{
			SessionID: sessionID,
			MessageID: assistantMsgID,
			Error:     errMsg,
			Done:      true,
		}
	} else {
		// Add assistant message with full content
		sm.AddMessage(sessionID, "assistant", fullContent.String())

		responseCh <- StreamResponse{
			SessionID: sessionID,
			MessageID: assistantMsgID,
			Done:      true,
		}
	}

	session.mu.Lock()
	session.cmd = nil
	session.mu.Unlock()
}
