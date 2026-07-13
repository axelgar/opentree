package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func specs() []jsonHook {
	return []jsonHook{
		{event: "UserPromptSubmit", command: statusHookCommand("in_progress")},
		{event: "Notification", command: statusHookCommand("needs_input")},
	}
}

// hookCount returns how many command hooks are registered under hooks[event].
func hookCount(t *testing.T, path, event string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, data)
	}
	hooks, _ := m["hooks"].(map[string]any)
	arr, _ := hooks[event].([]any)
	return len(arr)
}

func TestInstallJSONHooks_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "settings.json")
	added, err := installJSONHooks(path, specs())
	if err != nil {
		t.Fatalf("installJSONHooks: %v", err)
	}
	if added != 2 {
		t.Fatalf("expected 2 hooks added, got %d", added)
	}
	if got := hookCount(t, path, "UserPromptSubmit"); got != 1 {
		t.Errorf("UserPromptSubmit: expected 1 hook, got %d", got)
	}
	if got := hookCount(t, path, "Notification"); got != 1 {
		t.Errorf("Notification: expected 1 hook, got %d", got)
	}
}

func TestInstallJSONHooks_PreservesExistingKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	seed := `{"model":"opus","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"echo hi"}]}]}}`
	if err := os.WriteFile(path, []byte(seed), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := installJSONHooks(path, specs()); err != nil {
		t.Fatalf("installJSONHooks: %v", err)
	}

	data, _ := os.ReadFile(path)
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "opus" {
		t.Errorf("existing 'model' key not preserved: %v", m["model"])
	}
	// The pre-existing echo hook must survive alongside the new opentree hook.
	if got := hookCount(t, path, "UserPromptSubmit"); got != 2 {
		t.Errorf("UserPromptSubmit: expected 2 hooks (existing + opentree), got %d", got)
	}
	if !strings.Contains(string(data), "echo hi") {
		t.Errorf("pre-existing hook was dropped:\n%s", data)
	}
}

func TestInstallJSONHooks_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := installJSONHooks(path, specs()); err != nil {
		t.Fatal(err)
	}
	added, err := installJSONHooks(path, specs())
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("second run should add 0 hooks, added %d", added)
	}
	if got := hookCount(t, path, "UserPromptSubmit"); got != 1 {
		t.Errorf("UserPromptSubmit: expected 1 hook after re-run, got %d", got)
	}
}

func TestInstallJSONHooks_BacksUpExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"model":"opus"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := installJSONHooks(path, specs()); err != nil {
		t.Fatal(err)
	}
	bak, err := os.ReadFile(path + ".opentree.bak")
	if err != nil {
		t.Fatalf("expected backup file: %v", err)
	}
	if string(bak) != `{"model":"opus"}` {
		t.Errorf("backup should hold pre-opentree content, got: %s", bak)
	}
}

func TestReadJSONObject_RejectsInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte("not json"), 0644)
	if _, err := readJSONObject(path); err == nil {
		t.Error("expected error on invalid JSON, got nil")
	}
}
