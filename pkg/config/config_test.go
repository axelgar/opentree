package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg == nil {
		t.Fatal("Default() returned nil")
	}
	if cfg.Agent.Command != "opencode" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "opencode")
	}
	if cfg.Agent.Args == nil {
		t.Error("Agent.Args should not be nil")
	}
	if cfg.Worktree.BaseDir != ".opentree" {
		t.Errorf("Worktree.BaseDir = %q, want %q", cfg.Worktree.BaseDir, ".opentree")
	}
	if cfg.Worktree.DefaultBase != "main" {
		t.Errorf("Worktree.DefaultBase = %q, want %q", cfg.Worktree.DefaultBase, "main")
	}
	if cfg.Tmux.SessionPrefix != "opentree" {
		t.Errorf("Tmux.SessionPrefix = %q, want %q", cfg.Tmux.SessionPrefix, "opentree")
	}
	if cfg.GitHub.AutoPush != false {
		t.Errorf("GitHub.AutoPush = %v, want false", cfg.GitHub.AutoPush)
	}
}

func TestLoad_NonExistentFile_ReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("Load() with non-existent file failed: %v", err)
	}
	defaults := Default()
	if cfg.Agent.Command != defaults.Agent.Command {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, defaults.Agent.Command)
	}
	if cfg.Worktree.BaseDir != defaults.Worktree.BaseDir {
		t.Errorf("Worktree.BaseDir = %q, want %q", cfg.Worktree.BaseDir, defaults.Worktree.BaseDir)
	}
}

func TestLoad_ValidTOML(t *testing.T) {
	toml := `
[agent]
command = "custom-agent"
args = ["--flag", "--other"]

[worktree]
base_dir = ".custom"
default_base = "develop"

[tmux]
session_prefix = "myapp"

[github]
auto_push = true
`
	path := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Agent.Command != "custom-agent" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "custom-agent")
	}
	if len(cfg.Agent.Args) != 2 || cfg.Agent.Args[0] != "--flag" {
		t.Errorf("Agent.Args = %v, want [--flag --other]", cfg.Agent.Args)
	}
	if cfg.Worktree.BaseDir != ".custom" {
		t.Errorf("Worktree.BaseDir = %q, want %q", cfg.Worktree.BaseDir, ".custom")
	}
	if cfg.Worktree.DefaultBase != "develop" {
		t.Errorf("Worktree.DefaultBase = %q, want %q", cfg.Worktree.DefaultBase, "develop")
	}
	if cfg.Tmux.SessionPrefix != "myapp" {
		t.Errorf("Tmux.SessionPrefix = %q, want %q", cfg.Tmux.SessionPrefix, "myapp")
	}
	if !cfg.GitHub.AutoPush {
		t.Error("GitHub.AutoPush = false, want true")
	}
}

func TestLoad_PartialTOML_MergesWithDefaults(t *testing.T) {
	toml := `
[agent]
command = "override"
`
	path := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Agent.Command != "override" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "override")
	}
	// Fields not set in TOML should remain at default values.
	if cfg.Worktree.BaseDir != ".opentree" {
		t.Errorf("Worktree.BaseDir = %q, want default %q", cfg.Worktree.BaseDir, ".opentree")
	}
	if cfg.Tmux.SessionPrefix != "opentree" {
		t.Errorf("Tmux.SessionPrefix = %q, want default %q", cfg.Tmux.SessionPrefix, "opentree")
	}
}

func TestLoad_MalformedTOML_ReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(path, []byte("this is not [valid toml !!@@"), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() with malformed TOML expected error, got nil")
	}
}

func TestSave_And_Load_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opentree.toml")

	original := &Config{
		Agent: AgentConfig{
			Command: "my-agent",
			Args:    []string{"-v", "--debug"},
		},
		Worktree: WorktreeConfig{
			BaseDir:     ".trees",
			DefaultBase: "develop",
		},
		Tmux: TmuxConfig{
			SessionPrefix: "proj",
		},
		GitHub: GitHubConfig{
			AutoPush: true,
		},
	}

	if err := Save(original, path); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after Save() failed: %v", err)
	}

	if loaded.Agent.Command != original.Agent.Command {
		t.Errorf("Agent.Command = %q, want %q", loaded.Agent.Command, original.Agent.Command)
	}
	if len(loaded.Agent.Args) != len(original.Agent.Args) {
		t.Errorf("Agent.Args len = %d, want %d", len(loaded.Agent.Args), len(original.Agent.Args))
	}
	if loaded.Worktree.BaseDir != original.Worktree.BaseDir {
		t.Errorf("Worktree.BaseDir = %q, want %q", loaded.Worktree.BaseDir, original.Worktree.BaseDir)
	}
	if loaded.Worktree.DefaultBase != original.Worktree.DefaultBase {
		t.Errorf("Worktree.DefaultBase = %q, want %q", loaded.Worktree.DefaultBase, original.Worktree.DefaultBase)
	}
	if loaded.Tmux.SessionPrefix != original.Tmux.SessionPrefix {
		t.Errorf("Tmux.SessionPrefix = %q, want %q", loaded.Tmux.SessionPrefix, original.Tmux.SessionPrefix)
	}
	if loaded.GitHub.AutoPush != original.GitHub.AutoPush {
		t.Errorf("GitHub.AutoPush = %v, want %v", loaded.GitHub.AutoPush, original.GitHub.AutoPush)
	}
}

func TestAgentConfig_Validate_EmptyCommand(t *testing.T) {
	a := AgentConfig{Command: ""}
	if err := a.Validate(); err == nil {
		t.Fatal("Validate() with empty command expected error, got nil")
	}
}

func TestAgentConfig_Validate_MissingBinary(t *testing.T) {
	a := AgentConfig{Command: "nonexistent-binary-xyz-12345"}
	if err := a.Validate(); err == nil {
		t.Fatal("Validate() with missing binary expected error, got nil")
	}
}

func TestAgentConfig_Validate_ValidBinary(t *testing.T) {
	// "go" should be available in any Go test environment.
	a := AgentConfig{Command: "go"}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() with valid binary failed: %v", err)
	}
}

func TestAgentConfig_CommandLine_NoArgs(t *testing.T) {
	a := AgentConfig{Command: "opencode"}
	if got := a.CommandLine(); got != "opencode" {
		t.Errorf("CommandLine() = %q, want %q", got, "opencode")
	}
}

func TestAgentConfig_CommandLine_WithArgs(t *testing.T) {
	a := AgentConfig{Command: "claude", Args: []string{"--flag", "value"}}
	want := "claude --flag value"
	if got := a.CommandLine(); got != want {
		t.Errorf("CommandLine() = %q, want %q", got, want)
	}
}

func TestSave_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "dir", "opentree.toml")

	if err := Save(Default(), path); err != nil {
		t.Fatalf("Save() to nested path failed: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Config file not created at %q: %v", path, err)
	}
}
