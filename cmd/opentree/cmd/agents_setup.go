package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/axelgar/opentree/pkg/config"
)

// statusFileEnv is the environment variable opentree exports into every agent
// shell; the installed hooks write their status JSON to the path it holds and
// no-op when it is unset (i.e. outside opentree).
const statusFileEnv = "OPENTREE_STATUS_FILE"

var agentsSetupCmd = &cobra.Command{
	Use:   "setup <name>",
	Short: "Install status-reporting hooks for a coding agent",
	Long: `Install the hooks that let an agent report its status to opentree.

The agent writes .opentree-status.json when it starts working and when it is
waiting for your input; opentree shows that as a badge in the workspace list.
Hooks are installed once at the user level and guarded so they only fire inside
opentree-launched sessions.`,
	Args: cobra.ExactArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var completions []string
		for _, a := range config.PredefinedAgents {
			completions = append(completions, fmt.Sprintf("%s\t%s", a.Name, a.Description))
		}
		return completions, cobra.ShellCompDirectiveNoFileComp
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		agent := config.FindAgent(args[0])
		if agent == nil {
			fmt.Printf("Unknown agent %q. Available agents:\n", args[0])
			for _, a := range config.PredefinedAgents {
				fmt.Printf("  - %s (%s)\n", a.Name, a.Command)
			}
			return fmt.Errorf("agent %q not found", args[0])
		}

		inst, ok := hookInstallers[agent.Command]
		if !ok {
			return fmt.Errorf("no status-hook setup is available for %q yet", agent.Name)
		}

		if inst.install != nil {
			if err := inst.install(); err != nil {
				return err
			}
		} else {
			fmt.Print(inst.manual)
		}

		fmt.Printf("\nopentree exports %s into each agent shell automatically, so these\n", statusFileEnv)
		fmt.Println("hooks activate on your next workspace launch (and stay inert elsewhere).")
		return nil
	},
}

// hookInstaller either auto-installs an agent's status hooks (install != nil)
// or prints manual instructions (manual, when auto-install isn't supported).
type hookInstaller struct {
	install func() error
	manual  string
}

var hookInstallers = map[string]hookInstaller{
	"claude":   {install: installClaudeHooks},
	"codex":    {install: installCodexHooks},
	"gemini":   {install: installGeminiHooks},
	"opencode": {install: installOpenCodeHooks},
	"gh":       {manual: copilotManual}, // Copilot: no waiting-for-input event
	"pi":       {manual: piManual},      // Pi: single-slot notify.json we won't clobber
}

// statusHookCommand builds the guarded shell command that writes a status value
// to $OPENTREE_STATUS_FILE. The command is a no-op when the variable is unset.
func statusHookCommand(status string) string {
	return fmt.Sprintf(`[ -n "$%s" ] && printf '{"status":"%s"}' > "$%s"`, statusFileEnv, status, statusFileEnv)
}

// Each agent maps two events to in_progress (a new prompt started) and one or
// more to needs_input (a permission prompt, a notification, or the turn ending
// so it's the user's move again). Turn-end → needs_input closes the gap where a
// finished turn would otherwise still read "working..." until an idle timeout.
var (
	working  = statusHookCommand("in_progress")
	needsYou = statusHookCommand("needs_input")
)

// ---------------------------------------------------------------------------
// Claude Code — ~/.claude/settings.json (JSON hooks, run through a shell)
// ---------------------------------------------------------------------------

func installClaudeHooks() error {
	path, err := homePath(".claude", "settings.json")
	if err != nil {
		return err
	}
	return installAndReport("Claude Code", path, []jsonHook{
		{event: "UserPromptSubmit", command: working},
		{event: "Notification", command: needsYou},
		{event: "Stop", command: needsYou},
	})
}

// ---------------------------------------------------------------------------
// Codex — ~/.codex/hooks.json (same JSON hook shape; additive across layers)
// ---------------------------------------------------------------------------

func installCodexHooks() error {
	path, err := homePath(".codex", "hooks.json")
	if err != nil {
		return err
	}
	return installAndReport("Codex", path, []jsonHook{
		{event: "UserPromptSubmit", command: working},
		{event: "PermissionRequest", command: needsYou},
		{event: "Stop", command: needsYou},
	})
}

// ---------------------------------------------------------------------------
// Gemini CLI — ~/.gemini/settings.json (same JSON hook shape)
// ---------------------------------------------------------------------------

func installGeminiHooks() error {
	path, err := homePath(".gemini", "settings.json")
	if err != nil {
		return err
	}
	return installAndReport("Gemini CLI", path, []jsonHook{
		{event: "BeforeAgent", command: working},
		{event: "Notification", command: needsYou},
		{event: "AfterAgent", command: needsYou},
	})
}

// ---------------------------------------------------------------------------
// OpenCode — ~/.config/opencode/plugin/opentree-status.js (JS/TS plugin)
// ---------------------------------------------------------------------------

