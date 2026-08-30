package plugins

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const validManifest = `{"$schema": "` + ManifestSchema + `", "name": "shipped-plugin",
	"version": "1.2.0", "description": "A plugin for the tests."}` + "\n"

// plugin lays a plugin directory out on disk, one file per entry, so a test
// reads as the package layout it exercises.
func plugin(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoad_MinimalPlugin(t *testing.T) {
	dir := plugin(t, map[string]string{"plugin.json": validManifest})

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Name != "shipped-plugin" || p.Version != "1.2.0" {
		t.Errorf("manifest not carried through: %+v", p)
	}
	if len(p.Skills) != 0 || len(p.Servers) != 0 || len(p.Problems) != 0 {
		t.Errorf("a minimal plugin grew components: %+v", p)
	}
}

func TestLoad_RejectsADirectoryWithNoManifest(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("Load accepted a directory with no plugin.json")
	}
}

// Discovery is fixed at one level: an immediate child directory with a
// SKILL.md is a skill, and nothing deeper or looser is — the spec forbids
// recursing, which is also what keeps a skill's reference material from
// listing as more skills.
func TestLoad_DiscoversSkillsOneLevelDeep(t *testing.T) {
	dir := plugin(t, map[string]string{
		"plugin.json":                       validManifest,
		"skills/summarize/SKILL.md":         "---\nname: summarize\n---\n",
		"skills/deploy/SKILL.md":            "---\nname: deploy\n---\n",
		"skills/deploy/references/notes.md": "reference material",
		"skills/not-a-skill/README.md":      "no SKILL.md here",
		"skills/nested/inner/SKILL.md":      "too deep to be found",
		"skills/loose-file":                 "a file is not a skill",
	})

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(p.Skills, " "); got != "deploy summarize" {
		t.Errorf("skills = %q, want %q", got, "deploy summarize")
	}
	if len(p.Problems) != 0 {
		t.Errorf("problems = %v, want none — a childless directory is not an error", p.Problems)
	}
}

// A symlink is how a package path escapes the package, and a git clone can
// carry one. The skill is skipped and named, per the spec's narrowest failure
// boundary; the plugin and its honest skills survive.
func TestLoad_SkipsASkillThatEscapesThePlugin(t *testing.T) {
	outside := plugin(t, map[string]string{"SKILL.md": "---\nname: outside\n---\n"})
	dir := plugin(t, map[string]string{
		"plugin.json":            validManifest,
		"skills/honest/SKILL.md": "---\nname: honest\n---\n",
	})
	if err := os.Symlink(outside, filepath.Join(dir, "skills", "escape")); err != nil {
		t.Fatal(err)
	}

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(p.Skills, " "); got != "honest" {
		t.Errorf("skills = %q, want just the honest one", got)
	}
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "escape") {
		t.Errorf("problems = %v, want one naming the escaping skill", p.Problems)
	}
}

// A fixed location of the wrong filesystem kind invalidates that component
// and nothing else.
func TestLoad_ReportsSkillsThatIsNotADirectory(t *testing.T) {
	dir := plugin(t, map[string]string{
		"plugin.json": validManifest,
		"skills":      "a file where the skills directory should be",
	})

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(p.Skills) != 0 {
		t.Errorf("skills = %v, want none", p.Skills)
	}
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "not a directory") {
		t.Errorf("problems = %v, want one about the kind", p.Problems)
	}
}

// The components validate independently: a plugin whose server configuration
// is garbage still has every readable skill loaded.
func TestLoad_KeepsSkillsWhenMCPIsBroken(t *testing.T) {
	dir := plugin(t, map[string]string{
		"plugin.json":            validManifest,
		"skills/deploy/SKILL.md": "---\nname: deploy\n---\n",
		"mcp.json":               "not even json",
	})

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(p.Skills) != 1 {
		t.Errorf("skills = %v, want the one skill despite broken MCP", p.Skills)
	}
	if len(p.Problems) != 1 || !strings.Contains(p.Problems[0], "MCP disabled") {
		t.Errorf("problems = %v, want MCP disabled", p.Problems)
	}
}

