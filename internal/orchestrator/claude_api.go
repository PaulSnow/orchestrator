package orchestrator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Claude API constants
const (
	ClaudeAPIURL         = "https://api.anthropic.com/v1/messages"
	ClaudeAPIVersion     = "2023-06-01"
	DefaultModel         = "claude-sonnet-4-20250514"
	MaxTokens            = 4096
	SessionTimeout       = 30 * time.Minute
	MaxSessionHistory    = 50 // Maximum messages to keep in history
	RateLimitRequests    = 50 // Max requests per minute
	RateLimitWindow      = time.Minute
)

// ClaudeSession represents an active Claude API session
type ClaudeSession struct {
	ID           string            `json:"id"`
	WorkingDir   string            `json:"working_dir"`
	Context      string            `json:"context,omitempty"`
	Messages     []ClaudeMessage   `json:"messages"`
	CreatedAt    time.Time         `json:"created_at"`
	LastActivity time.Time         `json:"last_activity"`
	Status       string            `json:"status"` // active, streaming, completed, cancelled, error
	Error        string            `json:"error,omitempty"`
	mu           sync.Mutex
	cancelFunc   context.CancelFunc
}

// ClaudeContentBlock represents a content block in a message
type ClaudeContentBlock struct {
	Type      string          `json:"type"` // text, tool_use, tool_result
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ClaudeMessage represents a message in the conversation
type ClaudeMessage struct {
	Role      string               `json:"role"` // user, assistant
	Content   []ClaudeContentBlock `json:"content"`
	Timestamp time.Time            `json:"timestamp,omitzero"`
}

// ClaudeAPIRequest represents a request to the Claude API
type ClaudeAPIRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []ClaudeMessage `json:"messages"`
	Stream    bool            `json:"stream"`
	Tools     []ClaudeTool    `json:"tools,omitempty"`
}

// ClaudeTool represents a tool definition for Claude
type ClaudeTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ClaudeAPIResponse represents a response from the Claude API
type ClaudeAPIResponse struct {
	ID           string               `json:"id"`
	Type         string               `json:"type"`
	Role         string               `json:"role"`
	Content      []ClaudeContentBlock `json:"content"`
	Model        string               `json:"model"`
	StopReason   string               `json:"stop_reason"`
	StopSequence string               `json:"stop_sequence,omitempty"`
	Usage        ClaudeUsage          `json:"usage"`
}

// ClaudeUsage represents token usage information
type ClaudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ClaudeStreamEvent represents an SSE event from Claude's streaming API
type ClaudeStreamEvent struct {
	Type         string               `json:"type"`
	Index        int                  `json:"index,omitempty"`
	ContentBlock *ClaudeContentBlock  `json:"content_block,omitempty"`
	Delta        *ClaudeStreamDelta   `json:"delta,omitempty"`
	Message      *ClaudeAPIResponse   `json:"message,omitempty"`
	Usage        *ClaudeUsage         `json:"usage,omitempty"`
}

// ClaudeStreamDelta represents incremental content in streaming
type ClaudeStreamDelta struct {
	Type         string `json:"type,omitempty"`
	Text         string `json:"text,omitempty"`
	PartialJSON  string `json:"partial_json,omitempty"`
	StopReason   string `json:"stop_reason,omitempty"`
}

// ClaudeAPIClient handles communication with the Claude API
type ClaudeAPIClient struct {
	apiKey       string
	httpClient   *http.Client
	sessions     map[string]*ClaudeSession
	sessionsMu   sync.RWMutex
	rateLimiter  *RateLimiter
	sessionDir   string
}

// RateLimiter implements a simple sliding window rate limiter
type RateLimiter struct {
	requests []time.Time
	limit    int
	window   time.Duration
	mu       sync.Mutex
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make([]time.Time, 0),
		limit:    limit,
		window:   window,
	}
}

// Allow checks if a request is allowed under the rate limit
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Remove old requests outside the window
	newRequests := make([]time.Time, 0)
	for _, t := range rl.requests {
		if t.After(windowStart) {
			newRequests = append(newRequests, t)
		}
	}
	rl.requests = newRequests

	if len(rl.requests) >= rl.limit {
		return false
	}

	rl.requests = append(rl.requests, now)
	return true
}

