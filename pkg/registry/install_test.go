package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageSpec_AlwaysPinned(t *testing.T) {
	pinned := PackageDist{Package: "@augmentcode/auggie@0.36.0"}
	if got := packageSpec(pinned, "9.9.9"); got != "@augmentcode/auggie@0.36.0" {
		t.Errorf("packageSpec(pinned) = %q, want the registry's own pin kept", got)
	}
	// An entry that arrives unpinned is pinned to its own version — "whatever
	// latest resolves to this morning" is not what the user was shown.
	bare := PackageDist{Package: "some-agent"}
	if got := packageSpec(bare, "1.2.3"); got != "some-agent@1.2.3" {
		t.Errorf("packageSpec(bare) = %q, want the entry version appended", got)
	}
}

func TestSplitPackageSpec(t *testing.T) {
	for spec, want := range map[string][2]string{
		"@scope/name@1.2.3": {"@scope/name", "1.2.3"},
		"name@1.2.3":        {"name", "1.2.3"},
		"name":              {"name", ""},
		"@scope/name":       {"@scope/name", ""},
	} {
		name, version := splitPackageSpec(spec)
		if name != want[0] || version != want[1] {
			t.Errorf("splitPackageSpec(%q) = %q, %q; want %q, %q", spec, name, version, want[0], want[1])
		}
	}
}

