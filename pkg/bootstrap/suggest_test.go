package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

// The file that says "dev": "next dev" also says which package manager
// installs it, which is why the two halves are proposed together.
func TestSuggest_PackageJSON(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "package.json"), `{
	  "packageManager": "pnpm@9.1.0",
	  "scripts": {"build": "next build", "dev": "next dev"}
	}`)

	s, ok := Suggest(repo)
	if !ok {
		t.Fatal("Suggest found nothing in a package.json that says both")
	}
	if s.From != "package.json" {
		t.Errorf("From = %q", s.From)
	}
	if len(s.Setup) != 1 || s.Setup[0] != "pnpm install --frozen-lockfile" {
		t.Errorf("Setup = %v, want the reproducible pnpm install", s.Setup)
	}
	if s.Run != "pnpm run dev" {
		t.Errorf("Run = %q, want %q", s.Run, "pnpm run dev")
	}
}

// No packageManager field: the lockfile says the same thing.
func TestSuggest_PackageManagerFromLockfile(t *testing.T) {
	for _, tt := range []struct{ lockfile, install string }{
		{"yarn.lock", "yarn install --immutable"},
		{"bun.lockb", "bun install --frozen-lockfile"},
		{"package-lock.json", "npm ci"},
	} {
		repo := t.TempDir()
		write(t, filepath.Join(repo, "package.json"), `{"scripts": {"dev": "vite"}}`)
		write(t, filepath.Join(repo, tt.lockfile), "")

		s, ok := Suggest(repo)
		if !ok {
			t.Fatalf("%s: Suggest found nothing", tt.lockfile)
		}
		if len(s.Setup) != 1 || s.Setup[0] != tt.install {
			t.Errorf("%s: Setup = %v, want [%s]", tt.lockfile, s.Setup, tt.install)
		}
	}
}

// start is the fallback for a project without a dev script; a project with
// both means start for production and dev for a worktree.
func TestSuggest_FallsBackToStart(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "package.json"), `{"scripts": {"start": "node server.js"}}`)

	s, ok := Suggest(repo)
	if !ok {
		t.Fatal("Suggest found nothing")
	}
	if s.Run != "npm run start" {
		t.Errorf("Run = %q, want %q", s.Run, "npm run start")
	}
}

// Only the web process: the others are workers and schedulers, which are not
// what "open this branch in a browser" means.
func TestSuggest_Procfile(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "Procfile"), "worker: bundle exec sidekiq\nweb: bundle exec rails s -p $PORT\n")
	write(t, filepath.Join(repo, "Gemfile"), "source 'https://rubygems.org'\n")

	s, ok := Suggest(repo)
	if !ok {
		t.Fatal("Suggest found nothing in a Procfile with a web process")
	}
	if s.From != "Procfile" {
		t.Errorf("From = %q", s.From)
	}
	if s.Run != "bundle exec rails s -p $PORT" {
		t.Errorf("Run = %q, want the web process", s.Run)
	}
	if len(s.Setup) != 1 || s.Setup[0] != "bundle install" {
		t.Errorf("Setup = %v, want the Gemfile beside it to be read", s.Setup)
	}
}

// A guess that runs silently is a guess nobody checked, so nothing to go on is
// an answer rather than a default.
func TestSuggest_NothingToGoOn(t *testing.T) {
	repo := t.TempDir()
	// A Makefile deliberately does not count: `make dev` means something
	// different in every repository that has one.
	write(t, filepath.Join(repo, "Makefile"), "dev:\n\tgo run .\n")
	write(t, filepath.Join(repo, "Procfile"), "worker: ./worker\n")

	if s, ok := Suggest(repo); ok {
		t.Errorf("Suggest = %+v, want nothing", s)
	}
}

func TestSuggest_MalformedPackageJSONIsNotACrash(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, "package.json"), "{ not json")

	if _, ok := Suggest(repo); ok {
		t.Error("Suggest read something out of a broken package.json")
	}
	if _, err := os.Stat(filepath.Join(repo, "package.json")); err != nil {
		t.Fatal(err)
	}
}
