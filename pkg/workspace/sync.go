package workspace

import (
	"fmt"
	"strings"

	"github.com/axelgar/opentree/pkg/worktree"
)

// Sync brings the workspace's base into its branch — origin's copy when it
// can be reached — and reports what happened. The dashboard tracked whether a
// PR had conflicts with its base and offered nothing to do about them; this
// is the thing to do, and the conflicts it leaves behind are the agent's.
func (s *Service) Sync(name string, noFetch bool) (worktree.SyncResult, error) {
	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return worktree.SyncResult{}, err
	}
	base := ws.BaseBranch
	if base == "" {
		base = s.cfg.Worktree.DefaultBase
	}
	return s.worktrees.Sync(ws.Branch, base, !noFetch)
}

// SyncConflictPrompt is the message that hands a stopped merge to the agent:
// what was merged, which files stopped it, and what finishing looks like.
func SyncConflictPrompt(branch string, res worktree.SyncResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Merging %s into %s stopped on conflicts in:\n", res.Ref, branch)
	for _, f := range res.Conflicts {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	b.WriteString("The merge is in progress in this worktree (git status shows it). ")
	b.WriteString("Resolve the conflicts keeping the intent of both sides, run the project's checks, ")
	b.WriteString("and complete the merge with `git commit`.")
	return b.String()
}
