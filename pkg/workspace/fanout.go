package workspace

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

// fanoutName is the branch a sibling gets: the group's base name with the
// agent appended, suffixed past collisions the way dispatchBranchName suffixes
// its slugs — so a fan-out of feat/x lands beside an unrelated feat/x-claude
// the user already had, rather than refusing or clobbering it.
func fanoutName(base, agent string, taken func(string) bool) string {
	name := base + "-" + agent
	for i := 2; taken(name); i++ {
		name = fmt.Sprintf("%s-%s-%d", base, agent, i)
	}
	return name
}

// CreateFanout creates one workspace per agent, all from the same base branch
// and all stamped as one group, so the same task can be raced across them and
// the dashboard can show them as one thing.
//
// Everything that can be checked is checked before anything is created: every
// agent resolved and on PATH, the seed list valid, every sibling name picked.
// Half a fan-out because the third agent was missing would be worse than no
// fan-out — the user fixes their PATH once and reruns.
//
// A failure past that point is different: it is systemic (git, tmux, disk),
// and the siblings already created are live workspaces with running agents.
// Rolling those back would destroy more than the failure did, so they are
// kept, the error says so, and the loop stops rather than piling identical
// errors on top. `Create` already rolls back its own half-made sibling, so
// everything in state is whole either way; a partial group is a functioning
// group — promote it or delete it like any other.
func (s *Service) CreateFanout(base, baseBranch string, agents []string) ([]*state.Workspace, error) {
	resolved := make([]*config.PredefinedAgent, 0, len(agents))
	seen := make(map[string]bool, len(agents))
	for _, name := range agents {
		agent := config.FindAgent(name)
		if agent == nil {
			return nil, config.UnknownAgentError(name)
		}
		// Checked after normalization so claude,Claude is caught too. A
		// duplicate is refused rather than deduped: it is almost always a
		// typo'd fourth agent, and silently racing three where four were
		// asked for hides exactly the mistake worth surfacing.
		if seen[agent.Command] {
			return nil, fmt.Errorf("agent %q appears more than once in the fan-out", agent.Command)
		}
		seen[agent.Command] = true
		resolved = append(resolved, agent)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("a fan-out needs at least one agent")
	}

	var missing []string
	for _, a := range resolved {
		if _, err := exec.LookPath(a.Command); err != nil {
			missing = append(missing, a.Command)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("cannot fan out: not installed: %s — every agent must be on PATH before any sibling is created", strings.Join(missing, ", "))
	}
	if err := bootstrap.ValidateSeed(s.repoRoot, s.cfg.Workspace.Seed); err != nil {
		return nil, err
	}

	// Names are picked against existing workspaces in both raw and sanitized
	// form. Sanitization is not injective — feat/x-claude and feat-x/claude
	// share a worktree directory, tmux window and socket — so matching only
	// exact names would let two workspaces fight over one directory, caught
	// late by `git worktree add` instead of sidestepped here.
	takenRaw := make(map[string]bool)
	takenSanitized := make(map[string]bool)
	for _, ws := range s.ListWorkspaces() {
		takenRaw[ws.Name] = true
		takenSanitized[gitutil.SanitizeBranchName(ws.Name)] = true
	}
	taken := func(candidate string) bool {
		return takenRaw[candidate] || takenSanitized[gitutil.SanitizeBranchName(candidate)]
	}
	names := make([]string, len(resolved))
	for i, a := range resolved {
		names[i] = fanoutName(base, a.Command, taken)
		takenRaw[names[i]] = true
		takenSanitized[gitutil.SanitizeBranchName(names[i])] = true
	}

	created := make([]*state.Workspace, 0, len(resolved))
	for i, a := range resolved {
		ws, err := s.CreateWith(names[i], baseBranch, CreateOpts{Agent: a.Command, FanoutGroup: base})
		if err != nil {
			if len(created) == 0 {
				return nil, fmt.Errorf("failed to create %q: %w", names[i], err)
			}
			kept := make([]string, len(created))
			for j, c := range created {
				kept[j] = c.Name
			}
			return created, fmt.Errorf("failed to create %q — the siblings already created are kept (%s): %w", names[i], strings.Join(kept, ", "), err)
		}
		created = append(created, ws)
	}
	return created, nil
}
