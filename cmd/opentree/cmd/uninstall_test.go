package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// homeWithNothingInIt points every path uninstall knows about at a temp
// directory: $HOME for the tools prefix, the trust file and the completions,
// an empty $XDG_CONFIG_HOME so the global config lands under it too, and an
// empty $ZSH so zshCompletionDir does not find the developer's own oh-my-zsh.
func homeWithNothingInIt(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("ZSH", "")
	return home
}

func writeUnder(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// captureStdout collects what fn prints. The command reports with fmt.Print the
// way every other command in this package does, so the only way to read it back
// is to stand in for the file it writes to.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	collected := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		collected <- b.String()
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-collected
	_ = r.Close()
	return out
}

// runUninstall drives the real cobra command, flags and all. The flags are
// reset afterwards because the command is a package-level var: leaving --yes
// set would silently arm the next test that expected to be asked.
func runUninstall(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	return captureStdout(t, func() {
		UninstallCmd.SetArgs(args)
		UninstallCmd.SetIn(strings.NewReader(stdin))
		defer func() {
			UninstallCmd.SetArgs(nil)
			for _, name := range []string{"yes", "dry-run"} {
				if err := UninstallCmd.Flags().Set(name, "false"); err != nil {
					t.Errorf("resetting --%s: %v", name, err)
				}
			}
		}()
		if err := UninstallCmd.Execute(); err != nil {
			t.Errorf("uninstall %v: %v", args, err)
		}
	})
}

