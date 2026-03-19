package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func getTestScriptPath(t *testing.T) string {
	// Find the test_runner.py script relative to the test file
	// We're in cmd/orchestrator/, script is in scripts/
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Try different paths to find the script
	candidates := []string{
		filepath.Join(cwd, "scripts", "test_runner.py"),
		filepath.Join(cwd, "..", "..", "scripts", "test_runner.py"),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	t.Fatalf("could not find test_runner.py in any of: %v", candidates)
	return ""
}

func TestFindPythonInterpreter(t *testing.T) {
	// Reset cached interpreter for clean test
	pythonInterpreter = ""

	python, err := findPythonInterpreter()
	if err != nil {
		t.Fatalf("findPythonInterpreter() error = %v", err)
	}
	if python == "" {
		t.Fatal("findPythonInterpreter() returned empty string")
	}
	if !strings.Contains(python, "python") {
		t.Errorf("findPythonInterpreter() = %v, expected path containing 'python'", python)
	}
}

func TestRunPythonScript(t *testing.T) {
	scriptPath := getTestScriptPath(t)

	t.Run("no args", func(t *testing.T) {
		output, err := RunPythonScript(scriptPath)
		if err != nil {
			t.Fatalf("RunPythonScript() error = %v", err)
		}
		expected := "test_runner: no args\n"
		if output != expected {
			t.Errorf("RunPythonScript() = %q, want %q", output, expected)
		}
	})

	t.Run("with args", func(t *testing.T) {
		output, err := RunPythonScript(scriptPath, "hello", "world")
		if err != nil {
			t.Fatalf("RunPythonScript() error = %v", err)
		}
		expected := "hello world\n"
		if output != expected {
			t.Errorf("RunPythonScript() = %q, want %q", output, expected)
		}
	})

	t.Run("captures stdout correctly", func(t *testing.T) {
		output, err := RunPythonScript(scriptPath, "foo", "bar", "baz")
		if err != nil {
			t.Fatalf("RunPythonScript() error = %v", err)
		}
		if !strings.Contains(output, "foo bar baz") {
			t.Errorf("RunPythonScript() = %q, should contain 'foo bar baz'", output)
		}
	})
}

func TestRunPythonScriptWithInput(t *testing.T) {
	scriptPath := getTestScriptPath(t)

	t.Run("echo stdin", func(t *testing.T) {
		input := "hello from stdin"
		output, err := RunPythonScriptWithInput(scriptPath, input, "--echo-stdin")
		if err != nil {
			t.Fatalf("RunPythonScriptWithInput() error = %v", err)
		}
		if !strings.Contains(output, input) {
			t.Errorf("RunPythonScriptWithInput() = %q, should contain %q", output, input)
		}
	})
}

func TestRunPythonScriptError(t *testing.T) {
	scriptPath := getTestScriptPath(t)

	t.Run("non-zero exit code", func(t *testing.T) {
		_, err := RunPythonScript(scriptPath, "--exit-code", "1")
		if err == nil {
			t.Fatal("RunPythonScript() expected error for exit code 1, got nil")
		}
	})

	t.Run("stderr in error message", func(t *testing.T) {
		_, err := RunPythonScript(scriptPath, "--to-stderr", "error message", "--exit-code", "1")
		if err == nil {
			t.Fatal("RunPythonScript() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "error message") {
			t.Errorf("error = %v, should contain 'error message'", err)
		}
	})
}

func TestRunPythonScriptTimeout(t *testing.T) {
	scriptPath := getTestScriptPath(t)

	t.Run("timeout triggers", func(t *testing.T) {
		start := time.Now()
		_, err := RunPythonScriptWithTimeout(scriptPath, 100*time.Millisecond, "--sleep", "10")
		elapsed := time.Since(start)

		if err == nil {
			t.Fatal("RunPythonScriptWithTimeout() expected timeout error, got nil")
		}
		if !strings.Contains(err.Error(), "timeout") {
			t.Errorf("error = %v, should contain 'timeout'", err)
		}
		// Should complete in roughly the timeout period, not 10 seconds
		if elapsed > 2*time.Second {
			t.Errorf("timeout took %v, expected ~100ms", elapsed)
		}
	})
}

func TestRunPythonModule(t *testing.T) {
	// Test with a built-in module that should be available
	t.Run("run json.tool module", func(t *testing.T) {
		input := `{"hello": "world"}`
		output, err := RunPythonModuleWithInput("json.tool", input)
		if err != nil {
			t.Fatalf("RunPythonModuleWithInput() error = %v", err)
		}
		if !strings.Contains(output, "hello") {
			t.Errorf("RunPythonModuleWithInput() = %q, should contain 'hello'", output)
		}
	})
}

func TestGetScriptsDir(t *testing.T) {
	scriptsDir := getScriptsDir()
	if scriptsDir == "" {
		t.Error("getScriptsDir() returned empty string")
	}
	if !strings.Contains(scriptsDir, "scripts") {
		t.Errorf("getScriptsDir() = %v, expected path containing 'scripts'", scriptsDir)
	}
}
