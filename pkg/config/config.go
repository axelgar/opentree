package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/axelgar/opentree/pkg/fsutil"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/notify"
)

// Config represents the opentree configuration
type Config struct {
	Agent     AgentConfig     `toml:"agent"`
	Worktree  WorktreeConfig  `toml:"worktree"`
	Workspace WorkspaceConfig `toml:"workspace"`
	Tmux      TmuxConfig      `toml:"tmux"`
	GitHub    GitHubConfig    `toml:"github"`
	Notify    NotifyConfig    `toml:"notify"`
}

// AgentConfig configures the coding agent
type AgentConfig struct {
	Command string `toml:"command"`
}

// Validate checks that the agent is one opentree can drive, and that it is
// installed.
//
// Being on PATH is not enough: opentree only speaks the Agent Client Protocol,
// so an agent it has no ACP spec for cannot be run at all. Catching that here
// turns it into one clear message at `opentree new` rather than a puzzling
// failure later, inside the chat that was supposed to open.
func (a AgentConfig) Validate() error {
	if a.Command == "" {
		return fmt.Errorf("agent command is empty")
	}
	if FindAgent(a.Command) == nil {
		return UnknownAgentError(a.Command)
	}
	if _, err := exec.LookPath(a.Command); err != nil {
		return fmt.Errorf("agent command %q not found on PATH — install it or set [agent] command in opentree.toml (known agents: %s)", a.Command, knownAgentCommands())
	}
	return nil
}

// WorktreeConfig configures git worktree behavior
type WorktreeConfig struct {
	BaseDir     string `toml:"base_dir"`
	DefaultBase string `toml:"default_base"`
}

// WorkspaceConfig is what a worktree needs beyond what git carries.
//
// A worktree created by `git worktree add` holds only tracked files: no .env,
// no node_modules, no .venv. Seed names the untracked files to link in, setup
// the commands that build what linking cannot copy, and run the dev server to
// start on demand.
//
// It lives in the repository's own opentree.toml rather than the global one on
// purpose: a bootstrap sequence is a property of the project, and one kept per
// machine drifts until nobody maintains it. That also makes setup and run
// executable code arriving with a clone, which is why they are gated by trust
// before they run.
type WorkspaceConfig struct {
	Setup []string `toml:"setup"`
	Seed  []string `toml:"seed"`
	Run   string   `toml:"run"`
}

// TmuxConfig configures tmux behavior
type TmuxConfig struct {
	SessionPrefix string `toml:"session_prefix"`
}

// GitHubConfig configures GitHub integration
type GitHubConfig struct {
	AutoPush *bool `toml:"auto_push,omitempty"`
}

// NotifyConfig is how you like to be interrupted when an agent needs you.
//
// It is read from the global config only, and stripped from a repository's own
// (see LoadWithSources). This inverts the rule WorkspaceConfig follows, on
// purpose: a bootstrap sequence is a property of the project, but notification
// preference is a property of the person and the room they are sitting in.
// There is no version of "this repository would like to send you desktop
// banners" that is a reasonable thing for a clone to be able to say.
type NotifyConfig struct {
	// On is the events worth an interruption: "blocked", "done", "stopped".
	// An empty list switches notifications off entirely.
	On []string `toml:"on"`

	// Desktop is whether to raise an OS banner as well as the tmux bell. A
	// pointer so an explicit false in the file is not the same as saying
	// nothing.
	Desktop *bool `toml:"desktop"`
}

