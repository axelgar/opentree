package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/config"
)

// worktreeNotice is the sentence this command exists to be able to say out
// loud. <repo>/.opentree is not opentree's data — it is the user's unfinished
// work, sitting in checked-out worktrees with uncommitted changes in them, and
// an uninstaller that swept it up would be the most expensive bug in the
// project. Removing a worktree also means removing its branch and its tmux
// session, which is `opentree delete`'s whole job; there is nothing to be
// gained by doing it badly here.
const worktreeNotice = `No repository is touched. The worktrees under <repo>/.opentree are your own
work in progress — remove those yourself, one at a time, with:
  opentree delete <branch>`

var UninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove what opentree installed outside your repositories",
	Long: `Remove the files opentree wrote into your home directory.

There are four of them: the agent adapters opentree fetches into its own npm
prefix at ~/.opentree/tools (a few hundred megabytes each, and by far the
largest thing it leaves behind), the record of which setup and run commands
this machine has approved, the shell completion script, and the global config
file. Everything present is listed with its size before anything is removed.

Three things are deliberately left alone. Repositories: the worktrees under
<repo>/.opentree hold work in progress, and only 'opentree delete <branch>'
takes one of those away. The agents themselves, and any skills opentree
installed into their directories: those live in your agents' own configuration
and outlive opentree. And the opentree binary, which belongs to whichever of
brew, npm or go install put it there — the command to remove it is printed at
the end.

  opentree uninstall --dry-run   list what would go, and stop
  opentree uninstall --yes       do not ask (for scripts)`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		assumeYes, _ := cmd.Flags().GetBool("yes")

		plan, err := uninstallPlan()
		if err != nil {
			return err
		}
		if len(plan) == 0 {
			fmt.Println("opentree has nothing outside your repositories — nothing to remove.")
			fmt.Printf("\n%s\n\n%s\n", worktreeNotice, binaryRemoval())
			return nil
		}

		fmt.Print(planReport(plan))
		fmt.Printf("\n%s\n\n", worktreeNotice)

		if dryRun {
			fmt.Printf("Dry run — nothing was removed.\n\n%s\n", binaryRemoval())
			return nil
		}
		if !assumeYes && !confirm(cmd.InOrStdin(), "Remove all of it?") {
			fmt.Println("Cancelled — nothing was removed. Pass --yes to answer this from a script.")
			return nil
		}
		if err := removeArtefacts(plan); err != nil {
			return err
		}
		pruneOpentreeHome()
		fmt.Printf("\n%s\n", binaryRemoval())
		return nil
	},
}

// artefact is one thing opentree wrote outside the repositories it manages,
// measured so the confirmation can say what removing it buys.
type artefact struct {
	label string
	path  string
	size  int64
}

// uninstallPlan is everything opentree has actually left on this machine, in
// the order worth reading: the adapters first, because they are three orders of
// magnitude larger than the rest put together. Paths that are not there are
// dropped rather than listed as absent — four lines of "not found" tell the
// user nothing they can act on.
func uninstallPlan() ([]artefact, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot find your home directory: %w", err)
	}

	// The three completion paths are spelled out again here rather than shared
	// with install-completion, which builds each one inline as it writes the
	// file. TestUninstallPlan_FindsEveryCompletionInstallWrites installs all
	// three for real and asserts this plan finds them, so the duplication
	// cannot drift apart quietly.
	zshDir, _ := zshCompletionDir()

	candidates := []artefact{
		{label: "agent adapters", path: config.ToolsDir()},
		{label: "approved setup and run commands", path: bootstrap.TrustPath()},
		{label: "global config", path: config.GlobalConfigPath()},
		{label: "zsh completion", path: filepath.Join(zshDir, "_opentree")},
		{label: "bash completion", path: filepath.Join(home, ".bash_completion.d", "opentree")},
		{label: "fish completion", path: filepath.Join(home, ".config", "fish", "completions", "opentree.fish")},
	}

	var plan []artefact
	for _, a := range candidates {
		// A relative path means a home directory that could not be resolved:
		// joining onto an empty string yields something under the working
		// directory, which is the one place an uninstaller must never delete
		// from.
		if !filepath.IsAbs(a.path) {
			continue
		}
		size, err := measure(a.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", a.path, err)
		}
		a.size = size
		plan = append(plan, a)
	}
	return plan, nil
}

// measure is what the artefact occupies on disk. A directory is walked, which
// for ~/.opentree/tools means stat-ing a node_modules tree of some tens of
// thousands of files — a moment at worst, and worth it, because "303 MB" is the
// number that explains why anyone is running this command.
func measure(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return info.Size(), nil
	}
	var total int64
	// Anything that vanishes or refuses to be read mid-walk counts as zero, and
	// the walk carries on: this is a number for a confirmation prompt, not an
	// audit, and refusing to report a size because one file in an npm tree is
	// unreadable would block the removal over nothing.
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr == nil && d != nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total, nil
}

