package orchestrator

import "sync"

// Version information (set by main.go at startup)
var (
	version   = "dev"
	gitCommit = "unknown"
	buildDate = "unknown"
	mu        sync.RWMutex
)

// SetVersion sets the version information for the orchestrator.
// Called by main.go at startup with build-time values.
func SetVersion(v, commit, date string) {
	mu.Lock()
	defer mu.Unlock()
	if v != "" {
		version = v
	}
	if commit != "" {
		gitCommit = commit
	}
	if date != "" {
		buildDate = date
	}
}

// GetVersion returns the current version.
func GetVersion() string {
	mu.RLock()
	defer mu.RUnlock()
	return version
}

// GetGitCommit returns the git commit hash.
func GetGitCommit() string {
	mu.RLock()
	defer mu.RUnlock()
	return gitCommit
}

// GetBuildDate returns the build date.
func GetBuildDate() string {
	mu.RLock()
	defer mu.RUnlock()
	return buildDate
}

// GetVersionInfo returns all version information as a formatted string.
func GetVersionInfo() string {
	mu.RLock()
	defer mu.RUnlock()
	info := version
	if gitCommit != "unknown" && gitCommit != "" {
		info += " (" + gitCommit[:7] + ")"
	}
	return info
}
