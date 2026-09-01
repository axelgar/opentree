package registry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/axelgar/opentree/pkg/config"
)

// The fixture is the published index's shape, verbatim: five real entries
// copied from the registry repository at commit 979f6179 — an npx entry with
// env (auggie), a binary entry for every platform (devin), a binary entry
// missing linux-aarch64 (vtcode), a uvx-only entry (fast-agent), and one
// whose id collides with a predefined agent (gemini).
func fixtureEntries(t *testing.T) []Entry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct {
		Agents []Entry `json:"agents"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(index.Agents) != 5 {
		t.Fatalf("fixture holds %d entries, want 5", len(index.Agents))
	}
	return index.Agents
}

func fixtureEntry(t *testing.T, id string) Entry {
	t.Helper()
	for _, e := range fixtureEntries(t) {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no %q in the fixture", id)
	return Entry{}
}

func TestEntry_ParsesTheRealIndexShape(t *testing.T) {
	entries := fixtureEntries(t)
	for _, e := range entries {
		if err := e.Validate(); err != nil {
			t.Errorf("real entry %s fails validation: %v", e.ID, err)
		}
	}

	auggie := fixtureEntry(t, "auggie")
	if auggie.Distribution.Npx == nil || auggie.Distribution.Npx.Package != "@augmentcode/auggie@0.36.0" {
		t.Errorf("auggie npx = %+v, want the pinned package", auggie.Distribution.Npx)
	}
	if auggie.Distribution.Npx.Env["AUGMENT_DISABLE_AUTO_UPDATE"] != "1" {
		t.Error("auggie's env did not survive decoding — the agent would self-update under opentree")
	}

	devin := fixtureEntry(t, "devin")
	target, ok := devin.Distribution.Binary["linux-x86_64"]
	if !ok {
		t.Fatal("devin has no linux-x86_64 target in the fixture")
	}
	if target.Cmd != "./bin/devin" || len(target.Args) != 1 || target.Args[0] != "acp" {
		t.Errorf("devin target = %+v, want ./bin/devin acp", target)
	}

	if _, ok := fixtureEntry(t, "vtcode").Distribution.Binary["linux-aarch64"]; ok {
		t.Error("vtcode grew a linux-aarch64 target — the missing-platform tests need a new specimen")
	}
}

func TestValidateID(t *testing.T) {
	for _, id := range []string{"devin", "fast-agent", "a", "glm-acp-agent"} {
		if err := ValidateID(id); err != nil {
			t.Errorf("ValidateID(%q) = %v, want nil", id, err)
		}
	}
	// The id becomes a directory name and a command name; everything here
	// would be a path escape, an option injection, or an invisible entry.
	for _, id := range []string{"", "../x", "a/b", ".hidden", "-flag", "UPPER", "café", "9start"} {
		if err := ValidateID(id); err == nil {
			t.Errorf("ValidateID(%q) accepted", id)
		}
	}
}

func TestEntry_Validate_Refusals(t *testing.T) {
	good := fixtureEntry(t, "auggie")

	e := good
	e.Distribution = Distribution{}
	if err := e.Validate(); err == nil {
		t.Error("an entry with no distribution validated")
	}

	e = good
	e.Version = ""
	if err := e.Validate(); err == nil {
		t.Error("an entry with no version validated")
	}

	e = good
	e.Distribution.Npx = &PackageDist{}
	if err := e.Validate(); err == nil {
		t.Error("an npx distribution naming no package validated")
	}

	e = fixtureEntry(t, "devin")
	target := e.Distribution.Binary["linux-x86_64"]
	target.Cmd = ""
	e.Distribution.Binary["linux-x86_64"] = target
	if err := e.Validate(); err == nil {
		t.Error("a binary target with no command validated")
	}
}

// installFake fabricates one installed agent the way `agents add` will: the
// tree, a fake binary inside it, and the record written last. Returns the
// binary's path.
func installFake(t *testing.T, entry Entry, args []string, env map[string]string) string {
	t.Helper()
	dir := filepath.Join(agentsDir(), entry.ID)
	bin := filepath.Join(dir, "tree", "bin", entry.ID)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Record{
		Entry:       entry,
		IndexURL:    DefaultIndexURL,
		Command:     bin,
		Args:        args,
		Env:         env,
		InstalledAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestInstalled_EmptyStoreAndMissingHome(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	records, problems := Installed()
	if len(records) != 0 || len(problems) != 0 {
		t.Errorf("a store that does not exist yet = %d records, %d problems; want none", len(records), len(problems))
	}
}

func TestInstalled_ReadsRecordsAndReportsTheBroken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	installFake(t, fixtureEntry(t, "devin"), []string{"acp"}, nil)

	// A staging directory is an install in flight, not an agent.
	if err := os.MkdirAll(filepath.Join(agentsDir(), ".install-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A directory without a readable record is a survivable problem: it must
	// be named — an invisible broken install is one nobody can remove — and
	// must not hide the healthy install beside it.
	broken := filepath.Join(agentsDir(), "broken-agent")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, recordFile), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	records, problems := Installed()
	if len(records) != 1 || records[0].Entry.ID != "devin" {
		t.Fatalf("records = %+v, want just devin", records)
	}
	if records[0].Dir != filepath.Join(agentsDir(), "devin") {
		t.Errorf("Dir = %q, want the store directory", records[0].Dir)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one line for broken-agent", problems)
	}
}

func TestInstalled_RefusesARecordUnderTheWrongName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	installFake(t, fixtureEntry(t, "devin"), nil, nil)
	// A record that names a different id than its directory is either a copy
	// gone wrong or something worse; loading it would let the file under one
	// name run as another.
	if err := os.Rename(filepath.Join(agentsDir(), "devin"), filepath.Join(agentsDir(), "auggie")); err != nil {
		t.Fatal(err)
	}
	records, problems := Installed()
	if len(records) != 0 || len(problems) != 1 {
		t.Errorf("renamed install = %+v / %v, want no record and one problem", records, problems)
	}
}

// snapshotAgents guards the package-level registry: these tests append to it
// through LoadInstalled, and -shuffle runs them in any order against every
// other test in the package. No test in this file may call t.Parallel.
func snapshotAgents(t *testing.T) {
	t.Helper()
	prev := config.PredefinedAgents
	config.PredefinedAgents = append([]config.PredefinedAgent{}, prev...)
	t.Cleanup(func() { config.PredefinedAgents = prev })
}

func TestLoadInstalled_MakesTheAgentFirstClass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	snapshotAgents(t)
	bin := installFake(t, fixtureEntry(t, "devin"), []string{"acp"}, map[string]string{"DEVIN_X": "1"})

	if problems := LoadInstalled(); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}

	agent := config.FindAgent("devin")
	if agent == nil {
		t.Fatal("FindAgent(devin) = nil after LoadInstalled")
	}
	if byName := config.FindAgent("Devin"); byName != agent {
		t.Error("the display name resolves to a different agent than the id")
	}
	if !agent.IsInstalled() {
		t.Error("a freshly loaded install reports not installed")
	}
	if got := agent.ResolveACPCommand(); got != bin {
		t.Errorf("ResolveACPCommand() = %q, want %q", got, bin)
	}
	if args := agent.ACPArgs("/tmp/wt"); len(args) != 1 || args[0] != "acp" {
		t.Errorf("ACPArgs = %v, want the recorded args", args)
	}
	if env := agent.ACPEnv(); len(env) == 0 || env[len(env)-1] != "DEVIN_X=1" {
		t.Error("the recorded env does not reach the launch")
	}
	if agent.Origin == nil || agent.Origin.ID != "devin" || agent.Origin.Version != "3000.6.7" {
		t.Errorf("Origin = %+v, want the registry provenance", agent.Origin)
	}

	// The invariants every predefined agent already satisfies — an agent that
	// broke them would be offered in the picker and then render wrong.
	if agent.ACPCommand() == "" {
		t.Error("no ACP command")
	}
	if agent.Brand.Mark == "" || agent.Brand.Colour == "" || len(agent.Brand.Logo) == 0 {
		t.Errorf("Brand = %+v, want mark, colour and logo", agent.Brand)
	}
	if agent.Skills.UserDirs != nil || agent.Skills.RepoDirs != nil {
		t.Error("a registry agent guessed at skills directories")
	}
}

func TestLoadInstalled_BuiltInsWinCollisions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	snapshotAgents(t)
	before := len(config.PredefinedAgents)
	// The registry lists gemini too; installing it and then loading must not
	// shadow or duplicate the predefined Gemini CLI.
	installFake(t, fixtureEntry(t, "gemini"), []string{"--acp"}, nil)

	problems := LoadInstalled()
	if len(config.PredefinedAgents) != before {
		t.Errorf("registry grew to %d agents, want the built-in to win", len(config.PredefinedAgents))
	}
	if len(problems) != 1 {
		t.Errorf("problems = %v, want the collision named", problems)
	}
	if agent := config.FindAgent("gemini"); agent == nil || agent.Origin != nil {
		t.Error("FindAgent(gemini) should still be the built-in")
	}
}

func TestLoadInstalled_ADeadInstallIsNotInstalled(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	snapshotAgents(t)
	bin := installFake(t, fixtureEntry(t, "devin"), nil, nil)
	if err := os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	LoadInstalled()
	agent := config.FindAgent("devin")
	if agent == nil {
		t.Fatal("a loaded record should still resolve — the refusal belongs to IsInstalled")
	}
	if agent.IsInstalled() {
		t.Error("an install whose binary is gone reports installed")
	}
}

func TestLoadInstalled_ReplacesRatherThanAppends(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	snapshotAgents(t)
	builtins := len(config.PredefinedAgents)
	installFake(t, fixtureEntry(t, "devin"), nil, nil)

	// The dashboard reloads after every install and removal, so a second
	// load must describe the store, not add to the first load's answer.
	LoadInstalled()
	LoadInstalled()
	if got := len(config.PredefinedAgents); got != builtins+1 {
		t.Fatalf("two loads left %d agents, want %d (built-ins plus one)", got, builtins+1)
	}
	for _, a := range config.PredefinedAgents[:builtins] {
		if a.Origin != nil {
			t.Errorf("%s: a built-in lost its place to a registry agent", a.Name)
		}
	}

	if err := Remove("devin"); err != nil {
		t.Fatal(err)
	}
	LoadInstalled()
	if got := len(config.PredefinedAgents); got != builtins {
		t.Errorf("after removal the list holds %d agents, want %d", got, builtins)
	}
	if config.FindAgent("devin") != nil {
		t.Error("a removed install still resolves after the reload")
	}
}

func TestBrandFor_DeterministicAndPlain(t *testing.T) {
	a, b := brandFor("devin"), brandFor("devin")
	if a.Colour != b.Colour {
		t.Error("the same id got two colours")
	}
	if a.Mark != registryMark || len(a.Logo) == 0 {
		t.Errorf("brand = %+v, want the shared mark and a logo", a)
	}
	if brandFor("auggie").Colour == "" {
		t.Error("no colour picked")
	}
}
