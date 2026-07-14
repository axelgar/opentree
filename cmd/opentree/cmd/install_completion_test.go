package cmd

import (
	"path/filepath"
	"testing"
)

func TestZshCompletionDir(t *testing.T) {
	// oh-my-zsh present (via $ZSH pointing at an existing dir) → its completions
	// dir, which is already on fpath.
	omz := t.TempDir()
	t.Setenv("ZSH", omz)
	dir, ohMyZsh := zshCompletionDir()
	if !ohMyZsh || dir != filepath.Join(omz, "completions") {
		t.Errorf("with $ZSH set: got (%q, %v), want (%q, true)", dir, ohMyZsh, filepath.Join(omz, "completions"))
	}

	// No oh-my-zsh (empty $ZSH, no ~/.oh-my-zsh) → fallback to ~/.zsh/completions.
	home := t.TempDir()
	t.Setenv("ZSH", "")
	t.Setenv("HOME", home)
	dir, ohMyZsh = zshCompletionDir()
	if ohMyZsh || dir != filepath.Join(home, ".zsh", "completions") {
		t.Errorf("without oh-my-zsh: got (%q, %v), want (%q, false)", dir, ohMyZsh, filepath.Join(home, ".zsh", "completions"))
	}
}
