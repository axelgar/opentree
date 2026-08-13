package skills

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

// writeSkill creates dir/<name>/SKILL.md with the given frontmatter body.
func writeSkill(t *testing.T, dir, name, body string) string {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return skillDir
}

// claudeSpec is the registry's Claude Code entry, which every scan test leans
// on for its directory names.
func claudeSpec(t *testing.T) config.SkillsSpec {
	t.Helper()
	a := config.FindAgent("claude")
	if a == nil {
		t.Fatal("registry has no Claude Code entry")
	}
	return a.Skills
}

// claudeRepoDir is Claude Code's canonical per-repository skills directory.
func claudeRepoDir(t *testing.T) string {
	t.Helper()
	dirs := claudeSpec(t).RepoDirs
	if len(dirs) == 0 {
		t.Fatal("Claude Code has no repo skills directory")
	}
	return dirs[0]
}

func TestFrontmatter(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantName   string
		wantDesc   string
		wantManual bool
	}{
		{
			name:     "name and description",
			input:    "---\nname: research\ndescription: Investigate a question.\n---\n\nBody.",
			wantName: "research",
			wantDesc: "Investigate a question.",
		},
		{
			// A description almost always contains a colon, so cutting on the
			// last one — or on every one — would truncate most of the list.
			name:     "colon inside the description",
			input:    "---\nname: release\ndescription: Ship it: to all three channels.\n---\n",
			wantName: "release",
			wantDesc: "Ship it: to all three channels.",
		},
		{
			name:     "quoted values",
			input:    "---\nname: \"quoted\"\ndescription: 'single'\n---\n",
			wantName: "quoted",
			wantDesc: "single",
		},
		{
			// Nested keys belong to something else; picking their values up
			// would put a skill's metadata in the description column.
			name:     "ignores unrelated keys",
			input:    "---\nname: tagged\nmetadata:\n  type: user\n---\n",
			wantName: "tagged",
			wantDesc: "",
		},
		{
			// A skill asking not to be model-invoked is loaded and slash-
			// invocable, which is why it can be missing from an agent's skill
			// listing and still answer to its name.
			name:       "disable-model-invocation",
			input:      "---\nname: ask-matt\ndescription: A router.\ndisable-model-invocation: true\n---\n",
			wantName:   "ask-matt",
			wantDesc:   "A router.",
			wantManual: true,
		},
		{
			name:  "no frontmatter at all",
			input: "# Just a heading\n",
		},
		{
			name:  "unterminated block",
			input: "---\nname: broken\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := frontmatter(tt.input)
			if got.name != tt.wantName {
				t.Errorf("name = %q, want %q", got.name, tt.wantName)
			}
			if got.description != tt.wantDesc {
				t.Errorf("description = %q, want %q", got.description, tt.wantDesc)
			}
			if got.manualOnly != tt.wantManual {
				t.Errorf("manualOnly = %v, want %v", got.manualOnly, tt.wantManual)
			}
		})
	}
}

func TestScan_FindsUserAndRepoSkills(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".claude", "skills"), "research",
		"---\nname: research\ndescription: Read the primary sources.\n---\n")
	writeSkill(t, filepath.Join(repo, claudeRepoDir(t)), "release",
		"---\nname: release\ndescription: Ship it.\n---\n")

	got := Scan(repo)
	if len(got) != 2 {
		t.Fatalf("Scan found %d skills, want 2: %+v", len(got), got)
	}
	// Sorted by name: release before research.
	if got[0].Name != "release" || got[0].Scope != ScopeRepo {
		t.Errorf("first = %q/%v, want release/repo", got[0].Name, got[0].Scope)
	}
	if got[1].Name != "research" || got[1].Scope != ScopeUser {
		t.Errorf("second = %q/%v, want research/user", got[1].Name, got[1].Scope)
	}
	if got[1].Description != "Read the primary sources." {
		t.Errorf("description = %q", got[1].Description)
	}
	if !slices.Contains(got[0].Agents, "Claude Code") {
		t.Errorf("agents = %v, want Claude Code among them", got[0].Agents)
	}
}

