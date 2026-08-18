package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	if cfg.Worktree.BaseDir != ".opentree" {
		t.Errorf("Worktree.BaseDir = %q, want %q", cfg.Worktree.BaseDir, ".opentree")
	}
	if cfg.Worktree.DefaultBase != "main" {
		t.Errorf("Worktree.DefaultBase = %q, want %q", cfg.Worktree.DefaultBase, "main")
	}
	if cfg.Tmux.SessionPrefix != "opentree" {
		t.Errorf("Tmux.SessionPrefix = %q, want %q", cfg.Tmux.SessionPrefix, "opentree")
	}
	if cfg.GitHub.AutoPush == nil || *cfg.GitHub.AutoPush != true {
		t.Errorf("GitHub.AutoPush = %v, want true", cfg.GitHub.AutoPush)
	}
}

func TestLoad_NonExistentFile_ReturnsDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no agents installed → detection is a no-op
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
	if cfg.Worktree.BaseDir != ".custom" {
		t.Errorf("Worktree.BaseDir = %q, want %q", cfg.Worktree.BaseDir, ".custom")
	}
	if cfg.Worktree.DefaultBase != "develop" {
		t.Errorf("Worktree.DefaultBase = %q, want %q", cfg.Worktree.DefaultBase, "develop")
	}
	if cfg.Tmux.SessionPrefix != "myapp" {
		t.Errorf("Tmux.SessionPrefix = %q, want %q", cfg.Tmux.SessionPrefix, "myapp")
	}
	if cfg.GitHub.AutoPush == nil || !*cfg.GitHub.AutoPush {
		t.Error("GitHub.AutoPush = false/nil, want true")
	}
}

func TestLoad_PartialTOML_MergesWithDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
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

func TestLoad_WorkspaceBlock(t *testing.T) {
	toml := `
[workspace]
setup = ["pnpm install --frozen-lockfile"]
seed = [".env", ".npmrc"]
run = "pnpm dev"
`
	path := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if got := strings.Join(cfg.Workspace.Setup, "|"); got != "pnpm install --frozen-lockfile" {
		t.Errorf("Workspace.Setup = %v", cfg.Workspace.Setup)
	}
	if got := strings.Join(cfg.Workspace.Seed, "|"); got != ".env|.npmrc" {
		t.Errorf("Workspace.Seed = %v", cfg.Workspace.Seed)
	}
	if cfg.Workspace.Run != "pnpm dev" {
		t.Errorf("Workspace.Run = %q, want %q", cfg.Workspace.Run, "pnpm dev")
	}
}

