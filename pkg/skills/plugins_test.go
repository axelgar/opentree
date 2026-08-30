package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/plugins"
)

// storePlugin fabricates an installed plugin directly in the store, skipping
// the clone: linking starts from what Installed reads, and Installed reads
// the store.
func storePlugin(t *testing.T, name string, skillNames ...string) string {
	t.Helper()
	dir := filepath.Join(plugins.Dir(), name)
	manifest := `{"$schema": "` + plugins.ManifestSchema + `", "name": "` + name + `"}`
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0644); err != nil {
		t.Fatal(err)
	}
	for _, skill := range skillNames {
		writeSkill(t, filepath.Join(dir, "skills"), skill,
			"---\nname: "+skill+"\ndescription: From a plugin.\n---\n")
	}
	return dir
}

// userTrees is every agent's canonical user tree, deduped — counted from the
// registry rather than written down, so a fifth agent does not turn these
// tests red.
func userTrees(t *testing.T) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for _, agent := range config.PredefinedAgents {
		if len(agent.Skills.UserDirs) == 0 {
			continue
		}
		dir := ExpandUserDir(agent.Skills.UserDirs[0])
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	if len(out) == 0 {
		t.Fatal("no agent in the registry has a user skills tree")
	}
	return out
}

func TestLinkPlugins_ReachesEveryAgentTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	src := storePlugin(t, "tools-plugin", "deploy")

	linked, err := LinkPlugins()
	if err != nil {
		t.Fatalf("LinkPlugins: %v", err)
	}
	trees := userTrees(t)
	if len(linked) != len(trees) {
		t.Errorf("linked %d, want one link per agent tree (%d)", len(linked), len(trees))
	}
	for _, tree := range trees {
		target, err := os.Readlink(filepath.Join(tree, "deploy"))
		if err != nil {
			t.Errorf("%s has no link to the plugin skill: %v", tree, err)
			continue
		}
		if want := filepath.Join(src, "skills", "deploy"); target != want {
			t.Errorf("%s links to %s, want %s", tree, target, want)
		}
	}
}

// Linking twice is the shape `skills sync` runs in: repair must find nothing
// to repair when nothing is broken.
func TestLinkPlugins_IsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	storePlugin(t, "tools-plugin", "deploy")

	if _, err := LinkPlugins(); err != nil {
		t.Fatal(err)
	}
	linked, err := LinkPlugins()
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 0 {
		t.Errorf("a second run linked again: %v", linked)
	}
}

// A user skill that shares a plugin skill's name keeps its tree. The plugin
// loses that one reader, which is the cheaper mistake — the user's skill may
// have diverged, and clobbering it loses work nothing is holding.
func TestLinkPlugins_NeverOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	storePlugin(t, "tools-plugin", "deploy")
	mine := "---\nname: deploy\ndescription: Mine, not the plugin's.\n---\n"
	tree := userTrees(t)[0]
	writeSkill(t, tree, "deploy", mine)

	if _, err := LinkPlugins(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(tree, "deploy", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Errorf("the user's own skill was replaced: %q", got)
	}
}

func TestUnlinkPlugin_TakesOnlyItsOwnLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	doomed := storePlugin(t, "doomed-plugin", "deploy")
	storePlugin(t, "kept-plugin", "research")
	if _, err := LinkPlugins(); err != nil {
		t.Fatal(err)
	}
	tree := userTrees(t)[0]
	writeSkill(t, tree, "hand-written", "---\nname: hand-written\ndescription: Not a link.\n---\n")

	if err := UnlinkPlugin(doomed); err != nil {
		t.Fatalf("UnlinkPlugin: %v", err)
	}
	for _, tree := range userTrees(t) {
		if _, err := os.Lstat(filepath.Join(tree, "deploy")); !os.IsNotExist(err) {
			t.Errorf("%s still links the removed plugin's skill", tree)
		}
		if _, err := os.Lstat(filepath.Join(tree, "research")); err != nil {
			t.Errorf("%s lost the other plugin's link: %v", tree, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tree, "hand-written", "SKILL.md")); err != nil {
		t.Errorf("a hand-written skill was taken with the plugin: %v", err)
	}
}

// The links land in user trees, so a scan first meets a plugin skill as an
// ordinary user skill reachable four ways. The resolved directory is what
// says a plugin owns it: one row, scope plugin, naming its source.
func TestScan_AttributesPluginSkills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	storePlugin(t, "tools-plugin", "deploy")
	if _, err := LinkPlugins(); err != nil {
		t.Fatal(err)
	}

	var rows []Skill
	for _, s := range Scan("") {
		if s.Name == "deploy" {
			rows = append(rows, s)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("the plugin skill lists %d times, want once: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Scope != ScopePlugin || row.Scope.String() != "plugin" {
		t.Errorf("scope = %v (%q), want plugin", row.Scope, row.Scope)
	}
	if row.Source != "tools-plugin" {
		t.Errorf("source = %q, want the plugin's name", row.Source)
	}
	if len(row.Agents) < 2 {
		t.Errorf("agents = %v, want the links to have reached more than one agent", row.Agents)
	}
}
