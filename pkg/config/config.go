package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

// Validate checks that the agent command exists on PATH.
func (a AgentConfig) Validate() error {
	if a.Command == "" {
		return fmt.Errorf("agent command is empty")
	}
	if _, err := exec.LookPath(a.Command); err != nil {
		return fmt.Errorf("agent command %q not found on PATH: %w", a.Command, err)
	}
	return nil
}

// CommandLine returns the full command string (command + args) for shell execution.
func (a AgentConfig) CommandLine() string {
	if len(a.Args) == 0 {
		return a.Command
	}
	return a.Command + " " + strings.Join(a.Args, " ")
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
	AutoPush *bool `toml:"auto_push,omitempty"`
}

// ConfigSource tracks which config file provided each value.
type ConfigSource struct {
	AgentCommand        string
	AgentArgs           string
	WorktreeBaseDir     string
	WorktreeDefaultBase string
	TmuxSessionPrefix   string
	GitHubAutoPush      string
}

const (
	SourceDefault = "default"
	SourceGlobal  = "global"
	SourceRepo    = "repo"
)

// boolPtr returns a pointer to b.
func boolPtr(b bool) *bool { return &b }

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
			AutoPush: boolPtr(false),
		},
	}
}

// GlobalConfigPath returns the path to the global config file:
// $XDG_CONFIG_HOME/opentree/opentree.toml or ~/.config/opentree/opentree.toml.
func GlobalConfigPath() string {
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		xdgConfig = filepath.Join(home, ".config")
	}
	return filepath.Join(xdgConfig, "opentree", "opentree.toml")
}

// FindConfigFile walks up from the current directory looking for opentree.toml,
// mirroring how git finds .git. Returns "opentree.toml" if nothing is found.
func FindConfigFile() string {
	dir, err := os.Getwd()
	if err != nil {
		return "opentree.toml"
	}
	for {
		candidate := filepath.Join(dir, "opentree.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "opentree.toml"
}

// loadRaw reads a TOML file into a Config without applying defaults.
// Returns nil config (not an error) if the file doesn't exist.
func loadRaw(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// mergeInto applies non-zero values from src onto dst.
// For slices, a non-nil (even empty) src slice replaces dst.
func mergeInto(dst, src *Config) {
	if src == nil {
		return
	}
	if src.Agent.Command != "" {
		dst.Agent.Command = src.Agent.Command
	}
	if src.Agent.Args != nil {
		dst.Agent.Args = src.Agent.Args
	}
	if src.Worktree.BaseDir != "" {
		dst.Worktree.BaseDir = src.Worktree.BaseDir
	}
	if src.Worktree.DefaultBase != "" {
		dst.Worktree.DefaultBase = src.Worktree.DefaultBase
	}
	if src.Tmux.SessionPrefix != "" {
		dst.Tmux.SessionPrefix = src.Tmux.SessionPrefix
	}
	if src.GitHub.AutoPush != nil {
		dst.GitHub.AutoPush = src.GitHub.AutoPush
	}
}

// computeSources compares a resolved config against global and repo raw configs
// to determine which source provided each final value.
func computeSources(resolved, global, repo *Config) ConfigSource {
	src := ConfigSource{
		AgentCommand:        SourceDefault,
		AgentArgs:           SourceDefault,
		WorktreeBaseDir:     SourceDefault,
		WorktreeDefaultBase: SourceDefault,
		TmuxSessionPrefix:   SourceDefault,
		GitHubAutoPush:      SourceDefault,
	}

	if global != nil && global.Agent.Command != "" {
		src.AgentCommand = SourceGlobal
	}
	if repo != nil && repo.Agent.Command != "" {
		src.AgentCommand = SourceRepo
	}

	if global != nil && global.Agent.Args != nil {
		src.AgentArgs = SourceGlobal
	}
	if repo != nil && repo.Agent.Args != nil {
		src.AgentArgs = SourceRepo
	}

	if global != nil && global.Worktree.BaseDir != "" {
		src.WorktreeBaseDir = SourceGlobal
	}
	if repo != nil && repo.Worktree.BaseDir != "" {
		src.WorktreeBaseDir = SourceRepo
	}

	if global != nil && global.Worktree.DefaultBase != "" {
		src.WorktreeDefaultBase = SourceGlobal
	}
	if repo != nil && repo.Worktree.DefaultBase != "" {
		src.WorktreeDefaultBase = SourceRepo
	}

	if global != nil && global.Tmux.SessionPrefix != "" {
		src.TmuxSessionPrefix = SourceGlobal
	}
	if repo != nil && repo.Tmux.SessionPrefix != "" {
		src.TmuxSessionPrefix = SourceRepo
	}

	if global != nil && global.GitHub.AutoPush != nil {
		src.GitHubAutoPush = SourceGlobal
	}
	if repo != nil && repo.GitHub.AutoPush != nil {
		src.GitHubAutoPush = SourceRepo
	}

	return src
}

// LoadWithSources loads configuration with merge precedence:
// hardcoded defaults → global config → repo config.
// Also returns a ConfigSource indicating which source provided each value.
func LoadWithSources(repoPath string) (*Config, ConfigSource, error) {
	if repoPath == "" {
		repoPath = FindConfigFile()
	}

	globalPath := GlobalConfigPath()

	globalCfg, err := loadRaw(globalPath)
	if err != nil {
		return nil, ConfigSource{}, fmt.Errorf("failed to read global config %s: %w", globalPath, err)
	}

	repoCfg, err := loadRaw(repoPath)
	if err != nil {
		return nil, ConfigSource{}, fmt.Errorf("failed to read repo config %s: %w", repoPath, err)
	}

	resolved := Default()
	mergeInto(resolved, globalCfg)
	mergeInto(resolved, repoCfg)

	sources := computeSources(resolved, globalCfg, repoCfg)
	return resolved, sources, nil
}

// Load reads configuration from a file, falling back to defaults.
// Merge precedence: defaults → global config → repo config.
func Load(path string) (*Config, error) {
	cfg, _, err := LoadWithSources(path)
	return cfg, err
}

// LoadGlobal reads only the global config file, returning defaults if it doesn't exist.
func LoadGlobal() (*Config, error) {
	cfg := Default()
	path := GlobalConfigPath()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
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

// SaveGlobal writes the configuration to the global config file.
func SaveGlobal(cfg *Config) error {
	path := GlobalConfigPath()
	if path == "" {
		return fmt.Errorf("could not determine global config path: home directory not found")
	}
	return Save(cfg, path)
}
