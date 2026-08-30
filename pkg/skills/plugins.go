package skills

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/plugins"
)

// LinkPlugins hands every agent the skills of every installed plugin.
//
// The store is per machine, so the links go into each agent's user-scope
// tree — the one place every repository and every worktree already reads —
// and installing a plugin once is what makes its skills available everywhere,
// with no per-worktree work and nothing new for workspace creation to do.
//
// Symlinks rather than copies, for the reason Link chose them: the store's
// copy is the plugin, updating it updates every agent at once, and Scan
// collapses the aliases onto the store directory so four links still list as
// one skill. A destination that already exists is skipped, the same
// never-overwrite rule as everywhere else a skill lands in a tree.
//
// Returns one entry per link created, so a caller can say what changed and
// stay quiet when nothing did.
func LinkPlugins() ([]string, error) {
	var linked []string
	var errs []error
	seen := map[string]bool{} // two agents can share a canonical tree

	for _, p := range plugins.Installed() {
		for _, name := range p.Skills {
			src := filepath.Join(p.Dir, "skills", name)
			for _, agent := range config.PredefinedAgents {
				if len(agent.Skills.UserDirs) == 0 {
					continue
				}
				dir := ExpandUserDir(agent.Skills.UserDirs[0])
				if dir == "" {
					continue
				}
				dst := filepath.Join(dir, name)
				if seen[dst] {
					continue
				}
				seen[dst] = true
				if _, err := os.Lstat(dst); err == nil {
					continue
				}
				if err := os.MkdirAll(dir, 0755); err != nil {
					errs = append(errs, err)
					continue
				}
				if err := os.Symlink(src, dst); err != nil {
					errs = append(errs, err)
					continue
				}
				linked = append(linked, name+" → "+dir)
			}
		}
	}
	return linked, errors.Join(errs...)
}

// UnlinkPlugin removes every link into one plugin's directory from the agent
// trees, so removing the store entry afterwards cannot leave the agents
// holding dangling links.
//
// Links are matched by where they resolve rather than by name — the rule Scan
// and Delete already follow — because a user skill that happens to share a
// plugin skill's name is the user's, and taking it would be the mistake this
// resolution exists to prevent.
func UnlinkPlugin(dir string) error {
	resolved := resolve(dir)
	var errs []error
	for _, agent := range config.PredefinedAgents {
		for _, userDir := range agent.Skills.UserDirs {
			tree := ExpandUserDir(userDir)
			if tree == "" {
				continue
			}
			entries, err := os.ReadDir(tree)
			if err != nil {
				continue // an agent with no tree has no links to lose
			}
			for _, entry := range entries {
				if entry.Type()&fs.ModeSymlink == 0 {
					continue
				}
				path := filepath.Join(tree, entry.Name())
				target := resolve(path)
				if target == resolved || strings.HasPrefix(target, resolved+string(filepath.Separator)) {
					errs = append(errs, os.Remove(path))
				}
			}
		}
	}
	return errors.Join(errs...)
}

// attributePlugin marks a skill whose real directory sits in the plugin
// store. The links put it in an agent's user tree, so a scan first sees it as
// an ordinary user skill; the resolved path is what says where it truly
// lives, and the first path element under the store is which plugin brought
// it — worth naming, because "installed by ponytail" is what the user needs
// to know to update or remove it.
func attributePlugin(skill *Skill, store string) {
	if store == "" || !strings.HasPrefix(skill.Dir, store+string(filepath.Separator)) {
		return
	}
	rel := strings.TrimPrefix(skill.Dir, store+string(filepath.Separator))
	skill.Scope = ScopePlugin
	skill.Source = strings.SplitN(rel, string(filepath.Separator), 2)[0]
}
