package testutil

import (
	"embed"
	"path/filepath"
	"runtime"
)

//go:embed testdata/config/*.json
var configFixtures embed.FS

//go:embed testdata/state/workers/*.json
var stateFixtures embed.FS

//go:embed testdata/logs/*.txt
var logFixtures embed.FS

// FixturesDir returns the absolute path to the testdata directory.
// This is useful for tests that need to access fixture files directly.
func FixturesDir() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	return filepath.Join(filepath.Dir(filename), "testdata")
}

// ConfigFixturesDir returns the path to the config fixtures directory.
func ConfigFixturesDir() string {
	return filepath.Join(FixturesDir(), "config")
}

// StateFixturesDir returns the path to the state fixtures directory.
func StateFixturesDir() string {
	return filepath.Join(FixturesDir(), "state")
}

// LogFixturesDir returns the path to the log fixtures directory.
func LogFixturesDir() string {
	return filepath.Join(FixturesDir(), "logs")
}

// ReadConfigFixture reads a config fixture file by name.
func ReadConfigFixture(name string) ([]byte, error) {
	return configFixtures.ReadFile(filepath.Join("testdata/config", name))
}

// ReadStateFixture reads a state fixture file by relative path (e.g., "workers/worker-1.json").
func ReadStateFixture(relPath string) ([]byte, error) {
	return stateFixtures.ReadFile(filepath.Join("testdata/state", relPath))
}

// ReadLogFixture reads a log fixture file by name.
func ReadLogFixture(name string) ([]byte, error) {
	return logFixtures.ReadFile(filepath.Join("testdata/logs", name))
}

// ListConfigFixtures returns the names of all config fixture files.
func ListConfigFixtures() ([]string, error) {
	entries, err := configFixtures.ReadDir("testdata/config")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
