package config

import (
	"encoding/json"
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
	// A registry install records its command as an absolute path already;
	// joining that under the tools prefix would manufacture a path nothing
	// ever wrote to.
	if filepath.IsAbs(name) {
		return name
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
		// An absolute answer used to be proof of existence, because only the
		// stat above could produce one. A registry agent's spec is absolute
		// before any stat, so existence has to be checked rather than implied
		// — a deleted install must not keep reporting itself ready.
		info, err := os.Stat(cmd)
		return err == nil && !info.IsDir()
	}
	_, err := exec.LookPath(cmd)
	return err == nil
}

// ACPPackageSpec is what the install asks npm for: the package at the version
// this build of opentree pins, or the bare package name for a registry entry
// that has not been pinned yet. Empty when the agent serves ACP itself.
func (a PredefinedAgent) ACPPackageSpec() string {
	if a.ACP.Package == "" {
		return ""
	}
	if a.ACP.Version == "" {
		return a.ACP.Package
	}
	return a.ACP.Package + "@" + a.ACP.Version
}

// ACPInstallCommand is the argv that installs this agent's adapter, or nil when
// the agent serves ACP itself and there is nothing to fetch.
//
// Three of these arguments are the whole security posture of the feature, and
// they live here rather than at any of the three places that run it — the
// picker's enter, the picker's i, and `opentree agents setup` — so that fixing
// one of them fixes all three:
//
//   - -g with an explicit prefix gives a bin/ shim without touching the user's
//     global npm root.
//   - The version pin means the tarball is the one this release was built
//     against rather than whatever latest resolved to this morning.
//   - --ignore-scripts stops npm executing install hooks out of the hundred-odd
//     packages the adapter pulls in, each of which would otherwise run as the
//     user. The adapter's tree declares none: its per-platform binaries arrive
//     as optional dependencies, which npm resolves by downloading, not by
//     building. Established by installing with the flag and completing an ACP
//     initialize handshake against the result — if that ever stops holding, the
//     adapter fails loudly at startup rather than quietly at runtime.
func (a PredefinedAgent) ACPInstallCommand() []string {
	spec := a.ACPPackageSpec()
	if spec == "" {
		return nil
	}
	dir := ToolsDir()
	if dir == "" {
		return nil
	}
	return []string{"npm", "install", "-g", "--prefix", dir, "--ignore-scripts", spec}
}

// ACPInstalledVersion is the version of the adapter sitting in opentree's own
// prefix, or "" when opentree did not put one there — an adapter found on PATH
// is somebody else's install, and a copy nobody here placed is not opentree's
// to report on or replace.
func (a PredefinedAgent) ACPInstalledVersion() string {
	if a.ACP.Package == "" {
		return ""
	}
	dir := ToolsDir()
	if dir == "" {
		return ""
	}
	// npm's prefix layout: the shim in bin/, the tree under lib/node_modules/,
	// where the version it actually resolved is recorded. Reading the manifest
	// beats asking the adapter, which would mean starting a 300MB Node process
	// to answer a question about a file.
	manifest := filepath.Join(dir, "lib", "node_modules",
		filepath.FromSlash(a.ACP.Package), "package.json")
	data, err := os.ReadFile(manifest) // #nosec G304 -- opentree's own tools prefix, under the user's home
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	return pkg.Version
}