// binPrefix fabricates the layout npm leaves behind: the manifest with the
// given bin field, and shims for the named commands.
func binPrefix(t *testing.T, pkgName, binField string, shims ...string) string {
	t.Helper()
	prefix := t.TempDir()
	pkgDir := filepath.Join(prefix, "lib", "node_modules", filepath.FromSlash(pkgName))
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":"1.0.0","bin":` + binField + `}`
	if err := os.WriteFile(filepath.Join(pkgDir, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(prefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, shim := range shims {
		if err := os.WriteFile(filepath.Join(prefix, "bin", shim), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return prefix
}

func TestResolveBin_TheThreeShapesNpmAllows(t *testing.T) {
	// A string bin means one command named after the package, scope stripped.
	prefix := binPrefix(t, "@augmentcode/auggie", `"./dist/cli.js"`, "auggie")
	if got, err := resolveBin(prefix, "@augmentcode/auggie"); err != nil || got != filepath.Join(prefix, "bin", "auggie") {
		t.Errorf("string bin: %q, %v", got, err)
	}

	// A one-entry map is that entry, whatever it is called.
	prefix = binPrefix(t, "droid", `{"factory-droid":"./cli.js"}`, "factory-droid")
	if got, err := resolveBin(prefix, "droid"); err != nil || got != filepath.Join(prefix, "bin", "factory-droid") {
		t.Errorf("map bin: %q, %v", got, err)
	}

	// A multi-entry map takes the one named after the package…
	prefix = binPrefix(t, "kilo", `{"kilo":"./cli.js","kilo-helper":"./helper.js"}`, "kilo", "kilo-helper")
	if got, err := resolveBin(prefix, "kilo"); err != nil || got != filepath.Join(prefix, "bin", "kilo") {
		t.Errorf("multi-map bin: %q, %v", got, err)
	}

	// …and refuses to guess when none matches: picking wrong here means
	// executing the wrong program on every chat open.
	prefix = binPrefix(t, "vague", `{"a":"./a.js","b":"./b.js"}`, "a", "b")
	if _, err := resolveBin(prefix, "vague"); err == nil || !strings.Contains(err.Error(), "cannot pick") {
		t.Errorf("ambiguous bin: err = %v, want a refusal naming the choices", err)
	}

	// npm succeeded but the shim is not there — the --ignore-scripts case.
	prefix = binPrefix(t, "ghost", `"./cli.js"`)
	if _, err := resolveBin(prefix, "ghost"); err == nil {
		t.Error("a missing shim resolved")
	}
}

func TestNewPlan_ChoosesAndRefuses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	plan, err := NewPlan(fixtureEntry(t, "auggie"), DefaultIndexURL)
	if err != nil {
		t.Fatalf("NewPlan(auggie): %v", err)
	}
	if plan.Kind != "npm" {
		t.Errorf("Kind = %q, want npm", plan.Kind)
	}
	argv := strings.Join(plan.NpmArgv, " ")
	for _, part := range []string{"--ignore-scripts", "--prefix", "@augmentcode/auggie@0.36.0", filepath.Join("agents", "auggie", "npm")} {
		if !strings.Contains(argv, part) {
			t.Errorf("argv %q is missing %q", argv, part)
		}
	}
	if desc := plan.Describe(); !strings.Contains(desc, argv) || !strings.Contains(desc, "Auggie") {
		t.Errorf("Describe() = %q, want the full command and the name in it", desc)
	}

	if _, err := NewPlan(fixtureEntry(t, "fast-agent"), DefaultIndexURL); err == nil || !strings.Contains(err.Error(), "uvx") {
		t.Errorf("uvx-only entry: err = %v, want the unsupported-yet refusal", err)
	}
}

// fakeNpm puts a fake npm first on PATH — the stubGH pattern — that
// fabricates the prefix layout a real install leaves: manifest, bin shim.
func fakeNpm(t *testing.T, body string) {
	t.Helper()
	binDir := t.TempDir()
	script := `#!/bin/sh
prefix=""; spec=""
while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) prefix="$2"; shift 2;;
    install|-g|--ignore-scripts) shift;;
    *) spec="$1"; shift;;
  esac
done
name="${spec%@*}"; base="${name##*/}"
` + body + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "npm"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const fakeNpmSuccess = `mkdir -p "$prefix/lib/node_modules/$name" "$prefix/bin"
printf '{"version":"1.0.0","bin":"./cli.js"}' > "$prefix/lib/node_modules/$name/package.json"
printf '#!/bin/sh\n' > "$prefix/bin/$base"
chmod +x "$prefix/bin/$base"
exit 0`

func TestPlanRun_NpmInstallCommitsARecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeNpm(t, fakeNpmSuccess)
	entry := fixtureEntry(t, "auggie")

	plan, err := NewPlan(entry, DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := plan.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := filepath.Join(plan.Dir, "npm", "bin", "auggie"); rec.Command != want {
		t.Errorf("Command = %q, want %q", rec.Command, want)
	}
	if rec.Env["AUGMENT_DISABLE_AUTO_UPDATE"] != "1" {
		t.Error("the entry's env did not reach the record")
	}

	records, problems := Installed()
	if len(records) != 1 || len(problems) != 0 {
		t.Fatalf("store after install = %d records, %v", len(records), problems)
	}
	if records[0].Entry.Version != "0.36.0" || records[0].IndexURL != DefaultIndexURL {
		t.Errorf("record = %+v, want the version and index recorded", records[0])
	}

	// The second install is refused; update is the verb for refreshing.
	if _, err := plan.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Errorf("second Run: %v, want the already-installed refusal", err)
	}
}

func TestPlanRun_ReportsToTheWritersItIsGiven(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeNpm(t, `echo "added 1 package"; echo "npm warn deprecated" >&2; `+fakeNpmSuccess)
	plan, err := NewPlan(fixtureEntry(t, "auggie"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	var out, errs strings.Builder
	plan.Stdout, plan.Stderr = &out, &errs
	if _, err := plan.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "added 1 package") {
		t.Errorf("stdout = %q, want npm's output captured", out.String())
	}
	if !strings.Contains(errs.String(), "deprecated") {
		t.Errorf("stderr = %q, want npm's warnings captured", errs.String())
	}
}

func TestPlanRun_AFailedInstallLeavesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	fakeNpm(t, "exit 1")
	plan, err := NewPlan(fixtureEntry(t, "auggie"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Run(context.Background()); err == nil {
		t.Fatal("a failing npm reported success")
	}
	if _, err := os.Lstat(plan.Dir); !os.IsNotExist(err) {
		t.Error("a failed install left its directory behind")
	}
	if records, problems := Installed(); len(records) != 0 || len(problems) != 0 {
		t.Errorf("store after failure = %d records, %v; want empty", len(records), problems)
	}
}

func TestPlanRun_AnInstallWithNoShimNamesTheScriptsPolicy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// npm "succeeds" but the package needed its install hooks: manifest there,
	// shim not. The error must point at the policy, not at npm.
	fakeNpm(t, `mkdir -p "$prefix/lib/node_modules/$name" "$prefix/bin"
printf '{"version":"1.0.0","bin":"./cli.js"}' > "$prefix/lib/node_modules/$name/package.json"
exit 0`)
	plan, err := NewPlan(fixtureEntry(t, "auggie"), DefaultIndexURL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plan.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "install hooks disabled") {
		t.Errorf("err = %v, want the --ignore-scripts explanation", err)
	}
	if _, statErr := os.Lstat(plan.Dir); !os.IsNotExist(statErr) {
		t.Error("the half-install was left behind")
	}
}

func TestRemove_TakesAnIdNeverAPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	installFake(t, fixtureEntry(t, "devin"), nil, nil)

	for _, bad := range []string{"../devin", "devin/..", ".", "", "a/b"} {
		if err := Remove(bad); err == nil {
			t.Errorf("Remove(%q) accepted", bad)
		}
	}
	if err := Remove("not-installed"); err == nil {
		t.Error("removing an agent that is not there should say so")
	}
	if err := Remove("devin"); err != nil {
		t.Fatalf("Remove(devin): %v", err)
	}
	if records, _ := Installed(); len(records) != 0 {
		t.Error("the install survived Remove")
	}
}

// A store entry whose record is broken must still be removable by name —
// that is half the point of the command.
func TestRemove_ClearsABrokenInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(agentsDir(), "broken-agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Remove("broken-agent"); err != nil {
		t.Fatalf("Remove(broken): %v", err)
	}
	if _, problems := Installed(); len(problems) != 0 {
		t.Errorf("problems after remove = %v, want none", problems)
	}
}

func TestCachedEntries_AnswersFromDiskOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := CachedEntries(); got != nil {
		t.Errorf("no cache should mean no completions, got %d", len(got))
	}
	idx := Index{Version: "1.0.0", Agents: fixtureEntries(t)}
	writeCache(idx, DefaultIndexURL)
	if got := CachedEntries(); len(got) != 5 {
		t.Errorf("CachedEntries() = %d, want the cached 5", len(got))
	}
}

// Guard against the fixture and the marshalling drifting: a record written
// by writeRecord must load back identically through Installed.
func TestWriteRecord_RoundTrips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := filepath.Join(agentsDir(), "auggie")
	bin := filepath.Join(dir, "npm", "bin", "auggie")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := Record{Entry: fixtureEntry(t, "auggie"), IndexURL: DefaultIndexURL, Command: bin}
	if err := writeRecord(dir, in); err != nil {
		t.Fatal(err)
	}
	records, problems := Installed()
	if len(records) != 1 || len(problems) != 0 {
		t.Fatalf("round trip = %d records, %v", len(records), problems)
	}
	want, _ := json.Marshal(in.Entry)
	got, _ := json.Marshal(records[0].Entry)
	if !bytes.Equal(want, got) {
		t.Errorf("entry changed in the round trip:\n%s\n%s", want, got)
	}
}