// A repository that seeds nothing is the default, and it has to be sayable: an
// empty list in the repo config must beat a global one rather than being read
// as "unset, inherit".
func TestLoad_EmptySeedListOverridesGlobal(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("[workspace]\nseed = [\".env\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	repoPath := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(repoPath, []byte("[workspace]\nseed = []\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(repoPath)
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if len(cfg.Workspace.Seed) != 0 {
		t.Errorf("Workspace.Seed = %v, want nothing", cfg.Workspace.Seed)
	}
	if sources.WorkspaceSeed != SourceRepo {
		t.Errorf("sources.WorkspaceSeed = %q, want %q", sources.WorkspaceSeed, SourceRepo)
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

func TestSetKeys_And_Load_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opentree.toml")

	original := &Config{
		Agent: AgentConfig{
			Command: "my-agent",
		},
		Worktree: WorktreeConfig{
			BaseDir:     ".trees",
			DefaultBase: "develop",
		},
		Tmux: TmuxConfig{
			SessionPrefix: "proj",
		},
		GitHub: GitHubConfig{
			AutoPush: boolPtr(true),
		},
	}

	if err := SetKeys(path, map[string]any{
		"agent.command":         original.Agent.Command,
		"worktree.base_dir":     original.Worktree.BaseDir,
		"worktree.default_base": original.Worktree.DefaultBase,
		"tmux.session_prefix":   original.Tmux.SessionPrefix,
		"github.auto_push":      *original.GitHub.AutoPush,
	}); err != nil {
		t.Fatalf("SetKeys() failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() after SetKeys() failed: %v", err)
	}

	if loaded.Agent.Command != original.Agent.Command {
		t.Errorf("Agent.Command = %q, want %q", loaded.Agent.Command, original.Agent.Command)
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
	if loaded.GitHub.AutoPush == nil || *loaded.GitHub.AutoPush != *original.GitHub.AutoPush {
		t.Errorf("GitHub.AutoPush = %v, want %v", loaded.GitHub.AutoPush, *original.GitHub.AutoPush)
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
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "opencode")
	t.Setenv("PATH", binDir)

	a := AgentConfig{Command: "opencode"}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() with an installed registry agent failed: %v", err)
	}
}

// Being installed is not enough. opentree only speaks ACP, so a binary it has
// no spec for cannot be run at all — and catching that here is the difference
// between one clear message and a chat that opens and immediately dies.
func TestAgentConfig_Validate_RejectsAgentOutsideTheRegistry(t *testing.T) {
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "codex") // installed, but opentree cannot drive it
	t.Setenv("PATH", binDir)

	err := AgentConfig{Command: "codex"}.Validate()
	if err == nil {
		t.Fatal("Validate() accepted an agent with no ACP spec")
	}
	// The message has to name the way out, or the only remedy is guesswork.
	if !strings.Contains(err.Error(), "opencode") {
		t.Errorf("error = %q, want it to list the agents that do work", err)
	}
}

func TestGlobalConfigPath_UsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got := GlobalConfigPath()
	want := filepath.Join(dir, "opentree", "opentree.toml")
	if got != want {
		t.Errorf("GlobalConfigPath() = %q, want %q", got, want)
	}
}

func TestGlobalConfigPath_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	got := GlobalConfigPath()
	if got == "" {
		t.Fatal("GlobalConfigPath() returned empty string")
	}
	if filepath.Base(filepath.Dir(got)) != "opentree" {
		t.Errorf("GlobalConfigPath() = %q, expected .../opentree/opentree.toml", got)
	}
}

func TestLoadGlobal_NonExistent_ReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal() failed: %v", err)
	}
	defaults := Default()
	if cfg.Agent.Command != defaults.Agent.Command {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, defaults.Agent.Command)
	}
}

func TestLoadGlobal_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	// SetKeys is the production path that writes the global config file.
	if err := SetKeys(GlobalConfigPath(), map[string]any{
		"agent.command":         "my-agent",
		"worktree.default_base": "develop",
	}); err != nil {
		t.Fatalf("SetKeys(global): %v", err)
	}

	loaded, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal() failed: %v", err)
	}

	if loaded.Agent.Command != "my-agent" {
		t.Errorf("Agent.Command = %q, want %q", loaded.Agent.Command, "my-agent")
	}
	if loaded.Worktree.DefaultBase != "develop" {
		t.Errorf("Worktree.DefaultBase = %q, want %q", loaded.Worktree.DefaultBase, "develop")
	}
}

func TestLoadWithSources_GlobalOverridesDefault(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	globalToml := `
[agent]
command = "global-agent"
`
	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(globalToml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}

	if cfg.Agent.Command != "global-agent" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "global-agent")
	}
	if sources.AgentCommand != SourceGlobal {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceGlobal)
	}
	if sources.WorktreeBaseDir != SourceDefault {
		t.Errorf("sources.WorktreeBaseDir = %q, want %q", sources.WorktreeBaseDir, SourceDefault)
	}
}

// fakeAgentBinary drops an executable stub named cmd into dir.
func fakeAgentBinary(t *testing.T, dir, cmd string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, cmd), []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestLoadWithSources_DetectsInstalledAgent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "claude")
	t.Setenv("PATH", binDir)

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	// claude is second in the registry, so this is detection finding the only
	// installed agent rather than falling through to the first entry.
	if cfg.Agent.Command != "claude" {
		t.Errorf("Agent.Command = %q, want detected %q", cfg.Agent.Command, "claude")
	}
	if sources.AgentCommand != SourceDefault {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceDefault)
	}
}