func TestUninstallPlan_ListsOnlyWhatIsActuallyThere(t *testing.T) {
	home := homeWithNothingInIt(t)
	tools := filepath.Join(home, ".opentree", "tools")
	writeUnder(t, filepath.Join(tools, "lib", "node_modules", "adapter", "index.js"), strings.Repeat("x", 4096))
	store := filepath.Join(home, ".opentree", "plugins")
	writeUnder(t, filepath.Join(store, "a-plugin", "plugin.json"), `{}`)
	trust := filepath.Join(home, ".opentree", "trust.json")
	writeUnder(t, trust, `{"repos":{}}`)

	plan, err := uninstallPlan()
	if err != nil {
		t.Fatalf("uninstallPlan: %v", err)
	}

	var got []string
	for _, a := range plan {
		got = append(got, a.path)
	}
	want := []string{tools, store, trust}
	if len(got) != len(want) {
		t.Fatalf("plan = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("plan[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// The adapter tree is the reason this command exists, so its size has to be
	// the size of the tree and not of the directory entry.
	if plan[0].size < 4096 {
		t.Errorf("tools size = %d, want at least the 4096 bytes written into it", plan[0].size)
	}
}

// TestUninstallPlan_FindsEveryCompletionInstallWrites is the guard on the one
// piece of duplication here: uninstall.go spells out the three completion paths
// that install_completion.go builds inline as it writes them. Installing all
// three for real and asking the plan what it sees is the only thing that keeps
// the two lists honest — a path changed on one side and not the other leaves a
// file behind for ever, and nothing else would notice.
func TestUninstallPlan_FindsEveryCompletionInstallWrites(t *testing.T) {
	home := homeWithNothingInIt(t)

	root := &cobra.Command{Use: "opentree"}
	captureStdout(t, func() {
		for _, install := range []func(*cobra.Command) error{
			installZshCompletion, installBashCompletion, installFishCompletion,
		} {
			if err := install(root); err != nil {
				t.Errorf("installing completion: %v", err)
			}
		}
	})

	plan, err := uninstallPlan()
	if err != nil {
		t.Fatalf("uninstallPlan: %v", err)
	}
	planned := map[string]bool{}
	for _, a := range plan {
		planned[a.path] = true
	}

	for _, want := range []string{
		filepath.Join(home, ".zsh", "completions", "_opentree"),
		filepath.Join(home, ".bash_completion.d", "opentree"),
		filepath.Join(home, ".config", "fish", "completions", "opentree.fish"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("install-completion did not write %s: %v", want, err)
		}
		if !planned[want] {
			t.Errorf("uninstall would leave %s behind", want)
		}
	}
}

// TestUninstall_RemovesItsOwnFilesAndLeavesTheRepositoryAlone runs from inside
// a repository holding a worktree, because that is the failure that would
// matter: <repo>/.opentree is unfinished work, and an uninstaller that swept it
// up would take the user's branches with it.
func TestUninstall_RemovesItsOwnFilesAndLeavesTheRepositoryAlone(t *testing.T) {
	home := homeWithNothingInIt(t)
	tools := filepath.Join(home, ".opentree", "tools")
	writeUnder(t, filepath.Join(tools, "bin", "claude-agent-acp"), "#!/bin/sh\n")
	writeUnder(t, filepath.Join(home, ".opentree", "trust.json"), `{"repos":{}}`)
	writeUnder(t, filepath.Join(home, ".config", "opentree", "opentree.toml"), "[agent]\n")

	repo := t.TempDir()
	worktreeFile := filepath.Join(repo, ".opentree", "feat-x", "main.go")
	writeUnder(t, worktreeFile, "package main\n")
	t.Chdir(repo)

	out := runUninstall(t, "", "--yes")

	for _, gone := range []string{
		tools,
		filepath.Join(home, ".opentree", "trust.json"),
		filepath.Join(home, ".config", "opentree", "opentree.toml"),
		// Emptied of everything opentree put there, ~/.opentree goes too.
		filepath.Join(home, ".opentree"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall (stat err = %v)", gone, err)
		}
	}
	if _, err := os.Stat(worktreeFile); err != nil {
		t.Errorf("uninstall touched the repository's worktree: %v", err)
	}
	if !strings.Contains(out, "opentree delete") {
		t.Errorf("output never says how to remove worktrees:\n%s", out)
	}
	if !strings.Contains(out, "Remove the binary itself") {
		t.Errorf("output never says how to remove the binary:\n%s", out)
	}
}

func TestUninstall_DryRunRemovesNothing(t *testing.T) {
	home := homeWithNothingInIt(t)
	trust := filepath.Join(home, ".opentree", "trust.json")
	writeUnder(t, trust, `{"repos":{}}`)

	out := runUninstall(t, "", "--dry-run")

	if _, err := os.Stat(trust); err != nil {
		t.Errorf("--dry-run removed %s: %v", trust, err)
	}
	if !strings.Contains(out, trust) {
		t.Errorf("--dry-run never named %s:\n%s", trust, out)
	}
	if !strings.Contains(out, "Dry run") {
		t.Errorf("--dry-run did not say it was one:\n%s", out)
	}
}

// TestUninstall_UnansweredPromptRemovesNothing covers the shape a cron job
// takes: no --yes, and nothing on stdin. Reading end-of-input as consent would
// delete the adapters of anyone who ran this from a script by mistake.
func TestUninstall_UnansweredPromptRemovesNothing(t *testing.T) {
	home := homeWithNothingInIt(t)
	trust := filepath.Join(home, ".opentree", "trust.json")
	writeUnder(t, trust, `{"repos":{}}`)

	out := runUninstall(t, "")

	if _, err := os.Stat(trust); err != nil {
		t.Errorf("an unanswered prompt removed %s: %v", trust, err)
	}
	if !strings.Contains(out, "Cancelled") {
		t.Errorf("output does not say it stopped:\n%s", out)
	}
}

func TestConfirm_OnlyYesMeansYes(t *testing.T) {
	tests := []struct {
		typed string
		want  bool
	}{
		{"y\n", true},
		{"Y\n", true},
		{"yes\n", true},
		{" yes \n", true},
		{"\n", false},
		{"n\n", false},
		{"no\n", false},
		{"maybe\n", false},
		{"", false}, // end of input, nobody there to ask
	}
	for _, tt := range tests {
		var got bool
		captureStdout(t, func() {
			got = confirm(strings.NewReader(tt.typed), "Remove all of it?")
		})
		if got != tt.want {
			t.Errorf("confirm(%q) = %v, want %v", tt.typed, got, tt.want)
		}
	}
}

// TestPlanReport_NamesEveryPathAndItsSize covers the four magnitudes a size can
// land in, because the report is the whole basis on which someone answers the
// question: an adapter tree reported as "333447168 B" is the same as not
// reporting it.
func TestPlanReport_NamesEveryPathAndItsSize(t *testing.T) {
	report := planReport([]artefact{
		{label: "agent adapters", path: "/home/u/.opentree/tools", size: 2 << 30},
		{label: "second adapter", path: "/home/u/.opentree/tools/other", size: 318 << 20},
		{label: "zsh completion", path: "/home/u/.zsh/completions/_opentree", size: 12 << 10},
		{label: "global config", path: "/home/u/.config/opentree/opentree.toml", size: 286},
	})

	for _, want := range []string{
		"/home/u/.opentree/tools", "2.0 GB",
		"/home/u/.opentree/tools/other", "318.0 MB",
		"/home/u/.zsh/completions/_opentree", "12.0 KB",
		"/home/u/.config/opentree/opentree.toml", "286 B",
		"total", "2.3 GB",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not mention %q:\n%s", want, report)
		}
	}
}

// The binary is the one thing uninstall cannot remove: brew, npm and go install
// each record having put it there, and deleting it from underneath them leaves
// them describing a file that has gone. So it names the command instead, and
// naming the wrong one is the same failure with extra steps.
func TestRemovalFor_NamesTheCommandThatOwnsTheBinary(t *testing.T) {
	const home = "/Users/someone"
	for _, tc := range []struct {
		name string
		exe  string
		want string
	}{
		{
			// opentree ships as a Homebrew cask. `brew uninstall` without
			// --cask leaves the cask's own bookkeeping behind.
			"a cask",
			"/opt/homebrew/Caskroom/opentree/1.0.2/opentree",
			"brew uninstall --cask opentree",
		},
		{
			"a formula, for anyone who installed one before it was a cask",
			"/opt/homebrew/Cellar/opentree/1.0.1/bin/opentree",
			"brew uninstall opentree",
		},
		{
			"the npm launcher, which execs the platform binary",
			home + "/.nvm/versions/node/v24.0.0/lib/node_modules/@axelgar/opentree/bin/opentree",
			"npm uninstall -g @axelgar/opentree",
		},
		{
			"go install, which records nothing, so the path is the whole remedy",
			home + "/go/bin/opentree",
			"rm " + home + "/go/bin/opentree",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := removalFor(tc.exe, home); !strings.Contains(got, tc.want) {
				t.Errorf("removalFor(%q) = %q, want it to name %q", tc.exe, got, tc.want)
			}
		})
	}
}

// A binary outside the home directory needs a word about sudo, and one inside
// it must not get one — the note is only useful where it is true.
func TestRemovalFor_MentionsSudoOnlyOutsideHome(t *testing.T) {
	const home = "/Users/someone"
	if got := removalFor("/usr/local/bin/opentree", home); !strings.Contains(got, "sudo") {
		t.Errorf("removalFor() = %q, want a note about sudo for a path outside home", got)
	}
	if got := removalFor(home+"/go/bin/opentree", home); strings.Contains(got, "sudo") {
		t.Errorf("removalFor() = %q, want no sudo note for a path inside home", got)
	}
}