// planReport is the list shown before anything is touched: what it is, how big,
// and where — the path last and in full, because that is the part a suspicious
// user wants to check against their own shell before answering.
func planReport(plan []artefact) string {
	width := 0
	var total int64
	for _, a := range plan {
		if len(a.label) > width {
			width = len(a.label)
		}
		total += a.size
	}

	var b strings.Builder
	b.WriteString("opentree will remove:\n\n")
	for _, a := range plan {
		fmt.Fprintf(&b, "  %-*s  %9s  %s\n", width, a.label, humanSize(a.size), a.path)
	}
	if len(plan) > 1 {
		fmt.Fprintf(&b, "\n  %-*s  %9s\n", width, "total", humanSize(total))
	}
	return b.String()
}

// humanSize is the size a person would say out loud, in the powers of 1024 that
// npm and the machine's own disk tools use for the same directory.
func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// confirm asks and reads one line, the way `opentree delete` does. End of input
// is a no: a cron job that pipes nothing into an uninstaller must come out the
// same as one that answered no — not hang, and above all not be read as a yes.
// --yes is how a script says yes on purpose.
func confirm(in io.Reader, question string) bool {
	fmt.Printf("%s [y/N]: ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(line))
	return answer == "y" || answer == "yes"
}

// removeArtefacts removes each entry and says so. One failure does not stop the
// rest: a completion script the user has since made root-owned should not leave
// three hundred megabytes of adapters sitting on the disk. Each failure is
// printed where it happened and only the count comes back, so the exit code
// reports the trouble without the shell repeating every message.
func removeArtefacts(plan []artefact) error {
	failed := 0
	for _, a := range plan {
		if err := os.RemoveAll(a.path); err != nil {
			fmt.Printf("✗ %s: %v\n", a.path, err)
			failed++
			continue
		}
		fmt.Printf("✓ Removed %s\n", a.path)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d could not be removed — run this again once you can write those paths", failed, len(plan))
	}
	return nil
}

// pruneOpentreeHome takes away ~/.opentree once the things inside it are gone.
// os.Remove rather than os.RemoveAll, and the error is dropped: os.Remove
// refuses a directory that still holds something, so anything a newer opentree
// keeps there survives an older copy of this command that had never heard of
// it. Leaving one empty directory behind is a far cheaper mistake.
func pruneOpentreeHome() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".opentree")
	if err := os.Remove(dir); err == nil {
		fmt.Printf("✓ Removed %s\n", dir)
	}
}

// binaryRemoval names the command that takes the binary away, which this one
// cannot do for itself: brew, npm and go install each own that file and record
// it in a manifest of their own, so deleting it from underneath them leaves
// them describing something that is no longer there. The install method is read
// off the path the running process was started from.
func binaryRemoval() string {
	exe, err := os.Executable()
	if err != nil {
		return "Remove the opentree binary itself the way you installed it — brew, npm or go install."
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, _ := os.UserHomeDir()
	return removalFor(exe, home)
}

// removalFor is the classification on its own, so the four outcomes can be
// tested without a binary installed four ways.
func removalFor(exe, home string) string {
	sep := string(filepath.Separator)
	switch {
	// Homebrew keeps the real file out of PATH and links it in. A cask stages
	// it under Caskroom and a formula under Cellar. Both are checked, and they
	// give different answers: `brew uninstall` without --cask leaves a cask's
	// own bookkeeping behind. opentree ships as a cask; the formula arm is for
	// anyone who installed one before it did.
	case strings.Contains(exe, sep+"Caskroom"+sep):
		return "Remove the binary itself with:\n  brew uninstall --cask opentree"
	case strings.Contains(exe, sep+"Cellar"+sep):
		return "Remove the binary itself with:\n  brew uninstall opentree"
	// The npm package is a launcher that execs the platform binary out of
	// node_modules, so that is where os.Executable lands.
	case strings.Contains(exe, sep+"node_modules"+sep):
		return "Remove the binary itself with:\n  npm uninstall -g @axelgar/opentree"
	}
	// go install, make install and a hand copy all leave a plain file and none
	// of them records having done so. Naming the path is the whole remedy.
	hint := fmt.Sprintf("Remove the binary itself with:\n  rm %s", exe)
	if home == "" || !strings.HasPrefix(exe, home+sep) {
		hint += "\n(it sits outside your home directory, so that may need sudo)"
	}
	return hint
}

func init() {
	UninstallCmd.Flags().BoolP("yes", "y", false, "Remove without asking first")
	UninstallCmd.Flags().Bool("dry-run", false, "List what would be removed and stop")
}
