package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/workspace"
)

// listRow is one workspace and the service that knows where it lives.
type listRow struct {
	repo string
	svc  *workspace.Service
	ws   *state.Workspace
}

var ListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		asJSON, _ := cmd.Flags().GetBool("json")

		rows, err := listRows(all)
		if err != nil {
			return err
		}

		if asJSON {
			// Overwrite the persisted (never-updated) Status field with the
			// live value so scripts see real state, not a constant "active" —
			// and the path with where the worktree actually is, for a record
			// written before the path was recorded.
			out := make([]map[string]any, 0, len(rows))
			for _, r := range rows {
				r.ws.Status = liveStatus(r.svc, r.ws.Name)
				r.ws.WorktreeDir = r.svc.WorktreePath(r.ws.Name)
				data, err := json.Marshal(r.ws)
				if err != nil {
					return fmt.Errorf("failed to marshal workspaces: %w", err)
				}
				var obj map[string]any
				if err := json.Unmarshal(data, &obj); err != nil {
					return fmt.Errorf("failed to marshal workspaces: %w", err)
				}
				if all {
					obj["repo"] = r.repo
				}
				out = append(out, obj)
			}
			data, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal workspaces: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		if len(rows) == 0 {
			fmt.Println("No workspaces found.")
			return nil
		}

		// The path last, where a long one pushes nothing else out of line:
		// it is the column a reader copies rather than scans.
		if all {
			fmt.Printf("%-20s %-30s %-15s %-15s %-10s %s\n", "REPO", "NAME", "BRANCH", "BASE", "STATUS", "PATH")
			fmt.Println(strings.Repeat("-", 111))
			for _, r := range rows {
				fmt.Printf("%-20s %-30s %-15s %-15s %-10s %s\n", filepath.Base(r.repo), r.ws.Name, r.ws.Branch, r.ws.BaseBranch, liveStatus(r.svc, r.ws.Name), r.svc.WorktreePath(r.ws.Name))
			}
			return nil
		}
		fmt.Printf("%-30s %-15s %-15s %-10s %s\n", "NAME", "BRANCH", "BASE", "STATUS", "PATH")
		fmt.Println(strings.Repeat("-", 90))
		for _, r := range rows {
			fmt.Printf("%-30s %-15s %-15s %-10s %s\n", r.ws.Name, r.ws.Branch, r.ws.BaseBranch, liveStatus(r.svc, r.ws.Name), r.svc.WorktreePath(r.ws.Name))
		}
		return nil
	},
}

// listRows is every workspace of this repository, or of every repository
// with state when all is set. A repository whose config or state cannot be
// opened is reported and skipped rather than failing the whole list.
func listRows(all bool) ([]listRow, error) {
	if !all {
		repoRoot, err := gitutil.RepoRoot()
		if err != nil {
			return nil, err
		}
		cfg, err := config.Load("")
		if err != nil {
			return nil, fmt.Errorf("failed to load config: %w", err)
		}
		svc, err := workspace.New(repoRoot, cfg)
		if err != nil {
			return nil, err
		}
		return rowsOf(repoRoot, svc), nil
	}
	roots, errs := state.Roots()
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	var rows []listRow
	for _, root := range roots {
		cfg, err := config.Load(filepath.Join(root, "opentree.toml"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", root, err)
			continue
		}
		svc, err := workspace.New(root, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %s: %v\n", root, err)
			continue
		}
		rows = append(rows, rowsOf(root, svc)...)
	}
	return rows, nil
}

func rowsOf(repo string, svc *workspace.Service) []listRow {
	var rows []listRow
	for _, ws := range svc.ListWorkspaces() {
		rows = append(rows, listRow{repo: repo, svc: svc, ws: ws})
	}
	return rows
}

// liveStatus is a workspace's status derived from its tmux window: active /
// idle / stopped, or "unknown" when the window list is unavailable.
func liveStatus(svc *workspace.Service, name string) string {
	if s := svc.WindowStatuses()[name]; s != "" {
		return s
	}
	return "unknown"
}

func init() {
	ListCmd.Flags().BoolP("json", "j", false, "Output workspaces as JSON")
	ListCmd.Flags().BoolP("all", "A", false, "Every repository with opentree state, not just this one")
}