func TestScan_SkipsNonSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	dir := filepath.Join(home, ".claude", "skills")
	writeSkill(t, dir, "real", "---\nname: real\ndescription: A skill.\n---\n")
	// A directory with no SKILL.md, and a loose file: neither is a skill, and
	// listing them would put phantom rows in the view.
	if err := os.MkdirAll(filepath.Join(dir, "empty-dir"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	got := Scan("")
	if len(got) != 1 || got[0].Name != "real" {
		t.Fatalf("Scan = %+v, want just the real skill", got)
	}
}

// A skill with no frontmatter still has a directory name, and a row that says
// nothing is better than a row that says "".
func TestScan_FallsBackToDirectoryName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".claude", "skills"), "no-frontmatter", "# Just a heading\n")

	got := Scan("")
	if len(got) != 1 {
		t.Fatalf("Scan = %+v, want 1 skill", got)
	}
	if got[0].Name != "no-frontmatter" {
		t.Errorf("name = %q, want the directory name", got[0].Name)
	}
}

// Missing directories are the normal case — most users have one agent
// installed, not both — so a scan of a machine with nothing on it must be
// empty rather than an error.
func TestScan_NoTreesAtAll(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := Scan(t.TempDir()); len(got) != 0 {
		t.Errorf("Scan = %+v, want empty", got)
	}
}

func TestExpandUserDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	t.Setenv("XDG_CONFIG_HOME", "")
	if got, want := ExpandUserDir("~/.claude/skills"), filepath.Join(home, ".claude/skills"); got != want {
		t.Errorf("ExpandUserDir = %q, want %q", got, want)
	}
	if got, want := ExpandUserDir("~/.config/opencode/skills"), filepath.Join(home, ".config/opencode/skills"); got != want {
		t.Errorf("without XDG = %q, want %q", got, want)
	}

	// opencode reads XDG_CONFIG_HOME, so a user who moved their config must not
	// be told they have no skills.
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := ExpandUserDir("~/.config/opencode/skills"), filepath.Join(xdg, "opencode/skills"); got != want {
		t.Errorf("with XDG = %q, want %q", got, want)
	}
	// ~/.claude is not under ~/.config and must not follow it.
	if got, want := ExpandUserDir("~/.claude/skills"), filepath.Join(home, ".claude/skills"); got != want {
		t.Errorf("claude with XDG set = %q, want %q", got, want)
	}

	if got := ExpandUserDir(""); got != "" {
		t.Errorf("empty spec = %q, want empty", got)
	}
	if got := ExpandUserDir("/absolute/path"); got != "/absolute/path" {
		t.Errorf("absolute path = %q, want it unchanged", got)
	}
}

func TestCopyTo(t *testing.T) {
	src := t.TempDir()
	dstTree := filepath.Join(t.TempDir(), "skills")
	dir := writeSkill(t, src, "shared", "---\nname: shared\ndescription: Works anywhere.\n---\n")
	// Nested files come along; a skill is a directory, not a single file.
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "notes.md"), []byte("notes"), 0644); err != nil {
		t.Fatal(err)
	}

	s := Skill{Name: "shared", Dir: dir}
	if err := CopyTo(s, dstTree); err != nil {
		t.Fatalf("CopyTo: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstTree, "shared", "references", "notes.md")); err != nil {
		t.Errorf("nested file did not come along: %v", err)
	}

	// Copying again must refuse: the two may have diverged, and overwriting is
	// the one mistake no git history would undo.
	if err := CopyTo(s, dstTree); err == nil {
		t.Error("CopyTo over an existing skill succeeded, want a refusal")
	}
}

func TestDelete(t *testing.T) {
	dir := writeSkill(t, t.TempDir(), "doomed", "---\nname: doomed\n---\n")
	if err := Delete(Skill{Name: "doomed", Dir: dir}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("skill directory survived deletion")
	}
	// A Skill with no directory would delete the working directory if RemoveAll
	// were handed "".
	if err := Delete(Skill{Name: "empty"}); err == nil {
		t.Error("Delete with no directory succeeded, want a refusal")
	}
}

// The bug this replaced: opentree reported "OpenCode 0" while opencode was
// visibly offering the same skills as slash commands. opencode 1.18.16's own
// documentation lists ~/.claude/skills under "External skills (auto-loaded)",
// so a skill installed once is readable by both agents and has to say so.
func TestScan_OpenCodeReadsClaudeCodesSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".claude", "skills"), "ask-matt",
		"---\nname: ask-matt\ndescription: A router over the skills in this repo.\n---\n")

	got := Scan("")
	if len(got) != 1 {
		t.Fatalf("Scan = %+v, want one skill listed once, not once per agent", got)
	}
	for _, want := range []string{"Claude Code", "OpenCode"} {
		if !slices.Contains(got[0].Agents, want) {
			t.Errorf("agents = %v, want %s among them", got[0].Agents, want)
		}
	}
}

