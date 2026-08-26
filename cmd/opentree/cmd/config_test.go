package cmd

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
)

// configStructKeys is every setting opentree.toml can spell, read off the type
// that parses it, mapped to the kind of value it holds. Deriving them rather
// than listing them is the point: a test that repeats the registry by hand
// drifts from it exactly as readily as the four hand-kept lists the registry
// replaced. A pointer field — the ones that tell "said nothing" apart from
// "said false" — is reported as the kind it points at.
func configStructKeys(t *testing.T) map[string]reflect.Kind {
	t.Helper()
	keys := map[string]reflect.Kind{}
	top := reflect.TypeOf(config.Config{})
	for i := range top.NumField() {
		section := top.Field(i)
		name := tomlName(section)
		if name == "" || section.Type.Kind() != reflect.Struct {
			t.Fatalf("config.Config field %s is not a tagged section — teach this test what it is", section.Name)
		}
		for j := range section.Type.NumField() {
			field := section.Type.Field(j)
			if !field.IsExported() {
				continue
			}
			leaf := tomlName(field)
			if leaf == "" {
				t.Fatalf("%s.%s carries no toml tag", section.Name, field.Name)
			}
			kind := field.Type
			if kind.Kind() == reflect.Pointer {
				kind = kind.Elem()
			}
			keys[name+"."+leaf] = kind.Kind()
		}
	}
	return keys
}

func tomlName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	return name
}

// The registry in config.go is the CLI's entire idea of what a setting is: what
// `config list` prints, what `get` resolves, what `set` writes, what the help
// documents and what the shell completes. A field added to config.Config and
// not to that table is invisible from the command line, which is how
// workspace.setup and workspace.run — the commands the trust gate exists to
// approve — came to answer "unknown config key" when asked for by name.
func TestConfigKeys_NameEverySettingTheConfigCarries(t *testing.T) {
	unclaimed := make(map[string]bool, len(configKeys))
	for _, k := range configKeys {
		if unclaimed[k.name] {
			t.Errorf("%s appears twice in configKeys", k.name)
		}
		unclaimed[k.name] = true
	}

	for _, name := range slices.Sorted(maps.Keys(configStructKeys(t))) {
		if !unclaimed[name] {
			t.Errorf("config.Config carries %s and the config CLI does not — add it to configKeys", name)
		}
		delete(unclaimed, name)
	}
	for _, name := range slices.Sorted(maps.Keys(unclaimed)) {
		t.Errorf("configKeys names %s, which opentree.toml has nowhere to put", name)
	}
}

// An entry has to agree with the shape of the setting it names, and this is the
// drift that damages the file rather than the CLI: `config set` writes the value
// straight into opentree.toml, so a boolean registered without a parser lands as
// auto_push = "true", which no longer unmarshals into a *bool — the next command
// fails to read the config it was just asked to change. A list registered as a
// scalar goes the same way.
func TestConfigKeys_MatchTheShapeOfTheSettingTheyName(t *testing.T) {
	kinds := configStructKeys(t)
	for _, k := range configKeys {
		kind, known := kinds[k.name]
		if !known {
			continue // reported by TestConfigKeys_NameEverySettingTheConfigCarries
		}
		switch kind {
		case reflect.Slice:
			if k.settable() {
				t.Errorf("%s is a list — it needs a listOf, or `set` will write one string over the lot", k.name)
			}
		case reflect.Bool:
			if k.parse == nil {
				t.Errorf("%s is a boolean and has no parser — `set` would quote it into a string", k.name)
			}
		case reflect.String:
			if !k.settable() {
				t.Errorf("%s is one string; there is nothing for `set` to split, so nothing to refuse", k.name)
			}
			if k.parse != nil {
				t.Errorf("%s is a string and needs no parser", k.name)
			}
		default:
			t.Errorf("%s is a %s, a shape `config set` has never had to write — decide what it does before shipping it", k.name, kind)
		}
	}
}

// Every entry must be whole. A record missing its reader panics `config list`
// on the line it reaches, and one missing its source accessor prints a blank
// pair of brackets where the file name goes.
func TestConfigKeys_EntriesAreComplete(t *testing.T) {
	for _, k := range configKeys {
		if k.desc == "" {
			t.Errorf("%s has no description — completion and the help block both print it", k.name)
		}
		if k.get == nil {
			t.Errorf("%s has no reader", k.name)
		}
		if k.source == nil {
			t.Errorf("%s has no source accessor", k.name)
		}
	}
}