func TestLoad_ReadsTheServers(t *testing.T) {
	dir := plugin(t, map[string]string{
		"plugin.json": validManifest,
		"mcp.json": `{"$schema": "` + MCPSchema + `", "mcpServers": {
			"validator": {"type": "stdio", "command": "./bin/validator"}}}`,
	})

	p, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(p.Servers) != 1 || p.Servers[0].Name != "validator" || p.Servers[0].Command != "./bin/validator" {
		t.Errorf("servers = %+v", p.Servers)
	}
}

// gitPlugin makes a local repository holding a plugin, so the install path can
// be tested without a network. The repository's own directory name is not the
// plugin's: the manifest decides what the store entry is called.
func gitPlugin(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := plugin(t, files)
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"add", "."},
		{"commit", "-qm", "plugin"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func TestInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := gitPlugin(t, map[string]string{
		"plugin.json":            validManifest,
		"skills/deploy/SKILL.md": "---\nname: deploy\ndescription: Ships it.\n---\n",
	})

	p, err := Install(src)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if p.Dir != filepath.Join(Dir(), "shipped-plugin") {
		t.Errorf("installed to %s, want the manifest's name in the store", p.Dir)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "skills", "deploy", "SKILL.md")); err != nil {
		t.Errorf("installed plugin is missing its skill: %v", err)
	}
	// The .git is kept for the same reason skill clones keep theirs: it is
	// what makes `git -C <dir> pull` an update.
	if _, err := os.Stat(filepath.Join(p.Dir, ".git")); err != nil {
		t.Errorf("install lost the clone's .git: %v", err)
	}
	src2, err := readSource(p.Dir)
	if err != nil {
		t.Fatalf("no provenance was recorded: %v", err)
	}
	if src2.URL != src || src2.Name != "shipped-plugin" {
		t.Errorf("provenance = %+v, want the install URL and name", src2)
	}

	list := Installed()
	if len(list) != 1 || list[0].Name != "shipped-plugin" || list[0].Origin != src {
		t.Errorf("Installed() = %+v, want the one plugin with its origin", list)
	}
}

// A repository that fails validation must leave nothing behind — not a store
// entry, and not staging litter for Installed to trip over.
func TestInstall_RefusesAnInvalidPluginAndCleansUp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := gitPlugin(t, map[string]string{"README.md": "not a plugin"})

	if _, err := Install(src); err == nil {
		t.Fatal("Install accepted a repository with no plugin.json")
	}
	entries, err := os.ReadDir(Dir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Errorf("the failed install left %s behind", entry.Name())
	}
	if got := Installed(); len(got) != 0 {
		t.Errorf("Installed() = %+v, want nothing", got)
	}
}

func TestInstall_RefusesASecondCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := gitPlugin(t, map[string]string{"plugin.json": validManifest})

	if _, err := Install(src); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	if _, err := Install(src); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("second Install = %v, want a refusal naming the conflict", err)
	}
}

func TestRemove(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := gitPlugin(t, map[string]string{"plugin.json": validManifest})
	if _, err := Install(src); err != nil {
		t.Fatal(err)
	}

	if err := Remove("shipped-plugin"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "shipped-plugin")); !os.IsNotExist(err) {
		t.Error("the store entry survived its removal")
	}
	if err := Remove("shipped-plugin"); err == nil {
		t.Error("removing a plugin twice should say it is not installed")
	}
}

// Remove takes a name, never a location: anything path-shaped is refused
// before it can reach beyond the store.
func TestRemove_RefusesAnythingButAName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, name := range []string{"", "..", "../tools", "a/b", ".hidden"} {
		if err := Remove(name); err == nil {
			t.Errorf("Remove(%q) accepted a non-name", name)
		}
	}
}

// A store entry that no longer loads still lists, failure and all. An
// invisible broken plugin is one nobody can find to remove.
func TestInstalled_ListsABrokenEntry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	broken := filepath.Join(Dir(), "broken-plugin")
	if err := os.MkdirAll(broken, 0755); err != nil {
		t.Fatal(err)
	}

	list := Installed()
	if len(list) != 1 || list[0].Name != "broken-plugin" {
		t.Fatalf("Installed() = %+v, want the broken entry by directory name", list)
	}
	if len(list[0].Problems) == 0 {
		t.Error("the broken entry does not say what is wrong with it")
	}
}
