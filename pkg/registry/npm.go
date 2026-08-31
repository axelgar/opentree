package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// npmArgv is the install command for a package distribution, into the
// agent's own prefix. The three security-load-bearing arguments are the
// adapter install's, for the reasons documented on ACPInstallCommand in
// pkg/config/acptools.go: -g with an explicit prefix keeps the user's global
// npm root untouched, the spec is pinned, and --ignore-scripts stops npm
// executing install hooks out of whatever the package pulls in. The one
// difference from the adapter is the prefix: each registry agent gets its
// own, so removing the agent is removing one directory rather than asking
// npm to pick a package back out of a shared tree.
func npmArgv(prefix, spec string) []string {
	return []string{"npm", "install", "-g", "--prefix", prefix, "--ignore-scripts", spec}
}

// packageSpec is what the install asks npm for: the distribution's package,
// pinned. The registry pins inside the package field ("name@1.2.3"); an
// entry that arrives unpinned is pinned to the entry's own version, because
// "whatever latest resolves to this morning" is not what the user was shown.
func packageSpec(d PackageDist, version string) string {
	if _, v := splitPackageSpec(d.Package); v != "" {
		return d.Package
	}
	return d.Package + "@" + version
}

// splitPackageSpec separates "name@1.2.3" — and "@scope/name@1.2.3", whose
// first @ is part of the name — into name and version.
func splitPackageSpec(spec string) (name, version string) {
	if at := strings.LastIndex(spec, "@"); at > 0 {
		return spec[:at], spec[at+1:]
	}
	return spec, ""
}

// resolveBin is the shim npm just wrote, found by asking the installed
// package rather than guessing: package.json's bin field is a name→file map
// (or one string, meaning the package's own base name), and the shim lands
// in the prefix's bin under that name. The package name is no guide at all —
// factory-droid's npm package is "droid" and its bin something else again.
func resolveBin(prefix, pkgName string) (string, error) {
	manifest := filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(pkgName), "package.json")
	data, err := os.ReadFile(manifest) // #nosec G304 -- the prefix this install just wrote, under the user's home
	if err != nil {
		return "", fmt.Errorf("npm reported success but left no package manifest: %w", err)
	}
	var pkg struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", fmt.Errorf("unreadable package manifest: %w", err)
	}

	base := pkgName
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}

	var binName string
	var asString string
	binMap := map[string]string{}
	switch {
	case len(pkg.Bin) == 0:
		return "", fmt.Errorf("%s installs no command — its package.json has no bin", pkgName)
	case json.Unmarshal(pkg.Bin, &asString) == nil:
		// A string bin means "one command, named after the package".
		binName = base
	case json.Unmarshal(pkg.Bin, &binMap) == nil && len(binMap) == 1:
		for name := range binMap {
			binName = name
		}
	case len(binMap) > 1:
		// Several commands and no registry field saying which serves ACP:
		// guessing here would execute the wrong one. Prefer the entry named
		// after the package, refuse otherwise.
		if _, ok := binMap[base]; ok {
			binName = base
		} else {
			names := make([]string, 0, len(binMap))
			for name := range binMap {
				names = append(names, name)
			}
			return "", fmt.Errorf("%s installs %d commands (%s) — opentree cannot pick one", pkgName, len(binMap), strings.Join(names, ", "))
		}
	default:
		return "", fmt.Errorf("%s has a bin field opentree does not understand", pkgName)
	}

	shim := filepath.Join(prefix, "bin", binName)
	if info, err := os.Stat(shim); err != nil || info.IsDir() {
		return "", fmt.Errorf("npm reported success but %s is not there", shim)
	}
	return shim, nil
}
