package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every adapter in the registry must name a version. An unpinned entry resolves
// `latest` at the moment the user agrees to the download, which means the
// adapter two users are running is decided by the day they set up rather than
// by anything in this repository — and a bad release reaches everyone who
// installs while it is up.
func TestACPPackageSpec_EveryAdapterIsPinned(t *testing.T) {
	for _, agent := range PredefinedAgents {
		if agent.ACP.Package == "" {
			continue
		}
		if agent.ACP.Version == "" {
			t.Errorf("%s installs %s unpinned — give it a version", agent.Name, agent.ACP.Package)
			continue
		}
		want := agent.ACP.Package + "@" + agent.ACP.Version
		if got := agent.ACPPackageSpec(); got != want {
			t.Errorf("%s spec = %q, want %q", agent.Name, got, want)
		}
	}

	// An agent that serves ACP itself has nothing to ask npm for, and a spec
	// built out of an empty package name would be the string "@1.2.3".
	if got := FindAgent("opencode").ACPPackageSpec(); got != "" {
		t.Errorf("opencode spec = %q, want none — it serves ACP itself", got)
	}
}

// The install argv is the one place all three entry points meet — the picker's
// enter, the picker's i and `opentree agents setup` — so the flags that decide
// what npm is allowed to do are asserted here rather than at any of them.
func TestACPInstallCommand_PinnedPrefixedAndScriptless(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	claude := FindAgent("claude")
	got := claude.ACPInstallCommand()
	if len(got) == 0 {
		t.Fatal("Claude Code is reached through an adapter and needs an install command")
	}

	// The package spec must be exactly the pinned one. Contains() would pass on
	// a bare package name too, which is the bug this guards.
	if last := got[len(got)-1]; last != claude.ACPPackageSpec() {
		t.Errorf("install fetches %q, want the pinned %q", last, claude.ACPPackageSpec())
	}
	if !slices.Contains(got, "--ignore-scripts") {
		t.Error("install runs npm's lifecycle scripts — a hundred packages' postinstall hooks run as the user")
	}
	if i := slices.Index(got, "--prefix"); i < 0 || i+1 >= len(got) || got[i+1] != ToolsDir() {
		t.Errorf("install = %v, want it prefixed into %s so nothing lands in the global npm root", got, ToolsDir())
	}
}

// Whether the adapter is opentree's own copy and which version it is are
// different questions, and the setup command needs both: a copy the user
// installed themselves is not opentree's to replace, and a copy opentree
// installed at an older pin is exactly what re-running setup should move on.
func TestACPInstalledVersion_ReadsOpentreesOwnCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	claude := FindAgent("claude")
	if got := claude.ACPInstalledVersion(); got != "" {
		t.Errorf("with nothing installed = %q, want empty", got)
	}

	pkgDir := filepath.Join(home, ".opentree", "tools", "lib", "node_modules",
		filepath.FromSlash(claude.ACP.Package))
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(pkgDir, "package.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"x","version":"0.1.2"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := claude.ACPInstalledVersion(); got != "0.1.2" {
		t.Errorf("installed version = %q, want 0.1.2", got)
	}

	// A manifest npm half-wrote, or a layout that has moved under us, must read
	// as "cannot tell" rather than taking the setup command down.
	if err := os.WriteFile(manifest, []byte(`{"name":`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := claude.ACPInstalledVersion(); got != "" {
		t.Errorf("unreadable manifest = %q, want empty", got)
	}

	// opencode has no package, so there is no manifest to look for.
	if got := FindAgent("opencode").ACPInstalledVersion(); got != "" {
		t.Errorf("opencode version = %q, want none", got)
	}
}

// The pinned version has to be a version, not a dist-tag: "latest" or "next"
// in this field would reintroduce the floating install through a field that
// looks pinned.
func TestACPSpec_VersionIsAVersionNotATag(t *testing.T) {
	for _, agent := range PredefinedAgents {
		v := agent.ACP.Version
		if v == "" {
			continue
		}
		if strings.ContainsAny(v, "^~*x") || !strings.Contains(v, ".") {
			t.Errorf("%s pins %q — a range or a tag is not a pin", agent.Name, v)
		}
	}
}
