package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RepoConfig represents a single managed repository.
type RepoConfig struct {
	Name          string   `json:"name"`
	Platform      string   `json:"platform"`
	Remote        string   `json:"remote"`
	Local         string   `json:"local"`
	DefaultBranch string   `json:"default_branch"`
	Language      string   `json:"language"`
	HasClaudeMD   bool     `json:"has_claude_md"`
	Tags          []string `json:"tags"`
	Description   string   `json:"description"`
}

// ReposFile is the top-level structure of repos.json.
type ReposFile struct {
	Repositories []RepoConfig `json:"repositories"`
}

// Config holds the loaded orchestrator configuration.
type Config struct {
	Repos    ReposFile
	RepoMap  map[string]RepoConfig // keyed by name
	RootPath string                // orchestrator repo root
}

// Load reads configuration from the orchestrator root directory.
func Load(rootPath string) (*Config, error) {
	c := &Config{
		RootPath: rootPath,
		RepoMap:  make(map[string]RepoConfig),
	}

	reposPath := filepath.Join(rootPath, "config", "repos.json")
	data, err := os.ReadFile(reposPath)
	if err != nil {
		return nil, fmt.Errorf("reading repos.json: %w", err)
	}

	if err := json.Unmarshal(data, &c.Repos); err != nil {
		return nil, fmt.Errorf("parsing repos.json: %w", err)
	}

	for _, r := range c.Repos.Repositories {
		c.RepoMap[r.Name] = r
	}

	return c, nil
}

// GetRepo returns the configuration for a named repository.
func (c *Config) GetRepo(name string) (RepoConfig, bool) {
	r, ok := c.RepoMap[name]
	return r, ok
}

// AllRepos returns all configured repositories.
func (c *Config) AllRepos() []RepoConfig {
	return c.Repos.Repositories
}