func installOpenCodeHooks() error {
	path, err := homePath(".config", "opencode", "plugin", "opentree-status.js")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(openCodePlugin), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	fmt.Printf("✓ Installed OpenCode status plugin at %s\n", path)
	return nil
}

// openCodePlugin listens on the OpenCode plugin event bus and writes the status
// file. It reads OPENTREE_STATUS_FILE from the process env opentree exports and
// no-ops when unset. Direct fs write avoids shell-quoting pitfalls.
const openCodePlugin = `import { writeFileSync } from "node:fs"

export const OpentreeStatus = async () => {
  const write = (status) => {
    const f = process.env.OPENTREE_STATUS_FILE
    if (!f) return
    try {
      writeFileSync(f, JSON.stringify({ status }))
    } catch {}
  }
  return {
    event: async ({ event }) => {
      if (event.type === "session.idle") write("needs_input")
      if (event.type === "session.status" && event.properties?.status?.type === "busy") write("in_progress")
    },
    "permission.ask": async () => {
      write("needs_input")
    },
  }
}
`

func homePath(parts ...string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, parts...)...), nil
}

func installAndReport(name, path string, specs []jsonHook) error {
	added, err := installJSONHooks(path, specs)
	if err != nil {
		return fmt.Errorf("failed to update %s: %w", path, err)
	}
	printInstallResult(name, path, added)
	return nil
}

// ---------------------------------------------------------------------------
// Shared JSON hook merge (Claude Code; reusable for other JSON-hook agents)
// ---------------------------------------------------------------------------

type jsonHook struct {
	event   string
	command string
}

// installJSONHooks merges the given hooks into a `{ "hooks": { <event>: [...] } }`
// style JSON config, preserving all existing keys. It is idempotent: a hook
// whose command already contains the OPENTREE_STATUS_FILE marker is not added
// again. Returns the number of hooks actually added.
func installJSONHooks(path string, specs []jsonHook) (int, error) {
	m, err := readJSONObject(path)
	if err != nil {
		return 0, err
	}

	added := 0
	for _, sp := range specs {
		if addJSONHook(m, sp.event, sp.command) {
			added++
		}
	}

	if added > 0 {
		if err := backupOnce(path); err != nil {
			return added, err
		}
		if err := writeJSONObject(path, m); err != nil {
			return added, err
		}
	}
	return added, nil
}

// addJSONHook appends a command hook under hooks[event] unless an opentree hook
// is already present there. Returns true if it added one.
func addJSONHook(m map[string]any, event, command string) bool {
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		m["hooks"] = hooks
	}
	arr, _ := hooks[event].([]any)

	for _, entry := range arr {
		e, _ := entry.(map[string]any)
		inner, _ := e["hooks"].([]any)
		for _, h := range inner {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, statusFileEnv) {
				return false // already installed
			}
		}
	}

	arr = append(arr, map[string]any{
		"hooks": []any{
			map[string]any{"type": "command", "command": command},
		},
	})
	hooks[event] = arr
	return true
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("existing config is not valid JSON: %w", err)
	}
	return m, nil
}

func writeJSONObject(path string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep && and > readable in the shell commands
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// backupOnce copies path to path+".opentree.bak" the first time we touch it, so
// there's always a pre-opentree snapshot to fall back to. No-op if the file
// doesn't exist yet or a backup already exists.
func backupOnce(path string) error {
	bak := path + ".opentree.bak"
	if _, err := os.Stat(bak); err == nil {
		return nil // backup already exists
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // nothing to back up
	}
	if err != nil {
		return err
	}
	return os.WriteFile(bak, data, 0644)
}

func printInstallResult(name, path string, added int) {
	if added > 0 {
		fmt.Printf("✓ Installed %s status hooks in %s\n", name, path)
	} else {
		fmt.Printf("✓ %s status hooks already present in %s (no change)\n", name, path)
	}
}

// ---------------------------------------------------------------------------
// Manual-only agents
// ---------------------------------------------------------------------------

const copilotManual = `GitHub Copilot CLI does not expose a "waiting for input" hook event, so opentree
cannot drive the "needs input" badge for it automatically.

You can still signal "working" from a session-start hook — add to
~/.copilot/hooks/opentree.json:
  {
    "version": 1,
    "hooks": {
      "userPromptSubmitted": [
        { "command": "sh -c '[ -n \"$OPENTREE_STATUS_FILE\" ] && printf '\\''{\"status\":\"in_progress\"}'\\'' > \"$OPENTREE_STATUS_FILE\"'" }
      ]
    }
  }
`

const piManual = `Pi reports via a single-script notification config that opentree will not
overwrite. Edit ~/.pi/agent/pi-lab/notify.json and point its "script" at a
wrapper that writes $OPENTREE_STATUS_FILE, e.g. a script containing:
  [ -n "$OPENTREE_STATUS_FILE" ] && printf '{"status":"needs_input"}' > "$OPENTREE_STATUS_FILE"
Pi only fires this on permission requests / turn end, so it drives "needs input"
but not "working".
`