// Detection must never override an agent set in a config file.
func TestLoadWithSources_ConfigBeatsDetection(t *testing.T) {
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "opencode")
	t.Setenv("PATH", binDir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	repoDir := t.TempDir()
	repoPath := filepath.Join(repoDir, "opentree.toml")
	if err := os.WriteFile(repoPath, []byte("[agent]\ncommand = \"my-agent\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, sources, err := LoadWithSources(repoPath)
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if cfg.Agent.Command != "my-agent" {
		t.Errorf("Agent.Command = %q, want configured %q", cfg.Agent.Command, "my-agent")
	}
	if sources.AgentCommand != SourceRepo {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceRepo)
	}
}

// agent.args used to exist and is now unread. Someone upgrading has it sitting
// in their config file, and the loader has to walk past it rather than refuse.
func TestLoad_IgnoresRetiredAgentArgsKey(t *testing.T) {
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "opencode")
	t.Setenv("PATH", binDir)

	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("[agent]\ncommand = \"claude\"\nargs = [\"--yolo\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if cfg.Agent.Command != "claude" {
		t.Errorf("Agent.Command = %q, want %q — a stale args key must not derail the rest", cfg.Agent.Command, "claude")
	}
	if sources.AgentCommand != SourceGlobal {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceGlobal)
	}
}

func TestLoadWithSources_RepoOverridesGlobal(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	globalToml := `
[agent]
command = "global-agent"

[worktree]
default_base = "develop"
`
	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(globalToml), 0644); err != nil {
		t.Fatal(err)
	}

	repoToml := `
[agent]
command = "repo-agent"
`
	repoPath := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(repoPath, []byte(repoToml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(repoPath)
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}

	if cfg.Agent.Command != "repo-agent" {
		t.Errorf("Agent.Command = %q, want %q", cfg.Agent.Command, "repo-agent")
	}
	if sources.AgentCommand != SourceRepo {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceRepo)
	}
	if cfg.Worktree.DefaultBase != "develop" {
		t.Errorf("Worktree.DefaultBase = %q, want %q (global value not merged)", cfg.Worktree.DefaultBase, "develop")
	}
	if sources.WorktreeDefaultBase != SourceGlobal {
		t.Errorf("sources.WorktreeDefaultBase = %q, want %q", sources.WorktreeDefaultBase, SourceGlobal)
	}
}

// TestLoadWithSources_NotifyIgnoresTheRepo is the one section that does not
// follow the precedence the rest of this file tests. A bootstrap sequence is a
// property of the project; how you like to be interrupted is not, and a cloned
// repository must not be able to start sending desktop banners.
func TestLoadWithSources_NotifyIgnoresTheRepo(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	globalToml := `
[notify]
on = ["blocked", "done"]
desktop = false
`
	if err := os.WriteFile(globalPath, []byte(globalToml), 0644); err != nil {
		t.Fatal(err)
	}

	repoToml := `
[notify]
on = ["blocked", "done", "stopped"]
desktop = true
`
	repoPath := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(repoPath, []byte(repoToml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(repoPath)
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if strings.Join(cfg.Notify.On, ",") != "blocked,done" {
		t.Errorf("Notify.On = %v, want the global list — the repo's was ignored", cfg.Notify.On)
	}
	if cfg.Notify.Desktop == nil || *cfg.Notify.Desktop {
		t.Error("the repository switched desktop banners back on")
	}
	if sources.NotifyOn != SourceGlobal || sources.NotifyDesktop != SourceGlobal {
		t.Errorf("sources = %q/%q, want both global", sources.NotifyOn, sources.NotifyDesktop)
	}
}

// And with nothing configured anywhere, the defaults stand.
func TestLoadWithSources_NotifyDefaults(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "opentree.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if strings.Join(cfg.Notify.On, ",") != "blocked,stopped" {
		t.Errorf("Notify.On = %v, want blocked and stopped on, done off", cfg.Notify.On)
	}
	if cfg.Notify.Desktop == nil || !*cfg.Notify.Desktop {
		t.Error("desktop banners should be on by default")
	}
	if sources.NotifyOn != SourceDefault {
		t.Errorf("sources.NotifyOn = %q, want %q", sources.NotifyOn, SourceDefault)
	}
}

func TestLoadWithSources_RepoFalseOverridesGlobalTrue(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)

	globalToml := `
[github]
auto_push = true
`
	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte(globalToml), 0644); err != nil {
		t.Fatal(err)
	}

	repoToml := `
[github]
auto_push = false
`
	repoPath := filepath.Join(t.TempDir(), "opentree.toml")
	if err := os.WriteFile(repoPath, []byte(repoToml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(repoPath)
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}

	if cfg.GitHub.AutoPush == nil || *cfg.GitHub.AutoPush != false {
		t.Errorf("GitHub.AutoPush = %v, want false (repo should override global)", cfg.GitHub.AutoPush)
	}
	if sources.GitHubAutoPush != SourceRepo {
		t.Errorf("sources.GitHubAutoPush = %q, want %q", sources.GitHubAutoPush, SourceRepo)
	}
}