// Each key must report its own source. The failure this pins is a copied line:
// two entries sharing one accessor makes `config list` attribute a value to the
// file a different setting came from, which is worse than saying nothing.
func TestConfigKeys_ReportDistinctSources(t *testing.T) {
	// Every field of ConfigSource set to its own name, so the accessor's answer
	// identifies which field it read.
	var sources config.ConfigSource
	v := reflect.ValueOf(&sources).Elem()
	for i := range v.NumField() {
		v.Field(i).SetString(v.Type().Field(i).Name)
	}

	seen := map[string]string{}
	for _, k := range configKeys {
		if k.source == nil {
			continue
		}
		got := k.source(sources)
		if got == "" {
			t.Errorf("%s reports no source field", k.name)
			continue
		}
		if other, dup := seen[got]; dup {
			t.Errorf("%s and %s both read ConfigSource.%s", other, k.name, got)
		}
		seen[got] = k.name
	}
}

// getConfigValue answers for every key in the registry, in the spelling
// opentree.toml uses, so what comes back is what you would type in. The values
// are all distinct so a reader wired to the neighbouring field is caught.
func TestGetConfigValue_ReadsEverySetting(t *testing.T) {
	autoPush := true
	desktop := false
	cfg := &config.Config{
		Agent:     config.AgentConfig{Command: "opencode"},
		Worktree:  config.WorktreeConfig{BaseDir: ".opentree", DefaultBase: "main"},
		Workspace: config.WorkspaceConfig{Setup: []string{"npm ci"}, Seed: []string{".env"}, Run: "npm run dev", Check: "npm test"},
		Tmux:      config.TmuxConfig{SessionPrefix: "ot"},
		GitHub:    config.GitHubConfig{AutoPush: &autoPush},
		Notify:    config.NotifyConfig{On: []string{"blocked"}, Desktop: &desktop},
	}

	want := map[string]string{
		"agent.command":         "opencode",
		"worktree.base_dir":     ".opentree",
		"worktree.default_base": "main",
		"workspace.setup":       `["npm ci"]`,
		"workspace.seed":        `[".env"]`,
		"workspace.run":         "npm run dev",
		"workspace.check":       "npm test",
		"tmux.session_prefix":   "ot",
		"github.auto_push":      "true",
		"notify.on":             `["blocked"]`,
		"notify.desktop":        "false",
	}

	for _, k := range configKeys {
		expected, pinned := want[k.name]
		if !pinned {
			t.Errorf("%s is in the registry with no value pinned here — add a case", k.name)
			continue
		}
		got, err := getConfigValue(cfg, k.name)
		if err != nil {
			t.Errorf("getConfigValue(%s) = %v, want %q", k.name, err, expected)
			continue
		}
		if got != expected {
			t.Errorf("getConfigValue(%s) = %q, want %q", k.name, got, expected)
		}
	}
}

// The regression: `opentree config get workspace.setup` answered "unknown
// config key" for the two settings `opentree trust` prints and approves. There
// was no way to ask opentree what it thought it had been approved to run.
func TestGetConfigValue_AnswersForTheTrustGatedCommands(t *testing.T) {
	cfg := &config.Config{Workspace: config.WorkspaceConfig{
		Setup: []string{"npm ci", "npm run build"},
		Run:   "npm run dev",
	}}

	setup, err := getConfigValue(cfg, "workspace.setup")
	if err != nil {
		t.Fatalf("getConfigValue(workspace.setup) = %v, want the commands the trust gate approves", err)
	}
	if setup != `["npm ci", "npm run build"]` {
		t.Errorf("workspace.setup = %q, want it spelled the way opentree.toml spells it", setup)
	}

	run, err := getConfigValue(cfg, "workspace.run")
	if err != nil {
		t.Fatalf("getConfigValue(workspace.run) = %v, want the dev server command", err)
	}
	if run != "npm run dev" {
		t.Errorf("workspace.run = %q, want npm run dev", run)
	}
}

func TestGetConfigValue_UnknownKeySaysWhereToLook(t *testing.T) {
	_, err := getConfigValue(config.Default(), "worktree.base")
	if err == nil {
		t.Fatal("expected an error for a key that does not exist")
	}
	if !strings.Contains(err.Error(), "worktree.base") || !strings.Contains(err.Error(), "config list") {
		t.Errorf("error = %q, want it to quote the key and point at `config list`", err)
	}
}

