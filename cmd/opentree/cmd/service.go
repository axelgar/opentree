package cmd

import (
	"fmt"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/daemon"
	"github.com/axelgar/opentree/pkg/workspace"
)

// newService ensures the background daemon is running and creates a workspace
// service backed by the daemon client. Used by all CLI commands that need a
// workspace.Service.
func newService(repoRoot string, cfg *config.Config) (*workspace.Service, error) {
	if err := daemon.EnsureDaemon(repoRoot); err != nil {
		return nil, fmt.Errorf("failed to start daemon: %w", err)
	}
	pm := daemon.NewClient(repoRoot)
	return workspace.New(repoRoot, cfg, pm)
}