// Wait blocks until a request is allowed
func (rl *RateLimiter) Wait(ctx context.Context) error {
	for {
		if rl.Allow() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
			// Retry
		}
	}
}

// NewClaudeAPIClient creates a new Claude API client
func NewClaudeAPIClient(sessionDir string) (*ClaudeAPIClient, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return nil, errors.New("ANTHROPIC_API_KEY environment variable not set")
	}

	if sessionDir == "" {
		sessionDir = filepath.Join(os.TempDir(), "claude-sessions")
	}
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	client := &ClaudeAPIClient{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		sessions:    make(map[string]*ClaudeSession),
		rateLimiter: NewRateLimiter(RateLimitRequests, RateLimitWindow),
		sessionDir:  sessionDir,
	}

	// Load existing sessions from disk
	client.loadSessions()

	// Start session cleanup goroutine
	go client.cleanupSessions()

	return client, nil
}

// CreateSession creates a new Claude session
func (c *ClaudeAPIClient) CreateSession(workingDir, context string, initialPrompt string) (*ClaudeSession, error) {
	sessionID := fmt.Sprintf("session-%d", time.Now().UnixNano())

	// Validate working directory
	if workingDir != "" {
		info, err := os.Stat(workingDir)
		if err != nil {
			return nil, fmt.Errorf("invalid working directory: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("working_dir is not a directory: %s", workingDir)
		}
	}

	session := &ClaudeSession{
		ID:           sessionID,
		WorkingDir:   workingDir,
		Context:      context,
		Messages:     make([]ClaudeMessage, 0),
		CreatedAt:    time.Now(),
		LastActivity: time.Now(),
		Status:       "active",
	}

	// Add initial user message if provided
	if initialPrompt != "" {
		session.Messages = append(session.Messages, ClaudeMessage{
			Role: "user",
			Content: []ClaudeContentBlock{
				{Type: "text", Text: initialPrompt},
			},
			Timestamp: time.Now(),
		})
	}

	c.sessionsMu.Lock()
	c.sessions[sessionID] = session
	c.sessionsMu.Unlock()

	// Save session to disk
	c.saveSession(session)

	return session, nil
}

// GetSession retrieves a session by ID
func (c *ClaudeAPIClient) GetSession(sessionID string) (*ClaudeSession, error) {
	c.sessionsMu.RLock()
	session, exists := c.sessions[sessionID]
	c.sessionsMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	return session, nil
}

// DeleteSession cancels and removes a session
func (c *ClaudeAPIClient) DeleteSession(sessionID string) error {
	c.sessionsMu.Lock()
	session, exists := c.sessions[sessionID]
	if exists {
		if session.cancelFunc != nil {
			session.cancelFunc()
		}
		delete(c.sessions, sessionID)
	}
	c.sessionsMu.Unlock()

	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	// Remove session file
	sessionPath := filepath.Join(c.sessionDir, sessionID+".json")
	os.Remove(sessionPath)

	return nil
}

// SendMessage sends a message to Claude and streams the response
func (c *ClaudeAPIClient) SendMessage(ctx context.Context, sessionID string, message string, streamCh chan<- ClaudeStreamEvent) error {
	session, err := c.GetSession(sessionID)
	if err != nil {
		return err
	}

	session.mu.Lock()
	if session.Status == "streaming" {
		session.mu.Unlock()
		return errors.New("session is currently streaming a response")
	}
	session.Status = "streaming"
	session.LastActivity = time.Now()

	// Add user message to history
	session.Messages = append(session.Messages, ClaudeMessage{
		Role: "user",
		Content: []ClaudeContentBlock{
			{Type: "text", Text: message},
		},
		Timestamp: time.Now(),
	})

	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)
	session.cancelFunc = cancel
	session.mu.Unlock()

	defer func() {
		session.mu.Lock()
		session.cancelFunc = nil
		if session.Status == "streaming" {
			session.Status = "active"
		}
		session.mu.Unlock()
		c.saveSession(session)
	}()

	// Wait for rate limit
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait cancelled: %w", err)
	}

	// Build system prompt
	systemPrompt := c.buildSystemPrompt(session)

	// Build request
	request := ClaudeAPIRequest{
		Model:     DefaultModel,
		MaxTokens: MaxTokens,
		System:    systemPrompt,
		Messages:  c.prepareMessages(session.Messages),
		Stream:    true,
		Tools:     c.getTools(),
	}

	return c.streamRequest(ctx, session, request, streamCh)
}

