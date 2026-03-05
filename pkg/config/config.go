package config

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the opentree configuration
type Config struct {
	Agent    AgentConfig    `toml:"agent"`
	Worktree WorktreeConfig `toml:"worktree"`
	Tmux     TmuxConfig     `toml:"tmux"`
	GitHub   GitHubConfig   `toml:"github"`
}

// AgentConfig configures the coding agent
type AgentConfig struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// WorktreeConfig configures git worktree behavior
type WorktreeConfig struct {
	BaseDir     string `toml:"base_dir"`
	DefaultBase string `toml:"default_base"`
}

// TmuxConfig configures tmux behavior
type TmuxConfig struct {
	SessionPrefix string `toml:"session_prefix"`
}

// GitHubConfig configures GitHub integration
type GitHubConfig struct {
	AutoPush bool `toml:"auto_push"`
}

// Default returns the default configuration
func Default() *Config {
	return &Config{
		Agent: AgentConfig{
			Command: "opencode",
			Args:    []string{},
		},
		Worktree: WorktreeConfig{
			BaseDir:     ".opentree",
			DefaultBase: "main",
		},
		Tmux: TmuxConfig{
			SessionPrefix: "opentree",
		},
		GitHub: GitHubConfig{
			AutoPush: false,
		},
	}
}

// Load reads configuration from a file, falling back to defaults
func Load(path string) (*Config, error) {
	cfg := Default()
	
	// If no path specified, look for opentree.toml in current directory
	if path == "" {
		path = "opentree.toml"
	}
	
	// If file doesn't exist, return defaults
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}
	
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	
	return cfg, nil
}

// Save writes the configuration to a file
func Save(cfg *Config, path string) error {
	if path == "" {
		path = "opentree.toml"
	}
	
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	
	data, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	
	return os.WriteFile(path, data, 0644)
}
