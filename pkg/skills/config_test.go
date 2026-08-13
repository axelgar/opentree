package skills

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

// agentSpec is one registry entry's skills, by command.
func agentSpec(t *testing.T, command string) config.SkillsSpec {
	t.Helper()
	a := config.FindAgent(command)
	if a == nil {
		t.Fatalf("registry has no %s entry", command)
	}
	return a.Skills
}

func opencodeSpec(t *testing.T) config.SkillsSpec {
	t.Helper()
	return agentSpec(t, "opencode")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestStripJSONC(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"line comment", "{\n // a note\n \"a\": 1\n}", `{"a":1}`},
		{"block comment", `{/* a */ "a": 1}`, `{"a":1}`},
		{"trailing comma in object", `{"a": 1,}`, `{"a":1}`},
		{"trailing comma in array", `{"a": [1, 2,]}`, `{"a":[1,2]}`},
		{"comma before a comment then close", "{\"a\": 1, // why\n}", `{"a":1}`},
		// The one a naive stripper gets wrong.
		{"slashes inside a string survive", `{"a": "https://x/y"}`, `{"a":"https://x/y"}`},
		{"an escaped quote does not end the string", `{"a": "say \" // no"}`, `{"a":"say \" // no"}`},
		{"plain json is unchanged but for whitespace", "{\n  \"a\": 1\n}", `{"a":1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(stripJSONC([]byte(tt.input))); got != tt.want {
				t.Errorf("stripJSONC() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDeclaredDirs_ArrayShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	writeFile(t, path, `{"skills": ["./team", "/abs/tree", "https://example.com/list.json"]}`)

	got := declaredDirs(path, "skills")
	want := []string{filepath.Join(dir, "team"), "/abs/tree"}
	if !slices.Equal(got, want) {
		t.Errorf("declaredDirs() = %v, want %v (a URL is not a directory)", got, want)
	}
}

// The older shape, which opencode still migrates from — a config written before
// the flat array should not read as "no skills registered".
func TestDeclaredDirs_ObjectShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	writeFile(t, path, `{"skills": {"paths": ["./team"], "urls": ["https://example.com/list.json"]}}`)

	got := declaredDirs(path, "skills")
	if want := []string{filepath.Join(dir, "team")}; !slices.Equal(got, want) {
		t.Errorf("declaredDirs() = %v, want %v", got, want)
	}
}

func TestDeclaredDirs_ExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	writeFile(t, path, `{"skills": ["~/elsewhere"]}`)

	if got := declaredDirs(path, "skills"); !slices.Equal(got, []string{filepath.Join(home, "elsewhere")}) {
		t.Errorf("declaredDirs() = %v, want the home-relative path expanded", got)
	}
}

func TestDeclaredDirs_ToleratesMissingAndBroken(t *testing.T) {
	dir := t.TempDir()
	if got := declaredDirs(filepath.Join(dir, "absent.json"), "skills"); got != nil {
		t.Errorf("an absent config gave %v, want nil", got)
	}

	broken := filepath.Join(dir, "opencode.json")
	writeFile(t, broken, `{ not json at all`)
	if got := declaredDirs(broken, "skills"); got != nil {
		t.Errorf("an unparseable config gave %v, want nil", got)
	}

	none := filepath.Join(dir, "none.json")
	writeFile(t, none, `{"model": "anthropic/claude"}`)
	if got := declaredDirs(none, "skills"); got != nil {
		t.Errorf("a config with no skills key gave %v, want nil", got)
	}
}

// Each agent keeps its registered directories under a name of its own —
// "skills" for opencode, "skillDirectories" for Copilot — and reading the wrong
// one finds nothing without saying so.
func TestConfigTrees_ReadsEachAgentsOwnKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	spec := agentSpec(t, "copilot")
	writeFile(t, filepath.Join(home, ".copilot/settings.json"),
		`{"skillDirectories": ["/team/tree"], "skills": ["/not/this/one"]}`)

	if got := configTrees(spec, ""); !slices.Equal(got, []string{"/team/tree"}) {
		t.Errorf("configTrees() = %v, want the directory under this agent's own key", got)
	}
}

func TestConfigTrees_ReadsGlobalAndRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	repo := t.TempDir()

	writeFile(t, filepath.Join(home, ".config/opencode/opencode.json"), `{"skills": ["/global/tree"]}`)
	writeFile(t, filepath.Join(repo, "opencode.json"), `{"skills": ["./project-skills"]}`)

	got := configTrees(opencodeSpec(t), repo)
	want := []string{"/global/tree", filepath.Join(repo, "project-skills")}
	if !slices.Equal(got, want) {
		t.Errorf("configTrees() = %v, want %v", got, want)
	}
}

// A registered directory is searched at any depth, unlike the standard trees:
// the user named a place to look rather than a tree of skills.
func TestScan_WalksARegisteredDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	repo := t.TempDir()

	writeSkill(t, filepath.Join(repo, "team", "nested"), "deep",
		"---\nname: deep\ndescription: Buried.\n---\n")
	writeFile(t, filepath.Join(repo, "opencode.json"), `{"skills": ["./team"]}`)

	found := Scan(repo)
	i := slices.IndexFunc(found, func(s Skill) bool { return s.Name == "deep" })
	if i < 0 {
		t.Fatalf("Scan() did not find the nested skill: %v", found)
	}
	if !slices.Contains(found[i].Agents, "OpenCode") {
		t.Errorf("Agents = %v, want the agent whose config registered the tree", found[i].Agents)
	}
	if found[i].Scope != ScopeRepo {
		t.Errorf("Scope = %v, want repo for a directory inside the repository", found[i].Scope)
	}
}

// Only registered trees are walked. A SKILL.md nested inside a standard tree is
// a skill's own reference material, and listing it would put a row in front of
// the user that no agent will ever load.
func TestScan_DoesNotWalkTheStandardTrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".claude/skills"), "outer",
		"---\nname: outer\ndescription: The real skill.\n---\n")
	writeSkill(t, filepath.Join(home, ".claude/skills/outer", "examples"), "inner",
		"---\nname: inner\ndescription: An example, not a skill.\n---\n")

	for _, s := range Scan("") {
		if s.Name == "inner" {
			t.Errorf("Scan() listed a nested SKILL.md from a standard tree")
		}
	}
}
