package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Detection exists here and nowhere else: as something you ask for once, that
// prints a block for you to read and keep.
//
// It is not a fallback the tool reaches for when the config is empty. A guess
// that runs silently is a guess nobody checked, and the cost of getting it
// wrong — an install command that is not this project's, a server started from
// the wrong script — is paid every time a worktree is made. Read once and
// committed, it becomes the project's own answer.

// Suggestion is a proposed [workspace] block, and the file it was read from.
type Suggestion struct {
	Setup []string
	Run   string
	From  string
}

// Suggest reads what the project already says about how it is built and run.
//
// package.json first, because it answers both halves at once: the file that
// says "dev": "next dev" also says which package manager installs it, and a
// setup command from one file and a run command from another is a pairing
// nobody wrote down.
//
// Not Makefiles. `make dev` means something different in every repository that
// has one, and a suggestion that is wrong half the time is worse than none.
func Suggest(repoRoot string) (Suggestion, bool) {
	if s, ok := suggestFromPackageJSON(repoRoot); ok {
		return s, true
	}
	return suggestFromProcfile(repoRoot)
}

// packageJSON is the part of the file this reads.
type packageJSON struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

// lockfiles name a package manager as reliably as the packageManager field,
// for the projects that do not set one.
var lockfiles = []struct{ file, manager string }{
	{"pnpm-lock.yaml", "pnpm"},
	{"yarn.lock", "yarn"},
	{"bun.lockb", "bun"},
	{"package-lock.json", "npm"},
}

// installCommands are the reproducible install for each manager — the one that
// fails on a stale lockfile rather than quietly rewriting it, which is what a
// fresh worktree wants.
var installCommands = map[string]string{
	"pnpm": "pnpm install --frozen-lockfile",
	"yarn": "yarn install --immutable",
	"bun":  "bun install --frozen-lockfile",
	"npm":  "npm ci",
}

func suggestFromPackageJSON(repoRoot string) (Suggestion, bool) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "package.json")) // #nosec G304 -- the repository's own root
	if err != nil {
		return Suggestion{}, false
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Suggestion{}, false
	}

	manager := packageManager(repoRoot, pkg)
	s := Suggestion{From: "package.json"}
	if install, ok := installCommands[manager]; ok {
		s.Setup = []string{install}
	}
	// dev first, then start: a repository with both means the second for
	// production and the first for a worktree.
	for _, script := range []string{"dev", "start"} {
		if _, ok := pkg.Scripts[script]; ok {
			s.Run = manager + " run " + script
			break
		}
	}
	return s, len(s.Setup) > 0 || s.Run != ""
}

// packageManager is what the project installs with: what it declared, what its
// lockfile implies, or npm.
func packageManager(repoRoot string, pkg packageJSON) string {
	// "pnpm@9.1.0" — corepack's form, where the version is not our business.
	if name, _, _ := strings.Cut(pkg.PackageManager, "@"); name != "" {
		return name
	}
	for _, l := range lockfiles {
		if _, err := os.Stat(filepath.Join(repoRoot, l.file)); err == nil {
			return l.manager
		}
	}
	return "npm"
}

// suggestFromProcfile reads the other file that already says how to start this
// project. Only the web process: the others are workers and schedulers, which
// are not what "open this branch in a browser" means.
func suggestFromProcfile(repoRoot string) (Suggestion, bool) {
	data, err := os.ReadFile(filepath.Join(repoRoot, "Procfile")) // #nosec G304 -- the repository's own root
	if err != nil {
		return Suggestion{}, false
	}

	s := Suggestion{From: "Procfile"}
	for _, line := range strings.Split(string(data), "\n") {
		name, command, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) != "web" {
			continue
		}
		s.Run = strings.TrimSpace(command)
		break
	}
	if s.Run == "" {
		return Suggestion{}, false
	}
	// A Procfile says how to run, never how to install. The Gemfile beside it
	// does, and it is the only one this pairs with — anything more would be
	// guessing at a stack from a filename.
	if _, err := os.Stat(filepath.Join(repoRoot, "Gemfile")); err == nil {
		s.Setup = []string{"bundle install"}
	}
	return s, true
}
