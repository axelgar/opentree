package skills

import (
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
	if got, want := settingsPath("~/.claude/settings.json", "/repo"), filepath.Join(home, ".claude/settings.json"); got != want {
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