// opencode spells its own trees "skill(s)" — both are read. A user with the
// singular directory would otherwise be shown nothing.
func TestScan_AcceptsSingularSkillDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".config", "opencode", "skill"), "solo",
		"---\nname: solo\ndescription: In the singular directory.\n---\n")

	got := Scan("")
	if len(got) != 1 || got[0].Name != "solo" {
		t.Fatalf("Scan = %+v, want the skill in the singular directory", got)
	}
}

// Every tree is read once. Scanning per agent instead of per directory would
// list one shared skill twice, which is what the earlier model did.
func TestTrees_AreDeduplicated(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	seen := map[string]bool{}
	for _, tree := range Trees("/repo") {
		if seen[tree.Dir] {
			t.Errorf("%s listed twice", tree.Dir)
		}
		seen[tree.Dir] = true
	}

	// The shared external tree carries both readers.
	claudeUser := ExpandUserDir("~/.claude/skills")
	for _, tree := range Trees("") {
		if tree.Dir != claudeUser {
			continue
		}
		if len(tree.Agents) < 2 {
			t.Errorf("%s agents = %v, want both readers", tree.Dir, tree.Agents)
		}
		return
	}
	t.Errorf("Trees did not include %s", claudeUser)
}

// A skill installer that keeps the real directory in one tree and links it
// into another makes the same SKILL.md reachable by two paths. That is one
// skill both agents can read, not two, and the surviving entry has to be the
// real directory so deleting it removes the skill rather than a link to it.
func TestScan_CollapsesTreesLinkedToTheSameSkill(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	real := writeSkill(t, filepath.Join(home, ".agents", "skills"), "ask-matt",
		"---\nname: ask-matt\ndescription: A router over the skills.\n---\n")

	claudeTree := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(claudeTree, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(claudeTree, "ask-matt")); err != nil {
		t.Fatal(err)
	}

	got := Scan("")
	if len(got) != 1 {
		t.Fatalf("Scan = %+v, want the linked skill listed once", got)
	}
	if got[0].Dir != resolve(real) {
		t.Errorf("Dir = %q, want the real directory %q", got[0].Dir, real)
	}
	for _, want := range []string{"Claude Code", "OpenCode"} {
		if !slices.Contains(got[0].Agents, want) {
			t.Errorf("agents = %v, want %s among them", got[0].Agents, want)
		}
	}
	// Registry order, so the marks never reshuffle between renders.
	if got[0].Agents[0] != "OpenCode" {
		t.Errorf("agents = %v, want registry order", got[0].Agents)
	}
}

// Two different files answering to one name is a real conflict — one shadows
// the other — and must stay two rows.
func TestScan_KeepsGenuinelyDifferentSkillsOfTheSameName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".claude", "skills"), "review",
		"---\nname: review\ndescription: Claude's version.\n---\n")
	writeSkill(t, filepath.Join(home, ".config", "opencode", "skills"), "review",
		"---\nname: review\ndescription: OpenCode's version.\n---\n")

	if got := Scan(""); len(got) != 2 {
		t.Fatalf("Scan = %+v, want both kept — they are different files", got)
	}
}

// A skill with no description is loaded and answers to its name, but has
// nothing for a model to match against — so the agent does not offer it
// unaided. That is the state disable-model-invocation asks for, reached by
// omission rather than on purpose, and a row calling it plain "on" promises a
// capability that is not there.
func TestScan_ADescriptionlessSkillIsManualOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	tree := filepath.Join(home, ".claude", "skills")
	writeSkill(t, tree, "bare", "---\nname: bare\n---\n\nBody with no description.\n")
	writeSkill(t, tree, "described", "---\nname: described\ndescription: Says what it does.\n---\n")

	for _, s := range Scan("") {
		want := StateOn
		if s.Name == "bare" {
			want = StateManualOnly
		}
		if got := s.State("Claude Code"); got != want {
			t.Errorf("%s: State = %q, want %q", s.Name, got, want)
		}
	}
}

// An explicit override still wins. The missing description is a default, not a
// verdict, and a user who wrote a state into settings meant it.
func TestScan_AnOverrideBeatsTheMissingDescription(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	writeSkill(t, filepath.Join(home, ".claude", "skills"), "bare", "---\nname: bare\n---\n")
	writeSettings(t, filepath.Join(home, ".claude"), `{"skillOverrides": {"bare": "off"}}`)

	found := Scan("")
	if len(found) != 1 {
		t.Fatalf("Scan() = %d skills, want 1", len(found))
	}
	if got := found[0].State("Claude Code"); got != StateOff {
		t.Errorf("State = %q, want the override to win", got)
	}
}
