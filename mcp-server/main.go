package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const orchestratorRoot = "/home/paul/go/src/github.com/PaulSnow/orchestrator"

// Request is a JSON-RPC-like request read from stdin.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     interface{}     `json:"id,omitempty"`
}

// Response is a JSON-RPC-like response written to stdout.
type Response struct {
	Result interface{} `json:"result,omitempty"`
	Error  *RpcError   `json:"error,omitempty"`
	ID     interface{} `json:"id,omitempty"`
}

// RpcError represents an error in the response.
type RpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	rootPath := orchestratorRoot

	// Allow override via environment variable.
	if env := os.Getenv("ORCHESTRATOR_ROOT"); env != "" {
		rootPath = env
	}

	// Also allow override via -root flag for convenience.
	for i, arg := range os.Args[1:] {
		if arg == "-root" && i+1 < len(os.Args)-1 {
			rootPath = os.Args[i+2]
		}
	}

	// Resolve to absolute path.
	absPath, err := filepath.Abs(rootPath)
	if err == nil {
		rootPath = absPath
	}

	srv, err := NewServer(rootPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize server: %v\n", err)
		os.Exit(1)
	}
	defer srv.Shutdown()

	fmt.Fprintf(os.Stderr, "orchestrator-mcp-server ready (root: %s)\n", rootPath)
	fmt.Fprintf(os.Stderr, "Reading JSON requests from stdin. One JSON object per line.\n")

	scanner := bufio.NewScanner(os.Stdin)
	// Allow up to 1MB per line for large responses.
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			writeResponse(Response{
				Error: &RpcError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}

		resp := dispatch(srv, req)
		resp.ID = req.ID
		writeResponse(resp)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "stdin read error: %v\n", err)
		os.Exit(1)
	}
}

func dispatch(srv *Server, req Request) Response {
	switch req.Method {

	case "scan-repos":
		result, err := ToolScanRepos(srv)
		return makeResponse(result, err)

	case "repo-status":
		name, err := extractStringParam(req.Params, "repo")
		if err != nil {
			return errorResponse(-32602, "invalid params: "+err.Error())
		}
		result, err := ToolRepoStatus(srv, name)
		return makeResponse(result, err)

	case "run-tests":
		name, err := extractStringParam(req.Params, "repo")
		if err != nil {
			return errorResponse(-32602, "invalid params: "+err.Error())
		}
		result, err := ToolRunTests(srv, name)
		return makeResponse(result, err)

	case "build-repo":
		name, err := extractStringParam(req.Params, "repo")
		if err != nil {
			return errorResponse(-32602, "invalid params: "+err.Error())
		}
		result, err := ToolBuildRepo(srv, name)
		return makeResponse(result, err)

	case "list-tasks":
		result, err := ToolListTasks(srv)
		return makeResponse(result, err)

	case "start-task":
		id, err := extractStringParam(req.Params, "id")
		if err != nil {
			return errorResponse(-32602, "invalid params: "+err.Error())
		}
		result, err := ToolStartTask(srv, id)
		return makeResponse(result, err)

	case "complete-task":
		id, err := extractStringParam(req.Params, "id")
		if err != nil {
			return errorResponse(-32602, "invalid params: "+err.Error())
		}
		result, err := ToolCompleteTask(srv, id)
		return makeResponse(result, err)

	case "list-tools":
		return Response{Result: listTools()}

	default:
		return errorResponse(-32601, "unknown method: "+req.Method)
	}
}

// listTools returns metadata about all available tools.
func listTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "scan-repos",
			"description": "Scan all configured repositories and return their git statuses",
			"params":      map[string]interface{}{},
		},
		{
			"name":        "repo-status",
			"description": "Get the git status of a single named repository",
			"params": map[string]interface{}{
				"repo": "string (required) - repository name",
			},
		},
		{
			"name":        "run-tests",
			"description": "Run tests for a named repository",
			"params": map[string]interface{}{
				"repo": "string (required) - repository name",
			},
		},
		{
			"name":        "build-repo",
			"description": "Build a named repository",
			"params": map[string]interface{}{
				"repo": "string (required) - repository name",
			},
		},
		{
			"name":        "list-tasks",
			"description": "List all backlog and active tasks",
			"params":      map[string]interface{}{},
		},
		{
			"name":        "start-task",
			"description": "Move a task from backlog to active by ID",
			"params": map[string]interface{}{
				"id": "string (required) - task ID",
			},
		},
		{
			"name":        "complete-task",
			"description": "Complete a task by ID (move from active to completed)",
			"params": map[string]interface{}{
				"id": "string (required) - task ID",
			},
		},
	}
}

// extractStringParam pulls a named string from JSON params.
// Accepts either {"name": "value"} or just "value" as a bare string.
func extractStringParam(raw json.RawMessage, key string) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%s is required", key)
	}

	// Try object form first: {"repo": "myrepo"}
	var obj map[string]interface{}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if v, ok := obj[key]; ok {
			if s, ok := v.(string); ok {
				return s, nil
			}
			return "", fmt.Errorf("%s must be a string", key)
		}
		return "", fmt.Errorf("%s is required", key)
	}

	// Try bare string: "myrepo"
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	return "", fmt.Errorf("params must be an object with %q key or a bare string", key)
}

func makeResponse(result string, err error) Response {
	if err != nil {
		return errorResponse(-32000, err.Error())
	}
	// Return the result string as raw JSON if it's valid JSON, otherwise as a string.
	var js json.RawMessage
	if json.Unmarshal([]byte(result), &js) == nil {
		return Response{Result: js}
	}
	return Response{Result: result}
}

func errorResponse(code int, msg string) Response {
	return Response{Error: &RpcError{Code: code, Message: msg}}
}

func writeResponse(resp Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		// Last resort: write a minimal error.
		fmt.Fprintf(os.Stdout, `{"error":{"code":-32603,"message":"internal marshal error"}}`+"\n")
		return
	}
	fmt.Fprintf(os.Stdout, "%s\n", data)
}