func TestLoadWithSources_DefaultsWhenNeitherConfigExists(t *testing.T) {
	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	t.Setenv("PATH", t.TempDir()) // no agents installed → detection is a no-op

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}

	defaults := Default()
	if cfg.Agent.Command != defaults.Agent.Command {
		t.Errorf("Agent.Command = %q, want default %q", cfg.Agent.Command, defaults.Agent.Command)
	}
	if sources.AgentCommand != SourceDefault {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceDefault)
	}
	if sources.WorktreeBaseDir != SourceDefault {
		t.Errorf("sources.WorktreeBaseDir = %q, want %q", sources.WorktreeBaseDir, SourceDefault)
	}
}

// ---- SetKeys ----

// Regression: `config set` used to load the fully merged config (defaults +
// global + repo) and Save all of it, freezing every inherited value into the
// target file so later global changes silently stopped applying.
func TestSetKeys_OnlyWritesGivenKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opentree.toml")
	if err := os.WriteFile(path, []byte("[agent]\ncommand = 'claude'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := SetKeys(path, map[string]any{"worktree.default_base": "develop"}); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "default_base = 'develop'") {
		t.Errorf("file missing the set key:\n%s", content)
	}
	if !strings.Contains(content, "command = 'claude'") {
		t.Errorf("file lost a pre-existing key:\n%s", content)
	}
	// Keys the file never set must not appear (no frozen defaults).
	for _, frozen := range []string{"base_dir", "session_prefix", "auto_push", "args"} {
		if strings.Contains(content, frozen) {
			t.Errorf("file gained unrelated key %q (merged config frozen in):\n%s", frozen, content)
		}
	}
}

func TestSetKeys_CreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "opentree.toml")
	if err := SetKeys(path, map[string]any{"agent.command": "claude"}); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent.Command != "claude" {
		t.Errorf("agent.command = %q, want claude", cfg.Agent.Command)
	}
}

// ---- FindConfigFile repo anchoring ----

// Regression: FindConfigFile walked above the repository root, so a stray
// ~/opentree.toml was adopted as (and overwritten as) the repo's config, and
// a miss returned a cwd-relative path that scattered configs across subdirs.
func TestFindConfigFile_StopsAtRepoRoot(t *testing.T) {
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skip("git not available")
	}
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q")

	// A config ABOVE the repo must be ignored.
	if err := os.WriteFile(filepath.Join(outer, "opentree.toml"), []byte("[agent]\ncommand='evil'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(sub)
	got := FindConfigFile()
	realRepo, _ := filepath.EvalSymlinks(repo)
	want := filepath.Join(realRepo, "opentree.toml")
	if got != want {
		t.Errorf("FindConfigFile() = %q, want repo-root anchored %q", got, want)
	}

	// A config at the repo root is found from a subdir.
	if err := os.WriteFile(want, []byte("[agent]\ncommand='ok'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := FindConfigFile(); got != want {
		t.Errorf("FindConfigFile() = %q, want %q", got, want)
	}
}

// A repository may say where its worktrees go, and may not say "somewhere
// else entirely". base_dir is joined to the repo root and the result is what
// `opentree delete` hands to os.RemoveAll, so a cloned repository asking for
// "../.." is asking to have the directory above the clone removed.
func TestLoadWithSources_RepoBaseDirCannotEscape(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no global config in the way

	for _, base := range []string{"../..", "..", "../worktrees", "/tmp/elsewhere"} {
		t.Run(base, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opentree.toml")
			toml := "[worktree]\nbase_dir = \"" + base + "\"\n"
			if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}

			cfg, src, err := LoadWithSources(path)
			if err != nil {
				t.Fatalf("LoadWithSources(): %v", err)
			}
			if cfg.Worktree.BaseDir != Default().Worktree.BaseDir {
				t.Errorf("BaseDir = %q, want the default %q — an escaping repo value must be dropped",
					cfg.Worktree.BaseDir, Default().Worktree.BaseDir)
			}
			if src.WorktreeBaseDir == SourceRepo {
				t.Error("source says repo for a value that was dropped")
			}
		})
	}
}

