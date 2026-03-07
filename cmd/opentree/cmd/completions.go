package cmd

import (
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/spf13/cobra"
)

func workspaceCompletions(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	store, err := state.New(repoRoot)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	workspaces := store.ListWorkspaces()
	names := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		names = append(names, ws.Name)
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}