// streamRequest handles the streaming API request
func (c *ClaudeAPIClient) streamRequest(ctx context.Context, session *ClaudeSession, request ClaudeAPIRequest, streamCh chan<- ClaudeStreamEvent) error {
	for {
		response, err := c.doStreamRequest(ctx, request, streamCh)
		if err != nil {
			session.mu.Lock()
			session.Status = "error"
			session.Error = err.Error()
			session.mu.Unlock()
			return err
		}

		// Add assistant response to history
		session.mu.Lock()
		session.Messages = append(session.Messages, ClaudeMessage{
			Role:      "assistant",
			Content:   response.Content,
			Timestamp: time.Now(),
		})
		session.mu.Unlock()

		// Check if we need to handle tool use
		if response.StopReason == "tool_use" {
			toolResults, err := c.executeTools(ctx, session, response.Content)
			if err != nil {
				return err
			}

			// Add tool results to messages
			session.mu.Lock()
			session.Messages = append(session.Messages, ClaudeMessage{
				Role:      "user",
				Content:   toolResults,
				Timestamp: time.Now(),
			})
			session.mu.Unlock()

			// Update request with new messages and continue
			request.Messages = c.prepareMessages(session.Messages)

			// Wait for rate limit
			if err := c.rateLimiter.Wait(ctx); err != nil {
				return err
			}

			continue
		}

		// Done
		session.mu.Lock()
		session.Status = "active"
		session.mu.Unlock()
		break
	}

	// Trim history if too long
	c.trimSessionHistory(session)

	return nil
}

