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
			AutoPush: boolPtr(true),
		},
	}

	if err := SetKeys(path, map[string]any{
		"agent.command":         original.Agent.Command,
		"agent.args":            original.Agent.Args,
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
	// "go" should be available in any Go test environment.
	a := AgentConfig{Command: "go"}
	if err := a.Validate(); err != nil {
		t.Fatalf("Validate() with valid binary failed: %v", err)
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
	fakeAgentBinary(t, binDir, "codex")
	t.Setenv("PATH", binDir)

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if cfg.Agent.Command != "codex" {
		t.Errorf("Agent.Command = %q, want detected %q", cfg.Agent.Command, "codex")
	}
	if sources.AgentCommand != SourceDefault {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceDefault)
	}
}

// Detection must carry the agent's default args (gh needs "copilot") and must
// never override an agent set in a config file.
func TestLoadWithSources_DetectionArgsAndConfigPrecedence(t *testing.T) {
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "gh")
	t.Setenv("PATH", binDir)

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, _, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if cfg.Agent.Command != "gh" || len(cfg.Agent.Args) != 1 || cfg.Agent.Args[0] != "copilot" {
		t.Errorf("detected agent = %q %v, want \"gh\" [copilot]", cfg.Agent.Command, cfg.Agent.Args)
	}

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

// A config file that sets args without a command must disable detection:
// user-authored args must never be paired with a binary the user didn't choose.
func TestLoadWithSources_ArgsOnlyConfigSkipsDetection(t *testing.T) {
	binDir := t.TempDir()
	fakeAgentBinary(t, binDir, "codex")
	t.Setenv("PATH", binDir)

	xdgDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdgDir)
	globalPath := filepath.Join(xdgDir, "opentree", "opentree.toml")
	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalPath, []byte("[agent]\nargs = [\"--yolo\"]\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, sources, err := LoadWithSources(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatalf("LoadWithSources() failed: %v", err)
	}
	if want := Default().Agent.Command; cfg.Agent.Command != want {
		t.Errorf("Agent.Command = %q, want default %q (not detected codex)", cfg.Agent.Command, want)
	}
	if len(cfg.Agent.Args) != 1 || cfg.Agent.Args[0] != "--yolo" {
		t.Errorf("Agent.Args = %v, want [--yolo]", cfg.Agent.Args)
	}
	if sources.AgentCommand != SourceDefault {
		t.Errorf("sources.AgentCommand = %q, want %q", sources.AgentCommand, SourceDefault)
	}
	if sources.AgentArgs != SourceGlobal {
		t.Errorf("sources.AgentArgs = %q, want %q", sources.AgentArgs, SourceGlobal)
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
	if err := SetKeys(path, map[string]any{"agent.command": "claude", "agent.args": []string{"-x"}}); err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent.Command != "claude" {
		t.Errorf("agent.command = %q, want claude", cfg.Agent.Command)
	}
	if len(cfg.Agent.Args) != 1 || cfg.Agent.Args[0] != "-x" {
		t.Errorf("agent.args = %v, want [-x]", cfg.Agent.Args)
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
