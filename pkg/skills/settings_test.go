package skills

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
)

// realSettings is shaped like a settings file people actually keep: several
// hand-ordered keys, the overrides object in the middle, two-space indent.
const realSettings = `{
  "permissions": {
    "defaultMode": "auto"
  },
  "model": "opus",
  "skillOverrides": {
    "tdd": "off",
    "triage": "off"
  },
  "hooks": {},
  "statusLine": {
    "type": "command"
  },
  "voiceEnabled": false
}
`

func writeSettings(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// specFor points the registry's settings mechanism at a temp file.
func specFor(path string) config.SkillsSpec {
	return config.SkillsSpec{SettingsFiles: []string{path}, OverridesKey: "skillOverrides"}
}

func TestReadOverrides(t *testing.T) {
	path := writeSettings(t, t.TempDir(), realSettings)
	got := readOverrides(specFor(path), "")
	if len(got) != 2 {
		t.Fatalf("readOverrides = %v, want two entries", got)
	}
	if got["tdd"] != StateOff {
		t.Errorf("tdd = %q, want off", got["tdd"])
	}
}

// Later files win, which is the order the agent resolves them in.
func TestReadOverrides_LayersFiles(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	local := filepath.Join(dir, "local.json")
	if err := os.WriteFile(user, []byte(`{"skillOverrides":{"tdd":"off","teach":"off"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`{"skillOverrides":{"tdd":"on"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	spec := config.SkillsSpec{SettingsFiles: []string{user, local}, OverridesKey: "skillOverrides"}

	got := readOverrides(spec, "")
	if got["tdd"] != StateOn {
		t.Errorf("tdd = %q, want the later file's on", got["tdd"])
	}
	if got["teach"] != StateOff {
		t.Errorf("teach = %q, want off — the earlier file still counts", got["teach"])
	}
}

// The other shape the same files come in: a list of the disabled, kept at each
// scope, where a name missing from one file says nothing at all. So the two
// union rather than the later one winning — which is what `gemini skills
// disable` was observed to do with --scope user and --scope workspace.
func TestReadOverrides_UnionsADisabledList(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	workspace := filepath.Join(dir, "workspace.json")
	if err := os.WriteFile(user, []byte(`{"skills":{"disabled":["tdd"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspace, []byte(`{"skills":{"disabled":["teach"]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	spec := config.SkillsSpec{SettingsFiles: []string{user, workspace}, DisabledKey: "skills.disabled"}

	got := readOverrides(spec, "")
	for _, name := range []string{"tdd", "teach"} {
		if got[name] != StateOff {
			t.Errorf("%s = %q, want off — a skill switched off at either scope stays off", name, got[name])
		}
	}
}

// A missing or unparseable settings file is the common case on a fresh machine,
// and must read as "nothing overridden" rather than fail the scan.
func TestReadOverrides_ToleratesMissingAndBroken(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	spec := config.SkillsSpec{
		SettingsFiles: []string{filepath.Join(dir, "absent.json"), broken},
		OverridesKey:  "skillOverrides",
	}
	if got := readOverrides(spec, ""); len(got) != 0 {
		t.Errorf("readOverrides = %v, want empty", got)
	}
	// An agent with no override mechanism has none, not an empty search.
	if got := readOverrides(config.SkillsSpec{}, ""); got != nil {
		t.Errorf("readOverrides with no key = %v, want nil", got)
	}
}

// The heart of it: changing one entry must not reformat the rest of a file the
// user hand-maintains and probably keeps in git.
func TestSetOverride_LeavesTheRestOfTheFileAlone(t *testing.T) {
	path := writeSettings(t, t.TempDir(), realSettings)
	spec := specFor(path)

	if _, err := SetOverride(spec, "", "research", StateOff); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Every other key keeps its position and its bytes.
	before, _, _ := strings.Cut(realSettings, `  "skillOverrides"`)
	if !strings.HasPrefix(string(after), before) {
		t.Errorf("bytes before the overrides changed:\n%s", after)
	}
	_, tail, _ := strings.Cut(realSettings, "  },\n  \"hooks\"")
	if !strings.HasSuffix(string(after), "  \"hooks\""+tail) {
		t.Errorf("bytes after the overrides changed:\n%s", after)
	}
	// Key order is preserved, which a whole-document rewrite would lose.
	var order []string
	dec := json.NewDecoder(strings.NewReader(string(after)))
	dec.Token()
	for dec.More() {
		k, _ := dec.Token()
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		order = append(order, k.(string))
	}
	want := []string{"permissions", "model", "skillOverrides", "hooks", "statusLine", "voiceEnabled"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("key order = %v, want %v", order, want)
	}

	if got := readOverrides(spec, "")["research"]; got != StateOff {
		t.Errorf("research = %q, want off", got)
	}
}

// Turning a skill back on clears the override rather than writing "on": a skill
// whose own frontmatter asks not to be model-invoked would be promoted to fully
// automatic by an explicit "on", which is not what turning it on means.
func TestSetOverride_ClearingRemovesTheEntry(t *testing.T) {
	path := writeSettings(t, t.TempDir(), realSettings)
	spec := specFor(path)

	if _, err := SetOverride(spec, "", "tdd", ""); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	got := readOverrides(spec, "")
	if _, still := got["tdd"]; still {
		t.Errorf("tdd is still overridden: %v", got)
	}
	if got["triage"] != StateOff {
		t.Errorf("triage = %q, want the other override untouched", got["triage"])
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), `"tdd"`) {
		t.Errorf("tdd still present in the file:\n%s", data)
	}
}

// A settings file with no overrides object yet gets one, without disturbing
// what is already there.
func TestSetOverride_InsertsTheKeyWhenAbsent(t *testing.T) {
	path := writeSettings(t, t.TempDir(), "{\n  \"model\": \"opus\"\n}\n")
	spec := specFor(path)

	if _, err := SetOverride(spec, "", "tdd", StateOff); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	if got := readOverrides(spec, "")["tdd"]; got != StateOff {
		t.Errorf("tdd = %q, want off", got)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("wrote invalid JSON: %v\n%s", err, data)
	}
	if doc["model"] != "opus" {
		t.Errorf("model was lost: %s", data)
	}
}

func TestSetOverride_CreatesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	spec := specFor(path)
	if _, err := SetOverride(spec, "", "tdd", StateOff); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	if got := readOverrides(spec, "")["tdd"]; got != StateOff {
		t.Errorf("tdd = %q, want off", got)
	}
}

// An agent with no override mechanism refuses rather than inventing a file.
func TestSetOverride_RefusesWithoutAMechanism(t *testing.T) {
	if _, err := SetOverride(config.SkillsSpec{}, "", "tdd", StateOff); err == nil {
		t.Error("SetOverride succeeded for an agent with no overrides key")
	}
}

// The write lands where the override already lives, so it takes effect rather
// than being shadowed by a lower-precedence file that still says off.
func TestOverrideFile_PrefersWhereTheOverrideAlreadyIs(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	local := filepath.Join(dir, "local.json")
	if err := os.WriteFile(user, []byte(`{"skillOverrides":{"tdd":"off"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`{"skillOverrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	spec := config.SkillsSpec{SettingsFiles: []string{user, local}, OverridesKey: "skillOverrides"}

	if got := OverrideFile(spec, "", "tdd"); got != user {
		t.Errorf("OverrideFile = %q, want the file already holding it (%q)", got, user)
	}
	// A skill nobody has overridden goes to the first file the agent reads.
	if got := OverrideFile(spec, "", "brand-new"); got != user {
		t.Errorf("OverrideFile = %q, want the first settings file (%q)", got, user)
	}
}

func TestSettingsPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, want := settingsPath("~/.claude/settings.json", "/repo"), filepath.Join(home, ".claude", "settings.json"); got != want {
		t.Errorf("settingsPath = %q, want %q", got, want)
	}
	if got, want := settingsPath(".claude/settings.json", "/repo"), "/repo/.claude/settings.json"; got != want {
		t.Errorf("settingsPath = %q, want %q", got, want)
	}
	// Outside a repository there is no project settings file to read.
	if got := settingsPath(".claude/settings.json", ""); got != "" {
		t.Errorf("settingsPath = %q, want empty", got)
	}
	// An absolute path is taken as given, repository or not.
	if got := settingsPath("/etc/opentree/settings.json", ""); got != "/etc/opentree/settings.json" {
		t.Errorf("settingsPath = %q, want it unchanged", got)
	}
}

// geminiSettings is the other shape people keep: a list of the disabled nested
// a level down, with hand-ordered keys either side of it at both levels.
const geminiSettings = `{
  "theme": "GitHub",
  "skills": {
    "folders": ["~/work/skills"],
    "disabled": [
      "tdd"
    ]
  },
  "selectedAuthType": "oauth-personal"
}
`

func geminiSpec(paths ...string) config.SkillsSpec {
	return config.SkillsSpec{SettingsFiles: paths, DisabledKey: "skills.disabled"}
}

func TestSetDisabled_AddsAName(t *testing.T) {
	path := writeSettings(t, t.TempDir(), geminiSettings)
	spec := geminiSpec(path)

	if _, err := SetDisabled(spec, "", "teach", StateOff); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	got := readOverrides(spec, "")
	for _, name := range []string{"tdd", "teach"} {
		if got[name] != StateOff {
			t.Errorf("%s = %q, want off", name, got[name])
		}
	}

	// Everything around it keeps its bytes — at both levels, since the list is
	// nested and the keys beside it are the user's own.
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, keep := range []string{
		`"theme": "GitHub"`,
		`"folders": ["~/work/skills"]`,
		`"selectedAuthType": "oauth-personal"`,
	} {
		if !strings.Contains(string(body), keep) {
			t.Errorf("%s was rewritten:\n%s", keep, body)
		}
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("the settings file stopped being JSON: %v\n%s", err, body)
	}
}

// The list sits two levels in, and marshalling it as if it were at the margin
// left the whole file looking edited. Pinned to the byte, because "still valid
// JSON" was true of the wrong version too.
func TestSetDisabled_KeepsTheDocumentsShape(t *testing.T) {
	path := writeSettings(t, t.TempDir(), geminiSettings)
	if _, err := SetDisabled(geminiSpec(path), "", "teach", StateOff); err != nil {
		t.Fatal(err)
	}
	const want = `{
  "theme": "GitHub",
  "skills": {
    "folders": ["~/work/skills"],
    "disabled": [
      "tdd",
      "teach"
    ]
  },
  "selectedAuthType": "oauth-personal"
}
`
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The files union when read, so clearing only the one an addition would go to
// would report a skill as on that the agent goes on ignoring.
func TestSetDisabled_ClearsEveryFileThatNamesIt(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.json")
	workspace := filepath.Join(dir, "workspace.json")
	for _, path := range []string{user, workspace} {
		if err := os.WriteFile(path, []byte(`{"skills":{"disabled":["tdd","teach"]}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	spec := geminiSpec(user, workspace)

	if _, err := SetDisabled(spec, "", "tdd", ""); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	if got := readOverrides(spec, ""); got["tdd"] == StateOff {
		t.Error("tdd is still off — a file that named it was left alone")
	}
	// And the skill beside it on both lists is untouched.
	if got := readOverrides(spec, ""); got["teach"] != StateOff {
		t.Errorf("teach = %q, want it left off", got["teach"])
	}
}

// A list says a name is on it or it is not. Anything in between is refused
// rather than rounded to the nearest thing the list can hold.
func TestSetDisabled_RefusesWhatAListCannotSay(t *testing.T) {
	path := writeSettings(t, t.TempDir(), geminiSettings)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SetDisabled(geminiSpec(path), "", "teach", StateManualOnly); err == nil {
		t.Fatal("want a refusal, got none")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("the refused write changed the file anyway")
	}
}

// Nothing to nest into: the branch is written whole, which is the one case
// where opentree chooses the shape of something it did not find.
func TestSetDisabled_WritesTheBranchWhenThereIsNone(t *testing.T) {
	path := writeSettings(t, t.TempDir(), "{\n  \"theme\": \"GitHub\"\n}\n")
	spec := geminiSpec(path)

	if _, err := SetDisabled(spec, "", "teach", StateOff); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	if got := readOverrides(spec, ""); got["teach"] != StateOff {
		t.Errorf("teach = %q, want off", got["teach"])
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"theme": "GitHub"`) {
		t.Errorf("the key already there was lost:\n%s", body)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("not JSON afterwards: %v\n%s", err, body)
	}
}

// Callers ask for a state, not for a mechanism.
func TestSetState_UsesWhicheverTheAgentHas(t *testing.T) {
	overrides := writeSettings(t, t.TempDir(), realSettings)
	list := writeSettings(t, t.TempDir(), geminiSettings)

	for _, tc := range []struct {
		name string
		spec config.SkillsSpec
	}{
		{"a map of states", specFor(overrides)},
		{"a list of the disabled", geminiSpec(list)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SetState(tc.spec, "", "teach", StateOff); err != nil {
				t.Fatalf("SetState: %v", err)
			}
			if got := readOverrides(tc.spec, ""); got["teach"] != StateOff {
				t.Errorf("teach = %q, want off", got["teach"])
			}
		})
	}

	// An agent with neither says so rather than reporting a write it did not do.
	if _, err := SetState(config.SkillsSpec{}, "", "teach", StateOff); err == nil {
		t.Error("an agent with no mechanism accepted a write")
	}
}

func TestStateLabel(t *testing.T) {
	// On is the unremarkable case and earns no badge, so a clean list stays clean.
	if got := StateOn.Label(); got != "" {
		t.Errorf("StateOn.Label() = %q, want empty", got)
	}
	for state, want := range map[State]string{
		StateOff:        "off",
		StateManualOnly: "manual",
		StateNameOnly:   "name only",
	} {
		if got := state.Label(); got != want {
			t.Errorf("%q.Label() = %q, want %q", state, got, want)
		}
	}
}
