package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

var InstallCompletionCmd = &cobra.Command{
	Use:   "install-completion",
	Short: "Install shell completion for opentree",
	Long: `Install shell tab completion for opentree commands.

Detects your current shell and installs the completion script to the
appropriate location. Supports zsh, bash, and fish.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		shell := detectShell()

		switch shell {
		case "zsh":
			return installZshCompletion(cmd.Root())
		case "bash":
			return installBashCompletion(cmd.Root())
		case "fish":
			return installFishCompletion(cmd.Root())
		default:
			return fmt.Errorf("unsupported shell %q — run manually:\n  opentree completion <bash|zsh|fish>", shell)
		}
	},
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	return filepath.Base(shell)
}

// zshCompletionDir chooses where to write the zsh completion script. oh-my-zsh
// keeps $ZSH/completions on fpath, so installing there works with no .zshrc edit;
// otherwise we fall back to ~/.zsh/completions, which the user must add to fpath.
func zshCompletionDir() (dir string, ohMyZsh bool) {
	home := os.Getenv("HOME")
	omz := os.Getenv("ZSH")
	if omz == "" {
		omz = filepath.Join(home, ".oh-my-zsh")
	}
	if fi, err := os.Stat(omz); err == nil && fi.IsDir() {
		return filepath.Join(omz, "completions"), true
	}
	return filepath.Join(home, ".zsh", "completions"), false
}

func installZshCompletion(root *cobra.Command) error {
	dir, ohMyZsh := zshCompletionDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}

	dest := filepath.Join(dir, "_opentree")
	var buf bytes.Buffer
	if err := root.GenZshCompletion(&buf); err != nil {
		return fmt.Errorf("failed to generate completion: %w", err)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}

	fmt.Printf("✓ Installed zsh completion to %s\n\n", dest)

	if ohMyZsh {
		// $ZSH/completions is already on fpath; just rebuild the cache and reload.
		fmt.Println("oh-my-zsh detected — this directory is already on your fpath.")
		fmt.Println("Activate it now with:")
		fmt.Println("  rm -f ~/.zcompdump*; exec zsh")
		return nil
	}

	// Plain zsh: the directory must be on fpath before compinit runs.
	fmt.Println("Make sure your ~/.zshrc contains this BEFORE any compinit call:")
	fmt.Printf("  fpath=(%s $fpath)\n", dir)
	fmt.Println("  autoload -U compinit && compinit")

	zshrc := filepath.Join(os.Getenv("HOME"), ".zshrc")
	data, _ := os.ReadFile(zshrc)
	if !strings.Contains(string(data), dir) {
		fmt.Printf("\nTo add it automatically:\n  echo 'fpath=(%s $fpath)' >> ~/.zshrc\n", dir)
	}
	fmt.Println("\nThen reload your shell: rm -f ~/.zcompdump*; exec zsh")

	return nil
}

func installBashCompletion(root *cobra.Command) error {
	dir := filepath.Join(os.Getenv("HOME"), ".bash_completion.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}

	dest := filepath.Join(dir, "opentree")
	var buf bytes.Buffer
	if err := root.GenBashCompletionV2(&buf, true); err != nil {
		return fmt.Errorf("failed to generate completion: %w", err)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}

	fmt.Printf("✓ Installed bash completion to %s\n\n", dest)

	// Check if sourced in .bashrc
	bashrc := filepath.Join(os.Getenv("HOME"), ".bashrc")
	data, _ := os.ReadFile(bashrc)
	sourceLine := fmt.Sprintf("source %s", dest)
	if !strings.Contains(string(data), dest) {
		fmt.Println("Add to your ~/.bashrc to load it automatically:")
		fmt.Printf("  echo '%s' >> ~/.bashrc\n", sourceLine)
	}
	fmt.Println("\nThen restart your shell or run: source ~/.bashrc")
	return nil
}

func installFishCompletion(root *cobra.Command) error {
	dir := filepath.Join(os.Getenv("HOME"), ".config", "fish", "completions")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", dir, err)
	}

	dest := filepath.Join(dir, "opentree.fish")
	var buf bytes.Buffer
	if err := root.GenFishCompletion(&buf, true); err != nil {
		return fmt.Errorf("failed to generate completion: %w", err)
	}
	if err := os.WriteFile(dest, buf.Bytes(), 0600); err != nil {
		return fmt.Errorf("failed to write %s: %w", dest, err)
	}

	fmt.Printf("✓ Installed fish completion to %s\n", dest)
	fmt.Println("Restart your shell or start a new fish session to activate.")
	return nil
}
