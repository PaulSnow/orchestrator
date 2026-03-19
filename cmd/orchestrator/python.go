package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const defaultPythonTimeout = 5 * time.Minute

var pythonInterpreter string

// findPythonInterpreter locates python3 or python in PATH.
func findPythonInterpreter() (string, error) {
	if pythonInterpreter != "" {
		return pythonInterpreter, nil
	}

	// Try python3 first, then python
	for _, name := range []string{"python3", "python"} {
		path, err := exec.LookPath(name)
		if err == nil {
			pythonInterpreter = path
			return pythonInterpreter, nil
		}
	}
	return "", fmt.Errorf("no Python interpreter found (tried python3, python)")
}

// getScriptsDir returns the path to the scripts directory.
func getScriptsDir() string {
	// Get the directory of the current executable
	execPath, err := os.Executable()
	if err != nil {
		// Fallback to current working directory
		cwd, _ := os.Getwd()
		return filepath.Join(cwd, "scripts")
	}
	// Assume scripts/ is at the repo root (3 levels up from cmd/orchestrator/binary)
	return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(execPath))), "scripts")
}

// RunPythonScript runs a Python script and returns its stdout.
// On non-zero exit, returns an error containing stderr content.
func RunPythonScript(scriptPath string, args ...string) (string, error) {
	return RunPythonScriptWithTimeout(scriptPath, defaultPythonTimeout, args...)
}

// RunPythonScriptWithTimeout runs a Python script with a custom timeout.
func RunPythonScriptWithTimeout(scriptPath string, timeout time.Duration, args ...string) (string, error) {
	return runPython(scriptPath, "", timeout, args...)
}

// RunPythonScriptWithInput runs a Python script with stdin input and returns its stdout.
func RunPythonScriptWithInput(scriptPath string, input string, args ...string) (string, error) {
	return runPython(scriptPath, input, defaultPythonTimeout, args...)
}

// RunPythonModule runs a Python module (python -m module) and returns its stdout.
func RunPythonModule(module string, args ...string) (string, error) {
	return runPythonModule(module, "", defaultPythonTimeout, args...)
}

// RunPythonModuleWithInput runs a Python module with stdin input.
func RunPythonModuleWithInput(module string, input string, args ...string) (string, error) {
	return runPythonModule(module, input, defaultPythonTimeout, args...)
}

// runPython is the internal implementation for running Python scripts.
func runPython(scriptPath string, input string, timeout time.Duration, args ...string) (string, error) {
	python, err := findPythonInterpreter()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdArgs := append([]string{scriptPath}, args...)
	cmd := exec.CommandContext(ctx, python, cmdArgs...)

	// Set PYTHONPATH to include scripts directory
	scriptsDir := getScriptsDir()
	env := os.Environ()
	pythonPath := scriptsDir
	for _, e := range env {
		if len(e) > 11 && e[:11] == "PYTHONPATH=" {
			pythonPath = e[11:] + ":" + scriptsDir
			break
		}
	}
	cmd.Env = append(env, "PYTHONPATH="+pythonPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if input != "" {
		cmd.Stdin = bytes.NewBufferString(input)
	}

	err = cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.String(), fmt.Errorf("timeout after %v: %w", timeout, err)
		}
		stderrStr := stderr.String()
		if stderrStr != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, stderrStr)
		}
		return stdout.String(), err
	}

	return stdout.String(), nil
}

// runPythonModule is the internal implementation for running Python modules.
func runPythonModule(module string, input string, timeout time.Duration, args ...string) (string, error) {
	python, err := findPythonInterpreter()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmdArgs := append([]string{"-m", module}, args...)
	cmd := exec.CommandContext(ctx, python, cmdArgs...)

	// Set PYTHONPATH to include scripts directory
	scriptsDir := getScriptsDir()
	env := os.Environ()
	pythonPath := scriptsDir
	for _, e := range env {
		if len(e) > 11 && e[:11] == "PYTHONPATH=" {
			pythonPath = e[11:] + ":" + scriptsDir
			break
		}
	}
	cmd.Env = append(env, "PYTHONPATH="+pythonPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if input != "" {
		cmd.Stdin = bytes.NewBufferString(input)
	}

	err = cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return stdout.String(), fmt.Errorf("timeout after %v: %w", timeout, err)
		}
		stderrStr := stderr.String()
		if stderrStr != "" {
			return stdout.String(), fmt.Errorf("%w: %s", err, stderrStr)
		}
		return stdout.String(), err
	}

	return stdout.String(), nil
}