// ConfigSource tracks which config file provided each value.
type ConfigSource struct {
	AgentCommand        string
	WorktreeBaseDir     string
	WorktreeDefaultBase string
	WorkspaceSetup      string
	WorkspaceSeed       string
	WorkspaceRun        string
	TmuxSessionPrefix   string
	GitHubAutoPush      string
	// The notify keys have no repo source to report: LoadWithSources strips
	// that layer before the merge.
	NotifyOn      string
	NotifyDesktop string
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
		},
		Worktree: WorktreeConfig{
			BaseDir:     ".opentree",
			DefaultBase: "main",
		},
		Tmux: TmuxConfig{
			SessionPrefix: "opentree",
		},
		GitHub: GitHubConfig{
			AutoPush: boolPtr(true),
		},
		Notify: NotifyConfig{
			On:      notify.Default(),
			Desktop: boolPtr(true),
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

// FindConfigFile walks up from the current directory looking for
// opentree.toml, stopping at the repository root: an unrelated opentree.toml
// above the repo (e.g. in $HOME) must never be adopted — or overwritten — as
// this repo's config. When nothing is found it returns the repo-root path
// where the file should be created, so writes land in the same place no
// matter which subdirectory the command runs from. Outside a git repository
// it falls back to "opentree.toml" in the current directory.
func FindConfigFile() string {
	root, rootErr := gitutil.RepoRoot()
	dir, err := os.Getwd()
	if err != nil {
		if rootErr == nil {
			return filepath.Join(root, "opentree.toml")
		}
		return "opentree.toml"
	}
	if rootErr != nil {
		candidate := filepath.Join(dir, "opentree.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		return "opentree.toml"
	}
	// Resolve symlinks so the walk can recognize the repo root (RepoRoot
	// returns a symlink-resolved path).
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for {
		candidate := filepath.Join(dir, "opentree.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // cwd was not under the repo root
		}
		dir = parent
	}
	return filepath.Join(root, "opentree.toml")
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
	if src.Worktree.BaseDir != "" {
		dst.Worktree.BaseDir = src.Worktree.BaseDir
	}
	if src.Worktree.DefaultBase != "" {
		dst.Worktree.DefaultBase = src.Worktree.DefaultBase
	}
	if src.Workspace.Setup != nil {
		dst.Workspace.Setup = src.Workspace.Setup
	}
	if src.Workspace.Seed != nil {
		dst.Workspace.Seed = src.Workspace.Seed
	}
	if src.Workspace.Run != "" {
		dst.Workspace.Run = src.Workspace.Run
	}
	if src.Tmux.SessionPrefix != "" {
		dst.Tmux.SessionPrefix = src.Tmux.SessionPrefix
	}
	if src.GitHub.AutoPush != nil {
		dst.GitHub.AutoPush = src.GitHub.AutoPush
	}
	if src.Notify.On != nil {
		dst.Notify.On = src.Notify.On
	}
	if src.Notify.Desktop != nil {
		dst.Notify.Desktop = src.Notify.Desktop
	}
}

// computeSources compares a resolved config against global and repo raw configs
// to determine which source provided each final value.
func computeSources(resolved, global, repo *Config) ConfigSource {
	src := ConfigSource{
		AgentCommand:        SourceDefault,
		WorktreeBaseDir:     SourceDefault,
		WorktreeDefaultBase: SourceDefault,
		WorkspaceSetup:      SourceDefault,
		WorkspaceSeed:       SourceDefault,
		WorkspaceRun:        SourceDefault,
		TmuxSessionPrefix:   SourceDefault,
		GitHubAutoPush:      SourceDefault,
		NotifyOn:            SourceDefault,
		NotifyDesktop:       SourceDefault,
	}

	if global != nil && global.Agent.Command != "" {
		src.AgentCommand = SourceGlobal
	}
	if repo != nil && repo.Agent.Command != "" {
		src.AgentCommand = SourceRepo
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

	if global != nil && global.Workspace.Setup != nil {
		src.WorkspaceSetup = SourceGlobal
	}
	if repo != nil && repo.Workspace.Setup != nil {
		src.WorkspaceSetup = SourceRepo
	}

	if global != nil && global.Workspace.Seed != nil {
		src.WorkspaceSeed = SourceGlobal
	}
	if repo != nil && repo.Workspace.Seed != nil {
		src.WorkspaceSeed = SourceRepo
	}

	if global != nil && global.Workspace.Run != "" {
		src.WorkspaceRun = SourceGlobal
	}
	if repo != nil && repo.Workspace.Run != "" {
		src.WorkspaceRun = SourceRepo
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

	// No repo arm for these two: the section is stripped from that layer before
	// it is merged, so global is as far as they go.
	if global != nil && global.Notify.On != nil {
		src.NotifyOn = SourceGlobal
	}
	if global != nil && global.Notify.Desktop != nil {
		src.NotifyDesktop = SourceGlobal
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

	// The one section a repository may not carry. Everything else here is a
	// property of the project; how you like to be interrupted is a property of
	// you, and a cloned repository does not get to start sending you desktop
	// banners. Dropped rather than refused: an opentree.toml written for
	// somebody else's machine should still load.
	if repoCfg != nil {
		repoCfg.Notify = NotifyConfig{}
	}

	// And the one value a repository may not choose freely. base_dir is joined
	// to the repository root, and the result is what `opentree delete` hands to
	// os.RemoveAll — so `base_dir = "../.."` in a cloned repository's
	// opentree.toml is that repository asking to have the directory above the
	// clone removed, on a keypress meant to delete a workspace.
	//
	// Only the repository layer, and only for a path that leaves the
	// repository: "../worktrees" is a documented layout, and the user's own
	// config is entitled to ask for it. IsLocal is the same question phrased
	// as the standard library asks it — does joining this to a directory stay
	// inside that directory. Dropped rather than refused, as above.
	if repoCfg != nil && repoCfg.Worktree.BaseDir != "" && !filepath.IsLocal(repoCfg.Worktree.BaseDir) {
		repoCfg.Worktree.BaseDir = ""
	}

	resolved := Default()
	mergeInto(resolved, globalCfg)
	mergeInto(resolved, repoCfg)

	sources := computeSources(resolved, globalCfg, repoCfg)

	// No config file named an agent: use the first installed one so the first
	// run works with whatever the user already has. The hardcoded default
	// stands otherwise, and Validate reports it if it isn't installed.
	if sources.AgentCommand == SourceDefault {
		if a := FirstInstalledAgent(); a != nil {
			resolved.Agent.Command = a.Command
		}
	}

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

// SetKeys updates only the given dotted keys (e.g. "agent.command") in the
// TOML file at path, preserving exactly what the file already contains.
// Unlike Save with a merged Config, this never freezes defaults or another
// source's values into the file — a later change to the global config still
// applies to any key the repo file doesn't set itself.
func SetKeys(path string, values map[string]any) error {
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := toml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	for key, value := range values {
		section, field, ok := strings.Cut(key, ".")
		if !ok {
			return fmt.Errorf("invalid config key %q", key)
		}
		table, _ := raw[section].(map[string]any)
		if table == nil {
			table = map[string]any{}
		}
		table[field] = value
		raw[section] = table
	}

	out, err := toml.Marshal(raw)
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(path, out)
}
