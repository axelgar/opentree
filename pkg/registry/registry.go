// Package registry opens opentree's agent list to the ACP Registry — the
// index of agents that speak the Agent Client Protocol, published at
// cdn.agentclientprotocol.com and consumed by every ACP client.
//
// opentree's angle is the one pkg/config already has: the four predefined
// agents stay the curated, branded defaults, and a registry agent joins them
// as a fifth entry in the same slice. `opentree agents add <id>` installs an
// entry into a per-machine store beside the adapters and the plugins, and a
// loader appends what is installed to config.PredefinedAgents at startup —
// before anything looks an agent up, so FindAgent, the picker, fan-out and
// per-workspace overrides work on registry agents without knowing the
// difference. The one mark of it is config.RegistryOrigin, which `agents
// list`, doctor and the refusal messages key on.
//
// Ordinary commands never touch the network. The loader reads install
// records from disk; only `agents add`, `agents update` and `agents search`
// fetch, and each says so before it does. Installing executes code, so it
// sits behind the same doctrine as the adapter download and the trust gate:
// what will be fetched, from where, and what will run are printed in full
// before anything happens.
package registry

import (
	"os"
	"path/filepath"
)

// DefaultIndexURL is the published registry index. A constant rather than
// config for now: the registry has one canonical address, and a configurable
// one is a decision to take when somebody runs a mirror.
const DefaultIndexURL = "https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json"

// Dir is the per-machine registry store: the cached index and one directory
// per installed agent. Empty when the home directory cannot be resolved, same
// as config.ToolsDir and plugins.Dir — callers treat "" as "nowhere", and
// `opentree uninstall` skips non-absolute paths on exactly that signal.
func Dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".opentree", "registry")
}

// agentsDir is where installs land, one directory per registry id.
func agentsDir() string {
	dir := Dir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "agents")
}
