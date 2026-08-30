// Package plugins makes opentree a client of the Agent Plugins standard
// (agent-plugins.org, version 1.0.0): a plugin is a directory with a closed
// plugin.json manifest, optional skills under skills/, and an optional
// mcp.json naming MCP servers.
//
// opentree's angle is the same one pkg/skills already has — the propagator.
// A plugin is installed once, into a per-machine store beside the adapters
// and the trust record, and its skills are handed to every agent in every
// worktree from there. opentree once dropped plugin skills because vendor
// layouts had no contract; the open standard is the contract, so the store
// holds only what validates against it and says which component failed.
//
// Nothing a plugin ships executes in v1. Skills are text the agents read, and
// mcp.json is listed rather than launched — writing it into agents' own MCP
// configs is issue #36. When that lands it goes behind the same doctrine as
// pkg/bootstrap/trust.go: a plugin arrives from a URL, and anything of it
// that ever executes must be approved per machine, per exact content.
package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/axelgar/opentree/pkg/fsutil"
)

// Dir is the per-machine plugin store. Empty when the home directory cannot
// be resolved, same as config.ToolsDir — callers treat "" as "nowhere",
// and `opentree uninstall` skips non-absolute paths on exactly that signal.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".opentree", "plugins")
}

// Plugin is one installed (or installable) plugin as the listing shows it.
//
// Problems carries the spec's non-fatal diagnostics — the unknown manifest
// field, the skill or server entry that was skipped, the mcp.json that
// disabled MCP — because the spec's failure model is "keep loading, but
// report", and a report that goes nowhere is the same as none.
type Plugin struct {
	Name        string   `json:"name"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Dir         string   `json:"dir"`
	Origin      string   `json:"origin,omitempty"` // the URL it was installed from
	Skills      []string `json:"skills,omitempty"` // directory names under skills/
	Servers     []Server `json:"servers,omitempty"`
	Problems    []string `json:"problems,omitempty"`
}

// sourceFile records where a plugin came from, hidden beside its manifest —
// the same pattern the skills publisher installs use. The clone's .git also
// remembers the URL, but only for as long as it stays a clone; this file is
// opentree's own record, so `plugins list` can answer "installed from where?"
// without shelling out to git.
const sourceFile = ".opentree-source.json"

type source struct {
	URL         string    `json:"url"`
	Name        string    `json:"name"`
	InstalledAt time.Time `json:"installed_at"`
}

// Load reads and validates one plugin directory under the spec's failure
// boundaries. An error means the manifest itself failed and nothing of the
// plugin may be used; anything survivable — a skipped skill, a skipped
// server, MCP disabled — comes back inside Plugin.Problems instead, with the
// components that did validate loaded around it.
func Load(dir string) (Plugin, error) {
	p := Plugin{Dir: dir}

	// Containment is checked against the resolved root because a git clone
	// can carry symlinks, and a symlink is exactly how a package path escapes
	// the package. A root that cannot be resolved cannot be checked, which is
	// the same as failing.
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return p, fmt.Errorf("cannot resolve %s: %w", dir, err)
	}

	manifestPath := filepath.Join(dir, "plugin.json")
	if !within(root, manifestPath) {
		return p, fmt.Errorf("plugin.json resolves outside the plugin")
	}
	data, err := os.ReadFile(manifestPath) // #nosec G304 -- a path inside opentree's own store, or one the user named
	if err != nil {
		return p, fmt.Errorf("no readable plugin.json: %w", err)
	}
	m, problems, err := parseManifest(data)
	if err != nil {
		return p, err
	}
	p.Name, p.Version, p.Description, p.Problems = m.Name, m.Version, m.Description, problems

	if src, err := readSource(dir); err == nil {
		p.Origin = src.URL
	}

	p.loadSkills(root)
	p.loadMCP(root)
	return p, nil
}

// loadSkills discovers skills/ exactly as the spec fixes it: each immediate
// child directory whose SKILL.md resolves to a regular file inside the
// plugin, no deeper search. A child that escapes by symlink is skipped and
// said, not silently dropped — the difference between a plugin that has no
// skill by that name and one that tried to read outside itself matters to
// the person deciding whether to keep it.
func (p *Plugin) loadSkills(root string) {
	dir := filepath.Join(p.Dir, "skills")
	if _, err := os.Lstat(dir); err != nil {
		return // a missing fixed location is not an error
	}
	if !within(root, dir) {
		p.Problems = append(p.Problems, "skills/ resolves outside the plugin — skills disabled")
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		p.Problems = append(p.Problems, "skills/ is not a directory — skills disabled")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		p.Problems = append(p.Problems, fmt.Sprintf("skills/ is unreadable: %v — skills disabled", err))
		return
	}
	for _, entry := range entries {
		child := filepath.Join(dir, entry.Name())
		md := filepath.Join(child, "SKILL.md")
		info, err := os.Stat(md)
		if err != nil || !info.Mode().IsRegular() {
			continue // a directory without a SKILL.md is not a skill, and says nothing
		}
		if !within(root, child) || !within(root, md) {
			p.Problems = append(p.Problems, fmt.Sprintf("skill %q resolves outside the plugin, skipped", entry.Name()))
			continue
		}
		p.Skills = append(p.Skills, entry.Name())
	}
}

// loadMCP reads mcp.json where the spec fixes it. Whatever goes wrong here is
// worth at most the MCP component: the spec is explicit that a plugin's
// skills do not become unusable because its server configuration is broken.
func (p *Plugin) loadMCP(root string) {
	path := filepath.Join(p.Dir, "mcp.json")
	if _, err := os.Lstat(path); err != nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || !within(root, path) {
		p.Problems = append(p.Problems, "mcp.json is not a regular file inside the plugin — MCP disabled")
		return
	}
	data, err := os.ReadFile(path) // #nosec G304 -- containment against the plugin root was just checked
	if err != nil {
		p.Problems = append(p.Problems, fmt.Sprintf("mcp.json is unreadable: %v — MCP disabled", err))
		return
	}
	servers, problems := parseMCP(data)
	p.Servers = servers
	p.Problems = append(p.Problems, problems...)
}

// within reports whether path, symlinks and all, stays inside the resolved
// root. Failing to resolve counts as escaping: a path that cannot be pinned
// down cannot be vouched for.
func within(root, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return resolved == root || strings.HasPrefix(resolved, root+string(filepath.Separator))
}

// Installed is every plugin in the store, valid or not. A store entry that no
// longer loads still lists — with the failure where its components would be —
// because an invisible broken plugin is one nobody can remove.
func Installed() []Plugin {
	store := Dir()
	if store == "" {
		return nil
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		return nil
	}
	var out []Plugin
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue // litter, or an install that died mid-stage
		}
		dir := filepath.Join(store, entry.Name())
		p, err := Load(dir)
		if err != nil {
			p = Plugin{Name: entry.Name(), Dir: dir, Problems: []string{err.Error()}}
		}
		out = append(out, p)
	}
	return out
}

// Install clones a plugin repository, validates it, and moves it into the
// store under its manifest name.
//
// The clone lands in a staging directory first because the store directory
// name is the manifest's to choose and the manifest cannot be read before the
// clone exists — and because a plugin that fails validation must leave
// nothing behind. Only a package the spec accepts ever takes a name in the
// store.
func Install(url string) (Plugin, error) {
	store := Dir()
	if store == "" {
		return Plugin{}, fmt.Errorf("cannot locate the home directory")
	}
	if err := os.MkdirAll(store, 0755); err != nil {
		return Plugin{}, err
	}
	staging, err := os.MkdirTemp(store, ".install-*")
	if err != nil {
		return Plugin{}, err
	}
	defer os.RemoveAll(staging)

	cloned := filepath.Join(staging, "plugin")
	if err := clone(url, cloned); err != nil {
		return Plugin{}, err
	}
	p, err := Load(cloned)
	if err != nil {
		return Plugin{}, err
	}

	dst := filepath.Join(store, p.Name)
	if _, err := os.Lstat(dst); err == nil {
		return Plugin{}, fmt.Errorf("%s is already installed — `opentree plugins remove %s` first", p.Name, p.Name)
	}
	if err := writeSource(cloned, source{URL: url, Name: p.Name, InstalledAt: time.Now().UTC()}); err != nil {
		return Plugin{}, err
	}
	if err := os.Rename(cloned, dst); err != nil {
		return Plugin{}, err
	}
	p.Dir, p.Origin = dst, url
	return p, nil
}

// clone is the same shape as the skills clone, for the same reasons: "--"
// ends the option list, GIT_TERMINAL_PROMPT=0 fails a credential prompt
// rather than hanging a terminal something else owns, and a failed clone
// leaves nothing on disk. .git is kept so `git -C <dir> pull` can update the
// plugin later.
func clone(url, dst string) error {
	// #nosec G702 -- the URL is the user's own, typed into their own terminal,
	// and "--" ends the option list so it cannot become a git flag.
	cmd := exec.Command("git", "clone", "--depth", "1", "--", url, dst)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dst)
		msg := strings.TrimSpace(string(out))
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = msg[i+1:] // git's own last word is the useful one
		}
		return fmt.Errorf("git clone: %s", msg)
	}
	return nil
}

// Remove deletes a plugin's store entry. Unlinking what points into it is the
// caller's job first — pkg/skills knows the agent trees, this package only
// knows the store — and the name is checked against the store rather than
// trusted as a path: remove takes a plugin's name, never a location.
func Remove(name string) error {
	store := Dir()
	if store == "" {
		return fmt.Errorf("cannot locate the home directory")
	}
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("%q does not name a plugin", name)
	}
	dir := filepath.Join(store, name)
	if _, err := os.Lstat(dir); err != nil {
		return fmt.Errorf("%s is not installed", name)
	}
	return os.RemoveAll(dir)
}

func writeSource(dir string, src source) error {
	data, err := json.MarshalIndent(src, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteAtomic(filepath.Join(dir, sourceFile), append(data, '\n'))
}

func readSource(dir string) (source, error) {
	var src source
	data, err := os.ReadFile(filepath.Join(dir, sourceFile)) // #nosec G304 -- opentree's own record beside the manifest
	if err != nil {
		return src, err
	}
	if err := json.Unmarshal(data, &src); err != nil {
		return src, err
	}
	return src, nil
}
