package config

import (
	"os"
	"os/exec"
	"path/filepath"
)

// Agents reached through an adapter need that adapter installed. opentree keeps
// them in its own prefix rather than the user's global npm root: they are
// opentree's dependency, not the user's, and uninstalling opentree should not
// leave a stray package behind.

// ToolsDir is where opentree installs agent adapters.
func ToolsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".opentree", "tools")
}

// ResolveACPCommand is the path to the agent's ACP server: opentree's own copy
// when it has one, otherwise the bare name for a PATH lookup — someone who
// installed the adapter themselves should not have opentree install it twice.
func (a PredefinedAgent) ResolveACPCommand() string {
	name := a.ACPCommand()
	if name == "" {
		return ""
	}
	if dir := ToolsDir(); dir != "" {
		managed := filepath.Join(dir, "bin", name)
		if info, err := os.Stat(managed); err == nil && !info.IsDir() {
			return managed
		}
	}
	return name
}

// ACPInstalled reports whether the adapter can be found at all.
func (a PredefinedAgent) ACPInstalled() bool {
	cmd := a.ResolveACPCommand()
	if cmd == "" {
		return false
	}
	if filepath.IsAbs(cmd) {
		return true
	}
	_, err := exec.LookPath(cmd)
	return err == nil
}

// ACPInstallCommand is the argv that installs this agent's adapter, or nil when
// the agent serves ACP itself and there is nothing to fetch.
func (a PredefinedAgent) ACPInstallCommand() []string {
	if a.ACP.Package == "" {
		return nil
	}
	dir := ToolsDir()
	if dir == "" {
		return nil
	}
	// -g with an explicit prefix gives a bin/ shim without touching the user's
	// global npm root.
	return []string{"npm", "install", "-g", "--prefix", dir, a.ACP.Package}
}