// The narrowness is the point: a repository that names a directory inside
// itself is doing something ordinary and supported.
func TestLoadWithSources_RepoBaseDirLocalIsKept(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	for _, base := range []string{".custom", "build/worktrees"} {
		t.Run(base, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opentree.toml")
			toml := "[worktree]\nbase_dir = \"" + base + "\"\n"
			if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
				t.Fatalf("WriteFile(): %v", err)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if cfg.Worktree.BaseDir != base {
				t.Errorf("BaseDir = %q, want %q", cfg.Worktree.BaseDir, base)
			}
		})
	}
}

// Regression: inside a linked worktree, the walk resolved to the worktree's
// own checked-out opentree.toml. That file belongs to a branch, so reads got
// whatever the branch said and `config set` edited the branch — dirtying an
// agent's working tree and scoping the setting to it. The repository's file is
// the only right answer, which is the choice chat.go and setup.go already make
// by hand.
func TestFindConfigFile_InsideALinkedWorktreeUsesTheRepoRoot(t *testing.T) {
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "commit", "--allow-empty", "--no-gpg-sign", "-q", "-m", "init")

	realRepo, _ := filepath.EvalSymlinks(repo)
	repoConfig := filepath.Join(realRepo, "opentree.toml")
	if err := os.WriteFile(repoConfig, []byte("[agent]\ncommand='project'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(repo, ".opentree", "feat-x")
	run("git", "worktree", "add", "-q", "-b", "feat/x", wt)
	// The branch carries its own copy, saying something else.
	if err := os.WriteFile(filepath.Join(wt, "opentree.toml"), []byte("[agent]\ncommand='branch'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Chdir(wt)
	if got := FindConfigFile(); got != repoConfig {
		t.Errorf("FindConfigFile() = %q, want the repository's %q", got, repoConfig)
	}
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.Agent.Command != "project" {
		t.Errorf("agent.command = %q, want the project's value, not the branch's", cfg.Agent.Command)
	}
}

// And a worktree living beside the repository rather than inside it — the
// documented `base_dir = "../worktrees"` layout — is the sharper case: the
// repo-root break is unreachable from there, so the walk used to climb past
// the worktree and adopt any opentree.toml it met on the way to /.
func TestFindConfigFile_WorktreeOutsideTheRepoDoesNotAdoptAStrayConfig(t *testing.T) {
	if err := exec.Command("git", "--version").Run(); err != nil {
		t.Skip("git not available")
	}
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	run("git", "init", "-q")
	run("git", "config", "user.email", "test@example.com")
	run("git", "config", "user.name", "Test")
	run("git", "commit", "--allow-empty", "--no-gpg-sign", "-q", "-m", "init")

	// A stray config above the worktree, which is not this repository's.
	if err := os.WriteFile(filepath.Join(outer, "opentree.toml"), []byte("[agent]\ncommand='stray'\n"), 0600); err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(outer, "worktrees", "feat-x")
	run("git", "worktree", "add", "-q", "-b", "feat/x", wt)

	t.Chdir(wt)
	realRepo, _ := filepath.EvalSymlinks(repo)
	want := filepath.Join(realRepo, "opentree.toml")
	if got := FindConfigFile(); got != want {
		t.Errorf("FindConfigFile() = %q, want the repository's %q", got, want)
	}
}