// doStreamRequest performs a single streaming API request
func (c *ClaudeAPIClient) doStreamRequest(ctx context.Context, request ClaudeAPIRequest, streamCh chan<- ClaudeStreamEvent) (*ClaudeAPIResponse, error) {
	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ClaudeAPIURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", ClaudeAPIVersion)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse SSE stream
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var response ClaudeAPIResponse
	var currentContentBlocks []ClaudeContentBlock

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event ClaudeStreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// Send event to channel
		if streamCh != nil {
			select {
			case streamCh <- event:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		// Build response from events
		switch event.Type {
		case "message_start":
			if event.Message != nil {
				response = *event.Message
			}
		case "content_block_start":
			if event.ContentBlock != nil {
				currentContentBlocks = append(currentContentBlocks, *event.ContentBlock)
			}
		case "content_block_delta":
			if event.Delta != nil && event.Index < len(currentContentBlocks) {
				block := &currentContentBlocks[event.Index]
				if event.Delta.Text != "" {
					block.Text += event.Delta.Text
				}
				if event.Delta.PartialJSON != "" {
					// Accumulate partial JSON for tool input
					var existingInput string
					if block.Input != nil {
						existingInput = string(block.Input)
					}
					block.Input = json.RawMessage(existingInput + event.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if event.Delta != nil {
				response.StopReason = event.Delta.StopReason
			}
			if event.Usage != nil {
				response.Usage = *event.Usage
			}
		case "message_stop":
			response.Content = currentContentBlocks
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream error: %w", err)
	}

	response.Content = currentContentBlocks
	return &response, nil
}

// executeTools executes tool calls from the assistant response
func (c *ClaudeAPIClient) executeTools(ctx context.Context, session *ClaudeSession, content []ClaudeContentBlock) ([]ClaudeContentBlock, error) {
	var results []ClaudeContentBlock

	for _, block := range content {
		if block.Type != "tool_use" {
			continue
		}

		result := c.executeTool(ctx, session, block)
		results = append(results, result)
	}

	return results, nil
}

// executeTool executes a single tool call
func (c *ClaudeAPIClient) executeTool(ctx context.Context, session *ClaudeSession, toolCall ClaudeContentBlock) ClaudeContentBlock {
	result := ClaudeContentBlock{
		Type:      "tool_result",
		ToolUseID: toolCall.ID,
	}

	var input map[string]any
	if err := json.Unmarshal(toolCall.Input, &input); err != nil {
		result.Content = fmt.Sprintf("Error parsing tool input: %v", err)
		result.IsError = true
		return result
	}

	switch toolCall.Name {
	case "read_file":
		result.Content = c.toolReadFile(session.WorkingDir, input)
	case "write_file":
		result.Content = c.toolWriteFile(session.WorkingDir, input)
	case "bash":
		result.Content = c.toolBash(ctx, session.WorkingDir, input)
	case "list_files":
		result.Content = c.toolListFiles(session.WorkingDir, input)
	default:
		result.Content = fmt.Sprintf("Unknown tool: %s", toolCall.Name)
		result.IsError = true
	}

	return result
}

// Tool implementations

func (c *ClaudeAPIClient) toolReadFile(workingDir string, input map[string]any) string {
	path, ok := input["path"].(string)
	if !ok {
		return "Error: path is required"
	}

	// Resolve path relative to working directory
	if !filepath.IsAbs(path) && workingDir != "" {
		path = filepath.Join(workingDir, path)
	}

	// Security check: ensure path doesn't escape working directory
	if workingDir != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Sprintf("Error resolving path: %v", err)
		}
		absWorkDir, _ := filepath.Abs(workingDir)
		if !strings.HasPrefix(absPath, absWorkDir) {
			return "Error: path must be within working directory"
		}
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("Error reading file: %v", err)
	}

	// Truncate if too large
	const maxSize = 100000
	if len(content) > maxSize {
		return string(content[:maxSize]) + "\n... (truncated)"
	}

	return string(content)
}

func (c *ClaudeAPIClient) toolWriteFile(workingDir string, input map[string]any) string {
	path, ok := input["path"].(string)
	if !ok {
		return "Error: path is required"
	}
	content, ok := input["content"].(string)
	if !ok {
		return "Error: content is required"
	}

	// Resolve path relative to working directory
	if !filepath.IsAbs(path) && workingDir != "" {
		path = filepath.Join(workingDir, path)
	}

	// Security check
	if workingDir != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Sprintf("Error resolving path: %v", err)
		}
		absWorkDir, _ := filepath.Abs(workingDir)
		if !strings.HasPrefix(absPath, absWorkDir) {
			return "Error: path must be within working directory"
		}
	}

	// Create parent directories if needed
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Sprintf("Error creating directories: %v", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Sprintf("Error writing file: %v", err)
	}

	return fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path)
}

func (c *ClaudeAPIClient) toolBash(ctx context.Context, workingDir string, input map[string]any) string {
	command, ok := input["command"].(string)
	if !ok {
		return "Error: command is required"
	}

	// Security: block dangerous commands
	dangerousPatterns := []string{"rm -rf /", ":(){ :|:& };:", "dd if=", "> /dev/sd"}
	for _, pattern := range dangerousPatterns {
		if strings.Contains(command, pattern) {
			return "Error: command blocked for safety reasons"
		}
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "Error: command timed out after 30 seconds"
		}
		return fmt.Sprintf("Command failed: %v\nOutput: %s", err, string(output))
	}

	// Truncate if too large
	const maxSize = 50000
	if len(output) > maxSize {
		return string(output[:maxSize]) + "\n... (truncated)"
	}

	return string(output)
}

func (c *ClaudeAPIClient) toolListFiles(workingDir string, input map[string]any) string {
	path, ok := input["path"].(string)
	if !ok {
		path = "."
	}

	// Resolve path relative to working directory
	if !filepath.IsAbs(path) && workingDir != "" {
		path = filepath.Join(workingDir, path)
	}

	// Security check
	if workingDir != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Sprintf("Error resolving path: %v", err)
		}
		absWorkDir, _ := filepath.Abs(workingDir)
		if !strings.HasPrefix(absPath, absWorkDir) {
			return "Error: path must be within working directory"
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf("Error listing directory: %v", err)
	}

	var result strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if entry.IsDir() {
			result.WriteString(fmt.Sprintf("%s/\n", entry.Name()))
		} else {
			result.WriteString(fmt.Sprintf("%s (%d bytes)\n", entry.Name(), info.Size()))
		}
	}

	return result.String()
}