// A list-valued key is refused whatever flags accompany it, so the refusal has
// to name the file rather than a flag. `config set notify.on blocked` used to
// say "run with --global", and running it with --global then said "edit it in
// the file" — the first message sent the user to a flag that could not help.
func TestConfigSet_ListValuedKeysNameTheFileToEdit(t *testing.T) {
	t.Cleanup(func() { configSetGlobal = false })
	for _, key := range []string{"notify.on", "workspace.seed", "workspace.setup"} {
		for _, global := range []bool{false, true} {
			configSetGlobal = global
			err := configSetCmd.RunE(configSetCmd, []string{key, "whatever"})
			if err == nil {
				t.Fatalf("config set %s (--global=%v) succeeded, want a refusal", key, global)
			}
			if !strings.Contains(err.Error(), "edit it in") {
				t.Errorf("config set %s (--global=%v) = %q, want it to name the file to edit", key, global, err)
			}
			if strings.Contains(err.Error(), "--global") {
				t.Errorf("config set %s (--global=%v) = %q, want no mention of a flag that cannot help", key, global, err)
			}
		}
	}
}

// notify.* is stripped from a repository's config on read, so writing it there
// would save, print back, and do nothing.
func TestConfigSet_GlobalOnlyKeyRefusesTheRepoFile(t *testing.T) {
	configSetGlobal = false
	err := configSetCmd.RunE(configSetCmd, []string{"notify.desktop", "false"})
	if err == nil {
		t.Fatal("config set notify.desktop succeeded against the repo config, want a refusal")
	}
	if !strings.Contains(err.Error(), "--global") {
		t.Errorf("error = %q, want it to name the flag that makes the write land somewhere it is read from", err)
	}
}

func TestConfigSet_UnknownKeyIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	configSetGlobal = false
	err := configSetCmd.RunE(configSetCmd, []string{"agent.commnd", "claude"})
	if err == nil {
		t.Fatal("expected an error for a misspelled key")
	}
	if !strings.Contains(err.Error(), "agent.commnd") {
		t.Errorf("error = %q, want it to quote what was typed", err)
	}
}

func TestParseConfigValue_BoolsAndStrings(t *testing.T) {
	parsed, err := parseConfigValue("github.auto_push", "true")
	if err != nil {
		t.Fatalf("parseConfigValue(github.auto_push, true) = %v", err)
	}
	if parsed != true {
		t.Errorf("github.auto_push parsed to %#v, want the TOML boolean — a quoted \"true\" reads back as a string", parsed)
	}

	if _, err := parseConfigValue("notify.desktop", "yes"); err == nil {
		t.Error("parseConfigValue(notify.desktop, yes) succeeded, want a refusal naming the two values TOML accepts")
	} else if !strings.Contains(err.Error(), "notify.desktop") || !strings.Contains(err.Error(), "true or false") {
		t.Errorf("error = %q, want it to name the key and the accepted values", err)
	}

	parsed, err = parseConfigValue("agent.command", "claude")
	if err != nil {
		t.Fatalf("parseConfigValue(agent.command, claude) = %v", err)
	}
	if parsed != "claude" {
		t.Errorf("agent.command parsed to %#v, want the string as typed", parsed)
	}
}

// Completing a key `set` is certain to refuse is the shell recommending a
// mistake: tab used to offer notify.on and workspace.seed, the two keys set
// could never write. `get` still offers everything, because everything can be
// read.
func TestConfigKeyCompletions_SetOffersOnlyWhatItCanWrite(t *testing.T) {
	settable := completionNames(t, configSetCmd.ValidArgsFunction)
	for _, k := range configKeys {
		offered := slices.Contains(settable, k.name)
		if k.settable() && !offered {
			t.Errorf("`config set` does not complete %s, which it can write", k.name)
		}
		if !k.settable() && offered {
			t.Errorf("`config set` completes %s, which it always refuses", k.name)
		}
	}

	readable := completionNames(t, configGetCmd.ValidArgsFunction)
	for _, k := range configKeys {
		if !slices.Contains(readable, k.name) {
			t.Errorf("`config get` does not complete %s", k.name)
		}
	}
}

func completionNames(t *testing.T, complete func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)) []string {
	t.Helper()
	completions, _ := complete(nil, nil, "")
	names := make([]string, 0, len(completions))
	for _, c := range completions {
		name, desc, _ := strings.Cut(c, "\t")
		if desc == "" {
			t.Errorf("completion %q carries no description for the shell to show", name)
		}
		names = append(names, name)
	}
	return names
}

// The help block is generated from the registry, and this pins the reason: a
// key documented in one place and missing from the other is the drift the
// single table exists to prevent.
func TestConfigLongHelp_DocumentsEveryKey(t *testing.T) {
	for _, k := range configKeys {
		if !strings.Contains(ConfigCmd.Long, k.name) {
			t.Errorf("`opentree config --help` never mentions %s", k.name)
		}
	}
	if !strings.Contains(ConfigCmd.Long, "notify.on              Events worth an interruption: blocked, done, stopped — global only, edit the file") {
		t.Errorf("the key block lost its alignment or its restrictions:\n%s", ConfigCmd.Long)
	}
}
