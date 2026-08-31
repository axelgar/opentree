package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/registry"
)

// runAgents drives the real agents command tree, flags and all, and returns
// what it printed and the error. Args are reset afterwards because the
// commands are package-level vars.
func runAgents(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var err error
	out := captureStdout(t, func() {
		AgentsCmd.SetArgs(args)
		defer func() {
			AgentsCmd.SetArgs(nil)
			_ = agentsAddCmd.Flags().Set("yes", "false")
		}()
		err = AgentsCmd.Execute()
	})
	return out, err
}

// snapshotAgentRegistry guards config.PredefinedAgents for tests that load
// registry installs into it. No test using it may call t.Parallel.
func snapshotAgentRegistry(t *testing.T) {
	t.Helper()
	prev := config.PredefinedAgents
	config.PredefinedAgents = append([]config.PredefinedAgent{}, prev...)
	t.Cleanup(func() { config.PredefinedAgents = prev })
}

// fabricateRegistryInstall writes a complete install into the store under
// $HOME the way `agents add` would have, and returns its binary's path.
func fabricateRegistryInstall(t *testing.T, home, id, name, version string) string {
	t.Helper()
	dir := filepath.Join(home, ".opentree", "registry", "agents", id)
	bin := filepath.Join(dir, "npm", "bin", id)
	writeUnder(t, bin, "#!/bin/sh\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	record := map[string]any{
		"entry": map[string]any{
			"id": id, "name": name, "version": version, "description": "a test agent",
			"distribution": map[string]any{"npx": map[string]any{"package": id + "@" + version}},
		},
		"index_url": registry.DefaultIndexURL,
		"command":   bin,
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeUnder(t, filepath.Join(dir, "install.json"), string(data))
	return bin
}

// Both refusals happen before any fetch, which is the point of testing them
// here: no network, no stub, and the message names the right next command.
func TestAgentsAdd_RefusesBuiltInsBeforeFetching(t *testing.T) {
	homeWithNothingInIt(t)
	out, err := runAgents(t, "add", "claude")
	if err == nil || !strings.Contains(err.Error(), "built into opentree") {
		t.Errorf("add claude: err = %v, out = %q; want the built-in refusal", err, out)
	}
	if _, err := runAgents(t, "add", "../escape"); err == nil {
		t.Error("a path-shaped id was accepted")
	}
}

func TestAgentsAdd_AlreadyInstalledPointsAtUpdate(t *testing.T) {
	home := homeWithNothingInIt(t)
	snapshotAgentRegistry(t)
	fabricateRegistryInstall(t, home, "demo-agent", "Demo Agent", "1.2.3")
	if problems := registry.LoadInstalled(); len(problems) != 0 {
		t.Fatalf("load: %v", problems)
	}

	out, err := runAgents(t, "add", "demo-agent")
	if err != nil {
		t.Fatalf("add of an installed agent should not error: %v", err)
	}
	if !strings.Contains(out, "already installed") || !strings.Contains(out, "agents update demo-agent") {
		t.Errorf("out = %q, want the update pointer", out)
	}
}

func TestAgentsRemove_BuiltInsAndInstalls(t *testing.T) {
	home := homeWithNothingInIt(t)
	snapshotAgentRegistry(t)
	fabricateRegistryInstall(t, home, "demo-agent", "Demo Agent", "1.2.3")
	registry.LoadInstalled()

	if _, err := runAgents(t, "remove", "opencode"); err == nil || !strings.Contains(err.Error(), "cannot be removed") {
		t.Errorf("remove opencode: err = %v, want the built-in refusal", err)
	}

	out, err := runAgents(t, "remove", "demo-agent")
	if err != nil {
		t.Fatalf("remove demo-agent: %v", err)
	}
	if !strings.Contains(out, "removed demo-agent") {
		t.Errorf("out = %q", out)
	}
	if records, _ := registry.Installed(); len(records) != 0 {
		t.Error("the store still holds the install")
	}

	if _, err := runAgents(t, "remove", "demo-agent"); err == nil {
		t.Error("removing an agent that is gone should say so")
	}
}

// The setup command must not offer an adapter install for a registry agent —
// something was installed, just not by that command.
func TestAgentsSetup_PointsRegistryInstallsAtUpdate(t *testing.T) {
	home := homeWithNothingInIt(t)
	snapshotAgentRegistry(t)
	fabricateRegistryInstall(t, home, "demo-agent", "Demo Agent", "1.2.3")
	registry.LoadInstalled()

	out, err := runAgents(t, "setup", "demo-agent")
	if err != nil {
		t.Fatalf("setup demo-agent: %v", err)
	}
	if !strings.Contains(out, "agents update demo-agent") {
		t.Errorf("out = %q, want the update pointer", out)
	}
}

func TestAgentsList_MarksRegistryInstalls(t *testing.T) {
	home := homeWithNothingInIt(t)
	snapshotAgentRegistry(t)
	fabricateRegistryInstall(t, home, "demo-agent", "Demo Agent", "1.2.3")
	registry.LoadInstalled()

	out, err := runAgents(t, "list")
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "demo-agent") {
			line = l
		}
	}
	if !strings.Contains(line, "registry 1.2.3") || !strings.Contains(line, "installed") {
		t.Errorf("list row = %q, want the source column and installed status", line)
	}
	if !strings.Contains(out, "built-in") {
		t.Error("the built-ins lost their source label")
	}
}