// getTools returns the available tool definitions
func (c *ClaudeAPIClient) getTools() []ClaudeTool {
	return []ClaudeTool{
		{
			Name:        "read_file",
			Description: "Read the contents of a file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to the file to read (relative to working directory or absolute)",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "write_file",
			Description: "Write content to a file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The path to write to (relative to working directory or absolute)",
					},
					"content": map[string]any{
						"type":        "string",
						"description": "The content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			Name:        "bash",
			Description: "Execute a bash command",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{
						"type":        "string",
						"description": "The bash command to execute",
					},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "list_files",
			Description: "List files in a directory",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "The directory path to list (defaults to current directory)",
					},
				},
			},
		},
	}
}

// buildSystemPrompt builds the system prompt for a session
func (c *ClaudeAPIClient) buildSystemPrompt(session *ClaudeSession) string {
	var sb strings.Builder

	sb.WriteString("You are a helpful software engineering assistant. You have access to tools to read and write files, and execute bash commands.\n\n")

	if session.WorkingDir != "" {
		sb.WriteString(fmt.Sprintf("Working directory: %s\n\n", session.WorkingDir))
	}

	if session.Context != "" {
		sb.WriteString("Additional context:\n")
		sb.WriteString(session.Context)
		sb.WriteString("\n\n")
	}

	sb.WriteString("Guidelines:\n")
	sb.WriteString("- Use tools to explore and understand the codebase before making changes\n")
	sb.WriteString("- Be careful with file modifications and always verify your changes\n")
	sb.WriteString("- When executing bash commands, prefer safe, non-destructive operations\n")
	sb.WriteString("- Explain your actions and reasoning clearly\n")

	return sb.String()
}

// prepareMessages converts session messages for API request
func (c *ClaudeAPIClient) prepareMessages(messages []ClaudeMessage) []ClaudeMessage {
	result := make([]ClaudeMessage, 0, len(messages))
	for _, msg := range messages {
		// Create a clean copy without internal fields
		result = append(result, ClaudeMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}
	return result
}

// trimSessionHistory keeps only recent messages
func (c *ClaudeAPIClient) trimSessionHistory(session *ClaudeSession) {
	session.mu.Lock()
	defer session.mu.Unlock()

	if len(session.Messages) > MaxSessionHistory {
		// Keep the most recent messages
		session.Messages = session.Messages[len(session.Messages)-MaxSessionHistory:]
	}
}

// saveSession saves a session to disk
func (c *ClaudeAPIClient) saveSession(session *ClaudeSession) {
	session.mu.Lock()
	defer session.mu.Unlock()

	sessionPath := filepath.Join(c.sessionDir, session.ID+".json")
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return
	}

	os.WriteFile(sessionPath, data, 0644)
}

// loadSessions loads sessions from disk
func (c *ClaudeAPIClient) loadSessions() {
	entries, err := os.ReadDir(c.sessionDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		sessionPath := filepath.Join(c.sessionDir, entry.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		var session ClaudeSession
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		// Skip expired sessions
		if time.Since(session.LastActivity) > SessionTimeout {
			os.Remove(sessionPath)
			continue
		}

		c.sessions[session.ID] = &session
	}
}

// cleanupSessions periodically removes expired sessions
func (c *ClaudeAPIClient) cleanupSessions() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.sessionsMu.Lock()
		for id, session := range c.sessions {
			if time.Since(session.LastActivity) > SessionTimeout {
				if session.cancelFunc != nil {
					session.cancelFunc()
				}
				delete(c.sessions, id)

				// Remove session file
				sessionPath := filepath.Join(c.sessionDir, id+".json")
				os.Remove(sessionPath)
			}
		}
		c.sessionsMu.Unlock()
	}
}

// ListSessions returns all active sessions
func (c *ClaudeAPIClient) ListSessions() []*ClaudeSession {
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()

	sessions := make([]*ClaudeSession, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}
